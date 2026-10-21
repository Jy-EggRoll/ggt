// ui_git_args_test.go 覆盖推送 / 拉取 / 拉取的参数构造，以及看板上创建标签的校验与端点。
//
// 三组参数函数是从 HTTP 处理函数里抽出来的纯函数，这里直接断言参数形状：靠起服务、
// 建仓库、真的推一次来验证“有没有带 --follow-tags”代价大得多，而且失败原因常常被
// 网络或凭据掩盖。标签端点则在真实临时仓库上跑一次，确认校验之后 git 真的建出了附注标签。
package cmd

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jy-eggroll/ggt/internal/config"
)

func TestPushArgs(t *testing.T) {
	cases := []struct {
		name     string
		withTags bool
		want     string
	}{
		{"附带标签", true, "push --follow-tags"},
		{"不带标签", false, "push"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := strings.Join(pushArgs(c.withTags), " "); got != c.want {
				t.Fatalf("pushArgs(%v) = %q，期望 %q", c.withTags, got, c.want)
			}
		})
	}
}

func TestFetchArgs(t *testing.T) {
	cases := []struct {
		name  string
		prune bool
		want  string
	}{
		// 修剪打开时必须是 --prune 而不是 --prune-tags：后者会把本地标签一起带走
		{"修剪分支", true, "fetch --all --prune"},
		{"不修剪", false, "fetch --all"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := strings.Join(fetchArgs(c.prune), " ")
			if got != c.want {
				t.Fatalf("fetchArgs(%v) = %q，期望 %q", c.prune, got, c.want)
			}
			if strings.Contains(got, "--prune-tags") {
				t.Fatalf("不应出现 --prune-tags：本地标签一律不自动修剪，实得 %q", got)
			}
		})
	}
}

func TestPullArgs(t *testing.T) {
	cases := []struct {
		name  string
		prune bool
		want  string
	}{
		{"修剪分支", true, "pull --ff-only --prune"},
		{"不修剪", false, "pull --ff-only"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := strings.Join(pullArgs(c.prune), " "); got != c.want {
				t.Fatalf("pullArgs(%v) = %q，期望 %q", c.prune, got, c.want)
			}
		})
	}
}

// TestTagArgs 断言校验的拒绝路径与合法输入的参数形状。
// 每一条拒绝都对应一种“放过去就会出事”的输入：哈希为空会把标签建在别的东西上，
// 以 - 开头的名字会被 git 当成选项（-d 是删除标签），空说明会让 git 去开编辑器
func TestTagArgs(t *testing.T) {
	const (
		hash = "0123456789abcdef0123456789abcdef01234567"
		msg  = "第一个版本"
	)

	cases := []struct {
		name    string
		hash    string
		tag     string
		message string
		want    string
		wantErr string
	}{
		{"合法", hash, "v0.1.0", msg, "tag -a v0.1.0 " + hash + " -m " + msg, ""},
		{"首尾空白被规整", " " + hash + " ", " v0.1.0 ", " " + msg + " ", "tag -a v0.1.0 " + hash + " -m " + msg, ""},
		{"哈希太短", "abc", "v0.1.0", msg, "", "Invalid commit hash"},
		{"哈希含非十六进制字符", "zzzz51", "v0.1.0", msg, "", "Invalid commit hash"},
		{"标签名为空", hash, "  ", msg, "", "A tag name is required"},
		{"标签名以 - 开头", hash, "-d", msg, "", "Invalid tag name"},
		{"标签名含空格", hash, "v0 1", msg, "", "Invalid tag name"},
		{"标签名含控制字符", hash, "v0\t1", msg, "", "Invalid tag name"},
		{"说明为空", hash, "v0.1.0", "   ", "", "A tag message is required"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args, err := tagArgs(c.hash, c.tag, c.message)
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("期望报错 %q，实际通过，参数为 %q", c.wantErr, strings.Join(args, " "))
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("错误文案 = %q，期望含 %q", err.Error(), c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("不应报错：%v", err)
			}
			if got := strings.Join(args, " "); got != c.want {
				t.Fatalf("参数 = %q，期望 %q", got, c.want)
			}
		})
	}
}

// newTagTestRepo 建一个有一个提交的临时仓库。
// 直接调 git 而不是走 internal/git：这里要的是布置场景，与产品代码的封装无关
func newTagTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", "ggt test"},
		{"config", "user.email", "ggt@example.com"},
		{"commit", "-q", "--allow-empty", "-m", "初始提交"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s 失败: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	return dir
}

