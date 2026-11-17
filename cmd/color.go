// color.go 决定本次运行的输出是否带着色转义。
//
// 为什么必须自己判定：pterm 在包 init 里调用 gookit/color 的 ForceColor()，把库里
// “输出不是终端就不上色”的那套自动判断整个覆盖掉。于是 ggt 的输出去向不管是终端、
// 管道、重定向还是 CI 的日志采集，pterm 都照样写出 ANSI 转义——`ggt config show | jq`
// 因此直接解析失败，此前 CI 上那两条用例就是这么挂的（CI 没有设 NO_COLOR，本地 shell
// 设了，于是问题只在 CI 暴露）。
//
// 判定口径：pterm 的着色开关是全局的（gookit/color 的 Enable 与 pterm.PrintColor 各一份），
// 没有按 writer 分流的接口，而 ggt 的数据走 stdout、提示走 stderr。只有一份开关，两路
// 输出就只能在“交集”上收敛：只要 stdout 与 stderr 里有一个不是终端，整体关色。这样
// 两条要求同时成立——管道里的数据不带转义，终端里仍然有颜色。
//
// 为什么不按单路判定：那需要两份互不相干的开关，而 pterm 做不到。退一步说，即使能做到，
// 两个方向都不对：按 stdout 判定，`ggt status 2>build.log` 会把转义写进日志；按 stderr
// 判定，`ggt config show | jq` 仍然解析失败。代价是 `ggt status | less` 时 stderr 的提示
// 也不再着色——这是“宁可少色，不可污染管道”的取舍，换来同一屏里的样式保持一致。
package cmd

import (
	"errors"
	"io"
	"os"
	"strings"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/pterm/pterm"
	"github.com/spf13/pflag"
	"golang.org/x/term"
)

// colorMode 是 --color 的取值。
//
//	auto   两个输出流都是终端时才着色（默认）
//	always 强制着色，不看终端、也不看 NO_COLOR
//	never  一律不着色
type colorMode int

const (
	colorAuto colorMode = iota
	colorAlways
	colorNever
)

// String / Set / Type 让 colorMode 直接充当 pflag.Value。
//
// 预扫描与 cobra 解析因此共用同一份取值转换：两处对“什么算合法取值”不可能给出
// 不同结论，也不会出现一边接受 ALWAYS、另一边拒绝的情况。
func (m *colorMode) String() string {
	switch *m {
	case colorAlways:
		return "always"
	case colorNever:
		return "never"
	default:
		return "auto"
	}
}

// Set 接受大小写与首尾空白都不敏感的 auto / always / never。
//
// 合法值刻意不做前缀匹配：`--color=a` 这类缩写一旦被接受，将来加取值就会悄悄改变
// 已有命令的含义。
func (m *colorMode) Set(raw string) error {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "auto":
		*m = colorAuto
	case "always":
		*m = colorAlways
	case "never":
		*m = colorNever
	default:
		return errors.New(l10n.T(`Expected "auto", "always" or "never"`, nil))
	}
	return nil
}

// Type 出现在 --help 里（--color auto|always|never），顺带把合法取值告诉用户。
func (m *colorMode) Type() string { return "auto|always|never" }

// resolveColorMode 从原始命令行参数里预扫描 --color 的取值。
//
// 为什么早于 cobra 解析：着色开关必须赶在任何一行输出之前落实，而 ggt 在解析之前就会
// 打印语言回退提示与配置加载失败的报错；--help 路径更是不跑 PersistentPreRunE。
// 与 --lang 同一套做法（见 lang.go），同样用 pflag 而不是手写循环：取值与 flag 分离、
// `--` 之后不再解析、未知 flag 的参数不被误读为取值，这些细节 pflag 已经处理妥当。
//
// 刻意忽略 Parse 返回的错误：非法取值留给 cobra 报给用户（那时语言与命令树都已就绪，
// 报错文案能跟界面语言一致），这里先按 auto 继续，最坏结果只是多一道“可能没颜色”的判定。
func resolveColorMode(args []string) colorMode {
	mode := colorAuto
	fs := pflag.NewFlagSet("color-scan", pflag.ContinueOnError)
	// 允许出现未知 flag，否则子命令自己的 flag 会让预扫描直接失败
	fs.ParseErrorsWhitelist.UnknownFlags = true
	// 屏蔽 pflag 自己的报错输出：这里只是预扫描，真正的报错由 cobra 负责
	fs.SetOutput(io.Discard)
	fs.Var(&mode, "color", "")
	_ = fs.Parse(args)

	return mode
}

// applyColorMode 把口径落实到 pterm 的全局开关上。
//
// 显式调用 EnableColor 而不是“只在需要关闭时调 DisableColor”：全局开关的初值取决于
// pterm 的 init（那里强行开了颜色），调用方不该依赖这个初值。
func applyColorMode(mode colorMode) {
	_, noColor := os.LookupEnv("NO_COLOR")
	if useColor(mode, noColor, isTerminal(os.Stdout), isTerminal(os.Stderr)) {
		pterm.EnableColor()
		return
	}
	pterm.DisableColor()
}

// useColor 是着色口径唯一的判定点。
//
// 三个判断依据都由调用方传进来而不是在这里现读：判定本身与 os 无关，才能把“显式取值、
// NO_COLOR、两路是否终端”的组合逐条固定下来（见 color_test.go）。
func useColor(mode colorMode, noColorSet, stdoutTerminal, stderrTerminal bool) bool {
	switch mode {
	case colorAlways:
		return true
	case colorNever:
		return false
	}

	// NO_COLOR 是跨工具约定（https://no-color.org）：该变量存在即关色，与它的取值无关。
	// 它优先于终端判定——用户设它就是在说“别给我颜色”。显式 --color=always 仍能覆盖它：
	// 那是本次命令的直接要求，约定本身也允许命令行覆盖环境变量
	if noColorSet {
		return false
	}

	// 取交集：两路都要是终端（理由见文件头）
	return stdoutTerminal && stderrTerminal
}

// isTerminal 判断 f 是否接到真正的终端。
//
// 必须用 x/term 的 ioctl 检测，不能用 `f.Stat()` 的 ModeCharDevice：**/dev/null 也是
// 字符设备**，重定向到它时会被当成终端。这两个用途都踩在这个判定上——着色（重定向到
// /dev/null 还上色毫无意义）与 stdin 的交互性判断（`ggt config reset --all` 接 /dev/null
// 时若被误判成交互环境，就会卡在确认提示上等不到输入）。
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}
