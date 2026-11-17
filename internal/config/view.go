// view.go 把注册表投影成网页设置面板要的形状，并提供写入的共享入口。
//
// 为什么要投影而不是让页面直接读注册表：注册表是 Go 结构体，页面拿不到；而“页面自己知道
// 有哪些配置项”恰恰是要避免的——那意味着每加一项配置都要同步改页面。这一层因此只做搬运：
// 注册表里有什么，页面就画什么，页面不需要认识任何一个具体的键
package config

import (
	"encoding/json"
	"errors"
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
	// Value 是当前生效值（文件里有就用文件里的，否则用内置默认值）。
	// 取值非法时这里给的是内置默认值——控件要有一个能显示的东西——此时 Invalid 为真，
	// 页面必须同时把 Raw 与 Error 摆出来，否则用户看到的是“一项没改过的配置”
	Value string `json:"value"`
	// Raw 是这一项在配置文件里写下的原始文本，键不在文件里时为空串。
	// 取值非法时它是唯一还留着“文件里到底是什么”的证据，因此不跟着 Value 回退
	Raw string `json:"raw"`
	// Invalid 为真表示文件里这一项的取值无法通过 Normalize，运行期加载会因此失败。
	// 它与“偏离默认值”是两件事：一个取值合法的自定义主题也只算偏离默认值
	Invalid bool `json:"invalid"`
	// Error 是 Invalid 时的原因，与命令行加载失败、config validate 共用同一句文案
	Error string `json:"error,omitempty"`
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
	// ApplyAt 是这一项改完之后何时生效（immediate / reload / restart），面板据此常驻标注
	ApplyAt ApplyAt `json:"applyAt"`
}

// SettingsViewAt 返回全部配置项在设置面板里的样子，顺序与注册表一致。
//
// 返回值里的 error 只表示“整份文件读不出来”（语法错误、权限、大小写重复键）。此时仍然
// 逐项返回按默认值填好的视图：面板必须把每一项都画出来，否则用户会以为配置丢了，而
// “文件读不出来”这件事由调用方放在响应的顶层告诉页面（见 cmd/ui_settings.go）。
//
// 单项取值非法（Error/Invalid）不放进 error：那一项的键还在文件里，面板可以就地看着它、
// 恢复默认后立刻修好；把它升级成整份失败反而会让用户连别的项都改不了
func SettingsViewAt(path string) ([]SettingView, error) {
	raw, readErr := ReadRawAt(path)

	settings := Settings()
	out := make([]SettingView, 0, len(settings))
	for _, s := range settings {
		view := SettingView{
			Key:   s.Key,
			Title: l10n.Retranslate(s.Title, nil),
			Kind:  s.Kind,
			// 标题与说明在这里现查一次语言：注册表是包级变量，构造时 l10n 还没 Init，
			// 里面存的只是英文源串（见 settings.go 顶部那段说明）。视图是按请求生成的，
			// 这里求值才能拿到当前语言；用 Retranslate 而不是 T——T 要求首参是字面量，
			// 提取器会拒绝变量（消息 id 必须能被静态看见）
			Default:     valueTextLines(s, s.Default),
			Expected:    l10n.Retranslate(s.Expected, nil),
			AllowCustom: s.AllowCustom,
			Min:         s.Min,
			Max:         s.Max,
			ManagedBy:   s.ManagedBy,
			// 零值（注册表未标注）统一规范成 immediate，页面因此总是拿到三个具体取值之一
			ApplyAt: s.ApplyAt.normalized(),
		}
		view.Value, view.Raw, view.Invalid, view.Error = viewValue(s, raw)
		view.Options = viewOptions(s, view.Value)
		out = append(out, view)
	}
	return out, readErr
}

