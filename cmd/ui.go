// ui.go 实现 "ggt ui"：把全部仓库的状态以本机 WebUI 的形式呈现。
//
// 为什么需要它，而不是继续增强终端输出：
//   - 终端只能单列纵向排列，仓库一多就得滚动很久才能找到目标；页面可以利用屏幕宽度横向铺开，
//     屏幕越宽，一屏内能同时呈现的仓库与变更明细越多
//   - 终端难以在一屏内同时说清“哪些仓库有变更、每个仓库改了哪些文件”，而这两件事恰恰是
//     多仓库日常最需要一眼看清的
//
// 分工：监听、端口顺延、Host/Origin/token 三项检查、静态资源托管、拉起浏览器全部由
// eggokit/webui 提供（本文件不重复实现）；这里只负责命令行参数、业务 API（/api/repos、
// /api/diff）、采集与排序，以及启动摘要的打印
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
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/eggokit/logger"
	"github.com/jy-eggroll/eggokit/webui"
	"github.com/jy-eggroll/ggt/internal/config"
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
// 分组的依据是用户明确提出的排序诉求：有待提交的变更最靠前，其次是“提交了但还没推”的仓库，
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
// 轮询间隔，这样正常轮询总能拿到新数据，而“多个标签页同时刷新”这类并发只算一次
const uiCacheTTL = 2 * time.Second

// uiFile 是页面使用的单个变更文件。
//
// 字段一律 camelCase：这份结构只用于 JSON 传输，使用方是 JavaScript，
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

// uiRepo 是页面使用的单个仓库快照。
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
	// Error 非空表示这次采集失败，页面显示为异常状态而不是“干净”
	Error string `json:"error,omitempty"`
	// Group 是排序分组，由服务端算好，页面只按数组顺序渲染，不再自己排序——
	// 排序规则只有一处实现，避免前后端各有一套而漂移
	Group int `json:"group"`
}

// uiPayload 是 /api/repos 的响应体。
type uiPayload struct {
	Repos []uiRepo `json:"repos"`
	// GeneratedAt 与 DurationMs 是给页面显示“这份数据多旧、采一次要多久”的，
	// 用户据此判断页面是否卡住，不必靠猜
	GeneratedAt time.Time `json:"generatedAt"`
	DurationMs  int64     `json:"durationMs"`
}

// uiCache 缓存一次全量采集的结果。
//
// 用互斥锁而不是“读时无锁、过期再锁”：后者在缓存刚过期时会让多个并发请求同时开始采集，
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

// invalidate 丢弃当前快照，让下一次读取重新采集。
//
// 写操作之后必须调用：缓存窗口是 2 秒，不清掉的话页面紧接着刷新拿到的仍是写之前的状态，
// 表现为“点了没反应”、两秒后才突然变化——这种迟一拍的反馈比慢更让人困惑
func (c *uiCache) invalidate() {
	c.mu.Lock()
	c.data = nil
	c.mu.Unlock()
}

// collectUIPayload 并发采集全部仓库的状态并排好序。
//
// 入口用 ExpandRepos(ctx, GetRepoList()) 而不是 AllRepos：后者在仓库列表为空时会直接
// os.Exit(0)（那是命令行的合理行为——“没有活可干”就正常退出），但对常驻的 WebUI 服务
// 等于进程自杀。空列表在这里是正常状态，应当由页面显示“尚未配置仓库”
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
			// 采集失败不能当成“干净”：那会让一个权限错误或损坏的仓库看起来毫无问题。
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
			// 没有文件变更但有未推送的提交——用户要的“提交了没推”也属于待办
			r.Group = uiGroupAheadOnly
		default:
			r.Group = uiGroupClean
		}
		return r
	})

	// 排序：先按分组，再按仓库名。SliceStable 让顺序相同的仓库保持采集顺序，
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
	// 状态是实时数据，任何一层缓存都不该留下副本：这条响应本身就是“当前真相”的快照，
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

// uiDiffLimit 是单次 diff 回给页面的字节上限。
//
// 为什么要截断：锁文件、压缩产物这类自动生成的大文件，一次 diff 可能有几十 MB，
// 而看板的用途只是“看一眼改了什么”。把整份写进 JSON 会让浏览器解析与排版一起卡住，
// 而堆内存也白花。截断处落在行边界上，页面会明确标注“输出已截断”，
// 不会让人误以为改动只有这些
const uiDiffLimit = 2 << 20

// uiUntrackedLimit 是未跟踪文件正文送入页面的字节上限（理由同 uiDiffLimit）
const uiUntrackedLimit = 1 << 20

// uiBinarySniffLen 是判断“是不是二进制”时嗅探的前缀长度。
// 与 git 自身的规则一致：只看前 8000 字节里有没有 NUL，不读全文
const uiBinarySniffLen = 8000

// uiDiff 是 /api/diff 的响应体。
//
// 正文是一串“段”而不是固定的两三个字段：段是这类视图唯一的组织方式，而视图只会越加越多
// （已暂存、未暂存、某条提交……）。每加一种视图就多一对字段、页面多一个 if，迟早没人清得干净
type uiDiff struct {
	Repo string `json:"repo"`
	// File 为空表示“整个仓库”，即用户点的是仓库标题行
	File     string `json:"file,omitempty"`
	OrigPath string `json:"origPath,omitempty"`
	// Commit 非空表示这是“某条提交改了什么”，值是那条提交的哈希。此时正文只有一段，
	// 内容是相对第一个父提交的改动（理由见 commitDiffText）
	Commit string `json:"commit,omitempty"`
	// Untracked 为真时正文是文件正文而不是 diff：未跟踪文件不在 index 里，
	// git 对它不产生 diff（替代写法 git diff --no-index /dev/null 在 Windows 上不成立，
	// 那边没有 /dev/null）。页面把它整体按“新增”渲染
	Untracked bool `json:"untracked"`
	Unmerged  bool `json:"unmerged"`
	// Binary 只用于未跟踪文件：它是二进制时不返回正文（返回了也是乱码），
	// 页面据此显示提示。已跟踪文件的二进制改动由 git 自己在 diff 里写成
	// "Binary files ... differ"，页面认那一行，后端不必再判一次
	Binary    bool            `json:"binary"`
	Truncated bool            `json:"truncated"`
	Sections  []uiDiffSection `json:"sections"`
	Error     string          `json:"error,omitempty"`
}

