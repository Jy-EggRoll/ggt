package config

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// 本文件按“配置项类型”覆盖 Normalize 的规则，再用一张表把同一份输入送进四条路径，
// 断言它们对“收不收、收成什么”给出同一个结论。类型规则与路径一致性分开写，
// 是因为前者能穷举边界，后者要证明这些规则真的被四条路径共用。

type normalizeCase struct {
	name    string
	in      any
	want    any
	wantErr bool
}

func mustSetting(t *testing.T, key string) Setting {
	t.Helper()
	s, ok := Lookup(key)
	if !ok {
		t.Fatalf("注册表里没有 %q", key)
	}
	return s
}

func runNormalizeCases(t *testing.T, s Setting, cases []normalizeCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := s.Normalize(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("Normalize(%#v) 错误 = %v，wantErr=%v", c.in, err, c.wantErr)
			}
			if c.wantErr {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Normalize(%#v) = %#v，应为 %#v", c.in, got, c.want)
			}
		})
	}
}

// TestNormalizeStringSetting 断言字符串项只接受 JSON 字符串。
//
// theme 是自由字符串（不校验主题是否存在），正好用来单独观察类型规则：
// 数字、布尔、对象、数组、null 都必须被拒绝，而不是被转成 "1"/"true"/"[...]"。
func TestNormalizeStringSetting(t *testing.T) {
	s := mustSetting(t, "theme")
	runNormalizeCases(t, s, []normalizeCase{
		{"普通字符串", "builtin:vscode/2026-dark.json", "builtin:vscode/2026-dark.json", false},
		{"去首尾空白", "  builtin:x  ", "builtin:x", false},
		{"空串合法", "", "", false},
		{"JSON 数字", json.Number("1"), nil, true},
		{"JSON 布尔", true, nil, true},
		{"JSON 对象", map[string]any{"a": 1}, nil, true},
		{"JSON 数组", []any{"x"}, nil, true},
		{"null", nil, nil, true},
	})
}

// TestNormalizeIntSetting 覆盖整数项的全部边界：JSON 整数值、规范十进制串、
// 各种“看起来像整数”的写法，以及注册表声明的范围。
func TestNormalizeIntSetting(t *testing.T) {
	timeout := mustSetting(t, "notify_timeout") // 0..600，下界 0 本身合法
	runNormalizeCases(t, timeout, []normalizeCase{
		{"JSON 整数", json.Number("60"), 60, false},
		{"JSON 整数带小数零", json.Number("60.0"), 60, false},
		{"JSON 整数带指数", json.Number("6e1"), 60, false},
		{"十进制字符串", "60", 60, false},
		{"下界零", json.Number("0"), 0, false},
		{"负零字符串", "-0", nil, true},
		{"JSON 浮点", json.Number("1.5"), nil, true},
		{"精度超过浮点的非整数", json.Number("1.0000000000000001"), nil, true},
		{"带首部空白", " 60", nil, true},
		{"带尾部空白", "60 ", nil, true},
		{"前导零", "060", nil, true},
		{"十六进制", "0x3c", nil, true},
		{"二进制前缀", "0b111100", nil, true},
		{"下划线分隔", "6_0", nil, true},
		{"浮点字符串", "60.0", nil, true},
		{"正号前缀", "+60", nil, true},
		{"空串", "", nil, true},
		{"JSON 布尔", true, nil, true},
		{"JSON 对象", map[string]any{}, nil, true},
		{"JSON 数组", []any{}, nil, true},
		{"null", nil, nil, true},
		{"低于下界", json.Number("-1"), nil, true},
		{"高于上界", json.Number("601"), nil, true},
	})

	low := mustSetting(t, "size_bucket_low_mb") // 1..1000000
	runNormalizeCases(t, low, []normalizeCase{
		{"正整数", json.Number("500"), 500, false},
		{"数字字符串", "500", 500, false},
		{"下界外一号", json.Number("0"), nil, true},
		{"上界", json.Number("1000000"), 1000000, false},
		{"上界外一号", json.Number("1000001"), nil, true},
	})
}

