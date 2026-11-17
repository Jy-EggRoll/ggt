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
// 唯一依据（面板按注册表生成，因此新增配置项不必再改页面代码），也是写入校验的依据。
// 元数据本身的自洽性（清单里的值必须能通过自己的 Normalize、整数边界与默认值是否相容等）
// 由 TestSettingMetadataIsComplete 守住
package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/eggokit/logger"
	// 主题偏好的默认值直接引用主题包里的常量，而不是在这里再抄一遍 "builtin:..." 这个 id 格式：
	// 那个前缀是主题包自己的约定，抄一份就会出现“改了前缀、默认值指到不存在的主题”
	"github.com/jy-eggroll/eggokit/theme"
	"github.com/jy-eggroll/ggt/internal/locales"
)

// ErrInvalidValue 表示用户输入的值不合法。
//
// 这里刻意不携带具体原因文案：合法取值的人类可读描述由 Setting.Expected 提供，
// 由调用方用统一模板渲染（见 normalizeValue / init 里的 Parse 包装）。若让每个
// Normalize 各返回一句散文，用户可见文案的数量会随校验规则数线性增长，
// 中英双语都得跟着维护。
var ErrInvalidValue = errors.New("invalid value")

// invalidValueError 把“取值非法”渲染成用户可见的那一句话。
//
// 运行期加载、config get、config set、config validate 四处共用这一处实现：
// 各写一份的话，同一份文件在不同命令下会给出不同说法，用户没法判断到底哪个才是真的
// （{"concurrency": true} 曾经就是“运行期当成并发数 1 跑、config get 却报错”的分叉）。
// 合法取值的描述统一取自 Setting.Expected，不再有第二条措辞来源。
func invalidValueError(s Setting, text string) error {
	return fmt.Errorf("%s", l10n.T("Invalid value for {{.Key}}: {{.Value}} (expected {{.Expected}})",
		map[string]any{"Key": s.Key, "Value": text, "Expected": l10n.Retranslate(s.Expected, nil)}))
}

// errDeprecatedConcurrency 表示写入的是旧版并发语义值（CPUHalf 等）。
//
// 单独成一个错误类别，是为了让两条路径给出符合事实的结论：config validate 把它报成
// 警告——运行期确实会按 auto 继续跑；config set 仍按普通非法值拒绝——用户正在显式
// 写一个新值，没有理由再把废弃词写回文件。
var errDeprecatedConcurrency = errors.New("deprecated concurrency value")

// Kind 描述配置项的取值类型。
type Kind string

const (
	KindString Kind = "string"
	KindBool   Kind = "bool"
	KindInt    Kind = "int"
	KindPaths  Kind = "paths" // 字符串数组
)

// ApplyAt 描述一项配置改完之后“何时生效”，是注册表里的一份面向用户的承诺。
//
// 为什么要声明在注册表而不是页面：用户在设置面板改完一项，最想知道的正是“现在生效了吗、
// 还要不要做点什么”。这份信息此前散在三处——cmd 包的 uiReloadKeys 记“改完要刷新页面的键”、
// settingNote 记“改完要重启的键”、其余靠默认立即生效。三处各写一份，加一项配置就可能漏掉提示。
// 收进注册表后，页面与命令行都从这里派生，只有一份真相。
//
// 它描述的是“对用户的生效承诺”，不是“页面怎么实现”：页面仍可自行决定拿到 reload 档时做什么
// （当前是自动刷新页面），但“这一项要不要刷新/重启”这个判断统一由这里给出。
type ApplyAt string

const (
	// ApplyImmediate 表示保存后立即生效。它是零值：绝大多数配置项都属于这一档，
	// 注册表里因此不必满屏显式标注，只在需要刷新或重启的少数项上打破默认。
	ApplyImmediate ApplyAt = "immediate"
	// ApplyReload 表示取值在渲染首页时被写进 HTML 或样式表，需刷新页面才看得到效果。
	ApplyReload ApplyAt = "reload"
	// ApplyRestart 表示取值在进程启动时定死（如语言由 l10n.Init 决定），需重启 ggt 才生效。
	ApplyRestart ApplyAt = "restart"
)

