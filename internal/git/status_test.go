// status_test 覆盖 porcelain v2 状态采集与解析。
//
// 分成两类，理由不同：
//   - ParseStatus 是纯函数，用固定样本断言每种记录类型与边界（含空格路径、重命名、
//     游离 HEAD、空仓库、格式异常），这属于"契约测试"：样本即 git 输出格式的书面约定，
//     一旦解析被改坏，这里会立刻失败而不必依赖真实 git
//   - RunStatus 是集成测试，验证真实 git 确实产出了样本所假设的格式。只靠样本测试
//     会漏掉"git 版本差异导致格式与假设不符"这一整类问题，而这类问题在样本里永远
//     看不出来（样本是按我的假设手写的）
package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runGit 在指定目录执行 git 命令，用于构造测试场景。
// dir 为空时继承当前目录（用于 git init --bare 这类在仓库之外执行的命令）。
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v 失败: %v\n%s", args, err, out)
	}
}

// TestParseStatus_AllRecordKinds 用一份覆盖全部记录类型的样本验证解析结果。
// 记录之间以 NUL 分隔，形态与 `git status --porcelain=v2 -z --branch` 完全一致。
func TestParseStatus_AllRecordKinds(t *testing.T) {
	// 注意 "2" 记录（重命名）在 -z 下把旧路径放在紧随其后的另一条 NUL 记录里，
	// 因此样本里 "renamed.txt" 与 "a.txt" 是两条相邻记录
	sample := strings.Join([]string{
		"# branch.oid 790273f880ffbf48a8defc101ee3cce2e88b7ee1",
		"# branch.head main",
		"# branch.upstream origin/main",
		"# branch.ab +2 -3",
		"1 .M N... 100644 100644 100644 aaaa bbbb src/modified file.go",
		"1 A. N... 000000 100644 100644 0000 cccc staged.txt",
		"2 RM N... 100644 100644 100644 aaaa bbbb R100 renamed.txt",
		"a.txt",
		"? untracked file.txt",
		"! ignored.log",
		"u UU N... 100644 100644 100644 100644 aaaa bbbb cccc conflict.txt",
	}, "\x00") + "\x00"

	st := ParseStatus(sample)

	if st.Branch != "main" {
		t.Errorf("Branch = %q，期望 main", st.Branch)
	}
	if st.Upstream != "origin/main" {
		t.Errorf("Upstream = %q，期望 origin/main", st.Upstream)
	}
	if st.Ahead != 2 || st.Behind != 3 {
		t.Errorf("Ahead/Behind = %d/%d，期望 2/3", st.Ahead, st.Behind)
	}
	if st.Detached || st.NoCommits {
		t.Errorf("不应判定为游离 HEAD 或空仓库: Detached=%v NoCommits=%v", st.Detached, st.NoCommits)
	}
	if len(st.Files) != 6 {
		t.Fatalf("变更条目数 = %d，期望 6: %+v", len(st.Files), st.Files)
	}

	// 路径含空格时必须是完整路径——若误用不限制段数的切分，这里会变成 "src/modified"
	if f := st.Files[0]; f.Index != "." || f.Work != "M" || f.Path != "src/modified file.go" {
		t.Errorf("第 1 条 = %+v，期望 Index=. Work=M Path=\"src/modified file.go\"", f)
	}
	if f := st.Files[1]; f.Index != "A" || f.Work != "." || f.Path != "staged.txt" {
		t.Errorf("第 2 条 = %+v，期望 Index=A Work=. Path=staged.txt", f)
	}
	// 重命名：新路径在 Path，旧路径在 OrigPath；顺序与 --short 的 "旧 -> 新" 相反
	if f := st.Files[2]; f.Index != "R" || f.Path != "renamed.txt" || f.OrigPath != "a.txt" {
		t.Errorf("第 3 条 = %+v，期望 Index=R Path=renamed.txt OrigPath=a.txt", f)
	}
	if f := st.Files[3]; !f.Untracked || f.Path != "untracked file.txt" {
		t.Errorf("第 4 条 = %+v，期望 Untracked 且 Path=\"untracked file.txt\"", f)
	}
	if f := st.Files[4]; !f.Ignored || f.Path != "ignored.log" {
		t.Errorf("第 5 条 = %+v，期望 Ignored 且 Path=ignored.log", f)
	}
	if f := st.Files[5]; !f.Unmerged || f.Path != "conflict.txt" {
		t.Errorf("第 6 条 = %+v，期望 Unmerged 且 Path=conflict.txt", f)
	}
}

