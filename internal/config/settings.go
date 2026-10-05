// settings.go 定义 ggt 配置项的注册表。
//
// get / set / reset / validate 四个操作共用这一份声明，避免四处各写一套
// “这个键叫什么、什么类型、默认值是多少、什么算合法”——那种重复迟早会变成
// “set 能写进去、validate 说它非法”这类自相矛盾。
//
// 新增配置项时：在这里加一条，并在 Config 结构体上加同名的 json tag。
// 两处不一致会被 TestSettingsMatchConfigFields 的反射断言拦下。
//
// 同时要按需填上 Options / AllowCustom / Min / Max：这些元数据是网页设置面板生成控件的
// 唯一依据（面板按注册表长出来，因此新增配置项不必再改页面代码），也是写入校验的依据。
// 元数据本身的自洽性（清单里的值必须能通过自己的 Parse、整数边界与默认值是否相容等）
// 由 TestSettingMetadataIsComplete 守住
package config

import (
	"errors"
	"strconv"
	"strings"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/eggokit/logger"
	"github.com/jy-eggroll/ggt/internal/locales"
	// 主题偏好的默认值直接引用主题包里的常量，而不是在这里再抄一遍 "builtin:..." 这个 id 格式：
	// 那个前缀是主题包自己的约定，抄一份就会出现“改了前缀、默认值指到不存在的主题”
	"github.com/jy-eggroll/ggt/internal/theme"
)

// ErrInvalidValue 表示用户输入的值不合法。
//
// 这里刻意不携带具体原因文案：合法取值的人类可读描述由 Setting.Expected 提供，
// 由调用方用统一模板渲染（见 cmd/config.go）。若让每个 Parse 各返回一句散文，
// 用户可见文案的数量会随校验规则数线性增长，中英双语都得跟着维护。
var ErrInvalidValue = errors.New("invalid value")

// Kind 描述配置项的取值类型。
type Kind string

const (
	KindString Kind = "string"
	KindBool   Kind = "bool"
	KindInt    Kind = "int"
	KindPaths  Kind = "paths" // 字符串数组
)

// 数值上限不是洁癖，而是跨平台安全边界：
//   - concurrency 会一路传到 worker.Map 的 make(chan struct{}, concurrency)，
//     本项目发布 386 目标，若允许填 20 亿就会直接 OOM
//   - 分桶阈值若超过 2^31-1，64 位机器能写进去，386 二进制却解码失败，
//     会导致该平台上所有命令不可用
const (
	maxConcurrency = 1024
	maxBucketMB    = 1_000_000
	// maxNotifyTimeout 是通知自动消失时长的上界（秒）。
	// 它没有内存安全的理由，纯是“别让用户填一个等于永不消失的数”——那种意图应当直接填 0
	maxNotifyTimeout = 600
)

// Option 是配置项的一个候选取值。
//
// 用切片加结构体，而不是照抄 VSCode 那种“值数组 + 说明数组 + 显示名数组”的平行数组：
// 平行数组要求几份长度严格一致，长度不一致在 Go 里要运行期才暴露；用结构体切片，
// 对不齐这件事根本不可能发生
// Option 是某一个候选取值。
//
// 字段名带 json 标签而不是用 Go 的字段名（默认大写）：这份结构只有 WebUI 一个使用方，
// 页面按 o.value / o.label 取值。缺了标签时页面拿到的是 undefined——表现为下拉框里
// 一排空白选项，而服务端这边的任何测试都发现不了（契约横跨两种语言，只有跑起来才看得见）
type Option struct {
	// Value 是写进配置文件的值
	Value string `json:"value"`
	// Label 是页面上的显示名，空表示直接显示 Value。
	// 空值这一种特殊情况由页面按自己的文案补上（值本身是空串说明它代表“跟随系统”一类含义）
	Label string `json:"label"`
	// Note 是跟在候选取值后面的一句说明，空表示不显示
	Note string `json:"note,omitempty"`
	// Group 是候选值的分组标题，空表示不分组。
	// 主题自带来源分组（VSCode、Catppuccin 是各自的品牌名，因此不翻译）
	Group string `json:"group,omitempty"`
}

