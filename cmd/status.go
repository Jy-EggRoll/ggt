package cmd

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

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

		cfg := GetConfig()
		var wg sync.WaitGroup
		sem := make(chan struct{}, cfg.Concurrency)

		for _, repoPath := range repos {
			wg.Add(1)
			go func(path string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				showRepoStatus(path)
			}(repoPath)
		}

		wg.Wait()
	},
}

func showRepoStatus(repoPath string) {
	output, err := runGitCommand(repoPath, "status", "--short", "--branch", "--untracked-files")
	if err != nil {
		pterm.Warning.Printf("仓库 %s: 执行失败\n", repoPath)
		return
	}

	if output == "" {
		name := getRepoName(repoPath)
		pterm.FgGreen.Printf("[%s] ", name)
		pterm.Println("已就绪")
		return
	}
	name := getRepoName(repoPath)
	pterm.FgYellow.Printf("[%s]\n", name)
	pterm.Println(output)
}

func runGitCommand(repoPath string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoPath
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Aliases = []string{"st"}
}
