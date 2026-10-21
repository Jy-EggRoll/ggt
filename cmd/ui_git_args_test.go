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
