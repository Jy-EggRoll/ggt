package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

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
		repos := MustGetRepoList()
		pterm.Info.Printf("共 %d 个仓库，开始获取所有权...\n\n", len(repos))

		var successCount, failCount int
		for _, repoPath := range repos {
			err := takeownRepo(repoPath)
			if err != nil {
				failCount++
				ErrorMsg(fmt.Sprintf("处理失败: %s - %s", getRepoName(repoPath), err))
			} else {
				successCount++
				SuccessMsg(fmt.Sprintf("成功处理: %s", getRepoName(repoPath)))
			}
		}

		pterm.Println()
		pterm.Info.Printf("处理完成: 成功 %d, 失败 %d\n", successCount, failCount)
	},
}

func takeownRepo(repoPath string) error {
	// takeown on main repo
	if err := runTakeown(repoPath); err != nil {
		return err
	}

	// takeown on .git directory
	gitPath := filepath.Join(repoPath, ".git")
	if _, err := os.Stat(gitPath); err == nil {
		if err := runTakeown(gitPath); err != nil {
			return err
		}
	}

	// handle submodules
	submoduleOutput, err := runGitCommand(repoPath, "submodule", "foreach", "--quiet", "echo $name")
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

func runTakeown(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	cmd := exec.Command("takeown", "/F", path)
	output, err := cmd.Output()
	if err != nil {
		// takeown might fail if already owned, ignore
		pterm.FgYellow.Printf("  takeown on %s: %s\n", path, string(output))
	}
	return nil
}

func init() {
	rootCmd.AddCommand(ownedCmd)
}
