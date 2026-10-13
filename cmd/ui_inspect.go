// ui_inspect.go 是看板上两个“按需查看单个仓库”的只读端点：仓库大小与全库文件清单。
//
// 为什么单独一个文件：与 ui_settings.go 同理，这两个端点与仓库状态快照没有共同前提——
// 它们不读快照里的变更列表、不发写请求，而是打开某个仓库时才现跑一条 git。
// 塞进已经上千行的 ui.go 只会让“哪些端点会动仓库”这条分界线更模糊。
//
// 都挂在 GET 上：两者都只读，且基座的 writeGuard 只对非 GET/HEAD 做同源校验，
// 挂成 POST 既没有意义，也会让页面多写一层请求体
package cmd

import (
	"net/http"

	"github.com/jy-eggroll/eggokit/l10n"
)

// uiFileListLimit 是文件清单一次回给页面的条数上限。
//
// 为什么要截断：大仓库的清单轻松上万条，而这条响应是用户点开“文件”时现取的。
// 取 10000 与 internal/git 的 statusLimit 同值——项目里“超过一万条就截断并如实告知”
// 已经是既定做法，两处用同一个数目，用户不会在不同地方看到两个上限。
// 截断只影响这一次响应，不会漏报：Total 仍写真实条数，页面据此提示“只显示前 N 条”
const uiFileListLimit = 10000

// uiSizeInfo 是 GET /api/size 的响应体。
type uiSizeInfo struct {
	Repo string `json:"repo"`
	Name string `json:"name"`
	// Disk 与 Pack 是 git 的原话（形如 "80.85 MiB"），页面照抄而不在服务端重新格式化：
	// 重新格式化会引入第二套单位换算，与终端 ggt size 的输出迟早对不上
	Disk string `json:"disk,omitempty"`
	Pack string `json:"pack,omitempty"`
	// Bytes 是磁盘占用与包大小合计的字节数，分桶按它比较
	Bytes int64 `json:"bytes"`
	// Bucket 取值 small / mid / large，页面据此选颜色。终端的分桶着色与本字段同源
	Bucket string `json:"bucket,omitempty"`
	LowMB  int    `json:"lowMb"`
	HighMB int    `json:"highMb"`
	// Unit 是本次分桶实际使用的口径（decimal 或 binary）。页面上要标明 MB 是哪一种：
	// 同一份数字换个口径差约 4.8%，不写清楚用户无从判断阈值是按哪种算的
	Unit string `json:"unit"`
	// Details 是次要指标（对象数、包数、可裁剪、垃圾）。键沿用 git 的原话不翻译，
	// 与终端输出保持一致，将来 git 新增字段也不必改这里
	Details []uiSizeDetail `json:"details,omitempty"`
	// Error 非空表示这条 git 命令没跑成。它属于“请求合法、这次没量到”，
	// 因此仍回 200，让页面只有一条渲染路径
	Error string `json:"error,omitempty"`
}

// uiSizeDetail 是大小视图里的一条次要指标。
type uiSizeDetail struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// uiFilesInfo 是 GET /api/files 的响应体。
type uiFilesInfo struct {
	Repo string `json:"repo"`
	Name string `json:"name"`
	// Total 是截断前的真实条数，Files 可能比它短（见 uiFileListLimit）
	Total     int      `json:"total"`
	Truncated bool     `json:"truncated"`
	Files     []string `json:"files"`
	Error     string   `json:"error,omitempty"`
}

// handleSize 是 /api/size 的处理函数。
//
// 仓库同样只从已有快照里取（与 /api/diff 一条理由）：即便有人手工构造请求，
// 也量不到配置之外的目录
func (c *uiCache) handleSize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeUIJSON(w, http.StatusMethodNotAllowed, uiSizeInfo{Error: l10n.T("Only GET is allowed", nil)})
		return
	}

	repo, ok := findUIRepo(c.repos(), r.URL.Query().Get("repo"))
	if !ok {
		writeUIJSON(w, http.StatusNotFound, uiSizeInfo{Error: l10n.T("Unknown repository", nil)})
		return
	}

	low, high, unit, _ := sizeThresholds(0, 0, "")
	out := uiSizeInfo{
		Repo:   repo.Path,
		Name:   repo.Name,
		LowMB:  low,
		HighMB: high,
		Unit:   unit,
	}

	// 用服务级 ctx 而不是 r.Context()：用户在页面上等这条 git 跑完的时间通常不到一秒，
	// 而两者对“点开仓库后立刻关掉页面”的处理没有区别，取现成的更省一层
	info, err := collectRepoSize(c.ctx, repo.Path)
	if err != nil {
		// 透 git 的原话而不是“统计失败”：失败可能是仓库损坏、可能是权限，
		// 笼统的提示等于让用户自己去猜
		out.Error = err.Error()
		writeUIJSON(w, http.StatusOK, out)
		return
	}

	out.Disk = info["size"]
	out.Pack = info["size-pack"]
	out.Bytes = calcTotalBytes(info)
	out.Bucket = sizeBucket(out.Bytes, low, high, unit)
	for _, k := range secondarySizeKeys {
		if v, ok := info[k]; ok {
			out.Details = append(out.Details, uiSizeDetail{Key: k, Value: v})
		}
	}
	writeUIJSON(w, http.StatusOK, out)
}

// handleFiles 是 /api/files 的处理函数。
//
// 采集直接调 files.go 的 showRepoFiles：终端 ggt files 与看板必须是同一份实现，
// 否则 --cached --others --exclude-standard 这三个开关迟早只改得动一处
func (c *uiCache) handleFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeUIJSON(w, http.StatusMethodNotAllowed, uiFilesInfo{Error: l10n.T("Only GET is allowed", nil)})
		return
	}

	repo, ok := findUIRepo(c.repos(), r.URL.Query().Get("repo"))
	if !ok {
		writeUIJSON(w, http.StatusNotFound, uiFilesInfo{Error: l10n.T("Unknown repository", nil)})
		return
	}

	out := uiFilesInfo{Repo: repo.Path, Name: repo.Name, Files: []string{}}

	// showRepoFiles 只用到 RepoEntry 的 Path 与 Name，这里按快照里的条目拼一个：
	// 它不从快照读状态，所以不存在“快照里没有的工作树”这类问题
	res := showRepoFiles(c.ctx, RepoEntry{Path: repo.Path, Name: repo.Name})
	if res.err != nil {
		out.Error = res.err.Error()
		writeUIJSON(w, http.StatusOK, out)
		return
	}

	out.Total = len(res.files)
	if out.Total > uiFileListLimit {
		out.Files = res.files[:uiFileListLimit]
		out.Truncated = true
	} else {
		out.Files = res.files
	}
	writeUIJSON(w, http.StatusOK, out)
}