// uiDiffSection 是 diff 视图里的一段正文。
type uiDiffSection struct {
	// Kind 决定页面用哪条文案当段标题
	Kind string `json:"kind"`
	// Text 是不带颜色的统一 diff（未跟踪文件则是文件正文，整份按新增渲染）
	Text string `json:"text"`
	// Files 与 Text 里各分段的顺序一致，整仓视图才有。页面据此把正文切成“每文件一段”
	// 并标出名字与增删行数——从文本里反解路径要重新处理引号、转义与改名，
	// 而 git 已经用机器可读的方式给了一份（形状沿用 git.CommitFile，提交卡的文件行也是它）
	Files []git.CommitFile `json:"files,omitempty"`
}

// 段的种类。字符串而不是数字：它要进 JSON，出问题时一眼看得出是哪一段
const (
	diffKindStaged   = "staged"
	diffKindUnstaged = "unstaged"
	diffKindCommit   = "commit"
)

// handleDiff 是 /api/diff 的处理函数。
//
// 仓库与文件都只从“已有快照里实际存在的条目”里取，而不是直接采信请求里的路径：
// 页面传来的 <repo, file> 必须能在上一次 /api/repos 的结果里找到，否则一律 404。
// 这样即便有人手工构造请求（token 已在 Host/Origin/token 三项检查之内，但通过检查不等于
// 授权任意路径），也读不到配置之外的仓库、更读不到仓库之外的文件
func (c *uiCache) handleDiff(w http.ResponseWriter, r *http.Request) {
	repoPath := r.URL.Query().Get("repo")
	filePath := r.URL.Query().Get("file")

	repo, ok := findUIRepo(c.repos(), repoPath)
	if !ok {
		writeUIDiffError(w, http.StatusNotFound, l10n.T("Unknown repository", nil))
		return
	}

	// 提交视图问的是“这条提交改了什么”，与当前工作区快照无关，因此单独走一条路径：
	// 它的合法输入来自这条提交自己的改动清单，而不是上一次快照
	if hash := strings.TrimSpace(r.URL.Query().Get("commit")); hash != "" {
		handleCommitDiff(w, r, repo, hash, filePath)
		return
	}

	out := uiDiff{Repo: repo.Path, File: filePath}

	// file 为 nil 表示“整个仓库”。指针而不是零值：需要区分“没有这个文件”与“没传文件”
	var file *uiFile
	var paths []string
	if filePath != "" {
		f, err := lookupUIFile(repo, filePath)
		if err != nil {
			writeUIDiffError(w, statusOf(err, http.StatusInternalServerError), err.Error())
			return
		}
		file = &f
		out.File = f.Path
		out.OrigPath = f.OrigPath
		out.Untracked = f.Untracked
		out.Unmerged = f.Unmerged
		// 重命名要同时给新旧两个路径，理由见 uiAffectedPaths
		paths = uiAffectedPaths(f.Path, f.OrigPath)
	}

	ctx := r.Context()
	if out.Untracked {
		text, binary, truncated, err := readUntrackedFile(repo.Path, out.File)
		if err != nil {
			writeUIDiffError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// 未跟踪文件放在“未暂存”那一段的位置上：它确实是还没进 index 的改动，
		// 这也是它一直以来的归属，页面因此不必为它单开一种段
		out.Binary, out.Truncated = binary, truncated
		if text != "" {
			out.Sections = []uiDiffSection{{Kind: diffKindUnstaged, Text: text}}
		}
		writeUIDiff(w, out)
		return
	}

	// 按状态位跳过必然为空的那一侧：一个文件通常只有一侧有改动（暂存了或没暂存），
	// 少起一个 git 进程直接反映为点击之后的等待更短。整个仓库没有状态位可依据，两侧都取
	if file == nil || (file.Index != "." && file.Index != "?") {
		text, truncated, err := diffText(ctx, repo.Path, true, paths)
		if err != nil {
			writeUIDiffError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out.Truncated = out.Truncated || truncated
		// 正文为空就不占一段：页面上会多出一个只有标题的空段
		if text != "" {
			section := uiDiffSection{Kind: diffKindStaged, Text: text}
			if file == nil {
				section.Files = numstatOrWarn(ctx, repo.Path, true, nil)
			}
			out.Sections = append(out.Sections, section)
		}
	}
	if file == nil || file.Work != "." {
		text, truncated, err := diffText(ctx, repo.Path, false, paths)
		if err != nil {
			writeUIDiffError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out.Truncated = out.Truncated || truncated
		if text != "" {
			section := uiDiffSection{Kind: diffKindUnstaged, Text: text}
			if file == nil {
				section.Files = numstatOrWarn(ctx, repo.Path, false, nil)
			}
			out.Sections = append(out.Sections, section)
		}
	}

	writeUIDiff(w, out)
}

// handleCommitDiff 处理“某条提交改了什么”。
//
// 单独一条路径而不是挤进工作区那条：两者的“什么才算合法输入”根本不同——工作区看的是上一次
// 快照（文件此刻在不在、暂没暂存），提交看的是这条提交自己的改动清单（文件可能早就删了，
// 更谈不上暂存状态）。硬凑成一条会让两边的校验互相打架
func handleCommitDiff(w http.ResponseWriter, r *http.Request, repo *uiRepo, hash, filePath string) {
	// 哈希会直接进 git 的命令行，必须先确认它只是一串十六进制（同 handleCommitFiles）
	if !validHash(hash) {
		writeUIDiffError(w, http.StatusBadRequest, l10n.T("Invalid commit hash", nil))
		return
	}

	ctx := r.Context()
	files, err := git.CommitFiles(ctx, repo.Path, hash)
	if err != nil {
		writeUIDiffError(w, statusOf(err, http.StatusInternalServerError), err.Error())
		return
	}

	out := uiDiff{Repo: repo.Path, Commit: hash, File: filePath}
	var paths []string
	if filePath != "" {
		f, ok := findCommitFile(files, filePath)
		if !ok {
			// 不在这条提交的改动清单里就是没有这个文件：既挡住了路径穿越，
			// 也挡住了“拿别的提交的文件名来问”
			writeUIDiffError(w, http.StatusNotFound, l10n.T("Unknown file", nil))
			return
		}
		out.File, out.OrigPath, out.Binary = f.Path, f.OrigPath, f.Binary
		paths = uiAffectedPaths(f.Path, f.OrigPath)
	}

	text, truncated, err := commitDiffText(ctx, repo.Path, hash, paths)
	if err != nil {
		writeUIDiffError(w, statusOf(err, http.StatusInternalServerError), err.Error())
		return
	}
	out.Truncated = truncated
	out.Sections = []uiDiffSection{{Kind: diffKindCommit, Text: text, Files: files}}
	writeUIDiff(w, out)
}

// findCommitFile 在一条提交的改动清单里按路径找文件，重命名时新旧两个路径都算命中
func findCommitFile(files []git.CommitFile, path string) (git.CommitFile, bool) {
	for _, f := range files {
		if f.Path == path || (f.OrigPath != "" && f.OrigPath == path) {
			return f, true
		}
	}
	return git.CommitFile{}, false
}

// commitDiffText 取一条提交的改动文本。
//
//   - --format= 去掉提交头：元信息由卡片显示，这里只要改动
//   - --diff-merges=first-parent 是为了合并提交：git 默认对合并提交用组合格式，那种格式
//     一列里同时写“与父提交甲、父提交乙分别差什么”，页面认不出（它按行首单个 +/- 着色），
//     读的人也分不清哪一行属于哪一次比较。统一成“相对第一个父提交”，页面上再注明这一点
//   - --find-renames 与 --unified=3 是钉住取值：别让用户配置里的 diff.renames / diff.context
//     改变页面上的显示（改名会被当成“删一个加一个”，上下文行数也会变得五花八门）
func commitDiffText(ctx context.Context, repoPath, hash string, paths []string) (string, bool, error) {
	args := []string{
		"show", "--format=", "--no-color", "--no-ext-diff", "--no-textconv",
		"--find-renames", "--diff-merges=first-parent", "--unified=3", hash,
	}
	if len(paths) > 0 {
		// "--" 之前是选项之后是路径：少了它，以 - 开头的文件名会被当成选项
		args = append(args, "--")
		args = append(args, paths...)
	}

	out, err := git.RunContext(ctx, repoPath, args...)
	if err != nil {
		return "", false, err
	}
	text, truncated := truncateAtLine(out, uiDiffLimit)
	return text, truncated, nil
}

// diffText 取一份不带颜色的统一 diff，staged 为真取 index vs HEAD，否则取工作区 vs index。
//
// 三个参数都是“为了让输出可解析”而不是为了好看：
//   - --no-color：去掉 ANSI 转义，页面按行首字符自己着色，两处都上色会互相打架
//   - --no-ext-diff：挡住用户配置的外部 diff 工具（diff.external），它可能输出 HTML
//     或任何格式，页面解析不了，表现为“点了没反应”
//   - --no-textconv：挡住 textconv 过滤器，否则二进制文件会被转成文本，
//     页面再也认不出它是二进制
func diffText(ctx context.Context, repoPath string, staged bool, paths []string) (string, bool, error) {
	args := []string{"diff", "--no-color", "--no-ext-diff", "--no-textconv"}
	if staged {
		args = append(args, "--cached")
	}
	if len(paths) > 0 {
		// "--" 之前是选项之后是路径：少了它，以 - 开头的文件名会被当成选项
		args = append(args, "--")
		args = append(args, paths...)
	}

	out, err := git.RunContext(ctx, repoPath, args...)
	if err != nil {
		return "", false, err
	}
	text, truncated := truncateAtLine(out, uiDiffLimit)
	return text, truncated, nil
}

// numstatOrWarn 取文件级增删清单，失败只记一条日志、不打断请求。
//
// 为什么可以容忍失败：这份清单只影响整仓 diff 的分段标题，diff 正文本身已经拿到了。
// 为它返回 500 等于把一份能读的 diff 扔掉；页面那边会退化成用 git 原文里的
// "diff --git" 行当标题，仍然是分段的，只是少了增删行数
func numstatOrWarn(ctx context.Context, repoPath string, staged bool, paths []string) []git.CommitFile {
	files, err := git.DiffNumstat(ctx, repoPath, staged, paths)
	if err != nil {
		logger.Warn("取 diff 的文件级增删行数失败", "repo", repoPath, "staged", staged, "err", err)
		return nil
	}
	return files
}

// truncateAtLine 把文本截到 limit 字节以内，并保证截断落在行边界上。
// 返回是否真的截断了。落在行边界是为了既不切出半个 UTF-8 字符，也不留下半行
// 让人以为文件就长这样
func truncateAtLine(s string, limit int) (string, bool) {
	if len(s) <= limit {
		return s, false
	}
	if i := strings.LastIndexByte(s[:limit], '\n'); i >= 0 {
		return s[:i+1], true
	}
	// 整份内容只有一行且超过上限：只能硬切，但要退到合法的 UTF-8 边界，
	// 否则 JSON 编码会用 U+FFFD 替换掉残缺字节，页面上出现一串乱码方块
	cut := s[:limit]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut, true
}

// readUntrackedFile 读未跟踪文件的正文，供页面按“整份都是新增”展示。
//
// 相对路径与仓库根拼接后会解析符号链接再比对：仓库里可能存在指向仓库外的软链，
// 只做 filepath.IsLocal 挡不住它，而这道读取是唯一一处按请求触碰文件系统的地方
func readUntrackedFile(repoPath, rel string) (string, bool, bool, error) {
	root, err := filepath.EvalSymlinks(repoPath)
	if err != nil {
		return "", false, false, err
	}
	full, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", false, false, err
	}
	back, err := filepath.Rel(root, full)
	if err != nil || !filepath.IsLocal(back) {
		return "", false, false, fmt.Errorf("%s", l10n.T("Invalid file path", nil))
	}

	f, err := os.Open(full)
	if err != nil {
		return "", false, false, err
	}
	defer f.Close()

	// 多读一个字节用来判断有没有被截断：LimitReader 读到上限就停，
	// 只看长度是否等于上限无法区分“刚好到上限”与“还有更多”
	data, err := io.ReadAll(io.LimitReader(f, uiUntrackedLimit+1))
	if err != nil {
		return "", false, false, err
	}
	truncated := len(data) > uiUntrackedLimit
	if truncated {
		data = data[:uiUntrackedLimit]
	}

	sniff := data
	if len(sniff) > uiBinarySniffLen {
		sniff = sniff[:uiBinarySniffLen]
	}
	if bytes.IndexByte(sniff, 0) >= 0 {
		return "", true, truncated, nil
	}
	return string(data), false, truncated, nil
}

// findUIRepo 在快照里按路径精确匹配仓库。路径由页面原样回传（它就是从这份快照拿的），
// 因此不需要也不应该做任何规范化：规范化会引入“两个不同请求映射到同一仓库”的可能
func findUIRepo(p *uiPayload, path string) (*uiRepo, bool) {
	for i := range p.Repos {
		if p.Repos[i].Path == path {
			return &p.Repos[i], true
		}
	}
	return nil, false
}

// findUIFile 在仓库快照里按相对路径精确匹配变更文件。
func findUIFile(r *uiRepo, path string) (uiFile, bool) {
	for _, f := range r.Files {
		if f.Path == path {
			return f, true
		}
	}
	return uiFile{}, false
}

// uiParamError 是“页面传来的参数不合法”这一类错误，附带应当回给页面的状态码。
//
// 为什么要带状态码：同一份路径校验被读（/api/diff）与写（暂存、取消暂存）两条路径共用，
// 而两条路径的失败码不同——读是 400/404，写更贴近 409。让校验处决定状态码、
// 调用处只管回响应，就不会出现两处各自发挥、慢慢漂移的情况
type uiParamError struct {
	status int
	msg    string
}

func (e *uiParamError) Error() string { return e.msg }

// statusOf 取出错误里携带的状态码；不是 uiParamError 时返回 fallback。
//
// 两条调用路径的 fallback 不同，这正是它必须成为参数的原因：
//   - 读路径（/api/diff）的校验失败只可能是参数错误，真出现别的就是我们的 bug，fallback 用 500
//   - 写路径还会遇到 git 自身的拒绝（存在冲突、没有暂存内容、未配置身份），
//     那属于“当前状态不允许这个操作”，fallback 用 409 与真正的服务端故障区分开
func statusOf(err error, fallback int) int {
	var pe *uiParamError
	if errors.As(err, &pe) {
		return pe.status
	}
	return fallback
}

// lookupUIFile 校验页面传来的文件路径，返回快照里的条目。
//
// 两道检查都与 /api/diff 一致，且只有这一份实现：
//   - 必须命中快照里的条目 -> 404。页面传来的 <仓库, 文件> 只能来自上一次 /api/repos，
//     因此即便有人手工构造请求，也读不到、更动不到配置之外的仓库与仓库之外的文件
//   - 必须是仓库内的相对路径 -> 400。纵深防御：porcelain 给出的路径本就应当是仓库内的相对路径，
//     这里再挡一次，免得将来某条状态解析路径被改坏之后，页面能顺着 ../ 动到仓库外
func lookupUIFile(repo *uiRepo, path string) (uiFile, error) {
	file, ok := findUIFile(repo, path)
	if !ok {
		return uiFile{}, &uiParamError{http.StatusNotFound, l10n.T("Unknown file", nil)}
	}
	if !filepath.IsLocal(path) {
		return uiFile{}, &uiParamError{http.StatusBadRequest, l10n.T("Invalid file path", nil)}
	}
	return file, nil
}

// uiAffectedPaths 给出一次 git 操作要覆盖的路径集合。
//
// 重命名必须同时给新旧两个路径：这一点在 diff 上是“看不出是重命名”，在暂存操作上更严重——
// 实测只给新路径取消暂存，旧路径那份删除会留在暂存区（状态变成 "D old + ?? new"），
// 看起来像没撤干净。
// 收两个字符串而不是 uiFile：提交视图里拿到的是 git.CommitFile，两个类型都能用
func uiAffectedPaths(path, origPath string) []string {
	paths := []string{path}
	if origPath != "" {
		paths = append(paths, origPath)
	}
	return paths
}

// writeUIDiff 输出 diff 响应。与 handleRepos 一样关掉 HTML 转义：
// diff 正文里 < > & 极常见，转义后排查接口时看到的 JSON 无法阅读
func writeUIDiff(w http.ResponseWriter, out uiDiff) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// diff 是随工作区变化的实时数据，任何一层缓存都不该留下副本
	w.Header().Set("Cache-Control", "no-store")
	writeUIDiffJSON(w, out)
}

// writeUIDiffError 用合适的 HTTP 状态码回一个只有 Error 字段的响应。
// 失败的响应同样走 JSON：页面只有一条解析路径，不必为错误另写一套分支
func writeUIDiffError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	writeUIDiffJSON(w, uiDiff{Error: msg})
}

