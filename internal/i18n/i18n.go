// i18n 包提供 ggt 的国际化能力。
//
// 采用与 VSCode @vscode/l10n 相同的模型：**英文源串本身就是消息 id**，
// 不存在独立的符号 key。由此带来几个直接后果，也是本包设计的前提：
//
//   - 源码里写 T("Total size: {{.Size}}", data)，"Total size: {{.Size}}" 既是
//     待翻译的英文原文，也是查表用的 id；改文案即改 id
//   - locales/en.json 是**生成物**（由 tools/l10n export 扫描源码覆盖写入），
//     内容是 { 源串: 源串 } 的自映射，不是手工维护的翻译文件
//   - locales/zh-CN.json 是**手工维护**的译文，形如 { 英文源串: 中文 }
//   - 缺失翻译时 go-i18n 会逐级回落到默认语言、再到 DefaultMessage，而
//     DefaultMessage 就是源串本身，因此任何漏翻都表现为"回退显示英文"，
//     不会出现空串或裸 key
//
// 调用约定：Init 必须在任何并发调用 T() 之前完成（由 cmd.Execute 在最早期调用）。
// Init 之后本包状态只读，可安全地被多个 goroutine 并发读取——这不是巧合而是必要
// 前提：部分命令会在 worker 并发任务里调用 T()。
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/nicksnyder/go-i18n/v2/i18n/template"
	"golang.org/x/text/language"
)

// DefaultLanguage 是未指定语言时使用的默认语言。
// 取英文而非中文：英文源串即消息 id，默认语言自然就是英文。
const DefaultLanguage = "en"

// supported 是随二进制发布的语言列表，同时决定语言文件的加载顺序。
//
// 顺序必须固定且显式，不能改成 map 迭代：go-i18n 的 language.Matcher 会依据
// 语言标签的注册顺序挑选最匹配项，依赖 map 的随机迭代顺序会让"请求 zh 时究竟
// 落到哪个标签"变得不确定。
var supported = []string{"en", "zh-CN"}

//go:embed locales/*.json
var localeFS embed.FS

// missingKeyErrorParser 让模板变量缺失时直接报错，而不是静默渲染成 <no value>。
//
// go-i18n 的 template.TextParser 默认 Option 为 "missingkey=default"（见
// i18n/template/text_parser.go），漏传模板变量既不报错也不返回 error，用户会看到
// 诸如 "{{.Count}} repos" 渲染出的 "<no value> repos"，且本包的容错逻辑抓不到。
// 改成 missingkey=error 后，这类问题会退化为回退显示源串，在开发阶段即可暴露。
//
// 注意：不含 {{ 的纯文本消息会走 TextParser 的快速路径直接返回，不经过模板解析。
var missingKeyErrorParser = &template.TextParser{Option: "missingkey=error"}

// reservedKeys 是 go-i18n 在解析消息文件时识别为"消息字段"的键名（见
// i18n/message.go 的 isReserved）。本项目的语言文件是平铺的 { 源串: 译文 }，
// 因此顶层键就是消息 id——若某条 id 恰好等于这些词（忽略大小写），go-i18n 会把
// 它当成消息元数据字段而非一条消息：
//   - 若它是文件里唯一的键，整份文件会被当成"一条消息"解析，所有条目丢失
//   - 若与其它键共存，则触发 mixedKeysError，整份文件解析失败
//
// 两种情况的后果都是全部译文静默失效，因此必须在加载时显式拦截。
var reservedKeys = map[string]struct{}{
	"id": {}, "description": {}, "hash": {}, "leftdelim": {}, "rightdelim": {},
	"zero": {}, "one": {}, "two": {}, "few": {}, "many": {}, "other": {}, "translation": {},
}

var (
	// localizer 是当前语言的翻译器。Init 成功后只读。
	// 允许为 nil（Init 未调用或调用失败），此时 T 必须安全降级而不是 panic。
	localizer *goi18n.Localizer

	// current 是当前生效的语言标签。
	current = DefaultLanguage
)