// TestParseStatus_SpecialHeads 覆盖游离 HEAD、空仓库与无上游三种头记录形态。
func TestParseStatus_SpecialHeads(t *testing.T) {
	detached := ParseStatus("# branch.oid 6eba12e6db14ed10a2e90fb0661014df44d12b85\x00# branch.head (detached)\x00? f.txt\x00")
	if !detached.Detached {
		t.Error("branch.head 为 (detached) 时应判定为游离 HEAD")
	}
	if detached.Branch != "" {
		t.Errorf("游离 HEAD 时 Branch 应为空串，实际 %q", detached.Branch)
	}
	if len(detached.Files) != 1 {
		t.Errorf("游离 HEAD 不应影响变更解析，条目数 = %d", len(detached.Files))
	}

	initial := ParseStatus("# branch.oid (initial)\x00# branch.head main\x00")
	if !initial.NoCommits {
		t.Error("branch.oid 为 (initial) 时应判定为仓库尚无提交")
	}
	if initial.Branch != "main" {
		t.Errorf("空仓库也应带上分支名，实际 %q", initial.Branch)
	}

	// 无上游：git 不输出 branch.upstream 与 branch.ab 两条头记录
	noUpstream := ParseStatus("# branch.oid abc\x00# branch.head feature-x\x00")
	if noUpstream.Upstream != "" || noUpstream.Ahead != 0 || noUpstream.Behind != 0 {
		t.Errorf("无上游时不应有上游与计数，实际 %q %d/%d", noUpstream.Upstream, noUpstream.Ahead, noUpstream.Behind)
	}
}

// TestParseStatus_SkipsMalformed 验证格式不符的记录被跳过，而不是被当作路径展示。
func TestParseStatus_SkipsMalformed(t *testing.T) {
	// 依次为：段数不足的 "1" 记录、无法识别的记录类型、空记录、恰好合法的未跟踪记录
	sample := strings.Join([]string{"1 .M", "x garbage", "", "? ok.txt"}, "\x00") + "\x00"
	st := ParseStatus(sample)
	if len(st.Files) != 1 {
		t.Fatalf("只应保留 1 条合法记录，实际 %d: %+v", len(st.Files), st.Files)
	}
	if st.Files[0].Path != "ok.txt" {
		t.Errorf("保留的记录应为 ok.txt，实际 %q", st.Files[0].Path)
	}
}

// TestRunStatus_RealRepo 在真实仓库上验证采集：分支名、未跟踪与工作区修改都要被正确识别。
func TestRunStatus_RealRepo(t *testing.T) {
	repo := newTestRepo(t)
	// 修改已跟踪文件，并新增一个含空格且未跟踪的文件
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed"), 0644); err != nil {
		t.Fatalf("写入文件失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new file.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("写入文件失败: %v", err)
	}

	st, err := RunStatus(context.Background(), repo)
	if err != nil {
		t.Fatalf("RunStatus 返回错误: %v", err)
	}
	if st.Branch == "" {
		t.Error("真实仓库应能解析出当前分支名")
	}
	// 本地临时仓库没有上游，不应误判出上游或计数
	if st.Upstream != "" {
		t.Errorf("本地仓库无上游，实际 %q", st.Upstream)
	}

	var sawModified, sawUntracked bool
	for _, f := range st.Files {
		if f.Path == "README.md" && f.Work == "M" {
			sawModified = true
		}
		if f.Path == "new file.txt" && f.Untracked {
			sawUntracked = true
		}
	}
	if !sawModified {
		t.Errorf("应识别出 README.md 的工作区修改，实际条目: %+v", st.Files)
	}
	if !sawUntracked {
		t.Errorf("应识别出未跟踪的 \"new file.txt\"，实际条目: %+v", st.Files)
	}
}

