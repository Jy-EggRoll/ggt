package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/jy-eggroll/ggt/internal/locales"
)

// 本文件的测试一律用 t.TempDir() 显式传路径，**绝不依赖 HOME**。
// 那些走默认路径的函数会读 os.UserHomeDir，而它在 Windows 上读的是 USERPROFILE——
// 一旦测试要写文件或删文件，漏一处就会动到开发者自己的真实配置。

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入 %s 失败: %v", path, err)
	}
}

// TestSettingsMatchConfigFields 断言注册表与 Config 结构体的 json tag 一一对应。
//
// 将来给 Config 加字段却忘了登记进 Settings() 时，这条测试会失败：
// 否则那个键既不能被 set，也不会被 validate 检查，成为静默的盲区。
func TestSettingsMatchConfigFields(t *testing.T) {
	tags := map[string]bool{}
	rt := reflect.TypeOf(Config{})
	for i := 0; i < rt.NumField(); i++ {
		if tag := rt.Field(i).Tag.Get("json"); tag != "" && tag != "-" {
			tags[tag] = true
		}
	}

	registered := map[string]bool{}
	for _, s := range Settings() {
		registered[s.Key] = true
	}

	for tag := range tags {
		if !registered[tag] {
			t.Errorf("Config 的字段 %q 未登记进 Settings()，该键将无法 set 也不会被 validate 检查", tag)
		}
	}
	for key := range registered {
		if !tags[key] {
			t.Errorf("Settings() 里的 %q 在 Config 结构体上没有对应的 json tag", key)
		}
	}
}

// TestReadRawAtMissingFile 断言文件不存在时返回空 map 而非错误——首次写入总得有起点。
func TestReadRawAtMissingFile(t *testing.T) {
	raw, err := ReadRawAt(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("文件不存在不应报错: %v", err)
	}
	if len(raw) != 0 {
		t.Errorf("应为空 map，实得 %v", raw)
	}
}

// TestReadRawAtNullContent 断言文件内容为 null 时不会 panic。
// json.Unmarshal 到 nil map 后直接赋值会 panic，这是最容易被忽略的一处。
func TestReadRawAtNullContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	writeFile(t, path, "null")

	raw, err := ReadRawAt(path)
	if err != nil {
		t.Fatalf("内容为 null 不应报错: %v", err)
	}
	if len(raw) != 0 {
		t.Errorf("应为空 map，实得 %v", raw)
	}
	// 真正会暴露 nil map 的是写入
	if err := SetKeyAt(path, "concurrency", "8"); err != nil {
		t.Fatalf("在 null 文件上写入失败: %v", err)
	}
}

// TestReadRawAtPreservesLargeNumbers 断言未知键里的大整数不会因 float64 而变形。
func TestReadRawAtPreservesLargeNumbers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	const big = "12345678901234567890"
	writeFile(t, path, `{"unknown_big": `+big+`}`)

	raw, err := ReadRawAt(path)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	num, ok := raw["unknown_big"].(json.Number)
	if !ok {
		t.Fatalf("大整数应保持 json.Number，实得 %T", raw["unknown_big"])
	}
	if num.String() != big {
		t.Errorf("大整数被改写: %s", num)
	}
}

// TestReadRawAtRejectsCaseVariantKeys 断言仅大小写不同的重复键被拒绝而不是静默合并。
// 放着不管的话，哪一份值胜出取决于 map 的随机迭代顺序，每次运行结果可能不同。
func TestReadRawAtRejectsCaseVariantKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	writeFile(t, path, `{"Language": "en", "language": "zh-CN"}`)

	if _, err := ReadRawAt(path); err == nil {
		t.Fatal("仅大小写不同的重复键应被拒绝")
	}
}

// TestSetKeyNormalizesKeyCase 断言写入的键统一小写，与 viper 的读行为对齐。
func TestSetKeyNormalizesKeyCase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	if err := SetKeyAt(path, "CONCURRENCY", "8"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	raw, err := ReadRawAt(path)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if _, ok := raw["concurrency"]; !ok {
		t.Errorf("键未被小写化: %v", raw)
	}
}

