package cmd

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

var remoteAll bool

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

var remoteURLRegex = regexp.MustCompile(`^(?:https://|git@)([^:/]+)[:/](.+?)(?:\.git)?(?:/)?$`)

const remoteOrigin = "origin"

type remoteInfo struct {
	host string
	path string
}

func parseRemoteURL(raw string) (*remoteInfo, error) {
	matches := remoteURLRegex.FindStringSubmatch(strings.TrimSpace(raw))
	if len(matches) < 3 {
		return nil, fmt.Errorf("无法解析 URL，仅支持主流托管平台 (GitHub/GitLab/Gitee 等)")
	}
	return &remoteInfo{host: matches[1], path: matches[2]}, nil
}

func buildHTTPSURL(info *remoteInfo) string {
	return fmt.Sprintf("https://%s/%s.git", info.host, info.path)
}

func buildSSHURL(info *remoteInfo) string {
	return fmt.Sprintf("git@%s:%s.git", info.host, info.path)
}

func detectProtocol(raw string) string {
	if strings.HasPrefix(raw, "http") {
		return "HTTPS"
	}
	return "SSH"
}

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

	result := doSwitchRemote(wd, target)
	printRemoteResult(result)
}

func switchAllRepos(target string) {
	repos := MustGetRepoList()
	pterm.Info.Printf("共 %d 个仓库，开始切换协议至 [%s] ...\n\n", len(repos), pterm.Cyan(strings.ToUpper(target)))

	cfg := GetConfig()
	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.Concurrency)

	type repoResult struct {
		name   string
		result switchRemoteResult
	}
	results := make(chan repoResult, len(repos))

	for _, repoPath := range repos {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			res := doSwitchRemote(path, target)
			results <- repoResult{name: getRepoName(path), result: res}
		}(repoPath)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var success, skipped, failed int
	for r := range results {
		name := pterm.FgCyan.Sprintf("[%s]", r.name)
		switch r.result.status {
		case "switched":
			pterm.Success.Printfln("%s %s → %s", name, pterm.FgRed.Sprint(detectProtocol(r.result.oldURL)), pterm.FgGreen.Sprint(strings.ToUpper(target)))
			success++
		case "same":
			pterm.Info.Printfln("%s 已是 %s 协议，无需切换", name, pterm.Cyan(detectProtocol(r.result.oldURL)))
			skipped++
		case "error":
			pterm.Error.Printfln("%s %s", name, r.result.err)
			failed++
		}
	}

	pterm.Println()
	pterm.Info.Printf("处理完成: 成功 %d, 跳过 %d, 失败 %d\n", success, skipped, failed)
}

type switchRemoteResult struct {
	oldURL string
	newURL string
	status string
	err    string
}

func doSwitchRemote(repoPath string, target string) switchRemoteResult {
	raw, err := runGitCommand(repoPath, "remote", "get-url", remoteOrigin)
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

	_, err = runGitCommand(repoPath, "remote", "set-url", remoteOrigin, newURL)
	if err != nil {
		return switchRemoteResult{status: "error", err: fmt.Sprintf("切换失败: %v", err)}
	}

	return switchRemoteResult{status: "switched", oldURL: oldURL, newURL: newURL}
}

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
