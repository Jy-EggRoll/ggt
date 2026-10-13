package cmd

import (
	"regexp"
	"strings"
	"testing"
)

// TestCardWiringMatchesActionSemantics 把仓库卡片顶栏那几个写按钮的“元素 id → 后端端点”对起来。
//
// 这里出过一次错配：按钮写着“拉取”，接的却是 /api/fetch——只取远程数据、不动工作区。
// 后端一直有 /api/pull（git pull --ff-only），但没有任何按钮调它，界面上因此根本没有
// 真正的拉取入口。这类错配不报任何异常：端点都在、请求也成功、界面照常提示成功，
// 只有用户以为分支已经快进过而实际没有。接错的端点同样不会让其它测试变红，
// 所以由这张表守住。
//
// 表只列这几个按钮，不写成“界面上的按钮只有这些”：其它分支还会往界面加按钮，
// 穷举会让后来者做一次无关改动就撞上失败。表里没列的按钮，这条测试不管。
//
// 读到的是 //go:embed 打进二进制的那份资产，与 TestUIFallbacksMatchTokens 同一套读法：
// 要校验的应当是真正发出去的那份页面
func TestCardWiringMatchesActionSemantics(t *testing.T) {
	want := []struct {
		id       string // ui/index.html 里按钮元素的 id
		endpoint string // 点击后应当发的写端点
		labelKey string // 按钮字样取自哪个文案键
	}{
		{"graph-fetch", "/api/fetch", "fetchRepo"},
		{"graph-pull", "/api/pull", "pullRepo"},
		{"graph-sync", "/api/sync", "pullPush"},
	}

	html := readUIAsset(t, "ui/index.html")
	appJS := readUIAsset(t, "ui/app.js")

	// 页面脚本先用 const xEl = document.getElementById('graph-pull') 拿元素，
	// 点按处理函数挂在这个变量上，所以要先从元素 id 找到变量名
	vars := map[string]string{}
	consts := regexp.MustCompile(`const\s+(\w+)\s*=\s*document\.getElementById\('([^']+)'\)`).FindAllStringSubmatch(appJS, -1)
	if len(consts) == 0 {
		t.Fatal("ui/app.js 里一条 document.getElementById 都没解析出来，说明这条测试的解析方式已经与页面脚本对不上")
	}
	for _, m := range consts {
		vars[m[2]] = m[1]
	}

	// 再接两步：这个变量点了发哪个端点、字样取自哪个键
	endpoints := map[string][]string{}
	for _, m := range regexp.MustCompile(`(\w+)\.addEventListener\('click',\s*\(\)\s*=>\s*cardWrite\('([^']+)'`).FindAllStringSubmatch(appJS, -1) {
		endpoints[m[1]] = append(endpoints[m[1]], m[2])
	}
	labels := map[string]string{}
	for _, m := range regexp.MustCompile(`(\w+)\.textContent\s*=\s*t\('([^']+)'\)`).FindAllStringSubmatch(appJS, -1) {
		labels[m[1]] = m[2]
	}

	for _, w := range want {
		if !strings.Contains(html, `id="`+w.id+`"`) {
			t.Errorf("ui/index.html 里没有 id 为 %s 的按钮，ui/app.js 里那句取元素会拿到空值", w.id)
			continue
		}
		v, ok := vars[w.id]
		if !ok {
			t.Errorf("ui/app.js 里没有读 %s 的接线，这个按钮按下去不会有任何动作", w.id)
			continue
		}
		got := endpoints[v]
		if len(got) != 1 || got[0] != w.endpoint {
			t.Errorf("%s 应当只接 %s，实际接的是 %v：接错端点不会报任何异常，请求照样成功、"+
				"界面照样提示成功，只是做的事与按钮说的不是一回事", w.id, w.endpoint, got)
		}
		// 字样这一半同样要校：端点没动、字样被换成另一个动作时，用户按下去做的仍是老动作
		if labels[v] != w.labelKey {
			t.Errorf("%s 的字样应当取自 %s，实际取自 %q", w.id, w.labelKey, labels[v])
		}
	}
}
