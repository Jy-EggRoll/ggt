// locales 包收纳 ggt 的语言文件，以及它们对应的 l10n 配置。
//
// 这是**项目专属**的部分：默认语言、语言列表、文件位置都在这里定；l10n 库本身
// 对这些一无所知，因此可以被别的项目直接复用。
//
// 文案分两层来源：本包 embed 的 *.json 只覆盖 ggt 自己的文案，随 eggokit 发布的
// 库文案（自升级、分级日志）通过 ExtraLayers 叠加进来，因此这里**不该**再收录库的消息——
// 收了就会随库升级而变化，改了英文原文两边就对不上
//
// 本包的另一个用途是给 tools 侧当参数来源：Taskfile 里的 l10n 任务需要用同一份
// 语言列表去扫描与校验，改动这里时要同步 Taskfile 的 --src/--dir 参数。
package locales

import (
	"embed"

	"github.com/jy-eggroll/eggokit/l10n"
	eggolocales "github.com/jy-eggroll/eggokit/locales"
)

// Default 是 ggt 的默认语言，同时也是配置文件里 language 项的默认值。
const Default = "en"

//go:embed *.json
var files embed.FS

// Options 返回 ggt 的 l10n 配置。
//
// Supported 的顺序是语义的一部分：go-i18n 按注册顺序挑选最匹配的语言标签，
// 因此默认语言放在首位。改动前请先读 l10n.Options 的说明。
//
// ExtraLayers 登记 eggokit 自带的语言文件：叠加时主来源最后加载、优先级最高，
// 因此 ggt 自己的译文始终权威，库文案只在 ggt 未提供时才用到库的译文
func Options() l10n.Options {
	return l10n.Options{
		Default:     Default,
		Supported:   Supported(),
		FS:          files,
		ExtraLayers: []l10n.Layer{eggolocales.Layer()},
	}
}

// Supported 返回 ggt 随二进制发布的语言列表（副本）。
// 供需要在 Init 之外做语言校验的场合使用，如配置项的取值校验。
func Supported() []string {
	return []string{"en", "zh-CN"}
}

// displayNames 是语言标签与它自身语言名称的对照表，键必须覆盖 Supported 的全部取值。
//
// 名称刻意用该语言自己的写法、不做翻译：语言选择器的作用是让不懂英文的人也能找到自己的
// 语言，把中文写成 Chinese 恰好会让最需要它的人找不到它
//
// 代价是 l10n 的扫描清单会一直把“简体中文”报成“未被消息函数或内嵌翻译表包裹的中文文案”：
// 那个清单只看字符串里有没有中文，分辨不出这类刻意不翻译的取值。这一条是预期内的，将来
// 再看到清单里只剩它时不必去改
var displayNames = map[string]string{
	"en":    "English",
	"zh-CN": "简体中文",
}

// DisplayName 返回语言标签的显示名，缺项时退回标签本身。
//
// 为什么把这张表放在 Supported 旁边，而不是让调用方（配置项注册表）自己写一份：
// 那张表的键必须与 Supported 完全对应，两者分处不同文件时，对不上的表现是“页面上少了一个
// 语言选项”且没有任何编译错误；放在同一个文件里，改语言列表的人一眼能看见它要一起改
func DisplayName(tag string) string {
	if name, ok := displayNames[tag]; ok {
		return name
	}
	return tag
}
