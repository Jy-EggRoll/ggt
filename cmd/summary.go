package cmd

import (
	"context"
	"strings"
	"time"

	"ggt/internal/git"
	"ggt/internal/i18n"
	"ggt/internal/worker"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

// dirtyRepo 记录检测到变更有待处理的仓库。
// hasUncommitted 标记是否存在未提交的文件变更（false 表示仅是本地 ahead 未 push）。
type dirtyRepo struct {
	path           string
	name           string
	isSubmodule    bool
	statusOutput   string
	hasUncommitted bool
}

// summaryCmd 实现 "ggt summary"（简写 ggt sum）。
// 先并发检查所有仓库的状态，再对有变更的仓库进行交互式提交流程。
func newSummaryCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "summary",
		Short: i18n.T("Iterate over all repositories, show changes, and offer to commit", nil),
		Long: i18n.T(`Iterate over all repositories, show the changes, and offer to commit them.

Examples:
  ggt summary          Review changes and commit
  ggt sum              Short form`, nil),
		Run: func(cmd *cobra.Command, args []string) {
			// 统一 ctx：第一阶段并发检查与第二阶段交互式操作（diff/count-objects/add/commit/push）
			// 都复用同一 ctx，确保这些耗时 git 调用也受全局超时与取消约束，不再用无 ctx 的 git.Run。
			ctx := context.Background()
			repos := MustGetAllRepos(ctx, GetConfig().IgnoreSubmodules)
			Infof("%s\n", i18n.T("Repositories: {{.Count}} — checking for changes...", map[string]any{"Count": len(repos)}))

			// 第一阶段：并发检查所有仓库（含子模块）的 git 状态
			t := NewDebugTimer(i18n.T("Status check (repositories: {{.Count}})", map[string]any{"Count": len(repos)}))
			results := worker.Map(ctx, repos, GetConfig().ConcurrencyValue(), func(ctx context.Context, e RepoEntry) *dirtyRepo {
				statusOutput, err := git.RunContext(ctx, e.Path, "-c", "color.status=always", "status", "--short", "--branch", "--untracked-files")
				if err != nil {
					return nil
				}

				lines := strings.Split(strings.TrimRight(statusOutput, "\n"), "\n")
				nonEmptyCount := 0
				for _, line := range lines {
					if strings.TrimSpace(line) != "" {
						nonEmptyCount++
					}
				}
				// 只有分支行说明没有文件变更，但需检查是否为 ahead（已 commit 未 push）
				if nonEmptyCount <= 1 {
					if !strings.Contains(statusOutput, "[ahead") {
						return nil
					}
					// 仅有 ahead，无未提交的文件变更
					return &dirtyRepo{
						path:           e.Path,
						name:           e.Name,
						isSubmodule:    e.IsSubmodule,
						statusOutput:   statusOutput,
						hasUncommitted: false,
					}
				}

				return &dirtyRepo{
					path:           e.Path,
					name:           e.Name,
					isSubmodule:    e.IsSubmodule,
					statusOutput:   statusOutput,
					hasUncommitted: true,
				}
			})
			t.Done()

			// 第二阶段：顺序处理每个有变更的仓库（交互+提交需等待用户输入）
			for _, d := range results {
				if d == nil {
					continue
				}

				PrintSeparator()
				RepoLine(d.name, i18n.T("changes detected", nil), d.isSubmodule)
				PrintRaw(d.statusOutput)

				// 显示详细的 diff 统计
				pterm.Println(Muted(i18n.T("Change details:", nil)))
				diffOutput, err := git.RunContext(ctx, d.path, "diff", "--color=always", "--stat")
				if err != nil {
					WarnMsg(i18n.T("Failed to get the diff: {{.Err}}", map[string]any{"Err": err}))
				} else if diffOutput != "" {
					PrintRaw(diffOutput)
				}

				// 交互式确认：根据仓库状态动态调整提示文案
				promptText := i18n.T("Commit all changes and push?", nil)
				if !d.hasUncommitted {
					promptText = i18n.T("Push the committed changes?", nil)
				}
				result, _ := pterm.DefaultInteractiveConfirm.WithDefaultValue(false).WithDefaultText(promptText).Show()
				if !result {
					continue
				}

				InfoMsg(i18n.T("Processing {{.Name}} ...", map[string]any{"Name": d.name}))

				// 仅在存在未提交的文件变更时执行 add + commit
				if d.hasUncommitted {
					// git add -A：暂存所有更改
					if out, err := git.RunCombinedContext(ctx, d.path, "add", "-A"); err != nil {
						Errorf("%s\n%s", i18n.T("git add failed:", nil), out)
						continue
					}

					// git commit：自动生成提交信息
					msg := i18n.T("chore: automated terminal update {{.Time}}",
						map[string]any{"Time": time.Now().Format("2006-01-02 15:04:05")})
					if out, err := git.RunCombinedContext(ctx, d.path, "commit", "-m", msg); err != nil {
						Errorf("%s\n%s", i18n.T("git commit failed:", nil), out)
						continue
					}
				}

				// git push：推送到远程，使用 RunCombinedContext 确保捕获 stderr 错误信息
				if out, err := git.RunCombinedContext(ctx, d.path, "push"); err != nil {
					Errorf("%s\n%s", i18n.T("push failed:", nil), out)
				} else {
					SuccessMsg(i18n.T("Push complete!", nil))
				}

				// 显示提交后的仓库大小信息
				pterm.Println(Muted(i18n.T("Size information:", nil)))
				countOutput, err := git.RunContext(ctx, d.path, "count-objects", "-vH")
				if err != nil {
					WarnMsg(i18n.T("Failed to get size information: {{.Err}}", map[string]any{"Err": err}))
				} else if countOutput != "" {
					PrintRaw(countOutput)
				}
			}
		},
	}
	c.Aliases = []string{"sum"}
	return c
}

func init() {
	register(func(root *cobra.Command) { root.AddCommand(newSummaryCmd()) })
}
