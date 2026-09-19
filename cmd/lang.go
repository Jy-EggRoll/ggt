// lang.go 负责在命令行解析之前确定输出语言。
//
// 为什么必须提前于 cobra 解析：语言的生效范围包括各命令的 Short/Long 与 flag 说明，
// 而 cobra 的 --help 路径不会执行 PersistentPreRunE（那些文本要立即翻译好才能输出），
// 因此语言取值不能依赖 cobra 的解析结果，只能自行预扫描原始参数。
//
// 已知范围限制：本版本只支持命令行与配置文件两种来源，不读取 LANG/LC_ALL 等环境变量。
package cmd

import (
	"io"
	"os"
	"strings"

	"ggt/internal/config"
	"ggt/internal/i18n"
	"github.com/spf13/pflag"
)

// resolveLanguage 按优先级确定输出语言：
//
//	命令行 --lang/-l  >  配置文件 language 字段  >  默认语言（英文）
//
// 任何一步失败都静默降级、绝不返回错误：语言只影响展示，不该让命令整体失败。
// 尤其是配置文件损坏时也必须能正常输出帮助——这是 --help 路径会走到这里的前提。
func resolveLanguage() string {
	if lang := scanLangFlag(os.Args[1:]); lang != "" {
		return i18n.Normalize(lang)
	}
	// LoadLanguage 只做裸 JSON 读取，不会像 LoadConfig 那样在主目录不可用时退出进程
	if lang, err := config.LoadLanguage(); err == nil && lang != "" {
		return i18n.Normalize(lang)
	}
	return i18n.DefaultLanguage
}

// scanLangFlag 从原始命令行参数里预扫描 --lang/-l 的取值。
//
// 用 pflag 而不是手写循环，原因是手写极容易在两点上出错，而 pflag 已经处理妥当：
//   - 四种等价写法 --lang=en / --lang en / -l en / -len 都要能识别
//   - 独立的 -- 之后应当停止 flag 解析，其后的内容不能被当成 flag
//
// pflag 还会对未知 flag 做"剥离取值"处理（stripUnknownFlagValue），因此
// `ggt size --low 200` 里的 200 不会被误读成语言。
//
// 刻意忽略 Parse 返回的错误：pflag 是边解析边赋值的，即使后面遇到无法识别的参数而
// 报错，之前已经解析出的 --lang 值依然有效。
// args 传入的是不含程序名的参数切片（即 os.Args[1:]），单独作为参数是为了可测试。
func scanLangFlag(args []string) string {
	fs := pflag.NewFlagSet("lang-scan", pflag.ContinueOnError)
	// 允许出现未知 flag，否则子命令的 flag 会让预扫描直接失败
	fs.ParseErrorsWhitelist.UnknownFlags = true
	// 屏蔽 pflag 自身的报错输出，避免污染用户终端
	fs.SetOutput(io.Discard)

	var lang string
	fs.StringVarP(&lang, "lang", "l", "", "")
	_ = fs.Parse(args)

	return strings.TrimSpace(lang)
}

// 语言串的归一化与白名单校验统一在 i18n 包（i18n.Normalize / i18n.IsSupported）。
// 放在那里是因为 config 包也要用（config set language 需要严格校验），而 config
// 不能反向依赖 cmd。
