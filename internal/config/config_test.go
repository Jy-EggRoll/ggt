package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jy-eggroll/eggokit/logger"
)

// TestAutoConcurrencyFor 用固定核数验证 auto 的两条边界：至少为 1、封顶 8。
//
// 刻意不拿 runtime.NumCPU() 当期望值：那样只是把实现抄一遍，换一台核数不同的机器
// 就得到不同答案，也测不出“封顶”这条规则。官方信源：https://pkg.go.dev/runtime#NumCPU
func TestAutoConcurrencyFor(t *testing.T) {
	cases := []struct {
		ncpu int
		want int
	}{
		{0, 1},    // 极端值兜底，防止 max(1, ...) 被删掉
		{1, 1},    // 单核的一半是 0，必须抬到 1
		{2, 1},    // 2/2=1
		{4, 2},    // 4/2=2
		{15, 7},   // 15/2=7，尚未封顶
		{16, 8},   // 16/2=8，正好到顶
		{17, 8},   // 从 17 核起 17/2=8，仍然等于上限
		{32, 8},   // 32/2=16，被压到 8
		{1024, 8}, // 核数再多也停在 8
	}
	for _, c := range cases {
		if got := autoConcurrencyFor(c.ncpu); got != c.want {
			t.Errorf("autoConcurrencyFor(%d) = %d, want %d", c.ncpu, got, c.want)
		}
	}
}

// TestConcurrencyValue 验证 auto、显式数字与空串的解析结果，以及越界一律报错。
//
// 空串与 auto 必须落到同一处：缺键、空串、零值 Config 都会走到这里，
// 若空串另有一套默认值，auto 的含义就有了第二份实现。
func TestConcurrencyValue(t *testing.T) {
	// auto 一族只断言落在 [1, 8] 内，不拿本机核数算出一个具体值当期望：
	// 后者等于把实现抄进测试，换台机器就得到不同答案。具体边界由 TestAutoConcurrencyFor 固定
	for _, raw := range []string{"", "auto", "AUTO", " Auto "} {
		cfg := &Config{Concurrency: raw}
		got, err := cfg.ConcurrencyValue()
		if err != nil {
			t.Errorf("ConcurrencyValue(%q) 返回错误: %v", raw, err)
			continue
		}
		if got < 1 || got > autoConcurrencyCeiling {
			t.Errorf("ConcurrencyValue(%q) = %d，超出 [1, %d]", raw, got, autoConcurrencyCeiling)
		}
	}

	// 显式数字原样生效，上限本身合法
	valid := []struct {
		raw  string
		want int
	}{
		{"1", 1},
		{"8", 8},
		{"  8  ", 8},
		{"64", maxConcurrency},
	}
	for _, c := range valid {
		cfg := &Config{Concurrency: c.raw}
		got, err := cfg.ConcurrencyValue()
		if err != nil {
			t.Errorf("ConcurrencyValue(%q) 返回错误: %v", c.raw, err)
			continue
		}
		if got != c.want {
			t.Errorf("ConcurrencyValue(%q) = %d, want %d", c.raw, got, c.want)
		}
	}

	// 越界与旧语义值都不得静默回退：并发数是本机保护参数，写错了必须让命令失败
	for _, raw := range []string{"0", "-3", "65", "1024", "2000000000", "abc", "CPUHalf", "CPUFull", "CPUQuarter"} {
		cfg := &Config{Concurrency: raw}
		if _, err := cfg.ConcurrencyValue(); err == nil {
			t.Errorf("ConcurrencyValue(%q) 应当报错，实际通过", raw)
		}
	}
}

// TestValidateCommandLineConcurrency 验证 -c 专用的入口：空串与纯空白必须报错，
// 合法取值与配置文件同一套口径。
//
// 空串按 auto 是配置文件的性质（缺键、空值都是文件的正常形态），命令行不能沿用：
// 脚本里 -c "$VAR" 而 VAR 未设值时，静默变成 auto 就是一次静默忽略，
// 还能让一份写着非法值的配置文件借 -c "" 绕过启动校验。
func TestValidateCommandLineConcurrency(t *testing.T) {
	for _, raw := range []string{"", "   ", "\t", " \n "} {
		_, err := ValidateCommandLineConcurrency(raw)
		if err == nil {
			t.Errorf("ValidateCommandLineConcurrency(%q) 应报错", raw)
			continue
		}
		// 报错必须点出允许范围，且不能出现空值导致的“value : expected”缺口
		if msg := err.Error(); !strings.Contains(msg, "64") || !strings.Contains(msg, "(empty)") {
			t.Errorf("空值的报错应写明范围并以 (empty) 指代空值，实际 %q", msg)
		}
	}

	if n, err := ValidateCommandLineConcurrency("auto"); err != nil || n < 1 || n > autoConcurrencyCeiling {
		t.Errorf("auto 应解析成 [1, %d] 内的值，实得 (%d, %v)", autoConcurrencyCeiling, n, err)
	}
	if n, err := ValidateCommandLineConcurrency(" 8 "); err != nil || n != 8 {
		t.Errorf("\" 8 \" 应解析成 8，实得 (%d, %v)", n, err)
	}
	for _, raw := range []string{"0", "-1", "65", "abc", "CPUFull"} {
		if _, err := ValidateCommandLineConcurrency(raw); err == nil {
			t.Errorf("ValidateCommandLineConcurrency(%q) 应报错", raw)
		}
	}
}

