package cmd

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
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
		"__GGT_FONT_CSS__",
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
		// 生效时机必须随每项下发：面板据此常驻标注，缺了它页面只能显示一个空标签
		if item.ApplyAt == "" {
			t.Errorf("%q 缺 applyAt：页面没法标注它何时生效", item.Key)
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

// TestHandleSettingsPostMatchesCommandLineStrictness 断言面板与命令行对同一批取值给出同一结论。
//
// 三个取值都是实测被面板单独拒绝、而命令行接受的：language 的别名 zh、自定义主题 id、
// theme_dark 的空串。合法取值的唯一判据是注册表的 Normalize，候选清单只是“页面上能直接
// 点的那几个”——拿它当写入判据，同一份请求在不同机器上会给出不同结论
func TestHandleSettingsPostMatchesCommandLineStrictness(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggt-config.json")
	rec := callSettings(t, handleSettings(path), http.MethodPost,
		`{"values":{"language":"zh","theme":"/tmp/my-theme.json","theme_dark":""}}`)
	out := decodeSettingsResult(t, rec, http.StatusOK)

	if len(out.Errors) != 0 {
		t.Fatalf("命令行能写的取值，面板也该写进去：%+v", out.Errors)
	}
	raw, err := config.ReadRawAt(path)
	if err != nil {
		t.Fatalf("回读失败：%v", err)
	}
	if raw["language"] != "zh-CN" {
		t.Errorf("language 应归一成 zh-CN，实得 %#v", raw["language"])
	}
	if raw["theme"] != "/tmp/my-theme.json" {
		t.Errorf("自定义主题 id 应原样落盘，实得 %#v", raw["theme"])
	}
	// 空串的落盘形态就是空串：它表示“回落到内置默认主题”（见 cmd/ui_theme.go 的 themePref）
	if v, exists := raw["theme_dark"]; !exists || v != "" {
		t.Errorf("theme_dark 应写成空串，实得 %#v（存在：%v）", v, exists)
	}
}

// TestHandleSettingsPostRejectsUnknownKeys 断言请求里的未知键进 errors，而不是被静默丢掉。
//
// 此前端点只按注册表遍历请求，多出来的键一声不响地消失，响应还是 200 空结果——
// 用户以为那一项保存了
func TestHandleSettingsPostRejectsUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggt-config.json")
	rec := callSettings(t, handleSettings(path), http.MethodPost,
		`{"values":{"Log_Levle":"debug","log_level":"debug"}}`)
	out := decodeSettingsResult(t, rec, http.StatusOK)

	errText, ok := out.Errors["Log_Levle"]
	if !ok {
		t.Fatalf("未知键应以原样键名报错，实得 %+v", out.Errors)
	}
	if !strings.Contains(errText, "Log_Levle") {
		t.Errorf("报错应点名那个键，实得 %q", errText)
	}
	if len(out.Applied) != 1 || out.Applied[0] != "log_level" {
		t.Errorf("同批里合法的项仍应写入，实得 %v", out.Applied)
	}
}

// TestHandleSettingsPostMatchesKeysCaseInsensitively 断言键名匹配与命令行同一规则。
//
// config.Lookup 忽略大小写与首尾空白，面板此前却按请求里的原样键名去查注册表，
// 于是 "Language" 既不生效也不报错。两种口径必须一致，否则“面板存的、命令行读的”会分叉
func TestHandleSettingsPostMatchesKeysCaseInsensitively(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggt-config.json")
	rec := callSettings(t, handleSettings(path), http.MethodPost, `{"values":{" Language ":"zh"}}`)
	out := decodeSettingsResult(t, rec, http.StatusOK)

	if len(out.Errors) != 0 {
		t.Fatalf("大小写与空白不该影响键名匹配：%+v", out.Errors)
	}
	if len(out.Applied) != 1 || out.Applied[0] != "language" {
		t.Errorf("应报告规范键名 language 已写入，实得 %v", out.Applied)
	}
	raw, err := config.ReadRawAt(path)
	if err != nil {
		t.Fatalf("回读失败：%v", err)
	}
	if raw["language"] != "zh-CN" {
		t.Errorf("文件里的 language 应为 zh-CN，实得 %#v", raw["language"])
	}
}