// Init 加载内嵌语言文件并建立当前语言的翻译器，失败时返回 error。
//
// 之所以返回 error 而不是静默降级：语言文件是 //go:embed 进来的，解析失败属于
// 构建期错误，运行期无从补救。若默默跳过，用户看到的只是"中文界面变成了英文"，
// 没有任何报错，排查成本极高。调用方（cmd.Execute）应把 error 打印后退出。
func Init(lang string) error {
	bundle := goi18n.NewBundle(language.English)
	// go-i18n 在 format 为 json 且未注册解码器时会自动退回 json.Unmarshal
	// （见 i18n/parse.go），这里显式注册只为让意图更清楚
	bundle.RegisterUnmarshalFunc("json", json.Unmarshal)

	for _, tag := range supported {
		if _, err := loadLocaleFile(bundle, tag); err != nil {
			return err
		}
	}

	localizer = goi18n.NewLocalizer(bundle, lang)
	current = lang
	return nil
}

// loadLocaleFile 校验并加载一份语言文件，返回其中的消息条数。
//
// 除了解析，这里还刻意做了三项结构校验，它们共同保证"文件里的每条译文都真的
// 会被 go-i18n 认作一条消息"——这是静默失效的唯一入口：
//
//  1. 必须是平铺的「字符串 -> 字符串」对象。嵌套会在 go-i18n 里被拼成 a.b.c 形式的
//     id，与"源串即 id"的模型冲突，本项目的生成器也只产出平铺结构
//  2. 键不得命中 reservedKeys，否则整份文件解析失败或条目丢失
//  3. 解析出的消息条数必须等于文件里的键数，否则说明有条目被 go-i18n 丢弃
//
// 路径必须保持 "locales/<tag>.json" 的形态：go-i18n 是从文件路径反推语言标签的
// （见 i18n/parse.go 的 parsePath），改路径会让标签解析失败。
func loadLocaleFile(bundle *goi18n.Bundle, tag string) (int, error) {
	path := "locales/" + tag + ".json"
	// 本文件里的错误信息一律用英文：它们描述的是"语言文件本身有问题"，
	// 此时翻译器恰恰不可用，不存在翻译它们的可能。详见包注释的说明。
	buf, err := localeFS.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("failed to read locale file %s: %w", path, err)
	}

	var raw map[string]any
	if err := json.Unmarshal(buf, &raw); err != nil {
		return 0, fmt.Errorf("failed to parse locale file %s: %w", path, err)
	}
	for key, val := range raw {
		if _, ok := val.(string); !ok {
			return 0, fmt.Errorf("locale file %s: value of %q is not a string; locale files must be a flat {source string: translation} object",
				path, key)
		}
		if err := ValidateMessageID(key); err != nil {
			return 0, fmt.Errorf("locale file %s: %w", path, err)
		}
	}

	file, err := bundle.ParseMessageFileBytes(buf, path)
	if err != nil {
		return 0, fmt.Errorf("failed to parse locale file %s: %w", path, err)
	}
	if len(file.Messages) != len(raw) {
		return 0, fmt.Errorf("locale file %s: parsed %d messages but the file has %d keys, so some entries were dropped by go-i18n",
			path, len(file.Messages), len(raw))
	}
	return len(file.Messages), nil
}

// ValidateMessageID 校验 id 能否安全地用作消息 id。
//
// 命中 go-i18n 保留字的 id 会让整份语言文件解析失败或条目丢失（见 reservedKeys 的
// 说明），必须在写入语言文件之前就拦下。提取工具在生成 en.json 时调用本函数，
// 与运行期加载共用同一份判定，避免两处规则漂移。
func ValidateMessageID(id string) error {
	if _, ok := reservedKeys[strings.ToLower(id)]; ok {
		return fmt.Errorf("message id %q collides with a go-i18n reserved word (%s), which would make the whole locale file fail to parse or silently drop entries; please reword this message",
			id, strings.Join(reservedWords(), "/"))
	}
	return nil
}

