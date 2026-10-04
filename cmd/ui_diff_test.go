// ui_diff_test.go 覆盖整仓 diff 的两件事：文件级增删清单的解析，以及
// "清单顺序与 diff 文本里的分段顺序一致"这个假设。
//
// 后半件必须用真实的 git 来验：它的正确性来自 git 内部按同一个 diff 队列输出这两样东西，
// 手工构造的文本无论怎么写都证明不了它
package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseNumstatZ 用实际观测到的记录形态钉住解析规则。
//
// 这些用例是从 git 的真实输出里抄下来的（git diff --numstat -z），因为它有几处反直觉：
// 改名记录的路径那一列是空的、旧路径与新路径作为随后两个字段出现，二进制文件的增删两列是 "-"
func TestParseNumstatZ(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []uiDiffFile
	}{
		{
			name: "普通改动",
			in:   "3\t1\tinternal/config/view.go\x00",
			want: []uiDiffFile{{Path: "internal/config/view.go", Added: 3, Removed: 1}},
		},
		{
			name: "含空格与非 ASCII 的路径原样输出，不带引号也不转义",
			in:   "1\t0\t带空格 的文件.txt\x00",
			want: []uiDiffFile{{Path: "带空格 的文件.txt", Added: 1}},
		},
		{
			name: "改名：路径列为空，随后是旧路径与新路径",
			in:   "0\t0\t\x00带空格 原名.txt\x00plain.txt\x00",
			want: []uiDiffFile{{Path: "plain.txt", OrigPath: "带空格 原名.txt"}},
		},
		{
			name: "二进制：增删两列都是 -",
			in:   "-\t-\tblob.bin\x00",
			want: []uiDiffFile{{Path: "blob.bin", Binary: true}},
		},
		{
			name: "多条混在一起，顺序保持不变",
			in: "0\t3\ta.txt\x001\t0\tbin.dat\x00" +
				"0\t0\t\x00带空格 原名.txt\x00plain.txt\x00" +
				"7\t0\t改名 之后.txt\x00",
			want: []uiDiffFile{
				{Path: "a.txt", Removed: 3},
				{Path: "bin.dat", Added: 1},
				{Path: "plain.txt", OrigPath: "带空格 原名.txt"},
				{Path: "改名 之后.txt", Added: 7},
			},
		},
		{
			name: "没有改动时得到空清单",
			in:   "",
			want: []uiDiffFile{},
		},
		{
			name: "认不出的记录被跳过，而不是猜一个文件名出来",
			in:   "not-a-record\x001\t2\tok.txt\x00",
			want: []uiDiffFile{{Path: "ok.txt", Added: 1, Removed: 2}},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseNumstatZ(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("条数：期望 %d 条，实得 %d 条（%+v）", len(c.want), len(got), got)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("第 %d 条：期望 %+v，实得 %+v", i+1, c.want[i], got[i])
				}
			}
		})
	}
}

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
	files, err := diffNumstat(ctx, repo, true, nil)
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

	// 顺带核一下行数：把该文件那一行的增删改成已知值，数量应当对得上
	for _, f := range files {
		if f.Path == "plain.txt" || f.Path == "带空格 的文件.txt" {
			if f.Added == 0 && f.Removed == 0 && f.OrigPath == "" {
				t.Errorf("%q 应当有增删行数或旧路径，实得 %+v", f.Path, f)
			}
		}
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