// TestConcurrencyValueErrorNamesValueAndRange 断言报错信息同时写明实际值与允许范围。
// 只说“取值非法”时，用户分不清是 -c 写错了，还是配置文件里那份旧值没改。
func TestConcurrencyValueErrorNamesValueAndRange(t *testing.T) {
	_, err := (&Config{Concurrency: "128"}).ConcurrencyValue()
	if err == nil {
		t.Fatal("越界值应报错")
	}
	if msg := err.Error(); !strings.Contains(msg, "128") || !strings.Contains(msg, "64") {
		t.Errorf("报错信息应写明实际值 128 与上限 64，实际 %q", msg)
	}
}

// TestDefaultConfigConcurrency 验证默认配置存的是 auto，且它能解析成具体数字。
// 断言解析结果落在 [1, 8] 而不是等于某个固定值：默认值本来就跟着本机核数走。
func TestDefaultConfigConcurrency(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Concurrency != DefaultConcurrency {
		t.Errorf("默认 concurrency 应为 %q，实际 %q", DefaultConcurrency, cfg.Concurrency)
	}
	got, err := cfg.ConcurrencyValue()
	if err != nil {
		t.Fatalf("默认值解析失败: %v", err)
	}
	if got < 1 || got > autoConcurrencyCeiling {
		t.Errorf("默认并发数 %d 超出 [1, %d]", got, autoConcurrencyCeiling)
	}
}

