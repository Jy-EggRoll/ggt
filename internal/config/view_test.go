package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/ggt/internal/locales"
)

// newViewConfigPath 返回一个位于测试临时目录里的配置文件路径。
// 本文件所有用例都显式传路径，绝不触到开发者自己的真实配置
func newViewConfigPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "ggt-config.json")
}

// mustSettingsView 取一次设置面板视图；整份文件读不出来时直接让测试失败。
// 本文件的用例关心的都是“逐项长什么样”，文件读不出来属于另一类问题（见 TestSettingsViewReportsUnreadable）
func mustSettingsView(t *testing.T, path string) []SettingView {
	t.Helper()
	views, err := SettingsViewAt(path)
	if err != nil {
		t.Fatalf("读取设置视图失败：%v", err)
	}
	return views
}

// TestSetFromTextAtWrites 覆盖写入的几条路径：规范形态、键名归一、未知键、受管理的项、非法取值
func TestSetFromTextAtWrites(t *testing.T) {
	path := newViewConfigPath(t)

	// 大小写不敏感只是输入方便，落盘必须是规范形态：文件里出现 BINARY 这种同义异形，
	// 会让“体检”与“取值比较”两处都要考虑大小写
	written, err := SetFromTextAt(path, "size_unit", "BINARY")
	if err != nil {
		t.Fatalf("写入失败：%v", err)
	}
	if written != "binary" {
		t.Errorf("写入后的文本应为 binary，实得 %q", written)
	}

	// 键名同样归一：用户手敲时的大小写与首尾空白不该影响结果
	if _, err := SetFromTextAt(path, " Log_Level ", "debug"); err != nil {
		t.Fatalf("键名归一后应该能写入：%v", err)
	}

	raw, err := ReadRawAt(path)
	if err != nil {
		t.Fatalf("回读失败：%v", err)
	}
	if raw["size_unit"] != "binary" {
		t.Errorf("文件里的 size_unit 应为 binary，实得 %#v", raw["size_unit"])
	}
	if raw["log_level"] != "debug" {
		t.Errorf("文件里的 log_level 应为 debug，实得 %#v", raw["log_level"])
	}

	if _, err := SetFromTextAt(path, "nope", "1"); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("未知键应返回 ErrUnknownKey，实得 %v", err)
	}

	// 受命令管理的项：报错必须说出该用哪个命令，否则用户只知道“不让改”却不知道去哪改
	_, err = SetFromTextAt(path, "repo_paths", "[]")
	if err == nil || !strings.Contains(err.Error(), "ggt repo") {
		t.Errorf("repo_paths 应由 ggt repo 管理，实得 %v", err)
	}

	if _, err := SetFromTextAt(path, "size_unit", "octal"); err == nil {
		t.Error("非法取值应当被拒绝")
	}
}

// TestSetFromTextAtHasOneStrictness 断言写入只有一套判定，命令行与设置面板不可能再分叉。
//
// 三个取值都是实测报过的问题：language 的 zh 被候选清单（zh-CN）挡下、自定义主题 id 被挡下、
// theme_dark 的空串被挡下——而命令行三者都收。合法取值的唯一判据是 Normalize，
// 候选清单不参与写入判定（理由见 SetFromTextAt 的注释）
func TestSetFromTextAtHasOneStrictness(t *testing.T) {
	path := newViewConfigPath(t)

	// 别名按 Normalize 归一：zh 存成规范形态 zh-CN
	written, err := SetFromTextAt(path, "language", "zh")
	if err != nil {
		t.Fatalf("语言别名 zh 应被接受：%v", err)
	}
	if written != "zh-CN" {
		t.Errorf("zh 应归一成 zh-CN，实得 %q", written)
	}

	// 自定义主题：候选之外的 id 也收——主题文件是用户随时增删的，
	// 拿“此刻扫描到的候选”当判据会让同一份请求随机器不同而结论不同
	if _, err := SetFromTextAt(path, "theme", "/tmp/my-theme.json"); err != nil {
		t.Errorf("应接受自定义主题 id：%v", err)
	}

	// theme_dark 的空串：收，含义是回落到内置默认主题（见 cmd/ui_theme.go 的 themePref）。
	// 拒绝它只会让命令行与面板各说一套，而这个取值在运行期本来就有确定行为
	if _, err := SetFromTextAt(path, "theme_dark", ""); err != nil {
		t.Errorf("theme_dark 的空串应被接受：%v", err)
	}

	// 反过来：Normalize 说不行的一律不行，两道入口都不会放行
	if _, err := SetFromTextAt(path, "size_unit", "octal"); err == nil {
		t.Error("枚举项候选之外的值应被拒绝")
	}
	if _, err := SetFromTextAt(path, "ignore_submodules", "yes"); err == nil {
		t.Error("布尔项的非布尔取值应被拒绝")
	}
}

