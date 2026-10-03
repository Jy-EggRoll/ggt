// 本文件是看板主题的 ggt 侧：把某套 VSCode 主题解析成页面要的那组 CSS 变量。
//
// 通用部分——JSONC 规整、include 链、type 判定、VSCode 颜色注册表默认值、内置主题文件——
// 全在 internal/theme，本文件只负责 ggt 自己的两件事：要哪些颜色 id，以及 VSCode 里没有
// 对应物的自有令牌取什么值。这条分界见 internal/theme 的包注释：那个包将来要整包搬进
// 共享库 eggokit 给别的项目复用，因此 ggt 的页面概念不能出现在它里面
package cmd

import (
	"encoding/json"
	"net/http"
	"path/filepath"
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
}

// themeOwnVars 是 VSCode 里没有对应物的自有令牌，按明暗两套给出取值。
//
// 为什么只能由本项目的代码给：VSCode 的侧栏默认不画边框（sideBar.border 的默认值是 null），
// 而看板的卡片是 ggt 自己的设计；"（续 2）"那块续段标识更是只有 ggt 才有。
// 它们同样不写死在样式表里——样式表只用变量，取值仍然由这里（数据）提供
var themeOwnVars = map[theme.Type]map[string]string{
	theme.Dark: {
		"card-border": "#34343a",
		"cont-bg":     "#2d2d30",
		"cont-fg":     "#d7ba7d",
	},
	theme.Light: {
		"card-border": "#dcdcdc",
		"cont-bg":     "#efefef",
		"cont-fg":     "#8a6d1f",
	},
}

// uiThemeData 是注入页面的主题数据：当前选择 + 分组好的可选主题。
//
// 由服务端注入而不是让页面自己请求：首屏就该带上正确的配色（否则会先闪一下默认色），
// 而服务端渲染 index.html 时本来就在替换占位符，顺手带上这份数据不需要多一次往返
type uiThemeData struct {
	// Current 是当前选中的主题 id，空表示跟随系统
	Current string `json:"current"`
	// Groups 是选择器里的分组；Label 为空的那组表示"用户自己放进来的"，
	// 标题由页面按当前语言给（页面文案不走 Go 的 l10n 管线）
	Groups []uiThemeGroup `json:"groups"`
}

type uiThemeGroup struct {
	Label  string          `json:"label"`
	Themes []uiThemeOption `json:"themes"`
}

type uiThemeOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// themeDirs 是扫描用户主题的目录：配置目录本身，以及它的 themes 子目录。
// 两个都看——"把主题文件丢进配置目录"是最自然的用法，而主题多了之后又需要一个地方归置
func themeDirs() []string {
	dir := filepath.Dir(config.GetDefaultConfigPath())
	return []string{dir, filepath.Join(dir, "themes")}
}

