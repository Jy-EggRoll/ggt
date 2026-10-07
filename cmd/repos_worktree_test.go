package cmd

import (
	"path/filepath"
	"testing"

	"github.com/jy-eggroll/ggt/internal/git"
)

// worktreeState 值得测的分支都在“git 报的状态”与“目录是否存在”不一致的地方，
// 所以必须用真实目录，不能只造一个 Worktree 结构体。
func TestWorktreeState(t *testing.T) {
	existing := t.TempDir()
	missing := filepath.Join(t.TempDir(), "gone")

	tests := []struct {
		name string
		wt   git.Worktree
		want string
	}{
		{
			name: "正常：目录在、没加锁",
			wt:   git.Worktree{Path: existing},
			want: "",
		},
		{
			name: "locked：目录在、被锁住",
			wt:   git.Worktree{Path: existing, Locked: true},
			want: worktreeStateLocked,
		},
		{
			name: "prunable：git 已判定目录不在",
			wt:   git.Worktree{Path: existing, Prunable: true},
			want: worktreeStateMissing,
		},
		{
			// 这是整个判定里最要紧的一条：实测加锁之后删掉目录，git 只报 locked、
			// 不报 prunable，只能靠目录是否存在来判断
			name: "locked 且目录已删：git 只报 locked",
			wt:   git.Worktree{Path: missing, Locked: true},
			want: worktreeStateMissing,
		},
		{
			name: "目录已删，但 git 什么都没说",
			wt:   git.Worktree{Path: missing},
			want: worktreeStateMissing,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := worktreeState(tt.wt); got != tt.want {
				t.Errorf("worktreeState() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestWorktreeEntries 覆盖两条规则：跳过宿主自己，以及展示名怎么取。
func TestWorktreeEntries(t *testing.T) {
	host := "/repo/main"
	wts := []git.Worktree{
		{Path: "/repo/main", Branch: "main"},                                // 宿主自己，必须跳过
		{Path: "/repo/wt-normal", Branch: "wt-normal"},                      // 分支上：用分支名
		{Path: "/repo/wt-detached", Head: "9c513203f426e1", Detached: true}, // 游离 HEAD：退回短 SHA
	}
	got := worktreeEntries(host, wts)
	if len(got) != 2 {
		t.Fatalf("应得到 2 棵工作树（跳过宿主自己），实际 %d：%+v", len(got), got)
	}
	if got[0].WorktreeOf != host || got[0].Name != "wt-normal" || got[0].WorktreeDetached {
		t.Errorf("分支上的工作树字段不对：%+v", got[0])
	}
	if got[1].Name != "9c51320" || !got[1].WorktreeDetached {
		t.Errorf("游离 HEAD 的工作树应退回短 SHA 并标记 detached：%+v", got[1])
	}
}
