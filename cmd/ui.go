// ui.go 实现 "ggt ui"：把全部仓库的状态以本机 WebUI 的形式呈现。
//
// 为什么需要它，而不是继续增强终端输出：
//   - 终端只能单列纵向排列，仓库一多就得滚动很久才能找到目标；页面可以利用屏幕宽度横向铺开，
//     屏幕越宽，一屏内能同时呈现的仓库与变更明细越多
//   - 终端难以在一屏内同时说清"哪些仓库有变更、每个仓库改了哪些文件"，而这两件事恰恰是
//     多仓库日常最需要一眼看清的
//
// 分工：监听、端口顺延、Host/Origin/token 三道护栏、静态资源托管、拉起浏览器全部由
// eggokit/webui 提供（本文件不重复实现）；这里只负责命令行参数、业务 API（/api/repos）、
// 采集与排序，以及启动摘要的打印
//
// 页面资产的迭代方式：前端是原生 HTML/CSS/JS，经 go:embed 打进二进制，没有构建步骤。
// 因此改完前端必须重新 `go build` 才会生效（刷新浏览器不会），这是与项目其余部分一致的
// 单一 Go 构建路径所换来的代价
package cmd

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/eggokit/logger"
	"github.com/jy-eggroll/eggokit/webui"
	"github.com/jy-eggroll/ggt/internal/git"
	"github.com/jy-eggroll/ggt/internal/worker"
	"github.com/spf13/cobra"
)

// uiAssets 嵌入页面资产目录。
//
// 用 all: 前缀是因为默认的 go:embed 会跳过以 . 或 _ 开头的文件；资产目录将来若要放
// 例如 .gitkeep 或下划线开头的文件，没有 all: 会被静默漏掉，而漏掉的表现是页面 404，
// 排查时很难想到是嵌入规则而不是路径写错
//
// 资产目录里只有前端文件，没有任何 .go 源文件，因此这个嵌入不会把服务端代码带进二进制
//
//go:embed all:ui
var uiAssets embed.FS

// 排序分组。数值即优先级，越小越靠前。
//
// 分组的依据是用户明确提出的排序诉求：有待提交的变更最靠前，其次是"提交了但还没推"的仓库，
// 干净的仓库排在最后。同级之内再按仓库名升序，保证同一份数据每次渲染的顺序完全一致——
// 否则页面每次轮询都会重新排一次，卡片位置会无规律跳动
const (
	uiGroupChanged   = 0 // 有未提交的文件变更
	uiGroupAheadOnly = 1 // 工作区干净，但有未推送的提交
	uiGroupClean     = 2 // 无待办
	uiGroupFailed    = 3 // 状态采集失败（git 报错），排最后但仍展示，避免它被静默吞掉
)

// uiCacheTTL 是状态采集结果的复用窗口。
//
// 为什么要缓存：页面按固定间隔轮询 /api/repos，而每次采集都要为每个仓库起一个 git 进程。
// 几十个仓库的重复采集既浪费 CPU，也会让 git 频繁访问磁盘。窗口取 2 秒是为了小于页面的
// 轮询间隔，这样正常轮询总能拿到新数据，而"多个标签页同时刷新"这类并发只算一次
const uiCacheTTL = 2 * time.Second

// uiFile 是页面消费的单个变更文件。
//
// 字段一律 camelCase：这份结构只用于 JSON 传输，消费方是 JavaScript，
// 与 Go 侧习惯无关。配置文件的 snake_case 约定不适用于 API 载荷
type uiFile struct {
	// Index 与 Work 是 porcelain 的 XY 两个状态位，页面据此显示"已暂存/未暂存"
	Index string `json:"index"`
	Work  string `json:"work"`
	// Path 是相对仓库根的路径；重命名时为新路径
	Path string `json:"path"`
	// OrigPath 仅在重命名/复制时出现，为旧路径
	OrigPath string `json:"origPath,omitempty"`
	// Untracked 为未跟踪文件；Unmerged 为存在冲突的未合并条目
	Untracked bool `json:"untracked"`
	Unmerged  bool `json:"unmerged"`
}