// TestSettingsViewAtCoversRegistry 断言视图与注册表一一对应，且形态是页面能直接用的。
//
// 视图是页面唯一的数据来源，少一项就意味着某一项配置在页面上根本没有入口
func TestSettingsViewAtCoversRegistry(t *testing.T) {
	path := newViewConfigPath(t)
	views := mustSettingsView(t, path)
	settings := Settings()

	if len(views) != len(settings) {
		t.Fatalf("视图有 %d 项，注册表有 %d 项", len(views), len(settings))
	}
	for i, s := range settings {
		v := views[i]
		if v.Key != s.Key || v.Title != s.Title || v.Kind != s.Kind {
			t.Errorf("第 %d 项与注册表不一致：视图 %+v，注册表 %+v", i, v, s)
		}
		// 面板上每一项都要有个能读的名字，空标题会渲染成一个没有标签的控件
		if v.Title == "" {
			t.Errorf("%q 缺标题：面板上会显示一个空标签", s.Key)
		}
		// 候选必须是空数组而不是 nil：JSON 里 nil 会编码成 null，页面要为此多写一个分支
		if v.Options == nil {
			t.Errorf("%q 的候选是 nil（编码后是 null），应为空数组", s.Key)
		}
		if want := valueTextLines(s, s.Default); v.Default != want {
			t.Errorf("%q 的默认值文本应为 %q，实得 %q", s.Key, want, v.Default)
		}
		// 生效值必须与逐项读取一致，否则面板显示的不是运行时真正用的值
		eff, err := EffectiveAt(path, s.Key)
		if err != nil {
			t.Fatalf("读取 %q 的生效值失败：%v", s.Key, err)
		}
		if want := valueTextLines(s, eff); v.Value != want {
			t.Errorf("%q 的生效值文本应为 %q，实得 %q", s.Key, want, v.Value)
		}
	}
}

// TestSettingsViewKeepsForeignValue 断言文件里那个候选之外的取值会被补进候选。
//
// 不补的话，下拉框只会显示候选中第一个（HTML 的 select 只认候选项），用户看到的配置
// 与文件里的不是一回事，接着一保存就真的改成了它
func TestSettingsViewKeepsForeignValue(t *testing.T) {
	path := newViewConfigPath(t)
	if err := SetKeyAt(path, "theme", "/tmp/custom.json"); err != nil {
		t.Fatalf("写入失败：%v", err)
	}

	for _, v := range mustSettingsView(t, path) {
		if v.Key != "theme" {
			continue
		}
		if v.Value != "/tmp/custom.json" {
			t.Errorf("生效值应为文件里的自定义路径，实得 %q", v.Value)
		}
		found := false
		for _, o := range v.Options {
			if o.Value == v.Value {
				found = true
			}
		}
		if !found {
			t.Error("候选里应补上当前这个候选之外的值，否则下拉框会显示成别的取值")
		}
		return
	}
	t.Fatal("视图里没有 theme 这一项")
}

