package cmd

import (
	"context"
	"fmt"

	"ggt/internal/git"
	"ggt/internal/i18n"
	"ggt/internal/worker"
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
		Short: i18n.T("Show the git status of all repositories", nil),
		Long: i18n.T(`Iterate over all configured repositories and show the git status of each.

Examples:
  ggt status          Show the status of all repositories
  ggt st              Short form`, nil),
		Run: func(cmd *cobra.Command, args []string) {
			repos := MustGetAllRepos(context.Background(), GetConfig().IgnoreSubmodules)
			// 保留常量格式串 "%s\n" 以维持改造前的尾部空行（pterm 的 Println 会折叠结尾换行）
			Infof("%s\n", i18n.T("Repositories: {{.Count}} — checking status...", map[string]any{"Count": len(repos)}))

			t := NewDebugTimer(i18n.T("Status check (repositories: {{.Count}})", map[string]any{"Count": len(repos)}))
			results := worker.Map(context.Background(), repos, GetConfig().ConcurrencyValue(), showRepoStatus)
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
		// WarnStr 是纯文本通道；入参以 \n 结尾时 pterm 会折叠为单个换行，
		// 与改造前 WarnS(format, args) 的输出一致
		return WarnStr(i18n.T("Repository {{.Path}}: git failed - {{.Err}}",
			map[string]any{"Path": e.Path, "Err": err}) + "\n")
	}

	label := RepoLabel(e.Name, e.IsSubmodule)
	// 如果输出为空（极少出现，因为 --branch 至少输出分支行），表示完全干净
	if output == "" {
		return label + " " + i18n.T("ready", nil) + "\n"
	}
	// status 输出自带末尾换行，直接拼接即可，无需额外空行
	return label + "\n" + output
}

func init() {
	register(func(root *cobra.Command) { root.AddCommand(newStatusCmd()) })
}
