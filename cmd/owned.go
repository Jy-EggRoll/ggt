package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"ggt/internal/worker"
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
// 子模块在 ggt 中被视为一等仓库，由统一的仓库发现（MustGetAllRepos）展开后
// 逐个作为独立条目交给 takeownRepo 处理，无需在此命令内编写子模块专属循环。
//
// 注意：此命令仅适用于 Windows 系统（入口处 runtime.GOOS 检测提前返回）。
var ownedCmd = &cobra.Command{
	Use:   "owned",
	Short: "批量获取所有仓库的所有权",
	Long: `批量获取所有仓库的所有权。

严格遵循当前的 pwsh 命令模仿实现。
使用 takeown 命令获取目录及 .git 目录的所有权。
子模块会作为独立仓库一并处理。
	
使用示例:
  ggt owned          获取所有仓库所有权`,
	Run: func(cmd *cobra.Command, args []string) {
		if runtime.GOOS != "windows" {
			WarnMsg("ggt owned 仅支持 Windows 系统")
			return
		}
		repos := MustGetAllRepos(context.Background(), GetConfig().IgnoreSubmodules)
		Infof("共 %d 个仓库，开始获取所有权...\n", len(repos))

		// 并发执行 takeown（worker.Map 保证输出顺序），子模块作为独立条目参与
		results := worker.Map(context.Background(), repos, GetConfig().ConcurrencyValue(), func(ctx context.Context, e RepoEntry) takeownResult {
			return takeownResult{name: e.Name, isSubmodule: e.IsSubmodule, err: takeownRepo(ctx, e.Path)}
		})

		var successCount, failCount int
		for _, r := range results {
			label := RepoLabel(r.name, r.isSubmodule)
			if r.err != nil {
				failCount++
				Errorf("处理失败: %s - %s", label, r.err)
			} else {
				successCount++
				Successf("成功处理: %s", label)
			}
		}

		pterm.Println()
		Infof("处理完成: 成功 %d, 失败 %d", successCount, failCount)
	},
}

// takeownRepo 获取一个仓库（或子模块）目录及其 .git 的所有权。
// 子模块的 .git 可能是 gitdir 文件，这里统一对 .git 路径本身执行 takeown，
// 覆盖大多数权限场景；子模块在 ggt 中作为独立仓库由外层统一展开，无需在此递归处理。
// 接收上层 ctx 以便任务被整体取消时立即中断 git 调用。
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
		ListItem("takeown on " + path + ": " + strings.TrimSpace(string(output)))
	}
	return nil
}

func init() {
	rootCmd.AddCommand(ownedCmd)
}
