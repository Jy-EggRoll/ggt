// view.go 把注册表投影成网页设置面板要的形状，并提供写入的共享入口。
//
// 为什么要投影而不是让页面直接读注册表：注册表是 Go 结构体，页面拿不到；而“页面自己知道
// 有哪些配置项”恰恰是要避免的——那意味着每加一项配置都要同步改页面。这一层因此只做搬运：
// 注册表里有什么，页面就画什么，页面不需要认识任何一个具体的键
package config

import (
	"fmt"
	"strings"

	"github.com/jy-eggroll/eggokit/l10n"
)

// SettingView 是一项配置在设置面板里的样子：注册表的元数据，加当前生效值。
//
// 页面按 Kind 挑控件、按 Options 填候选、按 Min/Max 设输入边界、按 ManagedBy 决定是否只读，
// 因此新增一项配置不必改页面代码。Value 与 Default 一律是文本形态，与命令行 set 收字符串
// 完全一致：页面把用户改过的文本原样回传，解析与校验只在后端做一次
type SettingView struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Kind  Kind   `json:"kind"`
	// Value 是当前生效值（文件里有就用文件里的，否则用内置默认值）
	Value string `json:"value"`
	// Default 是内置默认值的文本形态，面板据此提供“恢复默认”
	Default string `json:"default"`
	// Expected 是合法取值的人类可读描述，与命令行的报错同源
	Expected string `json:"expected"`
	// Options 是候选取值。空数组表示自由输入，页面据此渲染成普通输入框
	Options []Option `json:"options"`
	// AllowCustom 为真时候选之外还接受别的写法，页面渲染成“输入框 + 候选”
	AllowCustom bool `json:"allowCustom"`
	// Min / Max 是整数输入的边界，指针为空表示这一侧不设限
	Min *int `json:"min"`
	Max *int `json:"max"`
	// ManagedBy 非空表示这一项由别的命令管理，面板只显示、不给改
	ManagedBy string `json:"managedBy"`
}

// SettingsViewAt 返回全部配置项在设置面板里的样子，顺序与注册表一致。
func SettingsViewAt(path string) []SettingView {
	settings := Settings()
	out := make([]SettingView, 0, len(settings))
	for _, s := range settings {
		v, err := EffectiveAt(path, s.Key)
		if err != nil {
			// 读不到就退回默认值：面板必须把每一项都画出来，少一项会让用户以为配置丢了。
			// 读取失败的真正原因由 validate 那条路径报告——那里才有足够的上下文说清问题
			v = s.Default
		}
		out = append(out, SettingView{
			Key:   s.Key,
			Title: l10n.Retranslate(s.Title, nil),
			Kind:  s.Kind,
			Value: valueTextLines(s, v),
			// 标题与说明在这里现查一次语言：注册表是包级变量，构造时 l10n 还没 Init，
			// 里面存的只是英文源串（见 settings.go 顶部那段说明）。视图是按请求生成的，
			// 这里求值才能拿到当前语言；用 Retranslate 而不是 T——T 要求首参是字面量，
			// 提取器会拒绝变量（消息 id 必须能被静态看见）
			Default:     valueTextLines(s, s.Default),
			Expected:    l10n.Retranslate(s.Expected, nil),
			Options:     viewOptions(s, valueTextLines(s, v)),
			AllowCustom: s.AllowCustom,
			Min:         s.Min,
			Max:         s.Max,
			ManagedBy:   s.ManagedBy,
		})
	}
	return out
}

// viewOptions 返回面板该显示的候选，必要时把当前值补进去。
//
// 为什么必须补：文件里可能是候选之外的值——命令行允许写自定义主题文件的路径，而网页只列
// 已知主题。此时下拉框会把当前值显示成某一个候选（HTML 的 select 只会显示候选项之一），
// 用户看到的是一个并非事实的配置，接着一保存就真的把它写成了那个候选
func viewOptions(s Setting, current string) []Option {
	opts := optionsOf(s)
	// 自由输入的控件（concurrency 那种）本来就原样显示输入框里的文本，不需要补
	if s.AllowCustom || len(opts) == 0 || current == "" {
		return opts
	}
	if choiceIn(opts, current) {
		return opts
	}
	return append(opts, Option{Value: current})
}

// optionsOf 把候选清单取成一份新切片，空的时候给空切片而不是 nil：
// JSON 里 nil 会编码成 null，页面要为此多写一个分支，而“没有候选”用空数组表达就够了
func optionsOf(s Setting) []Option {
	if s.Options == nil {
		return []Option{}
	}
	return s.Options()
}

