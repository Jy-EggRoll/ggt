// cmd 包是 ggt 的 Cobra 命令入口层。每个文件对应一个 ggt 子命令。
// 这里定义根命令、全局配置加载、仓库列表管理、以及各命令共享的辅助函数。
package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"ggt/internal/config"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

var (
	// concurrency 通过 --concurrency / -c 命令行参数传入，
	// 在 PersistentPreRunE 中覆盖配置文件的默认值。
	concurrency int
	// cfg 是全局配置实例，在 PersistentPreRunE 中初始化。
	cfg *config.Config
)

// rootCmd 是 ggt 的根命令，定义程序名称、简介、以及全局行为。
// PersistentPreRunE 在每个子命令执行前自动运行，用于加载配置。
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
			return fmt.Errorf("加载配置失败: %w", err)
		}

		// 命令行的 -c 参数优先级高于配置文件
		if concurrency > 0 {
			cfg.Concurrency = concurrency
		}

		return nil
	},
}

// Execute 是程序的入口，由 main.go 调用。
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		pterm.Error.Println("执行失败:", err)
		os.Exit(1)
	}
}

func init() {
	// -c 的默认值为 0，表示"未显式指定"；在 PersistentPreRunE 中仅当 >0 时才
	// 覆盖配置文件里的并发数。真正生效的默认值（CPU 核心数的一半）由
	// config 包的 getDefaultConcurrency 统一计算，避免出现两处默认值逻辑不一致。
	rootCmd.PersistentFlags().IntVarP(&concurrency, "concurrency", "c", 0, "并发数 (省略时取配置文件值，配置文件也未设则取 CPU 核心数的一半)")
}

// GetConfig 返回全局配置实例。
func GetConfig() *config.Config {
	return cfg
}

// ——— 统一的 pterm 输出辅助函数 ———
// 所有命令都应通过这些函数输出，不要直接在命令里调用 pterm.*。
// 这样做的好处：未来若要统一换主题色、换输出库、或接入日志系统，
// 只需修改本文件这一处，而不必改动各业务命令。
// 命名约定：Msg 系列接收纯字符串；f 系列接收 format + 参数（对应 pterm 的 Printf/Printfln）。

// Header 打印带样式的标题（使用 Section 风格，比 DefaultHeader 方块更简洁）。
func Header(title string) {
	pterm.DefaultSection.Println(title)
}

// SuccessMsg 打印绿色成功消息。
func SuccessMsg(msg string) {
	pterm.Success.Println(msg)
}

// Successf 以绿色成功样式打印格式化消息。
func Successf(format string, args ...any) {
	pterm.Success.Printfln(format, args...)
}

// ErrorMsg 打印红色错误消息。
func ErrorMsg(msg string) {
	pterm.Error.Println(msg)
}

// Errorf 以红色错误样式打印格式化消息。
func Errorf(format string, args ...any) {
	pterm.Error.Printfln(format, args...)
}

// InfoMsg 打印浅蓝信息消息（区别于成功的绿色，用于客观状态通报）。
func InfoMsg(msg string) {
	pterm.Info.Println(msg)
}

// Infof 以浅蓝信息样式打印格式化消息。
func Infof(format string, args ...any) {
	pterm.Info.Printfln(format, args...)
}

// WarnMsg 打印黄色警告消息。
func WarnMsg(msg string) {
	pterm.Warning.Println(msg)
}

// Warnf 以黄色警告样式打印格式化消息。
func Warnf(format string, args ...any) {
	pterm.Warning.Printfln(format, args...)
}

// PrintPath 以黄色缩进格式打印一个路径。
func PrintPath(path string) {
	pterm.FgYellow.Printf("  - %s\n", path)
}

// ——— 仓库列表管理 ———

// GetRepoList 返回所有有效仓库路径的列表。
// 合并直接添加的仓库（RepoPaths）和从父目录扫描到的仓库。
// 父目录扫描会检查每个子目录是否包含 .git 目录。
func GetRepoList() []string {
	repos := GetConfig().RepoPaths

	for _, parentPath := range GetConfig().ParentPaths {
		entries, err := os.ReadDir(parentPath)
		if err != nil {
			// 父目录不存在或无权访问，跳过
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

// isGitRepo 检查指定路径是否是一个有效的 git 仓库（存在 .git 目录）。
func isGitRepo(path string) bool {
	gitPath := path + string(os.PathSeparator) + ".git"
	info, err := os.Stat(gitPath)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// MustGetRepoList 获取仓库列表，如果为空则打印提示并以退出码 0 结束进程。
// 空列表属于"正常无任务可做"而非错误，因此用 os.Exit(0) 而非返回错误，
// 避免上层命令再去处理一个必然为空的列表。
// 所有需要仓库列表的命令都应调用此函数而非 GetRepoList。
func MustGetRepoList() []string {
	repos := GetRepoList()
	if len(repos) == 0 {
		WarnMsg("未配置任何仓库路径，请先使用 'ggt repo add <path>' 或 'ggt repo add-parent <path>' 添加")
		os.Exit(0)
	}
	return repos
}

// PrintRepoList 打印仓库列表的标题和所有路径。
func PrintRepoList(repos []string) {
	Header("仓库列表")
	for _, repo := range repos {
		PrintPath(repo)
	}
	pterm.Println()
	Infof("共 %d 个仓库", len(repos))
}

// getRepoName 从完整路径中提取仓库目录名。
// 如 "/home/user/GitRepo/my-project" → "my-project"
func getRepoName(path string) string {
	return filepath.Base(path)
}
