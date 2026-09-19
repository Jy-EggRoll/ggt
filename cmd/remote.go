package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"ggt/internal/git"
	"ggt/internal/i18n"
	"ggt/internal/worker"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

var remoteAll bool

// remoteCmd 实现 "ggt remote"（含子命令 https / ssh）。
// 在 HTTPS 和 SSH 协议之间切换远程 origin 的地址。
// 支持当前目录单仓库模式（默认）和 --all 批量模式。
//
// SSH → HTTPS 示例：
//
//	git@github.com:user/repo.git  →  https://github.com/user/repo.git
//
// HTTPS → SSH 示例：
//
//	https://github.com/user/repo.git  →  git@github.com:user/repo.git
func newRemoteCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "remote",
		Short: i18n.T("Switch the remote protocol (HTTPS ↔ SSH)", nil),
		Long: i18n.T(`Switch a git repository's remote origin between HTTPS and SSH.

By default this operates on the git repository in the current directory;
pass --all to switch every configured repository.

Examples:
  ggt remote https        Switch the current repository to HTTPS
  ggt remote ssh          Switch the current repository to SSH
  ggt remote https --all  Switch all repositories to HTTPS`, nil),
	}
	c.AddCommand(newRemoteHttpsCmd(), newRemoteSshCmd(), newRemoteToggleCmd())
	c.PersistentFlags().BoolVarP(&remoteAll, "all", "a", false,
		i18n.T("Switch the remote protocol of all configured repositories", nil))
	return c
}

func newRemoteHttpsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "https",
		Short: i18n.T("Switch the remote origin to HTTPS", nil),
		Run: func(cmd *cobra.Command, args []string) {
			if remoteAll {
				switchAllRepos("https")
			} else {
				switchCurrentRepo("https")
			}
		},
	}
	return c
}

func newRemoteSshCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "ssh",
		Short: i18n.T("Switch the remote origin to SSH", nil),
		Run: func(cmd *cobra.Command, args []string) {
			if remoteAll {
				switchAllRepos("ssh")
			} else {
				switchCurrentRepo("ssh")
			}
		},
	}
	return c
}

// remoteToggleCmd 实现 "ggt remote toggle"：在当前仓库的 HTTPS 与 SSH 协议之间取反切换。
// 仅操作当前目录下的仓库，不支持 --all（与 https/ssh 子命令的批量模式区分）。
func newRemoteToggleCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "toggle",
		Short: i18n.T("Toggle between HTTPS and SSH in the current repository", nil),
		Long: i18n.T(`Toggle the current repository's remote protocol between HTTPS and SSH.

Unlike the https/ssh subcommands, no target protocol is needed: it switches to
whichever protocol the origin is not currently using.
It only affects the git repository in the current directory and does not support --all.

Examples:
  ggt remote toggle       Toggle the current repository between HTTPS and SSH`, nil),
		Run: func(cmd *cobra.Command, args []string) {
			toggleCurrentRepo()
		},
	}
	return c
}

// remoteURLRegex 匹配主流托管平台的远程 URL，提取 host 和 path 部分。
// 支持三种格式：
//   - https://HOST/PATH.git
//   - http://HOST/PATH.git
//   - git@HOST:PATH.git
var remoteURLRegex = regexp.MustCompile(`^(?:https?://|git@)([^:/]+)[:/](.+?)(?:\.git)?(?:/)?$`)

const remoteOrigin = "origin"

// remoteURL 正则子分组索引。
const (
	remoteURLIdxFull  = iota // 完整匹配
	remoteURLIdxHost         // host 部分
	remoteURLIdxPath         // path 部分
	remoteURLIdxCount        // 分组总数（用于长度校验）
)

// remoteInfo 保存从远程 URL 中解析出的托管平台和仓库路径。
type remoteInfo struct {
	host string
	path string
}

