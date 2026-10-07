// ui_diff_test.go 验一件事：整仓 diff 的文件级清单与文本里的分段按序对齐。
//
// 必须用真实的 git 来验：它的正确性来自 git 内部按同一个 diff 队列输出这两样东西，
// 手工构造的文本无论怎么写都证明不了它。清单本身的解析（普通、二进制、改名三种记录形态）
// 在 internal/git/numstat_test.go 里，这里不重复
package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jy-eggroll/ggt/internal/config"
	"github.com/jy-eggroll/ggt/internal/git"
)

// TestDiffSectionsAlignWithNumstat 用真实仓库验“清单与分段按序对齐”这个假设。
//
// 页面是靠下标把第 i 段文本与第 i 条清单对上的（见 app.js 的 diffSections）。
// 一旦 git 这两样东西的顺序不再一致，页面上会出现“标题写着 A 文件、内容却是 B 文件”，
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
		// 改名与新增的路径在 +++ 行里写法不同，这里只核对“新路径出现在这一段里”：
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
		t.Error("测试素材里的二进制文件没有出现在清单里")
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
	// 改名同时改内容：相似度低到 git 会当成“删一个、加一个”
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

// TestHandleDiffCommitMode 断言提交视图的四条路：整条提交、单个文件、不在该提交改动清单里的
// 路径（404）、非法哈希（400）。
//
// 后两条不只是“输入校验”：哈希会直接进 git 的命令行，路径决定能读到仓库里的哪个文件。
// 浏览器验收走的是正常路径，拒绝路径用单测固定更省事，也才敢改这段代码
func TestHandleDiffCommitMode(t *testing.T) {
	// 隔离配置：快照采集读的是包级 cfg，这里直接换成只含临时仓库的那一份（同 withConfig），
	// HOME 也一并隔离，免得采集过程中顺带读了开发者自己的配置文件
	t.Setenv("HOME", t.TempDir())
	repo := initDiffTestRepo(t)
	withConfig(t, &config.Config{RepoPaths: []string{repo}})
	hash := gitOut(t, repo, "rev-parse", "HEAD")

	cache := &uiCache{ctx: context.Background()}
	cases := []struct {
		name   string
		query  url.Values
		status int
		check  func(t *testing.T, out uiDiff)
	}{
		{
			name:   "整条提交",
			query:  url.Values{"commit": {hash}},
			status: http.StatusOK,
			check: func(t *testing.T, out uiDiff) {
				if out.Commit != hash {
					t.Errorf("响应里的提交是 %q，期望 %q", out.Commit, hash)
				}
				if len(out.Sections) != 1 || out.Sections[0].Kind != diffKindCommit {
					t.Fatalf("提交视图应当只有一段 kind=%s：%+v", diffKindCommit, out.Sections)
				}
				// 路径与行数来自 git.CommitFiles，页面靠它与正文按下标对齐
				if len(out.Sections[0].Files) == 0 || out.Sections[0].Text == "" {
					t.Errorf("正文或文件清单为空：%+v", out.Sections[0])
				}
			},
		},
		{
			name:   "单个文件",
			query:  url.Values{"commit": {hash}, "file": {"keep.txt"}},
			status: http.StatusOK,
			check: func(t *testing.T, out uiDiff) {
				if out.File != "keep.txt" {
					t.Errorf("文件应当是 keep.txt，实得 %q", out.File)
				}
				if len(out.Sections) != 1 || !strings.Contains(out.Sections[0].Text, "keep.txt") {
					t.Errorf("正文里没有这个文件：%+v", out.Sections)
				}
				// 清单必须跟着正文收窄到这一条：页面按下标命名分段，清单若还是整份，
				// 标题会写成清单的第一个文件（实测就是 keep.txt），与正文对不上
				files := out.Sections[0].Files
				if len(files) != 1 || files[0].Path != "keep.txt" {
					t.Errorf("单文件视图的清单应当只有 keep.txt 这一条，实得 %+v", files)
				}
			},
		},
		{
			// 清单必须跟着正文收窄到这一条：页面按下标命名分段，清单若还是整份，
			// 标题会写成清单的第一个文件（实测就是 keep.txt），与正文对不上。
			// 因此这里特意挑一个不在清单首位的文件，只核对 keep.txt 是看不出这个问题的
			name:   "单个文件（非清单第一条）",
			query:  url.Values{"commit": {hash}, "file": {"renamed-both.txt"}},
			status: http.StatusOK,
			check: func(t *testing.T, out uiDiff) {
				files := out.Sections[0].Files
				if len(files) != 1 || files[0].Path != "renamed-both.txt" {
					t.Errorf("单文件视图的清单应当只有这一条，实得 %+v", files)
				}
				if !strings.Contains(out.Sections[0].Text, "renamed-both.txt") {
					t.Errorf("正文里没有这个文件：%+v", out.Sections)
				}
			},
		},
		{
			// 这条提交里没有这个文件：既挡住路径穿越，也挡住“拿别的提交的文件名来问”
			name:   "不在该提交里的路径",
			query:  url.Values{"commit": {hash}, "file": {"不存在的文件.txt"}},
			status: http.StatusNotFound,
		},
		{
			// 带前导 - 的字符串直接进 git 命令行就是一次参数注入
			name:   "非法哈希",
			query:  url.Values{"commit": {"--upload-pack=touch /tmp/pwned"}},
			status: http.StatusBadRequest,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := url.Values{"repo": {repo}}
			for k, v := range c.query {
				q[k] = v
			}
			req := httptest.NewRequest(http.MethodGet, "/api/diff?"+q.Encode(), nil)
			rec := httptest.NewRecorder()
			cache.handleDiff(rec, req)

			if rec.Code != c.status {
				t.Fatalf("状态码应为 %d，实得 %d（%s）", c.status, rec.Code, rec.Body.String())
			}
			var out uiDiff
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("响应不是合法 JSON：%v（%s）", err, rec.Body.String())
			}
			if c.status != http.StatusOK && out.Error == "" {
				t.Errorf("失败响应应当带上原因：%s", rec.Body.String())
			}
			if c.check != nil {
				c.check(t, out)
			}
		})
	}
}

