package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"golang.org/x/sync/semaphore"
)

var summaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "遍历所有仓库，显示变更，询问一键提交",
	Long: `遍历所有仓库，显示变更，询问一键提交。
	
使用示例:
  ggt summary          查看变更并提交
  ggt sum             简写形式`,
	Run: func(cmd *cobra.Command, args []string) {
		repos := MustGetRepoList()
		pterm.Info.Printf("共 %d 个仓库，开始检查变更...\n\n", len(repos))

		cfg := GetConfig()

		hasChanges := false
		changelog := make([]string, 0)

		for _, repoPath := range repos {
			changes := checkRepoChanges(repoPath)
			if len(changes) > 0 {
				hasChanges = true
				changelog = append(changelog, repoPath)
				printRepoChanges(repoPath, changes)
			}
		}

		if !hasChanges {
			SuccessMsg("所有仓库已是最新状态")
			return
		}

		pterm.Println()
		if !Confirm("是否一键提交所有更改并推送？") {
			pterm.Info.Println("已取消")
			return
		}

		pterm.Info.Println("开始提交并推送...")

		sem := semaphore.NewWeighted(int64(cfg.Concurrency))
		for _, repoPath := range changelog {
			if err := sem.Acquire(cmd.Context(), 1); err != nil {
				break
			}
			go func(path string) {
				defer sem.Release(1)
				commitAndPushRepo(path)
			}(repoPath)
		}

		sem.Acquire(cmd.Context(), int64(cfg.Concurrency))
	},
}

func checkRepoChanges(repoPath string) []string {
	output, err := runGitCommand(repoPath, "status", "--short", "--branch", "--untracked-files")
	if err != nil {
		return nil
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return nil
	}

	var changes []string
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			changes = append(changes, line)
		}
	}
	return changes
}

func printRepoChanges(repoPath string, changes []string) {
	name := getRepoName(repoPath)
	pterm.FgYellow.Printf("[%s] 检测到 %d 个变更\n", name, len(changes))
	for _, change := range changes {
		pterm.FgCyan.Printf("  %s\n", change)
	}
}

func commitAndPushRepo(repoPath string) {
	pterm.FgYellow.Printf("正在处理 %s...\n", getRepoName(repoPath))

	_, err := runGitCommand(repoPath, "add", "-A")
	if err != nil {
		ErrorMsg(fmt.Sprintf("git add 失败: %s", err))
		return
	}

	msg := fmt.Sprintf("🔨 chore: 终端推送更新 %s", time.Now().Format("2006-01-02 15:04:05"))
	_, err = runGitCommand(repoPath, "commit", "-m", msg)
	if err != nil {
		ErrorMsg(fmt.Sprintf("git commit 失败: %s", err))
		return
	}

	_, err = runGitCommand(repoPath, "push")
	if err != nil {
		ErrorMsg(fmt.Sprintf("git push 失败: %s", err))
		return
	}

	SuccessMsg(fmt.Sprintf("提交并推送完成: %s", getRepoName(repoPath)))

	output, _ := runGitCommand(repoPath, "count-objects", "-vH")
	if output != "" {
		pterm.FgCyan.Printf("  %s\n", getRepoName(repoPath))
		pterm.Println(output)
	}
}

func init() {
	rootCmd.AddCommand(summaryCmd)
	summaryCmd.Aliases = []string{"sum"}
}
