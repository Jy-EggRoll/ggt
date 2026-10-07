package cmd

import (
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jy-eggroll/eggokit/theme"
)

// TestUIFallbacksMatchTokens 把“同一个数字在 app.js 与 style.css 里各写一份”的那几处对起来。
//
// app.js 用 cssVar('--x', 回退值) 读布局令牌：取值要参与列高、行高与卡片高度的算术，
// 因此除了读计算样式还必须带一个回退值——样式尚未结算、或某个令牌被删掉时，
// 布局不至于拿到 NaN 而整块塌掉。
//
// 回退值与样式表里的取值本该是同一个数，而它已经错过一次：--statusbar-h 在 JS 里是 30、
// 在样式表里是 32，列高因此多算 2px，最后一行卡片正好被底栏压住一条边（肉眼看只是“少了一行”）。
// 这类错不报任何异常，只在特定视口高度下少显示一行，所以由这条测试守住。
//
// 时长令牌（motionMs）同样在这里校：它错了不会塌布局，症状是动画时长与样式表不一致，
// 而 Web Animations API 的 duration 只接受数字，所以它也必须带回退值。两类令牌分别到样式表的
// “数字 + px”与“数字 + ms”声明里找校对对象，各看各的。
//
// 读的是 //go:embed 打进二进制的那份资产，而不是磁盘上的路径：要校验的应当是真正发出去的东西
func TestUIFallbacksMatchTokens(t *testing.T) {
	css := readUIAsset(t, "ui/style.css")
	appJS := readUIAsset(t, "ui/app.js")

	// 只认整条取值就是“数字 + px”的令牌：合成色、var() 引用、百分比与毫秒那些不参与布局算术，
	// 也就没有回退值可比
	declared := map[string]float64{}
	decl := regexp.MustCompile(`(?m)^\s*(--[a-z0-9-]+)\s*:\s*(\d+(?:\.\d+)?)px\s*;`)
	for _, m := range decl.FindAllStringSubmatch(css, -1) {
		v, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			t.Fatalf("解析 %s 的取值 %q 出错：%v", m[1], m[2], err)
		}
		declared[m[1]] = v
	}
	if len(declared) == 0 {
		t.Fatal("ui/style.css 里一个 px 令牌都没解析出来，说明这条测试的解析方式已经与样式表对不上")
	}

	// 时长令牌另收一表。毫秒不参与布局算术，但它和 px 一样是“两边各写一份”的数，
	// 只是错开之后看得见的结果不同：列高塌掉 vs 动画时长与样式表不一致
	durations := map[string]float64{}
	durDecl := regexp.MustCompile(`(?m)^\s*(--[a-z0-9-]+)\s*:\s*(\d+(?:\.\d+)?)ms\s*;`)
	for _, m := range durDecl.FindAllStringSubmatch(css, -1) {
		v, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			t.Fatalf("解析 %s 的取值 %q 出错：%v", m[1], m[2], err)
		}
		durations[m[1]] = v
	}
	if len(durations) == 0 {
		t.Fatal("ui/style.css 里一个毫秒令牌都没解析出来，说明这条测试的解析方式已经与样式表对不上")
	}

	// 两类读取器各按字面量令牌名匹配。motionMs 内部是用变量名转调 cssVar 的，
	// 因此不会被上面那条正则收进来，同一个回退值不会在两个表里各校一遍
	calls := regexp.MustCompile(`cssVar\('(--[a-z0-9-]+)',\s*(\d+(?:\.\d+)?)\)`).FindAllStringSubmatch(appJS, -1)
	if len(calls) == 0 {
		t.Fatal("ui/app.js 里一个 cssVar 调用都没解析出来，说明这条测试的解析方式已经与页面脚本对不上")
	}
	for _, m := range calls {
		fallback, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			t.Fatalf("解析 %s 的回退值 %q 出错：%v", m[1], m[2], err)
		}
		got, ok := declared[m[1]]
		if !ok {
			t.Errorf("ui/app.js 读的 %s 在 ui/style.css 里没有“数字 + px”的声明，回退值 %v 无从校对", m[1], fallback)
			continue
		}
		if got != fallback {
			t.Errorf("%s 在 ui/style.css 里是 %vpx，而 ui/app.js 的回退值是 %vpx：两处不一致时列高会少算一截，"+
				"症状是最后一行卡片被底栏压住", m[1], got, fallback)
		}
	}

	motionCalls := regexp.MustCompile(`motionMs\('(--[a-z0-9-]+)',\s*(\d+(?:\.\d+)?)\)`).FindAllStringSubmatch(appJS, -1)
	if len(motionCalls) == 0 {
		t.Fatal("ui/app.js 里一个 motionMs 调用都没解析出来，说明这条测试的解析方式已经与页面脚本对不上")
	}
	for _, m := range motionCalls {
		fallback, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			t.Fatalf("解析 %s 的回退值 %q 出错：%v", m[1], m[2], err)
		}
		got, ok := durations[m[1]]
		if !ok {
			t.Errorf("ui/app.js 读的 %s 在 ui/style.css 里没有“数字 + ms”的声明，回退值 %v 无从校对", m[1], fallback)
			continue
		}
		if got != fallback {
			t.Errorf("%s 在 ui/style.css 里是 %vms，而 ui/app.js 的回退值是 %vms：两处不一致时，"+
				"读不到计算样式的那一瞬动画时长会与样式表不同", m[1], got, fallback)
		}
	}
}

