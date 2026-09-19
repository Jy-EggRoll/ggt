// output.go 集中收纳所有用户可见文本的输出封装。
//
// 所有命令都应经由这里的函数输出，不要直接在命令里调用 pterm.*——这样未来统一换主题色、
// 换输出库、或接入日志系统时，只需改这一个文件，而不必改动各业务命令。
//
// 命名约定是「4 种严重级别 × 4 种形态」：
//   - XxxMsg    打印纯字符串
//   - XxxLn     打印纯字符串，并在其后留一个空行
//   - XxxStr    返回着色后的字符串，供先缓存再统一打印的场合
//   - XxxStrLn  返回着色字符串，并在其后留一个空行
//
// 唯一例外是**面向脚本消费**的输出（ggt config get / validate / path）：pterm 不检测
// TTY，会把 ANSI 转义写进管道，让 `ggt config show | jq` 这类用法失败。那些命令
// 直接走 fmt 的裸输出，不受本文件的样式调整影响。
package cmd

import (
	"fmt"
	"strings"

	"ggt/internal/i18n"
	"github.com/pterm/pterm"
)

// ——— 统一的 pterm 输出辅助函数 ———
// 所有命令都应通过这些函数输出，不要直接在命令里调用 pterm.*。
// 这样做的好处：未来若要统一换主题色、换输出库、或接入日志系统，
// 只需修改本文件这一处，而不必改动各业务命令。
// 命名约定：Msg 系列接收纯字符串；f 系列接收 format + 参数（对应 pterm 的 Printf/Printfln）。
//
// 唯一例外是**面向脚本消费**的输出（ggt config get / validate / path）：pterm 不检测
// TTY，会把 ANSI 转义写进管道，让 `ggt config show | jq` 这类用法失败。那些命令
// 直接走 fmt 的裸输出，且不受本区块的样式调整影响。

// Header 打印带样式的标题（使用 Section 风格，比 DefaultHeader 方块更简洁）。
func Header(title string) {
	pterm.DefaultSection.Println(title)
}

// SuccessMsg 打印绿色成功消息。
func SuccessMsg(msg string) {
	pterm.Success.Println(msg)
}

// ErrorMsg 打印红色错误消息。
func ErrorMsg(msg string) {
	pterm.Error.Println(msg)
}

// InfoMsg 打印浅蓝信息消息（区别于成功的绿色，用于客观状态通报）。
func InfoMsg(msg string) {
	pterm.Info.Println(msg)
}

// WarnMsg 打印黄色警告消息。
func WarnMsg(msg string) {
	pterm.Warning.Println(msg)
}

// Ln 系列与 Msg 系列的唯一区别，是在输出之后**多留一个空行**。
//
// 这个空行不是随手加的：pterm 的 Printfln 会在渲染结果后追加换行，而 Println 会把
// 结尾换行**折叠掉**（PrefixPrinter.Sprint 对结尾 \n 先 TrimRight 再补一个），
// 所以"保留一个空行"只能靠常量格式串走 Printfln。该细节收口于此，
// 调用点不必再写 "%s\n"，也不必各自解释一遍。
func SuccessLn(msg string) { pterm.Success.Printfln("%s\n", msg) }
func ErrorLn(msg string)   { pterm.Error.Printfln("%s\n", msg) }
func InfoLn(msg string)    { pterm.Info.Printfln("%s\n", msg) }
func WarnLn(msg string)    { pterm.Warning.Printfln("%s\n", msg) }

// ErrorDetail 打印一句说明，并紧随其后原样输出一段外部内容（典型用途是把 git 的
// stderr 呈现给用户），整块之后留一个空行。
func ErrorDetail(msg, detail string) {
	pterm.Error.Printfln("%s\n%s", msg, detail)
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

// WarnStr/InfoStr/ErrorStr/SuccessStr 返回对应语义的着色字符串，
// 供需要拼接多行后再统一返回/打印的场合（如 sync 的逐仓库结果）使用。
//
// 它们接收**已渲染好的纯文本**而非格式串：T() 返回的译文里可能含字面 %，
// 若经 Sprintf 通道会被 fmt 当成格式动词解析成 %!?(MISSING)。使用约定是——
// 需要变量插值的场合，一律用 go-i18n 的 {{.Var}} 模板在 T() 里渲染完，再走这四个函数着色。
func WarnStr(s string) string    { return pterm.Warning.Sprint(s) }
func InfoStr(s string) string    { return pterm.Info.Sprint(s) }
func ErrorStr(s string) string   { return pterm.Error.Sprint(s) }
func SuccessStr(s string) string { return pterm.Success.Sprint(s) }

// StrLn 系列是 Str 系列的换行变体：返回值结尾多一个空行。
// 供"先并发收集、再顺序统一打印"的场合（如 sync 的逐仓库结果）拼接多行时使用，
// 免得每个调用点都自己写 + "\n"。
func WarnStrLn(s string) string    { return pterm.Warning.Sprint(s + "\n") }
func InfoStrLn(s string) string    { return pterm.Info.Sprint(s + "\n") }
func ErrorStrLn(s string) string   { return pterm.Error.Sprint(s + "\n") }
func SuccessStrLn(s string) string { return pterm.Success.Sprint(s + "\n") }

// DoneBanner 打印一条完成类收尾横幅（成功绿），统一各命令的结尾提示样式。
func DoneBanner(msg string) {
	pterm.Success.Println(msg)
}

// PrintProtocolSwitch 打印远程协议切换结果：仓库标签 + 灰色旧协议 → 绿色新协议。
// 仓库标签统一经 RepoLabel 着色，子模块自动带 [子] 前缀。
// 此前 remote 直接内联 FgRed/FgGreen，现已收敛到统一封装。
func PrintProtocolSwitch(name string, isSubmodule bool, oldProto, newProto string) {
	pterm.Success.Printfln("%s %s → %s", RepoLabel(name, isSubmodule), Muted(oldProto), pterm.FgGreen.Sprint(newProto))
}