func writeUIDiffJSON(w http.ResponseWriter, out uiDiff) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		// 响应头已发出，无法再改状态码，只能记录日志
		logger.Error(l10n.T("Failed to encode the diff response", nil), "error", err)
	}
}

// ——— 写操作：暂存 / 取消暂存 / 提交 / 推送 ———
//
// 四者都改仓库状态，因此一律挂在 POST 上：基座的 writeGuard 只对非 GET/HEAD 做同源校验，
// 挂在 GET 上等于自己把 CSRF 那道防线绕掉
//
// 共同的三条前提：
//   - 仓库必须命中已有快照，页面根本传不进配置之外的路径
//   - 文件必须同时命中该仓库快照里的条目、且是仓库内的相对路径
//   - 失败原因一律把 git 的原话透给页面，不翻译成“操作失败”：push 失败可能是没 upstream、
//     可能是网络、可能是权限，笼统的提示等于让用户自己去猜

// uiWriteRequest 是四个写端点共用的请求体。
// 不拆成四个结构：字段少且同名同义，拆开只会让前端多记几种形状
type uiWriteRequest struct {
	// Repo 是仓库绝对路径，必须与 /api/repos 返回的一致
	Repo string `json:"repo"`
	// File 是相对仓库根的路径，仅暂存/取消暂存使用
	File string `json:"file"`
	// Message 是提交信息，仅提交使用
	Message string `json:"message"`
	// Branch 是分支名，仅切换分支使用
	Branch string `json:"branch"`
}