// valueTextLines 把配置值转成面板用的文本：路径列表一行一条，其余沿用 ValueText。
//
// 为什么不让页面自己处理切片：ValueText 对切片给出的是 [a b] 这种 Go 打印形态
// （validate.go 的 default 分支），页面拿它没法还原成多行输入框里的内容
func valueTextLines(s Setting, v any) string {
	if s.Kind != KindPaths {
		return ValueText(v)
	}
	var lines []string
	switch t := v.(type) {
	case []string:
		lines = t
	case []any:
		for _, item := range t {
			if p, ok := item.(string); ok {
				lines = append(lines, p)
			}
		}
	}
	return strings.Join(lines, "\n")
}

// ChoiceAllowed 判断一个文本取值是否落在候选取值之内。
//
// 大小写不敏感：这与各解析器的口径一致（parseConcurrency、enumParser 都忽略大小写），
// 若这里严格而解析器宽松，就会出现“命令行存得进、页面存不进”的分叉
func ChoiceAllowed(s Setting, text string) bool {
	if s.Options == nil || s.AllowCustom {
		return true
	}
	return choiceIn(s.Options(), text)
}

// choiceIn 在候选清单里按值查找，大小写不敏感。
func choiceIn(opts []Option, text string) bool {
	t := strings.TrimSpace(text)
	for _, o := range opts {
		if strings.EqualFold(t, o.Value) {
			return true
		}
	}
	return false
}

// SetFromTextAt 按注册表把一个文本取值写进配置文件，返回写入后的规范文本。
//
// 命令行与网页设置面板共用这一条实现：两处各写一遍校验，迟早分叉成“页面能存、命令行存不进”
// 这类自相矛盾（旧的 /api/theme 就是这么长出来的——它自己实现了一套主题校验）
//
// strict 为真时额外要求取值落在候选之内，网页设置面板用严格模式：它的控件只会给出候选，
// 收到候选之外的取值说明请求不是页面发出来的。命令行用宽松模式——写一个自定义主题文件的
// 路径是合法用法，候选只是“能直接点的那几个”
//
// 取值的边界（Min/Max）刻意不在这里判：每个配置项的 Parse 已经把自己那套边界写死了
// （parsePositiveInt 之类），在这里再判一遍就有了两个真相源。测试断言“边界外一号的值
// 必须被 Parse 拒绝”，因此这份元数据只是给页面用的提示，不会与解析器脱节
func SetFromTextAt(path, key, text string, strict bool) (string, error) {
	s, ok := Lookup(key)
	if !ok {
		return "", ErrUnknownKey
	}
	if err := NotWritableError(s); err != nil {
		return "", err
	}
	if s.Parse == nil {
		// 注册表约定是“Parse 与 ManagedBy 恰有其一”，上面已经按 ManagedBy 拒绝过一次，
		// 走到这里说明有人往注册表里加了项却两样都没填（约定由 TestSettingMetadataIsComplete 守着）
		return "", fmt.Errorf("config: setting %q has neither Parse nor ManagedBy", s.Key)
	}
	if strict && !ChoiceAllowed(s, text) {
		return "", fmt.Errorf("%s", l10n.T("{{.Key}} must be one of: {{.Values}}",
			map[string]any{"Key": s.Key, "Values": optionValuesText(s.Options())}))
	}

	parsed, err := s.Parse(text)
	if err != nil {
		return "", fmt.Errorf("%s", l10n.T("Invalid value for {{.Key}}: {{.Value}} (expected {{.Expected}})",
			map[string]any{"Key": s.Key, "Value": text, "Expected": l10n.Retranslate(s.Expected, nil)}))
	}
	if err := SetKeyAt(path, s.Key, parsed); err != nil {
		return "", err
	}
	return ValueText(parsed), nil
}

// NotWritableError 返回“这一项由别的命令管理、不能在这里改”的错误，可以改时返回 nil。
//
// 命令行、网页设置面板、恢复默认三条路径都从这里取同一条文案：同一件事有三种说法时，
// 用户会以为是三种不同的限制
func NotWritableError(s Setting) error {
	if s.ManagedBy == "" {
		return nil
	}
	return fmt.Errorf("%s", l10n.T("{{.Key}} is managed by \"{{.Command}}\" and cannot be set here",
		map[string]any{"Key": s.Key, "Command": s.ManagedBy}))
}

// optionValuesText 把候选取值拼成一句可读的列举，供报错信息使用。
// 空值项显示成 (empty)：错误信息里出现一个空的引号只会让人以为是拼接 bug
func optionValuesText(opts []Option) string {
	values := make([]string, 0, len(opts))
	for _, o := range opts {
		if o.Value == "" {
			values = append(values, "(empty)")
			continue
		}
		values = append(values, o.Value)
	}
	return strings.Join(values, ", ")
}