// TestHandleSettingsPostRejectsConflictingDuplicates 断言同一项写成两个不同取值时不写入。
//
// 任选一个的后果是“保存成功”却写进了用户没打算用的值，而页面事后只能显示其中一个
func TestHandleSettingsPostRejectsConflictingDuplicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggt-config.json")
	rec := callSettings(t, handleSettings(path), http.MethodPost,
		`{"values":{"language":"zh","Language":"en"}}`)
	out := decodeSettingsResult(t, rec, http.StatusOK)

	if out.Errors["language"] == "" {
		t.Fatalf("同一项的两个不同取值应被拒绝：%+v", out)
	}
	if _, err := os.Stat(path); err == nil {
		t.Errorf("冲突的取值不该落盘")
	}
}

// TestHandleSettingsPostReportsRemainingProblems 断言保存后把文件里仍然存在的问题按项回传。
//
// 这一次保存只处理用户改过的那几项，文件里别的坏值（以及拼错的未知键）会被原样留着，
// 下一次启动仍然失败。只回一句“已保存”会让用户以为配置已经好了
func TestHandleSettingsPostReportsRemainingProblems(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggt-config.json")
	writeRawConfig(t, path, `{"repo_paths":[""],"typo_key":1,"log_level":"trace"}`)

	rec := callSettings(t, handleSettings(path), http.MethodPost, `{"values":{"log_level":"debug"}}`)
	out := decodeSettingsResult(t, rec, http.StatusOK)

	if len(out.Errors) != 0 {
		t.Fatalf("这一项应当写进去：%+v", out.Errors)
	}
	for _, key := range []string{"repo_paths", "typo_key"} {
		if out.Remaining[key] == "" {
			t.Errorf("剩余问题里应包含 %q，实得 %+v", key, out.Remaining)
		}
	}
	if _, still := out.Remaining["log_level"]; still {
		t.Error("刚改好的项不该出现在剩余问题里")
	}
}

// TestHandleSettingsUnsetManagedBrokenValue 断言受管项的坏值可以从面板清掉。
//
// 与命令行 reset 共用同一份判定（config.PlanUnsetAt）：受管约束的是“怎么设值”，
// 不该拦住“怎么清除一个已经坏掉的值”，否则配置坏掉时面板也救不回来
func TestHandleSettingsUnsetManagedBrokenValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggt-config.json")
	writeRawConfig(t, path, `{"repo_paths":[""]}`)

	rec := callSettings(t, handleSettings(path), http.MethodPost, `{"unset":["repo_paths"]}`)
	out := decodeSettingsResult(t, rec, http.StatusOK)

	if len(out.Errors) != 0 {
		t.Fatalf("受管项的坏值应允许清掉：%+v", out.Errors)
	}
	if out.Notes["repo_paths"] == "" {
		t.Error("应说明这次是清理受管项的坏值，而不是绕开了 ggt repo")
	}
	raw, err := config.ReadRawAt(path)
	if err != nil {
		t.Fatalf("回读失败：%v", err)
	}
	if _, exists := raw["repo_paths"]; exists {
		t.Error("恢复默认应当把键从文件里删掉")
	}
}

// TestHandleSettingsPostUnsetsUnknownKey 断言文件里确实写着的未知键能从面板删掉。
//
// 与命令行 reset 同一规则：键在文件里就放行（否则拼错一个键名只剩手改文件），
// 不在文件里则报错，拼写错误当场暴露
func TestHandleSettingsPostUnsetsUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggt-config.json")
	writeRawConfig(t, path, `{"typo_key":1}`)

	rec := callSettings(t, handleSettings(path), http.MethodPost, `{"unset":["typo_key"]}`)
	out := decodeSettingsResult(t, rec, http.StatusOK)
	if len(out.Errors) != 0 {
		t.Fatalf("文件里存在的未知键应允许删除：%+v", out.Errors)
	}

	rec = callSettings(t, handleSettings(path), http.MethodPost, `{"unset":["other_typo"]}`)
	out = decodeSettingsResult(t, rec, http.StatusOK)
	if out.Errors["other_typo"] == "" {
		t.Error("文件里不存在的未知键应报错")
	}
}

