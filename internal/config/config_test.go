package config

import (
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