// TestSettingsViewPathsAreLines 断言路径列表在视图里是一行一条。
// 页面把它放进多行输入框，若沿用 ValueText 的 [a b] 形态，用户看到的是一串方括号
func TestSettingsViewPathsAreLines(t *testing.T) {
	path := newViewConfigPath(t)
	if err := SetKeyAt(path, "repo_paths", []any{"/a/repo", "/b/repo"}); err != nil {
		t.Fatalf("写入失败：%v", err)
	}

	for _, v := range mustSettingsView(t, path) {
		if v.Key != "repo_paths" {
			continue
		}
		if v.Value != "/a/repo\n/b/repo" {
			t.Errorf("路径应为一行一条，实得 %q", v.Value)
		}
		return
	}
	t.Fatal("视图里没有 repo_paths 这一项")
}

// TestSettingViewJSONFieldNames 断言视图序列化后的字段名就是页面读的那几个名字。
//
// 页面按 o.value / o.label 取值，字段名一旦落成 Go 的字段名（默认大写）或者拼错，
// 页面拿到的就是 undefined——表现是下拉框里一排空白选项，而服务端这边一切正常，
// 单测也全绿。契约横跨两种语言，只能在把字段名钉在这里。
// 这条是实际遇到过的：Option 原来没有 json 标签，四个字段序列化成 Value/Label/Note/Group，
// 页面上所有候选都是空白，是浏览器验收把它抓出来的
func TestSettingViewJSONFieldNames(t *testing.T) {
	b, err := json.Marshal(SettingView{
		Key:         "theme",
		Title:       "Theme",
		Kind:        KindString,
		Value:       "",
		Raw:         "\"oops\"",
		Invalid:     true,
		Error:       "Invalid value for theme: oops",
		Default:     "",
		Expected:    "a theme id",
		Options:     []Option{{Value: "builtin:x.json", Label: "X", Note: "n", Group: "G"}},
		AllowCustom: true,
		Min:         intPtr(1),
		Max:         intPtr(9),
		ManagedBy:   "ggt repo",
	})
	if err != nil {
		t.Fatalf("序列化视图失败：%v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("视图不是合法 JSON：%v", err)
	}
	for _, name := range []string{
		"key", "title", "kind", "value", "raw", "invalid", "error", "default", "expected",
		"options", "allowCustom", "min", "max", "managedBy", "applyAt",
	} {
		if _, ok := raw[name]; !ok {
			t.Errorf("视图 JSON 缺字段 %q，实得 %v", name, jsonKeys(raw))
		}
	}

	var opts []map[string]json.RawMessage
	if err := json.Unmarshal(raw["options"], &opts); err != nil {
		t.Fatalf("候选不是合法 JSON：%v", err)
	}
	if len(opts) != 1 {
		t.Fatalf("候选应有 1 项，实得 %d", len(opts))
	}
	for _, name := range []string{"value", "label", "note", "group"} {
		if _, ok := opts[0][name]; !ok {
			t.Errorf("候选 JSON 缺字段 %q，实得 %v", name, jsonKeys(opts[0]))
		}
	}
}

// jsonKeys 取出对象里的字段名，用于报错时说清“实际有哪些字段”
func jsonKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestSettingsViewFollowsLanguage 断言面板拿到的标题与取值说明跟着当前语言走。
//
// 注册表是包级变量，在 l10n.Init 之前就构造好了，里面存下的只是英文源串；视图层必须
// 现查一次语言（Retranslate）。漏掉这一步的表现是“英文界面正常、中文界面上配置项名字
// 却全是英文”，而这种中英混排只有人看得出来，因此在这里固定下来。
// 顺带覆盖报错里那句取值说明：它与视图里的说明同源，同样要跟着语言走
func TestSettingsViewFollowsLanguage(t *testing.T) {
	if err := l10n.Init("zh-CN", locales.Options()); err != nil {
		t.Fatalf("初始化中文语言包失败：%v", err)
	}
	t.Cleanup(func() { _ = l10n.SetLanguage(locales.Default) })

	path := newViewConfigPath(t)
	found := false
	for _, v := range mustSettingsView(t, path) {
		if v.Key != "log_level" {
			continue
		}
		found = true
		if v.Title != "日志级别" {
			t.Errorf("标题应跟随语言，实得 %q", v.Title)
		}
		if v.Expected != "debug、info、warn、error 之一" {
			t.Errorf("取值说明应跟随语言，实得 %q", v.Expected)
		}
	}
	if !found {
		t.Fatal("视图里没有 log_level 这一项")
	}

	// 枚举错误按约定列出候选项；候选项是配置值，不翻译，但句式要跟着语言走
	_, err := SetFromTextAt(path, "log_level", "trace")
	if err == nil || !strings.Contains(err.Error(), "只能是下列取值之一") ||
		!strings.Contains(err.Error(), "debug, info, warn, error") {
		t.Errorf("报错应使用当前语言并列出候选项，实得 %v", err)
	}
}

// TestSettingBoundsMatchNormalize 断言整数项的取值边界与它自己的规范化入口一致。
//
// Min/Max 既是页面输入框的上下限，也是 Normalize 判定范围时读的那份值。两处若各写一份，
// 页面会允许用户填一个存不进去的值，或者反过来把合法值拦在外面——两者都没有编译错误，
// 只有用户会撞上，因此在这里用“边界外一号必须被拒绝”把它们钉在一起
func TestSettingBoundsMatchNormalize(t *testing.T) {
	for _, s := range Settings() {
		if s.Kind != KindInt || s.Parse == nil {
			continue
		}
		if s.Min != nil {
			if _, err := s.Normalize(*s.Min - 1); err == nil {
				t.Errorf("%q 声明下界 %d，但 Normalize 接受了 %d", s.Key, *s.Min, *s.Min-1)
			}
		}
		if s.Max != nil {
			if _, err := s.Normalize(*s.Max + 1); err == nil {
				t.Errorf("%q 声明上界 %d，但 Normalize 接受了 %d", s.Key, *s.Max, *s.Max+1)
			}
		}
	}
}

// TestSettingsViewApplyAtIsConcrete 断言投影给页面的生效时机总是三个具体取值之一。
//
// 注册表里未标注的项 ApplyAt 是零值（空串），投影时必须归一成 immediate；否则页面按这个字符串
// 拼 i18n 键（applyImmediate / applyReload / applyRestart）时会取到 "apply"，显示一个空标签，
// 而服务端这边一切正常——契约横跨两种语言，只能在这里钉住
func TestSettingsViewApplyAtIsConcrete(t *testing.T) {
	path := newViewConfigPath(t)
	valid := map[ApplyAt]bool{ApplyImmediate: true, ApplyReload: true, ApplyRestart: true}
	for _, v := range mustSettingsView(t, path) {
		if !valid[v.ApplyAt] {
			t.Errorf("%q 的 applyAt 是 %q，不是 immediate / reload / restart 之一", v.Key, v.ApplyAt)
		}
	}
}

// TestApplyAtDeclaredForDeferredKeys 断言“不能立即生效”的配置项都显式标注了 ApplyAt。
//
// 这份标注是设置面板标注、保存后提示、以及命令行 note 的唯一来源：漏标一项，用户改完就得不到
// 任何“要刷新 / 要重启”的提示，且不会有任何编译错误或服务端报错。这里把当前的三档归属固化下来，
// 顺带确认派生清单与标注一致——它一旦与注册表分叉，页面就会对错项的提示
func TestApplyAtDeclaredForDeferredKeys(t *testing.T) {
	want := map[string]ApplyAt{
		"concurrency":    ApplyRestart,
		"language":       ApplyRestart,
		"log_level":      ApplyRestart,
		"theme":          ApplyReload,
		"theme_dark":     ApplyReload,
		"theme_light":    ApplyReload,
		"notify_timeout": ApplyReload,
		"font_ui":        ApplyReload,
		"font_mono":      ApplyReload,
	}
	got := map[string]ApplyAt{}
	for _, s := range Settings() {
		if s.ApplyAt != "" {
			got[s.Key] = s.ApplyAt
		}
	}
	for key, at := range want {
		if got[key] != at {
			t.Errorf("%q 的 ApplyAt 应为 %q，实得 %q", key, at, got[key])
		}
	}
	if len(got) != len(want) {
		t.Errorf("显式标注 ApplyAt 的项有 %d 个，预期 %d 个：%v", len(got), len(want), got)
	}

	reload := KeysWithApplyAt(ApplyReload)
	if len(reload) != 6 {
		t.Errorf("ApplyReload 档应有 6 项，实得 %d：%v", len(reload), reload)
	}
	// concurrency 也属于重启档：它在启动时被装进 worker 的共享信号量，
	// 运行中改配置换不掉那份信号量（见 worker.Init）
	restart := KeysWithApplyAt(ApplyRestart)
	if strings.Join(restart, ",") != "concurrency,language,log_level" {
		t.Errorf("ApplyRestart 档应为 concurrency、language 与 log_level（按注册表顺序），实得 %v", restart)
	}
}

// TestSettingsViewMarksInvalidValue 断言文件里的坏值会被标出来，而且原值不丢。
//
// 这是“面板看不出文件是坏的”那类问题的根源：坏值此前被换成默认值交给页面，页面上它与
// “从没改过”一模一样，于是“恢复默认”按钮隐藏、坏值永远提交不上去。现在默认值仍给控件，
// 但 Raw / Invalid / Error 三个字段把“文件里原本写的是什么、为什么不行”一并交出去
func TestSettingsViewMarksInvalidValue(t *testing.T) {
	path := newViewConfigPath(t)
	writeRawConfigForView(t, path, `{"log_level":"trace","size_unit":"binary"}`)

	for _, v := range mustSettingsView(t, path) {
		switch v.Key {
		case "log_level":
			if !v.Invalid {
				t.Error("log_level 的取值非法，应标记 invalid")
			}
			if v.Raw != "trace" {
				t.Errorf("应原样给出文件里的取值 trace，实得 %q", v.Raw)
			}
			if v.Error == "" {
				t.Error("非法项必须给出错误说明，否则页面只能显示一句“这一项不对”")
			}
			if v.Value != "warn" {
				t.Errorf("控件仍应拿到默认值 warn，实得 %q", v.Value)
			}
		case "size_unit":
			if v.Invalid || v.Error != "" {
				t.Errorf("合法项不该被标成非法：%+v", v)
			}
			if v.Raw != "binary" {
				t.Errorf("合法项也应给出文件里的原值，实得 %q", v.Raw)
			}
		}
	}
}

// TestSettingsViewReportsUnreadableFile 断言整份文件读不出来时，视图照样逐项给出，
// 同时把读取错误单独返回——面板要能同时做两件事：照常画出配置表、说清文件坏在哪
func TestSettingsViewReportsUnreadableFile(t *testing.T) {
	path := newViewConfigPath(t)
	writeRawConfigForView(t, path, `{"log_level":`)

	views, err := SettingsViewAt(path)
	if err == nil {
		t.Fatal("语法错误的文件应返回读取错误")
	}
	if len(views) != len(Settings()) {
		t.Errorf("读不出文件时仍应逐项给出视图，实得 %d 项", len(views))
	}
	for _, v := range views {
		if v.Invalid {
			t.Errorf("%q 被标成非法：读不出整份文件时没有“这一项坏了”可言", v.Key)
		}
		if v.Value != v.Default {
			t.Errorf("%q 在文件读不出来时应显示默认值，实得 %q", v.Key, v.Value)
		}
	}
}

// TestSettingsViewExplainsEmptyThemePreference 断言“文件里写着空串”的下拉项有一个说得清的名字。
//
// theme_dark / theme_light 的候选里没有空值项，而文件里可以出现空串（含义是回落到内置默认）。
// 不把它补成候选，下拉框会显示成第一套主题——用户看到的配置并非文件里的那个值
func TestSettingsViewExplainsEmptyThemePreference(t *testing.T) {
	path := newViewConfigPath(t)
	writeRawConfigForView(t, path, `{"theme_dark":""}`)

	for _, v := range mustSettingsView(t, path) {
		if v.Key != "theme_dark" {
			continue
		}
		if v.Invalid {
			t.Fatal("空串是合法取值，不该标成非法")
		}
		if v.Value != "" {
			t.Errorf("生效值应为空串，实得 %q", v.Value)
		}
		for _, o := range v.Options {
			if o.Value == "" {
				if o.Label == "" {
					t.Error("补进候选的空值项应有说明，否则下拉框里是一个空白选项")
				}
				return
			}
		}
		t.Fatal("候选里应补上空值项，否则下拉框会显示成第一套主题")
	}
	t.Fatal("视图里没有 theme_dark 这一项")
}

// TestPlanUnsetAt 覆盖“能不能把这个键从文件里删掉”的四类情形：
// 普通项、受管项（取值合法时拒绝、非法时放行）、文件里存在的未知键、文件里不存在的未知键
func TestPlanUnsetAt(t *testing.T) {
	path := newViewConfigPath(t)

	// 普通项：键不在文件里也允许（删除是幂等的）
	plan, err := PlanUnsetAt(path, "log_level")
	if err != nil || plan.Key != "log_level" || !plan.Known {
		t.Fatalf("普通项应允许删除，实得 %+v / %v", plan, err)
	}

	// 受管项取值合法：拒绝，并说出该用哪个命令
	if err := SetKeyAt(path, "repo_paths", []any{"/tmp"}); err != nil {
		t.Fatalf("预置失败：%v", err)
	}
	if _, err := PlanUnsetAt(path, "repo_paths"); err == nil || !strings.Contains(err.Error(), "ggt repo") {
		t.Errorf("受管项取值合法时应拒绝删除，实得 %v", err)
	}

	// 受管项取值非法：放行一次，并标记这是例外
	if err := SetKeyAt(path, "repo_paths", []any{""}); err != nil {
		t.Fatalf("预置失败：%v", err)
	}
	plan, err = PlanUnsetAt(path, "repo_paths")
	if err != nil {
		t.Fatalf("受管项的取值非法时应放行删除：%v", err)
	}
	if !plan.Managed {
		t.Error("应标记这次删除是“受管项的例外放行”，否则调用方无法解释")
	}

	// 文件里确实写着的未知键：允许删除（否则拼错一个键名就只剩手改文件）
	if err := SetKeyAt(path, "typo_key", "1"); err != nil {
		t.Fatalf("预置失败：%v", err)
	}
	plan, err = PlanUnsetAt(path, " TYPO_KEY ")
	if err != nil {
		t.Fatalf("文件里存在的未知键应允许删除：%v", err)
	}
	if plan.Known || plan.Key != "typo_key" {
		t.Errorf("未知键的计划应带上归一后的键名且标为未知，实得 %+v", plan)
	}

	// 文件里没有的未知键：报 ErrUnknownKey，拼写错误当场暴露
	if _, err := PlanUnsetAt(path, "no_such_key"); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("文件里不存在的未知键应返回 ErrUnknownKey，实得 %v", err)
	}
}

// writeRawConfigForView 直接往配置文件落一份内容，绕过写入校验，
// 用来模拟“用户自己动手编辑配置文件”这种情形
func writeRawConfigForView(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("建配置目录失败：%v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("写配置文件失败：%v", err)
	}
}