// TestRunStatus_AheadAndBehind 在建立真实上游关系的仓库上验证领先/落后计数。
//
// 为什么必须做真实上游验证：Ahead 是 WebUI 排序的分组依据之一（有未推送提交的仓库
// 要排在干净仓库之前）。若计数取错，页面不会报错，只会安静地把顺序排错——
// 这正是纯样本测试覆盖不到、必须走真实链路的那部分。
func TestRunStatus_AheadAndBehind(t *testing.T) {
	repo := newTestRepo(t)

	// 建一个裸仓库作为上游，并把当前分支推上去建立 upstream 关系
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "-q", "--bare", remote)
	runGit(t, repo, "remote", "add", "origin", remote)
	runGit(t, repo, "push", "-q", "-u", "origin", "HEAD")

	// 本地再提交一次但不推送，于是相对上游领先 1 个提交
	if err := os.WriteFile(filepath.Join(repo, "local-only.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("写入文件失败: %v", err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "local only")

	st, err := RunStatus(context.Background(), repo)
	if err != nil {
		t.Fatalf("RunStatus 返回错误: %v", err)
	}
	if st.Upstream == "" {
		t.Fatalf("建立上游后应解析出 Upstream，实际为空: %+v", st)
	}
	if st.Ahead != 1 || st.Behind != 0 {
		t.Errorf("Ahead/Behind = %d/%d，期望 1/0", st.Ahead, st.Behind)
	}
}

// writeUntrackedFiles 在仓库里造 n 个未跟踪文件，用来把 status 输出撑到指定规模。
// 文件名补零是为了让 git 的排序结果稳定，断言里因此不必关心具体是哪些文件。
func writeUntrackedFiles(t *testing.T, repo string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		name := filepath.Join(repo, fmt.Sprintf("untracked-%03d.txt", i))
		if err := os.WriteFile(name, []byte("x"), 0644); err != nil {
			t.Fatalf("写入未跟踪文件失败: %v", err)
		}
	}
}

// TestRunStatus_LimitHit 验证「条目数超过上限就截断并终止 git 子进程」这条保护路径。
//
// 为什么必须走真实仓库：截断发生在子进程的输出流上（runWithRecordLimit 边读边数、超限
// 杀进程），属于 I/O 行为，喂一段固定样本给 ParseStatus 永远碰不到它。这条路径的价值
// 在于内存兜底——没有它，一个忘了写 .gitignore 的 node_modules 就能把几十 MB 读进内存。
//
// 上限注入 5 这种小值，而不是真造一万个文件：runStatus 把 limit 做成参数正是为了这件事
func TestRunStatus_LimitHit(t *testing.T) {
	repo := newTestRepo(t)
	writeUntrackedFiles(t, repo, 20)

	st, err := runStatus(context.Background(), repo, 5)
	if err != nil {
		t.Fatalf("runStatus 返回错误: %v", err)
	}
	if !st.LimitHit {
		t.Errorf("20 个未跟踪文件、上限 5，应报告本次采集被截断")
	}
	if len(st.Files) == 0 {
		t.Errorf("截断后仍应返回已经读到的条目，实际为空")
	}
	// 记录里还有 "# branch.*" 头记录，它们同样占记录数，所以这里只断言不超过上限
	if len(st.Files) > 5 {
		t.Errorf("截断后条目数 = %d，不应超过上限 5", len(st.Files))
	}
}

// TestRunStatus_LimitZeroMeansUnlimited 验证 limit 取 0 表示不限制，
// 与上游 git.ts:2788 的 "limit !== 0" 判断同义——上游允许把 statusLimit 配成 0 关掉保护
func TestRunStatus_LimitZeroMeansUnlimited(t *testing.T) {
	repo := newTestRepo(t)
	writeUntrackedFiles(t, repo, 20)

	st, err := runStatus(context.Background(), repo, 0)
	if err != nil {
		t.Fatalf("runStatus 返回错误: %v", err)
	}
	if st.LimitHit {
		t.Errorf("上限为 0 表示不限制，不应报告截断")
	}
	if len(st.Files) != 20 {
		t.Errorf("未限制时应返回全部 20 个未跟踪文件，实际 %d 个", len(st.Files))
	}
}