// newBareRemote 建一个裸仓库充当远端，返回它的路径。
// 用本地路径而不是网络地址：推送逻辑要验证的是“推给了哪个远端、带没带选项”，
// 不该被凭据与网络状态干扰
func newBareRemote(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bare := filepath.Join(dir, "remote.git")
	gitOut(t, dir, "init", "-q", "--bare", bare)
	return bare
}

// TestTagPushRemote 覆盖远端解析的四条分支。
// 有上游时的结果必须来自上游而不是远端数量：仓库同时配了多个远端时，
// 上游是唯一能说明“这一次该推给谁”的依据
func TestTagPushRemote(t *testing.T) {
	ctx := context.Background()

	t.Run("有上游时取上游的第一段", func(t *testing.T) {
		repo := newTagTestRepo(t)
		gitOut(t, repo, "remote", "add", "upstream", newBareRemote(t))
		gitOut(t, repo, "remote", "add", "other", newBareRemote(t))
		branch := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
		gitOut(t, repo, "push", "-q", "-u", "upstream", branch)

		got, err := tagPushRemote(ctx, repo)
		if err != nil {
			t.Fatalf("不应报错：%v", err)
		}
		if got != "upstream" {
			t.Fatalf("远端 = %q，期望 upstream", got)
		}
	})

	t.Run("无上游但只有一个远端", func(t *testing.T) {
		repo := newTagTestRepo(t)
		gitOut(t, repo, "remote", "add", "origin", newBareRemote(t))

		got, err := tagPushRemote(ctx, repo)
		if err != nil {
			t.Fatalf("不应报错：%v", err)
		}
		if got != "origin" {
			t.Fatalf("远端 = %q，期望 origin", got)
		}
	})

	for _, c := range []struct {
		name    string
		remotes int
	}{
		{"多个远端且无上游", 2},
		{"没有远端", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			repo := newTagTestRepo(t)
			for i := 0; i < c.remotes; i++ {
				gitOut(t, repo, "remote", "add", fmt.Sprintf("r%d", i), newBareRemote(t))
			}

			_, err := tagPushRemote(ctx, repo)
			if err == nil {
				t.Fatal("期望报错，实际取到了远端")
			}
			if status := statusOf(err, http.StatusInternalServerError); status != http.StatusConflict {
				t.Fatalf("状态 = %d，期望 %d", status, http.StatusConflict)
			}
			if !strings.Contains(err.Error(), "upstream") {
				t.Fatalf("错误文案应说明需要先设置上游：%q", err.Error())
			}
		})
	}
}

// TestTagPushArgs 断言推送单个标签的参数形状。
// 完整引用名与 --no-follow-tags 都不能少：前者决定推的是标签而不是同名分支，
// 后者决定会不会把本机其它附注标签一起带走
func TestTagPushArgs(t *testing.T) {
	got := strings.Join(tagPushArgs("origin", "v1.0.0"), " ")
	want := "push origin refs/tags/v1.0.0 --no-follow-tags"
	if got != want {
		t.Fatalf("参数 = %q，期望 %q", got, want)
	}
}

// TestHandleTagPush 验证端点上的“创建后推送”：成功后远端真的拿到标签，
// 推送失败时本地标签保留并在响应里说明这一点。
// 这两条正是“不回滚”这一决定的可观察结果，只测 tagPushArgs 看不到
func TestHandleTagPush(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cache := &uiCache{ctx: context.Background()}
	post := func(t *testing.T, cache *uiCache, body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		cache.handleTag(rec, httptest.NewRequest(http.MethodPost, "/api/tag", strings.NewReader(body)))
		return rec
	}

	t.Run("推送成功", func(t *testing.T) {
		repo := newTagTestRepo(t)
		bare := newBareRemote(t)
		gitOut(t, repo, "remote", "add", "origin", bare)
		branch := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
		gitOut(t, repo, "push", "-q", "-u", "origin", branch)
		withConfig(t, &config.Config{RepoPaths: []string{repo}, PushTags: true, PruneBranches: true})
		hash := gitOut(t, repo, "rev-parse", "HEAD")

		rec := post(t, cache, fmt.Sprintf(`{"repo":%q,"hash":%q,"name":"v1.0.0","message":"说明","push":true}`, repo, hash))
		if rec.Code != http.StatusOK {
			t.Fatalf("状态 = %d，期望 200，响应体 %s", rec.Code, rec.Body.String())
		}
		if tags := gitOut(t, bare, "tag", "-l"); !strings.Contains(tags, "v1.0.0") {
			t.Fatalf("远端没有收到标签，实得 %q", tags)
		}
		if body := rec.Body.String(); !strings.Contains(body, "v1.0.0") || !strings.Contains(body, "origin") {
			t.Fatalf("成功文案应说明标签名与远端，实得 %s", body)
		}
	})

	t.Run("推送失败不回滚本地标签", func(t *testing.T) {
		repo := newTagTestRepo(t)
		// 指向不存在的路径：远端名能解析出来，但推送一定失败
		gitOut(t, repo, "remote", "add", "origin", filepath.Join(t.TempDir(), "missing.git"))
		withConfig(t, &config.Config{RepoPaths: []string{repo}, PushTags: true, PruneBranches: true})
		hash := gitOut(t, repo, "rev-parse", "HEAD")

		rec := post(t, cache, fmt.Sprintf(`{"repo":%q,"hash":%q,"name":"v2.0.0","message":"说明","push":true}`, repo, hash))
		if rec.Code != http.StatusConflict {
			t.Fatalf("状态 = %d，期望 409，响应体 %s", rec.Code, rec.Body.String())
		}
		if got := gitOut(t, repo, "tag", "-l", "v2.0.0"); got != "v2.0.0" {
			t.Fatalf("推送失败后本地标签应保留，实得 %q", got)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "created locally") || !strings.Contains(body, "origin") {
			t.Fatalf("失败文案应说明本地已创建与远端名，实得 %s", body)
		}
		if !strings.Contains(body, "missing.git") && !strings.Contains(body, "does not appear") {
			t.Fatalf("失败文案应附上 git 的原始说明，实得 %s", body)
		}
	})
}

