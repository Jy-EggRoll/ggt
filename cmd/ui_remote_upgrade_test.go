// ui_remote_upgrade_test.go 覆盖看板上新增的两条端点：远程协议切换与版本检查。
//
// 两条端点都在真实临时仓库上跑，不依赖网络：协议切换只操作 .git/config，版本检查则把
// “怎么查”替换成打桩实现（见 uiUpgradeChecker），因此测试里没有一次出网请求
package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jy-eggroll/eggokit/updater"
)

// newProtocolTestRepo 建一个带 origin 的临时仓库，返回仓库路径。
// 用 exec.Command 直接调 git 而不是走 internal/git：这里要的是“布置场景”，
// 与产品代码的调用封装无关；失败即 t.Fatalf，避免后续断言在半个场景上给出误导结论
func newProtocolTestRepo(t *testing.T, originURL string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", originURL},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s 失败: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	return dir
}

// originOf 读回仓库当前的 origin 地址，用于核对切换结果真的落盘
func originOf(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("读取 origin 失败: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestDoSwitchRemote 验证共用的切换逻辑：两个方向都改对，已是目标协议时报 same，
// 无法解析的地址报 error 而不是悄悄写坏 origin
func TestDoSwitchRemote(t *testing.T) {
	ctx := context.Background()

	t.Run("HTTPS 转 SSH", func(t *testing.T) {
		dir := newProtocolTestRepo(t, "https://github.com/user/repo.git")
		res := doSwitchRemote(ctx, dir, "ssh")
		if res.status != "switched" {
			t.Fatalf("status = %q, 期望 switched（err=%s）", res.status, res.err)
		}
		if got := originOf(t, dir); got != "git@github.com:user/repo.git" {
			t.Fatalf("origin = %q", got)
		}
	})

	t.Run("SSH 转 HTTPS", func(t *testing.T) {
		dir := newProtocolTestRepo(t, "git@gitlab.com:group/repo.git")
		res := doSwitchRemote(ctx, dir, "https")
		if res.status != "switched" {
			t.Fatalf("status = %q, 期望 switched（err=%s）", res.status, res.err)
		}
		if got := originOf(t, dir); got != "https://gitlab.com/group/repo.git" {
			t.Fatalf("origin = %q", got)
		}
	})

	t.Run("已是目标协议", func(t *testing.T) {
		dir := newProtocolTestRepo(t, "https://github.com/user/repo.git")
		res := doSwitchRemote(ctx, dir, "https")
		if res.status != "same" {
			t.Fatalf("status = %q, 期望 same", res.status)
		}
	})
}

// TestResolveToggleTarget 验证取反规则与命令行共用同一份实现：
// HTTPS 仓库得到 ssh，SSH 仓库得到 https
func TestResolveToggleTarget(t *testing.T) {
	ctx := context.Background()

	fromHTTPS := newProtocolTestRepo(t, "https://github.com/user/repo.git")
	if got, err := resolveToggleTarget(ctx, fromHTTPS); err != nil || got != "ssh" {
		t.Fatalf("HTTPS 仓库取反 = %q, err=%v, 期望 ssh", got, err)
	}

	fromSSH := newProtocolTestRepo(t, "git@github.com:user/repo.git")
	if got, err := resolveToggleTarget(ctx, fromSSH); err != nil || got != "https" {
		t.Fatalf("SSH 仓库取反 = %q, err=%v, 期望 https", got, err)
	}
}

// newRemoteTestCache 造一个带快照的 uiCache，使 handleRemote 能命中仓库而不触发真实采集。
// 预置 data 与 at 是刻意的：collectUIPayload 会去读全局配置仓库，测试不该依赖开发机的配置
func newRemoteTestCache(dir string) *uiCache {
	return &uiCache{
		data: &uiPayload{Repos: []uiRepo{{Name: filepath.Base(dir), Path: dir}}},
		at:   time.Now(),
	}
}

// TestHandleRemote 覆盖端点的三种取值、方法校验与非法输入
func TestHandleRemote(t *testing.T) {
	t.Run("toggle 切成相反协议", func(t *testing.T) {
		dir := newProtocolTestRepo(t, "https://github.com/user/repo.git")
		c := newRemoteTestCache(dir)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/remote",
			strings.NewReader(`{"repo":"`+dir+`","protocol":"toggle"}`))
		c.handleRemote(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		var out uiRemoteResult
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("解析响应失败: %v", err)
		}
		if !out.Switched || out.NewURL != "git@github.com:user/repo.git" {
			t.Fatalf("结果 = %+v", out)
		}
		if got := originOf(t, dir); got != "git@github.com:user/repo.git" {
			t.Fatalf("origin 未落盘 = %q", got)
		}
	})

	t.Run("非法协议被拒", func(t *testing.T) {
		dir := newProtocolTestRepo(t, "https://github.com/user/repo.git")
		c := newRemoteTestCache(dir)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/remote",
			strings.NewReader(`{"repo":"`+dir+`","protocol":"ftp"}`))
		c.handleRemote(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, 期望 400", rec.Code)
		}
		if got := originOf(t, dir); got != "https://github.com/user/repo.git" {
			t.Fatalf("非法请求改动了 origin = %q", got)
		}
	})

	t.Run("GET 被拒", func(t *testing.T) {
		c := newRemoteTestCache(newProtocolTestRepo(t, "https://github.com/user/repo.git"))
		rec := httptest.NewRecorder()
		c.handleRemote(rec, httptest.NewRequest(http.MethodGet, "/api/remote", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, 期望 405", rec.Code)
		}
	})

	t.Run("未知仓库报 404", func(t *testing.T) {
		c := newRemoteTestCache(newProtocolTestRepo(t, "https://github.com/user/repo.git"))
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/remote",
			strings.NewReader(`{"repo":"/nonexistent","protocol":"ssh"}`))
		c.handleRemote(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, 期望 404", rec.Code)
		}
	})
}