// Setting 描述一个配置项。
type Setting struct {
	// Key 与配置文件里的 JSON 键完全一致（snake_case），不引入第二套命名。
	Key string
	// Title 是给网页设置面板看的短标题
	//
	// 注册表里要放一句标题，是“新增配置项不必再改页面”这条要求的必然结果：面板的控件全部
	// 由注册表长出来，若连“这一项叫什么”都得页面自己写一份，新增一项就又要改一处页面文案。
	// 标题保持英文与 Expected 一致——两者都是嵌在本地化句子里的专有说明，先不为它们单独
	// 开一条本地化管线（页面自带的那张翻译表只管界面自身的文案）
	Title string
	// Kind 决定 get 的输出形态与体检时的类型检查。
	Kind Kind
	// Default 是 reset --defaults 写入的值，也是配置文件缺该键时的生效值。
	Default any
	// Expected 是合法取值的人类可读描述，用于拼装错误信息。
	Expected string
	// Parse 把命令行字符串转成待写入的 JSON 值，失败时返回 ErrInvalidValue。
	// 它同时是 set 的校验器与 validate 的校验器。
	Parse func(string) (any, error)
	// ManagedBy 非空表示该项由别的命令管理：get 可读，set/reset 拒绝。
	// 仓库列表就属于这种——增删要走 ggt repo，那里有去重、git 仓库校验、路径规范化。
	ManagedBy string

	// Options 返回本项的候选取值，为空表示这一项只能自由输入。
	//
	// 刻意是函数而不是切片：候选取值分两类——写死的清单（size_unit 的 decimal 与 binary），
	// 与运行期才知道的清单（可用主题取决于目录扫描、可用语言取决于随二进制发布的语言包）。
	// 两者用同一种形状表达，读取方（网页设置面板、写入校验）因此不必为任何单项写特例，
	// 代价只是每次生成页面时求值一遍
	Options func() []Option
	// AllowCustom 表示候选取值之外还接受别的写法。
	// concurrency 就是这种：既能点选 CPUHalf 这类语义串，也能直接写具体数字。
	// 页面据此把控件渲染成“输入框 + 候选”而不是纯下拉
	AllowCustom bool
	// Min / Max 是整数项的取值边界，nil 表示这一侧不设限。仅对 KindInt 有意义。
	//
	// 用指针而不是“0 表示不设限”，因为 0 本身是合法边界（notify_timeout 的下界就是 0）
	Min *int
	Max *int
}