// TestSetKeyRefusesToTouchACorruptFile 断言文件损坏时拒绝写入且原文件字节不变。
//
// 这是最关键的一条：若在损坏的文件上照样写入，一次 set 就会把 repo_paths 整份丢掉。
func TestSetKeyRefusesToTouchACorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	corrupt := []byte(`{"repo_paths": ["/keep/me"], `)
	if err := os.WriteFile(path, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SetKeyAt(path, "concurrency", "8"); err == nil {
		t.Fatal("损坏的文件应拒绝写入")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, corrupt) {
		t.Errorf("损坏的文件被改动了:\n%s", got)
	}
}

// TestWriteRawAtPreservesUnknownKeys 断言写入不会丢掉 ggt 不认识的键。
func TestWriteRawAtPreservesUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	writeFile(t, path, `{"my_custom_key": "keep me", "concurrency": "auto"}`)

	if err := SetKeyAt(path, "size_unit", "binary"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	raw, err := ReadRawAt(path)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if raw["my_custom_key"] != "keep me" {
		t.Errorf("未知键被丢掉了: %v", raw)
	}
	if raw["concurrency"] != "auto" || raw["size_unit"] != "binary" {
		t.Errorf("已知键不正确: %v", raw)
	}
}

// TestWriteRawAtKeepsPermissions 断言写入不会改变文件权限。
// os.CreateTemp 建出来是 0600，不处理的话会把 0644 的配置变成只有属主可读。
func TestWriteRawAtKeepsPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SetKeyAt(path, "concurrency", "8"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("权限被改成 %v，应保持 0600", fi.Mode().Perm())
	}
}

// TestWriteRawAtLeavesNoTempFile 断言原子写不在目录里留下临时文件。
func TestWriteRawAtLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")

	if err := SetKeyAt(path, "concurrency", "8"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "c.json" {
			t.Errorf("目录里残留了文件: %s", e.Name())
		}
	}
}

// TestUnsetKeyIsIdempotent 断言删除不存在的键不报错。
func TestUnsetKeyIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	for i := 0; i < 2; i++ {
		if err := UnsetKeyAt(path, "concurrency"); err != nil {
			t.Fatalf("第 %d 次删除失败: %v", i+1, err)
		}
	}
}

// TestResetAllAtDeleteMissingFile 断言删除模式下文件不存在也算成功。
// 新用户第一次执行 reset --all 必然走这个分支，报错会很莫名。
func TestResetAllAtDeleteMissingFile(t *testing.T) {
	if err := ResetAllAt(filepath.Join(t.TempDir(), "absent.json"), false); err != nil {
		t.Fatalf("删除不存在的文件应成功: %v", err)
	}
}

// TestResetAllAtWritesEmptyArrays 断言写默认值时切片是 [] 而不是 null。
func TestResetAllAtWritesEmptyArrays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	if err := ResetAllAt(path, true); err != nil {
		t.Fatalf("写入默认值失败: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "null") {
		t.Errorf("不应写出 null:\n%s", data)
	}
	if !strings.Contains(string(data), `"repo_paths": []`) {
		t.Errorf("repo_paths 应为 []:\n%s", data)
	}
}

// TestSetKeyWritesEmptySliceAsArray 断言把路径列表清空后写回，落盘的是 [] 而不是 null。
// 覆盖 ggt repo remove 掉最后一个仓库后的形态：文件里必须是空数组，否则下次读回来是
// nil，“已配置 0 个仓库”和“这个字段从未设置过”在文件层面就分不清了。
// （写入保留未知键的行为由 TestWriteRawAtPreservesUnknownKeys 覆盖，此处不重复。）
func TestSetKeyWritesEmptySliceAsArray(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	writeFile(t, path, `{"repo_paths": ["/tmp/x"]}`)

	if err := SetKeyAt(path, "repo_paths", []string{}); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "null") {
		t.Errorf("空列表不应写成 null:\n%s", data)
	}
	if !strings.Contains(string(data), `"repo_paths": []`) {
		t.Errorf("repo_paths 应为 []:\n%s", data)
	}
}

