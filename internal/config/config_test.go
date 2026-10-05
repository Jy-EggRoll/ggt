package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// TestConcurrencyValue 验证语义串解析为实际并发数的各种分支：
// CPUHalf / CPUFull / CPUQuarter / 正整数串 / 空串 / 非法串回退。
// 官方信源：https://pkg.go.dev/runtime#NumCPU 与 https://pkg.go.dev/builtin#max
func TestConcurrencyValue(t *testing.T) {
	ncpu := runtime.NumCPU()
	half := max(1, ncpu/2)
	quarter := max(1, ncpu/4)

	cases := []struct {
		raw  string
		want int
	}{
		{"", half},
		{"CPUHalf", half},
		{"cpuhalf", half}, // 大小写不敏感
		{"CPUFull", ncpu},
		{"CPUQuarter", quarter},
		{"8", 8},
		{"  8  ", 8},  // 容忍空白
		{"abc", half}, // 非法串回退到 CPUHalf
		{"0", half},   // 非正整数回退
		{"-3", half},
	}

	for _, c := range cases {
		cfg := &Config{Concurrency: c.raw}
		if got := cfg.ConcurrencyValue(); got != c.want {
			t.Errorf("ConcurrencyValue(%q) = %d, want %d", c.raw, got, c.want)
		}
	}
}

// TestDefaultConfigConcurrency 验证默认配置文件（未设置 concurrency）时，
// 落盘/内存中的值是语义串 "CPUHalf" 而非具体数字。
func TestDefaultConfigConcurrency(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Concurrency != DefaultConcurrency {
		t.Errorf("默认 concurrency 应为 %q，实际 %q", DefaultConcurrency, cfg.Concurrency)
	}
}

// TestDefaultIgnoreSubmodules 验证 ignore_submodules 默认为 false（即默认包含子模块）。
func TestDefaultIgnoreSubmodules(t *testing.T) {
	cfg := defaultConfig()
	if cfg.IgnoreSubmodules {
		t.Errorf("ignore_submodules 默认应为 false（默认包含子模块），实际 true")
	}
}

// TestDefaultLanguage 验证 language 默认值为 "en"（英文是默认语言，中文为兼容层）。
func TestDefaultLanguage(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Language != "en" {
		t.Errorf("默认 language 应为 %q，实际 %q", "en", cfg.Language)
	}
}

// TestLoadLanguage 验证只读 language 字段的解析行为。
//
// 该函数被 --help 路径依赖（语言必须早于 cobra 解析确定），因此有一条硬要求：
// 任何异常情况都必须返回 error 而不是终止进程——调用方据此回退默认语言，
// 保证帮助永远能打印出来。
func TestLoadLanguage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// 配置文件不存在：返回 error，由调用方回退默认语言
	if _, err := LoadLanguage(); err == nil {
		t.Error("配置文件不存在时应返回 error")
	}

	cfgDir := filepath.Join(home, ".config", "go-git-ggt")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatalf("创建测试配置目录失败: %v", err)
	}
	path := filepath.Join(cfgDir, "ggt-config.json")

	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("写入测试配置失败: %v", err)
		}
	}

	write(`{"language": "zh-CN", "concurrency": "8"}`)
	if got, err := LoadLanguage(); err != nil || got != "zh-CN" {
		t.Errorf(`LoadLanguage() = (%q, %v), want ("zh-CN", nil)`, got, err)
	}

	// 未设置 language 字段时返回空串与 nil，是否回退由调用方决定
	write(`{"concurrency": "8"}`)
	if got, err := LoadLanguage(); err != nil || got != "" {
		t.Errorf("未设置 language 时应返回空串与 nil，实际 (%q, %v)", got, err)
	}

	// 格式非法时必须返回 error，而不是 panic 或终止进程
	write(`{not json`)
	if _, err := LoadLanguage(); err == nil {
		t.Error("配置文件格式非法时应返回 error")
	}
}

// assertDefaults 断言一份配置处于“全默认”状态。
// 用 DeepEqual 与 defaultConfig() 对比，而不是逐字段列举：后者每加一个字段都要改一遍，
// 漏改时这条测试反而会给出“通过”的假信号。
func assertDefaults(t *testing.T, cfg *Config) {
	t.Helper()
	want := defaultConfig()
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("应为全默认配置:\n got %#v\nwant %#v", cfg, want)
	}
}

