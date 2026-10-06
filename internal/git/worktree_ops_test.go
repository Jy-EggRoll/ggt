package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initRepo 在一个临时目录里建真实的 git 仓库。
//
// 本层的操作都是“调用 git 并看它怎么说”，用假仓库或打桩测不出真实行为：
// check-ref-format 拒绝哪些分支名、同一分支能不能建第二棵工作树、
// 有未跟踪文件时删除会不会被拒，这些答案只有 git 自己知道。
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v 失败：%v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	// 工作树要从某个提交上建出来，所以仓库里必须至少有一个提交
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "init")
	return dir
}

func TestDefaultWorktreePath(t *testing.T) {
	tests := []struct {
		repo   string
		branch string
		want   string
	}{
		{"/home/me/repo", "feat/x", "/home/me/repo.worktrees/feat/x"},
		{"/home/me/repo", "main", "/home/me/repo.worktrees/main"},
		// 结尾多一个斜杠不该改变结果
		{"/home/me/repo/", "fix", "/home/me/repo.worktrees/fix"},
	}
	for _, tt := range tests {
		if got := DefaultWorktreePath(tt.repo, tt.branch); got != tt.want {
			t.Errorf("DefaultWorktreePath(%q, %q) = %q, 期望 %q", tt.repo, tt.branch, got, tt.want)
		}
	}
}

func TestValidateBranchName(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()

	for _, ok := range []string{"main", "feat/x", "fix-1", "release/v1.2"} {
		if err := ValidateBranchName(ctx, repo, ok); err != nil {
			t.Errorf("%q 应当被接受，实际被拒：%v", ok, err)
		}
	}

	// 每一条都是 git 自己会拒绝的形态，其中 a..b 与 .hidden 尤其重要：
	// 它们会被拼进目标路径，放过去就等于允许写到仓库外面
	for _, bad := range []string{"", "   ", "a..b", ".hidden", "a b", "a~b", "a:b", "x.lock", "a/"} {
		if err := ValidateBranchName(ctx, repo, bad); err == nil {
			t.Errorf("%q 应当被拒绝，实际通过", bad)
		}
	}
}

func TestAddAndRemoveWorktree(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()
	path := DefaultWorktreePath(repo, "feat/one")

	if _, err := AddWorktree(ctx, repo, path, "feat/one", true); err != nil {
		t.Fatalf("新建工作树失败：%v", err)
	}

	// 新建的那棵必须出现在发现结果里，否则“建好了却看不到”就是断的
	wts, err := ListWorktrees(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, wt := range wts {
		if filepath.Clean(wt.Path) == filepath.Clean(path) && wt.Branch == "feat/one" {
			found = true
		}
	}
	if !found {
		t.Errorf("新建的工作树没有出现在列表里：%+v", wts)
	}

	if _, err := RemoveWorktree(ctx, repo, path, false); err != nil {
		t.Fatalf("删除工作树失败：%v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("删除之后目录仍然存在（err=%v）", err)
	}
}

// 同一分支不能同时挂在两棵工作树上，这是 git 的约束，也是界面上必须提前挡住的一种情况。
func TestAddWorktreeRejectsDuplicateBranch(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()
	first := DefaultWorktreePath(repo, "dup")

	if _, err := AddWorktree(ctx, repo, first, "dup", true); err != nil {
		t.Fatalf("第一棵应当能建起来：%v", err)
	}
	out, err := AddWorktree(ctx, repo, first+"-2", "dup", true)
	if err == nil {
		t.Fatalf("同一分支建第二棵应当被拒绝，实际通过了：%s", out)
	}
}

// 工作树里还有未跟踪内容时，删除必须被拒；只有明确加 force 才能删掉。
// 这条行为是“不会悄悄弄丢 Agent 没提交的改动”的依据。
func TestRemoveWorktreeRefusesDirtyWithoutForce(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()
	path := DefaultWorktreePath(repo, "dirty")

	if _, err := AddWorktree(ctx, repo, path, "dirty", true); err != nil {
		t.Fatalf("新建工作树失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "uncommitted.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := RemoveWorktree(ctx, repo, path, false); err == nil {
		t.Error("有未跟踪文件时删除应当被拒绝，实际通过了")
	}
	if _, err := RemoveWorktree(ctx, repo, path, true); err != nil {
		t.Errorf("加 force 之后应当能删除，实际失败：%v", err)
	}
}

// add 失败时不能留下孤儿分支。
//
// 目标目录被占住是最常见的失败原因，而 git 建出的分支不会自己收回去，
// 于是留下一条没挂在任何工作树上的分支，用户改用新建分支重试同名时
// 会撞上一句 "A branch named 'x' already exists"。
func TestAddWorktreeRollsBackBranchOnFailure(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()
	path := DefaultWorktreePath(repo, "orphan")

	// 用一个非空目录占住目标位置，让 add 必然失败
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "occupied.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := AddWorktree(ctx, repo, path, "orphan", true); err == nil {
		t.Fatal("目标目录被占住时 add 应当失败")
	}
	if branchExists(ctx, repo, "orphan") {
		t.Error("add 失败后留下了孤儿分支，用户重试同名会撞上 already exists")
	}
}
