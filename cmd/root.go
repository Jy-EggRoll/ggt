// cmd 包是 ggt 的 Cobra 命令入口层。每个文件对应一个 ggt 子命令。
// 这里定义根命令、全局配置加载、仓库列表管理、以及各命令共享的辅助函数。
package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ggt/internal/config"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

var (
	// concurrency 通过 --concurrency / -c 命令行参数传入，
	// 在 PersistentPreRunE 中覆盖配置文件的默认值。
	concurrency int
	// debug 通过 --debug 持久化 flag 传入，控制是否输出各阶段耗时计时。
	debug bool
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
		t := NewDebugTimer("配置加载")
		var err error
		cfg, err = config.LoadConfig()
		if err != nil {
			return fmt.Errorf("加载配置失败: %w", err)
		}

		// 命令行的 -c 参数优先级高于配置文件；-c 传的是具体数字，
		// 覆盖为数字串（如 "8"），语义串常量（CPUHalf 等）仅在配置文件未显式设置时生效。
		if concurrency > 0 {
			cfg.Concurrency = strconv.Itoa(concurrency)
		}
		t.Done()

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
	rootCmd.PersistentFlags().IntVarP(&concurrency, "concurrency", "c", 0, "并发数 (省略时取配置文件 concurrency 值；未设则默认语义值 CPUHalf，即 CPU 核心数的一半；也可写 CPUFull/CPUQuarter 或具体数字)")
	// --debug 持久化 flag：所有子命令均可使用，输出各阶段耗时用于性能诊断。
	rootCmd.PersistentFlags().BoolVar(&debug, "debug", false, "输出调试计时信息（各阶段耗时）")
}

// GetConfig 返回全局配置实例。
func GetConfig() *config.Config {
	return cfg
}

// GetDebug 返回 --debug flag 是否开启。
// 各命令据此决定是否输出阶段耗时计时。
func GetDebug() bool {
	return debug
}

// DebugTimer 记录阶段耗时，仅 --debug 时输出灰色计时行。
// 用法：t := NewDebugTimer("阶段名") ... defer t.Done() 或 t.Done()
type DebugTimer struct {
	label string
	start time.Time
}

// NewDebugTimer 创建计时器并记录起始时间。
func NewDebugTimer(label string) *DebugTimer {
	return &DebugTimer{label: label, start: time.Now()}
}

// Done 计算并输出耗时（仅 --debug 模式）。
func (t *DebugTimer) Done() {
	if debug {
		pterm.FgGray.Printf("  [debug] %s: %v\n", t.label, time.Since(t.start))
	}
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

// PrintPath 以统一列表项格式打印一个路径（灰色 "  - path"）。
// 与 ListItem 共用样式，避免不同命令的列表前缀/颜色割裂。
func PrintPath(path string) {
	ListItem(path)
}

// ——— 统一样式原子（所有命令的用户文本输出都应经由本区块，
// 不得再直接调用 pterm.* 原色或 fmt.Print*，git 自身着色输出除外，统一走 PrintRaw）———

// Muted 返回灰色（次要/细节）文本字符串，不立即打印。
// 用于 URL、说明性标签（如"变动详情："）等不希望抢占视觉重心的文本。
func Muted(text string) string {
	return pterm.FgGray.Sprint(text)
}

// ListItem 以统一的灰色项目符号打印一行列表项："  - text"。
// 全仓所有列表（仓库路径、分桶名、操作明细）共用此格式，
// 消除此前 FgYellow 的 "  - "、FgGray 的 "    - "、以及 "  takeown on ..." 三种割裂风格。
func ListItem(text string) {
	pterm.FgGray.Printf("  - %s\n", text)
}

// PrintSeparator 打印一条全宽浅黄分隔线，用于区分不同仓库/区块。
// 宽度取自当前终端宽度，保证跨命令一致（替代此前 size/summary 各自重复实现）。
func PrintSeparator() {
	pterm.FgLightYellow.Println(strings.Repeat("─", pterm.GetTerminalWidth()))
}

// buildSeparator 返回长度为 width 的浅黄分隔线字符串（纯函数，便于单测）。
// 与 PrintSeparator 共享同一着色逻辑，仅不负责打印。
func buildSeparator(width int) string {
	return pterm.FgLightYellow.Sprint(strings.Repeat("─", width))
}

// RepoName 返回青色包裹的仓库名前缀 "[name]"，全仓统一仓库名着色。
// 此前 status 用 FgYellow、size/summary/remote 用 FgCyan，同一语义三色并存，现收敛于此。
func RepoName(name string) string {
	return pterm.FgCyan.Sprintf("[%s]", name)
}

// RepoLabel 返回带"是否子模块"语义的仓库标签：
//   - 顶层仓库：青色 [name]
//   - 子模块：青色 [子] name
//
// 所有命令在打印仓库名时必须统一经此函数，消除此前各个命令对仓库名异色/无前缀的割裂处理，
// 也让"子模块"这一身份在任意命令输出里都有一致的 [子] 标识。
func RepoLabel(name string, isSubmodule bool) string {
	if isSubmodule {
		return pterm.FgCyan.Sprintf("[子] %s", name)
	}
	return RepoName(name)
}

// RepoLine 打印一行"仓库标签 + 备注"，作为各命令的仓库标题行（不含分隔线）。
// 仓库标签统一经 RepoLabel 着色，子模块自动带 [子] 前缀。
// 需要分隔线时另行调用 PrintSeparator。
func RepoLine(name, note string, isSubmodule bool) {
	label := RepoLabel(name, isSubmodule)
	if note == "" {
		pterm.Println(label)
	} else {
		pterm.Printf("%s %s\n", label, note)
	}
}

// PrintRaw 透传外部（如 git）自带 ANSI 着色的原始输出，仅做打印封装。
// 调用处可明确这是"透传"而非本工具自身样式，避免与统一封装混淆。
func PrintRaw(s string) {
	fmt.Print(s)
}

// WarnS/InfoS/ErrorS/SuccessS 返回对应语义的着色字符串，
// 供需要拼接多行后再统一返回/打印的场合（如 sync 的逐仓库结果）使用，
// 替代直接调用 pterm.Warning.Sprintf 等造成的风格割裂。
func WarnS(format string, args ...any) string {
	return pterm.Warning.Sprintf(format, args...)
}
func InfoS(format string, args ...any) string {
	return pterm.Info.Sprintf(format, args...)
}
func ErrorS(format string, args ...any) string {
	return pterm.Error.Sprintf(format, args...)
}
func SuccessS(format string, args ...any) string {
	return pterm.Success.Sprintf(format, args...)
}

// DoneBanner 打印一条完成类收尾横幅（成功绿），统一各命令的结尾提示样式。
// 此前 sync 用 pterm.Success.Println、owned/remote 用 Infof("处理完成...")，现已收敛。
func DoneBanner(msg string) {
	pterm.Success.Println(msg)
}

// PrintProtocolSwitch 打印远程协议切换结果：仓库标签 + 灰色旧协议 → 绿色新协议。
// 仓库标签统一经 RepoLabel 着色，子模块自动带 [子] 前缀。
// 此前 remote 直接内联 FgRed/FgGreen，现已收敛到统一封装。
func PrintProtocolSwitch(name string, isSubmodule bool, oldProto, newProto string) {
	pterm.Success.Printfln("%s %s → %s", RepoLabel(name, isSubmodule), Muted(oldProto), pterm.FgGreen.Sprint(newProto))
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