// settings 是全部配置项的注册表，顺序即 ggt config validate 与帮助里的展示顺序。
//
// Title 与 Expected 一律写成 l10n.T(“英文原文”, nil) 的形态，有两个作用：
// 一是让这两段文案进得了语言文件——提取器只认源码里 T(...) 的字面量，写成加工过的变量
// 它就看不见（见 eggokit/l10n 的 scan 包注释）；
// 二是这里存下的就是英文源串本身，它同时是视图层与报错文案查表用的键
// （见 SettingView 的投影与 invalidValueError）。包级变量在 l10n.Init 之前构造，
// 此处求值只能拿到英文原文，所以不要假定这个字段已经翻译过——要用它的地方现查一次
var settings = []Setting{
	{
		Key:      "concurrency",
		Title:    l10n.T("Concurrency", nil),
		Kind:     KindString,
		Default:  DefaultConcurrency,
		Expected: l10n.T("CPUHalf, CPUFull, CPUQuarter, or a positive integer (max 1024)", nil),
		Parse:    parseConcurrency,
		// 三个语义串之外还接受任意正整数，因此候选只是“能直接点的几个”，
		// 不是全部合法取值
		Options:     concurrencyOptions,
		AllowCustom: true,
	},
	{
		Key:      "ignore_submodules",
		Title:    l10n.T("Ignore submodules", nil),
		Kind:     KindBool,
		Default:  false,
		Expected: l10n.T("true or false", nil),
		Parse:    parseBool,
	},
	{
		Key:      "size_bucket_low_mb",
		Title:    l10n.T("Small file threshold", nil),
		Kind:     KindInt,
		Default:  500,
		Expected: l10n.T("a positive integer (max 1000000)", nil),
		Parse:    parsePositiveInt(maxBucketMB),
		// 边界与解析器用的是同一个常量：解析器保证写得进去，边界让页面提前拦住，
		// 两者分开写就会出现“页面允许填、提交后被拒”
		Min: intPtr(1),
		Max: intPtr(maxBucketMB),
	},
	{
		Key:      "size_bucket_high_mb",
		Title:    l10n.T("Large file threshold", nil),
		Kind:     KindInt,
		Default:  800,
		Expected: l10n.T("a positive integer (max 1000000)", nil),
		Parse:    parsePositiveInt(maxBucketMB),
		Min:      intPtr(1),
		Max:      intPtr(maxBucketMB),
	},
	{
		Key:      "size_unit",
		Title:    l10n.T("Size unit", nil),
		Kind:     KindString,
		Default:  "decimal",
		Expected: l10n.T("decimal or binary", nil),
		// 解析器由候选清单生成，合法取值因此只在 sizeUnitOptions 里写一遍
		Parse:   enumParser(sizeUnitOptions),
		Options: sizeUnitOptions,
	},
	{
		Key:      "language",
		Title:    l10n.T("Language", nil),
		Kind:     KindString,
		Default:  locales.Default,
		Expected: l10n.T("a supported language tag (see ggt --help for the current list)", nil),
		Parse:    parseLanguage,
		// 语言清单取自 locales 包，注册表不另抄一份
		Options: languageOptions,
	},
	{
		Key:      "log_level",
		Title:    l10n.T("Log level", nil),
		Kind:     KindString,
		Default:  logger.DefaultLevelText(),
		Expected: l10n.T("debug, info, warn, or error", nil),
		Parse:    parseLogLevel,
		Options:  logLevelOptions,
	},
	{
		Key:   "theme",
		Title: l10n.T("Theme", nil),
		Kind:  KindString,
		// 默认值是空串 = 跟随系统深浅
		Default:  "",
		Expected: l10n.T("a theme id, or empty to follow the system", nil),
		Parse:    parseTheme,
		// 候选里含一个空值项，代表“跟随系统”：这一项允许清空这件事由清单本身表达，
		// 而不是另加一个字段
		Options: themeOptions,
	},
	{
		// 这两个键对应 VSCode 的 workbench.preferredDarkColorTheme / preferredLightColorTheme：
		// “跟随系统”时深色用哪套、浅色用哪套，各自可选。它们只在 theme 为空（跟随系统）时生效，
		// 与 VSCode 里“自动检测关闭时 preferred* 被忽略”是同一个模型
		Key:      "theme_dark",
		Title:    l10n.T("Dark theme", nil),
		Kind:     KindString,
		Default:  theme.DefaultDarkID,
		Expected: l10n.T("a theme id used when following the system and the system is dark", nil),
		Parse:    parseTheme,
		// 与 theme 的区别只在候选里有没有空值：这两个偏好为空时无从渲染，
		// 因此清单里不含空值，写入校验据此拒绝空串
		Options: themePreferenceOptions,
	},
	{
		Key:      "theme_light",
		Title:    l10n.T("Light theme", nil),
		Kind:     KindString,
		Default:  theme.DefaultLightID,
		Expected: l10n.T("a theme id used when following the system and the system is light", nil),
		Parse:    parseTheme,
		Options:  themePreferenceOptions,
	},
	{
		// 通知自动消失的时长，秒。0 表示不自动消失——这也是默认值：
		// 默认让提示留着，比默认把用户还没看完的提示收走更安全
		Key:      "notify_timeout",
		Title:    l10n.T("Notification timeout", nil),
		Kind:     KindInt,
		Default:  0,
		Expected: l10n.T("seconds before a notification closes itself (0 = never)", nil),
		Parse:    parseIntInRange(0, maxNotifyTimeout),
		Min:      intPtr(0),
		Max:      intPtr(maxNotifyTimeout),
	},
	{
		Key:       "repo_paths",
		Title:     l10n.T("Repositories", nil),
		Kind:      KindPaths,
		Default:   []string{},
		ManagedBy: "ggt repo",
	},
	{
		Key:       "parent_paths",
		Title:     l10n.T("Scanned folders", nil),
		Kind:      KindPaths,
		Default:   []string{},
		ManagedBy: "ggt repo",
	},
}

