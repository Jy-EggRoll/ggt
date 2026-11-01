package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/ggt/internal/locales"
	"github.com/pterm/pterm"
)

// TestDecideSyncAction 用表驱动覆盖 sync 命令的全部决策分支。
//
// 抽成纯函数之前，这段判定写在 syncRepo 里，与 git 调用、输出着色混在一起，
// 只能靠人手动造四个不同的仓库状态去验证，于是长期是 0% 覆盖。
// 现在它不接触 git 也不产生输出，"本地/远程/共同祖先"三者的相等关系正好穷尽四种结果。
func TestDecideSyncAction(t *testing.T) {
	// 三个互不相同的假 commit hash。特意不用 a/b/c 这种单字符，
	// 以免有人误以为它们在比较“顺序”或“大小”——这里只比较相等关系
	const (
		older  = "1111111"
		middle = "2222222"
		newer  = "3333333"
	)

	cases := []struct {
		name   string
		local  string
		remote string
		base   string
		want   syncAction
	}{
		{
			name:   "三者相同：已是最新",
			local:  middle,
			remote: middle,
			base:   middle,
			want:   syncUpToDate,
		},
		{
			name:   "本地与远程相同：已是最新（判定优先于共同祖先）",
			local:  middle,
			remote: middle,
			base:   older,
			want:   syncUpToDate,
		},
		{
			name:   "本地等于共同祖先：线性落后，可快进",
			local:  older,
			remote: newer,
			base:   older,
			want:   syncFastForward,
		},
		{
			name:   "远程等于共同祖先：本地领先，只能手动推送",
			local:  newer,
			remote: older,
			base:   older,
			want:   syncAhead,
		},
		{
			name:   "三者互不相等：历史分叉，必须人工处理",
			local:  older,
			remote: newer,
			base:   middle,
			want:   syncDiverged,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decideSyncAction(c.local, c.remote, c.base); got != c.want {
				t.Errorf("decideSyncAction(%q, %q, %q) = %v, want %v",
					c.local, c.remote, c.base, got, c.want)
			}
		})
	}
}

// TestDecideSyncActionNeverAutoMerges 是一条语义防线，而不是行为测试：
// 只有“本地严格落后”这一种情形允许 ggt 自动动仓库，其余一律交回用户。
//
// 之所以单独立一条：判断顺序一旦被改动（例如把 remote == base 提到 local == base 之前），
// 分叉或领先的仓库就可能被误判成可快进而被自动 pull，那是会丢改动的操作。
func TestDecideSyncActionNeverAutoMerges(t *testing.T) {
	const (
		a = "aaaaaaa"
		b = "bbbbbbb"
		c = "ccccccc"
	)

	// 穷举三个 hash 两两相等/不相等组合中所有“非本地落后”的情形
	for _, tc := range [][3]string{
		{a, a, a}, // 一致
		{a, a, b}, // 本地与远程一致
		{b, a, a}, // 本地领先
		{a, b, c}, // 分叉
	} {
		if got := decideSyncAction(tc[0], tc[1], tc[2]); got == syncFastForward {
			t.Errorf("decideSyncAction(%q, %q, %q) 误判为可快进", tc[0], tc[1], tc[2])
		}
	}
}