// gitOut 在指定仓库里跑一条 git 命令并返回标准输出。
// 测试素材里的仓库由 initDiffTestRepo 造好，身份变量与它保持一致；
// 至于不读使用者的全局 git 配置，由本包的 TestMain 统一负责
func gitOut(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=ggt", "GIT_AUTHOR_EMAIL=ggt@example.com",
		"GIT_COMMITTER_NAME=ggt", "GIT_COMMITTER_EMAIL=ggt@example.com",
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v 失败：%v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// TestHandleDiffCompareMode 验范围比较这条路：两点之间、两点顺序反过来、单文件、
// 以及两端各自的非法哈希。
//
// 最要紧的是第一条里的“回退掉的那处改动不在结果里”。造出来的历史是
// 基线 → 改 a.txt → 改 b.txt → 把 a.txt 改回去 → 最终，于是“基线 vs 最终”里
// 只该剩下 b.txt。若哪天有人把它改成逐条提交叠加，这条断言会立刻失败——
// 那正是这个功能存在的理由，也是它与“看每一条提交改了什么”的分界
func TestHandleDiffCompareMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo, base, head := initCompareTestRepo(t)
	withConfig(t, &config.Config{RepoPaths: []string{repo}})

	cache := &uiCache{ctx: context.Background()}
	get := func(t *testing.T, q url.Values) (int, uiDiff) {
		t.Helper()
		q.Set("repo", repo)
		req := httptest.NewRequest(http.MethodGet, "/api/diff?"+q.Encode(), nil)
		rec := httptest.NewRecorder()
		cache.handleDiff(rec, req)
		var out uiDiff
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("响应不是合法 JSON：%v（%s）", err, rec.Body.String())
		}
		return rec.Code, out
	}

	t.Run("两点之间", func(t *testing.T) {
		code, out := get(t, url.Values{"from": {base}, "to": {head}})
		if code != http.StatusOK {
			t.Fatalf("状态码应为 200，实得 %d（%s）", code, out.Error)
		}
		if out.From != base || out.To != head {
			t.Errorf("响应里的端点是 %q → %q，期望 %q → %q", out.From, out.To, base, head)
		}
		if len(out.Sections) != 1 || out.Sections[0].Kind != diffKindCompare {
			t.Fatalf("范围视图应当只有一段 kind=%s：%+v", diffKindCompare, out.Sections)
		}
		files := out.Sections[0].Files
		if len(files) != 1 || files[0].Path != "b.txt" {
			t.Fatalf("基线到最终之间只应剩 b.txt 一处改动，实得 %+v", files)
		}
		if files[0].Adds != 1 || files[0].Dels != 1 {
			t.Errorf("b.txt 应当是一行换一行，实得 +%d −%d", files[0].Adds, files[0].Dels)
		}
		// 中间被回退掉的那处改动不能留痕：正文里不该出现 a.txt
		if strings.Contains(out.Sections[0].Text, "a.txt") {
			t.Errorf("回退掉的改动出现在了结果里：\n%s", out.Sections[0].Text)
		}
	})

	t.Run("两点顺序反过来", func(t *testing.T) {
		code, out := get(t, url.Values{"from": {head}, "to": {base}})
		if code != http.StatusOK {
			t.Fatalf("状态码应为 200，实得 %d（%s）", code, out.Error)
		}
		files := out.Sections[0].Files
		if len(files) != 1 || files[0].Path != "b.txt" {
			t.Fatalf("反过来的结果仍应只有 b.txt，实得 %+v", files)
		}
		// 方向反过来，增删应当互换：这是“谁减谁”这一件事的判据
		if files[0].Adds != 1 || files[0].Dels != 1 {
			t.Errorf("b.txt 反过来仍是一行换一行，实得 +%d −%d", files[0].Adds, files[0].Dels)
		}
		if !strings.Contains(out.Sections[0].Text, "-b1") || !strings.Contains(out.Sections[0].Text, "+b0") {
			t.Errorf("反过来的正文应当是 b1 改成 b0：\n%s", out.Sections[0].Text)
		}
	})

	t.Run("单个文件", func(t *testing.T) {
		code, out := get(t, url.Values{"from": {base}, "to": {head}, "file": {"b.txt"}})
		if code != http.StatusOK {
			t.Fatalf("状态码应为 200，实得 %d（%s）", code, out.Error)
		}
		if out.File != "b.txt" {
			t.Errorf("文件应当是 b.txt，实得 %q", out.File)
		}
		if len(out.Sections) != 1 || !strings.Contains(out.Sections[0].Text, "b.txt") {
			t.Fatalf("正文里没有这个文件：%+v", out.Sections)
		}
		// 清单必须跟着正文收窄，否则页面按下标取到的是别的文件（标题与正文对不上）
		if len(out.Sections[0].Files) != 1 || out.Sections[0].Files[0].Path != "b.txt" {
			t.Errorf("单文件视图的清单应当只有这一条，实得 %+v", out.Sections[0].Files)
		}
	})

	t.Run("两个端点都不在清单里的文件", func(t *testing.T) {
		// a.txt 在两个端点之间没有差别，它不在这次比较的清单里：拿它来问就该是 404
		code, out := get(t, url.Values{"from": {base}, "to": {head}, "file": {"a.txt"}})
		if code != http.StatusNotFound || out.Error == "" {
			t.Errorf("不在这次比较里的路径应当 404 且带上原因，实得 %d（%s）", code, out.Error)
		}
	})

	t.Run("非法哈希", func(t *testing.T) {
		cases := []struct {
			name     string
			from, to string
		}{
			// 带前导 - 的字符串直接进 git 命令行就是一次参数注入
			{"起点非法", "--upload-pack=touch /tmp/pwned", head},
			{"终点非法", base, "--upload-pack=touch /tmp/pwned"},
			{"只有一个端点", base, ""},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				q := url.Values{}
				if c.from != "" {
					q.Set("from", c.from)
				}
				if c.to != "" {
					q.Set("to", c.to)
				}
				code, out := get(t, q)
				if code != http.StatusBadRequest || out.Error == "" {
					t.Errorf("应当 400 且带上原因，实得 %d（%s）", code, out.Error)
				}
			})
		}
	})
}

// initCompareTestRepo 造出“基线 → 数次改动（中间回退过一处）→ 最终”这段历史，
// 返回仓库路径与基线和最终两个提交。a.txt 走了一圈又回到原样，
// 它因此是“回退掉的那处改动不参与计算”这条断言的判据
func initCompareTestRepo(t *testing.T) (string, string, string) {
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
	write("a.txt", "one\n")
	write("b.txt", "b0\n")
	git("add", "-A")
	git("commit", "-qm", "基线")
	base := gitOut(t, dir, "rev-parse", "HEAD")

	write("a.txt", "two\n")
	git("add", "-A")
	git("commit", "-qm", "改 a.txt")

	write("b.txt", "b1\n")
	git("add", "-A")
	git("commit", "-qm", "改 b.txt")

	// 回退：a.txt 回到与基线完全一样的内容
	write("a.txt", "one\n")
	git("add", "-A")
	git("commit", "-qm", "把 a.txt 改回去")

	head := gitOut(t, dir, "rev-parse", "HEAD")
	return dir, base, head
}