// TestHandleSettingsGetMarksInvalidEntries 断言 GET 把坏值及其原值一并交给页面。
//
// 这是“面板看不出文件是坏的”的直接修复：坏值此前被换成默认值发出，页面上与“从没改过”
// 一模一样，“恢复默认”按钮因此隐藏，坏值永远提交不上去
func TestHandleSettingsGetMarksInvalidEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggt-config.json")
	writeRawConfig(t, path, `{"log_level":"trace"}`)

	rec := callSettings(t, handleSettings(path), http.MethodGet, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET 应返回 200，实得 %d", rec.Code)
	}
	var out uiSettings
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应不是合法 JSON：%v", err)
	}
	if out.Error != "" {
		t.Errorf("单个坏键不该升级成整份文件的错误：%q", out.Error)
	}
	for _, item := range out.Items {
		if item.Key != "log_level" {
			continue
		}
		if !item.Invalid {
			t.Fatal("log_level 应被标成 invalid")
		}
		if item.Raw != "trace" || item.Error == "" {
			t.Errorf("应给出文件里的原值与原因，实得 raw=%q error=%q", item.Raw, item.Error)
		}
		return
	}
	t.Fatal("视图里没有 log_level")
}

// TestHandleSettingsGetReportsUnreadableFile 断言整份文件读不出来时响应顶层带 error，
// 同时仍然逐项给出视图（面板要能一边画表、一边说清文件坏在哪）
func TestHandleSettingsGetReportsUnreadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggt-config.json")
	writeRawConfig(t, path, `{"log_level":`)

	rec := callSettings(t, handleSettings(path), http.MethodGet, "")
	var out uiSettings
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应不是合法 JSON：%v", err)
	}
	if out.Error == "" {
		t.Fatal("读不出文件时顶层应有 error，页面据此显示原因")
	}
	if len(out.Items) != len(config.Settings()) {
		t.Errorf("仍应逐项给出视图，实得 %d 项", len(out.Items))
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

// TestUIReloadKeysDeriveFromRegistry 断言“改完要刷新页面”的键名清单来自注册表。
//
// 这份清单曾经手写、与注册表分处两个文件：那时配置项改名或删除会留下悬空的名字，不报错，
// 只是刷新提示永远不出现。改为派生后，清单与注册表不可能分叉；这里顺带确认它非空——
// 空了意味着所有项都被误标成“立即生效”，保存后不再提示刷新
func TestUIReloadKeysDeriveFromRegistry(t *testing.T) {
	if len(uiReloadKeys) == 0 {
		t.Fatal("uiReloadKeys 为空：没有任何配置项被标成 ApplyReload")
	}
	for _, key := range uiReloadKeys {
		s, ok := config.Lookup(key)
		if !ok {
			t.Errorf("uiReloadKeys 里的 %q 在注册表里不存在", key)
			continue
		}
		if s.ApplyAt != config.ApplyReload {
			t.Errorf("%q 出现在 uiReloadKeys 里，但它的 ApplyAt 是 %q", key, s.ApplyAt)
		}
	}
}

// TestHandleSettingsUnsetManagedBrokenListNeedsConfirmation 断言清掉一个还有可用条目的受管坏列表
// 之前必须确认，且服务端在缺确认时不写文件、只回传损失清单。
//
// 与命令行的 reset 是同一条保护：命令行会报“原有几条、可用几条”并要求 --yes，面板则把同一份
// 计数与条目交给页面弹一次确认。两边共用 config.SalvagedPathListAt，因此确认框里承诺的与实际
// 清掉的一定是同一批
func TestHandleSettingsUnsetManagedBrokenListNeedsConfirmation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggt-config.json")
	writeRawConfig(t, path, `{"repo_paths":["/tmp/keep-me",""],"parent_paths":["/tmp/parent"]}`)

	rec := callSettings(t, handleSettings(path), http.MethodPost, `{"unset":["repo_paths"]}`)
	out := decodeSettingsResult(t, rec, http.StatusOK)

	if len(out.Confirms) != 1 {
		t.Fatalf("缺确认时应回传损失清单，实得 %+v", out)
	}
	confirm := out.Confirms[0]
	if confirm.Key != "repo_paths" || confirm.Total != 2 || confirm.Usable != 1 {
		t.Errorf("损失清单应为 repo_paths：总 2 条、可用 1 条，实得 %+v", confirm)
	}
	if len(confirm.Lost) != 1 || confirm.Lost[0] != "/tmp/keep-me" {
		t.Errorf("应列出会丢掉的条目，实得 %#v", confirm.Lost)
	}
	if len(out.Applied) != 0 {
		t.Errorf("缺确认时不该写入，实得 %v", out.Applied)
	}
	raw, err := config.ReadRawAt(path)
	if err != nil {
		t.Fatalf("回读失败：%v", err)
	}
	if _, exists := raw["repo_paths"]; !exists {
		t.Error("缺确认时文件不该被改动")
	}

	// 用户确认后重发同一个请求，这次真的删掉
	rec = callSettings(t, handleSettings(path), http.MethodPost, `{"unset":["repo_paths"],"confirm":["repo_paths"]}`)
	out = decodeSettingsResult(t, rec, http.StatusOK)
	if len(out.Confirms) != 0 {
		t.Errorf("带确认时不该再问一次：%+v", out.Confirms)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("带确认后应删除成功：%+v", out.Errors)
	}
	raw, err = config.ReadRawAt(path)
	if err != nil {
		t.Fatalf("回读失败：%v", err)
	}
	if _, exists := raw["repo_paths"]; exists {
		t.Error("确认后应把键从文件里删掉")
	}
}