// TestValidateAtReportsProblems 断言体检能分级报出各类问题。
func TestValidateAtReportsProblems(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	writeFile(t, path, `{
	  "concurrency": "NONSENSE",
	  "typo_key": 1,
	  "size_bucket_low_mb": 900,
	  "size_bucket_high_mb": 800,
	  "repo_paths": ["relative/path"]
	}`)

	issues, err := ValidateAt(path)
	if err != nil {
		t.Fatalf("体检失败: %v", err)
	}

	var errors, warnings int
	for _, is := range issues {
		switch is.Level {
		case LevelError:
			errors++
		case LevelWarning:
			warnings++
		}
	}
	// concurrency 非法 + 未知键
	if errors < 2 {
		t.Errorf("应至少报出 2 个 error，实得 %d：%+v", errors, issues)
	}
	// 阈值倒置 + 类型不规范
	if warnings < 2 {
		t.Errorf("应至少报出 2 个 warning，实得 %d：%+v", warnings, issues)
	}
}

// TestValidateAtDetectsBOM 断言能识别 UTF-8 BOM。
// Windows 记事本“另存为 UTF-8”默认会写 BOM，而报错信息完全看不出是它引起的。
func TestValidateAtDetectsBOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	content := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"concurrency": "8"}`)...)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	issues, err := ValidateAt(path)
	if err != nil {
		t.Fatalf("体检失败: %v", err)
	}
	if len(issues) == 0 {
		t.Fatal("应报出 BOM 问题")
	}
}

// TestValidateAtMissingFileIsNotAProblem 断言文件不存在不算问题。
func TestValidateAtMissingFileIsNotAProblem(t *testing.T) {
	issues, err := ValidateAt(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("体检失败: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("文件不存在不应报问题，实得 %+v", issues)
	}
}

// TestSettingParse 覆盖各配置项解析器的边界。
func TestSettingParse(t *testing.T) {
	cases := []struct {
		key     string
		value   string
		wantErr bool
	}{
		{"concurrency", "auto", false},
		{"concurrency", "AUTO", false}, // 大小写不敏感
		{"concurrency", " Auto ", false},
		{"concurrency", "1", false},
		{"concurrency", "8", false},
		{"concurrency", "64", false}, // 上限本身合法
		{"concurrency", "0", true},
		{"concurrency", "-3", true},
		{"concurrency", "65", true}, // 上限外一号：显式调高最多 64
		{"concurrency", "abc", true},
		{"concurrency", "2000000000", true}, // 超上限：填这么大等于让信号量失去作用
		{"concurrency", "010", true},        // 前导零：与整数项同一套规范十进制规则
		{"concurrency", "+8", true},         // 正号：Atoi 会接受，配置解析刻意不接受
		{"concurrency", "8.0", true},        // 浮点字符串
		{"concurrency", "CPUFull", true},    // 旧语义值已移除，迁移在加载期完成
		{"size_bucket_low_mb", "100", false},
		{"size_bucket_low_mb", "0", true},
		{"size_bucket_low_mb", "100000000", true}, // 超上限：32 位平台会解码失败
		{"size_bucket_low_mb", " 100 ", true},     // 带首尾空白不是规范十进制串
		{"size_bucket_low_mb", "010", true},       // 前导零
		{"size_bucket_low_mb", "0x10", true},      // 进制前缀
		{"size_bucket_low_mb", "1_000", true},     // 下划线分隔
		{"size_bucket_low_mb", "100.0", true},     // 浮点
		{"size_bucket_high_mb", "800", false},
		{"size_unit", "binary", false},
		{"size_unit", "BINARY", false},
		{"size_unit", "hex", true},
		{"ignore_submodules", "true", false},
		{"ignore_submodules", "TRUE", false},
		{"ignore_submodules", " true ", false},
		{"ignore_submodules", "1", true},
		{"ignore_submodules", "0", true},
		{"ignore_submodules", "t", true},
		{"ignore_submodules", "f", true},
		{"ignore_submodules", "", true},
		{"ignore_submodules", "yes", true},
		{"language", "en", false},
		{"language", "zh", false},
		{"language", "en-US", false},
		{"language", "fr", true}, // 必须拒绝而不是静默回退成 en
	}

	for _, c := range cases {
		s, ok := Lookup(c.key)
		if !ok {
			t.Fatalf("未登记的键 %q", c.key)
		}
		if s.Parse == nil {
			t.Fatalf("%q 没有 Parse", c.key)
		}
		if _, err := s.Parse(c.value); (err != nil) != c.wantErr {
			t.Errorf("%s=%q: err=%v, wantErr=%v", c.key, c.value, err, c.wantErr)
		}
	}
}

// TestSettingParseStoresCanonicalForm 断言 auto 与语言存的是规范形态，
// 避免文件里出现 AUTO/Auto、zh/zh-Hans 这类同义异形写法。
func TestSettingParseStoresCanonicalForm(t *testing.T) {
	cases := []struct {
		key, value, want string
	}{
		{"concurrency", " AUTO ", "auto"},
		{"concurrency", "Auto", "auto"},
		{"concurrency", " 8 ", "8"},
		{"size_unit", "BINARY", "binary"},
		{"language", "zh", "zh-CN"},
		{"language", "zh-Hans", "zh-CN"},
		{"language", "en-US", "en"},
	}
	for _, c := range cases {
		s, _ := Lookup(c.key)
		got, err := s.Parse(c.value)
		if err != nil {
			t.Errorf("%s=%q 解析失败: %v", c.key, c.value, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s=%q 存成 %v，应为 %q", c.key, c.value, got, c.want)
		}
	}
}

// TestLookupIgnoresCaseAndSpace 断言查键容忍大小写与首尾空白。
func TestLookupIgnoresCaseAndSpace(t *testing.T) {
	for _, key := range []string{"concurrency", "CONCURRENCY", " concurrency "} {
		if _, ok := Lookup(key); !ok {
			t.Errorf("Lookup(%q) 应命中", key)
		}
	}
	if _, ok := Lookup("no_such_key"); ok {
		t.Error("未登记的键不应命中")
	}
}

// TestManagedKeysHaveNoParse 断言由 ggt repo 管理的键不提供 Parse，
// 这样 set 路径上就没有“怎么写进去”的入口。
func TestManagedKeysHaveNoParse(t *testing.T) {
	for _, key := range []string{"repo_paths", "parent_paths"} {
		s, ok := Lookup(key)
		if !ok {
			t.Fatalf("%q 应在注册表里（get 需要能读它）", key)
		}
		if s.ManagedBy == "" {
			t.Errorf("%q 应标记 ManagedBy", key)
		}
		if s.Parse != nil {
			t.Errorf("%q 不应提供 Parse，否则 set 会绕过 ggt repo 的校验", key)
		}
	}
}

// TestSettingMetadataIsComplete 断言注册表里的元数据自洽。
//
// 为什么必须有这条测试：候选清单、整数边界这些元数据都不是编译期能检查的东西，漏填或填错时
// 构建与其余测试全部通过，问题要到用户打开设置面板时才暴露——表现为“页面上能选，提交后却被
// 拒收”“页面允许填 0，解析器只收正整数”这类自相矛盾。这里遍历全部注册项一次守住，
// 加新配置项时不需要再补测试用例
func TestSettingMetadataIsComplete(t *testing.T) {
	for _, s := range Settings() {
		// Parse 与 ManagedBy 恰有其一：两者都缺的项写不进去也不说明该由谁管；
		// 两者都有的项意味着 ManagedBy 那道“不能在这里改”的限制可以被 set 绕过
		if s.Parse == nil && s.ManagedBy == "" {
			t.Errorf("%q 既没有 Parse 也没有 ManagedBy：它无法被写入，也没说明该由谁管", s.Key)
		}
		if s.Parse != nil && s.ManagedBy != "" {
			t.Errorf("%q 同时有 Parse 与 ManagedBy：set 会绕过 %s 的校验", s.Key, s.ManagedBy)
		}

		// 默认值必须能被自己的 Parse 接受，否则 reset --defaults 写出来的文件当场非法
		if s.Parse != nil {
			if _, err := s.Parse(ValueText(s.Default)); err != nil {
				t.Errorf("%q 的默认值 %#v 通不过自己的 Parse：%v", s.Key, s.Default, err)
			}
		}

		// 整数边界只对整数项有意义
		if (s.Min != nil || s.Max != nil) && s.Kind != KindInt {
			t.Errorf("%q 声明了整数边界，但 Kind 是 %s", s.Key, s.Kind)
		}
		if s.Min != nil && s.Max != nil && *s.Min > *s.Max {
			t.Errorf("%q 的下界 %d 大于上界 %d", s.Key, *s.Min, *s.Max)
		}
		if n, ok := s.Default.(int); ok {
			if s.Min != nil && n < *s.Min {
				t.Errorf("%q 的默认值 %d 小于下界 %d", s.Key, n, *s.Min)
			}
			if s.Max != nil && n > *s.Max {
				t.Errorf("%q 的默认值 %d 大于上界 %d", s.Key, n, *s.Max)
			}
		}

		// AllowCustom 只在有候选时才有意义：没有候选就无所谓“候选之外的写法”
		if s.AllowCustom && s.Options == nil {
			t.Errorf("%q 声明了 AllowCustom 却没有候选清单", s.Key)
		}

		// RejectBlank 只对文本入口有意义：受管项没有文本入口（Parse 为 nil），标了也不起作用；
		// 而默认值本身就是空白时，reset --defaults 会写入一个 set 自己拒收的值，自相矛盾
		if s.RejectBlank {
			if s.ManagedBy != "" {
				t.Errorf("%q 由 %s 管理，声明 RejectBlank 没有意义", s.Key, s.ManagedBy)
			}
			if strings.TrimSpace(ValueText(s.Default)) == "" {
				t.Errorf("%q 声明了 RejectBlank，但它的默认值是空白：reset --defaults 会写出一个自己拒收的值", s.Key)
			}
		}

		if s.Options == nil {
			continue
		}
		opts := s.Options()
		if len(opts) == 0 {
			t.Errorf("%q 的候选清单是空的：要么给出取值，要么别声明 Options（那表示自由输入）", s.Key)
		}
		seen := make(map[string]bool, len(opts))
		for _, o := range opts {
			if seen[o.Value] {
				t.Errorf("%q 的候选里 %q 出现了两次", s.Key, o.Value)
			}
			seen[o.Value] = true

			// 候选必须能通过自己的 Parse：这正是“页面能选、命令行却拒收”那道对不上的拦路测试
			if s.Parse == nil {
				continue
			}
			if _, err := s.Parse(o.Value); err != nil {
				t.Errorf("%q 的候选值 %q 通不过自己的 Parse：%v（页面能选，写入却会被拒）", s.Key, o.Value, err)
			}
		}
	}
}

// TestLanguageOptionsCoverSupported 断言语言项的候选取值就是随二进制发布的语言，且都带显示名。
//
// 它守的不是注册表自洽，而是注册表与 locales 包之间的一致性：语言清单若在两处各写一份，
// 会出现“l10n 认得一种语言、页面里却选不到”这种没有任何编译错误、也没人会发现的错位
func TestLanguageOptionsCoverSupported(t *testing.T) {
	s, ok := Lookup("language")
	if !ok {
		t.Fatal("注册表里没有 language")
	}
	if s.Options == nil {
		t.Fatal("language 应有候选清单")
	}

	labels := map[string]string{}
	for _, o := range s.Options() {
		labels[o.Value] = o.Label
	}
	supported := locales.Supported()
	if len(labels) != len(supported) {
		t.Errorf("语言候选有 %d 项，Supported() 有 %d 项：两处清单已经分叉", len(labels), len(supported))
	}
	for _, tag := range supported {
		label, ok := labels[tag]
		if !ok {
			t.Errorf("语言 %q 在 Supported() 里却不在候选中", tag)
			continue
		}
		// 显示名等于标签本身说明 DisplayName 的名称表漏了它、退回了标签
		if label == "" || label == tag {
			t.Errorf("语言 %q 缺显示名（实得 %q）：locales 的名称表漏了这一项", tag, label)
		}
	}
}

// TestThemeOptionsEmptyValueRule 断言主题三项的候选与“能不能清空”这条规则一致。
//
// theme 的空串是“跟随系统”，是有意义的取值；两个偏好的空串则意味着没配色可渲染。
// 网页设置面板据此判断该项允不允许清空（候选里有没有空值项），因此这条规则必须锁住——
// 它一旦松动，写入校验会放空串进来，页面渲染时整页没有颜色
func TestThemeOptionsEmptyValueRule(t *testing.T) {
	hasEmptyValue := func(opts []Option) bool {
		for _, o := range opts {
			if o.Value == "" {
				return true
			}
		}
		return false
	}

	themeSetting, ok := Lookup("theme")
	if !ok || themeSetting.Options == nil {
		t.Fatal("theme 应有候选清单")
	}
	themeOpts := themeSetting.Options()
	if !hasEmptyValue(themeOpts) {
		t.Error("theme 的候选里应有一个空值项，它代表跟随系统")
	}
	// 内置主题是 go:embed 进来的，任何时候都该存在。只剩一个空值项说明主题包枚举失败
	// 或调用方传错了目录，而那种退化光看“有没有空值项”是发现不了的
	if len(themeOpts) < 2 {
		t.Errorf("theme 的候选只有 %d 项：内置主题应当总是在列，说明主题枚举已经失效", len(themeOpts))
	}

	for _, key := range []string{"theme_dark", "theme_light"} {
		s, ok := Lookup(key)
		if !ok || s.Options == nil {
			t.Fatalf("%s 应有候选清单", key)
		}
		if hasEmptyValue(s.Options()) {
			t.Errorf("%s 的候选里不该有空值项：该偏好为空时页面没有配色可渲染", key)
		}
	}
}

// TestConcurrentKeyWritesKeepEveryKey 断言并发写不同的键时不会互相覆盖。
//
// 这是 writeMu 的行为级测试。SetKeyAt 是“读整份文件、改一个键、写回”，没有那把锁时两个
// 并发调用会各自读到旧内容再各写一份，后写的那次把先写的键整个抹掉。断言锁存在（例如
// 检查某个字段）只能测出“看起来加了锁”，而丢更新这件事只有真的并发写一次才看得见
func TestConcurrentKeyWritesKeepEveryKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")

	// 挑类型各不相同的几个键：字符串、布尔、整数都覆盖到
	writes := map[string]any{
		"concurrency":        "8",
		"log_level":          "debug",
		"size_unit":          "binary",
		"language":           "zh-CN",
		"ignore_submodules":  true,
		"theme":              "",
		"size_bucket_low_mb": 300,
	}

	var wg sync.WaitGroup
	for key, value := range writes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := SetKeyAt(path, key, value); err != nil {
				t.Errorf("写 %s 失败：%v", key, err)
			}
		}()
	}
	wg.Wait()

	raw, err := ReadRawAt(path)
	if err != nil {
		t.Fatalf("回读失败：%v", err)
	}
	for key, want := range writes {
		got, ok := raw[key]
		if !ok {
			t.Errorf("键 %s 被并发写入抹掉了（缺了它说明发生了丢更新）", key)
			continue
		}
		// 比文本形态而不是比原始值：JSON 回读后整数是 json.Number，直接比会因类型不同而误报
		if ValueText(got) != ValueText(want) {
			t.Errorf("%s 的值是 %q，应为 %q", key, ValueText(got), ValueText(want))
		}
	}
}

// TestConcurrencyOptionsMatchPanel 断言设置面板能直接点选的档位就是约定的五个。
//
// 页面只按注册表渲染候选，服务端不写死任何一项；候选一旦变动（例如又冒出 CPUHalf），
// 这里会立刻失败，而不是等到用户打开面板看见一个不存在的取值。
func TestConcurrencyOptionsMatchPanel(t *testing.T) {
	s, ok := Lookup("concurrency")
	if !ok || s.Options == nil {
		t.Fatalf("concurrency 应有候选清单")
	}

	values := make([]string, 0, 5)
	for _, o := range s.Options() {
		values = append(values, o.Value)
	}
	if got := strings.Join(values, ","); got != "auto,4,8,16,32" {
		t.Errorf("候选档位 = %q，期望 auto,4,8,16,32", got)
	}
	// 默认值必须就在候选里，页面才能把它显示成选中项
	if s.Default != "auto" || values[0] != s.Default {
		t.Errorf("默认值 %v 应是候选里的第一项 auto", s.Default)
	}
}

// TestValidateAtDeprecatedConcurrencyIsWarning 断言旧语义值只报警告。
//
// 运行期会把这些值当 auto 继续跑（并提示改写），若体检报成 error，升级后第一次
// validate 就会失败，与“命令其实能跑”的事实相矛盾。真正非法的值（0、65、乱码）
// 仍必须是 error，那条由 TestValidateAtReportsProblems 与 TestSettingParse 守着。
func TestValidateAtDeprecatedConcurrencyIsWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	writeFile(t, path, `{"concurrency": "CPUFull"}`)

	issues, err := ValidateAt(path)
	if err != nil {
		t.Fatalf("体检失败: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("应只有 1 条问题，实得 %+v", issues)
	}
	if issues[0].Level != LevelWarning {
		t.Errorf("旧语义值应报 warning，实得 %s：%s", issues[0].Level, issues[0].Message)
	}
	// 文案随当前语言变化，只断言值本身出现在提示里（两种语言的译文都保留了占位符）
	if !strings.Contains(issues[0].Message, "CPUFull") {
		t.Errorf("提示应点名实际写入的值，实得 %q", issues[0].Message)
	}
}

// TestEffectiveAtNormalizesConcurrency 断言 config get 的输出与运行期一致：
// 合法取值归一成规范形态，运行期会失败的值直接返回错误（get 据此非 0 退出）。
func TestEffectiveAtNormalizesConcurrency(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
		wantErr bool
	}{
		{"带空白的数字归一", `{"concurrency": " 8 "}`, "8", false},
		{"JSON 数字归一", `{"concurrency": 8}`, "8", false},
		{"JSON 浮点被拒", `{"concurrency": 8.5}`, "", true},
		{"JSON 布尔被拒", `{"concurrency": true}`, "", true},
		{"大写 auto 归一", `{"concurrency": "AUTO"}`, "auto", false},
		{"空串按 auto", `{"concurrency": ""}`, "auto", false},
		{"旧语义值按 auto", `{"concurrency": "CPUHalf"}`, "auto", false},
		{"上限本身合法", `{"concurrency": "64"}`, "64", false},
		{"越界报错", `{"concurrency": "65"}`, "", true},
		{"零报错", `{"concurrency": "0"}`, "", true},
		{"负数报错", `{"concurrency": "-3"}`, "", true},
		{"乱码报错", `{"concurrency": "abc"}`, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "c.json")
			writeFile(t, path, c.content)

			got, err := EffectiveAt(path, "concurrency")
			if (err != nil) != c.wantErr {
				t.Fatalf("EffectiveAt 错误 = %v，wantErr=%v", err, c.wantErr)
			}
			if c.wantErr {
				return
			}
			if got != c.want {
				t.Errorf("EffectiveAt = %v，应为 %q", got, c.want)
			}
		})
	}
}

// TestValueShapeRejectedConsistently 断言“布尔值写在字符串项上”在四条路径上都被拒绝。
//
// 改造前这四处是分叉的：运行期靠 mapstructure 的弱转换把 true 变成 "1" 照常跑，
// config get 却因为无法把 true 解析成并发数而报错退出。现在四处都拒绝；报错措辞按
// 输入形态分为两类——JSON 布尔是类型错误，命令行文本 "true" 是取值错误，两类都点名
// 出问题的键与实际值/类型，不会让用户以为是两件不同的事。
func TestValueShapeRejectedConsistently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	writeFile(t, path, `{"concurrency": true}`)

	_, loadErr := LoadConfigAt(path)
	if loadErr == nil {
		t.Fatal("运行期加载应拒绝 concurrency: true")
	}
	if !strings.Contains(loadErr.Error(), "concurrency") || !strings.Contains(loadErr.Error(), "bool") {
		t.Errorf("运行期报错应点名键与 JSON 类型，实得 %q", loadErr)
	}

	_, getErr := EffectiveAt(path, "concurrency")
	if getErr == nil {
		t.Fatal("config get 应拒绝 concurrency: true")
	}
	if getErr.Error() != loadErr.Error() {
		t.Errorf("config get 与运行期的报错应相同：\n  get: %q\n load: %q", getErr, loadErr)
	}

	issues, err := ValidateAt(path)
	if err != nil {
		t.Fatalf("体检失败: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("concurrency: true 应只报 1 条问题，实得 %+v", issues)
	}
	if issues[0].Level != LevelError {
		t.Fatalf("布尔形态应报 error（运行期直接失败），实得 %s", issues[0].Level)
	}
	if issues[0].Message != loadErr.Error() {
		t.Errorf("体检与运行期的报错应相同：\n  validate: %q\n      load: %q", issues[0].Message, loadErr)
	}

	// config set 收到的是文本 "true"，不是 JSON 布尔：这是字符串项里一个非法的取值，
	// 不是类型错误。结论必须同样是拒绝，措辞点明允许范围
	_, setErr := SetFromTextAt(path, "concurrency", "true", false)
	if setErr == nil {
		t.Fatal("config set concurrency true 应被拒绝")
	}
	for _, part := range []string{"concurrency", "true", "1 to 64"} {
		if !strings.Contains(setErr.Error(), part) {
			t.Errorf("config set 的报错应包含 %q，实得 %q", part, setErr)
		}
	}
}

// TestEffectiveAtRejectsWrongShape 断言 get 对形态不对的值一律报错，而不是原样打印，
// 且报错说法与体检同源；合法的数字与字符串形态不受影响。
func TestEffectiveAtRejectsWrongShape(t *testing.T) {
	cases := []struct {
		name    string
		content string
		key     string
		wantErr bool
		want    string
	}{
		{"并发数写成布尔", `{"concurrency": true}`, "concurrency", true, ""},
		{"并发数写成 false", `{"concurrency": false}`, "concurrency", true, ""},
		{"阈值写成布尔", `{"size_bucket_low_mb": true}`, "size_bucket_low_mb", true, ""},
		{"语言写成布尔", `{"language": true}`, "language", true, ""},
		{"路径列表写成布尔", `{"repo_paths": true}`, "repo_paths", true, ""},
		{"路径列表写 null 按未设置处理", `{"repo_paths": null}`, "repo_paths", false, "[]"},
		{"布尔项本身合法", `{"ignore_submodules": true}`, "ignore_submodules", false, "true"},
		{"布尔项的字符串形态仍合法", `{"push_tags": "false"}`, "push_tags", false, "false"},
		{"并发数的数字形态合法", `{"concurrency": 8}`, "concurrency", false, "8"},
		{"并发数写成浮点被拒", `{"concurrency": 1.5}`, "concurrency", true, ""},
		{"字符串形态仍合法", `{"concurrency": "8"}`, "concurrency", false, "8"},
		{"整数的字符串形态仍合法", `{"size_bucket_low_mb": "500"}`, "size_bucket_low_mb", false, "500"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "c.json")
			writeFile(t, path, c.content)

			got, err := EffectiveAt(path, c.key)
			if (err != nil) != c.wantErr {
				t.Fatalf("EffectiveAt 错误 = %v，wantErr=%v", err, c.wantErr)
			}
			if c.wantErr {
				// 形态问题不能漏出 mapstructure 的原文，得是可以直接给用户看的一句
				if strings.Contains(err.Error(), "unconvertible") {
					t.Errorf("报错不应漏出 mapstructure 原文：%q", err)
				}
				return
			}
			if ValueText(got) != c.want {
				t.Errorf("EffectiveAt = %v，应为 %q", got, c.want)
			}
		})
	}
}

// TestSetFromTextAtKeepsBoolKindsWritable 断言拒绝布尔形态没有波及布尔项本身：
// 用文本 true/false 写布尔项仍然可行，别的键也不受影响。
func TestSetFromTextAtKeepsBoolKindsWritable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")

	if _, err := SetFromTextAt(path, "ignore_submodules", "true", false); err != nil {
		t.Fatalf("写布尔项应成功: %v", err)
	}
	if _, err := SetFromTextAt(path, "concurrency", "4", false); err != nil {
		t.Fatalf("写数字形态的并发数应成功: %v", err)
	}

	cfg, err := LoadConfigAt(path)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if !cfg.IgnoreSubmodules {
		t.Error("ignore_submodules 应为 true")
	}
	if cfg.Concurrency != "4" {
		t.Errorf("concurrency = %q，应为 \"4\"", cfg.Concurrency)
	}
}

// TestValueShapePathMessage 断言路径项的形态错误沿用“应为数组”的说法。
//
// repo_paths / parent_paths 的 Setting.Expected 是空的（数组的合法取值没法用一句话概括），
// 若套用标量那句模板，报错会渲染成 “(expected )”，看着像拼接漏了东西。
func TestValueShapePathMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	writeFile(t, path, `{"repo_paths": true}`)

	_, err := EffectiveAt(path, "repo_paths")
	if err == nil {
		t.Fatal("repo_paths 写成布尔值应报错")
	}
	got := err.Error()
	for _, part := range []string{"repo_paths", "bool"} {
		if !strings.Contains(got, part) {
			t.Errorf("报错应点明键名与实际类型（%q）：%q", part, got)
		}
	}
	if strings.Contains(got, "(expected )") || strings.HasSuffix(got, "expected ") {
		t.Errorf("不得渲染出空的 expected：%q", got)
	}
}