// Settings 返回全部配置项（副本，调用方改动不影响注册表）。
func Settings() []Setting {
	out := make([]Setting, len(settings))
	copy(out, settings)
	return out
}

// Lookup 按键名查找配置项。键名比较忽略大小写与首尾空白，
// 因为用户手敲时大小写很难记得准，而配置文件里的键本身不区分大小写。
func Lookup(key string) (Setting, bool) {
	k := strings.ToLower(strings.TrimSpace(key))
	for _, s := range settings {
		if s.Key == k {
			return s, true
		}
	}
	return Setting{}, false
}

// parseConcurrency 解析并发数：三种 CPU 相对语义（大小写不敏感）或正整数。
// 语义串统一存成规范大小写，避免文件里出现 cpuhalf/CPUHALF 这类同义异形写法。
func parseConcurrency(s string) (any, error) {
	v := strings.TrimSpace(s)
	switch strings.ToLower(v) {
	case "cpuhalf":
		return "CPUHalf", nil
	case "cpufull":
		return "CPUFull", nil
	case "cpuquarter":
		return "CPUQuarter", nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 || n > maxConcurrency {
		return nil, ErrInvalidValue
	}
	return strconv.Itoa(n), nil
}

// parseBool 解析布尔项，接受 true/false 与 1/0，大小写不敏感。
func parseBool(s string) (any, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	}
	return nil, ErrInvalidValue
}

// parseIntInRange 返回一个“整数且落在 [min, max] 内”的解析器。
//
// 下界做成参数而不是固定为 1，是因为有的项 0 是合法取值：通知自动消失的时长用 0 表示
// “不自动消失”，而分桶阈值必须为正，传 1 即可——两种语义共用一处实现，
// 解析器与各项自己的 Min/Max 才不会各写一份、各自对不上
func parseIntInRange(min, max int) func(string) (any, error) {
	return func(s string) (any, error) {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n < min || n > max {
			return nil, ErrInvalidValue
		}
		return n, nil
	}
}

// parsePositiveInt 返回一个“正整数且不超过 max”的解析器。
func parsePositiveInt(max int) func(string) (any, error) {
	return parseIntInRange(1, max)
}

// enumParser 由候选清单生成解析器：取值必须命中清单（大小写不敏感），
// 写进文件的永远是清单里的规范形态
//
// 用生成而不是各写一个 switch：合法取值在 switch 与候选清单里各留一份，两处迟早会对不上，
// 而对不上的表现正是“页面上能选、命令行却拒收”这种自相矛盾
func enumParser(options func() []Option) func(string) (any, error) {
	return func(s string) (any, error) {
		v := strings.TrimSpace(s)
		for _, o := range options() {
			if strings.EqualFold(v, o.Value) {
				return o.Value, nil
			}
		}
		return nil, ErrInvalidValue
	}
}

// intPtr 返回整数的地址，用于表达“这一侧不设边界”（nil）与“边界就是 0”的区别
func intPtr(n int) *int { return &n }

// concurrencyOptions 返回并发数能直接点选的三个语义值。
// 具体数字（如 "8"）也合法，但那属于自由输入，由 AllowCustom 表达，不列进候选
func concurrencyOptions() []Option {
	return []Option{
		{Value: "CPUHalf"},
		{Value: "CPUFull"},
		{Value: "CPUQuarter"},
	}
}

// sizeUnitOptions 是 MB 换算口径的两个取值
func sizeUnitOptions() []Option {
	return []Option{{Value: "decimal"}, {Value: "binary"}}
}

