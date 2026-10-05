package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitOut 执行 git 并返回去掉首尾空白的输出。runGit（status_test.go）只负责在失败时终止，
// 不返回输出，而这里的断言需要拿到哈希
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := RunContext(context.Background(), dir, args...)
	if err != nil {
		t.Fatalf("git %v 失败: %v", args, err)
	}
	return strings.TrimSpace(out)
}

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
}

// runGitDated 与 runGit 相同，但把作者时间与提交时间固定为给定值。
//
// 存在的理由：--topo-order 在多个提交时间戳相同时的先后次序属于 git 的实现细节——实测
// 几个提交落在同一秒里时，`go test -race` 的负载一重就会变成侧分支的提交排在最前，
// 于是“最新提交排第一”这类断言偶发失败。把时间显式钉住，次序才是确定的
func runGitDated(t *testing.T, dir, when string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
		"GIT_AUTHOR_DATE="+when, "GIT_COMMITTER_DATE="+when,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v 失败: %v\n%s", args, err, out)
	}
}

func hasRef(refs []HistoryRef, name string) bool {
	for _, r := range refs {
		if r.Name == name {
			return true
		}
	}
	return false
}

// TestLogHistory_RealRepo 在真仓库上走一遍：拓扑序、父提交、引用挂载、作者与时间、计数。
//
// 场景是一个标准的“分叉后合并”：主线上一个提交、feature 上一个提交、一个 --no-ff 合并提交，
// 外加一个 tag 与一个没有被合并的游离分支（用来验证“只看当前分支”这个范围确实起作用）。
// 各提交的时间戳由用例显式钉住且严格递增，不依赖真实时钟——理由见 runGitDated
func TestLogHistory_RealRepo(t *testing.T) {
	dir := newTestRepo(t)
	base := gitOut(t, dir, "rev-parse", "--abbrev-ref", "HEAD")

	// 时间戳取当前时刻之后，保证比 newTestRepo 那个用真实时钟创建的 init 提交更新
	clock := time.Now().Add(2 * time.Second)
	at := func(step int) string {
		return clock.Add(time.Duration(step) * time.Second).Format(time.RFC3339)
	}

	runGit(t, dir, "checkout", "-q", "-b", "feature")
	writeTestFile(t, dir, "feature.txt", "f")
	runGit(t, dir, "add", ".")
	runGitDated(t, dir, at(1), "commit", "-q", "-m", "feature work")
	featureHash := gitOut(t, dir, "rev-parse", "HEAD")

	// 侧分支上那个没有被合并回来的提交。它的时间排在合并提交之前，
	// 因此在拓扑序里不会跑到合并提交前面（这正是本用例要断言的次序）
	runGit(t, dir, "checkout", "-q", "-b", "side", featureHash)
	writeTestFile(t, dir, "side.txt", "s")
	runGit(t, dir, "add", ".")
	runGitDated(t, dir, at(2), "commit", "-q", "-m", "side work")
	sideHash := gitOut(t, dir, "rev-parse", "HEAD")

	runGit(t, dir, "checkout", "-q", base)
	writeTestFile(t, dir, "base.txt", "b")
	runGit(t, dir, "add", ".")
	runGitDated(t, dir, at(3), "commit", "-q", "-m", "base work")
	runGitDated(t, dir, at(4), "merge", "-q", "--no-ff", "feature", "-m", "merge feature")
	runGit(t, dir, "tag", "v1")
	mergeHash := gitOut(t, dir, "rev-parse", "HEAD")

	ctx := context.Background()

	all, err := LogHistory(ctx, dir, HistoryOptions{All: true, Limit: 50})
	if err != nil {
		t.Fatalf("采集失败: %v", err)
	}
	if len(all) < 5 {
		t.Fatalf("至少应有 5 条提交（init / feature / base / merge / side），实得 %d", len(all))
	}
	if all[0].Hash != mergeHash {
		t.Errorf("拓扑序第一条应当是合并提交 %s，实得 %s", mergeHash, all[0].Hash)
	}
	if len(all[0].Parents) != 2 {
		t.Errorf("合并提交应有两个父提交，实得 %v", all[0].Parents)
	}
	if !hasRef(all[0].Refs, "v1") {
		t.Errorf("tag v1 应挂在合并提交上：%+v", all[0].Refs)
	}
	if !hasRef(all[0].Refs, base) {
		t.Errorf("当前分支的引用应挂在合并提交上：%+v", all[0].Refs)
	}
	if all[0].Author != "test" {
		t.Errorf("作者解析不对：%q", all[0].Author)
	}
	if all[0].Timestamp == 0 {
		t.Errorf("提交时间没有解析出来")
	}

	byHash := make(map[string]HistoryItem, len(all))
	for _, it := range all {
		byHash[it.Hash] = it
	}
	if !hasRef(byHash[featureHash].Refs, "feature") {
		t.Errorf("feature 分支的引用应在它自己的提交上：%+v", byHash[featureHash].Refs)
	}
	if _, ok := byHash[sideHash]; !ok {
		t.Errorf("跨全部分支采集时应当包含游离分支上的提交")
	}

	// 只看当前分支：游离分支上那个提交必须消失，而合并提交仍在
	cur, err := LogHistory(ctx, dir, HistoryOptions{Limit: 50})
	if err != nil {
		t.Fatalf("采集失败: %v", err)
	}
	for _, it := range cur {
		if it.Hash == sideHash {
			t.Errorf("只看当前分支时不该出现游离分支上的提交")
		}
	}
	if cur[0].Hash != mergeHash {
		t.Errorf("只看当前分支时第一条仍应是合并提交，实得 %s", cur[0].Hash)
	}

	n, err := CountHistory(ctx, dir, true)
	if err != nil {
		t.Fatalf("计数失败: %v", err)
	}
	if n != len(all) {
		t.Errorf("计数 %d 与一次取全部得到的 %d 不一致", n, len(all))
	}
	curN, err := CountHistory(ctx, dir, false)
	if err != nil {
		t.Fatalf("计数失败: %v", err)
	}
	if curN != len(cur) {
		t.Errorf("只看当前分支时计数 %d 与采集到的 %d 不一致", curN, len(cur))
	}
}

