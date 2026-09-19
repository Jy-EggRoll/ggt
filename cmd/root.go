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
	"ggt/internal/i18n"
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

// newRootCmd 构造根命令并注册全局持久化 flag。
//
// 命令树在语言加载之后才构造（见 registry.go），因此这里出现的 T() 都是真实翻译调用。
// PersistentPreRunE 在每个子命令执行前自动运行，用于加载配置。
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "ggt",
		Short: i18n.T("ggt - Git repository manager", nil),
		Long: i18n.T(`A CLI tool for managing multiple git repositories, with concurrent operations.

Help:
  ggt --help  Show detailed help

Config file: ~/.config/go-git-ggt/ggt-config.json`, nil),
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			t := NewDebugTimer(i18n.T("Loading configuration", nil))
			var err error
			cfg, err = config.LoadConfig()
			if err != nil {
				// 错误包装保持 Go 侧拼接：go-i18n 模板没有 %w 等价物，塞进模板会丢掉错误链
				return fmt.Errorf("%s: %w", i18n.T("Failed to load the configuration", nil), err)
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

	// -c 的默认值为 0，表示"未显式指定"；在 PersistentPreRunE 中仅当 >0 时才
	// 覆盖配置文件里的并发数。真正生效的默认值（CPU 核心数的一半）由
	// config 包的 resolveConcurrency 统一计算，避免出现两处默认值逻辑不一致。
	root.PersistentFlags().IntVarP(&concurrency, "concurrency", "c", 0,
		i18n.T("Concurrency (defaults to the concurrency config value; when unset uses the CPUHalf semantic value, i.e. half of the CPU cores; CPUFull/CPUQuarter or an explicit number are also accepted)", nil))
	// --debug 持久化 flag：所有子命令均可使用，输出各阶段耗时用于性能诊断。
	root.PersistentFlags().BoolVar(&debug, "debug", false,
		i18n.T("Print debug timing information (duration of each phase)", nil))
	// --lang 持久化 flag：声明它的唯一目的是让 cobra 认可这个参数，否则命令行里
	// 出现 --lang 会被判为 unknown flag。真正生效的取值由 resolveLanguage 预扫描
	// os.Args 得到（语言必须早于 cobra 解析才能确定），所以这里刻意不绑定变量，
	// 避免出现两个互相矛盾的取值来源。
	root.PersistentFlags().StringP("lang", "l", "", i18n.T("Output language (e.g. en, zh-CN); defaults to the language config value", nil))

	return root
}

// Execute 是程序的入口，由 main.go 调用。
//
// 三步顺序是有意为之，不能调换：
//  1. 先确定并加载语言。cobra 的 --help 路径不会执行 PersistentPreRunE，而命令描述
//     在构造时就要用到，所以语言解析必须走在最前面
//  2. 再构造命令树。各命令的构造函数会调用 T()，此时语言已就绪
//  3. 最后才交给 cobra 解析参数并执行
func Execute() {
	if err := i18n.Init(resolveLanguage()); err != nil {
		// 语言文件是 //go:embed 进来的，加载失败属于构建期错误，必须显式暴露。
		// 静默降级只会表现为"中文界面变成了英文"，没有任何报错，极难排查
		ErrorMsg(err.Error())
		os.Exit(1)
	}

	if err := buildRoot().Execute(); err != nil {
		ErrorMsg(i18n.T("Execution failed: {{.Err}}", map[string]any{"Err": err}))
		os.Exit(1)
	}
}

// 关于文案：本包不提供 T 的薄封装，各命令一律直接调用 i18n.T("英文原文", data)。
//
// 这不是风格偏好，而是提取工具的硬性要求：tools/l10n 规定"消息调用的首个参数必须是
// 字符串字面量才能确定消息 id"。任何一层透传封装都必然以变量为参转发
// （func T(msg string, ...) { return i18n.T(msg, ...) }），会被提取器判为违规。
// 去掉封装后规则全仓一致、无需任何例外，代价只是调用点多写一个包名前缀。
//
// 另注意 i18n.T 的返回值是已渲染好的纯文本，**不要**再当作 printf 的格式串传给
// Infof/WarnS 等，否则译文里出现的字面 %（如"完成度 100%"）会被 fmt 解析成 %!?(MISSING)。
// 需要输出时请使用 Msg 系列（InfoMsg 等）或 Str 系列（InfoStr 等）。

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
		return pterm.FgCyan.Sprintf("%s %s", i18n.T("[sub]", nil), name)
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

// WarnStr/InfoStr/ErrorStr/SuccessStr 返回对应语义的着色字符串，
// 与 WarnS/InfoS/ErrorS/SuccessS 的唯一区别是接收"已渲染好的纯文本"而非格式串。
//
// 这四个函数是为 i18n 而加的：T() 返回的译文里可能含字面 %（如"完成度 100%"），
// 若经 Sprintf 通道会被 fmt 当成格式动词解析成 %!?(MISSING)。使用约定是——
// 需要变量插值的场合，一律用 go-i18n 的 {{.Var}} 模板在 T() 里渲染完，
// 再走这四个函数着色；不要在译文上做二次 Sprintf。
//
// 换行行为与 pterm 保持一致：入参结尾的换行会被折叠为单个换行
// （PrefixPrinter.Sprint 对结尾 \n 先 TrimRight 再补一个），
// 所以需要保留末尾空行的场合要用常量格式串走 f 系列，例如 Infof("%s\n", T(...))。
func WarnStr(s string) string    { return pterm.Warning.Sprint(s) }
func InfoStr(s string) string    { return pterm.Info.Sprint(s) }
func ErrorStr(s string) string   { return pterm.Error.Sprint(s) }
func SuccessStr(s string) string { return pterm.Success.Sprint(s) }

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
		WarnMsg(i18n.T("No repositories configured; add one with 'ggt repo add <path>' or 'ggt repo add-parent <path>'", nil))
		os.Exit(0)
	}
	return repos
}

// PrintRepoList 打印仓库列表的标题和所有路径。
func PrintRepoList(repos []string) {
	Header(i18n.T("Repositories", nil))
	for _, repo := range repos {
		PrintPath(repo)
	}
	pterm.Println()
	InfoMsg(i18n.T("Total repositories: {{.Count}}", map[string]any{"Count": len(repos)}))
}

// getRepoName 从完整路径中提取仓库目录名。
// 如 "/home/user/GitRepo/my-project" → "my-project"
func getRepoName(path string) string {
	return filepath.Base(path)
}
