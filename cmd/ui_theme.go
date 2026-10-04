// 本文件是看板主题的 ggt 侧：把某套 VSCode 主题解析成页面要的那组 CSS 变量。
//
// 通用部分——JSONC 规整、include 链、type 判定、VSCode 颜色注册表默认值、内置主题文件——
// 全在 internal/theme，本文件只负责 ggt 自己的两件事：要哪些颜色 id，以及 VSCode 里没有
// 对应物的自有令牌取什么值。这条分界见 internal/theme 的包注释：那个包将来要整包搬进
// 共享库 eggokit 给别的项目复用，因此 ggt 的页面概念不能出现在它里面
package cmd

import (
	"sort"
	"strings"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/eggokit/logger"
	"github.com/jy-eggroll/ggt/internal/config"
	"github.com/jy-eggroll/ggt/internal/theme"
)

// cssVarNames 是「VSCode 颜色 id → 页面里的 CSS 变量名」的映射，只有这一处定义。
//
// 变量名沿用 style.css 里既有的短名（--git-modified 这些），而不是照抄 VSCode 的长名
// （--vscode-gitDecoration-modifiedResourceForeground 那种）：短名在样式表里读得更清楚，
// 而"哪个变量对应哪个 VSCode 令牌"看这张表就一目了然，映射关系不会因此变模糊
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
	// 文字又沿用 --text，于是"深灰字压重蓝底"只在个别主题上出现（2026 Light 的
	// badge.background 恰好是重蓝 #0069CC，而它的 --text 是 #202020），排查时很难联想到
	// 是令牌职责混用。拆开之后按钮的底色与前景来自同一组令牌，不会再各自漂移
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

	// 焦点边框：样式表原来拿 git-modified（未暂存修改色）当输入框的聚焦色，
	// 那是"文件被改过"的语义，与"这个控件拿到了焦点"无关
	"focusBorder": "focus-border",

	// diff 面板改成浮层之后，需要"浮层底色"与"浮层阴影"两个语义：VSCode 给的是
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

	"diffEditor.insertedLineBackground": "diff-add-bg",
	"diffEditor.removedLineBackground":  "diff-del-bg",

	// 分支图：泳道配色与引用配色用的是 VSCode 源码管理图那一套令牌（scmHistory.ts 的
	// colorRegistry 与三个 historyItem*RefColor）。五个前景色是上游写死的字面值，
	// 两个引用色一路引用到 charts.blue/purple → editorInfo.foreground，最终值同样照抄，
	// 见 internal/theme/defaults.json
	"scmGraph.foreground1":               "scm-graph-fg1",
	"scmGraph.foreground2":               "scm-graph-fg2",
	"scmGraph.foreground3":               "scm-graph-fg3",
	"scmGraph.foreground4":               "scm-graph-fg4",
	"scmGraph.foreground5":               "scm-graph-fg5",
	"scmGraph.historyItemRefColor":       "scm-graph-ref",
	"scmGraph.historyItemRemoteRefColor": "scm-graph-remote-ref",

	// 状态与控件层级：禁用前景、错误/警告/信息三种语义色、工具栏按钮的 hover、
	// 列表选中底色、键帽三件套、输入框占位符。取值逐条照抄上游注册表（见 defaults.json 的注释），
	// 它们都是"别处借不到"的语义——例如错误文本原先借的是 git-conflicting（"文件有合并冲突"）
	"disabledForeground":             "disabled-fg-token",
	"editorError.foreground":         "status-error",
	"editorWarning.foreground":       "status-warn",
	"editorInfo.foreground":          "status-info",
	"toolbar.hoverBackground":        "toolbar-hover-bg",
	"list.activeSelectionBackground": "list-selected-bg",
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
	// 浮层边框：上游默认是 null（深/浅两档都没值），因此它只做映射、不进 defaults.json——
	// 样式表里写成 var(--editor-widget-border, var(--card-border))，缺值时自然回落到卡片边框
	"editorWidget.border": "editor-widget-border",
}

// themeOwnVars 是 VSCode 里没有对应物的自有令牌，按明暗两套给出取值。
//
// 为什么只能由本项目的代码给：VSCode 的侧栏默认不画边框（sideBar.border 的默认值是 null），
// 而看板的卡片是 ggt 自己的设计；"（续 2）"那块续段标识更是只有 ggt 才有。
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
// 每次都从文件读，不吃进程启动时那份缓存：换主题是运行期动作（页面上点一下就会写配置），
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
// 为什么是"一个显式指定 + 两个偏好"而不是"跟随/指定开关 + 一个 id"：VSCode 的模型正是前者——
// 自动检测开着时用的不是某一套写死的主题，而是用户分别指定的深、浅两套；关掉之后才由
// colorTheme 说了算。照抄它意味着"跟随系统"也能定制，而不是只能吃内置的那两套
const (
	themeKey      = "theme"
	themeDarkKey  = "theme_dark"
	themeLightKey = "theme_light"
)

