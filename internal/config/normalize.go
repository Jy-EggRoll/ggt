// normalize.go 是配置值的唯一解析入口。
//
// 配置文件里的 JSON 值、命令行与网页传来的文本、config get 与 config validate 读到的
// 原始值，全部经 Setting.Normalize 收敛成同一个规范形态。此前四条路径各有一套规则，
// 同一份文件在不同命令下会给出相反结论：size_unit: "BINARY" 校验通过、运行期却按十进制；
// {"concurrency": true} 运行期按 1 放大运行；{"ignore_submodules": " true "} 运行期
// 加载失败、validate 却只说“仍然能工作”。把规则收进这里之后，类型判定只有一份实现。
//
// 这些规则刻意不追求“尽量猜用户想写什么”。弱类型转换正是上面那些分叉的来源：
// 布尔值能变成 "1"、数字能变成字符串、带空白的数字能变成整数——每条路径对这些变形的
// 容忍程度不同，于是同一份文件在四处得到不同答案。现在每个类型只接受一种明确形态，
// 其余一律报错并说清期望类型。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/jy-eggroll/eggokit/l10n"
)

// stringValueNormalizer 只接受 JSON 字符串，再把内容交给这一项自己的语义解析器。
//
// 字符串项不再接受数字、布尔、对象、数组：把它们弱转成文本正是“文件里写着一种形态、
// 各命令读出另一种含义”的入口。json.Number 的底层类型虽然是字符串，但它是独立类型，
// 这里的类型断言会把它挡在外面——只关掉 mapstructure 的弱类型开关是拦不住它的。
//
// 语义解析返回 ErrInvalidValue 时由调用方用 Setting.Expected 渲染成统一文案；
// 类型错误与枚举错误本身已经写清了键名、实际类型或候选，直接透传。
func stringValueNormalizer(key string, parse func(string) (any, error)) func(any) (any, error) {
	return func(v any) (any, error) {
		text, ok := v.(string)
		if !ok {
			return nil, expectedStringError(key, v)
		}
		return parse(text)
	}
}

// enumValueNormalizer 由候选清单生成规范化函数：去首尾空白后大小写不敏感地匹配，
// 返回候选清单里的规范值。
//
// 枚举项在文件里写 "BINARY" 时，运行期必须和 config get、validate 一样看到 "binary"。
// 若只校验不归一，size 命令的比较、页面的选中项都要各自再处理一次大小写，
// 而每一处漏掉都会表现为“配置写着 binary，实际按 decimal 算”。
func enumValueNormalizer(key string, options func() []Option) func(any) (any, error) {
	return func(v any) (any, error) {
		text, ok := v.(string)
		if !ok {
			return nil, expectedStringError(key, v)
		}
		value := strings.TrimSpace(text)
		opts := options()
		for _, o := range opts {
			if strings.EqualFold(value, o.Value) {
				return o.Value, nil
			}
		}
		return nil, choiceError(key, opts)
	}
}

// languageValueNormalizer 与 enumValueNormalizer 的区别是它还认 zh、zh-Hans 这类别名，
// 规范形态由 l10n.Normalize 决定（zh 与 zh-Hans 都存成 zh-CN）。
// 别名不是候选清单里的取值，所以匹配失败时仍按枚举的措辞列出可选语言。
func languageValueNormalizer(key string, options func() []Option) func(any) (any, error) {
	return func(v any) (any, error) {
		text, ok := v.(string)
		if !ok {
			return nil, expectedStringError(key, v)
		}
		if lang, err := parseLanguage(text); err == nil {
			return lang, nil
		}
		return nil, choiceError(key, options())
	}
}

// logLevelValueNormalizer 的合法取值交给 logger 判定（它是运行期唯一的真相），
// 报错时再按候选清单列举。若在注册表里另立一套 switch，logger 新增级别时
// 会出现“运行期认得、config set 拒收”的错位。
func logLevelValueNormalizer(key string, options func() []Option) func(any) (any, error) {
	return func(v any) (any, error) {
		text, ok := v.(string)
		if !ok {
			return nil, expectedStringError(key, v)
		}
		if level, err := parseLogLevel(text); err == nil {
			return level, nil
		}
		return nil, choiceError(key, options())
	}
}

// concurrencyNormalizer 处理并发数这一项的特殊取值域：auto 或 1 到 maxConcurrency 的整数。
//
// 它虽然声明为 KindString（Config 里的字段是字符串，auto 需要落盘成文本），但语义是数值型：
// 手写配置时 {"concurrency": 8} 是最自然的写法，若按普通字符串项拒绝 JSON 数字，
// 规则就会反过来卡住合理用法。因此这里单独放行 JSON 整数值，并把它与字符串形式
// 收敛到同一个规范结果（"8" 而非 8），让运行期、get、validate 看到同一形态。
// JSON 布尔、非整数数字、数组与对象仍然拒绝；错误文案沿用 invalidValueError 或
// “Expected ... got ...” 这一套。
func concurrencyNormalizer(key string) func(any) (any, error) {
	return func(v any) (any, error) {
		switch value := v.(type) {
		case string:
			return parseConcurrency(value)
		case json.Number:
			n, ok := jsonNumberToInt(value)
			if !ok || n < 1 || n > maxConcurrency {
				return nil, ErrInvalidValue
			}
			return strconv.Itoa(n), nil
		default:
			return nil, expectedConcurrencyError(key, v)
		}
	}
}