// viewValue 把文件里这一项的原始值折成“控件显示什么、文件里原本是什么、是否非法”。
//
// 取值非法时 Value 退回默认值而不是原样给出：控件（数字框、下拉框、复选框）只会
// 把原样文本重新解释一遍，例如候选之外的主题会被下拉框显示成第一个候选——那是一个
// 并非事实的配置，用户接着一保存就真的改成了它。原样文本交给 Raw 与 Error，
// 面板把它们当说明显示，控件本身保持可用
func viewValue(s Setting, raw map[string]any) (value, rawText string, invalid bool, problem string) {
	v, ok := raw[s.Key]
	if !ok {
		return valueTextLines(s, s.Default), "", false, ""
	}
	normalized, err := normalizeValue(s, v)
	if err != nil {
		// 废弃的并发语义值运行期照常按 auto 跑（见 migrateDeprecatedConcurrency），
		// 因此它既不算非法，也不该把文件里的原值当成生效值展示
		if errors.Is(err, errDeprecatedConcurrency) {
			return valueTextLines(s, s.Default), rawValueText(v), false, ""
		}
		return valueTextLines(s, s.Default), rawValueText(v), true, err.Error()
	}
	return valueTextLines(s, normalized), rawValueText(v), false, ""
}

// rawValueText 把文件里的原始值转成面板可读的一段文本。
//
// 与 ValueText 的差别：字符串原样给出（不再被 JSON 的引号包住），其余类型按 JSON 形态给出，
// 数组与对象因此能看出结构。面板只用它说明“文件里写的是什么”，不参与任何解析
func rawValueText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return "null"
	case json.Number:
		return t.String()
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ValueText(v)
	}
	return string(b)
}

// viewOptions 返回面板该显示的候选，必要时把当前值补进去。
//
// 为什么必须补：文件里可能是候选之外的值——命令行允许写自定义主题文件的路径，而网页只列
// 已知主题。此时下拉框会把当前值显示成某一个候选（HTML 的 select 只会显示候选项之一），
// 用户看到的是一个并非事实的配置，接着一保存就真的把它写成了那个候选
//
// 空串同样要补：theme 的候选里本来就有一个空值项（跟随系统），而 theme_dark / theme_light
// 的清单里没有，它们的空串表示“回落到内置默认主题”（见 themePref）。不补的话这一项在
// 下拉框里会显示成第一套主题
func viewOptions(s Setting, current string) []Option {
	opts := optionsOf(s)
	// 自由输入的控件（concurrency 那种）本来就原样显示输入框里的文本，不需要补
	if s.AllowCustom || len(opts) == 0 {
		return opts
	}
	if choiceIn(opts, current) {
		return opts
	}
	return append(opts, Option{Value: current, Label: emptyValueLabel(current)})
}