// TestDeprecatedConcurrencyFallsBackToAuto 验证旧语义值不是被换算成某个数字，
// 而是按 auto 处理——换算会把“跟着本机核数走”变成“跟着写下这行那天的那台机器走”。
// 同时断言迁移提示每个进程只出现一次：看板会周期性重读配置，每次重读都提示一遍会刷屏。
func TestDeprecatedConcurrencyFallsBackToAuto(t *testing.T) {
	var buf bytes.Buffer
	logger.Init(&logger.Config{Level: slog.LevelWarn, Writer: &buf})
	t.Cleanup(func() { logger.Init(logger.DefaultConfig()) })
	// 别的用例可能已经触发过同一条提示，这里显式复位，断言才不依赖测试执行顺序
	deprecatedConcurrencyWarned.Store(false)

	path := filepath.Join(t.TempDir(), "c.json")
	writeFile(t, path, `{"concurrency": "CPUFull"}`)

	for i := 0; i < 2; i++ {
		cfg, err := LoadConfigAt(path)
		if err != nil {
			t.Fatalf("第 %d 次读取失败: %v", i+1, err)
		}
		if cfg.Concurrency != DefaultConcurrency {
			t.Errorf("旧语义值应回落成 %q，实际 %q", DefaultConcurrency, cfg.Concurrency)
		}
	}
	if n := strings.Count(buf.String(), "CPUFull"); n != 1 {
		t.Errorf("迁移提示应只出现一次，实际 %d 次：%s", n, buf.String())
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
// 这是“默认值只有一处真相”的守门测试：配置结构体的默认值必须与 settings 注册表
// 登记的完全一致，否则 ggt config get 与 ggt config show 会对同一个键各说各话。
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
		  "concurrency": "8",
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
		if cfg.Concurrency != "8" || !cfg.IgnoreSubmodules {
			t.Errorf("标量键不正确: %#v", cfg)
		}
		if cfg.SizeBucketLowMB != 200 || cfg.SizeBucketHighMB != 600 || cfg.SizeUnit != "binary" {
			t.Errorf("尺寸键不正确: %#v", cfg)
		}
	})

	t.Run("数字型并发数归一成字符串", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "c.json")
		// concurrency 的语义是 auto 或 1 到 64 的整数，JSON 数字是最自然的写法；
		// 加载后统一存成字符串形态，运行期与 config get 看到的是同一个规范值
		writeFile(t, path, `{"concurrency": 8}`)

		cfg, err := LoadConfigAt(path)
		if err != nil {
			t.Fatalf("读取失败: %v", err)
		}
		if cfg.Concurrency != "8" {
			t.Errorf("concurrency = %q，应为 \"8\"", cfg.Concurrency)
		}
		got, err := cfg.ConcurrencyValue()
		if err != nil || got != 8 {
			t.Errorf("ConcurrencyValue() = (%d, %v)，应为 (8, nil)", got, err)
		}
	})

	t.Run("非法值在加载期直接报错", func(t *testing.T) {
		cases := []struct {
			name    string
			content string
		}{
			{"阈值为零", `{"size_bucket_low_mb": 0}`},
			{"阈值为负", `{"size_bucket_high_mb": -1}`},
			{"非法口径", `{"size_unit": "bogus"}`},
			{"空语言", `{"language": ""}`},
			{"并发数前导零", `{"concurrency": "010"}`},
			{"并发数正号", `{"concurrency": "+8"}`},
			{"空串仍按 auto", `{"concurrency": ""}`},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "c.json")
				writeFile(t, path, c.content)

				cfg, err := LoadConfigAt(path)
				if c.name == "空串仍按 auto" {
					if err != nil {
						t.Fatalf("空并发数是文件的正常形态，应按 auto 读取: %v", err)
					}
					if cfg.Concurrency != DefaultConcurrency {
						t.Errorf("concurrency = %q，应为 %q", cfg.Concurrency, DefaultConcurrency)
					}
					return
				}
				if err == nil {
					t.Fatalf("非法值应让加载失败，实际得到 %#v", cfg)
				}
			})
		}
	})

	t.Run("合法取值归一后加载", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "c.json")
		writeFile(t, path, `{"size_unit": "BINARY", "ignore_submodules": " TRUE "}`)

		cfg, err := LoadConfigAt(path)
		if err != nil {
			t.Fatalf("读取失败: %v", err)
		}
		if cfg.SizeUnit != "binary" {
			t.Errorf("size_unit 应归一成 binary，实得 %q", cfg.SizeUnit)
		}
		if !cfg.IgnoreSubmodules {
			t.Error("ignore_submodules 应接受带空白的 TRUE")
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
		{"空串表示不指定字体", "", "", true},
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

// TestPushTagsAndPruneBranchesDefault 覆盖两个默认值为 true 的开关。
//
// 它们与其余配置项的区别在于 bool 的零值是 false：只要有一条读路径退回“零值结构体 +
// 补默认值”，缺键的配置文件上就会得到 false，而设置面板仍显示 true——
// 表现为“明明开着，推送却不带标签”。因此三条读路径（空配置、结构体默认值、面板视图）
// 必须一起断言
func TestPushTagsAndPruneBranchesDefault(t *testing.T) {
	t.Run("空配置时两项为 true", func(t *testing.T) {
		cfg, err := LoadConfigAt(filepath.Join(t.TempDir(), "absent.json"))
		if err != nil {
			t.Fatalf("文件不存在不应报错: %v", err)
		}
		if !cfg.PushTags || !cfg.PruneBranches {
			t.Fatalf("空配置应两项都为 true，实得 push_tags=%v prune_branches=%v", cfg.PushTags, cfg.PruneBranches)
		}
	})

	t.Run("结构体默认值与空配置一致", func(t *testing.T) {
		cfg := defaultConfig()
		if !cfg.PushTags || !cfg.PruneBranches {
			t.Fatalf("defaultConfig 应两项都为 true，实得 push_tags=%v prune_branches=%v", cfg.PushTags, cfg.PruneBranches)
		}
	})

	t.Run("文件里显式写 false 时保留 false", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "c.json")
		writeFile(t, path, `{"push_tags": false, "prune_branches": false}`)

		cfg, err := LoadConfigAt(path)
		if err != nil {
			t.Fatalf("读取失败: %v", err)
		}
		if cfg.PushTags || cfg.PruneBranches {
			t.Fatalf("显式 false 不应被默认值覆盖，实得 push_tags=%v prune_branches=%v", cfg.PushTags, cfg.PruneBranches)
		}
	})

	t.Run("设置面板视图与默认值一致", func(t *testing.T) {
		// 视图是设置面板的唯一数据来源，它若与 LoadConfigAt 给出不同答案，
		// 用户看到的就是“面板上开着、实际没生效”
		dir := t.TempDir()
		path := filepath.Join(dir, "absent.json")
		for _, v := range SettingsViewAt(path) {
			if v.Key != "push_tags" && v.Key != "prune_branches" {
				continue
			}
			if v.Value != "true" || v.Default != "true" {
				t.Errorf("%s 的视图值应为 true，实得 value=%q default=%q", v.Key, v.Value, v.Default)
			}
		}
	})
}

