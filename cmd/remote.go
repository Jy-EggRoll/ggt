package cmd

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"ggt/internal/git"
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
var remoteCmd = &cobra.Command{
	Use:   "remote",
	Short: "切换远程仓库协议 (HTTPS ↔ SSH)",
	Long: `切换 git 仓库的远程 origin 协议，支持在 HTTPS 和 SSH 之间互相切换。

默认操作当前目录下的 git 仓库，使用 --all 可切换所有已配置仓库。

使用示例:
  ggt remote https        将当前仓库切换为 HTTPS
  ggt remote ssh          将当前仓库切换为 SSH
  ggt remote https --all  将所有仓库切换为 HTTPS`,
}

var remoteHttpsCmd = &cobra.Command{
	Use:   "https",
	Short: "将远程 origin 切换为 HTTPS 协议",
	Run: func(cmd *cobra.Command, args []string) {
		if remoteAll {
			switchAllRepos("https")
		} else {
			switchCurrentRepo("https")
		}
	},
}

var remoteSshCmd = &cobra.Command{
	Use:   "ssh",
	Short: "将远程 origin 切换为 SSH 协议",
	Run: func(cmd *cobra.Command, args []string) {
		if remoteAll {
			switchAllRepos("ssh")
		} else {
			switchCurrentRepo("ssh")
		}
	},
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
		return nil, fmt.Errorf("无法解析 URL，仅支持主流托管平台 (GitHub/GitLab/Gitee 等)")
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

// detectProtocol 检测当前远程 URL 使用的协议。
func detectProtocol(raw string) string {
	if strings.HasPrefix(raw, "http") {
		return "HTTPS"
	}
	return "SSH"
}

// switchCurrentRepo 切换当前目录下的 git 仓库的远程协议。
// 此模式仅操作一个仓库，直接打印结果。
func switchCurrentRepo(target string) {
	wd, err := os.Getwd()
	if err != nil {
		ErrorMsg("获取当前目录失败: " + err.Error())
		return
	}

	if !isGitRepo(wd) {
		ErrorMsg("当前目录不是 git 仓库: " + wd)
		return
	}

	result := doSwitchRemote(context.Background(), wd, target)
	printRemoteResult(result)
}

// switchAllRepos 切换所有配置仓库的远程协议。
// 使用 worker.Map 并发收集结果后顺序打印统计信息。
func switchAllRepos(target string) {
	repos := MustGetRepoList()
	Infof("共 %d 个仓库，开始切换协议至 [%s] ...\n", len(repos), pterm.Cyan(strings.ToUpper(target)))

	type repoResult struct {
		name   string
		result switchRemoteResult
	}

	results := worker.Map(context.Background(), repos, GetConfig().Concurrency, func(ctx context.Context, path string) repoResult {
		return repoResult{name: getRepoName(path), result: doSwitchRemote(ctx, path, target)}
	})

	var success, skipped, failed int
	for _, r := range results {
		name := pterm.FgCyan.Sprintf("[%s]", r.name)
		switch r.result.status {
		case "switched":
			pterm.Success.Printfln("%s %s → %s", name,
				pterm.FgRed.Sprint(detectProtocol(r.result.oldURL)),
				pterm.FgGreen.Sprint(strings.ToUpper(target)))
			success++
		case "same":
			pterm.Info.Printfln("%s 已是 %s 协议，无需切换", name,
				pterm.Cyan(detectProtocol(r.result.oldURL)))
			skipped++
		case "error":
			Errorf("%s %s", name, r.result.err)
			failed++
		}
	}

	pterm.Println()
	Infof("处理完成: 成功 %d, 跳过 %d, 失败 %d", success, skipped, failed)
}

// switchRemoteResult 保存远程协议切换的结果。
type switchRemoteResult struct {
	oldURL string
	newURL string
	status string // "switched", "same", "error"
	err    string
}

// doSwitchRemote 执行单个仓库的远程协议切换。
// 流程：获取当前 URL → 解析 host+path → 构建新 URL → git remote set-url。
// 接收上层 ctx 以便任务被整体取消时立即中断 git 调用。
func doSwitchRemote(ctx context.Context, repoPath string, target string) switchRemoteResult {
	raw, err := git.RunContext(ctx, repoPath, "remote", "get-url", remoteOrigin)
	if err != nil {
		return switchRemoteResult{status: "error", err: fmt.Sprintf("获取远程地址失败: %v", err)}
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
		return switchRemoteResult{status: "error", err: fmt.Sprintf("切换失败: %v", err)}
	}

	return switchRemoteResult{status: "switched", oldURL: oldURL, newURL: newURL}
}

// printRemoteResult 打印单个仓库的切换结果（用于单仓库模式）。
func printRemoteResult(r switchRemoteResult) {
	switch r.status {
	case "switched":
		pterm.Success.Printf("已切换协议: %s → %s\n",
			pterm.FgRed.Sprint(detectProtocol(r.oldURL)),
			pterm.FgGreen.Sprint(detectProtocol(r.newURL)))
		pterm.FgYellow.Printfln("  └ 旧: %s", r.oldURL)
		pterm.FgYellow.Printfln("  └ 新: %s", r.newURL)
	case "same":
		pterm.Info.Printf("已是 %s 协议，无需切换\n", pterm.Cyan(detectProtocol(r.oldURL)))
	case "error":
		ErrorMsg(r.err)
	}
}

func init() {
	rootCmd.AddCommand(remoteCmd)
	remoteCmd.AddCommand(remoteHttpsCmd)
	remoteCmd.AddCommand(remoteSshCmd)
	remoteCmd.PersistentFlags().BoolVarP(&remoteAll, "all", "a", false, "切换所有已配置仓库的远程协议")
}
