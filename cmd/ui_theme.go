// 本文件是看板主题的 ggt 侧：把某套 VSCode 主题解析成页面要的那组 CSS 变量，
// 并按配置生成页面字体的覆盖规则。字体与配色放同一处，是因为两者都是“服务端在渲染
// 首页时现算”的页面外观设置：同一份配置两次读取、两个注入点，改完都要刷新页面才生效
//
// 通用部分——JSONC 规整、include 链、type 判定、VSCode 颜色注册表默认值、内置主题文件——
// 全在 eggokit/theme，本文件只负责 ggt 自己的两件事：要哪些颜色 id，以及 VSCode 里没有
// 对应物的自有令牌取什么值。这条分界见 eggokit/theme 的包注释：那个包要整包搬进共享库
// eggokit 给别的项目复用，因此 ggt 的页面概念不能出现在它里面
package cmd

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/eggokit/logger"
	"github.com/jy-eggroll/eggokit/theme"
	"github.com/jy-eggroll/ggt/internal/config"
)

// cssVarNames 是“VSCode 颜色 id → 页面里的 CSS 变量名”的映射，只有这一处定义。
//
// 变量名沿用 style.css 里既有的短名（--git-modified 这些），而不是照抄 VSCode 的长名
// （--vscode-gitDecoration-modifiedResourceForeground 那种）：短名在样式表里读得更清楚，
// 而“哪个变量对应哪个 VSCode 令牌”看这张表就一目了然，映射关系不会因此变模糊
var cssVarNames = map[string]string{
	"editor.background":     "bg",
	"sideBar.background":    "card-bg",
	"foreground":            "text",
	"descriptionForeground": "text-dim",
	"list.hoverBackground":  "hover-bg",

	"badge.background": "badge-bg",
	"badge.foreground": "badge-fg",

	// 按钮、下拉框、输入框在 VSCode 里是各自独立的三组令牌（inputColors.ts），彼此取值
	// 可以毫无关系——实测 Catppuccin Latte 的 badge.background 是 #bcc0cc 而
	// button.background 是 #df8e1d。早期版本把 badge.background 当按钮底色的唯一样子、
	// 文字又沿用 --text，于是“深灰字压重蓝底”只在个别主题上出现（2026 Light 的
	// badge.background 恰好是重蓝 #0069CC，而它的 --text 是 #202020），排查时很难联想到
	// 是令牌职责混用。拆开之后按钮的底色与前景来自同一组令牌，不会再各自对不上
	"button.background":               "btn-bg",
	"button.foreground":               "btn-fg",
	"button.hoverBackground":          "btn-hover-bg",
	"button.secondaryBackground":      "btn-secondary-bg",
	"button.secondaryForeground":      "btn-secondary-fg",
	"button.secondaryHoverBackground": "btn-secondary-hover-bg",

	"dropdown.background": "dropdown-bg",
	"dropdown.foreground": "dropdown-fg",
	"dropdown.border":     "dropdown-border",

	"input.background": "input-bg",
	"input.foreground": "input-fg",
	// 上游的 input.border 在普通主题里是 null（inputColors.ts 里 dark 与 light 都是 null，
	// 只在高对比度主题才有值），所以这个变量只有高对比度主题才带得出值；普通主题下它由
	// themeBlock 自己兜一个种子再保证对比度，见 contrastPairs 里那一条
	"input.border": "input-border",

	// 焦点边框：样式表原来拿 git-modified（未暂存修改色）当输入框的聚焦色，
	// 那是“文件被改过”的语义，与“这个控件拿到了焦点”无关
	"focusBorder": "focus-border",

	// diff 面板改成浮层之后，需要“浮层底色”与“浮层阴影”两个语义：VSCode 给的是
	// editorWidget.background 与 widget.shadow（都在 editorColors.ts），
	// 不再拿 editor.background 与卡片边框硬凑
	"editorWidget.background": "panel-bg",
	"widget.shadow":           "widget-shadow",

	"gitDecoration.modifiedResourceForeground":      "git-modified",
	"gitDecoration.stageModifiedResourceForeground": "git-stage-modified",
	"gitDecoration.addedResourceForeground":         "git-added",
	"gitDecoration.untrackedResourceForeground":     "git-untracked",
	"gitDecoration.renamedResourceForeground":       "git-renamed",
	"gitDecoration.deletedResourceForeground":       "git-deleted",
	"gitDecoration.stageDeletedResourceForeground":  "git-stage-deleted",
	"gitDecoration.ignoredResourceForeground":       "git-ignored",
	"gitDecoration.conflictingResourceForeground":   "git-conflicting",
	// 子模块专用色。这份令牌在 4 套 catppuccin 主题里都有，却一直没被映射过。
	// 主题里没给值时由 CSS 回退到 --focus-border（见 style.css 的 .badge.sub），
	// 因此不像其它 gitDecoration 那样在 defaults.json 里给一份默认值
	"gitDecoration.submoduleResourceForeground": "git-submodule",

	"diffEditor.insertedLineBackground": "diff-add-bg",
	"diffEditor.removedLineBackground":  "diff-del-bg",

	// 行内（词级）高亮用的两档底色。它们与上面两个“整行底色”是 VSCode 里两组独立令牌
	// （见 editorColors.ts 的 diffInserted / diffRemoved）：一个铺满整行，一个只盖住行内变化的
	// 那几个字符。四者都是半透明色，后者叠在前者之上才是页面上的最终观感，
	// 所以这里也不能拿整行底色去凑——那会让行内高亮与整行一样深，等于看不出来
	"diffEditor.insertedTextBackground": "diff-add-text-bg",
	"diffEditor.removedTextBackground":  "diff-del-text-bg",

	// 分支图：泳道配色与引用配色用的是 VSCode 源码管理图那一套令牌（scmHistory.ts 的
	// colorRegistry 与三个 historyItem*RefColor）。五个前景色是上游写死的字面值，
	// 两个引用色一路引用到 charts.blue/purple → editorInfo.foreground，最终值同样照抄，
	// 见 eggokit/theme/defaults.json
	"scmGraph.foreground1":               "scm-graph-fg1",
	"scmGraph.foreground2":               "scm-graph-fg2",
	"scmGraph.foreground3":               "scm-graph-fg3",
	"scmGraph.foreground4":               "scm-graph-fg4",
	"scmGraph.foreground5":               "scm-graph-fg5",
	"scmGraph.historyItemRefColor":       "scm-graph-ref",
	"scmGraph.historyItemRemoteRefColor": "scm-graph-remote-ref",

	// 状态与控件层级：禁用前景、错误/警告/信息三种语义色、工具栏按钮的 hover、
	// 列表选中底色、键帽三件套、输入框占位符。取值逐条照抄上游注册表（见 defaults.json 的注释），
	// 它们都是“别处借不到”的语义——例如错误文本原先借的是 git-conflicting（“文件有合并冲突”）
	"disabledForeground":             "disabled-fg-token",
	"editorError.foreground":         "status-error",
	"editorWarning.foreground":       "status-warn",
	"editorInfo.foreground":          "status-info",
	"toolbar.hoverBackground":        "toolbar-hover-bg",
	"list.activeSelectionBackground": "list-selected-bg",
	// 选中行的底色与前景是一对令牌，必须一起映射。只映射底色时，浅色主题会把深灰字压在
	// 蓝底上：Light+ 与 Visual Studio Light 自己一个令牌都不写，全靠注册表默认值，
	// 于是提交图里选中那一行实测只有 1.01:1，等于读不出来
	"list.activeSelectionForeground": "list-selected-fg",
	"keybindingLabel.background":     "kbd-bg",
	"keybindingLabel.foreground":     "kbd-fg",
	"keybindingLabel.border":         "kbd-border",
	"input.placeholderForeground":    "input-placeholder",
	// 滚动条滑块三件套：不映射的话页面用的是浏览器默认滚动条，深色主题下会横着一条浅色的，
	// 是与 VSCode 观感差得最明显的一处。上游这三个都是半透明色（取值与来源见 defaults.json），
	// 正好能叠在任意底色上，不必按主题另调
	"scrollbarSlider.background":       "scrollbar",
	"scrollbarSlider.hoverBackground":  "scrollbar-hover",
	"scrollbarSlider.activeBackground": "scrollbar-active",
	// 选中文字的底色：浏览器默认那层半透明蓝压在深色主题的暗底上几乎看不出选区，
	// 而 diff 视图里最常见的动作就是选中一段代码再复制。取值照抄上游 editorColors.ts 的
	// editorSelectionBackground 注册表默认值（浅 #ADD6FF / 深 #264F78，见 defaults.json），
	// 只给底色、不设前景色——上游 editorSelectionForeground 的默认值就是 null（不改字色）
	"editor.selectionBackground": "selection-bg",
	// 浮层边框：上游默认是 null（深/浅两档都没值），因此它只做映射、不进 defaults.json——
	// 样式表里写成 var(--editor-widget-border, var(--card-border))，缺值时自然回落到卡片边框
	"editorWidget.border": "editor-widget-border",
}

