package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
// 放着不管的话，viper 读时会用随机迭代序挑选，导致每次运行结果不同。
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

// TestSaveConfigKeepsUnknownKeysAndWritesKnownOnes 断言整份保存既覆盖已知键、又保留未知键。
func TestSaveConfigKeepsUnknownKeysAndWritesKnownOnes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	writeFile(t, path, `{"my_custom_key": "keep me"}`)

	cfg := defaultConfig()
	cfg.Concurrency = "CPUFull"
	cfg.RepoPaths = []string{"/tmp/x"}
	if err := SaveConfigAt(path, cfg); err != nil {
		t.Fatalf("保存失败: %v", err)
	}

	raw, err := ReadRawAt(path)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if raw["my_custom_key"] != "keep me" {
		t.Errorf("未知键被丢掉了: %v", raw)
	}
	if raw["concurrency"] != "CPUFull" {
		t.Errorf("concurrency = %v", raw["concurrency"])
	}
	// nil 切片必须写成 []，否则 386 等平台读回来是空切片、语义上等价但文件不好看
	if _, ok := raw["parent_paths"].([]any); !ok {
		t.Errorf("parent_paths 应为数组，实得 %T", raw["parent_paths"])
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
// 这样 set 路径上就没有"怎么写进去"的入口。
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
