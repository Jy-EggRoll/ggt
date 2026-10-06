package git

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// 下面的输入取自 git 2.34.1 的真实输出（/tmp/ggt-worktree-fixture/out/），
// 不是按文档想象的格式：字段顺序、原因串里的空格与冒号都保持原样。
// 这样解析层一旦被人“顺手优化”成按引号或按最后一个字段切分，测试会立刻失败。
func TestParseWorktreeList(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []Worktree
	}{
		{
			name: "普通工作树：主工作区排在首位",
			input: "worktree /repo/main\n" +
				"HEAD 0a793d5479287b01d3e0a3424631ab5956cad2bd\n" +
				"branch refs/heads/main\n" +
				"\n" +
				"worktree /repo/wt-normal\n" +
				"HEAD 0a793d5479287b01d3e0a3424631ab5956cad2bd\n" +
				"branch refs/heads/wt-normal\n",
			want: []Worktree{
				{Path: "/repo/main", Head: "0a793d5479287b01d3e0a3424631ab5956cad2bd", Branch: "main"},
				{Path: "/repo/wt-normal", Head: "0a793d5479287b01d3e0a3424631ab5956cad2bd", Branch: "wt-normal"},
			},
		},
		{
			name: "detached：没有 branch 行",
			input: "worktree /repo/wt-detached\n" +
				"HEAD 9c513203f426e19e65b41f6bba5a59b605a14b6b\n" +
				"detached\n",
			want: []Worktree{
				{Path: "/repo/wt-detached", Head: "9c513203f426e19e65b41f6bba5a59b605a14b6b", Detached: true},
			},
		},
		{
			name: "locked：原因含空格与冒号，且不加引号",
			input: "worktree /repo/wt-locked-gone\n" +
				"HEAD 0a793d5479287b01d3e0a3424631ab5956cad2bd\n" +
				"branch refs/heads/wt-locked-gone\n" +
				"locked in use by fixture: do not prune\n",
			want: []Worktree{
				{
					Path:       "/repo/wt-locked-gone",
					Head:       "0a793d5479287b01d3e0a3424631ab5956cad2bd",
					Branch:     "wt-locked-gone",
					Locked:     true,
					LockReason: "in use by fixture: do not prune",
				},
			},
		},
		{
			name: "prunable：目录已不在",
			input: "worktree /repo/wt-prunable\n" +
				"HEAD 0a793d5479287b01d3e0a3424631ab5956cad2bd\n" +
				"branch refs/heads/wt-prunable\n" +
				"prunable gitdir file points to non-existent location\n",
			want: []Worktree{
				{
					Path:        "/repo/wt-prunable",
					Head:        "0a793d5479287b01d3e0a3424631ab5956cad2bd",
					Branch:      "wt-prunable",
					Prunable:    true,
					PruneReason: "gitdir file points to non-existent location",
				},
			},
		},
		{
			name: "bare：只有两行，没有 HEAD 也没有 branch",
			input: "worktree /repo/bare.git\n" +
				"bare\n",
			want: []Worktree{
				{Path: "/repo/bare.git", Bare: true},
			},
		},
		{
			name:  "空输出：这个仓库没有工作树",
			input: "",
			want:  nil,
		},
		{
			name: "末尾没有空行也要收尾",
			input: "worktree /repo/only\n" +
				"HEAD abc1234\n" +
				"branch refs/heads/only",
			want: []Worktree{
				{Path: "/repo/only", Head: "abc1234", Branch: "only"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseWorktreeList(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseWorktreeList() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// HasWorktrees 只看 .git/worktrees 是否存在，两种情况都要覆盖：
// 真正的仓库目录（存在 .git 目录）与 .git 是文件的情况。
func TestHasWorktrees(t *testing.T) {
	repoWith := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoWith, ".git", "worktrees"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !HasWorktrees(repoWith) {
		t.Error("有 .git/worktrees 目录时应返回 true")
	}

	repoWithout := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoWithout, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if HasWorktrees(repoWithout) {
		t.Error("没有 .git/worktrees 目录时应返回 false")
	}

	// .git 是文件（子模块、以及工作树自身都是这种形态）：Stat 会失败，应当返回 false
	repoGitFile := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoGitFile, ".git"), []byte("gitdir: /somewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if HasWorktrees(repoGitFile) {
		t.Error(".git 是文件时应返回 false")
	}
}