// uiWriteResult 是四个写端点的统一响应。
// 成功带 Output（git 的原话：提交摘要、push 进度），失败带 Error
type uiWriteResult struct {
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

// handleWrite 是所有写操作的公共骨架：方法校验 -> 取仓库 -> 跑具体动作 -> 失效快照 -> 回响应。
// 各端点只负责"跑哪条 git 命令"，路径校验与响应形状都收敛在这里
func (c *uiCache) handleWrite(w http.ResponseWriter, r *http.Request, run func(ctx context.Context, repo *uiRepo, req uiWriteRequest) (string, error)) {
	// 只认 POST：写操作挂在 GET 上会被基座的同源校验直接放过，那正是 CSRF 想利用的形状
	if r.Method != http.MethodPost {
		writeUIWrite(w, http.StatusMethodNotAllowed, uiWriteResult{Error: l10n.T("Only POST is allowed", nil)})
		return
	}

	var req uiWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeUIWrite(w, http.StatusBadRequest, uiWriteResult{Error: l10n.T("Invalid request body", nil)})
		return
	}

	repo, ok := findUIRepo(c.repos(), req.Repo)
	if !ok {
		writeUIWrite(w, http.StatusNotFound, uiWriteResult{Error: l10n.T("Unknown repository", nil)})
		return
	}

	out, err := run(r.Context(), repo, req)

	// 不论成败都失效快照：失败也可能是“部分生效”（例如 push 已经送达但退出码非零），
	// 而重新采集一次的代价远小于让页面停在一个错的旧状态上
	c.invalidate()

	if err != nil {
		// git 的说明在 output 里（RunCombinedContext 即使非零退出也返回它），而 err 只是
		// "exit status 128" 这种毫无信息量的包装。对用户来说，"没有 upstream 分支""没有
		// 可提交的内容"这类原话才是他判断该做什么的依据，因此有 output 就用 output
		msg := strings.TrimSpace(out)
		if msg == "" {
			msg = err.Error()
		}
		writeUIWrite(w, statusOf(err, http.StatusConflict), uiWriteResult{Error: msg})
		return
	}
	writeUIWrite(w, http.StatusOK, uiWriteResult{Output: out})
}