// TestHandleTagEndpoint 走一遍真实端点：合法请求建出附注标签并带上说明，
// 非法请求被拒且不留下半个标签。
//
// 单独验证“真的建出来”是因为 tagArgs 只证明参数对了，而“参数正确、命令却没执行”
// 这类错误（例如路由没注册、校验分支提前返回）只有跑端点才看得见
func TestHandleTagEndpoint(t *testing.T) {
	// 隔离配置与 HOME：configNow 会现读配置文件，不能让它摸到开发者的那一份
	t.Setenv("HOME", t.TempDir())
	repo := newTagTestRepo(t)
	withConfig(t, &config.Config{RepoPaths: []string{repo}, PushTags: true, PruneBranches: true})
	hash := gitOut(t, repo, "rev-parse", "HEAD")
	cache := &uiCache{ctx: context.Background()}

	post := func(t *testing.T, body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		cache.handleTag(rec, httptest.NewRequest(http.MethodPost, "/api/tag", strings.NewReader(body)))
		return rec
	}

	t.Run("成功创建附注标签", func(t *testing.T) {
		rec := post(t, fmt.Sprintf(`{"repo":%q,"hash":%q,"name":"v0.1.0","message":"第一个版本"}`, repo, hash))
		if rec.Code != http.StatusOK {
			t.Fatalf("状态 = %d，期望 200，响应体 %s", rec.Code, rec.Body.String())
		}
		// cat-file -t 对附注标签给出 tag，对轻量标签给出 commit：
		// 这是“建的是附注标签而不是轻量标签”的直接证据
		if typ := gitOut(t, repo, "cat-file", "-t", "refs/tags/v0.1.0"); typ != "tag" {
			t.Fatalf("标签类型 = %q，期望 tag（附注标签）", typ)
		}
		if out := gitOut(t, repo, "tag", "-l", "-n", "v0.1.0"); !strings.Contains(out, "第一个版本") {
			t.Fatalf("标签说明里没有提交的文案：%q", out)
		}
	})

	t.Run("引用名非法交给 git 复核", func(t *testing.T) {
		// v0..1 能通过本包的前置检查（无空白、不以 - 开头），但 git 的引用名规则拒绝连续的点。
		// 这一条用来确认“完整规则确实交给了 git check-ref-format”
		rec := post(t, fmt.Sprintf(`{"repo":%q,"hash":%q,"name":"v0..1","message":"说明"}`, repo, hash))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("状态 = %d，期望 400，响应体 %s", rec.Code, rec.Body.String())
		}
		if out := gitOut(t, repo, "tag", "-l", "v0..1"); out != "" {
			t.Fatalf("被拒绝的名字不应留下标签，实得 %q", out)
		}
	})

	t.Run("说明为空被拒", func(t *testing.T) {
		rec := post(t, fmt.Sprintf(`{"repo":%q,"hash":%q,"name":"v0.2.0","message":"  "}`, repo, hash))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("状态 = %d，期望 400，响应体 %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("只认 POST", func(t *testing.T) {
		rec := httptest.NewRecorder()
		cache.handleTag(rec, httptest.NewRequest(http.MethodGet, "/api/tag", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("状态 = %d，期望 405", rec.Code)
		}
	})
}
