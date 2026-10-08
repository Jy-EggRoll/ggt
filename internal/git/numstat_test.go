package git

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestParseNumstat 用实测到的字节形态验证解析：普通、二进制、重命名三种记录。
// 三份样本都是从真仓库上原样抄下来的（见 CommitFiles 的注释）
func TestParseNumstat(t *testing.T) {
	t.Run("普通与二进制混排", func(t *testing.T) {
		out := "0\t1\ta.txt\x00-\t-\tbin.dat\x002\t0\trenamed.txt\x00"
		files := parseNumstat(out)
		if len(files) != 3 {
			t.Fatalf("应解析出 3 个文件，实得 %d：%+v", len(files), files)
		}
		if files[0].Path != "a.txt" || files[0].Adds != 0 || files[0].Dels != 1 || files[0].Binary {
			t.Errorf("第一条不对：%+v", files[0])
		}
		if !files[1].Binary || files[1].Path != "bin.dat" {
			t.Errorf("二进制那条应标成 Binary 且不带行数：%+v", files[1])
		}
		if files[2].Adds != 2 || files[2].Path != "renamed.txt" {
			t.Errorf("第三条不对：%+v", files[2])
		}
	})

	t.Run("重命名", func(t *testing.T) {
		// 形态：第三个字段为空，紧跟旧路径与新路径
		files := parseNumstat("1\t0\t\x00big.txt\x00moved.txt\x00")
		if len(files) != 1 {
			t.Fatalf("应解析出 1 个文件，实得 %d：%+v", len(files), files)
		}
		if files[0].Path != "moved.txt" || files[0].OrigPath != "big.txt" {
			t.Errorf("重命名应给出新旧两个路径：%+v", files[0])
		}
		if files[0].Adds != 1 || files[0].Dels != 0 {
			t.Errorf("重命名的行数不对：%+v", files[0])
		}
	})

	t.Run("纯重命名与多文件", func(t *testing.T) {
		out := "0\t0\t\x00moved.txt\x00pure.txt\x001\t1\tx.txt\x00"
		files := parseNumstat(out)
		if len(files) != 2 {
			t.Fatalf("应解析出 2 个文件，实得 %d：%+v", len(files), files)
		}
		if files[0].OrigPath != "moved.txt" || files[0].Path != "pure.txt" || files[0].Adds != 0 {
			t.Errorf("纯重命名不对：%+v", files[0])
		}
		if files[1].Path != "x.txt" || files[1].Adds != 1 || files[1].Dels != 1 {
			t.Errorf("紧跟其后的普通记录被误读了：%+v", files[1])
		}
	})

	t.Run("空输出与截断记录", func(t *testing.T) {
		if files := parseNumstat(""); len(files) != 0 {
			t.Errorf("空输出应给空列表：%+v", files)
		}
		// 重命名只给到旧路径就断了：宁可少一条，也不要拿一个不存在的路径当新路径
		if files := parseNumstat("1\t0\t\x00only-old.txt\x00"); len(files) != 0 {
			t.Errorf("截断的重命名记录应被丢弃：%+v", files)
		}
	})
}

// 下面两份样本是从真仓库上原样抄下来的 `show --raw --numstat -z --format=` 输出（哈希缩写保留原样），
// 分别对应普通提交与 merge 提交。加 --raw 之后的解析依赖这两份的先后顺序与字段形态，所以要钉住

// ordinaryRawNumstat 是一条普通提交：raw 记录全部在前（M/D/R100/A），numstat 记录在后
const ordinaryRawNumstat = ":100644 100644 f0f2307 65e69b8 M\x00a.txt\x00" +
	":100644 000000 587be6b 0000000 D\x00del.txt\x00" +
	":100644 100644 ba87d54 ba87d54 R100\x00bin.dat\x00moved-bin.dat\x00" +
	":000000 100644 0000000 3e75765 A\x00new.txt\x00" +
	"2\t1\ta.txt\x000\t1\tdel.txt\x00-\t-\t\x00bin.dat\x00moved-bin.dat\x001\t0\tnew.txt\x00"

// mergeRawNumstat 是一条冲突解决的 merge：numstat 在前，combined diff 的 raw 记录在后。
// only-topic.txt 不在 combined diff 里（它只相对第一父有差异），因此拿不到状态字母
const mergeRawNumstat = "1\t1\tc.txt\x001\t0\tonly-topic.txt\x00" +
	"::100644 100644 100644 af70335 a068d3f bd9d3b5 MM\x00c.txt\x00"