// handleStage 暂存一个文件。
func (c *uiCache) handleStage(w http.ResponseWriter, r *http.Request) {
	c.handleWrite(w, r, func(ctx context.Context, repo *uiRepo, req uiWriteRequest) (string, error) {
		file, err := lookupUIFile(repo, req.File)
		if err != nil {
			return "", err
		}
		// 未合并的文件明确拒绝，这是本视图唯一一处“不给做”的操作。
		// 理由：对冲突文件执行 git add 等于把工作区那一份（通常还带着 <<<<<<< 标记）
		// 当成分辨结果暂存下来，一次点击就可能把冲突标记提交进去。
		// 本看板把冲突标成 "!"，含义是“这里要人来处理”，而不是“点一下就解决”
		if file.Unmerged {
			return "", &uiParamError{
				http.StatusConflict,
				l10n.T("This file is unmerged; stage the resolution from a terminal", nil),
			}
		}
		// add -A 而不是 add：工作区删除的文件也要能暂存，plain add 不记录删除
		// （实测 " D g.txt" 经 add 后变成 "D  g.txt"）
		return git.RunCombinedContext(ctx, repo.Path, append([]string{"add", "-A", "--"}, uiAffectedPaths(file.Path, file.OrigPath)...)...)
	})
}

// handleUnstage 取消暂存一个文件。
//
// 用 reset HEAD -- 而不是 restore --staged：实测在“尚无提交”的仓库上 restore 会直接失败
// （fatal: could not resolve HEAD），而"刚 add 完、还没第一次提交就想撤回"恰恰是最需要
// 这个按钮的时候；reset HEAD -- 在那种仓库上正常工作
func (c *uiCache) handleUnstage(w http.ResponseWriter, r *http.Request) {
	c.handleWrite(w, r, func(ctx context.Context, repo *uiRepo, req uiWriteRequest) (string, error) {
		file, err := lookupUIFile(repo, req.File)
		if err != nil {
			return "", err
		}
		return git.RunCombinedContext(ctx, repo.Path, append([]string{"reset", "-q", "HEAD", "--"}, uiAffectedPaths(file.Path, file.OrigPath)...)...)
	})
}

