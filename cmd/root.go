package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"

	"ggt/internal/config"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

var (
	concurrency int
	cfg         *config.Config
)

var rootCmd = &cobra.Command{
	Use:   "ggt",
	Short: "ggt - Git 仓库管理工具",
	Long: `一个用于管理多个 git 仓库的 CLI 工具，支持并发操作。
	
使用帮助:
  ggt --help 查看详细帮助

配置文件: ~/.config/go-git-ggt/ggt-config.json`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		var err error
		cfg, err = config.LoadConfig()
		if err != nil {
			pterm.Error.Println("加载配置失败:", err)
			os.Exit(1)
		}

		if concurrency > 0 {
			cfg.Concurrency = concurrency
		}

		return nil
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		pterm.Error.Println("执行失败:", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().IntVarP(&concurrency, "concurrency", "c", 0, "并发数 (默认为 CPU 核心数的一半)")
}

func GetConfig() *config.Config {
	return cfg
}

func Header(title string) {
	pterm.Println(pterm.DefaultHeader.Sprint(title))
}

func SuccessMsg(msg string) {
	pterm.Success.Println(msg)
}

func ErrorMsg(msg string) {
	pterm.Error.Println(msg)
}

func InfoMsg(msg string) {
	pterm.Info.Println(msg)
}

func WarnMsg(msg string) {
	pterm.Warning.Println(msg)
}

func PrintPath(path string) {
	pterm.FgYellow.Printf("  - %s\n", path)
}

func GetRepoList() []string {
	repos := GetConfig().RepoPaths

	for _, parentPath := range GetConfig().ParentPaths {
		entries, err := os.ReadDir(parentPath)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			repoPath := parentPath + string(os.PathSeparator) + entry.Name()
			if isGitRepo(repoPath) {
				repos = append(repos, repoPath)
			}
		}
	}

	return repos
}

func isGitRepo(path string) bool {
	gitPath := path + string(os.PathSeparator) + ".git"
	info, err := os.Stat(gitPath)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func MustGetRepoList() []string {
	repos := GetRepoList()
	if len(repos) == 0 {
		pterm.Warning.Println("未配置任何仓库路径，请先使用 'ggt repo add <path>' 或 'ggt repo add-parent <path>' 添加")
		os.Exit(0)
	}
	return repos
}

func PrintRepoList(repos []string) {
	Header("仓库列表")
	for _, repo := range repos {
		PrintPath(repo)
	}
	pterm.Println()
	pterm.Info.Printf("共 %d 个仓库\n", len(repos))
}

func PrintRepoStatus(repoPath string, status string) {
	name := getRepoName(repoPath)
	pterm.FgCyan.Printf("[%s]\n", name)
	pterm.Println(status)
}

func getRepoName(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[i+1:]
		}
	}
	return path
}

func PrintRepoSize(repoPath string, size string) {
	name := getRepoName(repoPath)
	fmt.Printf("  %s: %s\n", name, size)
}

func Confirm(prompt string) bool {
	pterm.Print(pterm.Yellow(prompt + " (y/n): "))
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	input := scanner.Text()
	return input == "y" || input == "Y"
}

func RunGitCommand(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}