// boolValueNormalizer 接受 JSON 布尔，以及忽略大小写与首尾空白的 "true"/"false"。
//
// 刻意不再接受 "1"/"0"/"t"/"f"：这些同义写法正是四条路径结论不一的来源。
// 布尔值本身没有歧义，多一套同义形式只会让“写 1 算不算开”变成各命令各自回答的问题。
func boolValueNormalizer(key string) func(any) (any, error) {
	return func(v any) (any, error) {
		if b, ok := v.(bool); ok {
			return b, nil
		}
		text, ok := v.(string)
		if !ok {
			return nil, expectedBoolError(key, v)
		}
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return nil, ErrInvalidValue
	}
}

// intValueNormalizer 接受 JSON 数字（值必须是整数）与十进制数字字符串。
//
// 字符串只收“纯十进制整数”这一种写法：带首尾空白、前导零、0x/0b 前缀、下划线或小数点
// 一律拒绝。这些写法大多能被 strconv 或某种语言悄悄接受，但它们的解释随入口而变，
// 正是需要消除的分歧。范围检查使用注册表声明的 Min/Max，页面输入框与运行期因此同源。
func intValueNormalizer(key string, min, max *int) func(any) (any, error) {
	return func(v any) (any, error) {
		var n int
		switch value := v.(type) {
		case json.Number:
			parsed, ok := jsonNumberToInt(value)
			if !ok {
				return nil, ErrInvalidValue
			}
			n = parsed
		case string:
			parsed, ok := decimalStringToInt(value)
			if !ok {
				return nil, ErrInvalidValue
			}
			n = parsed
		case int:
			// 规范形态（注册表默认值）也会经过这里；JSON 解码走 UseNumber，不会产生 int
			n = value
		default:
			return nil, expectedIntegerError(key, v)
		}
		if (min != nil && n < *min) || (max != nil && n > *max) {
			return nil, ErrInvalidValue
		}
		return n, nil
	}
}

// pathsValueNormalizer 只接受 JSON 数组，元素必须是非空字符串；null 视为空列表。
//
// 路径项没有“标量等价物”：写成 "/a" 时，接受它等于替用户猜“这是只有一个元素的列表”。
// 元素只做“去空白后非空”的判断，不裁剪原值——路径本身可以含首尾空格，
// 真正该拒绝的是整条都是空白、却看起来像配置了一条路径的条目。
func pathsValueNormalizer(key string) func(any) (any, error) {
	return func(v any) (any, error) {
		if v == nil {
			// JSON null 与缺键同义：运行期本来就是空列表
			return []string{}, nil
		}
		// JSON 解码只会给出 []any；[]string 来自注册表默认值（以及测试直接构造的规范值）。
		// 两种形态走同一套元素检查，默认值因此也能通过自己的 Normalize。
		var items []any
		switch list := v.(type) {
		case []any:
			items = list
		case []string:
			items = make([]any, len(list))
			for i, item := range list {
				items[i] = item
			}
		default:
			return nil, expectedArrayError(key, v)
		}
		out := make([]string, 0, len(items))
		for _, item := range items {
			path, ok := item.(string)
			if !ok {
				return nil, expectedPathError(key, item)
			}
			if strings.TrimSpace(path) == "" {
				return nil, emptyPathError(key)
			}
			out = append(out, path)
		}
		return out, nil
	}
}

// trimString 是主题类配置项的内容规则：只去首尾空白，不限定取值。
// 主题文件是用户随时增删的，写入时拒绝一个“当前不存在”的 id 会让
// “先删旧主题、再设新主题”这个正常顺序做不成。
func trimString(s string) (any, error) {
	return strings.TrimSpace(s), nil
}

// normalizeValue 调用 Setting.Normalize，并把“取值不合法”这一类错误渲染成
// 带键名与 Expected 的文案。类型错误、枚举候选错误、旧语义值错误都直接透传：
// 它们各自的措辞已经包含用户需要的信息，套上通用模板反而会丢掉候选列表。
//
// 运行期加载、config get、config validate 三处共用这里；config set 走 Parse，
// Parse 在 init 里由同一个 Normalize 派生，渲染规则也一致。
func normalizeValue(s Setting, v any) (any, error) {
	out, err := s.Normalize(v)
	if err == nil {
		return out, nil
	}
	if errors.Is(err, ErrInvalidValue) {
		return nil, invalidValueError(s, ValueText(v))
	}
	return nil, err
}