// TestHandleUpgradeCheck 用打桩的检查器覆盖版本检查端点的各条分支。
// 全部离线：真正的联网请求只在 uiUpgradeCheckerFromEnv 里，而它不参与这些用例
func TestHandleUpgradeCheck(t *testing.T) {
	stub := func(info *updater.UpdateInfo, err error) uiUpgradeChecker {
		return func(context.Context, updater.Channel) (*updater.UpdateInfo, error) { return info, err }
	}

	decode := func(t *testing.T, rec *httptest.ResponseRecorder) uiUpgradeResult {
		t.Helper()
		var out uiUpgradeResult
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("解析响应失败: %v (%s)", err, rec.Body.String())
		}
		return out
	}

	post := func(check uiUpgradeChecker) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handleUpgradeCheck(check)(rec, httptest.NewRequest(http.MethodPost, "/api/upgrade-check", nil))
		return rec
	}

	t.Run("没有可升级的目标", func(t *testing.T) {
		rec := post(stub(nil, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		out := decode(t, rec)
		if !out.UpToDate || out.Current == "" {
			t.Fatalf("结果 = %+v", out)
		}
	})

	t.Run("有候选版本", func(t *testing.T) {
		rec := post(stub(&updater.UpdateInfo{LatestVersion: "v9.9.9", CurrentComparable: true}, nil))
		out := decode(t, rec)
		if out.Latest != "v9.9.9" || !out.Comparable || out.UpToDate {
			t.Fatalf("结果 = %+v", out)
		}
	})

	t.Run("本地版本无法比较", func(t *testing.T) {
		rec := post(stub(&updater.UpdateInfo{LatestVersion: "v9.9.9", CurrentComparable: false}, nil))
		out := decode(t, rec)
		if out.Comparable {
			t.Fatalf("comparable 应为假: %+v", out)
		}
	})

	t.Run("检查失败", func(t *testing.T) {
		rec := post(stub(nil, errors.New("network unreachable")))
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, 期望 502", rec.Code)
		}
		if out := decode(t, rec); out.Error == "" {
			t.Fatalf("错误未透出: %+v", out)
		}
	})

	t.Run("GET 被拒", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handleUpgradeCheck(stub(nil, nil))(rec, httptest.NewRequest(http.MethodGet, "/api/upgrade-check", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, 期望 405", rec.Code)
		}
	})
}