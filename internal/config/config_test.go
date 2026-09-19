package config

import (
	"os"
	"path/filepath"
	"runtime"
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