// TestNormalizeConcurrencySetting 断言并发数的专属取值域：auto 或 1 到 64 的整数，
// JSON 数字与字符串两种写法都收敛到同一个字符串形态。
func TestNormalizeConcurrencySetting(t *testing.T) {
	s := mustSetting(t, "concurrency")
	runNormalizeCases(t, s, []normalizeCase{
		{"字符串 auto", "auto", "auto", false},
		{"大写 AUTO", "AUTO", "auto", false},
		{"空串按 auto", "", "auto", false},
		{"整数字符串", "8", "8", false},
		{"带空白的整数字符串", " 8 ", "8", false},
		{"JSON 整数", json.Number("8"), "8", false},
		{"JSON 整数带小数零", json.Number("8.0"), "8", false},
		{"JSON 整数带指数", json.Number("8e0"), "8", false},
		{"JSON 下界", json.Number("1"), "1", false},
		{"JSON 上界", json.Number("64"), "64", false},
		{"字符串下界", "1", "1", false},
		{"字符串上界", "64", "64", false},
		{"JSON 下界外", json.Number("0"), nil, true},
		{"JSON 上界外", json.Number("65"), nil, true},
		{"字符串上界外", "65", nil, true},
		{"字符串负数", "-3", nil, true},
		{"JSON 浮点", json.Number("8.5"), nil, true},
		{"浮点字符串", "8.5", nil, true},
		{"JSON 布尔", true, nil, true},
		{"JSON 数组", []any{}, nil, true},
		{"JSON 对象", map[string]any{}, nil, true},
		{"null", nil, nil, true},
		{"旧语义值", "CPUFull", nil, true},
	})
}

// TestNormalizeBoolSetting 断言布尔项的同义写法只保留 true/false，
// 且大小写与首尾空白不敏感。
func TestNormalizeBoolSetting(t *testing.T) {
	s := mustSetting(t, "ignore_submodules")
	runNormalizeCases(t, s, []normalizeCase{
		{"JSON true", true, true, false},
		{"JSON false", false, false, false},
		{"小写 true", "true", true, false},
		{"大写 TRUE", "TRUE", true, false},
		{"带空白", " true ", true, false},
		{"标题式 False", " False ", false, false},
		{"数字 1", "1", nil, true},
		{"数字 0", "0", nil, true},
		{"单字母 t", "t", nil, true},
		{"单字母 f", "f", nil, true},
		{"空串", "", nil, true},
		{"yes", "yes", nil, true},
		{"JSON 数字", json.Number("1"), nil, true},
		{"null", nil, nil, true},
	})
}

// TestNormalizeEnumSetting 断言枚举项先去空白再忽略大小写匹配，并返回候选里的规范值；
// 不匹配时报错必须列出候选项。
func TestNormalizeEnumSetting(t *testing.T) {
	s := mustSetting(t, "size_unit")
	runNormalizeCases(t, s, []normalizeCase{
		{"规范值", "decimal", "decimal", false},
		{"大写", "BINARY", "binary", false},
		{"带空白", "  binary  ", "binary", false},
		{"非法取值", "octal", nil, true},
		{"JSON 数字", json.Number("2"), nil, true},
		{"JSON 布尔", true, nil, true},
		{"null", nil, nil, true},
	})

	_, err := s.Normalize("octal")
	if err == nil {
		t.Fatal("非法枚举值应报错")
	}
	for _, part := range []string{"size_unit", "decimal", "binary"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("枚举报错应包含 %q，实得 %q", part, err)
		}
	}
}