// expectedStringError 说明 JSON 类型的期望值，而不是只给一句“取值非法”：
// 用户看到 "got number" 才知道该加引号，而不是去改数字本身。
func expectedStringError(key string, v any) error {
	return fmt.Errorf("%s", l10n.T("Expected a string for {{.Key}}, got {{.Type}}",
		map[string]any{"Key": key, "Type": jsonTypeName(v)}))
}

// expectedConcurrencyError 说明并发数既接受 auto 也接受整数，避免用户误以为
// 只能写某一种形态。
func expectedConcurrencyError(key string, v any) error {
	return fmt.Errorf("%s", l10n.T(`Expected "auto" or an integer for {{.Key}}, got {{.Type}}`,
		map[string]any{"Key": key, "Type": jsonTypeName(v)}))
}

// expectedIntegerError 用于整数项收到布尔、对象、数组或 null 的情形；
// 字符串写法的错误由 invalidValueError 报出，那里会连同允许范围一起说明。
func expectedIntegerError(key string, v any) error {
	return fmt.Errorf("%s", l10n.T("Expected an integer for {{.Key}}, got {{.Type}}",
		map[string]any{"Key": key, "Type": jsonTypeName(v)}))
}

// expectedBoolError 用于布尔项收到数字、对象、数组或 null 的情形。
func expectedBoolError(key string, v any) error {
	return fmt.Errorf("%s", l10n.T("Expected true or false for {{.Key}}, got {{.Type}}",
		map[string]any{"Key": key, "Type": jsonTypeName(v)}))
}

// expectedArrayError 沿用“应为数组”的说法。路径项的 Expected 是空的
// （数组的合法取值没法用一句话概括），套用标量模板会渲染出 “(expected )”。
func expectedArrayError(key string, v any) error {
	return fmt.Errorf("%s", l10n.T("Expected an array for {{.Key}}, got {{.Type}}",
		map[string]any{"Key": key, "Type": jsonTypeName(v)}))
}

// expectedPathError 说明数组元素必须是字符串。
func expectedPathError(key string, v any) error {
	return fmt.Errorf("%s", l10n.T("Expected a path string in {{.Key}}, got {{.Type}}",
		map[string]any{"Key": key, "Type": jsonTypeName(v)}))
}

// emptyPathError 说明路径条目不能为空。空串在运行期会被当成当前目录，
// 那等于把“少写了一条路径”变成“扫描了启动时所在的目录”，必须当场拒绝。
func emptyPathError(key string) error {
	return fmt.Errorf("%s", l10n.T("Empty path in {{.Key}}: entries must not be empty",
		map[string]any{"Key": key}))
}

// choiceError 列出枚举项的候选项。写清候选比只说“取值非法”有用：
// 用户不必再去翻文档或打开设置面板确认这一项到底认哪些值。
func choiceError(key string, opts []Option) error {
	return fmt.Errorf("%s", l10n.T("{{.Key}} must be one of: {{.Values}}",
		map[string]any{"Key": key, "Values": optionValuesText(opts)}))
}

// jsonNumberToInt 把 JSON 数字精确地取成 int，非整数值返回 false。
//
// 先用 Int64 处理最常见的整数写法；带小数点或指数的写法交给 big.Rat 精确判定，
// 不用 float64——1.0000000000000001 会被浮点舍入成 1，于是“不是整数”的输入被悄悄接受。
// 配置文件里的分桶阈值最大一百万，精确解析的代价可以忽略。
func jsonNumberToInt(n json.Number) (int, bool) {
	text := n.String()
	if i, err := strconv.ParseInt(text, 10, 64); err == nil {
		return intFromInt64(i)
	}
	r, ok := new(big.Rat).SetString(text)
	if !ok || !r.IsInt() || !r.Num().IsInt64() {
		return 0, false
	}
	return intFromInt64(r.Num().Int64())
}

// intFromInt64 在 32 位平台上拒绝超出 int 范围的值，避免截断后通过范围检查。
func intFromInt64(i int64) (int, bool) {
	n := int(i)
	if int64(n) != i {
		return 0, false
	}
	return n, true
}

// decimalStringToInt 只接受规范的十进制整数串：可选负号、无前导零、无空白、
// 无进制前缀、无下划线、无小数点。语法合法但超出声明范围的值由调用方拒绝。
func decimalStringToInt(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	body := s
	negative := false
	if body[0] == '-' {
		negative = true
		body = body[1:]
		if body == "" {
			return 0, false
		}
	}
	if body[0] == '0' && len(body) > 1 {
		return 0, false
	}
	for i := 0; i < len(body); i++ {
		if body[i] < '0' || body[i] > '9' {
			return 0, false
		}
	}
	// -0 不是十进制整数的规范写法；它和 0 是同一个值，接受它只会制造同义异形
	if negative && body == "0" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}
