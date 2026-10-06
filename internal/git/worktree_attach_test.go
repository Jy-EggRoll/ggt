package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// prepareAttachRepo 在 initRepo 的临时仓库上补齐接入场景需要的东西：
// 一份 .gitignore，一个被忽略的目录（依赖目录的替身）与一个被忽略的文件，
// 以及一个“从主工作区指出去的符号链接”。
//
// 用真实仓库而不是造假目录树：被忽略与否由 git 自己判定，只有真仓库才能验到这一点。
func prepareAttachRepo(t *testing.T) (repo string, wt string) {
	t.Helper()
	repo = initRepo(t)

	ignore := "node_modules/\n.env\nlinked-out\n"
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(ignore), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "node_modules", "dep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "node_modules", "dep", "index.js"), []byte("dep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("TOKEN=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 源本身是符号链接：它指向仓库外面，链过去或复制过去都会把仓库外的内容带进来
	if err := os.Symlink(os.TempDir(), filepath.Join(repo, "linked-out")); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-qm", "ignore rules")

	// 用新建分支：main 正被主工作区检出，git 不允许同一个分支同时挂在两棵工作树上
	wt = filepath.Join(t.TempDir(), "wt")
	if _, err := AddWorktree(context.Background(), repo, wt, "wt-attach-test", true); err != nil {
		t.Fatalf("建工作树失败：%v", err)
	}
	return repo, wt
}

// TestAttachIgnoredPathsLinksDirectory 验证链接模式：被忽略的目录以符号链接接过去。
func TestAttachIgnoredPathsLinksDirectory(t *testing.T) {
	repo, wt := prepareAttachRepo(t)
	outcomes, err := AttachIgnoredPaths(context.Background(), repo, wt, []string{"node_modules"}, AttachLink)
	if err != nil {
		t.Fatalf("接入失败：%v", err)
	}
	if len(outcomes) != 1 || outcomes[0].Err != nil {
		t.Fatalf("期望一条成功结果，得到 %+v", outcomes)
	}
	target := filepath.Join(wt, "node_modules")
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("目标不存在：%v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("期望符号链接，实际是普通目录或文件")
	}
	// 链接必须真的通到源目录的内容上，否则省下的只是一次“看起来成功了”
	got, err := os.ReadFile(filepath.Join(target, "dep", "index.js"))
	if err != nil {
		t.Fatalf("读链接目标失败：%v", err)
	}
	if string(got) != "dep\n" {
		t.Fatalf("链接内容不对：%q", got)
	}
}

// TestAttachIgnoredPathsCopiesFile 验证复制模式：被忽略的文件成为独立的真文件。
func TestAttachIgnoredPathsCopiesFile(t *testing.T) {
	repo, wt := prepareAttachRepo(t)
	outcomes, err := AttachIgnoredPaths(context.Background(), repo, wt, []string{".env"}, AttachCopy)
	if err != nil {
		t.Fatalf("接入失败：%v", err)
	}
	if len(outcomes) != 1 || outcomes[0].Err != nil {
		t.Fatalf("期望一条成功结果，得到 %+v", outcomes)
	}
	target := filepath.Join(wt, ".env")
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("目标不存在：%v", err)
	}
	// 必须是真文件：链接过去会让新工作树里的改动直接改写主工作区那份
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("期望真文件，实际是符号链接")
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("权限没有跟源文件对齐：%v", info.Mode().Perm())
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "TOKEN=1\n" {
		t.Fatalf("复制内容不对：%q", got)
	}
}

// TestAttachIgnoredPathsEdgeCases 一次覆盖几处边界。
func TestAttachIgnoredPathsEdgeCases(t *testing.T) {
	t.Run("已存在则不动它", func(t *testing.T) {
		repo, wt := prepareAttachRepo(t)
		if err := os.MkdirAll(filepath.Join(wt, "node_modules"), 0o755); err != nil {
			t.Fatal(err)
		}
		outcomes, err := AttachIgnoredPaths(context.Background(), repo, wt, []string{"node_modules"}, AttachLink)
		if err != nil {
			t.Fatal(err)
		}
		if len(outcomes) != 1 || !errors.Is(outcomes[0].Err, ErrAttachExists) {
			t.Fatalf("期望 ErrAttachExists，得到 %+v", outcomes)
		}
		info, err := os.Lstat(filepath.Join(wt, "node_modules"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			t.Fatal("已存在的目标被改成了链接")
		}
	})

	t.Run("源是符号链接则跳过", func(t *testing.T) {
		repo, wt := prepareAttachRepo(t)
		outcomes, err := AttachIgnoredPaths(context.Background(), repo, wt, []string{"linked-out"}, AttachCopy)
		if err != nil {
			t.Fatal(err)
		}
		if len(outcomes) != 1 || !errors.Is(outcomes[0].Err, ErrAttachSourceSymlink) {
			t.Fatalf("期望 ErrAttachSourceSymlink，得到 %+v", outcomes)
		}
		if _, err := os.Lstat(filepath.Join(wt, "linked-out")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("跳过的路径不该在工作树里留下任何东西")
		}
	})

	t.Run("类型不匹配则跳过", func(t *testing.T) {
		repo, wt := prepareAttachRepo(t)
		// .env 是文件，链接模式只处理目录
		outcomes, err := AttachIgnoredPaths(context.Background(), repo, wt, []string{".env"}, AttachLink)
		if err != nil {
			t.Fatal(err)
		}
		if len(outcomes) != 1 || !errors.Is(outcomes[0].Err, ErrAttachSourceKind) {
			t.Fatalf("期望 ErrAttachSourceKind，得到 %+v", outcomes)
		}
	})

	t.Run("没被忽略的路径不会被处理", func(t *testing.T) {
		repo, wt := prepareAttachRepo(t)
		// a.txt 由 initRepo 跟踪，不是被忽略的路径
		outcomes, err := AttachIgnoredPaths(context.Background(), repo, wt, []string{"a.txt"}, AttachCopy)
		if err != nil {
			t.Fatal(err)
		}
		if len(outcomes) != 0 {
			t.Fatalf("被跟踪的文件不该被接过去，得到 %+v", outcomes)
		}
	})

	t.Run("路径穿越被拒", func(t *testing.T) {
		repo, wt := prepareAttachRepo(t)
		for _, pattern := range []string{"../outside", "/etc/passwd", "a/../../b"} {
			_, err := AttachIgnoredPaths(context.Background(), repo, wt, []string{pattern}, AttachCopy)
			if !errors.Is(err, ErrAttachInvalidPattern) {
				t.Fatalf("%q 期望 ErrAttachInvalidPattern，得到 %v", pattern, err)
			}
		}
	})
}

// TestAttachSummary 验证汇总口径：成功与失败的分界只有一处定义。
func TestAttachSummary(t *testing.T) {
	done, problems := AttachSummary([]AttachOutcome{
		{Rel: "a"},
		{Rel: "b", Err: ErrAttachExists},
		{Rel: "c"},
	})
	if done != 2 || len(problems) != 1 || problems[0].Rel != "b" {
		t.Fatalf("汇总不对：done=%d problems=%+v", done, problems)
	}
}