// TestNormalizePathsSetting 断言路径列表只接受数组、元素必须是非空字符串，
// 并且 null 视为空列表。
func TestNormalizePathsSetting(t *testing.T) {
	s := mustSetting(t, "repo_paths")
	runNormalizeCases(t, s, []normalizeCase{
		{"空数组", []any{}, []string{}, false},
		{"null 视为空列表", nil, []string{}, false},
		{"普通路径", []any{"/a", "/b"}, []string{"/a", "/b"}, false},
		{"保留路径自身的首尾空格", []any{" /a "}, []string{" /a "}, false},
		{"空串元素", []any{""}, nil, true},
		{"纯空白元素", []any{"   "}, nil, true},
		{"数字元素", []any{json.Number("1")}, nil, true},
		{"布尔元素", []any{true}, nil, true},
		{"null 元素", []any{nil}, nil, true},
		{"标量字符串", "/a", nil, true},
		{"JSON 数字", json.Number("1"), nil, true},
		{"JSON 布尔", true, nil, true},
		{"JSON 对象", map[string]any{}, nil, true},
	})
}

// TestEverySettingHasNormalize 断言注册表里每一项都提供 Normalize，
// 且默认值经同一个入口能得到自身——否则 reset --defaults 写出的文件当场非法。
func TestEverySettingHasNormalize(t *testing.T) {
	for _, s := range Settings() {
		if s.Normalize == nil {
			t.Errorf("%q 没有 Normalize", s.Key)
			continue
		}
		got, err := s.Normalize(s.Default)
		if err != nil {
			t.Errorf("%q 的默认值 %#v 通不过自己的 Normalize：%v", s.Key, s.Default, err)
			continue
		}
		if !reflect.DeepEqual(got, s.Default) {
			t.Errorf("%q 的默认值 %#v 归一后变成 %#v", s.Key, s.Default, got)
		}
	}
}

// TestParseDelegatesToNormalize 断言文本入口与 JSON 入口对同一份字符串给出同一个结论。
// Parse 在 init 里由 Normalize 派生，这里用一批有代表性的输入把这条派生关系钉住。
func TestParseDelegatesToNormalize(t *testing.T) {
	cases := []struct{ key, text string }{
		{"concurrency", "auto"},
		{"concurrency", " 8 "},
		{"concurrency", "CPUFull"},
		{"concurrency", "nonsense"},
		{"size_unit", "BINARY"},
		{"size_unit", "octal"},
		{"language", "zh"},
		{"language", "fr"},
		{"log_level", "WARN"},
		{"log_level", "trace"},
		{"ignore_submodules", " TRUE "},
		{"ignore_submodules", "1"},
		{"size_bucket_low_mb", "500"},
		{"size_bucket_low_mb", "010"},
		{"theme", "  builtin:x  "},
		{"font_ui", " Inter, sans-serif "},
		{"font_ui", "Foo;} body{display:none"},
	}
	for _, c := range cases {
		s := mustSetting(t, c.key)
		if s.Parse == nil {
			t.Errorf("%q 没有 Parse", c.key)
			continue
		}
		normalized, normalizeErr := s.Normalize(c.text)
		parsed, parseErr := s.Parse(c.text)
		if (normalizeErr != nil) != (parseErr != nil) {
			t.Errorf("%s=%q：Normalize 错误=%v，Parse 错误=%v", c.key, c.text, normalizeErr, parseErr)
			continue
		}
		if parseErr == nil && !reflect.DeepEqual(normalized, parsed) {
			t.Errorf("%s=%q：Normalize 得到 %#v，Parse 得到 %#v", c.key, c.text, normalized, parsed)
		}
	}
}

// configFieldValue 按 json tag 取出 Config 结构体里某个键的值，供四路径测试比较
// “运行期实际拿到的规范值”，而不是只看返回值。
func configFieldValue(t *testing.T, cfg *Config, key string) any {
	t.Helper()
	v := reflect.ValueOf(cfg).Elem()
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).Tag.Get("json") == key {
			return v.Field(i).Interface()
		}
	}
	t.Fatalf("Config 结构体没有 json tag 为 %q 的字段", key)
	return nil
}

