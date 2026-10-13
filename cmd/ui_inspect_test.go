// ui_inspect_test.go 验 /api/size 与 /api/files 两条只读端点。
//
// 仓库快照不靠真实采集：uiCache.data 与 at 是包内字段，测试直接注入一份只含目标仓库的
// uiPayload 并把 at 设为当前时刻，repos() 于是原样返回它（见 ui.go 的 repos）。
// 这样构造有两个理由：其一，这两条端点只用到快照里的 Path 与 Name，采集本身不是被测对象；
// 其二，截断那条用例要造一万多个文件，若让 repos() 顺手再跑一次 git status，
// 光采集就比被测逻辑慢一个量级，失败时也难以分辨是采集还是端点出的问题。
// 仓库本身仍是真实 git 仓库：两条端点都要现跑 git，手工构造的输出证明不了它们跑得通。
//
// 截断用例顺带核对服务端与页面的口径一致：uiFileListLimit 是唯一的截断档，
// 页面按 Total 与 Truncated 拼出“共 N 个文件”“只显示前 M 个”，
// 因此这里断言的不只是长度，还有 Total 是截断前的真实条数
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jy-eggroll/ggt/internal/config"
)

// TestHandleSizeAndFiles 覆盖两条端点的正常取数、未知仓库、非 GET，以及文件清单的截断。
//
// 后两类不只是输入校验：仓库路径决定能读到磁盘上的哪个目录（见 ui_inspect.go 的注释），
// 只在浏览器上验收正常路径时，这些拒绝路径没有任何东西守着
func TestHandleSizeAndFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("环境里没有 git，跳过")
	}
	// 隔离 HOME：两条端点都要跑 git，会顺带读全局配置
	t.Setenv("HOME", t.TempDir())
	repo := initDiffTestRepo(t)
	// 阈值写成明确的两档，好核对服务端把配置里的值原样带给了页面
	withConfig(t, &config.Config{RepoPaths: []string{repo}, SizeBucketLowMB: 100, SizeBucketHighMB: 1000})

	cache := &uiCache{ctx: context.Background()}

	// 每次请求前重设快照与其时间戳：复用窗口只有两秒，而截断用例要造一万多个文件，
	// 中途一旦过期就会重新采集，那时拿到的会是真实配置里的仓库，用例会以 404 的形式失败
	snapshot := func(repos ...uiRepo) {
		cache.mu.Lock()
		cache.data = &uiPayload{Repos: repos}
		cache.at = time.Now()
		cache.mu.Unlock()
	}
	call := func(t *testing.T, h http.HandlerFunc, method string, q url.Values) (int, []byte) {
		t.Helper()
		req := httptest.NewRequest(method, "/api/inspect?"+q.Encode(), nil)
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec.Code, rec.Body.Bytes()
	}

	t.Run("大小取数成功", func(t *testing.T) {
		snapshot(uiRepo{Path: repo, Name: "probe"})
		code, body := call(t, cache.handleSize, http.MethodGet, url.Values{"repo": {repo}})
		if code != http.StatusOK {
			t.Fatalf("状态码应为 200，实得 %d（%s）", code, body)
		}
		var out uiSizeInfo
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("响应不是合法 JSON：%v（%s）", err, body)
		}
		if out.Error != "" {
			t.Fatalf("这条仓库应当量得出来，实得错误 %q", out.Error)
		}
		if out.Name != "probe" || out.Repo != repo {
			t.Errorf("响应里的仓库是 %q / %q，期望 probe / %q", out.Name, out.Repo, repo)
		}
		// Disk 与 Pack 是 git 的原话，页面照抄。这里不锁单位：小仓库的 pack 就是
		// "0 bytes"，写死 "KiB" 之类的形状会在别的 git 版本上误报
		if out.Disk == "" {
			t.Error("响应里没有磁盘占用：页面上这一行会显示成一个连字符")
		}
		// 分桶按字节数判定，字节数为零时任何阈值都判不出档位
		if out.Bytes <= 0 {
			t.Errorf("合计字节数应当大于零，实得 %d", out.Bytes)
		}
		// 阈值与口径由服务端算好给页面，页面不重新算一遍
		if out.LowMB != 100 || out.HighMB != 1000 {
			t.Errorf("阈值应当是配置里的 100 / 1000，实得 %d / %d", out.LowMB, out.HighMB)
		}
		if out.Unit != "decimal" {
			t.Errorf("配置未指定口径时应当是 decimal，实得 %q", out.Unit)
		}
		switch out.Bucket {
		case sizeBucketSmall, sizeBucketMid, sizeBucketLarge:
		case "":
			t.Error("响应里没有分桶：页面上这一行取不到颜色，只能落到中间那档")
		default:
			t.Errorf("分桶取值 %q 不在 small / mid / large 之内，页面按它拼出的 class 没有对应样式", out.Bucket)
		}
		// 次要指标的键沿用 git 的原话，count 是 count-objects -vH 一定会给的一项
		found := false
		for _, d := range out.Details {
			if d.Key == "count" && d.Value != "" {
				found = true
			}
		}
		if !found {
			t.Errorf("次要指标里没有 count：%+v", out.Details)
		}
	})

	t.Run("文件清单取数成功", func(t *testing.T) {
		snapshot(uiRepo{Path: repo, Name: "probe"})
		code, body := call(t, cache.handleFiles, http.MethodGet, url.Values{"repo": {repo}})
		if code != http.StatusOK {
			t.Fatalf("状态码应为 200，实得 %d（%s）", code, body)
		}
		var out uiFilesInfo
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("响应不是合法 JSON：%v（%s）", err, body)
		}
		if out.Error != "" {
			t.Fatalf("这条仓库应当列得出来，实得错误 %q", out.Error)
		}
		if out.Total <= 0 || len(out.Files) != out.Total {
			t.Fatalf("未截断时清单条数应当等于总数，实得 %d / %d", len(out.Files), out.Total)
		}
		if out.Truncated {
			t.Error("条数没有超过上限时不该标成已截断，页面会平白多出一条“只显示前 N 个”")
		}
		sawKeep := false
		for _, f := range out.Files {
			if f == "keep.txt" {
				sawKeep = true
			}
		}
		if !sawKeep {
			t.Errorf("清单里没有测试仓库已有的 keep.txt：%+v", out.Files)
		}
	})

	t.Run("未知仓库回 404", func(t *testing.T) {
		// 快照里只有一条仓库，请求的是另一条路径：这条路径不在配置之内，不该被量到
		snapshot(uiRepo{Path: repo, Name: "probe"})
		q := url.Values{"repo": {filepath.Join(t.TempDir(), "not-in-snapshot")}}

		code, body := call(t, cache.handleSize, http.MethodGet, q)
		if code != http.StatusNotFound {
			t.Errorf("大小端点的状态码应为 404，实得 %d（%s）", code, body)
		}
		var sizeOut uiSizeInfo
		if err := json.Unmarshal(body, &sizeOut); err != nil {
			t.Fatalf("响应不是合法 JSON：%v（%s）", err, body)
		}
		if sizeOut.Error == "" {
			t.Error("404 的响应里没有错误说明，页面只会显示一个空面板")
		}

		code, body = call(t, cache.handleFiles, http.MethodGet, q)
		if code != http.StatusNotFound {
			t.Errorf("文件端点的状态码应为 404，实得 %d（%s）", code, body)
		}
		var filesOut uiFilesInfo
		if err := json.Unmarshal(body, &filesOut); err != nil {
			t.Fatalf("响应不是合法 JSON：%v（%s）", err, body)
		}
		if filesOut.Error == "" {
			t.Error("404 的响应里没有错误说明，页面只会显示一个空面板")
		}
	})

	t.Run("非 GET 回 405", func(t *testing.T) {
		snapshot(uiRepo{Path: repo, Name: "probe"})
		q := url.Values{"repo": {repo}}

		code, body := call(t, cache.handleSize, http.MethodPost, q)
		if code != http.StatusMethodNotAllowed {
			t.Errorf("大小端点收到 POST 时状态码应为 405，实得 %d（%s）", code, body)
		}
		var sizeOut uiSizeInfo
		if err := json.Unmarshal(body, &sizeOut); err != nil {
			t.Fatalf("响应不是合法 JSON：%v（%s）", err, body)
		}
		if sizeOut.Error == "" {
			t.Error("405 的响应里没有错误说明")
		}

		code, body = call(t, cache.handleFiles, http.MethodPost, q)
		if code != http.StatusMethodNotAllowed {
			t.Errorf("文件端点收到 POST 时状态码应为 405，实得 %d（%s）", code, body)
		}
		var filesOut uiFilesInfo
		if err := json.Unmarshal(body, &filesOut); err != nil {
			t.Fatalf("响应不是合法 JSON：%v（%s）", err, body)
		}
		if filesOut.Error == "" {
			t.Error("405 的响应里没有错误说明")
		}
	})

	t.Run("git 跑不成时回 200 并透传原话", func(t *testing.T) {
		// 目录本身存在、也在快照里，只是不是 git 仓库：这是“请求合法、这次没量到”，
		// 因此仍回 200，页面只有一条渲染路径（实现在 ui_inspect.go 里已写明）
		plain := t.TempDir()
		snapshot(uiRepo{Path: plain, Name: "plain"})

		code, body := call(t, cache.handleSize, http.MethodGet, url.Values{"repo": {plain}})
		if code != http.StatusOK {
			t.Fatalf("git 失败时状态码应当仍是 200，实得 %d（%s）", code, body)
		}
		var out uiSizeInfo
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("响应不是合法 JSON：%v（%s）", err, body)
		}
		if out.Error == "" {
			t.Error("git 失败时应当把 git 的原话放进 error：笼统的“统计失败”等于让用户去猜")
		}
		if out.Disk != "" {
			t.Errorf("没量到的时候不该给出占用 %q", out.Disk)
		}
	})

	t.Run("超过上限时截断且总数照实回", func(t *testing.T) {
		snapshot(uiRepo{Path: repo, Name: "probe"})
		_, body := call(t, cache.handleFiles, http.MethodGet, url.Values{"repo": {repo}})
		var base uiFilesInfo
		if err := json.Unmarshal(body, &base); err != nil {
			t.Fatalf("响应不是合法 JSON：%v（%s）", err, body)
		}

		// 多造一条，总数正好越过分界线：这样就同时验到了“正好超一条”与“上限以内不截”
		const extra = uiFileListLimit + 1
		dir := filepath.Join(repo, "bulk")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("创建 bulk 目录失败：%v", err)
		}
		for i := 0; i < extra; i++ {
			name := filepath.Join(dir, fmt.Sprintf("f%05d.txt", i))
			if err := os.WriteFile(name, nil, 0o644); err != nil {
				t.Fatalf("写 %s 失败：%v", name, err)
			}
		}

		snapshot(uiRepo{Path: repo, Name: "probe"})
		code, body := call(t, cache.handleFiles, http.MethodGet, url.Values{"repo": {repo}})
		if code != http.StatusOK {
			t.Fatalf("状态码应为 200，实得 %d（%s）", code, body)
		}
		var out uiFilesInfo
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("响应不是合法 JSON：%v（%s）", err, body)
		}

		wantTotal := base.Total + extra
		if out.Total != wantTotal {
			t.Errorf("总数应当是截断前的真实条数 %d，实得 %d：页面据此显示“共 N 个文件”，"+
				"写成截断后的条数会让用户以为仓库里只有这些文件", wantTotal, out.Total)
		}
		if !out.Truncated {
			t.Error("条数已超过上限，响应里没有标出已截断，页面不会提示“只显示前 N 个”")
		}
		if len(out.Files) != uiFileListLimit {
			t.Errorf("清单应当只回前 %d 条，实得 %d", uiFileListLimit, len(out.Files))
		}
	})
}
