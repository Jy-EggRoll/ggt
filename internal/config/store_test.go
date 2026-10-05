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
	if err := SetKeyAt(path, "concurrency", "CPUFull"); err != nil {
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
	if err := SetKeyAt(path, "CONCURRENCY", "CPUFull"); err != nil {
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

	if err := SetKeyAt(path, "concurrency", "CPUFull"); err == nil {
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
	writeFile(t, path, `{"my_custom_key": "keep me", "concurrency": "CPUHalf"}`)

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
	if raw["concurrency"] != "CPUHalf" || raw["size_unit"] != "binary" {
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

	if err := SetKeyAt(path, "concurrency", "CPUFull"); err != nil {
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

	if err := SetKeyAt(path, "concurrency", "CPUFull"); err != nil {
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
	  "ignore_submodules": "true"
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
// Windows 记事本"另存为 UTF-8"默认会写 BOM，而报错信息完全看不出是它引起的。
func TestValidateAtDetectsBOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	content := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"concurrency": "CPUFull"}`)...)
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
		{"concurrency", "CPUFull", false},
		{"concurrency", "cpuhalf", false},
		{"concurrency", "8", false},
		{"concurrency", "0", true},
		{"concurrency", "-3", true},
		{"concurrency", "abc", true},
		{"concurrency", "2000000000", true}, // 超上限：会一路传到 make(chan, N)
		{"size_bucket_low_mb", "100", false},
		{"size_bucket_low_mb", "0", true},
		{"size_bucket_low_mb", "100000000", true}, // 超上限：32 位平台会解码失败
		{"size_bucket_high_mb", "800", false},
		{"size_unit", "binary", false},
		{"size_unit", "BINARY", false},
		{"size_unit", "hex", true},
		{"ignore_submodules", "true", false},
		{"ignore_submodules", "TRUE", false},
		{"ignore_submodules", "1", false},
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

// TestSettingParseStoresCanonicalForm 断言语义串与语言存的是规范形态，
// 避免文件里出现 cpuhalf/CPUHALF、zh/zh-Hans 这类同义异形写法。
func TestSettingParseStoresCanonicalForm(t *testing.T) {
	cases := []struct {
		key, value, want string
	}{
		{"concurrency", "cpuhalf", "CPUHalf"},
		{"concurrency", "cpuquarter", "CPUQuarter"},
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
// 构建与其余测试全部通过，问题要到用户打开设置面板时才暴露——表现为"页面上能选，提交后却被
// 拒收""页面允许填 0，解析器只收正整数"这类自相矛盾。这里遍历全部注册项一次守住，
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

			// 候选必须能通过自己的 Parse：这正是“页面能选、命令行却拒收”那道漂移的拦路测试
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
// 会出现"l10n 认得一种语言、页面里却选不到"这种没有任何编译错误、也没人会发现的错位
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
		"concurrency":        "CPUFull",
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