// parseRemoteURL 从原始的 git remote URL 中提取 host 和 path。
func parseRemoteURL(raw string) (*remoteInfo, error) {
	matches := remoteURLRegex.FindStringSubmatch(strings.TrimSpace(raw))
	if len(matches) < remoteURLIdxCount {
		return nil, errors.New(i18n.T("Cannot parse the URL; only major hosting platforms (GitHub/GitLab/Gitee, etc.) are supported", nil))
	}
	return &remoteInfo{host: matches[remoteURLIdxHost], path: matches[remoteURLIdxPath]}, nil
}

// buildHTTPSURL 根据 host + path 构建 HTTPS 格式的 URL。
func buildHTTPSURL(info *remoteInfo) string {
	return fmt.Sprintf("https://%s/%s.git", info.host, info.path)
}

// buildSSHURL 根据 host + path 构建 SSH 格式的 URL。
func buildSSHURL(info *remoteInfo) string {
	return fmt.Sprintf("git@%s:%s.git", info.host, info.path)
}

// detectProtocol 检测当前远程 URL 使用的协议（大小写不敏感）。
// 此前用 HasPrefix(raw, "http") 做大小写敏感判断，而 targetProto 传入的是大写
// "HTTPS"/"SSH"，导致 detectProtocol("HTTPS") 匹配不到 "http" 前缀、被误判为 SSH，
// 出现 "SSH → SSH" 这类与实际切换方向相反的显示错误。统一转小写后再判定即可修复。
func detectProtocol(raw string) string {
	if strings.HasPrefix(strings.ToLower(raw), "http") {
		return "HTTPS"
	}
	return "SSH"
}

// switchCurrentRepo 切换当前目录下的 git 仓库（含其已初始化子模块）的远程协议。
// 子模块在 ggt 中被视为一等仓库，这里通过 expand 把当前仓库及其子模块一并展开处理，
// 使 toggle / https / ssh 都能辐射到子模块（与 --all 行为一致）。
func switchCurrentRepo(target string) {
	wd, err := os.Getwd()
	if err != nil {
		ErrorMsg(i18n.T("Failed to get the current directory: {{.Err}}", map[string]any{"Err": err}))
		return
	}

	if !git.IsRepo(wd) {
		ErrorMsg(i18n.T("The current directory is not a git repository: {{.Path}}", map[string]any{"Path": wd}))
		return
	}

	entries := ExpandRepos(context.Background(), []string{wd})
	processSwitchResults(entries, target)
}

// toggleCurrentRepo 在当前仓库（含子模块）的 HTTPS 与 SSH 协议之间取反切换。
// 先读取当前仓库 origin 当前协议，再切换为相反协议；子模块复用同一目标协议。
func toggleCurrentRepo() {
	wd, err := os.Getwd()
	if err != nil {
		ErrorMsg(i18n.T("Failed to get the current directory: {{.Err}}", map[string]any{"Err": err}))
		return
	}

	if !git.IsRepo(wd) {
		ErrorMsg(i18n.T("The current directory is not a git repository: {{.Path}}", map[string]any{"Path": wd}))
		return
	}

	raw, err := git.RunContext(context.Background(), wd, "remote", "get-url", remoteOrigin)
	if err != nil {
		ErrorMsg(i18n.T("Failed to get the remote URL: {{.Err}}", map[string]any{"Err": err}))
		return
	}

	// detectProtocol 返回大写的 "HTTPS" 或 "SSH"，据此计算相反协议
	current := detectProtocol(strings.TrimSpace(raw))
	target := "https"
	if current == "HTTPS" {
		target = "ssh"
	}

	entries := ExpandRepos(context.Background(), []string{wd})
	processSwitchResults(entries, target)
}

// switchAllRepos 切换所有配置仓库（含子模块，受 ignore_submodules 控制）的远程协议。
// 使用 worker.Map 并发收集结果后顺序打印统计信息。
func switchAllRepos(target string) {
	entries := AllRepos(context.Background())
	InfoLn(i18n.T("Repositories: {{.Count}} — switching protocol to {{.Proto}}...",
		map[string]any{"Count": len(entries), "Proto": pterm.Cyan(strings.ToUpper(target))}))
	processSwitchResults(entries, target)
}