// uiRepo 是页面消费的单个仓库快照。
type uiRepo struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	IsSubmodule bool   `json:"isSubmodule"`
	// Branch 为空且 Detached 为真表示游离 HEAD；二者都空且 NoCommits 为真表示仓库尚无提交
	Branch    string `json:"branch"`
	Upstream  string `json:"upstream"`
	Detached  bool   `json:"detached"`
	NoCommits bool   `json:"noCommits"`
	Ahead     int    `json:"ahead"`
	Behind    int    `json:"behind"`
	// Files 始终是数组而非 null：页面遍历时不需要再判空
	Files []uiFile `json:"files"`
	// Error 非空表示这次采集失败，页面显示为异常状态而不是"干净"
	Error string `json:"error,omitempty"`
	// Group 是排序分组，由服务端算好，页面只按数组顺序渲染，不再自己排序——
	// 排序规则只有一处实现，避免前后端各有一套而漂移
	Group int `json:"group"`
}

// uiPayload 是 /api/repos 的响应体。
type uiPayload struct {
	Repos []uiRepo `json:"repos"`
	// GeneratedAt 与 DurationMs 是给页面显示"这份数据多旧、采一次要多久"的，
	// 用户据此判断页面是否卡住，不必靠猜
	GeneratedAt time.Time `json:"generatedAt"`
	DurationMs  int64     `json:"durationMs"`
}

// uiCache 缓存一次全量采集的结果。
//
// 用互斥锁而不是"读时无锁、过期再锁"：后者在缓存刚过期时会让多个并发请求同时开始采集，
// 而每次采集都要为每个仓库起 git 进程，代价很高。这里让并发的后来者直接排队等在前一次采集
// 之后，等同于把重复采集合并成一次
type uiCache struct {
	mu   sync.Mutex
	at   time.Time
	data *uiPayload
	// ctx 是服务级上下文，刻意不使用 HTTP 请求的 ctx：采集要跨请求复用，
	// 若挂在某个请求上，用户刷新页面（旧请求被取消）就会把这次采集一并中断，
	// 于是缓存永远填不上，每次都从零开始
	ctx context.Context
}

// repos 返回一份仓库状态快照，必要时重新采集。
func (c *uiCache) repos() *uiPayload {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.data != nil && time.Since(c.at) < uiCacheTTL {
		return c.data
	}
	c.data = collectUIPayload(c.ctx)
	c.at = time.Now()
	return c.data
}