// currentThemeID 读配置里选中的主题 id。
//
// 每次都从文件读，不吃进程启动时那份缓存：换主题是运行期动作（页面上点一下就会写配置），
// 若读的是启动时的旧值，用户换完主题刷新页面看到的还是旧配色，会以为选择没生效
func currentThemeID() string {
	v, err := config.EffectiveAt(config.GetDefaultConfigPath(), "theme")
	if err != nil {
		return ""
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// resolveTheme 解析出本次页面渲染该用的 CSS 与选择器数据。
//
// 选中的主题解析失败时（文件被删了、内容写坏了）回退到跟随系统并记一条日志：
// 页面不该因为一个坏主题文件就整页没有颜色，而"配色悄悄变回默认"这种事必须留下痕迹
func resolveTheme() (string, uiThemeData) {
	data := uiThemeData{Current: currentThemeID(), Groups: groupThemes(theme.Available(themeDirs()))}
	if data.Current == "" {
		return systemThemeCSS(), data
	}
	resolved, err := theme.Resolve(data.Current)
	if err != nil {
		logger.Warn(l10n.T("Failed to load the selected theme; following the system instead", nil),
			"theme", data.Current, "error", err)
		data.Current = ""
		return systemThemeCSS(), data
	}
	return themeBlock(resolved), data
}

// systemThemeCSS 是"跟随系统"时的样式：把内置的深、浅两套都写进去，浅色那份套在
// prefers-color-scheme 里，由浏览器自己按系统设置挑——服务端问不到你的系统是深是浅
func systemThemeCSS() string {
	var b strings.Builder
	for _, id := range []string{theme.DefaultDarkID, theme.DefaultLightID} {
		resolved, err := theme.Resolve(id)
		if err != nil {
			// 内置主题解析不了只可能是打包出了问题（单测里覆盖了），此时什么都不注入，
			// 页面会因为没有颜色变量而呈现为不可读——这正是我们想要的"响亮地失败"
			logger.Error(l10n.T("Failed to resolve a built-in theme", nil), "theme", id, "error", err)
			continue
		}
		if resolved.Type == theme.Light {
			b.WriteString("@media (prefers-color-scheme: light){")
			b.WriteString(themeBlock(resolved))
			b.WriteString("}")
			continue
		}
		b.WriteString(themeBlock(resolved))
	}
	return b.String()
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

// groupThemes 把可用主题按来源分组，供选择器用 <optgroup> 展示
func groupThemes(list []theme.Theme) []uiThemeGroup {
	var groups []uiThemeGroup
	index := map[string]int{}
	for _, t := range list {
		i, ok := index[t.Group]
		if !ok {
			groups = append(groups, uiThemeGroup{Label: t.Group})
			i = len(groups) - 1
			index[t.Group] = i
		}
		groups[i].Themes = append(groups[i].Themes, uiThemeOption{ID: t.ID, Name: t.Name})
	}
	return groups
}

// themeDataJSON 把主题数据序列化成能安全塞进 <script> 的 JSON。
//
// 用 json.Marshal 而不是关掉 HTML 转义的编码器：主题名来自用户放进来的文件，
// 里面出现 </script> 这类字样时，未转义的 JSON 会把 script 标签提前闭合，页面结构就破了。
// json.Marshal 默认把 < > & 转成 \u003c 这种形式，塞进脚本里是安全的
func themeDataJSON(data uiThemeData) string {
	b, err := json.Marshal(data)
	if err != nil {
		logger.Error(l10n.T("Failed to encode the theme list", nil), "error", err)
		return "{}"
	}
	return string(b)
}

// handleTheme 切换看板主题：把选中的主题 id 写进配置文件的 theme 键。
//
// 只接受"当前可用列表里确实存在的 id"，不接受任意字符串：这个端点的作用是让页面上的选择
// 生效，而页面上的选项就是 Available() 给出的那些。放任意值进来，就等于一次请求能让配置里的
// theme 指向任意路径，而渲染页面时我们会去读那个文件
func (c *uiCache) handleTheme(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeUIWrite(w, http.StatusMethodNotAllowed, uiWriteResult{Error: l10n.T("Only POST is allowed", nil)})
		return
	}
	var req struct {
		// ID 为空表示跟随系统
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeUIWrite(w, http.StatusBadRequest, uiWriteResult{Error: l10n.T("Invalid request body", nil)})
		return
	}

	id := strings.TrimSpace(req.ID)
	if id != "" {
		known := false
		for _, t := range theme.Available(themeDirs()) {
			if t.ID == id {
				known = true
				break
			}
		}
		if !known {
			writeUIWrite(w, http.StatusNotFound, uiWriteResult{Error: l10n.T("Unknown theme", nil)})
			return
		}
	}
	if err := config.SetKey("theme", id); err != nil {
		writeUIWrite(w, http.StatusInternalServerError, uiWriteResult{Error: err.Error()})
		return
	}
	writeUIWrite(w, http.StatusOK, uiWriteResult{Output: l10n.T("Theme saved", nil)})
}