// TestSummarizeSyncResults 用表驱动覆盖四类结局的计数。
//
// 收尾那行汇总的数字全从这里出，属于用户直接看到的结果：一旦某类被漏计，
// 屏幕上就会出现“3 个仓库需要手动处理、汇总却写 2”这种自相矛盾的收尾。
func TestSummarizeSyncResults(t *testing.T) {
	cases := []struct {
		name    string
		results []syncResult
		want    syncCounts
	}{
		{
			name:    "无仓库：全部为零",
			results: nil,
			want:    syncCounts{},
		},
		{
			name: "四类各一个",
			results: []syncResult{
				{name: "a", outcome: outcomePulled},
				{name: "b", outcome: outcomeUpToDate},
				{name: "c", outcome: outcomeManual},
				{name: "d", outcome: outcomeFailed},
			},
			want: syncCounts{pulled: 1, upToDate: 1, manual: 1, failed: 1},
		},
		{
			name: "同类多仓库分别累计",
			results: []syncResult{
				{name: "a", outcome: outcomePulled},
				{name: "b", outcome: outcomePulled},
				{name: "c", outcome: outcomeUpToDate},
				{name: "d", outcome: outcomeFailed},
				{name: "e", outcome: outcomeFailed},
			},
			want: syncCounts{pulled: 2, upToDate: 1, manual: 0, failed: 2},
		},
		{
			name: "顺序不影响计数",
			results: []syncResult{
				{name: "a", outcome: outcomeFailed},
				{name: "b", outcome: outcomeUpToDate},
				{name: "c", outcome: outcomePulled},
			},
			want: syncCounts{pulled: 1, upToDate: 1, manual: 0, failed: 1},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := summarizeSyncResults(c.results); got != c.want {
				t.Errorf("summarizeSyncResults() = %+v, want %+v", got, c.want)
			}
		})
	}
}

// syncResultsFixture 返回一份四类结局齐全的结果，供清单子集的测试共用。
// 顺序刻意打乱：清单必须跟随 results 的顺序，而不是按结局重新分组
func syncResultsFixture() []syncResult {
	return []syncResult{
		{name: "pulled-a", path: "/repos/a", outcome: outcomePulled},
		{name: "up-to-date", path: "/repos/b", outcome: outcomeUpToDate},
		{name: "manual", path: "/repos/c", outcome: outcomeManual, manualHint: "push manually"},
		{name: "failed", path: "/repos/d", outcome: outcomeFailed, manualHint: "check the network"},
		{name: "pulled-b", path: "/repos/e", outcome: outcomePulled},
	}
}

// TestIsUpdatedOnlyPulled 表驱动确认“已更新”判定只认 outcomePulled。
//
// 判定本身抽成具名方法后，它与调用点引用同一个符号：谁把方法放宽，这里立刻红；
// 而 TestPickSyncResultsUpdatedOnlyPulled 只负责锁“挑选时确实用了这个判定”
func TestIsUpdatedOnlyPulled(t *testing.T) {
	for _, c := range []struct {
		outcome syncOutcome
		want    bool
	}{
		{outcomePulled, true},
		{outcomeUpToDate, false},
		{outcomeManual, false},
		{outcomeFailed, false},
	} {
		if got := c.outcome.isUpdated(); got != c.want {
			t.Errorf("路径 %d 的 isUpdated() = %v, want %v", c.outcome, got, c.want)
		}
	}
}

// TestPickSyncResultsUpdatedOnlyPulled 锁住“已更新清单只收 outcomePulled”这条规则。
//
// 这条规则决定清单能不能一眼回答“哪些仓库真的动了”：混进 outcomeUpToDate 会让清单
// 退化成“所有仓库的名字”，混进 manual / failed 则会与紧随其后的手动清单重复。
func TestPickSyncResultsUpdatedOnlyPulled(t *testing.T) {
	updated := pickSyncResults(syncResultsFixture(), syncOutcome.isUpdated)

	for _, r := range updated {
		if !r.outcome.isUpdated() {
			t.Errorf("已更新清单里混进了 %d 的仓库 %q", r.outcome, r.name)
		}
	}
	if len(updated) != 2 {
		t.Fatalf("已更新清单长度 = %d, want 2（只有两个 outcomePulled）", len(updated))
	}
	if updated[0].name != "pulled-a" || updated[1].name != "pulled-b" {
		t.Errorf("已更新清单顺序 = [%q %q], want [pulled-a pulled-b]", updated[0].name, updated[1].name)
	}
}

