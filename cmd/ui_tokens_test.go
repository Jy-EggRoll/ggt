package cmd

import (
	"io/fs"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jy-eggroll/eggokit/fileicon"
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

// TestBadgeTextMeetsContrast 断言每一套内置主题里，各枚徽章的文字都在它实际落着的底色上读得出来，
// 文件类型图标也在它实际落着的行底色上读得出来。
//
// 这条断言针对的是一类“算不到”的缺陷，而不是算错了：三枚变体徽章的底色曾经在样式表里用
// color-mix 现算，对比度校验表只认识 badge-bg，混出来的三种底色从来没被看过一眼，于是
// Catppuccin Latte 下工作树 2.00:1、子模块 2.45:1、领先 3.10:1 全部漏了过去；错误徽章的文字
// 直接引用 status-error，12 套主题全部不达标（最差 1.14:1）；文件类型图标的颜色由图标包内联
// 写死，12 套主题共用一个值，实测只有 1.38-1.59:1。
//
// 这三处现在都由 themeBlock 兜底（见 applyBadgeMixFixes / applyFileIconFix），本条断言守住结果。
// 判据与真实渲染一致：徽章文字与文件图标都是“压在某种底色上”，底色要先按自身透明度合成到
// 它落着的表面上；而徽章与图标出现的地方不止一种表面（卡片底、状态行三档淡染、hover 叠色），
// 每一种都要判——混色底已经是不透明色，这一层合成对它们是空操作，对 badge-bg 这类半透明底色
// 则是必需的
func TestBadgeTextMeetsContrast(t *testing.T) {
	themes := theme.Available(nil)
	if len(themes) == 0 {
		t.Fatal("一套内置主题都没列出来，说明内置主题的打包或列举方式已经变了")
	}

	// 徽章里的字是正文，按 WCAG 2.2 的 1.4.3 取 4.5:1；文件类型图标不是文字，
	// 按 1.4.11 取 3:1
	const wantText = theme.MinContrast
	const wantIcon = 3.0

	parse := func(css, name string) string {
		m := regexp.MustCompile(`--` + regexp.QuoteMeta(name) + `:([^;}]+)`).FindStringSubmatch(css)
		if m == nil {
			return ""
		}
		return strings.TrimSpace(m[1])
	}

	for _, th := range themes {
		css := systemThemeCSS(th.ID, th.ID)

		// 徽章与图标落着的表面，两类元素分开收集，因为它们出现的地方不同：
		//
		// 徽章只出现在 .row.head（仓库头行），而 .row 自己不铺底色、行是透明的，
		// 所以徽章背后就是卡片底 --card-bg；工作树头行再叠 6% 的 --text-dim，鼠标掠过叠 --hover-bg
		//
		// 徽章只出现在 .row.head（仓库头行），而 .row 自己不铺底色、行是透明的，
		// 所以徽章背后就是卡片底 --card-bg；工作树头行再叠 6% 的 --text-dim，鼠标掠过叠 --hover-bg
		//
		// 图标那几种底色不在这里拼：由 fileIconSurfaces 算，生产代码用的是同一个函数。
		// 这里曾各自抄过一份，两份对不上也没有任何症状——旧断言只查「兜底后的值够不够」，
		// 兜多兜少它看不出来
		badgeSurfaces := make([]string, 0, 6)
		card := parse(css, "card-bg")
		if card != "" {
			badgeSurfaces = append(badgeSurfaces, card)
			// 工作树头行的淡染：color-mix(text-dim 6%, transparent) 叠在卡片底上
			if dim := parse(css, "text-dim"); dim != "" {
				badgeSurfaces = append(badgeSurfaces, theme.Over(scaleAlpha(dim, 0.06), card))
			}
		}
		iconSurfaces := fileIconSurfaces(func(name string) string { return parse(css, name) })
		if len(badgeSurfaces) == 0 || len(iconSurfaces) == 0 {
			t.Fatalf("主题 %s 渲染后取不到卡片底色，这条断言失去判定对象", th.ID)
		}
		// hover 是叠在底色之上的一层，徽章那几种底色各算一次（图标那几种已在 fileIconSurfaces 里叠过）
		addHover := func(list []string) []string {
			hover := parse(css, "hover-bg")
			if hover == "" {
				return list
			}
			out := append([]string{}, list...)
			for _, s := range list {
				out = append(out, theme.Over(hover, s))
			}
			return out
		}
		badgeSurfaces = addHover(badgeSurfaces)

		check := func(label, fgVar string, bg string, surfaces []string, min float64) {
			fg := parse(css, fgVar)
			if fg == "" {
				t.Errorf("主题 %s 渲染后没有 --%s：%s 会回落到别处的颜色，对比度不再受这条断言约束",
					th.ID, fgVar, label)
				return
			}
			if bg == "" {
				t.Errorf("主题 %s 渲染后没有这枚徽章的底色，%s 的判定失去对象", th.ID, label)
				return
			}
			// 底色先合成到每一种表面上：不透明的底色（三枚混色底）合成后就是它自己，
			// 这一步是空操作；半透明底色（badge-bg 在某些主题里）则必须逐个表面判
			backgrounds := make([]string, 0, len(surfaces))
			for _, s := range surfaces {
				backgrounds = append(backgrounds, theme.Over(bg, s))
			}
			worst, worstBg := math.MaxFloat64, ""
			for _, b := range backgrounds {
				got := theme.Contrast(fg, b)
				if got < worst {
					worst, worstBg = got, b
				}
			}
			if worst < min {
				t.Errorf("主题 %s 的%s %s 压在 %s 上只有 %.2f:1，达不到 %.1f:1",
					th.ID, label, fg, worstBg, worst, min)
			}
			t.Logf("%s 的%s %s 最差 %.2f:1（压在 %s 上）", th.ID, label, fg, worst, worstBg)
		}

		badgeBg := parse(css, "badge-bg")
		check("徽章文字", "badge-fg", badgeBg, badgeSurfaces, wantText)
		check("错误徽章文字", "badge-err-fg", badgeBg, badgeSurfaces, wantText)
		for _, m := range badgeSurfaceMixes {
			check(m.name+" 上的徽章文字", "badge-fg", parse(css, m.name), badgeSurfaces, wantText)
		}
		// 文件图标：Go 侧按图标包给的色号逐档判，只把读不出的那几档写成变量。
		// 这里要一起断言两件事——读不出的必须兜到 3:1；读得出的必须**没有**变量。
		// 后半件同样重要：整类覆盖过一次，结果是 go 的蓝、js 的黄、png 的紫全变成同一种灰
		palette := fileicon.Palette()
		if len(palette) == 0 {
			t.Fatalf("主题 %s：拿不到图标色号清单，图标兜底没有判定对象", th.ID)
		}
		adjusted := 0
		for _, color := range palette {
			slot := strings.ToLower(strings.TrimPrefix(color, "#"))
			fixed := parse(css, fileIconVarPrefix+slot)

			worst, worstBg := math.MaxFloat64, ""
			for _, s := range iconSurfaces {
				if got := theme.Contrast(color, s); got < worst {
					worst, worstBg = got, s
				}
			}
			if worst >= wantIcon {
				if fixed != "" {
					t.Errorf("主题 %s：图标色 %s 本来就有 %.2f:1，却仍被改成 %s（读得出的类型色不该被动）",
						th.ID, color, worst, fixed)
				}
				continue
			}
			if fixed == "" {
				t.Errorf("主题 %s：图标色 %s 压在 %s 上只有 %.2f:1，却没有兜底变量",
					th.ID, color, worstBg, worst)
				continue
			}
			adjusted++
			got, gotBg := math.MaxFloat64, ""
			for _, s := range iconSurfaces {
				if r := theme.Contrast(fixed, s); r < got {
					got, gotBg = r, s
				}
			}
			if got < wantIcon {
				t.Errorf("主题 %s：图标色 %s 兜成了 %s，压在 %s 上仍只有 %.2f:1",
					th.ID, color, fixed, gotBg, got)
			}
			// 覆盖规则也必须真的发出去，且引用的是同一枚变量：规则拼错时页面不会报错，
			// 只是颜色没兜住，所以这条断言是这套机制唯一的哨兵
			rule := ".seti-" + slot + "{color:var(--" + fileIconVarPrefix + slot + ")!important}"
			if !strings.Contains(css, rule) {
				t.Errorf("主题 %s：缺少图标覆盖规则 %s", th.ID, rule)
			}
		}
		t.Logf("%s：%d 档图标色里兜了 %d 档", th.ID, len(palette), adjusted)
	}
}
