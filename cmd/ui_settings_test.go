package cmd

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jy-eggroll/ggt/internal/config"
)

// TestRenderIndexHTMLInjectsEveryPlaceholder 断言首页渲染后不留占位符，
// 且注入进去的都是页面能直接读的东西。
//
// 这条测试的由来：占位符与它所在的表达式同名时（window.__X__ = __X__），ReplaceAll 会把
// 赋值左边也一起换掉，页面拿到一段语法错误的脚本——服务端没有任何异常，只是页面整个不动
func TestRenderIndexHTMLInjectsEveryPlaceholder(t *testing.T) {
	// 隔离配置：注入设置快照会读配置文件，不能碰开发者自己的那一份
	t.Setenv("HOME", t.TempDir())
	indexHTML, err := fs.ReadFile(uiAssets, "ui/index.html")
	if err != nil {
		t.Fatalf("读取首页模板失败：%v", err)
	}

	out := string(renderIndexHTML(indexHTML, "zh-CN"))
	// 逐个点名而不是断言“没有 __GGT_ 这样的子串”：页面里的变量名 __GGT_LANG__ 与
	// __GGT_SETTINGS__ 本来就带这个前缀，那种断言会把它们一起算成残留
	for _, placeholder := range []string{
		"__GGT_HTML_LANG__", "__GGT_LANG_VALUE__", "__GGT_SETTINGS_JSON__", "__GGT_THEME_CSS__",
	} {
		if strings.Contains(out, placeholder) {
			t.Errorf("首页仍留有占位符 %s", placeholder)
		}
	}
	for _, want := range []string{
		`<html lang="zh-CN">`,
		`window.__GGT_LANG__ = "zh-CN";`,
		// 数组字面量：注入的是整份视图，页面直接读它
		`window.__GGT_SETTINGS__ = [`,
		`"key":"notify_timeout"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("渲染后的首页里找不到 %q", want)
		}
	}
}

// callSettings 直接调用设置端点的处理函数。
//
// 刻意不经过 webui 基座：token 门禁、Host 校验、同源校验都是基座那一层的职责（那边有自己的
// 测试），本文件要测的是端点自身“读什么、写什么、拒绝什么”。写入落点用临时目录，
// 绝不碰开发者自己的真实配置
func callSettings(t *testing.T, handler http.HandlerFunc, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, "/api/settings", nil)
	} else {
		req = httptest.NewRequest(method, "/api/settings", strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

// decodeSettingsResult 把 POST 的响应解出来，顺带断言状态码
func decodeSettingsResult(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) uiSettingsResult {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("状态码应为 %d，实得 %d（%s）", wantStatus, rec.Code, rec.Body.String())
	}
	var out uiSettingsResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应不是合法 JSON：%v（%s）", err, rec.Body.String())
	}
	return out
}

// TestHandleSettingsGetCoversRegistry 断言 GET 把注册表完整而且可直接使用地发给页面
func TestHandleSettingsGetCoversRegistry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggt-config.json")
	rec := callSettings(t, handleSettings(path), http.MethodGet, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET 应返回 200，实得 %d", rec.Code)
	}

	var out uiSettings
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应不是合法 JSON：%v", err)
	}
	if out.Path != path {
		t.Errorf("响应里的配置路径应为 %q，实得 %q", path, out.Path)
	}
	if len(out.Items) != len(config.Settings()) {
		t.Fatalf("面板应拿到全部 %d 项，实得 %d", len(config.Settings()), len(out.Items))
	}
	for _, item := range out.Items {
		if item.Title == "" {
			t.Errorf("%q 缺标题", item.Key)
		}
		if item.Options == nil {
			t.Errorf("%q 的候选是 null，页面还得为此多写一个分支", item.Key)
		}
	}
}

// TestHandleSettingsPostWrites 断言 POST 真的把值写进文件，并且写的是规范形态
func TestHandleSettingsPostWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggt-config.json")
	rec := callSettings(t, handleSettings(path), http.MethodPost,
		`{"values":{"size_unit":"BINARY","log_level":"debug"}}`)
	out := decodeSettingsResult(t, rec, http.StatusOK)

	if len(out.Errors) != 0 {
		t.Fatalf("不该有失败项：%+v", out.Errors)
	}
	if len(out.Applied) != 2 {
		t.Errorf("应有两项写入成功，实得 %v", out.Applied)
	}
	// 两项都不是“页面已烧进去”的配置，不该要求刷新
	if out.Reload {
		t.Error("size_unit 与 log_level 不需要刷新页面")
	}

	raw, err := config.ReadRawAt(path)
	if err != nil {
		t.Fatalf("回读失败：%v", err)
	}
	if raw["size_unit"] != "binary" {
		t.Errorf("文件里的 size_unit 应为规范形态 binary，实得 %#v", raw["size_unit"])
	}
	if raw["log_level"] != "debug" {
		t.Errorf("文件里的 log_level 应为 debug，实得 %#v", raw["log_level"])
	}
}

// TestHandleSettingsPostRejectsUnknownChoice 断言严格模式挡住了候选之外的取值。
//
// 网页的下拉框只会给出候选取值，收到别的值说明请求不是页面发的——这条限制继承了
// 旧的 /api/theme：它同样只接受已知主题 id
func TestHandleSettingsPostRejectsUnknownChoice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggt-config.json")
	rec := callSettings(t, handleSettings(path), http.MethodPost,
		`{"values":{"theme":"/tmp/evil.json"}}`)
	out := decodeSettingsResult(t, rec, http.StatusOK)

	if out.Errors["theme"] == "" {
		t.Fatalf("候选之外的主题应被拒绝：%+v", out)
	}
	raw, err := config.ReadRawAt(path)
	if err != nil {
		t.Fatalf("回读失败：%v", err)
	}
	if _, exists := raw["theme"]; exists {
		t.Errorf("被拒绝的取值不该落盘，实得 %#v", raw["theme"])
	}
}

// TestHandleSettingsPostReloadFlag 断言改到“页面已经烧进去”的配置项时要求刷新
func TestHandleSettingsPostReloadFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggt-config.json")
	// 空值代表跟随系统，它是 theme 的候选之一
	rec := callSettings(t, handleSettings(path), http.MethodPost, `{"values":{"theme":""}}`)
	out := decodeSettingsResult(t, rec, http.StatusOK)
	if !out.Reload {
		t.Error("theme 由服务端注入首页，改完必须要求刷新页面")
	}
}

// TestHandleSettingsUnset 断言恢复默认走的是“从文件里删掉这个键”，而不是把默认值写进去
func TestHandleSettingsUnset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggt-config.json")
	if err := config.SetKeyAt(path, "log_level", "debug"); err != nil {
		t.Fatalf("预置失败：%v", err)
	}

	rec := callSettings(t, handleSettings(path), http.MethodPost, `{"unset":["log_level"]}`)
	out := decodeSettingsResult(t, rec, http.StatusOK)
	if len(out.Errors) != 0 {
		t.Fatalf("不该有失败项：%+v", out.Errors)
	}
	if len(out.Applied) != 1 || out.Applied[0] != "log_level" {
		t.Errorf("应报告 log_level 已恢复默认，实得 %v", out.Applied)
	}

	raw, err := config.ReadRawAt(path)
	if err != nil {
		t.Fatalf("回读失败：%v", err)
	}
	if _, exists := raw["log_level"]; exists {
		t.Error("恢复默认应当把键从文件里删掉，而不是写入默认值")
	}
}

// TestHandleSettingsManagedRejected 断言受命令管理的项既不能改也不能恢复默认
func TestHandleSettingsManagedRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggt-config.json")

	rec := callSettings(t, handleSettings(path), http.MethodPost, `{"values":{"repo_paths":"/tmp"}}`)
	out := decodeSettingsResult(t, rec, http.StatusOK)
	if !strings.Contains(out.Errors["repo_paths"], "ggt repo") {
		t.Errorf("报错应指出该用哪个命令，实得 %q", out.Errors["repo_paths"])
	}

	rec = callSettings(t, handleSettings(path), http.MethodPost, `{"unset":["repo_paths"]}`)
	out = decodeSettingsResult(t, rec, http.StatusOK)
	if !strings.Contains(out.Errors["repo_paths"], "ggt repo") {
		t.Errorf("恢复默认同样该被拒绝，实得 %q", out.Errors["repo_paths"])
	}
}

// TestHandleSettingsUnknownKey 断言未知键按项报错，且不影响同批里合法的项
func TestHandleSettingsUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggt-config.json")
	rec := callSettings(t, handleSettings(path), http.MethodPost,
		`{"values":{"log_level":"warn"},"unset":["nope"]}`)
	out := decodeSettingsResult(t, rec, http.StatusOK)
	if out.Errors["nope"] == "" {
		t.Error("未知键应报错")
	}
	if len(out.Applied) != 1 || out.Applied[0] != "log_level" {
		t.Errorf("同批里合法的项仍应写入，实得 %v", out.Applied)
	}
}

// TestHandleSettingsMethodNotAllowed 断言写只挂在 POST 上，别的动词一律拒绝
func TestHandleSettingsMethodNotAllowed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggt-config.json")
	rec := callSettings(t, handleSettings(path), http.MethodPut, `{}`)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT 应返回 405，实得 %d", rec.Code)
	}
}

// TestUIReloadKeysExist 断言 uiReloadKeys 里没有悬空的名字。
//
// 这份清单是“页面已经烧进去的键名”，与注册表分处两个文件；配置项被删掉或改名之后，
// 清单里残留的名字不会有任何编译错误，只会让刷新提示永远不出现
func TestUIReloadKeysExist(t *testing.T) {
	for _, key := range uiReloadKeys {
		if _, ok := config.Lookup(key); !ok {
			t.Errorf("uiReloadKeys 里的 %q 在注册表里不存在", key)
		}
	}
}