// TestLoadConfigAtRejectsBoolShape 断言运行期拒绝“布尔值用在非布尔配置项上”，
// 同时不碰合法的数字与字符串形态。
//
// 这条守卫的由来：{"concurrency": true} 曾被弱类型解码成 "1" 并照常运行，而
// config get 解析不了 true、直接报错退出——同一份配置在两处结论相反。
// 布尔值不是任何非布尔配置项的合法形态，因此这里一律拒绝。
func TestLoadConfigAtRejectsBoolShape(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr bool
		check   func(*testing.T, *Config)
	}{
		{
			name:    "并发数写成布尔",
			content: `{"concurrency": true}`,
			wantErr: true,
		},
		{
			name:    "阈值写成布尔",
			content: `{"size_bucket_low_mb": true}`,
			wantErr: true,
		},
		{
			name:    "语言写成布尔",
			content: `{"language": true}`,
			wantErr: true,
		},
		{
			name:    "路径列表写成布尔",
			content: `{"repo_paths": true}`,
			wantErr: true,
		},
		{
			name:    "路径列表写 null 按未设置处理",
			content: `{"repo_paths": null}`,
			wantErr: false,
			check: func(t *testing.T, cfg *Config) {
				if len(cfg.RepoPaths) != 0 {
					t.Errorf("repo_paths 应为空列表，实得 %#v", cfg.RepoPaths)
				}
			},
		},
		{
			name:    "匿名键写成布尔不算错误",
			content: `{"typo_key": true}`,
			wantErr: false,
			check: func(t *testing.T, cfg *Config) {
				if cfg.Concurrency != DefaultConcurrency {
					t.Errorf("未知键不应影响其它项，concurrency = %q", cfg.Concurrency)
				}
			},
		},
		{
			name:    "布尔项本身的布尔值合法",
			content: `{"ignore_submodules": true, "prune_branches": false}`,
			wantErr: false,
			check: func(t *testing.T, cfg *Config) {
				if !cfg.IgnoreSubmodules || cfg.PruneBranches {
					t.Errorf("布尔项读取不正确: %#v", cfg)
				}
			},
		},
		{
			name:    "并发数写成数字归一成字符串",
			content: `{"concurrency": 8}`,
			wantErr: false,
			check: func(t *testing.T, cfg *Config) {
				if cfg.Concurrency != "8" {
					t.Errorf("concurrency = %q，应为 \"8\"", cfg.Concurrency)
				}
			},
		},
		{
			name:    "并发数写成浮点被拒绝",
			content: `{"concurrency": 8.5}`,
			wantErr: true,
		},
		{
			name:    "整数项的数字形态合法",
			content: `{"size_bucket_low_mb": 200}`,
			wantErr: false,
			check: func(t *testing.T, cfg *Config) {
				if cfg.SizeBucketLowMB != 200 {
					t.Errorf("整数项读取不正确: low=%d", cfg.SizeBucketLowMB)
				}
			},
		},
		{
			name:    "字符串形态仍然合法",
			content: `{"concurrency": "8", "size_bucket_high_mb": "600"}`,
			wantErr: false,
			check: func(t *testing.T, cfg *Config) {
				if cfg.Concurrency != "8" || cfg.SizeBucketHighMB != 600 {
					t.Errorf("字符串形态读取不正确: concurrency=%q high=%d", cfg.Concurrency, cfg.SizeBucketHighMB)
				}
			},
		},
		{
			name:    "布尔项收到数字被拒绝",
			content: `{"ignore_submodules": 1}`,
			wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "c.json")
			writeFile(t, path, c.content)

			cfg, err := LoadConfigAt(path)
			if (err != nil) != c.wantErr {
				t.Fatalf("LoadConfigAt 错误 = %v，wantErr=%v", err, c.wantErr)
			}
			if c.wantErr {
				// 类型错误与取值错误各有措辞，但都必须点名出问题的键，
				// 而不是漏出 mapstructure 的 "unconvertible type" 原文
				if !strings.Contains(err.Error(), "Expected") && !strings.Contains(err.Error(), "Invalid value for") {
					t.Errorf("报错应指出是哪个键的取值/形态问题，实得 %q", err)
				}
				return
			}
			if c.check != nil {
				c.check(t, cfg)
			}
		})
	}
}
