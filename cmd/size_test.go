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
		// 十进制无 i 单位容错（git 未来可能改用 KB/MB/GB）
		{"1 KB", 1000},
		{"1 MB", 1000 * 1000},
		{"2 MB", 2 * 1000 * 1000},
		{"1 GB", 1000 * 1000 * 1000},
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

// TestBytesToMB 验证十进制与二进制两种口径的字节→MB 换算。
func TestBytesToMB(t *testing.T) {
	cases := []struct {
		bytes int64
		unit  string
		want  float64
	}{
		{1_000_000, "decimal", 1.0},
		{500_000_000, "decimal", 500.0},
		{1024 * 1024, "binary", 1.0},
		{500 * 1024 * 1024, "binary", 500.0},
		// 非 binary 一律按 decimal 处理
		{1_000_000, "whatever", 1.0},
	}
	for _, c := range cases {
		if got := bytesToMB(c.bytes, c.unit); got != c.want {
			t.Errorf("bytesToMB(%d, %q) = %v, 期望 %v", c.bytes, c.unit, got, c.want)
		}
	}
}

// TestClassifyBySize 验证分桶边界与失败仓库排除。
func TestClassifyBySize(t *testing.T) {
	results := []repoSizeResult{
		{name: "tiny", isSubmodule: false, size: 100_000_000, ok: true},       // 100 MB < 500 → small
		{name: "exact-low", isSubmodule: false, size: 500_000_000, ok: true},  // 500 MB → mid
		{name: "mid", isSubmodule: false, size: 600_000_000, ok: true},        // 600 MB → mid
		{name: "exact-high", isSubmodule: false, size: 800_000_000, ok: true}, // 800 MB → mid
		{name: "huge", isSubmodule: false, size: 900_000_000, ok: true},       // 900 MB → large
		{name: "failed", isSubmodule: false, size: 0, ok: false},              // 不应入任何桶
	}

	small, mid, large := classifyBySize(results, 500, 800, "decimal")

	wantSmall := []string{RepoLabel("tiny", false)}
	wantMid := []string{RepoLabel("exact-low", false), RepoLabel("mid", false), RepoLabel("exact-high", false)}
	wantLarge := []string{RepoLabel("huge", false)}

	assertNames := func(got, want []string) {
		if len(got) != len(want) {
			t.Errorf("分桶数量不符: 实际 %v, 期望 %v", got, want)
			return
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("分桶顺序/内容不符: 实际 %v, 期望 %v", got, want)
			}
		}
	}
	assertNames(small, wantSmall)
	assertNames(mid, wantMid)
	assertNames(large, wantLarge)
}