// themeOwnVars 是 VSCode 里没有对应物的自有令牌，按明暗两套给出取值。
//
// 为什么只能由本项目的代码给：VSCode 的侧栏默认不画边框（sideBar.border 的默认值是 null），
// 而看板的卡片是 ggt 自己的设计；“（续 2）”那块续段标识更是只有 ggt 才有。
// 它们同样不写死在样式表里——样式表只用变量，取值仍然由这里（数据）提供
//
// 卡片边框（card-border）原来也在这张表里，但它的取值不需要按明暗分两套，现在改由当前主题
// 已有的令牌混出来（见 themeBlock），于是从这张表移走——少维护两组没有出处的十六进制
var themeOwnVars = map[theme.Type]map[string]string{
	theme.Dark: {
		"cont-bg": "#2d2d30",
		"cont-fg": "#d7ba7d",
	},
	theme.Light: {
		"cont-bg": "#efefef",
		"cont-fg": "#8a6d1f",
	},
}

// currentThemeID 读配置里选中的主题 id。
//
// 每次都从文件读，不读进程启动时那份缓存：换主题是运行期动作（页面上点一下就会写配置），
// 若读的是启动时的旧值，用户换完主题刷新页面看到的还是旧配色，会以为选择没生效
func currentThemeID() string {
	v, err := config.EffectiveAt(config.GetDefaultConfigPath(), themeKey)
	if err != nil {
		return ""
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// 主题相关的三个配置键。它们逐项对应 VSCode 的三个设置项：
//
//	theme        <- workbench.colorTheme（空串 = 自动跟随系统）
//	theme_dark   <- workbench.preferredDarkColorTheme
//	theme_light  <- workbench.preferredLightColorTheme
//
// 为什么是“一个显式指定 + 两个偏好”而不是“跟随/指定开关 + 一个 id”：VSCode 的模型正是前者——
// 自动检测开着时用的不是某一套写死的主题，而是用户分别指定的深、浅两套；关掉之后才由
// colorTheme 说了算。照抄它意味着“跟随系统”也能定制，而不是只能用内置的那两套
const (
	themeKey      = "theme"
	themeDarkKey  = "theme_dark"
	themeLightKey = "theme_light"
)

// themePref 读一个“跟随系统”下的主题偏好，读不到或写成空串就回落到内置默认。
//
// 与 currentThemeID 的差别在于空串的含义：theme 的空串是“跟随系统”，是有意义的取值；
// 而这两个偏好一旦为空就无从渲染，必须回落到默认，故两者不能共用一个函数
func themePref(key, fallback string) string {
	v, err := config.EffectiveAt(config.GetDefaultConfigPath(), key)
	if err != nil {
		return fallback
	}
	s, _ := v.(string)
	if s = strings.TrimSpace(s); s == "" {
		return fallback
	}
	return s
}

// resolveTheme 解析出本次页面渲染该用的配色 CSS。
//
// 选中的主题解析失败时（文件被删了、内容写坏了）回退到跟随系统并记一条日志：
// 页面不该因为一个坏主题文件就整页没有颜色，而“配色悄悄变回默认”这种事必须留下痕迹
//
// 只返回 CSS：主题的候选清单与当前选择现在由设置面板自己取（/api/settings），
// 不再随首页注入一份同样的数据——同一件事只有一处来源
func resolveTheme() string {
	dark := themePref(themeDarkKey, theme.DefaultDarkID)
	light := themePref(themeLightKey, theme.DefaultLightID)
	current := currentThemeID()
	if current == "" {
		return systemThemeCSS(dark, light)
	}
	resolved, err := theme.Resolve(current)
	if err != nil {
		logger.Warn(l10n.T("Failed to load the selected theme; following the system instead", nil),
			"theme", current, "error", err)
		return systemThemeCSS(dark, light)
	}
	return themeBlock(resolved)
}

// 字体相关的两个配置键，与样式表里的两个变量一一对应：
//
//	font_ui   -> --font-ui    界面文字：看板的仓库名与提交信息、设置面板的说明文字
//	font_mono -> --font-mono  等宽文字：diff 正文、提交号、设置面板里的配置键名
//
// 它们不是主题令牌：VSCode 里字体与配色分属两个设置项（editor.fontFamily 与
// workbench.colorTheme），颜色注册表里也没有任何字体项可映射，因此这里直接读配置
const (
	fontUIKey   = "font_ui"
	fontMonoKey = "font_mono"
)

// fontBlock 生成本次页面渲染该用的字体覆盖规则，没有可覆盖的项时返回空串。
//
// 取值由用户提供、最终原样拼进样式表，所以这里对文件里的值再过一次合法性判定
// （config.ValidFontFamily）：注册表的 Parse 只在写入时把关，而渲染读的是配置文件里的
// 原始值，手改配置文件那条路绕过 Parse。不合法时丢掉这一项并记日志——页面随之回到
// “不指定字体”的默认状态（界面区继承浏览器默认字体、等宽区 monospace），而配置里的值
// 没有生效这件事必须留下痕迹，否则手改文件写坏一个字符，页面上只会看到字体悄悄变了
//
// 空串表示“不指定字体，交给浏览器”，此时不输出：写出 --font-ui: 这种空声明虽然会被浏览器丢掉、
// 级联也会继续用样式表里的值（界面区 inherit、等宽区 monospace），但依赖这种回退行为不如显式跳过
func fontBlock() string {
	var b strings.Builder
	for _, it := range []struct{ key, name string }{
		{fontUIKey, "--font-ui"},
		{fontMonoKey, "--font-mono"},
	} {
		v, err := config.EffectiveAt(config.GetDefaultConfigPath(), it.key)
		if err != nil {
			continue
		}
		raw, _ := v.(string)
		family, ok := config.ValidFontFamily(raw)
		if !ok {
			logger.Warn(l10n.T("Ignoring a font family in the configuration that is not a plain font list", nil),
				"key", it.key, "value", raw)
			continue
		}
		if family == "" {
			continue
		}
		b.WriteString(it.name)
		b.WriteString(":")
		b.WriteString(family)
		b.WriteString(";")
	}
	if b.Len() == 0 {
		return ""
	}
	// 与主题那条规则同样只写变量、不写任何颜色或字体字面量：样式表里只有变量的使用者，
	// 这里的值就是唯一来源
	return ":root{" + b.String() + "}"
}

// systemThemeCSS 是“跟随系统”时的样式：把深、浅两套都写进去，浅色那份套在
// prefers-color-scheme 里，由浏览器自己按系统设置挑——服务端问不到你的系统是深是浅。
//
// 哪一套是深、哪一套是浅由**角色**决定（theme_dark 进基础规则、theme_light 进浅色媒体查询），
// 不看主题自己声明的明暗类型：这是 VSCode 的语义——preferredDarkColorTheme 说的是“系统是深色时
// 用哪套”，用户可以真的把一套浅色主题填进去，那时它照样只在系统为深色时生效。
// 主题自己的 type 仍然决定它的自有令牌取哪一档（见 themeOwnVars），两件事互不干扰。
//
// 配置里的 id 指到不存在或解析不了的主题时回落到内置默认并记日志：与“选中的主题解析失败”
// 同一处理，页面不该因为配置里一个写坏的 id 就整页没有颜色
func systemThemeCSS(darkID, lightID string) string {
	var b strings.Builder
	for _, p := range []struct {
		role     string
		id       string
		fallback string
	}{
		{"dark", darkID, theme.DefaultDarkID},
		{"light", lightID, theme.DefaultLightID},
	} {
		resolved, err := theme.Resolve(p.id)
		if err != nil && p.fallback != p.id {
			logger.Warn(l10n.T("Failed to load the preferred theme; using the built-in default", nil),
				"role", p.role, "theme", p.id, "error", err)
			resolved, err = theme.Resolve(p.fallback)
		}
		if err != nil {
			// 内置主题也解析不了，只可能是打包出了问题（单测里覆盖了），此时什么都不注入，
			// 页面会因为没有颜色变量而呈现为不可读——这正是我们想要的“响亮地失败”
			logger.Error(l10n.T("Failed to resolve a built-in theme", nil), "theme", p.id, "error", err)
			continue
		}
		if p.role == "light" {
			b.WriteString("@media (prefers-color-scheme: light){")
			b.WriteString(themeBlock(resolved))
			b.WriteString("}")
			continue
		}
		b.WriteString(themeBlock(resolved))
	}
	return b.String()
}

// contrastPair 是一处“前景压在底色上”。
//
// surface 是这些底色实际落着的表面：底色带透明度时，必须先按透明度合成到它上面再判定。
// 把半透明底色当成不透明色来算，得出的是偏乐观的错值——2026 那两套主题的选中底色正是
// 半透明的（#00000025 / #ffffff25），漏合成就成了“算出来 11.6:1、看着只有 3.3:1”。
// 底色本来就不透明时这项不影响结果，留空即按页面底色 --bg 合成
//
// min 是这一对要求的门槛，留空按正文文字的 theme.MinContrast。非文字的门槛更低：WCAG 2.2
// 的 1.4.3 要求正文 4.5:1，1.4.11 对图形与控件边界只要求 3:1
type contrastPair struct {
	fg      string
	bgs     []string
	surface string
	min     float64
}

// minNonTextContrast 是「不是文字、但读不读得出决定信息能否传达」的那类元素的门槛。
// 控件边界与文件类型图标都按它判：WCAG 2.2 的 1.4.3 要求正文 4.5:1，
// 1.4.11 Non-text Contrast 对图形与控件边界只要求 3:1
// 出处：https://www.w3.org/TR/WCAG22/#non-text-contrast
const minNonTextContrast = 3.0

// badgeMixRatio 是变体徽章把语义色掺进徽章底的比例。
//
// 60% 是量出来的：低于它色相看不出来，高于它文字就压不住。这里与样式表原来那个
// color-mix 的 60% 是同一个数，改动只是把混色从样式表搬到 Go（见 applyBadgeMixFixes）
const badgeMixRatio = 0.6

// maxRowTintRatio 是文件行状态底色里最浓的那一档（g-index 是 18%，见 style.css 的
// .row.file.g-* 三条规则）。判断文件图标读不读得出来时按最浓的一档算，
// 于是三档一起达标
const maxRowTintRatio = 0.18

// headRowTintRatio 是工作树头行那层淡染的浓度（style.css 的 .row.head.wt 是
// color-mix(text-dim 6%, transparent)）
const headRowTintRatio = 0.06

// contrastPairs 记录“哪些前景会被铺在哪些底色上”。
//
// 放在这里而不是 eggokit/theme：哪些元素成对出现属于本项目的页面概念，而那个包要整包搬进
// 共享库给别的项目复用，不能带上 ggt 的页面知识（见该包的包注释）
//
// hover 底色也算进来：按钮的前景在常态与 hover 两种底色下都得读得出来。主题没给
// button.hoverBackground 时它不在 vars 里，这一项自然跳过——那时样式表回落到
// list.hoverBackground，是个中性色，风险低得多
var contrastPairs = []contrastPair{
	{fg: "btn-fg", bgs: []string{"btn-bg", "btn-hover-bg"}},
	{fg: "btn-secondary-fg", bgs: []string{"btn-secondary-bg", "btn-secondary-hover-bg"}},
	{fg: "dropdown-fg", bgs: []string{"dropdown-bg"}},
	{fg: "input-fg", bgs: []string{"input-bg"}},
	// 徽章不在这张表里，另见 applyBadgeFixes：它们背后的表面不是一种（卡片底、hover 叠色、
	// 工作树头行的淡染），而这张表一个 contrastPair 只能指定一个 surface
	// 键入提示的键帽与输入框占位符也要能读出来：它们同样是“前景压在底色上”。
	// 键帽的底色在上游就带透明度，而它只出现在浮层卡片的头部，表面是卡片底色
	{fg: "kbd-fg", bgs: []string{"kbd-bg"}, surface: "panel-bg"},
	{fg: "input-placeholder", bgs: []string{"input-bg"}},
	// 选中行：底色与前景成对，这里再兜一层。注册表默认值已经保证这一对齐全，而主题自己
	// 写了值时未必协调——2026 那两套的选中底色是半透明的，只有合成到卡片底色上算，
	// 才看得出 #757575 压在它上面读不出来
	{fg: "list-selected-fg", bgs: []string{"list-selected-bg"}, surface: "panel-bg"},
	// 输入控件的边界：样式表里 input/select/textarea 原本共用 card-border，而 card-border 是按
	// “轻淡的分隔线”配的（正文 12% 混进卡片底），对装饰够用，对“此处能输入”这个信息不够。
	// 上游本就故意不画这条边（见 input.border 映射处的注释），所以只能自己兜。实测 12 套主题
	// 全部低于 1.4:1，等于输入框完全看不出边界。
	//
	// 只要求它对着控件外面那层底色读得出来，不要求再与控件自己的填色拉开：后者是同一条视觉
	// 信息，强行拉会让每个输入框都变成一道重框
	{fg: "input-border", bgs: []string{"card-bg", "panel-bg"}, min: minNonTextContrast},
}

// badgeSurfaceMixes 是「底色由语义色混出来」的那几枚徽章：子模块、领先 N、工作树。
//
// 三枚同一个构造：seed 是本项目的语义令牌，name 是发给样式表的不透明底色变量，
// fallback 是上游没给该键时的回落令牌（与样式表原来的回落保持一致）。
//
// 混色从样式表搬到 Go 的唯一理由是**可校验**：底色在样式表里现算，校验表就只认识
// badge-bg，混出来的三种底色从来没被看过一眼——Catppuccin Latte 下工作树 2.00:1、
// 子模块 2.45:1、领先 3.10:1 全部漏了过去
var badgeSurfaceMixes = []struct {
	name     string
	seed     string
	fallback string
}{
	{"badge-sub-bg", "git-submodule", "focus-border"},
	{"badge-ahead-bg", "git-added", ""},
	{"badge-wt-bg", "text-dim", ""},
}

// mixInto 把 seed 按 ratio 混进 base，返回**不透明**色。
//
// 必须不透明：徽章压在行底色上，而行底色不止一种（普通行、三种状态淡染行、hover 叠色），
// 半透明的混色底最后成什么颜色由这一层决定，对比度也就无从守住。先合成成具体色值，
// 徽章底就与背后的行无关了
//
// 混法照 CSS color-mix 的语义：把 seed 自身的透明度乘上 ratio，再合成到 base 上。
// 主题作者常把前景色写成带透明度的形式，这一步同时把「它占多少份量」算对
func mixInto(seed, base string, ratio float64) string {
	return theme.Over(scaleAlpha(seed, ratio), base)
}

// scaleAlpha 把十六进制颜色的透明度乘以 factor，其余部分不动。
//
// 只认十六进制：eggokit 没有导出「设透明度」的接口，而 #rrggbb 这套写法覆盖本项目全部主题
// 令牌。别的写法（rgb() 之类）原样返回，theme.Over 会按它自带的透明度合成——结果同样是不
// 透明色，只是「掺 60%」这档份量退化成它原本的透明度。这样比在 ggt 里再写一份完整的颜色
// 解析要好：那份活儿属于 eggokit/theme（见 seeThrough 里同样的取舍）
func scaleAlpha(c string, factor float64) string {
	if !strings.HasPrefix(c, "#") {
		return c
	}
	h := c[1:]
	switch len(h) {
	case 3, 4:
		// #rgb / #rgba 先补成 #rrggbb / #rrggbbaa
		expanded := make([]byte, 0, len(h)*2)
		for i := 0; i < len(h); i++ {
			expanded = append(expanded, h[i], h[i])
		}
		h = string(expanded)
	case 6, 8:
	default:
		return c
	}
	a := 1.0
	if len(h) == 8 {
		v, err := strconv.ParseUint(h[6:8], 16, 8)
		if err != nil {
			return c
		}
		a = float64(v) / 255
	}
	a *= factor
	if a < 0 {
		a = 0
	}
	if a > 1 {
		a = 1
	}
	return "#" + h[:6] + fmt.Sprintf("%02x", int(math.Round(a*255)))
}

// worstRatio 返回 fg 压在 bgs 里最难读的那一档上的对比度
func worstRatio(fg string, bgs []string) float64 {
	worst := math.MaxFloat64
	for _, bg := range bgs {
		if r := theme.Contrast(fg, bg); r < worst {
			worst = r
		}
	}
	return worst
}

// ensureContrastRounded 在 EnsureContrast 之后再按**取整后的真实色值**复核，不够就抬高一档目标重来。
//
// 为什么要复核：EnsureContrast 挑的是“刚过线”的那一档明度，判线时用未取整的通道值，返回前却把
// 通道四舍五入成整数（见 eggokit/theme/contrast.go 的 hex）。这一步取整会把结果拉回线下——
// 实测 Catppuccin Frappe 的领先徽章 4.49:1、dark_modern 悬停时的错误徽章 4.4997:1。这是取整的
// 必然边界，不是谁算错了，所以在调用方补一档，而不是去改那个共用函数（它按未取整值判定本身是对的）
//
// 复核不过时**不能只是把结果喂回去**：每次都从当前明度取“第一个刚过线的档”，喂回去仍停在同一条
// 边界上，四舍五入后还是差那一丝。要抬高目标值（多要 0.05、再要 0.15），它才会真的往前走一档
func ensureContrastRounded(fg string, bgs []string, min float64) (string, bool) {
	cur, changed := fg, false
	for _, target := range []float64{min, min + 0.05, min + 0.15} {
		next, ok := theme.EnsureContrast(cur, bgs, target)
		if !ok {
			return cur, changed
		}
		cur, changed = next, true
		if worstRatio(cur, bgs) >= min {
			return cur, changed
		}
	}
	return cur, changed
}

// headRowSurfaces 返回徽章实际落着的那几种底色（不透明）。
//
// 徽章只出现在 .row.head（仓库头行），而 .row 自己不铺底色：行是透明的，所以徽章背后就是
// 卡片底 --card-bg；工作树头行再叠 6% 的 --text-dim（见 style.css 的 .row.head.wt），
// 鼠标掠过叠 --hover-bg。
//
// 为什么要把这几种都算上：badge.background 在部分主题里是**半透明**的（dark_modern 就是），
// 它最后成什么颜色由这一层决定。只按 --bg 一种表面判，会漏掉“hover 时才读不出来”这种情形——
// 实测 dark_modern 悬停时 4.50:1，差 0.0003
func headRowSurfaces(vars map[string]string) []string {
	card := vars["card-bg"]
	if card == "" {
		card = vars["bg"]
	}
	if card == "" {
		return nil
	}
	plain := []string{card}
	if dim := vars["text-dim"]; dim != "" {
		plain = append(plain, mixInto(dim, card, headRowTintRatio))
	}
	surfaces := append([]string{}, plain...)
	if hover := vars["hover-bg"]; hover != "" {
		for _, s := range plain {
			surfaces = append(surfaces, theme.Over(hover, s))
		}
	}
	return surfaces
}

// applyBadgeFixes 让徽章文字在徽章底色上读得出来：普通徽章（计数）、错误徽章，以及
// 三枚变体徽章的底色。
//
// 徽章不放进 contrastPairs，因为它背后的表面不止一种（卡片底、hover 叠色、工作树头行淡染），
// 而那张表一个条目只能指定一个 surface。只按 --bg 判会漏判：dark_modern 的 badge.background
// 是半透明的，悬停时错误徽章文字只有 4.50:1
//
// 错误徽章的文字种子优先取主题的错误色，上游没给时回落到冲突色（与样式表原来的回落一致）。
// 特意不把 status-error 本身拉进校验表：它还在别处当错误文本用，动它会波及其它表面
func applyBadgeFixes(vars map[string]string) int {
	base := vars["badge-bg"]
	if base == "" {
		return 0
	}
	if vars["badge-err-fg"] == "" {
		if v := vars["status-error"]; v != "" {
			vars["badge-err-fg"] = v
		} else if v := vars["git-conflicting"]; v != "" {
			vars["badge-err-fg"] = v
		}
	}
	surfaces := headRowSurfaces(vars)
	if len(surfaces) == 0 {
		return 0
	}
	adjusted := 0
	for _, name := range []string{"badge-fg", "badge-err-fg"} {
		fg := vars[name]
		if fg == "" {
			continue
		}
		bgs := make([]string, 0, len(surfaces))
		for _, s := range surfaces {
			bgs = append(bgs, theme.Over(base, s))
		}
		if fixed, changed := ensureContrastRounded(fg, bgs, theme.MinContrast); changed {
			vars[name] = fixed
			adjusted++
		}
	}
	return adjusted
}

// applyBadgeMixFixes 给三枚变体徽章算出不透明底色，并让主题给的文字色在这些底色上读得出来。
//
// 为什么调的是**底色**而不是文字色：徽章文字共用 --badge-fg，它还被别处引用、不能动；
// 而且深色主题里它是白色，白色没法再往亮调。对比度是对称的，谁当前景不影响比值大小，
// 所以这里反过来把混出来的底色当「前景」传给 EnsureContrast，让它沿明度轴（色相与饱和度
// 都不动）挪到刚过 4.5:1——观感就是徽章底比原来深一点或浅一点，文字照旧是主题那一档
func applyBadgeMixFixes(vars map[string]string) int {
	fg, base := vars["badge-fg"], vars["badge-bg"]
	if fg == "" || base == "" {
		return 0
	}
	adjusted := 0
	for _, m := range badgeSurfaceMixes {
		seed := vars[m.seed]
		if seed == "" && m.fallback != "" {
			seed = vars[m.fallback]
		}
		if seed == "" {
			// 语义色与回落都没有：不发这个变量，样式表回落到 badge-bg，与改动前一样
			continue
		}
		mixed := mixInto(seed, base, badgeMixRatio)
		if fixed, changed := ensureContrastRounded(mixed, []string{fg}, theme.MinContrast); changed {
			mixed = fixed
			adjusted++
		}
		vars[m.name] = mixed
	}
	return adjusted
}

// applyFileIconFix 给文件类型图标挑一个在它实际落着的行底色上都读得出的颜色。
//
// 图标按非文字算，门槛 3:1。行底色不止一种：普通行是卡片底色，状态行是在卡片底色上按
// 9% / 12% / 18% 掺进各自的 gitDecoration 色（见 style.css 的 .row.file.g-*），hover 再叠
// 一层。取最差的那个判：最差的一层过了，其余都过
//
// 种子取 --text-dim（descriptionForeground）：文件类型图标是「次要信息」那一档的装饰，
// 主题对它的意图就是这个色；不够读时才挪明度
func applyFileIconFix(vars map[string]string) int {
	seed, card := vars["text-dim"], vars["card-bg"]
	if seed == "" || card == "" {
		return 0
	}
	plain := []string{card}
	for _, name := range []string{
		"git-modified", "git-added", "git-deleted", "git-untracked", "git-renamed",
		"git-conflicting", "git-ignored", "git-stage-modified", "git-stage-deleted",
	} {
		if c := vars[name]; c != "" {
			plain = append(plain, mixInto(c, card, maxRowTintRatio))
		}
	}
	if sel := vars["list-selected-bg"]; sel != "" {
		plain = append(plain, theme.Over(sel, card))
	}
	// hover 是叠在行底色之上的一层，四种底色各自都要算：只算普通行会漏掉「选中再 hover」
	surfaces := append([]string{}, plain...)
	if hover := vars["hover-bg"]; hover != "" {
		for _, s := range plain {
			surfaces = append(surfaces, theme.Over(hover, s))
		}
	}
	if fixed, changed := ensureContrastRounded(seed, surfaces, minNonTextContrast); changed {
		vars["file-icon"] = fixed
		return 1
	}
	vars["file-icon"] = seed
	return 0
}

// applyContrastFixes 在对比度实在不够时只调那一处颜色的明度，返回调整过的项数。
// 调的对象包括文字前景与控件边界：两者都是“压在底色上、读不出来就得挪”，只是门槛不同
//
// 底色一律不动：那是主题的设计。改上游色值等于自己维护一份主题副本，上游一升级就得重做，
// 每套主题还都要各修一遍；而这个函数对任何主题都成立（见 eggokit/theme/contrast.go）
//
// 半透明底色先按透明度合成到它实际落着的表面上（theme.Over）再交给 EnsureContrast：
// 不合成的话，一层浅灰雾会被当成它字面上那个深色来算，比值虚高。这不是假想的错法——
// 键帽底色 #8080802b 曾被当成不透明 #808080，于是 #cccccc 被“修正”成 #161616，
// 而它实际压在深色浮层上只剩 1.02:1，比不修还糟
func applyContrastFixes(vars map[string]string) int {
	adjusted := 0
	for _, p := range contrastPairs {
		fg, ok := vars[p.fg]
		if !ok {
			continue
		}
		surface := vars[p.surface]
		if p.surface == "" {
			surface = vars["bg"]
		}
		bgs := make([]string, 0, len(p.bgs))
		for _, name := range p.bgs {
			v, ok := vars[name]
			if !ok {
				continue
			}
			if surface != "" {
				v = theme.Over(v, surface)
			}
			bgs = append(bgs, v)
		}
		if len(bgs) == 0 {
			continue
		}
		min := p.min
		if min == 0 {
			min = theme.MinContrast
		}
		if fixed, changed := ensureContrastRounded(fg, bgs, min); changed {
			vars[p.fg] = fixed
			adjusted++
		}
	}
	return adjusted
}

// seeThrough 判断一个颜色会不会透出它压着的底色：半透明与全透明都算。
//
// 这种颜色拿来做控件边界是没有用的：它最后有多深由底色决定，透明度锁死了它能拉开的差距。
// 2026 Light 的 input.border 是 #00000066，压在浅底上最多只到 2.73:1，再怎么挪它的明度也
// 上不去——所以要挑种子时就跳过它，换一个不透明的颜色来兜
//
// 判法是“压在黑上与压在白上结果是否不同”：不同就说明底色透了出来。这样不必在 ggt 里重新
// 实现一遍颜色解析（那份活儿属于 eggokit/theme）；解析不了的值会原样返回、两边相同，一并排除
func seeThrough(v string) bool {
	return theme.Over(v, "#000000") != theme.Over(v, "#ffffff")
}

// applyShadowFloor 在主题给的阴影浅到看不见时把它压深一档，返回是否调整过。
//
// 为什么需要它：看板卡片是浮在页面底色上的圆角块，靠阴影被看见“浮起来了”。而主题给的
// widget.shadow 有时根本没有这个差值——官方 2026-light 给的是全透明（差 0 级），
// Catppuccin 四套压在自家底色上只有 4-5 级，2026-dark 的底色近黑、只剩 7.2 级，屏幕上
// 都看不出卡片浮起来。改主题文件等于自己维护
// 一份上游副本，上游一升级就得重做；所以这里只动我们注入的这个值（见 eggokit/theme/shadow.go）。
//
// 配色深浅的配对属于本项目的页面概念，因此“阴影落在哪个底色上”写在 ggt 这边，不进那个包：
// 阴影铺在卡片的留白处，也就是页面底色（--bg，来自 editor.background），
// 而不是卡片自己的底色——卡片上沿与下沿的阴影正是落在卡片之外的页面上
func applyShadowFloor(vars map[string]string) bool {
	shadow, ok := vars["widget-shadow"]
	if !ok {
		return false
	}
	bg, ok := vars["bg"]
	if !ok {
		return false
	}
	fixed, changed := theme.EnsureShadow(shadow, bg)
	if !changed {
		return false
	}
	vars["widget-shadow"] = fixed
	return true

}

// themeBlock 把一套解析好的主题拼成一条 :root{...} 规则
func themeBlock(r *theme.Resolved) string {
	vars := make(map[string]string, len(cssVarNames)+len(themeOwnVars[r.Type]))
	for id, name := range cssVarNames {
		if v := r.Colors[id]; v != "" {
			vars[name] = v
		}
	}
	for name, v := range themeOwnVars[r.Type] {
		vars[name] = v
	}
	// 卡片边框由该主题自己的前景色与卡片底色混出来，而不是写死两个十六进制：
	// VSCode 的侧栏默认不画边框（sideBar.border 的默认值是 null），“给卡片一条边”是 ggt
	// 自己的决定，因此没有上游值可抄；混色让它在任何主题下都与该主题同族同调，也不必逐套主题校色。
	//
	// 12% 这个比例是按原来那两档硬编码值反推的：深色下 --text #cccccc 压 --card-bg #252526
	// 得 #393a3a（原值 #34343a），浅色下 --text #616161 压 --card-bg #f3f3f3 得 #e1e1e1
	// （原值 #dcdcdc），两档都只差 5 个灰阶上下，观感等价而少了两份要维护的字面值。
	// 另外它只用在边框上、从不当前景，所以不进 contrastPairs，对比度回退不会碰它
	vars["card-border"] = "color-mix(in srgb, var(--text) 12%, var(--card-bg))"

	// 兜输入控件的边界之前先给它一个种子。种子的挑法按“这条边该像什么”排：
	//
	//  1. 主题自己写了 input.border，且它是个看得见的颜色——高对比度主题就是靠这条
	//  2. 控件自己的填色 input-bg：它是主题里最中性、最同族的一档，挪明度之后就是一条边。
	//     特意不用 dropdown.border：好几套主题把它设成了强调色（Catppuccin 是黄），而强调色
	//     在这里另有用处——聚焦态用的正是它（--focus-border），静止态与聚焦态同色就分不出两者
	//  3. 文字色，兜底
	//
	// 会透出底色的种子一律跳过：它的深浅由底色决定，透明度锁死了能拉开的差距（2026 Light 的
	// input.border 是 #00000066，压在浅底上最多只到 2.73:1）。Catppuccin 四套的 input.border
	// 是全透明，同样在这里被跳过，落到第 2 步
	seed := ""
	for _, name := range []string{"input-border", "input-bg", "text"} {
		if v := vars[name]; v != "" && !seeThrough(v) {
			seed = v
			break
		}
	}
	if seed == "" {
		// 一个能用的种子都没有，索性不发这个变量：样式表回落到 card-border，与改动前一样
		delete(vars, "input-border")
	} else {
		vars["input-border"] = seed
	}

	// 错误徽章的文字种子：优先用主题的错误色，上游没给时回落到冲突色（与样式表原来的
	// 回落一致）。随后由 applyContrastFixes 按 badge-bg 兜对比度
	if v := vars["status-error"]; v != "" {
		vars["badge-err-fg"] = v
	} else if v := vars["git-conflicting"]; v != "" {
		vars["badge-err-fg"] = v
	}

	// 配色被我们动过就必须留痕：不留的话，用户看到“按钮文字比 VSCode 里深一点”会以为是主题
	// 自己的问题，而这条日志正是“为什么和你看到的 VSCode 不一样”的唯一线索
	if n := applyContrastFixes(vars); n > 0 {
		logger.Debug(l10n.T("Adjusted theme colors for contrast", nil), "theme", r.ID, "count", n)
	}

	// 徽章文字、三枚变体徽章的混色底、文件类型图标三处都在这之后算，且顺序不能调换：
	// 徽章文字要先定稿，混色底才是按最终的文字色兜出来的
	if n := applyBadgeFixes(vars) + applyBadgeMixFixes(vars) + applyFileIconFix(vars); n > 0 {
		logger.Debug(l10n.T("Adjusted theme colors for contrast", nil), "theme", r.ID, "count", n)
	}

	// 阴影同样要留痕：不留的话，用户看到“卡片阴影比 VSCode 里深”会以为是主题自己的问题，
	// 而这条日志正是“为什么和你看到的不一样”的唯一线索。它排在对比度之后，因为两者改的是
	// 不同的变量（那里改前景色，这里改 widget-shadow），顺序对结果没有影响
	if applyShadowFloor(vars) {
		logger.Debug(l10n.T("Adjusted the theme shadow so cards are visibly raised", nil), "theme", r.ID)
	}

	// 变量名排序后再拼：map 的遍历顺序随机，不排序则每次渲染出的 CSS 文本都不同，
	// 既让 diff 没法看，也让“页面是否变过”这类判断失去意义
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString(":root{")
	for _, name := range names {
		b.WriteString("--")
		b.WriteString(name)
		b.WriteString(":")
		b.WriteString(vars[name])
		b.WriteString(";")
	}
	b.WriteString("}")
	return b.String()
}