// emptyValueLabel 给补进候选的那种“清单里没有的空串”一个说得清的名字。
//
// 候选的显示名默认由页面给，页面只认得 theme 那个空串的含义（跟随系统）；其余项的空串是
// “回落到内置默认”，页面无从判断，因此由这里说明，页面按 label 原样显示
func emptyValueLabel(current string) string {
	if current != "" {
		return ""
	}
	return l10n.T("built-in default", nil)
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
// 这类自相矛盾（旧的 /api/theme 就是这么出现的——它自己实现了一套主题校验）。
//
// 合法取值的唯一判据是注册表的 Normalize（经 Parse 转发）。候选清单**不参与**写入判定，
// 理由有两条：
//   - 候选清单随机器与时刻变化。主题来自配置目录的扫描，语言来自随二进制发布的语言包；
//     拿它当判据，同一份请求在两台机器上会一个成功一个失败，而“能存进去的主题”还要取决于
//     用户此刻有没有把主题文件放进目录
//   - Normalize 已经把自己那一项的取值域写清了：枚举项（size_unit / language / log_level）
//     在 Normalize 里拒绝候选之外的值，开放项（theme / font_*）在 Normalize 里只去空白。
//     再加一道候选比对，等于给这些项各加一份更严的第二规则：language 的 "zh" 会被候选
//     （zh-CN）挡下、自定义主题路径会被挡下、theme_dark 的空串会被挡下——三者命令行都收
//
// 取值的边界（Min/Max）同样不在这里判：Normalize 直接读同一份 Min/Max，
// 在这里再判一遍就有了两个真相源。测试断言“边界外一号的值必须被 Normalize 拒绝”，
// 因此这份元数据只是给页面用的提示，不会与校验脱节
func SetFromTextAt(path, key, text string) (string, error) {
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
	// 显式写空值一律报错：空串在配置文件里可能是有含义的缺省形态（concurrency 的空串按
	// auto 跑），但 set 的语义是“我要写这个值”，替用户把它变成某一个具体取值没有道理。
	// 判断只落在声明了 RejectBlank 的项上，theme / font_* 的空串仍然有意义
	if s.RejectBlank && strings.TrimSpace(text) == "" {
		return "", invalidValueError(s, "(empty)")
	}

	parsed, err := s.Parse(text)
	if err != nil {
		// Parse 已经把语义错误渲染成带 Key/Expected 的文案，枚举错误还会列出候选；
		// 这里只补旧语义值的处理：config set 是在显式写一个新值，没有理由再把废弃词写回去
		if errors.Is(err, errDeprecatedConcurrency) {
			return "", invalidValueError(s, text)
		}
		return "", err
	}
	if err := SetKeyAt(path, s.Key, parsed); err != nil {
		return "", err
	}
	return ValueText(parsed), nil
}

// UnsetPlan 说明“把一个键从文件里删掉”这件事该怎么做。
//
// 命令行 reset 与设置面板的“恢复默认”共用同一份判定：受管项在这里被放行的条件必须完全一样，
// 否则会出现“命令行能清掉这个坏值、页面却清不掉”的死角——而配置坏掉时两者都是修配置的入口
type UnsetPlan struct {
	// Key 是规范化后的键名（小写、去首尾空白），实际删除的就是它
	Key string
	// Known 为假表示注册表里没有这个键，但文件里确实写着它——未知键要能被清掉，
	// 否则拼错一个键名就只剩手改文件或 reset --all 两条路
	Known bool
	// Managed 为真表示这一项平时由别的命令管理，这次是因为取值非法才放行
	Managed bool
}

// PlanUnsetAt 判断一个键能否从配置文件里删除，返回删除计划或拒绝的原因。
//
// 拒绝的两种情形：未知键且文件里并没有它（拼写错误当场暴露，而不是回一句“已删除”），
// 以及受管项的取值仍然合法（受管约束的是“怎么设值”，不该绕开 ggt repo）。
// 受管项的取值非法时放行一次：受管项坏掉之后所有命令都起不来，唯一能落地的修复就是清掉它。
//
// 文件读不出来时返回该错误：那时连“文件里有没有这个键”都无从判断，任何删除都会
// 覆盖掉一份读不懂的内容
func PlanUnsetAt(path, key string) (UnsetPlan, error) {
	raw, err := ReadRawAt(path)
	if err != nil {
		return UnsetPlan{Key: strings.ToLower(strings.TrimSpace(key))}, err
	}
	k := strings.ToLower(strings.TrimSpace(key))
	s, known := Lookup(k)
	if !known {
		if _, present := raw[k]; !present {
			return UnsetPlan{Key: k}, ErrUnknownKey
		}
		return UnsetPlan{Key: k}, nil
	}

	plan := UnsetPlan{Key: s.Key, Known: true}
	if s.ManagedBy == "" {
		return plan, nil
	}
	bad, err := ValueErrorAt(path, s.Key)
	if err != nil {
		return plan, err
	}
	if !bad {
		// 走删除语境的说法：这条路径是 reset 与“恢复默认”，说“不能在这里设置”会让人
		// 以为换个入口就能设，而实际约束是这两个列表必须由 ggt repo 增删
		return plan, fmt.Errorf("%s", l10n.T("{{.Key}} is managed by \"{{.Command}}\"; use that command to change it",
			map[string]any{"Key": s.Key, "Command": s.ManagedBy}))
	}
	plan.Managed = true
	return plan, nil
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
