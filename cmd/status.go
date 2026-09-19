package cmd

import (
	"context"
	"fmt"

	"ggt/internal/git"
	"ggt/internal/worker"
	"ggt/pkg/l10n"
	"github.com/spf13/cobra"
)

// statusCmd 实现 "ggt status"（简写 ggt st）。
// 并发检查所有配置仓库的 git 状态（未跟踪文件、修改、分支信息）。
//
// 输出安全：使用 worker.Map 并发收集结果 → 主 goroutine 顺序打印，
// 避免多个仓库的输出行互相插入。
func newStatusCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "status",
		Short: l10n.T("Show the git status of all repositories", nil),
		Long: l10n.T(`Iterate over all configured repositories and show the git status of each.

Examples:
  ggt status          Show the status of all repositories
  ggt st              Short form`, nil),
		Run: func(cmd *cobra.Command, args []string) {
			repos := AllRepos(context.Background())
			// 保留常量格式串 "%s\n" 以维持改造前的尾部空行（pterm 的 Println 会折叠结尾换行）
			InfoLn(l10n.T("Repositories: {{.Count}} — checking status...", map[string]any{"Count": len(repos)}))

			t := NewDebugTimer(l10n.T("Status check (repositories: {{.Count}})", map[string]any{"Count": len(repos)}))
			results := worker.Map(context.Background(), repos, Concurrency(), showRepoStatus)
			t.Done()

			for _, r := range results {
				fmt.Print(r)
			}
		},
	}
	c.Aliases = []string{"st"}
	return c
}

// showRepoStatus 检查单个仓库（含子模块）的 git 状态并返回格式化字符串。
// 使用 --short --branch --untracked-files 选项输出紧凑状态。
// 接收上层 ctx 以便任务被整体取消时立即中断 git 调用。
func showRepoStatus(ctx context.Context, e RepoEntry) string {
	output, err := git.RunContext(ctx, e.Path, "status", "--short", "--branch", "--untracked-files")
	if err != nil {
		// 入参以 \n 结尾：pterm 会把结尾换行折叠为单个换行，于是这行就是普通的单行告警
		return WarnStrLn(l10n.T("Repository {{.Path}}: git failed - {{.Err}}",
			map[string]any{"Path": e.Path, "Err": err}))
	}

	label := RepoLabel(e.Name, e.IsSubmodule)
	// 如果输出为空（极少出现，因为 --branch 至少输出分支行），表示完全干净
	if output == "" {
		return label + " " + l10n.T("ready", nil) + "\n"
	}
	// status 输出自带末尾换行，直接拼接即可，无需额外空行
	return label + "\n" + output
}

func init() {
	register(func(root *cobra.Command) { root.AddCommand(newStatusCmd()) })
}