// handleCommit 用页面给的提交信息创建一次提交，只提交已暂存的改动。
//
// “只提交已暂存”不需要额外判断：git commit 本来就只提交暂存区，有未合并条目时它自己会拒绝，
// 没有暂存内容时也会明确报错——那些原话对用户比任何自拟提示都有用
func (c *uiCache) handleCommit(w http.ResponseWriter, r *http.Request) {
	c.handleWrite(w, r, func(ctx context.Context, repo *uiRepo, req uiWriteRequest) (string, error) {
		msg := strings.TrimSpace(req.Message)
		if msg == "" {
			return "", &uiParamError{http.StatusBadRequest, l10n.T("The commit message is empty", nil)}
		}
		// 以 argv 直接传参（不经 shell），信息里的任何字符都不会被解释；
		// 以 - 开头也安全：-m 会把它当成自己的参数而不是选项（实测过 "-foo bar"）
		return git.RunCombinedContext(ctx, repo.Path, "commit", "-m", msg)
	})
}

// handlePush 推送当前分支。
//
// 不替用户建立跟踪关系（不加 -u）：往哪个远程、哪个分支推送是仓库拓扑的一部分，
// 由服务自作主张建好跟踪，一旦推错对象代价很高。git 自己会明确拒绝并说明该怎么建
// （有远程但没 upstream 时说 "has no upstream branch" 并给出 --set-upstream 的用法；
// 连远程都没配时说 "No configured push destination"），这些原话直接透给页面
func (c *uiCache) handlePush(w http.ResponseWriter, r *http.Request) {
	c.handleWrite(w, r, func(ctx context.Context, repo *uiRepo, _ uiWriteRequest) (string, error) {
		return git.RunCombinedContext(ctx, repo.Path, "push")
	})
}

// handleStageAll 暂存整个仓库的改动（看板上“未暂存的改动”那一行右边的 + 按钮）。
//
// 与单个文件的按钮保持一致：存在未合并文件时拒绝。git add -A 会把还带着冲突标记的文件一起
// 暂存进去，而“点一下全部暂存”正是最容易在没注意时把冲突标记提交进去的操作
func (c *uiCache) handleStageAll(w http.ResponseWriter, r *http.Request) {
	c.handleWrite(w, r, func(ctx context.Context, repo *uiRepo, _ uiWriteRequest) (string, error) {
		for _, f := range repo.Files {
			if f.Unmerged {
				return "", &uiParamError{
					http.StatusConflict,
					l10n.T("There are unmerged files; resolve them before staging everything", nil),
				}
			}
		}
		// add -A 而不是 add：工作区删除的文件也要能暂存（与单文件那条同样的理由）
		return git.RunCombinedContext(ctx, repo.Path, "add", "-A")
	})
}

// handleUnstageAll 取消暂存整个仓库（看板上“已暂存的改动”那一行右边的 − 按钮）。
//
// 用不带 HEAD、不带路径的 git reset：实测在“尚无提交”的仓库上 reset HEAD 会失败
// （fatal: ambiguous argument 'HEAD'），而那种仓库恰恰最需要这个按钮——刚 add 完、还没第一次提交
func (c *uiCache) handleUnstageAll(w http.ResponseWriter, r *http.Request) {
	c.handleWrite(w, r, func(ctx context.Context, repo *uiRepo, _ uiWriteRequest) (string, error) {
		return git.RunCombinedContext(ctx, repo.Path, "reset", "-q")
	})
}

// handleCheckout 切换分支。
//
// 工作区有未提交改动时直接拒绝：git checkout 在这种状态下常常“成功”——它把改动原样带到另一个
// 分支上，用户以为切干净了，其实改动跟了过来，之后切回去又是一堆意外。拒绝之后由用户决定
// 是提交、暂存还是丢弃
func (c *uiCache) handleCheckout(w http.ResponseWriter, r *http.Request) {
	c.handleWrite(w, r, func(ctx context.Context, repo *uiRepo, req uiWriteRequest) (string, error) {
		branch := strings.TrimSpace(req.Branch)
		if branch == "" {
			return "", &uiParamError{http.StatusBadRequest, l10n.T("A branch name is required", nil)}
		}
		// 分支名会进 git 的命令行：以 - 开头的会被当成选项（例如 -f 是强制切走、丢弃改动）
		if strings.HasPrefix(branch, "-") || strings.ContainsAny(branch, " \t\n") {
			return "", &uiParamError{http.StatusBadRequest, l10n.T("Invalid branch name", nil)}
		}
		if len(repo.Files) > 0 {
			return "", &uiParamError{
				http.StatusConflict,
				l10n.T("The working tree has uncommitted changes; commit or stash them before switching branches", nil),
			}
		}
		return git.RunCombinedContext(ctx, repo.Path, "checkout", branch)
	})
}

