package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

// takeownResult 记录单个仓库（含子模块）的 takeown 处理结果。
type takeownResult struct {
	name        string
	isSubmodule bool
	err         error
}

// ownedCmd 实现 "ggt owned"。
// 调用 Windows takeown 命令获取仓库文件所有权。
// 严格遵循原有的 pwsh 脚本逻辑，处理主仓库目录、.git 目录。
// 子模块在 ggt 中被视为一等仓库，由统一的仓库发现（AllRepos）展开后
// 逐个作为独立条目交给 takeownRepo 处理，无需在此命令内编写子模块专属循环。
//
// 注意：此命令仅适用于 Windows 系统（入口处 runtime.GOOS 检测提前返回）。
func newOwnedCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "owned",
		Short: l10n.T("Take ownership of all repositories", nil),
		Long: l10n.T(`Take ownership of all repositories.

Mirrors the original PowerShell implementation.
Uses the takeown command to take ownership of each repository directory and
its .git directory. Submodules are treated as standalone repositories.

Examples:
  ggt owned          Take ownership of all repositories`, nil),
		Run: func(cmd *cobra.Command, args []string) {
			if runtime.GOOS != "windows" {
				WarnMsg(l10n.T("ggt owned is only supported on Windows", nil))
				return
			}
			ctx := cmd.Context()
			repos := AllRepos(ctx)
			// 用 InfoLn 而非 InfoMsg：Ln 系列结尾会多留一个空行（"%s\n" 的细节统一在 output.go 处理）
			InfoLn(l10n.T("Repositories: {{.Count}} — taking ownership...", map[string]any{"Count": len(repos)}))

			// 并发执行 takeown（gather 保证输出顺序），子模块作为独立条目参与
			results := gather(ctx, l10n.T("Ownership (repositories: {{.Count}})", map[string]any{"Count": len(repos)}), repos, func(ctx context.Context, e RepoEntry) takeownResult {
				return takeownResult{name: e.Name, isSubmodule: e.IsSubmodule, err: takeownRepo(ctx, e.Path)}
			})

			var successCount, failCount int
			for _, r := range results {
				label := RepoLabel(r.name, r.isSubmodule)
				if r.err != nil {
					failCount++
					ErrorMsg(l10n.T("Failed: {{.Label}} - {{.Err}}", map[string]any{"Label": label, "Err": r.err}))
				} else {
					successCount++
					SuccessMsg(l10n.T("Done: {{.Label}}", map[string]any{"Label": label}))
				}
			}

			pterm.Println()
			ownedFinish(ctx, successCount, failCount)
		},
	}
	return c
}

// ownedFinish 打印 owned 的收尾汇总：空行、文案、样式。
//
// 抽成独立函数是为了让“被打断时说什么、以什么颜色说”能被用例直接驱动：owned 只在
// Windows 上执行，命令入口在其他平台会提前返回，收尾这段以前只能靠读代码确认，
// 于是中断判断传错、或者干脆写死成 false，测试也发现不了。
// 内容仍是原来那三步，没有为可测性改动行为
func ownedFinish(ctx context.Context, success, failed int) {
	pterm.Println()
	interrupted := ctx.Err() != nil
	SummaryMsg(ownedSummaryLine(interrupted, success, failed), interrupted)
}

// ownedSummaryLine 生成 owned 的收尾那一行，样式由 SummaryMsg 按同一次中断判断给出。
//
// takeown 自己不理会 ctx，被打断时已经启动的那几个仍会跑完，但 gather 之后交回来的
// 结果只含尝试过的仓库，因此不能说“全部完成”
func ownedSummaryLine(interrupted bool, success, failed int) string {
	data := map[string]any{"Success": success, "Failed": failed}
	if interrupted {
		return l10n.T("Interrupted: {{.Success}} succeeded, {{.Failed}} failed (only the repositories already attempted are counted)", data)
	}
	return l10n.T("Finished: {{.Success}} succeeded, {{.Failed}} failed", data)
}

// takeownRepo 获取一个仓库（或子模块）目录及其 .git 的所有权。
// 子模块的 .git 可能是 gitdir 文件，这里统一对 .git 路径本身执行 takeown，
// 覆盖大多数权限场景；子模块在 ggt 中作为独立仓库由外层统一展开，无需在此递归处理。
// ctx 仅为匹配 gather 的回调签名而入参：本函数不调用 git，也没有把它传给 takeown，
// 因此任务被整体取消时不会中断已经启动的 takeown 进程
func takeownRepo(ctx context.Context, repoPath string) error {
	// 获取仓库目录本身的所有权
	if err := runTakeown(repoPath); err != nil {
		return err
	}

	// 获取 .git 目录/文件的所有权
	gitPath := filepath.Join(repoPath, ".git")
	if _, err := os.Stat(gitPath); err == nil {
		if err := runTakeown(gitPath); err != nil {
			return err
		}
	}

	return nil
}

// runTakeown 对指定路径执行 takeown /F 命令。
// 如果路径不存在则跳过，如果 takeown 失败（如已拥有所有权）则记录但不中断流程。
func runTakeown(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	cmd := exec.Command("takeown", "/F", path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// takeown 在已经拥有所有权时也会失败，忽略此类错误
		ListItem(l10n.T("takeown on {{.Path}}: {{.Output}}",
			map[string]any{"Path": path, "Output": strings.TrimSpace(string(output))}))
	}
	return nil
}

func init() {
	register(func(root *cobra.Command) { root.AddCommand(newOwnedCmd()) })
}
