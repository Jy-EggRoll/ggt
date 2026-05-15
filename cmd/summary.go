package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

var summaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "遍历所有仓库，显示变更，询问一键提交",
	Long: `遍历所有仓库，显示变更，询问一键提交。

使用示例:
  ggt summary          查看变更并提交
  ggt sum             简写形式`,
	Run: func(cmd *cobra.Command, args []string) {
		var targetPaths []string
		if runtime.GOOS == "windows" {
			targetPaths = []string{"D:\\GitRepo", "R:\\GitRepoCache"}
		} else {
			home, _ := os.UserHomeDir()
			targetPaths = []string{filepath.Join(home, "GitRepo")}
		}
		for _, path := range targetPaths {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				continue
			}
			if runtime.GOOS == "windows" {
				fmt.Printf("%s 盘目录 Git 状态检查\n", pterm.FgMagenta.Sprint(string(path[0])))
			} else {
				pterm.FgMagenta.Printfln("%s Git 状态检查", path)
			}

			entries, err := os.ReadDir(path)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				repoPath := path + string(os.PathSeparator) + entry.Name()
				gitPath := repoPath + string(os.PathSeparator) + ".git"
				if _, err := os.Stat(gitPath); os.IsNotExist(err) {
					continue
				}

				statusOutput, err := runGitCommand(repoPath, "-c", "color.status=always", "status", "--short", "--branch", "--untracked-files")
				if err != nil {
					continue
				}
				lines := strings.Split(strings.TrimRight(statusOutput, "\n"), "\n")
				var nonEmpty []string
				for _, line := range lines {
					if strings.TrimSpace(line) != "" {
						nonEmpty = append(nonEmpty, line)
					}
				}
				if len(nonEmpty) <= 1 {
					continue
				}

				fmt.Print(pterm.FgGray.Sprint("*************************"), "\n")
				fmt.Printf("%s 检测到变动\n", pterm.FgCyan.Sprint(entry.Name()))
				fmt.Print(statusOutput)
				fmt.Printf("%s\n", pterm.FgCyan.Sprint("变动详情："))
				diffOutput, _ := runGitCommand(repoPath, "diff", "--color=always", "--stat")
				if diffOutput != "" {
					fmt.Print(diffOutput)
				}

				result, _ := pterm.DefaultInteractiveConfirm.WithDefaultValue(false).WithDefaultText("是否一键提交所有更改并推送？").Show()
				if result {
					fmt.Printf("%s\n", pterm.FgYellow.Sprint("正在处理推送..."))
					exec.Command("git", "-C", repoPath, "add", "-A").Run()
					msg := fmt.Sprintf("🔨 chore: 终端推送更新 %s", time.Now().Format("2006-01-02 15:04:05"))
					exec.Command("git", "-C", repoPath, "commit", "-m", msg).Run()
					exec.Command("git", "-C", repoPath, "push").Run()
					fmt.Printf("%s\n", pterm.FgGreen.Sprint("推送完成！"))
					fmt.Print("大小信息：\n")
					countOutput, _ := exec.Command("git", "-C", repoPath, "count-objects", "-vH").Output()
					if len(countOutput) > 0 {
						fmt.Print(string(countOutput))
					}
				}
				fmt.Print(pterm.FgGray.Sprint("*************************"), "\n")
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(summaryCmd)
	summaryCmd.Aliases = []string{"sum"}
}
