package cmd

import (
	"fmt"

	"ggt/internal/git"
	"ggt/internal/worker"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

// statusCmd 实现 "ggt status"（简写 ggt st）。
// 并发检查所有配置仓库的 git 状态（未跟踪文件、修改、分支信息）。
//
// 输出安全：使用 worker.Map 并发收集结果 → 主 goroutine 顺序打印，
// 避免多个仓库的输出行互相插入。
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "显示所有仓库的 git 状态",
	Long: `遍历所有已配置的仓库，显示每个仓库的 git 状态。
	
使用示例:
  ggt status          显示所有仓库状态
  ggt st             简写形式`,
	Run: func(cmd *cobra.Command, args []string) {
		repos := MustGetRepoList()
		pterm.Info.Printf("共 %d 个仓库，开始检查状态...\n\n", len(repos))

		results := worker.Map(repos, GetConfig().Concurrency, showRepoStatus)
		for _, r := range results {
			fmt.Print(r)
		}
	},
}

// showRepoStatus 检查单个仓库的 git 状态并返回格式化字符串。
// 使用 --short --branch --untracked-files 选项输出紧凑状态。
func showRepoStatus(repoPath string) string {
	output, err := git.Run(repoPath, "status", "--short", "--branch", "--untracked-files")
	if err != nil {
		return pterm.Warning.Sprintf("仓库 %s: 执行失败\n", repoPath)
	}

	name := getRepoName(repoPath)
	// 如果输出为空（极少出现，因为 --branch 至少输出分支行），表示完全干净
	if output == "" {
		return pterm.FgGreen.Sprintf("[%s] ", name) + "已就绪\n"
	}
	return pterm.FgYellow.Sprintf("[%s]\n", name) + output
}

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Aliases = []string{"st"}
}