// languageOptions 返回随二进制发布的语言，清单与显示名都取自 locales 包。
// 注册表再抄一份语言列表，就会出现“l10n 支持三种语言、页面上只列两种”这种没人会发现的错位
func languageOptions() []Option {
	tags := locales.Supported()
	out := make([]Option, 0, len(tags))
	for _, tag := range tags {
		out = append(out, Option{Value: tag, Label: locales.DisplayName(tag)})
	}
	return out
}

// logLevelOptions 是诊断日志的四个级别。
//
// 合法取值的真相在 eggokit/logger（parseLogLevel 把判定直接交给它），而它没有导出级别清单，
// 因此这里手写一份，由 TestSettingMetadataIsComplete 断言“清单里每个值都能被 Parse 接受”
// 来防这种对不上——为拿到清单去改依赖库，代价比一条测试大得多
func logLevelOptions() []Option {
	return []Option{{Value: "debug"}, {Value: "info"}, {Value: "warn"}, {Value: "error"}}
}

// themeOptions 返回主题的候选取值：一个空值项（跟随系统）加上当前可用的全部主题。
// 清单每次现算，因为用户随时会往配置目录里丢主题文件
func themeOptions() []Option {
	return append([]Option{{Value: ""}}, availableThemeOptions()...)
}

// themePreferenceOptions 是 theme_dark / theme_light 的候选取值：比 themeOptions 少掉
// 那个空值项——这两个偏好的空串没有任何含义（跟随系统时没有配色可渲染，页面会一片无色）
func themePreferenceOptions() []Option {
	return availableThemeOptions()
}

// availableThemeOptions 把主题包给出的清单翻成候选取值。
// 显示名用主题自己的 name，分组标题用来源名；分组为空表示“用户自己放进去的”，
// 那个标题由页面按当前语言给（主题来源名是品牌名，不翻译）
func availableThemeOptions() []Option {
	themes := theme.Available(ThemeDirs())
	out := make([]Option, 0, len(themes))
	for _, t := range themes {
		out = append(out, Option{Value: t.ID, Label: t.Name, Group: t.Group})
	}
	return out
}

// parseLanguage 解析输出语言。
//
// 必须用 l10n.IsSupported 而不是 l10n.Normalize：后者对不认识的输入回退默认语言，
// 照搬会把用户输入的 fr 静默改写成 en——而“我要法语”和“我要英语”显然不是一回事。
// 校验通过后存入归一化结果，使文件里只有规范形态（zh 与 zh-Hans 都存成 zh-CN）。
func parseLanguage(s string) (any, error) {
	v := strings.TrimSpace(s)
	if !l10n.IsSupported(v, locales.Supported()) {
		return nil, ErrInvalidValue
	}
	return l10n.Normalize(v, locales.Supported(), locales.Default), nil
}

// parseTheme 解析网页看板选中的主题。
//
// 刻意不在这里校验主题是否存在：主题文件是用户随手粘贴、随时增删的，写进配置的值日后
// 可能指向一个已被删掉的文件；那种情况该在渲染页面时回退到跟随系统并告警（与 language
// 的未知取值一样），而不是让 ggt config set 当场拒绝——否则“先删旧主题、再设新主题”
// 这个再正常不过的顺序就做不成了。值可以是内置主题的 id（builtin: 前缀），
// 也可以是外部主题文件的绝对路径
func parseTheme(s string) (any, error) {
	return strings.TrimSpace(s), nil
}

// parseLogLevel 解析诊断日志级别。
//
// 合法取值的唯一真相刻意放在 eggokit/logger：这里复用它同时供“校验”与“运行期解析”用。
// 若在注册表里另立一套判定，两处迟早会对不上，表现为“set 说能写进去、运行期却回退默认级别”，
// 而那种矛盾没有任何测试能提前拦住。
//
// 写入时归一化为小写规范形态：logger 的解析大小写不敏感，允许用户写 WARN，
// 但文件里只该出现一种形态，否则同义异形会在 diff 里来回跳
func parseLogLevel(s string) (any, error) {
	level, err := logger.LogLevelFromString(s)
	if err != nil {
		return nil, ErrInvalidValue
	}
	return strings.ToLower(level.String()), nil
}