// TestFourPathsAgreeOnValue 把同一份输入分别送进运行期加载、config get、config validate、
// config set，断言四者对“收不收、收成什么”结论一致。
//
// 这几种输入都是实际踩过的：BINARY 校验通过却按十进制算、字符串形态的 true 运行期加载失败、
// 并发数写成布尔被弱转成 1、repo_paths 的空串与标量、整数前导零。路径项由 ggt repo 管理，
// config set 这里只断言它仍被管理策略拒绝，不参与取值结论的比较。
func TestFourPathsAgreeOnValue(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		json    string
		text    string
		want    any
		wantErr bool
		skipSet bool
	}{
		{"size_unit 大写归一", "size_unit", `"BINARY"`, "BINARY", "binary", false, false},
		{"size_unit 带空白", "size_unit", `" binary "`, " binary ", "binary", false, false},
		{"size_unit 非法", "size_unit", `"octal"`, "octal", nil, true, false},
		{"size_unit JSON 数字", "size_unit", `2`, "2", nil, true, false},
		{"bool 字符串带空白", "ignore_submodules", `" true "`, " true ", true, false, false},
		{"bool 数字字符串被拒", "ignore_submodules", `"1"`, "1", nil, true, false},
		{"bool JSON 数字被拒", "ignore_submodules", `1`, "1", nil, true, false},
		{"bool 空串被拒", "ignore_submodules", `""`, "", nil, true, false},
		{"bool 字符串 false", "push_tags", `" false "`, " false ", false, false, false},
		{"int JSON 数字", "size_bucket_low_mb", `500`, "500", 500, false, false},
		{"int 数字字符串", "size_bucket_low_mb", `"500"`, "500", 500, false, false},
		{"int 前导零被拒", "size_bucket_low_mb", `"010"`, "010", nil, true, false},
		{"int 带空白被拒", "size_bucket_low_mb", `" 500 "`, " 500 ", nil, true, false},
		{"int 零被拒", "size_bucket_low_mb", `0`, "0", nil, true, false},
		{"int 浮点被拒", "size_bucket_low_mb", `1.5`, "1.5", nil, true, false},
		{"int JSON 布尔被拒", "size_bucket_low_mb", `true`, "true", nil, true, false},
		{"concurrency auto", "concurrency", `"auto"`, "auto", "auto", false, false},
		{"concurrency 数字字符串", "concurrency", `"8"`, "8", "8", false, false},
		{"concurrency 带空白", "concurrency", `" 8 "`, " 8 ", "8", false, false},
		{"concurrency JSON 数字", "concurrency", `8`, "8", "8", false, false},
		{"concurrency JSON 浮点被拒", "concurrency", `8.5`, "8.5", nil, true, false},
		{"concurrency 上界外", "concurrency", `65`, "65", nil, true, false},
		{"concurrency 布尔被拒", "concurrency", `true`, "true", nil, true, false},
		{"language 别名归一", "language", `"zh"`, "zh", "zh-CN", false, false},
		{"language JSON 数字被拒", "language", `1`, "1", nil, true, false},
		{"log_level 大写归一", "log_level", `"WARN"`, "WARN", "warn", false, false},
		{"log_level JSON 布尔被拒", "log_level", `true`, "true", nil, true, false},
		{"theme 去空白", "theme", `"  builtin:x  "`, "  builtin:x  ", "builtin:x", false, false},
		{"font 去空白", "font_ui", `" Inter, sans-serif "`, " Inter, sans-serif ", "Inter, sans-serif", false, false},
		{"paths 普通列表", "repo_paths", `["/a"]`, "", []string{"/a"}, false, true},
		{"paths null", "repo_paths", `null`, "", []string{}, false, true},
		{"paths 空串元素", "repo_paths", `[""]`, "", nil, true, true},
		{"paths 标量", "repo_paths", `"/a"`, "", nil, true, true},
		{"paths JSON 对象", "repo_paths", `{}`, "", nil, true, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "c.json")
			writeFile(t, path, `{"`+c.key+`": `+c.json+`}`)
			s := mustSetting(t, c.key)

			cfg, loadErr := LoadConfigAt(path)
			if (loadErr != nil) != c.wantErr {
				t.Errorf("运行期加载错误 = %v，wantErr=%v", loadErr, c.wantErr)
			}

			got, getErr := EffectiveAt(path, c.key)
			if (getErr != nil) != c.wantErr {
				t.Errorf("config get 错误 = %v，wantErr=%v", getErr, c.wantErr)
			}

			issues, err := ValidateAt(path)
			if err != nil {
				t.Fatalf("体检失败: %v", err)
			}
			validateRejected := false
			for _, is := range issues {
				if is.Level == LevelError {
					validateRejected = true
				}
			}
			if validateRejected != c.wantErr {
				t.Errorf("config validate 拒绝=%v，wantErr=%v：%+v", validateRejected, c.wantErr, issues)
			}

			if !c.wantErr {
				if !reflect.DeepEqual(got, c.want) {
					t.Errorf("config get = %#v，应为 %#v", got, c.want)
				}
				if field := configFieldValue(t, cfg, c.key); !reflect.DeepEqual(field, c.want) {
					t.Errorf("运行期值 = %#v，应为 %#v", field, c.want)
				}
			}

			// 路径项由 ggt repo 管理：set 无论取值如何都应被管理策略拒绝，
			// 它的取值结论由上面三条路径负责
			if s.ManagedBy != "" {
				if _, err := SetFromTextAt(path, c.key, c.text, false); err == nil {
					t.Error("由 ggt repo 管理的项不应能通过 config set 写入")
				}
				return
			}
			if c.skipSet {
				return
			}

			written, setErr := SetFromTextAt(path, c.key, c.text, false)
			if (setErr != nil) != c.wantErr {
				t.Fatalf("config set 错误 = %v，wantErr=%v", setErr, c.wantErr)
			}
			if c.wantErr {
				return
			}
			if written != ValueText(c.want) {
				t.Errorf("config set 写入文本 = %q，应为 %q", written, ValueText(c.want))
			}
			// 写入后再读一遍：文本入口 → 文件 → 运行期这条完整链路也要给出同一个值
			cfg2, err := LoadConfigAt(path)
			if err != nil {
				t.Fatalf("写入后重新加载失败: %v", err)
			}
			if field := configFieldValue(t, cfg2, c.key); !reflect.DeepEqual(field, c.want) {
				t.Errorf("写入后运行期值 = %#v，应为 %#v", field, c.want)
			}
		})
	}
}

