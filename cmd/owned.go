package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"ggt/internal/git"
	"ggt/internal/worker"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

// takeownResult 记录单个仓库的 takeown 处理结果。
type takeownResult struct {
	name string
	err  error
}

// ownedCmd 实现 "ggt owned"。
// 调用 Windows takeown 命令获取仓库文件所有权。
// 严格遵循原有的 pwsh 脚本逻辑，处理主仓库目录、.git 目录、以及子模块。
//
// 注意：此命令仅适用于 Windows 系统（入口处 runtime.GOOS 检测提前返回）。
var ownedCmd = &cobra.Command{
	Use:   "owned",
	Short: "批量获取所有仓库的所有权",
	Long: `批量获取所有仓库的所有权。

严格遵循当前的 pwsh 命令模仿实现。
使用 takeown 命令获取目录及 .git 目录的所有权。
处理子模块的所有权。
	
使用示例:
  ggt owned          获取所有仓库所有权`,
	Run: func(cmd *cobra.Command, args []string) {
		if runtime.GOOS != "windows" {
			pterm.Warning.Println("ggt owned 仅支持 Windows 系统")
			return
		}
		repos := MustGetRepoList()
		pterm.Info.Printf("共 %d 个仓库，开始获取所有权...\n\n", len(repos))

		// 并发执行 takeown（worker.Map 保证输出顺序）
		results := worker.Map(context.Background(), repos, GetConfig().Concurrency, func(repoPath string) takeownResult {
			return takeownResult{name: getRepoName(repoPath), err: takeownRepo(repoPath)}
		})

		var successCount, failCount int
		for _, r := range results {
			if r.err != nil {
				failCount++
				ErrorMsg(fmt.Sprintf("处理失败: %s - %s", r.name, r.err))
			} else {
				successCount++
				SuccessMsg(fmt.Sprintf("成功处理: %s", r.name))
			}
		}

		pterm.Println()
		pterm.Info.Printf("处理完成: 成功 %d, 失败 %d\n", successCount, failCount)
	},
}

// takeownRepo 获取一个仓库的完整所有权：
// 1. 主仓库目录本身（takeown /F <path>）
// 2. .git 目录（通常有权限保护）
// 3. 所有子模块的目录和 .git 目录
func takeownRepo(repoPath string) error {
	// 获取主仓库目录的所有权
	if err := runTakeown(repoPath); err != nil {
		return err
	}

	// 获取 .git 目录的所有权
	gitPath := filepath.Join(repoPath, ".git")
	if _, err := os.Stat(gitPath); err == nil {
		if err := runTakeown(gitPath); err != nil {
			return err
		}
	}

	// 处理子模块：遍历所有子模块，获取其目录、.git 文件、以及元数据目录的所有权
	submoduleOutput, err := git.Run(repoPath, "submodule", "foreach", "--quiet", "echo $name")
	if err == nil && strings.TrimSpace(submoduleOutput) != "" {
		lines := strings.Split(strings.TrimSpace(submoduleOutput), "\n")
		for _, subPath := range lines {
			subPath = strings.TrimSpace(subPath)
			if subPath == "" {
				continue
			}

			fullSubPath := filepath.Join(repoPath, subPath)
			subDotGit := filepath.Join(fullSubPath, ".git")
			gitModuleMeta := filepath.Join(gitPath, "modules", subPath)

			runTakeown(fullSubPath)
			runTakeown(subDotGit)
			runTakeown(gitModuleMeta)
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
		pterm.FgYellow.Printf("  takeown on %s: %s\n", path, string(output))
	}
	return nil
}

func init() {
	rootCmd.AddCommand(ownedCmd)
}