// TestParseNumstatWithStatus_OrdinaryCommit 核对普通提交的状态字母、重命名与二进制标志
func TestParseNumstatWithStatus_OrdinaryCommit(t *testing.T) {
	files := parseNumstatWithStatus(ordinaryRawNumstat)
	if len(files) != 4 {
		t.Fatalf("应解析出 4 个文件，实得 %d：%+v", len(files), files)
	}
	byPath := map[string]CommitFile{}
	for _, f := range files {
		byPath[f.Path] = f
	}

	want := map[string]string{"a.txt": "M", "del.txt": "D", "moved-bin.dat": "R", "new.txt": "A"}
	for path, status := range want {
		if f, ok := byPath[path]; !ok {
			t.Errorf("缺 %s：%+v", path, files)
		} else if f.Status != status {
			t.Errorf("%s 的状态应为 %s，实得 %q", path, status, f.Status)
		}
	}

	// 重命名：状态取 R100 的首字母，旧路径与二进制标志都必须照旧
	if f := byPath["moved-bin.dat"]; f.OrigPath != "bin.dat" || !f.Binary {
		t.Errorf("二进制重命名应给旧路径并标成二进制：%+v", f)
	}
	// 行数不受 --raw 影响
	if f := byPath["a.txt"]; f.Adds != 2 || f.Dels != 1 {
		t.Errorf("a.txt 的行数不对：%+v", f)
	}
}

// TestParseNumstatWithStatus_MergeCombined 核对 merge 的 combined 记录：双冒号、每个父一个字母
func TestParseNumstatWithStatus_MergeCombined(t *testing.T) {
	files := parseNumstatWithStatus(mergeRawNumstat)
	if len(files) != 2 {
		t.Fatalf("应解析出 2 个文件，实得 %d：%+v", len(files), files)
	}
	byPath := map[string]CommitFile{}
	for _, f := range files {
		byPath[f.Path] = f
	}

	// "MM" 取首字母，不能把双冒号记录里的 sha 当成状态
	if f, ok := byPath["c.txt"]; !ok || f.Status != "M" || f.Adds != 1 || f.Dels != 1 {
		t.Errorf("combined 记录的状态应取首字母 M：%+v", f)
	}
	// 没进 combined diff 的文件给空串，前端据此不显示——这是 git 的行为，不是解析漏了
	if f, ok := byPath["only-topic.txt"]; !ok || f.Status != "" || f.Adds != 1 {
		t.Errorf("combined diff 未列出的文件状态应为空串：%+v", f)
	}
}

// TestParseNumstat_RawRecordsDoNotDisturbStats 钉住“加 --raw 不改变行数解析”这条约束：
// 两份记录混排时的结果，必须和只有 numstat 时逐字段相同
func TestParseNumstat_RawRecordsDoNotDisturbStats(t *testing.T) {
	const numstatOnly = "1\t1\tc.txt\x001\t0\tonly-topic.txt\x00"

	withRaw := parseNumstat(mergeRawNumstat)
	without := parseNumstat(numstatOnly)
	if len(withRaw) != len(without) {
		t.Fatalf("文件条数变了：混排 %d，纯 numstat %d", len(withRaw), len(without))
	}
	for i := range without {
		if withRaw[i].Path != without[i].Path || withRaw[i].OrigPath != without[i].OrigPath ||
			withRaw[i].Adds != without[i].Adds || withRaw[i].Dels != without[i].Dels ||
			withRaw[i].Binary != without[i].Binary {
			t.Errorf("第 %d 条被 raw 记录带偏：混排 %+v，纯 numstat %+v", i, withRaw[i], without[i])
		}
	}
}

// TestParseRawStatus_DegradesToEmpty 状态字段不是大写字母、或记录被截断时，宁可给空串也不猜
func TestParseRawStatus_DegradesToEmpty(t *testing.T) {
	if got := parseRawStatus(""); len(got) != 0 {
		t.Errorf("空输出应给空映射：%+v", got)
	}
	// 状态位缺失时最后一段是 sha，首字母小写，必须判为解析不出
	if got := parseRawStatus(":100644 100644 a068d3f bd9d3b5\x00x.txt\x00"); len(got) != 0 {
		t.Errorf("状态位缺失时不应给出状态：%+v", got)
	}
	// 重命名只给到旧路径就断了：连状态一起丢，避免把旧路径当新路径
	if got := parseRawStatus(":100644 100644 a b R100\x00old.txt\x00"); len(got) != 0 {
		t.Errorf("截断的重命名记录不应给出状态：%+v", got)
	}
}

// TestCommitFile_JSONStatusKey 固定前端依赖的键名与形态
func TestCommitFile_JSONStatusKey(t *testing.T) {
	b, err := json.Marshal(CommitFile{Path: "a.txt", Status: "M"})
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	if !strings.Contains(string(b), `"status":"M"`) {
		t.Errorf("status 键名或取值不对：%s", b)
	}
}

// runGitNoFail 与 runGit 同源，但允许 git 以非零退出：造冲突合并必须让 git merge 失败，
// 而 runGit 会当场终止用例
func runGitNoFail(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test")
	// 冲突退出码不是错误，输出也不参与断言
	_ = cmd.Run()
}

