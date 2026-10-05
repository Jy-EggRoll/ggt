// 本文件是分支图在页面侧的后端：采集提交历史、算泳道、把颜色 id 翻成页面上的 CSS 变量名。
//
// 为什么颜色在这里翻：internal/git 只认 VSCode 的颜色 id（它那一层不认识 CSS），而
// id → CSS 变量名的映射只有一份（ui_theme.go 的 cssVarNames）。两者在这一层接上，
// 页面拿到的东西就直接能拼成 var(--...)
package cmd

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/eggokit/logger"
	"github.com/jy-eggroll/ggt/internal/git"
)

const (
	// graphPageSize 是一批提交数：页面滚到底就把它翻倍再取一次
	graphPageSize = 100
	// graphMaxLimit 是单次采集的上限。再大就不该一次取回来：git log 的代价随行数增长，
	// 页面为几万行建元素也会卡。到上限后页面上的“继续加载”会停在原处并如实提示
	graphMaxLimit = 2000
)

// uiLogResponse 是 /api/log 的响应
type uiLogResponse struct {
	// Items 是“每个提交 + 它上下两侧的泳道”，泳道颜色已经翻成 CSS 变量名
	Items []uiLogItem `json:"items"`
	// Total 是按当前范围能采集到的总数，页面用它显示"已显示 X / 共 N"
	Total int `json:"total"`
	// Head 是 HEAD 指向的提交，Branch / Upstream 是当前分支与它的上游（“只看当前分支”这个开关要用）
	Head     string `json:"head"`
	Branch   string `json:"branch"`
	Upstream string `json:"upstream"`
	// Branches 是本地分支名（升序），供卡片顶栏的分支选择器用。顺带在这一次请求里给出，
	// 而不是让页面再打一个接口：两者本来就要一起用，多一次往返只会多一次闪烁
	Branches []string `json:"branches"`
	// MaxLimit 是单次采集的上限，页面据此知道“继续加载”什么时候该停（上限只有一处定义）
	MaxLimit int `json:"maxLimit"`
}

type uiLogItem struct {
	Item   uiLogCommit   `json:"item"`
	Kind   string        `json:"kind"`
	Input  []uiGraphNode `json:"inputSwimlanes"`
	Output []uiGraphNode `json:"outputSwimlanes"`
	Index  int           `json:"index"`
}

type uiGraphNode struct {
	ID string `json:"id"`
	// Color 是 CSS 变量名（不含 var() 包装），页面拼成 var(--<name>)
	Color string `json:"color"`
}

type uiLogCommit struct {
	Hash        string     `json:"hash"`
	Parents     []string   `json:"parents"`
	Author      string     `json:"author"`
	AuthorEmail string     `json:"authorEmail"`
	Timestamp   int64      `json:"timestamp"`
	Subject     string     `json:"subject"`
	Message     string     `json:"message"`
	Refs        []uiLogRef `json:"refs"`
}

type uiLogRef struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Color 同上：CSS 变量名，空串表示用默认引用色
	Color string `json:"color,omitempty"`
}

// uiCommitFilesResponse 是 /api/commit-files 的响应
type uiCommitFilesResponse struct {
	Files      []git.CommitFile `json:"files"`
	Insertions int              `json:"insertions"`
	Deletions  int              `json:"deletions"`
}

// uiGraphError 是分支图相关端点的统一错误响应：页面只有一条解析路径，不必为错误另写一套分支
type uiGraphError struct {
	Error string `json:"error"`
}

// cssVarOf 把 VSCode 的颜色 id 翻成 CSS 变量名，没映射过的返回空串（页面回落到默认色）
func cssVarOf(colorID string) string {
	return cssVarNames[colorID]
}

