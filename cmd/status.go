package cmd

import (
	"context"
	"fmt"

	"ggt/internal/git"
	"ggt/internal/worker"
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
		repos := MustGetAllRepos(context.Background(), GetConfig().IgnoreSubmodules)
		Infof("共 %d 个仓库，开始检查状态...\n", len(repos))

		t := NewDebugTimer(fmt.Sprintf("状态检查 (%d 个仓库)", len(repos)))
		results := worker.Map(context.Background(), repos, GetConfig().ConcurrencyValue(), showRepoStatus)
		t.Done()

		for _, r := range results {
			fmt.Print(r)
		}
	},
}

// showRepoStatus 检查单个仓库（含子模块）的 git 状态并返回格式化字符串。
// 使用 --short --branch --untracked-files 选项输出紧凑状态。
// 接收上层 ctx 以便任务被整体取消时立即中断 git 调用。
func showRepoStatus(ctx context.Context, e RepoEntry) string {
	output, err := git.RunContext(ctx, e.Path, "status", "--short", "--branch", "--untracked-files")
	if err != nil {
		return WarnS("仓库 %s: git 执行失败 - %v\n", e.Path, err)
	}

	label := RepoLabel(e.Name, e.IsSubmodule)
	// 如果输出为空（极少出现，因为 --branch 至少输出分支行），表示完全干净
	if output == "" {
		return label + " 已就绪\n"
	}
	// status 输出自带末尾换行，直接拼接即可，无需额外空行
	return label + "\n" + output
}

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Aliases = []string{"st"}
}