// TestCommitFiles_StatusOnRealRepo 在真仓库上跑通 CommitFiles：修改、新增、删除、二进制改名，
// 再加一条冲突解决的 merge。
//
// merge 那条的断言来自实测：git show 对 merge 默认走 combined diff，--raw 只列“每个父都改过”的
// 文件并给每个父一个状态字母（c.txt 是 MM，取 M），而 --numstat 列的是相对第一父的全部差异，
// 于是只在一侧新增的 only-topic.txt 拿不到状态字母，第一父已有的 only-main.txt 干脆不在清单里
func TestCommitFiles_StatusOnRealRepo(t *testing.T) {
	dir := newTestRepo(t)
	ctx := context.Background()

	writeTestFile(t, dir, "mod.txt", "l1\nl2\nl3\n")
	writeTestFile(t, dir, "del.txt", "x\n")
	// 带 NUL 的字节就能让 git 判成二进制
	writeTestFile(t, dir, "bin.dat", "bin\x00\x01\x02")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "base")

	writeTestFile(t, dir, "mod.txt", "l1\nCHANGED\nl3\nl4\n")
	writeTestFile(t, dir, "new.txt", "n\n")
	runGit(t, dir, "rm", "-q", "del.txt")
	runGit(t, dir, "mv", "bin.dat", "moved-bin.dat")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "second")
	second := gitOut(t, dir, "rev-parse", "HEAD")

	files, err := CommitFiles(ctx, dir, second)
	if err != nil {
		t.Fatalf("CommitFiles 失败：%v", err)
	}
	if len(files) != 4 {
		t.Fatalf("应给出 4 个文件，实得 %d：%+v", len(files), files)
	}
	byPath := map[string]CommitFile{}
	for _, f := range files {
		byPath[f.Path] = f
	}

	wantStatus := map[string]string{"mod.txt": "M", "new.txt": "A", "del.txt": "D", "moved-bin.dat": "R"}
	for path, status := range wantStatus {
		f, ok := byPath[path]
		if !ok {
			t.Errorf("缺 %s：%+v", path, files)
			continue
		}
		if f.Status != status {
			t.Errorf("%s 的状态应为 %s，实得 %q", path, status, f.Status)
		}
	}
	if f := byPath["mod.txt"]; f.Adds != 2 || f.Dels != 1 {
		t.Errorf("mod.txt 的行数不对：%+v", f)
	}
	if f := byPath["moved-bin.dat"]; f.OrigPath != "bin.dat" || !f.Binary || f.Adds != 0 || f.Dels != 0 {
		t.Errorf("二进制改名应带上旧路径、标成二进制且不给行数：%+v", f)
	}

	// 冲突解决的 merge：两侧都改了 c.txt
	mainBranch := gitOut(t, dir, "rev-parse", "--abbrev-ref", "HEAD")
	writeTestFile(t, dir, "c.txt", "a\nb\nc\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "c-base")

	runGit(t, dir, "checkout", "-q", "-b", "topic")
	writeTestFile(t, dir, "c.txt", "a\nTOPIC\nc\n")
	writeTestFile(t, dir, "only-topic.txt", "t\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "topic")

	runGit(t, dir, "checkout", "-q", mainBranch)
	writeTestFile(t, dir, "c.txt", "a\nMAIN\nc\n")
	writeTestFile(t, dir, "only-main.txt", "m\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "main-side")

	// 这次合并必然冲突，先让它失败，再手工解决
	runGitNoFail(t, dir, "merge", "topic", "-m", "merge topic")
	writeTestFile(t, dir, "c.txt", "a\nBOTH\nc\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "merge resolved")
	merge := gitOut(t, dir, "rev-parse", "HEAD")

	parents := strings.Fields(gitOut(t, dir, "rev-list", "--parents", "-n1", merge))
	if len(parents) != 3 {
		t.Fatalf("这条提交应当有两个父提交，实得 %v", parents)
	}

	mfiles, err := CommitFiles(ctx, dir, merge)
	if err != nil {
		t.Fatalf("CommitFiles(merge) 失败：%v", err)
	}
	mbyPath := map[string]CommitFile{}
	for _, f := range mfiles {
		mbyPath[f.Path] = f
	}

	if f, ok := mbyPath["c.txt"]; !ok || f.Status != "M" || f.Adds != 1 || f.Dels != 1 {
		t.Errorf("merge 里冲突解决的 c.txt 状态应为 M 且 1 增 1 删：%+v", f)
	}
	if f, ok := mbyPath["only-topic.txt"]; !ok || f.Status != "" || f.Adds != 1 {
		t.Errorf("combined diff 未列出的文件状态应为空串：%+v", f)
	}
	if _, ok := mbyPath["only-main.txt"]; ok {
		t.Errorf("第一父已有的文件不该出现在相对第一父的清单里：%+v", mfiles)
	}
}