// reservedWords 返回排好序的保留字列表，仅用于错误信息。
func reservedWords() []string {
	out := make([]string, 0, len(reservedKeys))
	for k := range reservedKeys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// T 返回 msg 在当前语言下的译文，data 是模板变量（可为 nil）。
//
// msg 既是英文原文也是消息 id。查不到译文时逐级回落，最终落到 DefaultMessage
// （即 msg 本身），所以本函数**永远不会返回空串或裸 key**，最差情况就是返回英文原文。
//
// 注意这里不看 error：go-i18n 在消息缺失时仍然返回兜底文本并附带非 nil error
// （见 i18n/localizer.go），以 error 为准会把"正常的英文兜底"误判成失败。
func T(msg string, data map[string]any) string {
	if localizer == nil {
		// Init 未调用或失败：返回源串而不是 panic，保证任何调用路径都不会崩
		return msg
	}
	out, _ := localizer.Localize(&goi18n.LocalizeConfig{
		MessageID: msg,
		// DefaultMessage.ID 必须与 MessageID 一致，否则 go-i18n 返回
		// messageIDMismatchErr（见 i18n/localizer.go 的 LocalizeWithTag）
		DefaultMessage: &goi18n.Message{ID: msg, Other: msg},
		TemplateData:   data,
		TemplateParser: missingKeyErrorParser,
	})
	if out == "" {
		// 模板渲染失败等异常情况，同样回落源串
		return msg
	}
	return out
}

// Current 返回当前生效的语言标签。
func Current() string {
	return current
}

// Supported 返回随二进制发布的语言标签列表。
// 返回副本，避免调用方改动内部状态。
func Supported() []string {
	out := make([]string, len(supported))
	copy(out, supported)
	return out
}

// Normalize 把外部传入的语言串收敛到受支持的语言标签，无法识别时回退默认语言。
//
// 这是**宽松**匹配，专为 --lang 这类"用户随手输入、不该因此失败"的入口而设计：
// 给什么都返回一个可用标签，最差落到默认语言。
//
// 需要严格校验的场合（如 ggt config set language）**不要**用它：Normalize("fr")
// 返回 "en"，照搬会把用户输入的 fr 静默改写成 en。那种场合应先用 Strict 判断，
// 或直接比对 Supported()。
//
// 匹配分两轮，均为大小写不敏感：
//  1. 完全相等，如 "zh-CN" 命中 "zh-CN"
//  2. 主语言子标签相等，使 "zh"、"zh-Hans"、"zh-TW" 都落到 "zh-CN"，
//     "en-US" 落到 "en" —— 当前只发布 en 与 zh-CN 两种，同语种内的地区差异
//     一律收敛到已发布的那一个
func Normalize(lang string) string {
	raw := strings.ToLower(strings.TrimSpace(lang))
	if raw == "" {
		return DefaultLanguage
	}

	supported := Supported()
	for _, s := range supported {
		if strings.EqualFold(s, raw) {
			return s
		}
	}

	primary := primarySubtag(raw)
	for _, s := range supported {
		if primarySubtag(strings.ToLower(s)) == primary {
			return s
		}
	}

	return DefaultLanguage
}

// IsSupported 严格判断 lang 是否对应一个受支持的语言。
//
// 与 Normalize 的区别在于对"无法识别"的态度：Normalize 回退默认语言（宽松，为 --lang
// 而生），本函数如实返回 false。config set language 必须用它——否则用户输入 fr 会被
// 静默改写成 en，而输入 fr 与输入 en 的意图显然不同。
//
// 同语种的地区/字形变体视为受支持，如 zh-Hans、en-US。
func IsSupported(lang string) bool {
	raw := strings.ToLower(strings.TrimSpace(lang))
	if raw == "" {
		return false
	}
	primary := primarySubtag(raw)
	for _, s := range Supported() {
		if primarySubtag(strings.ToLower(s)) == primary {
			return true
		}
	}
	return false
}

// primarySubtag 返回语言标签的主语言子标签，如 "zh-CN" -> "zh"、"en" -> "en"。
func primarySubtag(tag string) string {
	if i := strings.IndexByte(tag, '-'); i > 0 {
		return tag[:i]
	}
	return tag
}
