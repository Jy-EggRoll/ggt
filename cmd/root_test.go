// root_test 覆盖根命令这一层的纯函数逻辑：输出封装层的字符串构造（分隔线、仓库名、
// 次要文本）与 gather 的采集/取消语义，都不依赖终端。
package cmd

import (
	"bytes"
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/ggt/internal/config"
	"github.com/jy-eggroll/ggt/internal/locales"
)

// TestBuildSeparator 验证分隔线由 width 个 "─" 组成（ANSI 转义不影响可见字符数）。
func TestBuildSeparator(t *testing.T) {
	cases := []int{0, 1, 10, 80}
	for _, w := range cases {
		got := buildSeparator(w)
		if n := strings.Count(got, "─"); n != w {
			t.Errorf("buildSeparator(%d) 含 %d 个 '─', 期望 %d", w, n, w)
		}
	}
}

// TestRepoName 验证仓库名被青色包裹为 "[name]" 形式且包含原名。
func TestRepoName(t *testing.T) {
	got := RepoName("my-repo")
	if !strings.Contains(got, "[my-repo]") {
		t.Errorf("RepoName 应含 [my-repo], 实际 %q", got)
	}
}

// TestRepoTextLabels 钉住三个纯文本标签函数的文本构成。
//
// 方括号与 [子] 前缀只在这几个函数里拼：凡是要“不带着色的标签”的地方都从这里取，
// 免得各调用点自己拼一遍、慢慢拼出两种写法。顶层仓库在 RepoLabelText 里带方括号，
// 在 RepoInlineText 里是裸名——后者供嵌进句子的场合使用，那些地方原本就没有方括号。
func TestRepoTextLabels(t *testing.T) {
	// [sub] 的译文随全局语言变化，显式初始化成英文，免得同包其它测试切到中文后断言失败
	if err := l10n.Init(locales.Default, locales.Options()); err != nil {
		t.Fatalf("初始化 l10n 失败: %v", err)
	}

	cases := []struct {
		name        string
		isSubmodule bool
		wantName    string
		wantLabel   string
		wantInline  string
	}{
		{name: "my-repo", wantName: "[my-repo]", wantLabel: "[my-repo]", wantInline: "my-repo"},
		{name: "sub/a", isSubmodule: true, wantName: "[sub/a]", wantLabel: "[sub] sub/a", wantInline: "[sub] sub/a"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RepoNameText(c.name); got != c.wantName {
				t.Errorf("RepoNameText = %q, 期望 %q", got, c.wantName)
			}
			if got := RepoLabelText(c.name, c.isSubmodule); got != c.wantLabel {
				t.Errorf("RepoLabelText = %q, 期望 %q", got, c.wantLabel)
			}
			if got := RepoInlineText(c.name, c.isSubmodule); got != c.wantInline {
				t.Errorf("RepoInlineText = %q, 期望 %q", got, c.wantInline)
			}

			// 纯文本标签会进 JSON、进日志，一旦混入 ANSI 转义就是页面上的乱码
			for _, s := range []string{RepoNameText(c.name), RepoLabelText(c.name, c.isSubmodule), RepoInlineText(c.name, c.isSubmodule)} {
				if strings.Contains(s, "\x1b") {
					t.Errorf("%q 里出现了 ANSI 转义，纯文本标签不该着色", s)
				}
			}
		})
	}
}

// TestRepoLabelColorWrapsText 验证着色函数只是在纯文本外面套一层颜色，不改动文本本身：
// 去掉 ANSI 转义后必须与 Text 版本逐字节相同。
//
// 这里不断言具体色码，因为 pterm 底层的 gookit/color 在非 TTY 输出（go test 就是）
// 下会自行剥掉色码，断言色码等于把测试绑在运行环境上。色码层面的“逐字节不变”
// 由新旧二进制在同一伪终端下的输出比对来守，那是唯一能同时覆盖两者的场合。
func TestRepoLabelColorWrapsText(t *testing.T) {
	ansi := regexp.MustCompile("\x1b\\[[0-9;]*m")
	cases := []struct {
		name        string
		isSubmodule bool
	}{
		{name: "my-repo"},
		{name: "sub/a", isSubmodule: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ansi.ReplaceAllString(RepoName(c.name), ""); got != RepoNameText(c.name) {
				t.Errorf("RepoName 去掉转义后 = %q, 期望 %q", got, RepoNameText(c.name))
			}
			if got := ansi.ReplaceAllString(RepoLabel(c.name, c.isSubmodule), ""); got != RepoLabelText(c.name, c.isSubmodule) {
				t.Errorf("RepoLabel 去掉转义后 = %q, 期望 %q", got, RepoLabelText(c.name, c.isSubmodule))
			}
		})
	}
}

// TestMuted 验证次要文本被包裹且原文本可见。
func TestMuted(t *testing.T) {
	got := Muted("变动详情：")
	if !strings.Contains(got, "变动详情：") {
		t.Errorf("Muted 应保留原文本, 实际 %q", got)
	}
}

// TestGatherKeepsAllOrdered 验证未取消时 gather 返回全部条目，且顺序与输入一致。
func TestGatherKeepsAllOrdered(t *testing.T) {
	withConfig(t, &config.Config{Concurrency: "3"})
	repos := []RepoEntry{{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}, {Name: "e"}}

	got := gather(context.Background(), "test", repos, func(_ context.Context, e RepoEntry) string {
		return "got:" + e.Name
	})

	if len(got) != len(repos) {
		t.Fatalf("返回 %d 条, 期望 %d 条", len(got), len(repos))
	}
	for i, e := range repos {
		if want := "got:" + e.Name; got[i] != want {
			t.Errorf("第 %d 条 = %q, 期望 %q（顺序应与输入一致）", i, got[i], want)
		}
	}
}

