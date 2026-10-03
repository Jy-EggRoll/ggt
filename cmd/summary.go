package cmd

import (
	"context"
	"time"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/ggt/internal/git"
	"github.com/jy-eggroll/ggt/internal/worker"
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
		Short: l10n.T("Iterate over all repositories, show changes, and offer to commit", nil),
		Long: l10n.T(`Iterate over all repositories, show the changes, and offer to commit them.

Examples:
  ggt summary          Review changes and commit
  ggt sum              Short form`, nil),
		Run: func(cmd *cobra.Command, args []string) {
			// 统一 ctx：第一阶段并发检查与第二阶段交互式操作（diff/count-objects/add/commit/push）
			// 都复用同一 ctx，一旦它被取消，worker.Map 与正在执行的 git 调用会一起中断；
			// 这些调用一律走 git.RunContext / RunCombinedContext，未设截止时间时由 git 包叠加默认超时兜底
			ctx := context.Background()
			repos := AllRepos(ctx)
			InfoLn(l10n.T("Repositories: {{.Count}} — checking for changes...", map[string]any{"Count": len(repos)}))

			// 第一阶段：并发检查所有仓库（含子模块）的 git 状态
			t := NewDebugTimer(l10n.T("Status check (repositories: {{.Count}})", map[string]any{"Count": len(repos)}))
			results := worker.Map(ctx, repos, Concurrency(), func(ctx context.Context, e RepoEntry) *dirtyRepo {
				// 是否值得处理，一律由机器可读的 porcelain v2 状态判定。
				// 原实现是从给人看的 --short 文本里反推：统计非空行数判断"有没有文件变更"、
				// 用 strings.Contains(output, "[ahead") 判断"有没有待推送提交"。两处都依赖
				// git 的展示措辞与格式，一旦 git 改版、或用户配了 status.relativePaths /
				// color.status，判断就会静默失效——不报错，只是所有仓库的结论一起变成错的
				st, err := git.RunStatus(ctx, e.Path)
				if err != nil {
					return nil
				}
				// 既无未提交变更、也无待推送提交，无需人工介入
				if len(st.Files) == 0 && st.Ahead == 0 {
					return nil
				}

				// 展示仍然用 git 自己的彩色 --short 输出：它是给人看的格式，逐文件列出
				// XY 与路径，且着色由 git 决定，与本命令改造前、以及用户在其他场合见到的
				// status 完全一致。代价是"确实有变更"的仓库要多跑一次 git，而干净仓库已在
				// 上一行返回，因此这份开销只落在真正要处理的少数仓库上
				statusOutput, err := git.RunContext(ctx, e.Path, "-c", "color.status=always", "status", "--short", "--branch", "--untracked-files")
				if err != nil {
					// 判断已经成立，展示文本取不到时留空即可，不因此放弃这个仓库
					statusOutput = ""
				}

				return &dirtyRepo{
					path:           e.Path,
					name:           e.Name,
					isSubmodule:    e.IsSubmodule,
					statusOutput:   statusOutput,
					hasUncommitted: len(st.Files) > 0,
				}
			})
			t.Done()

			// 第二阶段：顺序处理每个有变更的仓库（交互+提交需等待用户输入）
			for _, d := range results {
				if d == nil {
					continue
				}

				PrintSeparator()
				RepoLine(d.name, l10n.T("changes detected", nil), d.isSubmodule)
				PrintRaw(d.statusOutput)

				// 显示详细的 diff 统计
				pterm.Println(Muted(l10n.T("Change details:", nil)))
				diffOutput, err := git.RunContext(ctx, d.path, "diff", "--color=always", "--stat")
				if err != nil {
					WarnMsg(l10n.T("Failed to get the diff: {{.Err}}", map[string]any{"Err": err}))
				} else if diffOutput != "" {
					PrintRaw(diffOutput)
				}

				// 交互式确认：根据仓库状态动态调整提示文案
				promptText := l10n.T("Commit all changes and push?", nil)
				if !d.hasUncommitted {
					promptText = l10n.T("Push the committed changes?", nil)
				}
				result, _ := pterm.DefaultInteractiveConfirm.WithDefaultValue(false).WithDefaultText(promptText).Show()
				if !result {
					continue
				}

				InfoMsg(l10n.T("Processing {{.Name}} ...", map[string]any{"Name": d.name}))

				// 仅在存在未提交的文件变更时执行 add + commit
				if d.hasUncommitted {
					// git add -A：暂存所有更改
					if out, err := git.RunCombinedContext(ctx, d.path, "add", "-A"); err != nil {
						ErrorDetail(l10n.T("git add failed:", nil), out)
						continue
					}

					// git commit：自动生成提交信息
					msg := l10n.T("chore: automated terminal update {{.Time}}",
						map[string]any{"Time": time.Now().Format("2006-01-02 15:04:05")})
					if out, err := git.RunCombinedContext(ctx, d.path, "commit", "-m", msg); err != nil {
						ErrorDetail(l10n.T("git commit failed:", nil), out)
						continue
					}
				}

				// git push：推送到远程，使用 RunCombinedContext 确保捕获 stderr 错误信息
				if out, err := git.RunCombinedContext(ctx, d.path, "push"); err != nil {
					ErrorDetail(l10n.T("push failed:", nil), out)
				} else {
					SuccessMsg(l10n.T("Push complete!", nil))
				}

				// 显示提交后的仓库大小信息
				pterm.Println(Muted(l10n.T("Size information:", nil)))
				countOutput, err := git.RunContext(ctx, d.path, "count-objects", "-vH")
				if err != nil {
					WarnMsg(l10n.T("Failed to get size information: {{.Err}}", map[string]any{"Err": err}))
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