// TestJSONNumberForConcurrency 断言数字型并发数重新成为合法输入：JSON 数字 8 与
// 字符串 "8" 归一成同一个 "8"，而浮点、布尔与超限值仍被拒绝。
func TestJSONNumberForConcurrency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	writeFile(t, path, `{"concurrency": 8}`)

	cfg, err := LoadConfigAt(path)
	if err != nil {
		t.Fatalf("数字型 concurrency 应能加载: %v", err)
	}
	if cfg.Concurrency != "8" {
		t.Errorf("运行期 concurrency = %q，应为 \"8\"", cfg.Concurrency)
	}
	got, err := EffectiveAt(path, "concurrency")
	if err != nil || got != "8" {
		t.Fatalf("config get = (%v, %v)，应为 (\"8\", nil)", got, err)
	}
	issues, err := ValidateAt(path)
	if err != nil {
		t.Fatalf("体检失败: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("数字型 concurrency 不应报问题，实得 %+v", issues)
	}

	// JSON 8 与文本 "8" 都写到同一个规范形态
	written, err := SetFromTextAt(path, "concurrency", "8", false)
	if err != nil {
		t.Fatalf("命令行文本 8 应能写入: %v", err)
	}
	if written != "8" {
		t.Errorf("写入文本 = %q，应为 8", written)
	}
}