// TestLogHistory_LimitAndTypes 分页参数与空切片形态：limit 生效、没有父提交/引用的字段是空切片
func TestLogHistory_LimitAndTypes(t *testing.T) {
	dir := newTestRepo(t)
	runGit(t, dir, "commit", "-q", "--allow-empty", "-m", "second")

	items, err := LogHistory(context.Background(), dir, HistoryOptions{All: true, Limit: 1})
	if err != nil {
		t.Fatalf("采集失败: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("limit=1 应当只返回一条，实得 %d", len(items))
	}
	if items[0].Parents == nil || items[0].Refs == nil {
		t.Errorf("父提交与引用应当是空切片而不是 nil：%+v", items[0])
	}
}

// TestHistoryRefs_AnnotatedTagAndRemoteHead annotated tag 要剥到提交上，远程 HEAD 要被跳过
func TestHistoryRefs_AnnotatedTagAndRemoteHead(t *testing.T) {
	dir := newTestRepo(t)
	head := gitOut(t, dir, "rev-parse", "HEAD")

	runGit(t, dir, "tag", "-a", "v2", "-m", "annotated")
	runGit(t, dir, "update-ref", "refs/remotes/origin/main", head)
	runGit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

	refs, err := historyRefs(context.Background(), dir)
	if err != nil {
		t.Fatalf("读引用失败: %v", err)
	}
	if !hasRef(refs[head], "v2") {
		t.Errorf("annotated tag 应剥开挂到提交上：%+v", refs[head])
	}
	if !hasRef(refs[head], "origin/main") {
		t.Errorf("远程分支应挂到提交上：%+v", refs[head])
	}
	for _, r := range refs[head] {
		if strings.HasSuffix(r.Name, "/HEAD") {
			t.Errorf("远程的 HEAD 是符号引用，不该被当作一个引用列出来：%+v", refs[head])
		}
	}
}

// TestCurrentRefs_RealRepo 当前分支、上游与 HEAD：新仓库没有上游，这里要如实返回空串而不是报错
func TestCurrentRefs_RealRepo(t *testing.T) {
	dir := newTestRepo(t)
	head := gitOut(t, dir, "rev-parse", "HEAD")
	branch := gitOut(t, dir, "rev-parse", "--abbrev-ref", "HEAD")

	gotBranch, upstream, gotHead := CurrentRefs(context.Background(), dir)
	if gotHead != head {
		t.Errorf("HEAD 应为 %s，实得 %s", head, gotHead)
	}
	if gotBranch != branch {
		t.Errorf("分支应为 %q，实得 %q", branch, gotBranch)
	}
	if upstream != "" {
		t.Errorf("新仓库不该有上游，实得 %q", upstream)
	}
}