// TestGatherDropsNotStartedAfterCancel 验证取消后没真正进入 fn 的条目不出现在返回值里。
//
// 用 channel 而不是 sleep 来同步：并发度取 1，保证同一时刻只有一个任务在跑；
// 等到第一个任务确实开始后再取消，然后才放它返回。此后排队的任务会在 fn 入口被拦掉，
// 因此返回值必须只剩这一条——这正是“中断时别把没跑过的仓库打成干净/成功”的依据。
func TestGatherDropsNotStartedAfterCancel(t *testing.T) {
	withConfig(t, &config.Config{Concurrency: "1"})
	repos := []RepoEntry{{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan string, len(repos))
	release := make(chan struct{})
	result := make(chan []string, 1)

	go func() {
		result <- gather(ctx, "test", repos, func(_ context.Context, e RepoEntry) string {
			started <- e.Name
			<-release
			return e.Name
		})
	}()

	// 并发度为 1 时谁先抢到信号量由调度器决定，因此从 channel 里取实际开始的那一个，
	// 而不是假定一定是 repos[0]
	first := <-started
	cancel()
	close(release)

	got := <-result
	if len(got) != 1 {
		t.Fatalf("取消后返回 %d 条 %v, 期望只剩已经跑过的 1 条", len(got), got)
	}
	if got[0] != first {
		t.Errorf("返回 %q, 期望是真正跑过的那条 %q", got[0], first)
	}
	select {
	case extra := <-started:
		t.Errorf("取消后又启动了 %q，应当不再启动新任务", extra)
	default:
	}
}

// TestHelpTextConfigPathMatchesDefaultPath 固定“帮助文本里印的配置路径”与
// config.GetDefaultConfigPath() 的一致性。
//
// 这条路径在帮助文案里写了一次，而那段文案同时是 l10n 消息 id 的一部分（改它要连带改语言
// 文件），真正的定义在 GetDefaultConfigPath，两处无法合成一处。只能靠这里比对：任一边先改，
// 测试就红，帮助里才不会长期印着一条已经不存在的路径。
//
// home 取自临时目录，HOME 与 USERPROFILE 同时指向它（os.UserHomeDir 在 Unix 与 Windows
// 上分别读这两个变量），因此既不读使用者真实的主目录，也不要求本机真的存在 ~/.config。
func TestHelpTextConfigPathMatchesDefaultPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// 帮助文本按英文渲染时为消息 id 原文；显式初始化，免得受同包其它测试切换语言的影响
	if err := l10n.Init(locales.Default, locales.Options()); err != nil {
		t.Fatalf("初始化 l10n 失败: %v", err)
	}

	defaultPath := config.GetDefaultConfigPath()
	rel, ok := strings.CutPrefix(defaultPath, home)
	if !ok {
		t.Fatalf("GetDefaultConfigPath() = %q，不以临时主目录 %q 开头", defaultPath, home)
	}
	want := "~" + filepath.ToSlash(rel)

	if help := newRootCmd().Long; !strings.Contains(help, want) {
		t.Errorf("根命令帮助文本未包含 %q，实际为:\n%s", want, help)
	}
}

// TestRootErrorsDoNotPrintUsage 断言错误路径只留错误本身，不附整段用法。
//
// 这里直接跑命令树而不是起子进程：argv 解析、Args 校验与 flag 解析都发生在
// cobra 内部，与本进程内调用完全同一条路径，能跑进 -race 那一轮。
func TestRootErrorsDoNotPrintUsage(t *testing.T) {
	if err := l10n.Init(locales.Default, locales.Options()); err != nil {
		t.Fatalf("初始化 l10n 失败: %v", err)
	}

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"缺参数", []string{"config", "get"}, "accepts 1 arg(s)"},
		{"未知 flag", []string{"--definitely-not-a-flag"}, "unknown flag"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := buildRoot()
			var out, errOut bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&errOut)
			root.SetArgs(c.args)

			if err := root.Execute(); err == nil {
				t.Fatal("这次调用应以错误结束")
			}
			combined := out.String() + errOut.String()
			if strings.Contains(combined, "Usage:") {
				t.Errorf("错误输出不应附整段用法：%q", combined)
			}
			// 具体文案由 cobra 给（进程入口处会再统一加上 ERROR 前缀），
			// 这里只确认错误本身还在，没有被“去掉用法”一起删掉
			if !strings.Contains(combined, c.want) {
				t.Errorf("错误输出应保留错误本身（含 %q）：%q", c.want, combined)
			}
		})
	}
}

// TestRootHelpStillPrintsUsage 断言“不打印用法”只针对错误：主动求助时（--help）
// 用法与命令表照常输出，根命令与子命令都一样。
func TestRootHelpStillPrintsUsage(t *testing.T) {
	if err := l10n.Init(locales.Default, locales.Options()); err != nil {
		t.Fatalf("初始化 l10n 失败: %v", err)
	}

	cases := [][]string{
		{"--help"},
		{"size", "--help"},
		{"config", "get", "--help"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := buildRoot()
			var out, errOut bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&errOut)
			root.SetArgs(args)

			if err := root.Execute(); err != nil {
				t.Fatalf("--help 不应报错: %v", err)
			}
			if !strings.Contains(out.String(), "Usage:") {
				t.Errorf("帮助输出应包含用法：%q", out.String())
			}
		})
	}
}