// handleLog 返回一段提交历史与它的泳道。
//
// 分页是"把 limit 加大再取一次"，不是 skip：泳道是逐行递推出来的，只取第二页的话第一行的
// 输入泳道无从得知，整页的线都会从最左边重新开始。VSCode 的做法也是每次对“已加载的全部提交”
// 重算一遍（toISCMHistoryItemViewModelArray），因此这里每次都从第一条开始算，limit 只增不减——
// 代价是 O(已加载条数)，而这也正是前面那条“前缀稳定”单测守住的契约
func (c *uiCache) handleLog(w http.ResponseWriter, r *http.Request) {
	repo, ok := findUIRepo(c.repos(), r.URL.Query().Get("repo"))
	if !ok {
		writeUIGraphJSON(w, http.StatusNotFound, uiGraphError{Error: l10n.T("Unknown repository", nil)})
		return
	}

	limit := graphPageSize
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > graphMaxLimit {
		limit = graphMaxLimit
	}
	// 默认跨全部分支与 tag：用户要的就是“默认全部”，只在明确传 all=0 时收窄到当前分支
	all := r.URL.Query().Get("all") != "0"

	ctx := r.Context()
	items, err := git.LogHistory(ctx, repo.Path, git.HistoryOptions{Limit: limit, All: all})
	if err != nil {
		writeUIGraphJSON(w, statusOf(err, http.StatusInternalServerError), uiGraphError{Error: err.Error()})
		return
	}

	// 计数失败不该让整张图打不开：退化成“已知条数”，页面上的"N"会小于真实值，但图仍然可用
	total, err := git.CountHistory(ctx, repo.Path, all)
	if err != nil {
		logger.Warn(l10n.T("Failed to count the commits", nil), "path", repo.Path, "error", err)
		total = len(items)
	}

	branch, upstream, head := git.CurrentRefs(ctx, repo.Path)
	viewModels := git.LayoutHistory(items, branch, upstream, head)

	// 分支列表读不出来时不影响图：选择器退化成“只有当前分支”这一个选项
	branches, err := git.LocalBranches(ctx, repo.Path)
	if err != nil {
		logger.Warn(l10n.T("Failed to list the branches", nil), "path", repo.Path, "error", err)
		branches = []string{}
	}

	resp := uiLogResponse{
		Items:    make([]uiLogItem, 0, len(viewModels)),
		Total:    total,
		Head:     head,
		Branch:   branch,
		Upstream: upstream,
		Branches: branches,
		MaxLimit: graphMaxLimit,
	}
	for _, vm := range viewModels {
		resp.Items = append(resp.Items, uiLogItem{
			Item:   uiLogCommitOf(vm.Item),
			Kind:   vm.Kind,
			Input:  uiGraphNodes(vm.Input),
			Output: uiGraphNodes(vm.Output),
			Index:  vm.Index,
		})
	}
	writeUIGraphJSON(w, http.StatusOK, resp)
}

// handleCommitFiles 返回一个提交改了哪些文件、各增减多少行（详情面板里的文件列表）
func (c *uiCache) handleCommitFiles(w http.ResponseWriter, r *http.Request) {
	repo, ok := findUIRepo(c.repos(), r.URL.Query().Get("repo"))
	if !ok {
		writeUIGraphJSON(w, http.StatusNotFound, uiGraphError{Error: l10n.T("Unknown repository", nil)})
		return
	}

	hash := strings.TrimSpace(r.URL.Query().Get("hash"))
	// 哈希会直接进 git 的命令行，必须先确认它只是一串十六进制：带前导 - 或空格的字符串
	// 进去就是一次参数注入（例如 --output=/path/to/file）
	if !validHash(hash) {
		writeUIGraphJSON(w, http.StatusBadRequest, uiGraphError{Error: l10n.T("Invalid commit hash", nil)})
		return
	}

	files, err := git.CommitFiles(r.Context(), repo.Path, hash)
	if err != nil {
		writeUIGraphJSON(w, statusOf(err, http.StatusInternalServerError), uiGraphError{Error: err.Error()})
		return
	}

	resp := uiCommitFilesResponse{Files: files}
	for _, f := range files {
		resp.Insertions += f.Adds
		resp.Deletions += f.Dels
	}
	writeUIGraphJSON(w, http.StatusOK, resp)
}

// validHash 判断一个提交哈希是否可信：只允许十六进制、长度 4..64
func validHash(s string) bool {
	if len(s) < 4 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}

func uiLogCommitOf(item git.HistoryItem) uiLogCommit {
	refs := make([]uiLogRef, 0, len(item.Refs))
	for _, ref := range item.Refs {
		refs = append(refs, uiLogRef{Name: ref.Name, Kind: ref.Kind, Color: cssVarOf(ref.Color)})
	}
	parents := item.Parents
	if parents == nil {
		parents = []string{}
	}
	return uiLogCommit{
		Hash:        item.Hash,
		Parents:     parents,
		Author:      item.Author,
		AuthorEmail: item.AuthorEmail,
		Timestamp:   item.Timestamp,
		Subject:     item.Subject,
		Message:     item.Message,
		Refs:        refs,
	}
}

func uiGraphNodes(nodes []git.GraphNode) []uiGraphNode {
	out := make([]uiGraphNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, uiGraphNode{ID: n.ID, Color: cssVarOf(n.Color)})
	}
	return out
}

// writeUIGraphJSON 写出分支图端点的响应：与其它几个端点同样的两个响应头。
// 状态码为 200 时不显式 WriteHeader（让 net/http 自己写，少一次调用）
func writeUIGraphJSON(w http.ResponseWriter, status int, out any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// 历史是随仓库变化的实时数据，任何一层缓存都不该留下副本
	w.Header().Set("Cache-Control", "no-store")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		// 响应头已发出，无法再改状态码，只能记录日志
		logger.Error(l10n.T("Failed to encode the history response", nil), "error", err)
	}
}