// TestDefaultConfigMatchesSettings 断言结构体形态的默认值与 settings 注册表登记的
// 默认值完全一致。
//
// 这是“默认值只有一处真相”的守门测试：applyConfigDefaults 一度把 500/800/"decimal"/
// "en" 又抄了一遍，两处一旦分叉，ggt config get（走 settings）与 ggt config show
// （走 applyConfigDefaults）就会各说各话，而当时没有任何测试能发现。
func TestDefaultConfigMatchesSettings(t *testing.T) {
	cfg := defaultConfig()
	got := map[string]any{
		"concurrency":         cfg.Concurrency,
		"ignore_submodules":   cfg.IgnoreSubmodules,
		"size_bucket_low_mb":  cfg.SizeBucketLowMB,
		"size_bucket_high_mb": cfg.SizeBucketHighMB,
		"size_unit":           cfg.SizeUnit,
		"language":            cfg.Language,
	}
	for key, v := range got {
		s, ok := Lookup(key)
		if !ok {
			t.Errorf("settings 里没有登记 %s", key)
			continue
		}
		if v != s.Default {
			t.Errorf("%s 的默认值不一致：结构体形态为 %#v，settings 登记的是 %#v", key, v, s.Default)
		}
	}

	// 路径列表：settings 登记的是空切片，结构体形态也必须是空切片而非 nil。
	// 否则 config show 打印 null、reset --defaults 却写 []，同一状态两种呈现。
	if cfg.RepoPaths == nil || cfg.ParentPaths == nil {
		t.Errorf("路径列表默认应为空切片而非 nil：repo=%#v parent=%#v", cfg.RepoPaths, cfg.ParentPaths)
	}
	for _, key := range []string{"repo_paths", "parent_paths"} {
		s, _ := Lookup(key)
		want, ok := s.Default.([]string)
		if !ok || len(want) != 0 {
			t.Errorf("%s 在 settings 里应登记为空切片，实得 %#v", key, s.Default)
		}
	}
}

// TestLoadConfigAt 覆盖配置文件读取的各类形态。
//
// 这些用例能存在本身就是本次改造的目的：原 LoadConfig 写死了默认路径、依赖 viper
// 包级单例，测试只能碰真实 HOME，函数级覆盖率始终为 0。
func TestLoadConfigAt(t *testing.T) {
	t.Run("文件不存在时全部取默认值", func(t *testing.T) {
		cfg, err := LoadConfigAt(filepath.Join(t.TempDir(), "absent.json"))
		if err != nil {
			t.Fatalf("文件不存在不应报错: %v", err)
		}
		assertDefaults(t, cfg)
	})

	t.Run("只设置部分键时其余保持默认", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "c.json")
		writeFile(t, path, `{"language": "zh-CN"}`)

		cfg, err := LoadConfigAt(path)
		if err != nil {
			t.Fatalf("读取失败: %v", err)
		}
		if cfg.Language != "zh-CN" {
			t.Errorf("language = %q, want zh-CN", cfg.Language)
		}
		if cfg.Concurrency != DefaultConcurrency || cfg.SizeUnit != "decimal" || cfg.SizeBucketLowMB != 500 {
			t.Errorf("未设置的键应保持默认，实得 %#v", cfg)
		}
	})

	t.Run("全键覆盖", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "c.json")
		writeFile(t, path, `{
		  "repo_paths": ["/tmp/a"],
		  "parent_paths": ["/tmp/b"],
		  "concurrency": "CPUFull",
		  "ignore_submodules": true,
		  "size_bucket_low_mb": 200,
		  "size_bucket_high_mb": 600,
		  "size_unit": "binary",
		  "language": "en"
		}`)

		cfg, err := LoadConfigAt(path)
		if err != nil {
			t.Fatalf("读取失败: %v", err)
		}
		if len(cfg.RepoPaths) != 1 || cfg.RepoPaths[0] != "/tmp/a" {
			t.Errorf("repo_paths = %#v", cfg.RepoPaths)
		}
		if len(cfg.ParentPaths) != 1 || cfg.ParentPaths[0] != "/tmp/b" {
			t.Errorf("parent_paths = %#v", cfg.ParentPaths)
		}
		if cfg.Concurrency != "CPUFull" || !cfg.IgnoreSubmodules {
			t.Errorf("标量键不正确: %#v", cfg)
		}
		if cfg.SizeBucketLowMB != 200 || cfg.SizeBucketHighMB != 600 || cfg.SizeUnit != "binary" {
			t.Errorf("尺寸键不正确: %#v", cfg)
		}
	})

	t.Run("兼容旧版数字型并发数", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "c.json")
		// 旧版把并发数存成 int，读取时必须能转成字符串形式的 "8" 而不是报错
		writeFile(t, path, `{"concurrency": 8}`)

		cfg, err := LoadConfigAt(path)
		if err != nil {
			t.Fatalf("读取失败: %v", err)
		}
		if cfg.Concurrency != "8" {
			t.Errorf("concurrency = %q, want \"8\"", cfg.Concurrency)
		}
		if got := cfg.ConcurrencyValue(); got != 8 {
			t.Errorf("ConcurrencyValue() = %d, want 8", got)
		}
	})

	t.Run("非法值按字段各有分工", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "c.json")
		writeFile(t, path, `{
		  "concurrency": "",
		  "size_bucket_low_mb": 0,
		  "size_bucket_high_mb": -1,
		  "size_unit": "bogus",
		  "language": ""
		}`)

		cfg, err := LoadConfigAt(path)
		if err != nil {
			t.Fatalf("读取失败: %v", err)
		}
		if cfg.Concurrency != DefaultConcurrency {
			t.Errorf("空并发数应回落默认，实得 %q", cfg.Concurrency)
		}
		if cfg.SizeBucketLowMB != 500 || cfg.SizeBucketHighMB != 800 {
			t.Errorf("非正阈值应回落默认，实得 %d/%d", cfg.SizeBucketLowMB, cfg.SizeBucketHighMB)
		}
		if cfg.Language != "en" {
			t.Errorf("空语言应回落默认，实得 %q", cfg.Language)
		}
		// size_unit 刻意**不**在加载期替换：非法取值由 ggt config validate 报出、
		// 由 size 命令在使用端回退到 decimal 并打印警告。若在加载期就悄悄改成 decimal，
		// 那句警告永远不会触发，用户的笔误也就永远看不见
		if cfg.SizeUnit != "bogus" {
			t.Errorf("size_unit 应原样保留、交给 validate 与使用端处理，实得 %q", cfg.SizeUnit)
		}
	})

	t.Run("JSON 损坏时报错", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "c.json")
		writeFile(t, path, `{not json`)

		if _, err := LoadConfigAt(path); err == nil {
			t.Error("损坏的配置文件应返回 error")
		}
	})

	t.Run("仅大小写不同的重复键被拒绝", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "c.json")
		writeFile(t, path, `{"Language": "en", "language": "zh-CN"}`)

		if _, err := LoadConfigAt(path); err == nil {
			t.Error("大小写重复键应返回 error，而不是随机选一个")
		}
	})
}

