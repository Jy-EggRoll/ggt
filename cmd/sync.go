package cmd

import (
	"fmt"
	"strings"
	"sync"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "遍历所有仓库，自动同步",
	Long: `遍历所有仓库，自动同步。

- 先执行 git fetch --all --prune
- 比较本地、远程、共同祖先的 commit hash
- 根据情况执行:
  - 本地与远程一致 → 跳过
  - 本地落后于远程（线性更新）→ git pull --ff-only
  - 本地领先于远程 → 跳过（提示用户手动推送）
  - 非线性更新 → 提示用户手动干预
	
使用示例:
  ggt sync          自动同步所有仓库`,
	Run: func(cmd *cobra.Command, args []string) {
		repos := MustGetRepoList()
		pterm.Info.Println("共 " + pterm.Cyan(len(repos)) + " 个仓库，开始同步...")

		cfg := GetConfig()
		var wg sync.WaitGroup
		sem := make(chan struct{}, cfg.Concurrency)

		for _, repoPath := range repos {
			wg.Add(1)
			go func(path string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				syncRepo(path)
			}(repoPath)
		}

		wg.Wait()
		pterm.Success.Println("所有仓库同步完成")
	},
}

func syncRepo(repoPath string) {
	name := getRepoName(repoPath)

	// fetch all
	_, err := runGitCommand(repoPath, "fetch", "--all", "--prune")
	if err != nil {
		WarnMsg(fmt.Sprintf("[%s] fetch 失败: %s", name, err))
		return
	}

	// check if working directory is clean
	status, err := runGitCommand(repoPath, "status", "--porcelain")
	if err != nil {
		WarnMsg(fmt.Sprintf("[%s] 检查状态失败: %s", name, err))
		return
	}

	if strings.TrimSpace(status) != "" {
		WarnMsg(fmt.Sprintf("[%s] 本地有未提交的更改，必须手动处理", name))
		return
	}

	// get local, remote, and base commit hashes
	local, err := runGitCommand(repoPath, "rev-parse", "HEAD")
	if err != nil {
		WarnMsg(fmt.Sprintf("[%s] 获取本地 HEAD 失败", name))
		return
	}
	local = strings.TrimSpace(local)

	remote, err := runGitCommand(repoPath, "rev-parse", "@{upstream}")
	if err != nil {
		WarnMsg(fmt.Sprintf("[%s] 获取远程分支失败", name))
		return
	}
	remote = strings.TrimSpace(remote)

	base, err := runGitCommand(repoPath, "merge-base", "HEAD", "@{upstream}")
	if err != nil {
		WarnMsg(fmt.Sprintf("[%s] 获取共同祖先失败", name))
		return
	}
	base = strings.TrimSpace(base)

	// compare and decide
	if local == remote {
		InfoMsg(fmt.Sprintf("[%s] 本地与远程一致，无需处理", name))
	} else if local == base {
		WarnMsg(fmt.Sprintf("[%s] 检测到线性更新，正在拉取...", name))
		_, err := runGitCommand(repoPath, "pull", "--ff-only")
		if err != nil {
			ErrorMsg(fmt.Sprintf("[%s] 拉取失败: %s", name, err))
		} else {
			SuccessMsg(fmt.Sprintf("[%s] 拉取成功", name))
		}
	} else if remote == base {
		WarnMsg(fmt.Sprintf("[%s] 本地领先于远程，请手动推送", name))
	} else {
		ErrorMsg(fmt.Sprintf("[%s] 非线性更新，必须手动处理", name))
	}
}

func init() {
	rootCmd.AddCommand(syncCmd)
}