// TestHandleSettingsUnsetBrokenListWithoutUsableEntries 断言没有可用条目时不打扰用户。
//
// 命令行的规矩是“usable 为 0 就不问”，面板必须同一条：一条可用条目都不会丢的时候，
// 再弹一次确认只是白拦一次
func TestHandleSettingsUnsetBrokenListWithoutUsableEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggt-config.json")
	writeRawConfig(t, path, `{"repo_paths":[""]}`)

	rec := callSettings(t, handleSettings(path), http.MethodPost, `{"unset":["repo_paths"]}`)
	out := decodeSettingsResult(t, rec, http.StatusOK)
	if len(out.Confirms) != 0 {
		t.Errorf("没有可用条目时不该要确认：%+v", out.Confirms)
	}
	if len(out.Errors) != 0 || len(out.Applied) != 1 {
		t.Errorf("应直接清掉，实得 applied=%v errors=%v", out.Applied, out.Errors)
	}
}

// TestHandleSettingsUnsetConfirmationIsPerKey 断言“确认”只对列出的键生效。
//
// 这是把请求级布尔换成按项确认的直接理由：同一批里放两个受管坏列表时，一个布尔会让
// “看过 repo_paths 的损失清单”顺带授权 parent_paths，后者在从未展示的情况下被清掉。
// 按项确认之后，未被列出的键只回损失清单、不动文件。
func TestHandleSettingsUnsetConfirmationIsPerKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggt-config.json")
	writeRawConfig(t, path, `{"repo_paths":["/tmp/keep-me",""],"parent_paths":["/tmp/parent",""]}`)

	// 两个都要确认：响应必须把两份损失清单都给出来，且一个都不清
	rec := callSettings(t, handleSettings(path), http.MethodPost, `{"unset":["repo_paths","parent_paths"]}`)
	out := decodeSettingsResult(t, rec, http.StatusOK)
	keys := make(map[string]bool, len(out.Confirms))
	for _, c := range out.Confirms {
		keys[c.Key] = true
	}
	if !keys["repo_paths"] || !keys["parent_paths"] {
		t.Fatalf("两个键的损失清单都要回传，实得 %+v", out.Confirms)
	}
	if len(out.Applied) != 0 {
		t.Fatalf("都没确认时不该清掉任何一项，实得 %v", out.Applied)
	}
	raw, err := config.ReadRawAt(path)
	if err != nil {
		t.Fatalf("回读失败：%v", err)
	}
	if _, exists := raw["repo_paths"]; !exists {
		t.Error("repo_paths 未被确认，不该被清掉")
	}
	if _, exists := raw["parent_paths"]; !exists {
		t.Error("parent_paths 未被确认，不该被清掉")
	}

	// 只确认 repo_paths：它被清掉，parent_paths 仍要确认且保持原样
	rec = callSettings(t, handleSettings(path), http.MethodPost,
		`{"unset":["repo_paths","parent_paths"],"confirm":["repo_paths"]}`)
	out = decodeSettingsResult(t, rec, http.StatusOK)
	if len(out.Applied) != 1 || out.Applied[0] != "repo_paths" {
		t.Fatalf("只该清掉已确认的那一项，实得 %v", out.Applied)
	}
	if len(out.Confirms) != 1 || out.Confirms[0].Key != "parent_paths" {
		t.Fatalf("parent_paths 仍应回一份损失清单，实得 %+v", out.Confirms)
	}
	raw, err = config.ReadRawAt(path)
	if err != nil {
		t.Fatalf("回读失败：%v", err)
	}
	if _, exists := raw["repo_paths"]; exists {
		t.Error("已确认的 repo_paths 应被清掉")
	}
	if _, exists := raw["parent_paths"]; !exists {
		t.Error("未确认的 parent_paths 不该被清掉")
	}
}