// collectUIPayload 并发采集全部仓库的状态并排好序。
//
// 入口用 ExpandRepos(ctx, GetRepoList()) 而不是 AllRepos：后者在仓库列表为空时会直接
// os.Exit(0)（那是命令行的合理行为——"没有活可干"就正常退出），但对常驻的 WebUI 服务
// 等于进程自杀。空列表在这里是正常状态，应当由页面显示"尚未配置仓库"
func collectUIPayload(ctx context.Context) *uiPayload {
	started := time.Now()
	entries := ExpandRepos(ctx, GetRepoList())

	repos := worker.Map(ctx, entries, Concurrency(), func(ctx context.Context, e RepoEntry) uiRepo {
		r := uiRepo{
			Name:        e.Name,
			Path:        e.Path,
			IsSubmodule: e.IsSubmodule,
			Files:       []uiFile{},
		}

		st, err := git.RunStatus(ctx, e.Path)
		if err != nil {
			// 采集失败不能当成"干净"：那会让一个权限错误或损坏的仓库看起来毫无问题。
			// 单独的失败分组让它排到最后但依然可见
			r.Error = err.Error()
			r.Group = uiGroupFailed
			return r
		}

		r.Branch = st.Branch
		r.Upstream = st.Upstream
		r.Detached = st.Detached
		r.NoCommits = st.NoCommits
		r.Ahead = st.Ahead
		r.Behind = st.Behind
		r.Files = make([]uiFile, 0, len(st.Files))
		for _, f := range st.Files {
			r.Files = append(r.Files, uiFile{
				Index:     f.Index,
				Work:      f.Work,
				Path:      f.Path,
				OrigPath:  f.OrigPath,
				Untracked: f.Untracked,
				Unmerged:  f.Unmerged,
			})
		}

		switch {
		case len(st.Files) > 0:
			r.Group = uiGroupChanged
		case st.Ahead > 0:
			// 没有文件变更但有未推送的提交——用户要的"提交了没推"也属于待办
			r.Group = uiGroupAheadOnly
		default:
			r.Group = uiGroupClean
		}
		return r
	})

	// 排序：先按分组，再按仓库名。SliceStable 保留采集顺序作为最终兜底，
	// 因此即便两个仓库同名（不同父目录下的同名目录），顺序也仍然稳定
	sort.SliceStable(repos, func(i, j int) bool {
		if repos[i].Group != repos[j].Group {
			return repos[i].Group < repos[j].Group
		}
		return repos[i].Name < repos[j].Name
	})

	if repos == nil {
		repos = []uiRepo{}
	}
	return &uiPayload{
		Repos:       repos,
		GeneratedAt: time.Now(),
		DurationMs:  time.Since(started).Milliseconds(),
	}
}

// handleRepos 是 /api/repos 的处理函数。
func (c *uiCache) handleRepos(w http.ResponseWriter, _ *http.Request) {
	payload := c.repos()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// 状态是实时数据，任何一层缓存都不该留下副本：这条响应本身就是"当前真相"的快照，
	// 被缓存后页面会一直看到过期的变更列表，而它恰恰是用来替代手动刷新的
	w.Header().Set("Cache-Control", "no-store")

	// 文件路径里可能含 < > &："把 HTML 转义关掉，否则中文与符号路径会被写成 \u003c 这类转义，
	// 前端虽然能解析，但排查接口时看到的 JSON 会难以阅读
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		// 响应头已发出，无法再改状态码，只能记录日志
		logger.Error(l10n.T("Failed to encode the repository status response", nil), "error", err)
	}
}

// newUICmd 构造 "ggt ui" 命令。
func newUICmd() *cobra.Command {
	var (
		port       int
		host       string
		noOpen     bool
		allowHosts []string
	)

	c := &cobra.Command{
		Use:   "ui",
		Short: l10n.T("Open a local web dashboard for every repository", nil),
		Long: l10n.T(`Open a local web dashboard showing the status of every repository.

Repositories are laid out in multiple columns: each column is filled to the height of the
window, and a repository with many changed files continues into the next column. Scroll
horizontally to reach the columns that follow.

Examples:
  ggt ui                 Open the dashboard and launch the browser
  ggt ui --no-open       Print the address only
  ggt ui --port 8721     Bind a fixed port`, nil),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUI(cmd, port, host, noOpen, allowHosts)
		},
	}

	// 默认端口 0 表示交给系统挑一个空闲端口：WebUI 是随手打开的辅助界面，地址由 webui
	// 自动打开或打印出来，用户并不需要记住它；固定端口反而会让"同时开两个实例"直接失败
	c.Flags().IntVar(&port, "port", 0,
		l10n.T("Port to listen on (0 picks a free port; if the port is taken it is advanced up to 100 times)", nil))
	c.Flags().StringVar(&host, "host", "",
		l10n.T("Address to bind (defaults to 127.0.0.1; setting 0.0.0.0 exposes the page to the local network)", nil))
	c.Flags().BoolVar(&noOpen, "no-open", false,
		l10n.T("Do not open the browser automatically", nil))
	c.Flags().StringSliceVar(&allowHosts, "allow-host", nil,
		l10n.T("Additional host name to accept in the Host header (repeatable)", nil))

	return c
}