// handlePull 拉取当前分支（--ff-only，与 ggt sync 的语义一致）。
//
// 不替用户合并或变基：--ff-only 在历史分叉时会失败并把 git 的原话交出来，
// 而那正是用户需要看到的信息（要不要 rebase 还是 merge，由他决定）
func (c *uiCache) handlePull(w http.ResponseWriter, r *http.Request) {
	c.handleWrite(w, r, func(ctx context.Context, repo *uiRepo, _ uiWriteRequest) (string, error) {
		return git.RunCombinedContext(ctx, repo.Path, "pull", "--ff-only")
	})
}

// handleSync 对应 VSCode 的“同步”：先 pull --ff-only，成功之后再 push。
//
// 两步串行而不是并行：pull 失败（本地分叉、没配 upstream）时不该再推一次——
// 那会把“同步失败”变成“推了一半”，用户更难判断当前处在什么状态
func (c *uiCache) handleSync(w http.ResponseWriter, r *http.Request) {
	c.handleWrite(w, r, func(ctx context.Context, repo *uiRepo, _ uiWriteRequest) (string, error) {
		pullOut, err := git.RunCombinedContext(ctx, repo.Path, "pull", "--ff-only")
		if err != nil {
			return pullOut, err
		}
		pushOut, err := git.RunCombinedContext(ctx, repo.Path, "push")
		if err != nil {
			return pullOut + pushOut, err
		}
		return pullOut + pushOut, nil
	})
}

// handleFetch 手动拉取远程数据：带 repo 就只拉那一个仓库（仓库卡片顶栏的按钮），
// 不带就拉快照里的全部仓库（看板底栏的按钮）。每个仓库跑一次 git fetch --all --prune。
//
// 一个端点两种范围，而不是两个端点：命令、并发方式与响应形状完全一样，差别只在“对哪些仓库跑”，
// 拆成两个端点等于把这段逻辑抄两遍
//
// 并发跑：与 ggt sync 共用 worker.Map 与同一个并发度设置。串行做几十个远程仓库要等到
// 地老天荒，并发下总耗时约等于最慢的那一个
//
// 不复用 handleWrite 那套骨架：那个是“针对某一个仓库、且仓库必填”的（要从请求里取 repo、
// 校验文件），而这里允许对全部仓库跑，生搬硬套会让它多出一个“仓库为空即全部”的隐式约定
func (c *uiCache) handleFetch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeUIWrite(w, http.StatusMethodNotAllowed, uiWriteResult{Error: l10n.T("Only POST is allowed", nil)})
		return
	}

	// body 是可选的：看板底栏那个按钮发的是空 body，解析失败（EOF）即当作“全部仓库”
	var req uiWriteRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	// 只需要仓库名与错误文本，不需要更多字段；outcome 保持按仓库顺序返回（worker.Map 保序）
	type outcome struct {
		name string
		err  string
	}
	repos := c.repos().Repos
	if strings.TrimSpace(req.Repo) != "" {
		repo, ok := findUIRepo(c.repos(), req.Repo)
		if !ok {
			writeUIWrite(w, http.StatusNotFound, uiWriteResult{Error: l10n.T("Unknown repository", nil)})
			return
		}
		repos = []uiRepo{*repo}
	}
	outcomes := worker.Map(r.Context(), repos, Concurrency(), func(ctx context.Context, repo uiRepo) outcome {
		// 用 RunCombinedContext：失败原因在 git 的 stderr 里，err 本身只是 "exit status N"
		out, err := git.RunCombinedContext(ctx, repo.Path, "fetch", "--all", "--prune")
		if err != nil {
			return outcome{name: repo.Name, err: strings.TrimSpace(out)}
		}
		return outcome{name: repo.Name}
	})

	// fetch 会改变 ahead/behind，因此不论成败都让快照失效，让页面立刻看到新的领先/落后
	c.invalidate()

	failures := make([]string, 0, len(outcomes))
	for _, o := range outcomes {
		if o.err != "" {
			failures = append(failures, o.name+": "+o.err)
		}
	}
	if len(failures) > 0 {
		// 只展开第一个失败的完整原话，其余报个数：失败可能有几十个，全写进一行提示
		// 会把它撑成一大段，反而看不出到底有几个仓库失败了
		msg := l10n.T("Fetched {{.Count}} repositories, {{.Failed}} failed",
			map[string]any{"Count": len(outcomes), "Failed": len(failures)})
		msg += "\n" + failures[0]
		if len(failures) > 1 {
			msg += "\n" + l10n.T("… and {{.Count}} more", map[string]any{"Count": len(failures) - 1})
		}
		writeUIWrite(w, http.StatusConflict, uiWriteResult{Error: msg})
		return
	}
	writeUIWrite(w, http.StatusOK, uiWriteResult{
		Output: l10n.T("Fetched {{.Count}} repositories", map[string]any{"Count": len(outcomes)}),
	})
}

// writeUIWrite 回一个写操作的结果。
// 与其它端点一样关掉 HTML 转义：git 的输出里 < > & 常见（例如冲突标记），
// 转义后页面看到的会是一串 \u003c
func writeUIWrite(w http.ResponseWriter, status int, out uiWriteResult) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		// 响应头已发出，无法再改状态码，只能记录日志
		logger.Error(l10n.T("Failed to encode the write response", nil), "error", err)
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
	// 自动打开或打印出来，用户并不需要记住它；固定端口反而会让“同时开两个实例”直接失败
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

