// lang.go 负责在命令行解析之前确定输出语言。
//
// 为什么必须提前于 cobra 解析：语言的生效范围包括各命令的 Short/Long 与 flag 说明，
// 而 cobra 的 --help 路径不会执行 PersistentPreRunE（那些文本要立即翻译好才能输出），
// 因此语言取值不能依赖 cobra 的解析结果，只能自行预扫描原始参数。
//
// 已知范围限制：本版本只支持命令行与配置文件两种来源，不读取 LANG/LC_ALL 等环境变量。
package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/ggt/internal/config"
	"github.com/jy-eggroll/ggt/internal/locales"
	"github.com/spf13/pflag"
)

// languageSource 说明一段语言取值来自哪里，只影响回退提示里的一句话。
type languageSource int

const (
	languageFromConfig languageSource = iota
	languageFromCommandLine
)

// languageNotice 描述“写下的语言没被接受、已回退默认语言”这件事。
//
// 为什么不在发现问题的当场打印：那一刻 l10n 还没初始化，文案只能是英文原文，
// 而用户接下来看到的界面可能是中文。这里只留下来源与原值，等 l10n.Init 之后
// 再按最终语言渲染一次。一次启动最多产生一条。
type languageNotice struct {
	source languageSource
	value  string
}

// text 按当前生效的语言渲染提示。
//
// 回退目标取自 l10n.Current() 而不是写死默认语言：它在 Init 之后就是本次运行真正
// 采用的标签，提示因此不会与实际界面语言不符。
func (n *languageNotice) text() string {
	value := strings.TrimSpace(n.value)
	if value == "" {
		// 与 concurrency 的报错同一写法：空值直接塞进句子会留下一个缺口
		value = "(empty)"
	}
	fallback := locales.DisplayName(l10n.Current())
	if n.source == languageFromCommandLine {
		return l10n.T("The --lang value is not supported: {{.Value}}. Falling back to {{.Fallback}}.",
			map[string]any{"Value": value, "Fallback": fallback})
	}
	return l10n.T("The language in the config file is not supported: {{.Value}}. Falling back to {{.Fallback}}.",
		map[string]any{"Value": value, "Fallback": fallback})
}

// chooseLanguage 按优先级挑出要使用的语言，并把没被接受的取值交回调用方提示。
//
//	命令行 --lang/-l  >  配置文件 language  >  空串（交由 l10n 用默认语言）
//
// 两条来源都经 config 里 language 项自己的 Normalize（与 config get/set/validate
// 同一份判定），这里不持有也不维护任何语言白名单：此前这条路径直接读原始 JSON、
// 由 l10n 层宽松回退，同一个取值在 config validate 里是 error、在“实际界面语言”上
// 却悄悄变成英文，用户得不到任何提示。
//
// 任何一步失败都只回退默认语言、绝不返回错误：语言只影响展示，不该让命令整体失败。
// 尤其是配置文件损坏时也必须能正常输出帮助——这是 --help 路径会走到这里的前提。
func chooseLanguage(args []string) (string, *languageNotice) {
	if raw := scanLangFlag(args); raw != "" {
		lang, err := config.ParseLanguage(raw)
		if err != nil {
			return "", &languageNotice{source: languageFromCommandLine, value: raw}
		}
		return lang, nil
	}

	// LoadLanguage 只读配置文件里的一个键，不会像 LoadConfig 那样在主目录不可用时退出进程
	lang, err := config.LoadLanguage()
	if err == nil {
		return lang, nil
	}
	// 只有“键在、但取值不被接受”才值得提示；文件不存在、读不出来或 JSON 非法时
	// 没有可回显的取值要讲，静默回退即可（配置文件的问题由各命令自己的加载流程报告）
	var invalid *config.InvalidLanguageError
	if errors.As(err, &invalid) {
		return "", &languageNotice{source: languageFromConfig, value: invalid.Raw}
	}
	return "", nil
}

// scanLangFlag 从原始命令行参数里预扫描 --lang/-l 的取值。
//
// 用 pflag 而不是手写循环，原因是手写极容易在两点上出错，而 pflag 已经处理妥当：
//   - 四种等价写法 --lang=en / --lang en / -l en / -len 都要能识别
//   - 独立的 -- 之后应当停止 flag 解析，其后的内容不能被当成 flag
//
// pflag 还会对未知 flag 做“剥离取值”处理（stripUnknownFlagValue），因此
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

// printLanguageNotice 把回退提示写到 stderr。
//
// 走 stderr 而不是 stdout：这段提示早于任何命令，而 config show / get 的 stdout 是给
// 脚本的（JSON、单个取值）。用 pterm 的告警样式而不是 logger，因为它与同屏的信息行
// 同属面向用户的输出（约定见 output.go）；输出流在这里显式指定，不依赖 pterm 的全局
// writer 设置——那些设置要等进入 Execute 之后才做。
func printLanguageNotice(notice *languageNotice) {
	if notice == nil {
		return
	}
	fmt.Fprintln(os.Stderr, WarnStr(notice.text()))
}

// 语言串的归一化与白名单校验统一在 i18n 包（l10n.Normalize / l10n.IsSupported）。
// 放在那里是因为 config 包也要用（config set language 需要严格校验），而 config
// 不能反向依赖 cmd。