// themePref 读一个"跟随系统"下的主题偏好，读不到或写成空串就回落到内置默认。
//
// 与 currentThemeID 的差别在于空串的含义：theme 的空串是"跟随系统"，是有意义的取值；
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
// 页面不该因为一个坏主题文件就整页没有颜色，而"配色悄悄变回默认"这种事必须留下痕迹
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

// systemThemeCSS 是"跟随系统"时的样式：把深、浅两套都写进去，浅色那份套在
// prefers-color-scheme 里，由浏览器自己按系统设置挑——服务端问不到你的系统是深是浅。
//
// 哪一套是深、哪一套是浅由**角色**决定（theme_dark 进基础规则、theme_light 进浅色媒体查询），
// 不看主题自己声明的明暗类型：这是 VSCode 的语义——preferredDarkColorTheme 说的是"系统是深色时
// 用哪套"，用户可以真的把一套浅色主题填进去，那时它照样只在系统为深色时生效。
// 主题自己的 type 仍然决定它的自有令牌取哪一档（见 themeOwnVars），两件事互不干扰。
//
// 配置里的 id 指到不存在或解析不了的主题时回落到内置默认并记日志：与"选中的主题解析失败"
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
			// 页面会因为没有颜色变量而呈现为不可读——这正是我们想要的"响亮地失败"
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

// contrastPairs 记录"哪些前景会被铺在哪些底色上"：键是前景的 CSS 变量名，值是该前景可能压住的底色。
//
// 放在这里而不是 internal/theme：哪些元素成对出现属于本项目的页面概念，而那个包要整包搬进
// 共享库给别的项目复用，不能带上 ggt 的页面知识（见该包的包注释）
//
// hover 底色也算进来：按钮的前景在常态与 hover 两种底色下都得读得出来。主题没给
// button.hoverBackground 时它不在 vars 里，这一项自然跳过——那时样式表回落到
// list.hoverBackground，是个中性色，风险低得多
var contrastPairs = map[string][]string{
	"btn-fg":           {"btn-bg", "btn-hover-bg"},
	"btn-secondary-fg": {"btn-secondary-bg", "btn-secondary-hover-bg"},
	"dropdown-fg":      {"dropdown-bg"},
	"input-fg":         {"input-bg"},
	"badge-fg":         {"badge-bg"},
	// 键入提示的键帽与输入框占位符也要能读出来：它们同样是"前景压在底色上"
	"kbd-fg":            {"kbd-bg"},
	"input-placeholder": {"input-bg"},
}

// applyContrastFixes 在对比度实在不够时只调前景色的明度，返回调整过的项数。
//
// 底色一律不动：那是主题的设计。改上游色值等于自己维护一份主题副本，上游一升级就得重做，
// 每套主题还都要各修一遍；而这个函数对任何主题都成立（见 internal/theme/contrast.go）
func applyContrastFixes(vars map[string]string) int {
	adjusted := 0
	for fgName, bgNames := range contrastPairs {
		fg, ok := vars[fgName]
		if !ok {
			continue
		}
		bgs := make([]string, 0, len(bgNames))
		for _, name := range bgNames {
			if v, ok := vars[name]; ok {
				bgs = append(bgs, v)
			}
		}
		if len(bgs) == 0 {
			continue
		}
		if fixed, changed := theme.EnsureContrast(fg, bgs, theme.MinContrast); changed {
			vars[fgName] = fixed
			adjusted++
		}
	}
	return adjusted
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
	// VSCode 的侧栏默认不画边框（sideBar.border 的默认值是 null），"给卡片一条边"是 ggt
	// 自己的决定，因此没有上游值可抄；混色让它在任何主题下都与该主题同族同调，也不必逐套主题校色。
	//
	// 12% 这个比例是按原来那两档硬编码值反推的：深色下 --text #cccccc 压 --card-bg #252526
	// 得 #393a3a（原值 #34343a），浅色下 --text #616161 压 --card-bg #f3f3f3 得 #e1e1e1
	// （原值 #dcdcdc），两档都只差 5 个灰阶上下，观感等价而少了两份要维护的字面值。
	// 另外它只用在边框上、从不当前景，所以不进 contrastPairs，对比度兜底不会碰它
	vars["card-border"] = "color-mix(in srgb, var(--text) 12%, var(--card-bg))"

	// 配色被我们动过就必须留痕：不留的话，用户看到"按钮文字比 VSCode 里深一点"会以为是主题
	// 自己的问题，而这条日志正是"为什么和你看到的 VSCode 不一样"的唯一线索
	if n := applyContrastFixes(vars); n > 0 {
		logger.Debug(l10n.T("Adjusted theme colors for contrast", nil), "theme", r.ID, "count", n)
	}

	// 变量名排序后再拼：map 的遍历顺序随机，不排序则每次渲染出的 CSS 文本都不同，
	// 既让 diff 没法看，也让"页面是否变过"这类判断失去意义
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