// processSwitchResults 对一组仓库条目并发执行远程协议切换，统一打印结果并汇总。
// 子模块作为独立条目参与，打印时经 RepoLabel 呈现 [子] 前缀，计数把子模块一并计入。
func processSwitchResults(entries []RepoEntry, target string) {
	targetProto := detectProtocol(strings.ToUpper(target))
	type switchOutcome struct {
		name        string
		isSubmodule bool
		res         switchRemoteResult
	}

	t := NewDebugTimer(i18n.T("Protocol switch (repositories: {{.Count}})", map[string]any{"Count": len(entries)}))
	results := worker.Map(context.Background(), entries, Concurrency(), func(ctx context.Context, e RepoEntry) switchOutcome {
		r := doSwitchRemote(ctx, e.Path, target)
		r.name = e.Name
		r.isSubmodule = e.IsSubmodule
		return switchOutcome{e.Name, e.IsSubmodule, r}
	})
	t.Done()

	var success, skipped, failed int
	for _, r := range results {
		label := RepoLabel(r.name, r.isSubmodule)
		switch r.res.status {
		case "switched":
			PrintProtocolSwitch(r.name, r.isSubmodule, detectProtocol(r.res.oldURL), targetProto)
			success++
		case "same":
			InfoMsg(i18n.T("{{.Label}} is already using {{.Proto}}, nothing to do",
				map[string]any{"Label": label, "Proto": detectProtocol(r.res.oldURL)}))
			skipped++
		case "error":
			ErrorMsg(i18n.T("{{.Label}} {{.Err}}", map[string]any{"Label": label, "Err": r.res.err}))
			failed++
		}
	}

	pterm.Println()
	InfoMsg(i18n.T("Finished: {{.Success}} switched, {{.Skipped}} skipped, {{.Failed}} failed",
		map[string]any{"Success": success, "Skipped": skipped, "Failed": failed}))
}

// switchRemoteResult 保存单个仓库（含子模块）远程协议切换的结果。
// name / isSubmodule 由外层遍历填充，用于打印时呈现 [子] 标识。
type switchRemoteResult struct {
	name        string
	isSubmodule bool
	oldURL      string
	newURL      string
	status      string // "switched", "same", "error"
	err         string
}

// doSwitchRemote 执行单个仓库（或子模块）的远程协议切换。
// 流程：获取当前 URL → 解析 host+path → 构建新 URL → git remote set-url。
// name/isSubmodule 由调用方填充，本函数只关注路径与协议。
// 接收上层 ctx 以便任务被整体取消时立即中断 git 调用。
func doSwitchRemote(ctx context.Context, repoPath string, target string) switchRemoteResult {
	raw, err := git.RunContext(ctx, repoPath, "remote", "get-url", remoteOrigin)
	if err != nil {
		return switchRemoteResult{status: "error", err: i18n.T("Failed to get the remote URL: {{.Err}}", map[string]any{"Err": err})}
	}

	oldURL := strings.TrimSpace(raw)
	info, err := parseRemoteURL(oldURL)
	if err != nil {
		return switchRemoteResult{status: "error", err: err.Error()}
	}

	currentProto := detectProtocol(oldURL)
	targetUpper := strings.ToUpper(target)

	if currentProto == targetUpper {
		return switchRemoteResult{status: "same", oldURL: oldURL}
	}

	var newURL string
	if targetUpper == "HTTPS" {
		newURL = buildHTTPSURL(info)
	} else {
		newURL = buildSSHURL(info)
	}

	_, err = git.RunContext(ctx, repoPath, "remote", "set-url", remoteOrigin, newURL)
	if err != nil {
		return switchRemoteResult{status: "error", err: i18n.T("Switch failed: {{.Err}}", map[string]any{"Err": err})}
	}

	return switchRemoteResult{status: "switched", oldURL: oldURL, newURL: newURL}
}

func init() {
	register(func(root *cobra.Command) { root.AddCommand(newRemoteCmd()) })
}