// renderIndexHTML 把语言、主题配色与设置快照注入首页模板。
//
// 从 runUI 里抽出来是为了能在测试里把注入结果整体看一遍：占位符与它所在的表达式同名时
// （例如 window.__X__ = __X__），ReplaceAll 会把赋值左边也一起换掉，生成一段语法错误的
// 脚本——服务端一切正常，只是页面整个不动。这一条是实际遇到过的
//
// 注入设置快照是给“页面行为”读用的（当前只有通知自动消失的时长）。注入整份而不只注入
// 用得到的那一项：占位符是“每加一项配置就要改一次渲染函数”的写法，而这份快照按注册表
// 生成，将来页面再多读一项也不必改这里
func renderIndexHTML(indexHTML []byte, lang string) []byte {
	out := bytes.ReplaceAll(indexHTML, []byte("__GGT_HTML_LANG__"), []byte(lang))
	out = bytes.ReplaceAll(out, []byte("__GGT_LANG_VALUE__"), []byte(lang))
	out = bytes.ReplaceAll(out, []byte("__GGT_SETTINGS_JSON__"), uiSettingsJSON(config.GetDefaultConfigPath()))
	return bytes.ReplaceAll(out, []byte("__GGT_THEME_CSS__"), []byte(resolveTheme()))
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
	// 点开某个仓库或文件时的只读 diff。与 /api/repos 挂在同一个 mux 上，
	// 因此同样在 webui 基座的 Host/Origin/token 三项检查之内
	mux.HandleFunc("/api/diff", cache.handleDiff)
	// 四个写端点。它们同样只挂在 mux 上而不额外加检查：基座对非 GET/HEAD 会先做
	// Origin/Referer 同源校验，再限 body 大小，写请求的 CSRF 面由那一层负责
	mux.HandleFunc("/api/stage", cache.handleStage)
	mux.HandleFunc("/api/unstage", cache.handleUnstage)
	// 整仓的暂存 / 取消暂存：看板上两个分组标题行右边的按钮
	mux.HandleFunc("/api/stage-all", cache.handleStageAll)
	mux.HandleFunc("/api/unstage-all", cache.handleUnstageAll)
	mux.HandleFunc("/api/commit", cache.handleCommit)
	mux.HandleFunc("/api/push", cache.handlePush)
	// 仓库卡片顶栏上的三个仓库级动作：切换分支、拉取当前分支、同步（pull --ff-only + push）
	mux.HandleFunc("/api/checkout", cache.handleCheckout)
	mux.HandleFunc("/api/pull", cache.handlePull)
	mux.HandleFunc("/api/sync", cache.handleSync)
	// 拉取是“对全部仓库”的一次行动，因此单独一个端点，不挂在某个仓库上
	mux.HandleFunc("/api/fetch", cache.handleFetch)
	// 分支图：提交历史与一个提交的文件列表，都是只读的。仓库操作（切分支、拉取、同步等）
	// 另有各自的端点，见上面那几行
	mux.HandleFunc("/api/log", cache.handleLog)
	mux.HandleFunc("/api/commit-files", cache.handleCommitFiles)
	// 设置面板：GET 读全部配置项（由注册表投影而来），POST 写。
	// 它不碰仓库，只读写配置文件，因此与上面那些端点没有共同前提
	mux.HandleFunc("/api/settings", handleSettings(config.GetDefaultConfigPath()))

	// 页面自己不会说“当前语言是哪个”，由 Go 端把语言写进两个占位符：
	//   - __GGT_LANG_VALUE__ 供页面内翻译表选语言
	//   - __GGT_HTML_LANG__ 供浏览器挑字体、断词与朗读规则（写成 zh-CN 时英文界面
	//     在无障碍工具里会被按中文朗读）
	// 前端文案不经过 Go 的 l10n 提取管线（那条管线只扫 .go），所以页面自带一份翻译表，
	// 后端不需要知道页面里有哪些文案
	//
	// 用 ReplaceAll 而不是 Replace(…, 1)：后者只替换第一处，一旦页面注释里出现占位符字面量，
	// 被替换的就是注释、真正的使用处原样留下，语言会静默停在默认值上——实际遇到过一次，
	// 表现为界面文案全是英文而所有数据正常，很难联想到是注释把占位符也替换掉了
	// 主题与语言一样每次请求现算：两者都能在运行期改（语言改配置，主题在设置面板里选），
	// 烧死在启动时就会表现为“改了不生效”。主题只注入配色 CSS：候选清单与当前选择由
	// 设置面板自己取（/api/settings），不再随首页多带一份
	renderIndex := func() []byte {
		return renderIndexHTML(indexHTML, l10n.Current())
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
	//“什么算回环”与警告文案都由 webui 给出，避免两处判断各说各话
	if warning := srv.NonLoopbackWarning(); warning != "" {
		WarnMsg(warning)
	}

	// 日志与用户可见摘要分开：日志受 log_level 过滤（默认 warn），而服务地址属于
	//“必须默认可见”的信息——用户要凭它打开页面，因此直接写标准输出
	logger.Info(l10n.T("Starting WebUI", nil), "addr", srv.Addr())
	fmt.Fprintln(cmd.OutOrStdout(), srv.Summary())

	// 拉起浏览器与阻塞都由 webui 负责；OpenBrowser 打开的是带 token 的完整地址
	return srv.Serve()
}

func init() {
	register(func(root *cobra.Command) { root.AddCommand(newUICmd()) })
}
