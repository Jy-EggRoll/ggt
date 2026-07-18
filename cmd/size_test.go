// size_test 对 cmd 包中 size 命令的纯函数逻辑进行单元测试，
// 覆盖 git count-objects 输出解析、单位换算与人类可读格式化。
package cmd

import "testing"

// TestParseSizeValue 验证不同单位字符串能正确换算为字节数。
func TestParseSizeValue(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"100 bytes", 100},
		{"1 KiB", 1024},
		{"1 MiB", 1024 * 1024},
		{"2 MiB", 2 * 1024 * 1024},
		{"1 GiB", 1024 * 1024 * 1024},
		{"  3 KiB  ", 3 * 1024},
	}
	for _, c := range cases {
		if got := parseSizeValue(c.in); got != c.want {
			t.Errorf("parseSizeValue(%q) = %d, 期望 %d", c.in, got, c.want)
		}
	}
}

// TestParseSizeOutput 验证键值对解析正确，空值被忽略。
func TestParseSizeOutput(t *testing.T) {
	raw := "size: 80.85 MiB\nsize-pack: 65.98 MiB\ncount: 221\n\n  garbage: 0\n"
	got := parseSizeOutput(raw)
	if got["size"] != "80.85 MiB" {
		t.Errorf("size 解析错误: %q", got["size"])
	}
	if got["size-pack"] != "65.98 MiB" {
		t.Errorf("size-pack 解析错误: %q", got["size-pack"])
	}
	if got["count"] != "221" {
		t.Errorf("count 解析错误: %q", got["count"])
	}
	if _, ok := got["garbage"]; !ok {
		t.Error("garbage 字段应被解析")
	}
}

// TestCalcTotalBytes 验证 size 与 size-pack 字节数相加。
func TestCalcTotalBytes(t *testing.T) {
	info := map[string]string{
		"size":      "1 MiB",
		"size-pack": "2 MiB",
		"count":     "10",
	}
	// 1 MiB + 2 MiB = 3 * 1024 * 1024
	want := int64(3 * 1024 * 1024)
	if got := calcTotalBytes(info); got != want {
		t.Errorf("calcTotalBytes = %d, 期望 %d", got, want)
	}

	// 缺字段时按 0 处理
	partial := map[string]string{"size": "1 KiB"}
	if got := calcTotalBytes(partial); got != 1024 {
		t.Errorf("缺 size-pack 时应只算 size, 实际 %d", got)
	}
}

// TestFormatSize 验证人类可读格式化输出。
func TestFormatSize(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
	}
	for _, c := range cases {
		if got := formatSize(c.in); got != c.want {
			t.Errorf("formatSize(%d) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}