// runUI 组装并启动 WebUI 服务，阻塞到服务结束。
func runUI(cmd *cobra.Command, port int, host string, noOpen bool, allowHosts []string) error {
	// 独立于任何请求的上下文：采集要跨请求复用，见 uiCache.ctx 的说明
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 首页字节先读出来，交给下面每次请求现渲染：语言可以在运行期切换，
	// 若把渲染结果烧死在启动时，切换语言后刷新页面拿到的还是旧语言
	indexHTML, err := fs.ReadFile(uiAssets, "ui/index.html")
	if err != nil {
		return fmt.Errorf("%s: %w", l10n.T("Failed to load the WebUI assets", nil), err)
	}
	assets, err := fs.Sub(uiAssets, "ui")
	if err != nil {
		return fmt.Errorf("%s: %w", l10n.T("Failed to load the WebUI assets", nil), err)
	}

	cache := &uiCache{ctx: ctx}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/repos", cache.handleRepos)

	// 页面自己不会说"当前语言是哪个"，由 Go 端把语言写进两个占位符：
	//   - __GGT_LANG_VALUE__ 供页面内翻译表选语言
	//   - __GGT_HTML_LANG__ 供浏览器挑字体、断词与朗读规则（写成 zh-CN 时英文界面
	//     在无障碍工具里会被按中文朗读）
	// 前端文案不经过 Go 的 l10n 提取管线（那条管线只扫 .go），所以页面自带一份翻译表，
	// 后端不需要知道页面里有哪些文案
	//
	// 用 ReplaceAll 而不是 Replace(…, 1)：后者只替换第一处，一旦页面注释里出现占位符字面量，
	// 被替换的就是注释、真正的使用处原样留下，语言会静默停在默认值上——实际踩过一次，
	// 表现为界面文案全是英文而所有数据正常，很难联想到是注释把占位符"吃掉"了
	renderIndex := func() []byte {
		lang := l10n.Current()
		out := bytes.ReplaceAll(indexHTML, []byte("__GGT_HTML_LANG__"), []byte(lang))
		return bytes.ReplaceAll(out, []byte("__GGT_LANG_VALUE__"), []byte(lang))
	}

	srv, err := webui.New(webui.Config{
		Host: host,
		// 端口顺延：指定端口被占用时依次 +1 重试。重启服务时上一个进程的 socket 可能还在
		// TIME_WAIT，直接监听同一个端口会失败，而用户只是重启了一次
		Port:      webui.Sequential(port, 100),
		Assets:    assets,
		IndexName: "index.html",
		Index:     renderIndex,
		API:       mux,
		// 只传额外授权；绑定地址由 webui 自动纳入白名单
		AllowHosts:  allowHosts,
		OpenBrowser: !noOpen,
		// 页面能读到本机全部仓库的路径与变更，本机其它进程不该能直接访问，因此开启 token 门禁
		Auth: webui.Auth{Enabled: true},
	})
	if err != nil {
		return err
	}

	// 绑定到非回环地址意味着同网段任何人都能打开这个页面，必须明确警告；
	//「什么算回环」与警告文案都由 webui 给出，避免两处判断各说各话
	if warning := srv.NonLoopbackWarning(); warning != "" {
		WarnMsg(warning)
	}

	// 日志与用户可见摘要分开：日志受 log_level 过滤（默认 warn），而服务地址属于
	//「必须默认可见」的信息——用户要凭它打开页面，因此直接写标准输出
	logger.Info(l10n.T("Starting WebUI", nil), "addr", srv.Addr())
	fmt.Fprintln(cmd.OutOrStdout(), srv.Summary())

	// 拉起浏览器与阻塞都由 webui 负责；OpenBrowser 打开的是带 token 的完整地址
	return srv.Serve()
}

func init() {
	register(func(root *cobra.Command) { root.AddCommand(newUICmd()) })
}