// writeRawConfig 直接往配置文件落一份内容，绕过 SetFromTextAt 那条写入校验，
// 用来模拟“用户自己动手编辑配置文件”这种情形
func writeRawConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("建配置目录失败：%v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("写配置文件失败：%v", err)
	}
}

// TestFontBlockFollowsConfig 断言字体覆盖规则只由配置决定，并且写坏的取值进不了样式表。
//
// 这一条盯的是“手改配置文件”那条路：注册表的 Parse 只在写入时跑，渲染读的是文件里的原始值，
// 非法取值必须在这里被挡下（丢掉这一项，页面回到“不指定字体”的默认状态），而不是原样拼进 <style>——
// 分号能就地起一条新声明、花括号能闭合整个 :root 块
func TestFontBlockFollowsConfig(t *testing.T) {
	// 隔离配置：fontBlock 读的是默认配置路径，不能碰开发者自己的那一份
	t.Setenv("HOME", t.TempDir())
	path := config.GetDefaultConfigPath()

	if got := fontBlock(); got != "" {
		t.Fatalf("两项都没配置时应返回空串（样式表内置的字体栈继续生效），实得 %q", got)
	}

	writeRawConfig(t, path, `{"font_ui":"Inter, system-ui, sans-serif","font_mono":"Foo;} body{display:none}"}`)
	got := fontBlock()
	if want := ":root{--font-ui:Inter, system-ui, sans-serif;}"; got != want {
		t.Errorf("字体覆盖规则应为 %q，实得 %q", want, got)
	}
	if strings.Contains(got, "display:none") {
		t.Error("非法的字体栈被原样拼进了样式表")
	}

	// 整页渲染时它必须落进那条专用的 <style id="fonts">：混进主题那条规则里的话，
	// “配色解析失败”会连带丢掉字体覆盖
	indexHTML, err := fs.ReadFile(uiAssets, "ui/index.html")
	if err != nil {
		t.Fatalf("读取首页模板失败：%v", err)
	}
	out := string(renderIndexHTML(indexHTML, "en"))
	if !strings.Contains(out, `<style id="fonts">:root{--font-ui:Inter, system-ui, sans-serif;}</style>`) {
		t.Error("渲染后的首页里找不到注入好的字体规则")
	}

	// 清空之后必须回到内置：空串表示“用样式表那套”，不能留下 --font-ui: 这种空声明
	writeRawConfig(t, path, `{"font_ui":"","font_mono":""}`)
	if got := fontBlock(); got != "" {
		t.Errorf("清空字体后应不再注入任何规则，实得 %q", got)
	}
}