// 写入测试配置内容一律复用 store_test.go 的 writeFile，本文件不再重复定义。

// TestValidFontFamily 验证字体栈取值的字符集、长度与引号配对判定。
//
// 这一项的值会被原样拼进页面的 <style> 里（见 cmd/ui_theme.go 的 fontBlock），因此分号、
// 花括号、反斜杠、尖括号以及落单的引号都必须挡住：前几个能就地起一条新声明或闭合整个
// :root 块，落单的引号则会把后面的字体名一起吞进同一个字符串，页面上表现为字体忽然全变了
func TestValidFontFamily(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"空串表示用内置字体栈", "", "", true},
		{"普通字体栈", "Inter, system-ui, sans-serif", "Inter, system-ui, sans-serif", true},
		{"带引号与中文名", `"Noto Sans CJK SC", 思源黑体, monospace`, `"Noto Sans CJK SC", 思源黑体, monospace`, true},
		{"首尾空白被去掉", "  Inter  ", "Inter", true},
		{"分号能就地起一条新声明", "Foo;} body{display:none", "", false},
		{"花括号能闭合 :root", "Foo} :root{--bg:red", "", false},
		{"反斜杠是转义口子", `Foo\26 bar`, "", false},
		{"尖括号是标签口子", "Foo<script>", "", false},
		{"单引号落单", "Foo'", "", false},
		{"双引号落单", `"Foo`, "", false},
		{"超长", strings.Repeat("a", fontFamilyMaxLen+1), "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ValidFontFamily(c.in)
			if ok != c.ok {
				t.Fatalf("合法判定应为 %v，实得 %v（规整值 %q）", c.ok, ok, got)
			}
			if got != c.want {
				t.Errorf("规整值应为 %q，实得 %q", c.want, got)
			}
			// 注册表的解析器必须与它给出一致的结论：写入侧与渲染侧各判一套时，
			// 表现为“能写进配置文件、渲染时却被丢掉”，而那种矛盾没有任何东西能提前发现
			v, err := parseFontFamily(c.in)
			if c.ok != (err == nil) {
				t.Fatalf("parseFontFamily 的结论与 ValidFontFamily 不一致：err=%v", err)
			}
			if c.ok && v != c.want {
				t.Errorf("parseFontFamily 应返回 %q，实得 %v", c.want, v)
			}
		})
	}
}
