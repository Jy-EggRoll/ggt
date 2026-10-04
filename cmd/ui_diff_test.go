// ui_diff_test.go 验一件事：整仓 diff 的文件级清单与文本里的分段按序对齐。
//
// 必须用真实的 git 来验：它的正确性来自 git 内部按同一个 diff 队列输出这两样东西，
// 手工构造的文本无论怎么写都证明不了它。清单本身的解析（普通、二进制、改名三种记录形态）
// 在 internal/git/numstat_test.go 里，这里不重复
package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jy-eggroll/ggt/internal/git"
)

// TestDiffSectionsAlignWithNumstat 用真实仓库验"清单与分段按序对齐"这个假设。
//
// 页面是靠下标把第 i 段文本与第 i 条清单对上的（见 app.js 的 diffSections）。
// 一旦 git 这两样东西的顺序不再一致，页面上会出现"标题写着 A 文件、内容却是 B 文件"，
// 而这种错位不会有任何报错
func TestDiffSectionsAlignWithNumstat(t *testing.T) {
	repo := initDiffTestRepo(t)

	ctx := context.Background()
	text, _, err := diffText(ctx, repo, true, nil)
	if err != nil {
		t.Fatalf("取暂存区 diff 失败：%v", err)
	}
	files, err := git.DiffNumstat(ctx, repo, true, nil)
	if err != nil {
		t.Fatalf("取暂存区增删行数失败：%v", err)
	}
	if len(files) == 0 {
		t.Fatal("测试仓库应当有若干条暂存改动")
	}

	// diff 文本按 "diff --git " 切段：split 的第一个元素是段前的内容（为空），丢掉它
	sections := strings.Split(text, "diff --git ")
	sections = sections[1:]
	if len(sections) != len(files) {
		t.Fatalf("分段数 %d 与清单条数 %d 不一致，页面会错位", len(sections), len(files))
	}

	for i, sec := range sections {
		// 改名与新增的路径在 +++ 行里写法不同，这里只核对"新路径出现在这一段里"：
		// 段内容本身来自 git，是判断对齐与否的最小充分条件
		if !strings.Contains(sec, files[i].Path) {
			t.Errorf("第 %d 段里找不到清单给出的路径 %q，两边的顺序可能已经不一致", i+1, files[i].Path)
		}
	}

	// 顺带核一下行数：纯改名之外的条目都该有增删行数，二进制那条由 git 标出来
	sawBinary := false
	for _, f := range files {
		if f.Binary {
			sawBinary = true
			continue
		}
		if f.Adds == 0 && f.Dels == 0 && f.OrigPath == "" {
			t.Errorf("%q 应当有增删行数或旧路径，实得 %+v", f.Path, f)
		}
	}
	if !sawBinary {
		t.Error("夹具里的二进制文件没有出现在清单里")
	}
}

// initDiffTestRepo 造一个带几种典型改动的临时仓库，返回其路径。
//
// 覆盖：普通修改、含空格与中文的新文件、二进制新文件、纯改名、改名同时改内容。
// 用真实 git 而不是手写 diff 文本，理由见本文件顶部
func initDiffTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("环境里没有 git，跳过")
	}

	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=ggt", "GIT_AUTHOR_EMAIL=ggt@example.com",
			"GIT_COMMITTER_NAME=ggt", "GIT_COMMITTER_EMAIL=ggt@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v 失败：%v（%s）", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("写 %s 失败：%v", name, err)
		}
	}

	git("init", "-q", "-b", "main")
	write("keep.txt", "one\ntwo\n")
	write("renamed-src.txt", "alpha\nbeta\n")
	write("renamed-both.txt", "x\ny\n")
	git("add", "-A")
	git("commit", "-qm", "init")

	// 普通修改
	write("keep.txt", "one\nTWO\nthree\n")
	// 纯改名
	git("mv", "renamed-src.txt", "renamed-dst.txt")
	// 改名同时改内容：相似度低到 git 会当成"删一个、加一个"
	git("mv", "renamed-both.txt", "改名 之后.txt")
	write("改名 之后.txt", "完全\n换了\n内容\n")
	// 含空格与中文的新文件
	write("带空格 的文件.txt", "第一行\n第二行\n")
	// 二进制新文件
	if err := os.WriteFile(filepath.Join(dir, "blob.bin"), []byte{0, 1, 2, 3, 0, 255}, 0o644); err != nil {
		t.Fatalf("写 blob.bin 失败：%v", err)
	}
	git("add", "-A")

	return dir
}
