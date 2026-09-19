// locales 包收纳 ggt 的语言文件，以及它们对应的 l10n 配置。
//
// 这是**项目专属**的部分：默认语言、语言列表、文件位置都在这里定；pkg/l10n 库本身
// 对这些一无所知，因此可以被别的项目直接复用。
//
// 本包的另一个用途是给 tools 侧当参数来源：Taskfile 里的 l10n 任务需要用同一份
// 语言列表去扫描与校验，改动这里时要同步 Taskfile 的 --src/--dir 参数。
package locales

import (
	"embed"

	"ggt/pkg/l10n"
)

// Default 是 ggt 的默认语言，同时也是配置文件里 language 项的默认值。
const Default = "en"

//go:embed *.json
var files embed.FS

// Options 返回 ggt 的 l10n 配置。
//
// Supported 的顺序是语义的一部分：go-i18n 按注册顺序挑选最匹配的语言标签，
// 因此默认语言放在首位。改动前请先读 l10n.Options 的说明。
func Options() l10n.Options {
	return l10n.Options{
		Default:   Default,
		Supported: Supported(),
		FS:        files,
	}
}

// Supported 返回 ggt 随二进制发布的语言列表（副本）。
// 供需要在 Init 之外做语言校验的场合使用，如配置项的取值校验。
func Supported() []string {
	return []string{"en", "zh-CN"}
}