// TestPickSyncResultsManualKeepsFailures 确保手动清单仍然同时收“需人工决策”与“某步失败”。
//
// 失败也要求用户停下手上的事去查网络或权限，两条清单的成员规则就此分家过一次：
// 若把这里收窄成只认 outcomeManual，失败仓库既不在成功清单也不在手动清单里，
// 用户只能回上面翻逐仓明细，而收尾行却还报着“有失败”
func TestPickSyncResultsManualKeepsFailures(t *testing.T) {
	manual := pickSyncResults(syncResultsFixture(), syncOutcome.needsManual)

	got := make([]string, 0, len(manual))
	for _, r := range manual {
		got = append(got, r.name)
	}
	want := []string{"manual", "failed"}
	if len(got) != len(want) {
		t.Fatalf("手动清单 = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("手动清单 = %v, want %v", got, want)
		}
	}
}

// TestPrintSyncList 覆盖清单打印的两条用户可见规则：空清单整段静默，
// 处理建议只跟随有建议的条目。
//
// printSyncList 直接写 pterm，要断言就得接管 pterm 的全局输出。本包其余测试都不碰
// 这个全局值，因此换到缓冲区、结束后恢复成 os.Stdout 即为还原默认状态。
// 标题由调用方传入的消息函数打印（决定黄/绿），测试用记录调用次数的替身即可判断
// “空清单连标题都不打”，不必断言标题的着色
func TestPrintSyncList(t *testing.T) {
	defer pterm.SetDefaultOutput(os.Stdout)

	// [sub] 前缀由 RepoLabel 经 l10n 拼出，译文随全局语言变化。显式初始化成英文，
	// 免得同包其它测试切到中文后这里断言失败（那会变成顺序相关的偶发红灯）
	if err := l10n.Init(locales.Default, locales.Options()); err != nil {
		t.Fatalf("初始化 l10n 失败: %v", err)
	}

	cases := []struct {
		name         string
		results      []syncResult
		wantHeading  bool
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:        "空清单：连标题都不打，也不留空行",
			results:     nil,
			wantHeading: false,
		},
		{
			name:         "已拉取成功：只有名称与路径",
			results:      []syncResult{{name: "repo-a", path: "/repos/a", outcome: outcomePulled}},
			wantHeading:  true,
			wantContains: []string{"[repo-a]", "/repos/a"},
			wantAbsent:   []string{"->"},
		},
		{
			name: "需要手动处理：建议另起一行跟在条目之后",
			results: []syncResult{
				{name: "repo-b", path: "/repos/b", outcome: outcomeManual, manualHint: "push manually"},
			},
			wantHeading:  true,
			wantContains: []string{"[repo-b]", "/repos/b", "-> push manually"},
		},
		{
			// 清单曾用 RepoName 而非 RepoLabel，子模块在明细里是 [sub] sub/a、在清单里
			// 却退化成 [sub/a]，同一批仓库出现两种写法。这条用例把两处的一致性固定下来
			name: "子模块：清单与逐仓库明细同样带 [sub] 前缀",
			results: []syncResult{
				{name: "sub/a", path: "/repos/a/sub/a", outcome: outcomePulled, isSubmodule: true},
			},
			wantHeading:  true,
			wantContains: []string{"[sub] sub/a", "/repos/a/sub/a"},
			wantAbsent:   []string{"[sub/a]"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			pterm.SetDefaultOutput(&buf)

			headingCalls := 0
			printSyncList("TITLE", c.results, func(string) { headingCalls++ })

			if got := headingCalls > 0; got != c.wantHeading {
				t.Errorf("是否打印标题 = %v, want %v", got, c.wantHeading)
			}
			out := buf.String()
			if !c.wantHeading && out != "" {
				t.Errorf("空清单不应产生任何输出，实际输出：%q", out)
			}
			for _, want := range c.wantContains {
				if !strings.Contains(out, want) {
					t.Errorf("输出缺少 %q，实际输出：%q", want, out)
				}
			}
			for _, absent := range c.wantAbsent {
				if strings.Contains(out, absent) {
					t.Errorf("输出不应包含 %q，实际输出：%q", absent, out)
				}
			}
		})
	}
}
