// ui_remote.go 是看板上的远程协议切换：把仓库 origin 的地址在 HTTPS 与 SSH 之间改写。
//
// 为什么单独一个文件：与 ui_settings.go 同一条理由——这个端点不读快照、不跑采集，
// 只做一次 git remote set-url，放进已经上千行的 ui.go 只会让“哪些端点会动仓库”这条
// 分界线更难看清
package cmd

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/eggokit/logger"
)

// 适用范围：网页里只切“用户点中的那一个仓库”，不提供“全部仓库”。
//
// 命令行默认作用于“当前目录的仓库”（含其子模块），网页里没有当前目录这个概念，
// 因此只剩下“某一个仓库”与“全部仓库”两种选择，这里选前者：
//   - 看板上已有的仓库级动作（切分支、拉取当前分支、同步、推送）都是单个仓库，
//     协议改写与它们是同一类动作，语义一致
//   - 改协议会改变该仓库之后的认证方式。用户很可能只给部分主机配了 SSH 密钥，
//     或只在部分主机上缓存了 HTTPS 凭据，一次批量改写会让原本能推送的仓库一起失败；
//     逐个仓库改则可以在出问题时对同一个仓库再点一次换回来，代价只有一次点击
//   - “全部”这条路命令行已经有（--all），那里有明确的目录上下文，也有逐仓库的结果
//     汇总（见 processSwitchResults），比网页上点一次就改掉几十个仓库更容易看清后果
//
// toggle 与 https/ssh 并列成第三种取值：命令行的 toggle 只作用于当前仓库（刻意不支持
// --all），它在网页里的对应物正是“用户点中的那一个仓库”，两边的含义刚好对齐

// uiRemoteProtocols 是网页端允许提交的目标取值。
//
// toggle 只是“取反”这个意图的名字，不是 git 认识的协议：它先由 resolveToggleTarget
// 读当前 URL 换算成 https 或 ssh 再落到仓库上。白名单写在一处而不是散在各个分支里判断，
// 是因为请求体属于外部输入，“页面能提交哪些值”只应该有一个答案
var uiRemoteProtocols = map[string]bool{"https": true, "ssh": true, "toggle": true}

// uiRemoteRequest 是 POST /api/remote 的请求体。
type uiRemoteRequest struct {
	// Repo 是仓库绝对路径，必须与 /api/repos 返回的一致（与其它写端点同一约定）
	Repo string `json:"repo"`
	// Protocol 取 https / ssh / toggle
	Protocol string `json:"protocol"`
}

// uiRemoteResult 是 POST /api/remote 的响应体。
//
// 回 oldUrl / newUrl 而不是只说一句“已切换”：origin 的地址是用户唯一能自己核对的凭据，
// 页面上直接看到 git@github.com:user/repo.git 变成 https://github.com/user/repo.git，
// 比任何自拟措辞都可靠，排查时也不用再回终端跑一次 git remote -v
type uiRemoteResult struct {
	OldURL string `json:"oldUrl,omitempty"`
	NewURL string `json:"newUrl,omitempty"`
	// Switched 为假表示原本就是目标协议（命令行里那一档 same），页面据此报“无需改动”
	Switched bool `json:"switched"`
	// Output 是成功时给页面显示的一行说明（切换前后地址，或“已经是目标协议”）
	Output string `json:"output,omitempty"`
	// Error 是失败原因，透传 git 与解析器的原话
	Error string `json:"error,omitempty"`
}

// handleRemote 是 /api/remote 的处理函数。
//
// 只认 POST：基座的 writeGuard 只对非 GET/HEAD 做同源校验，把这种会改仓库状态的动作
// 挂在 GET 上等于自己把 CSRF 那道防线绕掉（与其它写端点同一条理由）
func (c *uiCache) handleRemote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeUIRemote(w, http.StatusMethodNotAllowed, uiRemoteResult{Error: l10n.T("Only POST is allowed", nil)})
		return
	}

	var req uiRemoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeUIRemote(w, http.StatusBadRequest, uiRemoteResult{Error: l10n.T("Invalid request body", nil)})
		return
	}

	target := strings.ToLower(strings.TrimSpace(req.Protocol))
	if !uiRemoteProtocols[target] {
		writeUIRemote(w, http.StatusBadRequest, uiRemoteResult{Error: l10n.T("Unknown target protocol", nil)})
		return
	}

	// 仓库必须命中已有快照：页面根本传不进配置之外的路径，这和其它写端点的取仓库方式相同。
	// 这里不复用 handleWrite：那个流程要求“请求里必须带 repo 且只跑一条 git 命令”，
	// 而本端点除命令外还要先按协议取值换算目标（toggle 要读一次当前 URL），
	// 塞进去会让那个公共外壳多一条只服务于此处的分支
	repo, ok := findUIRepo(c.repos(), req.Repo)
	if !ok {
		writeUIRemote(w, http.StatusNotFound, uiRemoteResult{Error: l10n.T("Unknown repository", nil)})
		return
	}

	if target == "toggle" {
		resolved, err := resolveToggleTarget(r.Context(), repo.Path)
		if err != nil {
			writeUIRemote(w, http.StatusConflict, uiRemoteResult{Error: err.Error()})
			return
		}
		target = resolved
	}

	// 直接复用命令行那份实现：地址解析、协议判定、set-url 都在 doSwitchRemote 里，
	// 网页这一侧只负责取仓库与换算 toggle，不存在第二套改写规则
	res := doSwitchRemote(r.Context(), repo.Path, target)
	if res.status == "error" {
		writeUIRemote(w, http.StatusConflict, uiRemoteResult{Error: res.err})
		return
	}

	// 刻意不调用 c.invalidate()：协议只改 .git/config 里的一行，页面显示的字段
	// （分支、变更文件、领先落后）都不受影响，为一次点击重采全部仓库并不划算。
	// 将来若页面上出现“协议”这一列，这里必须补上失效，否则那一列会一直显示旧值
	out := uiRemoteResult{OldURL: res.oldURL, NewURL: res.newURL, Switched: res.status == "switched"}
	if out.Switched {
		out.Output = l10n.T("Remote origin switched: {{.Old}} → {{.New}}", map[string]any{
			"Old": res.oldURL, "New": res.newURL,
		})
	} else {
		out.Output = l10n.T("Already using {{.Proto}}, nothing to do", map[string]any{
			"Proto": detectProtocol(res.oldURL),
		})
	}
	writeUIRemote(w, http.StatusOK, out)
}

// writeUIRemote 输出协议切换端点的响应。与其它端点一样关掉 HTML 转义：
// 取值里是 URL，转义后排查接口时看到的 JSON 不便阅读
func writeUIRemote(w http.ResponseWriter, status int, out uiRemoteResult) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		// 响应头已发出，改不了状态码，只能记日志
		logger.Error(l10n.T("Failed to encode the remote response", nil), "error", err)
	}
}
