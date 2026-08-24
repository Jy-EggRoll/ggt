// root_test 对 cmd 包中统一输出封装层的纯函数逻辑进行单元测试，
// 覆盖分隔线、仓库名着色、次要文本着色的字符串构造，不依赖终端。
package cmd

import (
	"strings"
	"testing"
)

// TestBuildSeparator 验证分隔线由 width 个 "─" 组成（ANSI 转义不影响可见字符数）。
func TestBuildSeparator(t *testing.T) {
	cases := []int{0, 1, 10, 80}
	for _, w := range cases {
		got := buildSeparator(w)
		if n := strings.Count(got, "─"); n != w {
			t.Errorf("buildSeparator(%d) 含 %d 个 '─', 期望 %d", w, n, w)
		}
	}
}

// TestRepoName 验证仓库名被青色包裹为 "[name]" 形式且包含原名。
func TestRepoName(t *testing.T) {
	got := RepoName("my-repo")
	if !strings.Contains(got, "[my-repo]") {
		t.Errorf("RepoName 应含 [my-repo], 实际 %q", got)
	}
}

// TestMuted 验证次要文本被包裹且原文本可见。
func TestMuted(t *testing.T) {
	got := Muted("变动详情：")
	if !strings.Contains(got, "变动详情：") {
		t.Errorf("Muted 应保留原文本, 实际 %q", got)
	}
}