// normalized 把零值（注册表里未标注）规范成 ApplyImmediate。
//
// 注册表允许“不填即立即”，而对外（投影给页面、派生键名清单）只希望见到三个具体取值之一，
// 因此统一在这里归一，避免每处判断都写成“== \"\" || == ApplyImmediate”这种双重条件。
func (a ApplyAt) normalized() ApplyAt {
	if a == "" {
		return ApplyImmediate
	}
	return a
}

// 数值上限是给“误填”准备的跨平台安全边界，不是平台限流建议：
//   - concurrency 决定同时在跑的 git 子进程数。填得过大时信号量就失去了作用，
//     在跑的进程数趋近仓库数，先被耗尽的是进程数与文件描述符
//     （不是内存：chan struct{} 的占用与容量无关，实测一个容量 20 亿的 struct{} 通道只占约 112 字节（amd64））
//   - 分桶阈值若超过 2^31-1，64 位机器能写进去，386 二进制却解码失败，
//     会导致该平台上所有命令不可用
//
// maxConcurrency 与默认值上限 autoConcurrencyCeiling（见 config.go）是两个概念：
// 后者是默认保护值，保护本机响应与发热；前者是显式调高的安全边界，防止误填。
// 之所以留到 64 而不是更小：自建镜像与 GitHub 企业版的仓库规模、网络条件各不相同，
// 需要给“确实要调高”的人留出余量。
// GitHub 并未公布 git 协议（clone/fetch）的并发上限；其仓库限制文档只建议单个仓库的
// 读操作不超过每秒 15 次，而 ggt 的并发是“对不同仓库各发一次请求”，两者不是一回事，
// 因此不按那个数字收窄默认行为
const (
	maxConcurrency = 64
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
	// 由注册表生成，若连“这一项叫什么”都得页面自己写一份，新增一项就又要改一处页面文案。
	// 标题保持英文与 Expected 一致——两者都是嵌在本地化句子里的专有说明，先不为它们单独
	// 开一条本地化管线（页面自带的那张翻译表只管界面自身的文案）
	Title string
	// Kind 决定 get 的输出形态与体检时的类型检查。
	Kind Kind
	// Default 是 reset --defaults 写入的值，也是配置文件缺该键时的生效值。
	Default any
	// Expected 是合法取值的人类可读描述，用于拼装错误信息。
	Expected string
	// Normalize 是这一项唯一的取值入口：输入是配置文件里 JSON 解码后的原始值
	// （string、json.Number、bool、nil 或 []any），输出规范形态或错误。
	//
	// 运行期加载、config get、config validate、config set 四条路径全部经这里，
	// 类型判定因此只有一份实现。此前各路径各有一套规则，同一份文件会给出不同结论：
	// size_unit: "BINARY" 校验通过、运行期却按十进制；{"concurrency": true} 运行期按
	// 并发数 1 照跑、config get 与 validate 都拒绝。只收紧其中一条路径，问题只会从
	// “口径不一致”变成“运行期报错、校验仍说没问题”。
	//
	// 返回值一律是可直接交给 mapstructure 的规范类型（string、int、bool、[]string），
	// 中间形态（json.Number、[]any）不往下传。
	Normalize func(any) (any, error)
	// Parse 是文本入口（命令行与网页传来的字符串），由 init 派生自 Normalize，
	// 注册表里不再单独声明。这样“文本能写进去、文件里却读不出来”不可能发生：
	// 两个入口共用同一份规则，而不是两处各写一遍再靠测试对齐。
	// 唯一的例外是并发数的旧语义值：Normalize 返回 errDeprecatedConcurrency，让 validate
	// 能把“运行期还能跑、但该改写”与真正的非法值分成两个级别（见 validateKeys）。
	Parse func(string) (any, error)
	// ManagedBy 非空表示该项由别的命令管理：get 可读，set/reset 拒绝。
	// 仓库列表就属于这种——增删要走 ggt repo，那里有去重、git 仓库校验、路径规范化。
	ManagedBy string

	// ApplyAt 是这一项改完之后何时生效，供设置面板标注与保存后提示使用（见 ApplyAt 类型）。
	// 零值表示立即生效，因此只有需要刷新页面或重启 ggt 的项才显式标注。
	ApplyAt ApplyAt

	// Options 返回本项的候选取值，为空表示这一项只能自由输入。
	//
	// 刻意是函数而不是切片：候选取值分两类——写死的清单（size_unit 的 decimal 与 binary），
	// 与运行期才知道的清单（可用主题取决于目录扫描、可用语言取决于随二进制发布的语言包）。
	// 两者用同一种形状表达，读取方（网页设置面板、写入校验）因此不必为任何单项写特例，
	// 代价只是每次生成页面时求值一遍
	Options func() []Option
	// AllowCustom 表示候选取值之外还接受别的写法。
	// concurrency 就是这种：候选是 auto 与几个常用档位，1 到 64 的任意整数也合法。
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
		Key:     "concurrency",
		Title:   l10n.T("Concurrency", nil),
		Kind:    KindString,
		Default: DefaultConcurrency,
		// 这句文案里的 64 必须与 maxConcurrency 一起改：它是给用户看的允许范围，
		// 而提取器要求消息首参是字面量，没法在文案里插常量
		Expected:  l10n.T("auto or an integer from 1 to 64", nil),
		Normalize: concurrencyNormalizer("concurrency"),
		// auto 之外还接受 1 到 64 的任意整数，因此候选只是“能直接点的几个”，
		// 不是全部合法取值
		Options:     concurrencyOptions,
		AllowCustom: true,
		// 并发数在进程启动时一次性装进 worker 的共享信号量（见 worker.Init），
		// 运行中改配置换不掉它；标成“需重启”而不是让面板提示一个不会发生的“立即生效”
		ApplyAt: ApplyRestart,
	},
	{
		Key:      "ignore_submodules",
		Title:    l10n.T("Ignore submodules", nil),
		Kind:     KindBool,
		Default:  false,
		Expected: l10n.T("true or false", nil),
	},
	{
		Key:      "size_bucket_low_mb",
		Title:    l10n.T("Small file threshold", nil),
		Kind:     KindInt,
		Default:  500,
		Expected: l10n.T("a positive integer (max 1000000)", nil),
		// 边界与 Normalize 用的是同一份 Min/Max：Normalize 负责把关，边界让页面提前拦住，
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
		Min:      intPtr(1),
		Max:      intPtr(maxBucketMB),
	},
	{
		Key:      "size_unit",
		Title:    l10n.T("Size unit", nil),
		Kind:     KindString,
		Default:  "decimal",
		Expected: l10n.T("decimal or binary", nil),
		// 规范值由候选清单生成，合法取值因此只在 sizeUnitOptions 里写一遍
		Normalize: enumValueNormalizer("size_unit", sizeUnitOptions),
		Options:   sizeUnitOptions,
	},
	{
		Key:       "language",
		Title:     l10n.T("Language", nil),
		Kind:      KindString,
		Default:   locales.Default,
		Expected:  l10n.T("a supported language tag (see ggt --help for the current list)", nil),
		Normalize: languageValueNormalizer("language", languageOptions),
		// 语言清单取自 locales 包，注册表不另抄一份
		Options: languageOptions,
		// 语言在进程启动时由 l10n.Init 定死，改完必须重启 ggt——这一档当前是 language 与 log_level
		ApplyAt: ApplyRestart,
	},
	{
		Key:       "log_level",
		Title:     l10n.T("Log level", nil),
		Kind:      KindString,
		Default:   logger.DefaultLevelText(),
		Expected:  l10n.T("debug, info, warn, or error", nil),
		Normalize: logLevelValueNormalizer("log_level", logLevelOptions),
		Options:   logLevelOptions,
		// 级别在进程启动时由 logger.Init 定死（cmd/root.go 的 PersistentPreRunE），
		// 全仓没有第二处 Init，设置面板保存完也不会回灌，因此改完必须重启 ggt。
		// 级别来源链还有一层命令行 -v/-vv 优先级高于本项：运行期重设就得重放这条链，
		// 否则 `ggt web -vv` 的用户在面板上保存一个 warn，会把自己的调试输出关掉。
		// 两处合起来，这一档与 language 同属“启动时定死”，不假装立即生效
		ApplyAt: ApplyRestart,
	},
	{
		Key:   "theme",
		Title: l10n.T("Theme", nil),
		Kind:  KindString,
		// 默认值是空串 = 跟随系统深浅
		Default:   "",
		Expected:  l10n.T("a theme id, or empty to follow the system", nil),
		Normalize: stringValueNormalizer("theme", trimString),
		// 候选里含一个空值项，代表“跟随系统”：这一项允许清空这件事由清单本身表达，
		// 而不是另加一个字段
		Options: themeOptions,
		// 主题在渲染首页时被解析成 CSS 写进 <style>，改完要刷新页面才看得到
		ApplyAt: ApplyReload,
	},
	{
		// 这两个键对应 VSCode 的 workbench.preferredDarkColorTheme / preferredLightColorTheme：
		// “跟随系统”时深色用哪套、浅色用哪套，各自可选。它们只在 theme 为空（跟随系统）时生效，
		// 与 VSCode 里“自动检测关闭时 preferred* 被忽略”是同一个模型
		Key:       "theme_dark",
		Title:     l10n.T("Dark theme", nil),
		Kind:      KindString,
		Default:   theme.DefaultDarkID,
		Expected:  l10n.T("a theme id used when following the system and the system is dark", nil),
		Normalize: stringValueNormalizer("theme_dark", trimString),
		// 与 theme 的区别只在候选里有没有空值：这两个偏好为空时无从渲染，
		// 因此清单里不含空值，写入校验据此拒绝空串
		Options: themePreferenceOptions,
		// 与 theme 同一条理由：配色由服务端注入首页，改完要刷新页面
		ApplyAt: ApplyReload,
	},
	{
		Key:       "theme_light",
		Title:     l10n.T("Light theme", nil),
		Kind:      KindString,
		Default:   theme.DefaultLightID,
		Expected:  l10n.T("a theme id used when following the system and the system is light", nil),
		Normalize: stringValueNormalizer("theme_light", trimString),
		Options:   themePreferenceOptions,
		// 与 theme 同一条理由：配色由服务端注入首页，改完要刷新页面
		ApplyAt: ApplyReload,
	},
	{
		// 字体按“区域”分成两项，而不是给一个全局字体：同一个页面上，界面文字与等宽文字
		// （diff 正文、提交号、设置面板里的配置键名）本来就该是两套，一个全局值只能二选一。
		// 分区的边界取样式表里已有的两处落点，不新造概念（见 cmd/ui/style.css 的 --font-ui / --font-mono）
		Key:   "font_ui",
		Title: l10n.T("Interface font", nil),
		Kind:  KindString,
		// 空串 = 不指定字体：页面那层只声明“特性”（界面区 sans-serif、等宽区 monospace），
		// 具体落到哪个字体由浏览器按自己的设置回退决定。这是“有意义的零值”，
		// 因此默认值就是空串，不需要额外补值
		Default:   "",
		Expected:  l10n.T("a font-family list, or empty to leave the font to the browser", nil),
		Normalize: stringValueNormalizer("font_ui", parseFontFamily),
		// 刻意不给候选清单：本机装了什么字体只有使用者自己知道，内置一份清单等于替人预设审美，
		// 而清单里写到的字体在别人机器上多半没装。也不去枚举本机已装字体：三平台各要一套实现
		// （Linux 靠 fc-list、Windows 靠注册表、macOS 靠 system_profiler），代价远大于收益。
		// 面板因此是一个普通输入框，写什么字体栈由使用者决定，字面里出现字体名是使用者自己的选择
		// 字体栈在渲染首页时被拼进 <style>，改完要刷新页面
		ApplyAt: ApplyReload,
	},
	{
		// 与 font_ui 同一套口径：不给候选清单，留空则由浏览器挑一个等宽字体
		Key:       "font_mono",
		Title:     l10n.T("Code font", nil),
		Kind:      KindString,
		Default:   "",
		Expected:  l10n.T("a font-family list, or empty for the browser's monospace font", nil),
		Normalize: stringValueNormalizer("font_mono", parseFontFamily),
		// 与 font_ui 同一条理由：字体栈在渲染首页时被拼进 <style>，改完要刷新页面
		ApplyAt: ApplyReload,
	},
	{
		// 通知自动消失的时长，秒。0 表示不自动消失——这也是默认值：
		// 默认让提示留着，比默认把用户还没看完的提示收走更安全
		Key:      "notify_timeout",
		Title:    l10n.T("Notification timeout", nil),
		Kind:     KindInt,
		Default:  0,
		Expected: l10n.T("seconds before a notification closes itself (0 = never)", nil),
		Min:      intPtr(0),
		Max:      intPtr(maxNotifyTimeout),
		// 通知时长被页面读成一个常量（加载时就定下），改完要刷新页面才生效
		ApplyAt: ApplyReload,
	},
	{
		// 推送时附带标签。默认开：打好的附注标签本来就是要发出去的版本标记，
		// 推了代码却漏推标签，远端就少一个能对齐的点
		Key:      "push_tags",
		Title:    l10n.T("Push tags", nil),
		Kind:     KindBool,
		Default:  true,
		Expected: l10n.T("true or false", nil),
	},
	{
		// fetch / pull / sync 时修剪远程跟踪分支。默认开：远端删掉的分支若一直留在本地，
		// 分支列表会越来越长，界面上的领先/落后判断也拿它当依据。
		// 刻意的边界：这一项只管分支。对应的 git 选项因此是 --prune 而不是 --prune-tags，
		// 本地标签一律不自动修剪（标签是发布记录，被远程一次性带走属于事故）
		Key:      "prune_branches",
		Title:    l10n.T("Prune branches", nil),
		Kind:     KindBool,
		Default:  true,
		Expected: l10n.T("true or false", nil),
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

// KeysWithApplyAt 返回 ApplyAt 等于 at 的全部键名，顺序与注册表一致。
//
// 供 page 层派生“改完要刷新页面”这类键名清单：那份清单此前在 cmd 包手写了一份，
// 与注册表分处两个文件，配置项改名或删除后只会留下悬空的名字——不报错，只是提示永远不出现。
// 改为从这里派生后，清单只随注册表走。
func KeysWithApplyAt(at ApplyAt) []string {
	want := at.normalized()
	var out []string
	for _, s := range settings {
		if s.ApplyAt.normalized() == want {
			out = append(out, s.Key)
		}
	}
	return out
}

// parseConcurrency 解析并发数：语义值 auto（大小写不敏感）或 1 到 maxConcurrency 的整数。
// auto 统一存成小写规范形态，避免文件里出现 AUTO/Auto 这类同义异形写法。
// 空串按 auto 处理：显式写空串等于没设，与缺键走同一条路径，不另立一套默认值。
//
// 旧语义值返回 errDeprecatedConcurrency 而不是 ErrInvalidValue：调用方据此区分
// “运行期还能继续跑的废弃值”与“真正的非法值”（见 validate.go 的告警分级）。
func parseConcurrency(s string) (any, error) {
	v := strings.TrimSpace(s)
	if v == "" || strings.EqualFold(v, DefaultConcurrency) {
		return DefaultConcurrency, nil
	}
	if isDeprecatedConcurrencyValue(v) {
		return nil, errDeprecatedConcurrency
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > maxConcurrency {
		return nil, ErrInvalidValue
	}
	return strconv.Itoa(n), nil
}

// init 把文本入口接到 Normalize 上。
//
// Parse 与 Normalize 各写一份，就是“命令行能写进去、配置文件读不出来”的来源；
// 让 Parse 只做一次转发，两个入口的规则不可能分叉。管理型的项（仓库列表）不提供
// 文本入口：它们的增删要走 ggt repo，那里还有去重与 git 仓库校验。
func init() {
	for i := range settings {
		s := &settings[i]
		if s.Normalize == nil {
			// 整数、布尔、路径列表的规则只取决于 Kind（整数的范围取该项自己的 Min/Max），
			// 因此在这里按 Kind 绑定，注册表里不必逐项重抄一遍。
			// 字符串项的规则随语义不同（枚举、语言、字体栈），必须在注册表里显式声明。
			switch s.Kind {
			case KindInt:
				s.Normalize = intValueNormalizer(s.Key, s.Min, s.Max)
			case KindBool:
				s.Normalize = boolValueNormalizer(s.Key)
			case KindPaths:
				s.Normalize = pathsValueNormalizer(s.Key)
			default:
				panic(fmt.Sprintf("config: setting %q has no Normalize", s.Key))
			}
		}
		if s.ManagedBy != "" {
			continue
		}
		// Parse 只做一次转发：文本入口与 JSON 入口共用同一份规则，不可能分叉。
		// 语义错误在这里补上 Key 与 Expected，让 config set 的报错与其余路径同源。
		setting := *s
		normalize := setting.Normalize
		s.Parse = func(text string) (any, error) {
			out, err := normalize(text)
			if errors.Is(err, ErrInvalidValue) {
				return nil, invalidValueError(setting, text)
			}
			return out, err
		}
	}
}

// intPtr 返回整数的地址，用于表达“这一侧不设边界”（nil）与“边界就是 0”的区别
func intPtr(n int) *int { return &n }

// concurrencyOptions 返回设置面板能直接点选的档位：auto 与几个常用数字。
// 1 到 64 之间的任意整数同样合法，那属于自由输入，由 AllowCustom 表达，不逐一列出。
// auto 排在第一位，与默认值一致，面板打开时选中的就是它
func concurrencyOptions() []Option {
	return []Option{
		{Value: DefaultConcurrency},
		{Value: "4"},
		{Value: "8"},
		{Value: "16"},
		{Value: "32"},
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
// 因此这里手写一份，由 TestSettingMetadataIsComplete 断言“清单里每个值都能被 Normalize 接受”
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

// fontFamilyMaxLen 是字体栈的长度上限（按字符数，不是字节数——中文名字按字数算才符合直觉）。
//
// 上限不是洁癖：这个值会被拼进每一份首页的 <style> 里，几万字符的取值等于让每个请求都带上它；
// 而正常字体栈十几个名字也就一两百字符
const fontFamilyMaxLen = 200

// ValidFontFamily 判定一个字体栈取值能不能用，并返回规整后的形态（去掉首尾空白）。
// 空串合法，含义是“不指定字体，交给浏览器”而不是“没有字体”
//
// 为什么要限定字符集：这个值最终会被原样拼进样式表，分号能就地起一条新声明、花括号能闭合
// 整个 :root 块、反斜杠与尖括号是各种解析器的转义口子。而字体栈真正需要的东西很少——
// 字母（含中文）、数字、空格、逗号、连字符、下划线、点、单双引号，仅此而已
//
// 写入时（parseFontFamily）与渲染时（cmd/ui_theme.go 的 fontBlock）各用一次：
// 加载期已经会经 Normalize 校验，渲染侧仍保留一次判定作为纵深防御——
// 手工改过的配置若被别的路径塞进内存，也不该让样式表拼进分号或花括号
func ValidFontFamily(s string) (string, bool) {
	v := strings.TrimSpace(s)
	if v == "" {
		return "", true
	}
	if len([]rune(v)) > fontFamilyMaxLen {
		return "", false
	}
	quotes := 0
	for _, r := range v {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
		case r == ' ' || r == ',' || r == '-' || r == '_' || r == '.':
		case r == '"' || r == '\'':
			quotes++
		default:
			return "", false
		}
	}
	// 引号必须成对：落单的引号会把后面的字体名一起吞进同一个字符串里，页面上表现为
	// “字体忽然全变了”，而浏览器与 ggt 都不会报任何错
	if quotes%2 != 0 {
		return "", false
	}
	return v, true
}

// parseFontFamily 解析界面字体与等宽字体。合法取值的判断只有 ValidFontFamily 一处：
// 写入侧与渲染侧各写一套的话，“能写进配置文件、渲染时却被当成非法值丢掉”迟早会发生
func parseFontFamily(s string) (any, error) {
	v, ok := ValidFontFamily(s)
	if !ok {
		return nil, ErrInvalidValue
	}
	return v, nil
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