// readUIAsset 读一份随二进制分发的页面资产
func readUIAsset(t *testing.T, path string) string {
	t.Helper()
	b, err := fs.ReadFile(uiAssets, path)
	if err != nil {
		t.Fatalf("读不到页面资产 %s：%v", path, err)
	}
	return string(b)
}

// TestSelectionTokenReachesBothEnds 断言选区底色这条令牌两端都接上了：样式表里有规则读它，
// 渲染出来的主题配色里有它的取值。
//
// 两端各缺一端的症状不一样，而两处都不报任何异常：样式表里没人读它，映射表里那条
// editor.selectionBackground 就是一次白映射；注入的配色里没有它，::selection 就成了一个
// 没有值的变量，退回浏览器默认那层半透明蓝——深色主题下压在暗底上几乎看不出选区在哪
func TestSelectionTokenReachesBothEnds(t *testing.T) {
	if css := readUIAsset(t, "ui/style.css"); !strings.Contains(css, "var(--selection-bg)") {
		t.Error("ui/style.css 里没有读 --selection-bg 的规则，主题映射表里的 editor.selectionBackground 无人读取")
	}

	// 隔离配置：渲染首页要读配置文件与主题，不能碰开发者自己的那一份
	t.Setenv("HOME", t.TempDir())
	out := string(renderIndexHTML([]byte(readUIAsset(t, "ui/index.html")), "zh-CN"))
	// 只要求“变量带着一个颜色值到了页面上”，不锁定具体色值：色值由主题决定，
	// 内置的几套主题各自定义了这一项（例如 2026 Dark 是 #276782dd），
	// 缺这一项的主题才由 defaults.json 的上游默认值补上
	values := regexp.MustCompile(`--selection-bg:([^;}]+)`).FindAllStringSubmatch(out, -1)
	if len(values) == 0 {
		t.Fatal("渲染后的首页里没有 --selection-bg 的取值，::selection 会退回浏览器默认那层半透明蓝")
	}
	for _, m := range values {
		if !strings.HasPrefix(m[1], "#") && !strings.HasPrefix(m[1], "rgb") {
			t.Errorf("--selection-bg 的取值不像一个颜色：%q", m[1])
		}
	}
}

// TestControlBorderMeetsNonTextContrast 断言每一套内置主题渲染出来的输入控件边界都达到 3:1。
//
// 输入框、下拉框、多行框的边界在样式表里共用 --input-border，取值由 themeBlock 兜出来：上游在
// 普通主题里根本不画这条边（inputColors.ts 里 input.border 的 dark 与 light 都是 null，只有高
// 对比度主题才有值），深色下 dropdown.border 又等于它自己的底色，于是只能靠这一层兜底。兜底
// 失效不报任何异常，症状是输入框看不出边界——改动前 12 套主题实测全部低于 1.4:1，最差 1.08:1
//
// 判定前先把半透明色合成到页面底色上：把半透明当不透明算，得出的是偏乐观的错值
func TestControlBorderMeetsNonTextContrast(t *testing.T) {
	themes := theme.Available(nil)
	if len(themes) == 0 {
		t.Fatal("一套内置主题都没列出来，说明内置主题的打包或列举方式已经变了")
	}

	// WCAG 2.2 的 1.4.11 对图形与控件边界要求 3:1，比正文文字的 4.5:1 低
	const want = 3.0

	parse := func(css, name string) string {
		m := regexp.MustCompile(`--` + regexp.QuoteMeta(name) + `:([^;}]+)`).FindStringSubmatch(css)
		if m == nil {
			return ""
		}
		return strings.TrimSpace(m[1])
	}

	for _, th := range themes {
		css := systemThemeCSS(th.ID, th.ID)
		page := parse(css, "bg")
		on := func(v string) string {
			if page == "" {
				return v
			}
			return theme.Over(v, page)
		}

		border := on(parse(css, "input-border"))
		if border == "" {
			t.Errorf("主题 %s 渲染后没有 --input-border：输入框、下拉框与多行框会回落到给装饰用的 "+
				"card-border，那个浓度标不出“此处能输入”", th.ID)
			continue
		}
		surfaces := make([]string, 0, 2)
		for _, name := range []string{"card-bg", "panel-bg"} {
			if v := parse(css, name); v != "" {
				surfaces = append(surfaces, on(v))
			}
		}
		if len(surfaces) == 0 {
			t.Fatalf("主题 %s 渲染后既没有 --card-bg 也没有 --panel-bg，这条断言失去判定对象", th.ID)
		}
		worst := 0.0
		for _, s := range surfaces {
			got := theme.Contrast(border, s)
			if got < want {
				t.Errorf("主题 %s 的输入控件边界 %s 压在 %s 上只有 %.2f:1，达不到 %.1f:1",
					th.ID, border, s, got, want)
			}
			if worst == 0 || got < worst {
				worst = got
			}
		}
		// 量到的值也写进日志：这条断言只判“过没过线”，而人想知道它究竟被调成了什么色
		t.Logf("%s 的输入控件边界调成 %s，最差 %.2f:1", th.ID, border, worst)
	}
}
