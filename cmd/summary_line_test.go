// summary_line_test.go 覆盖 remote 与 owned 的收尾汇总：被打断时换措辞、换样式，
// 未被打断时与改动前逐字一致。
//
// 与 sync 的同类用例分开：sync 的收尾已由 sync_test.go 覆盖，这里只补另外两个命令，
// 免得同一段断言写三遍。remote 在本机可以完整跑通（离线也需要一个可解析的 origin 地址），
// 所以走命令入口验证接线；owned 只在 Windows 上执行，本机走不到真实路径，
// 只能直接驱动它的选词函数与共用样式函数
package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/jy-eggroll/ggt/internal/config"
	"github.com/jy-eggroll/ggt/internal/locales"
)

// TestRemoteClosingLineFollowsInterruption 断言 remote 的收尾跟着中断状态换文案与样式。
//
// 验证者实测过一次：600 个仓库跑到第 128 个被 SIGINT，收尾仍是“处理完成”，
// 屏幕上看不出这次没跑完。被打断时 gather 交回来的只有尝试过的仓库，因此这一行
// 必须换措辞，并且用告警样式才够醒目；未被打断时保持原句与原样式
func TestRemoteClosingLineFollowsInterruption(t *testing.T) {
	initL10n(t, locales.Default)

	// --all 绑在包级变量上，命令解析后会留下 true，用完复位免得漏给别的用例
	t.Cleanup(func() { remoteAll = false })

	repo := initGitRepoWithFile(t, "alpha.txt")
	addGitRemote(t, repo, "https://github.com/example/repo.git")
	withConfig(t, &config.Config{Concurrency: "2", RepoPaths: []string{repo}})

	cases := []struct {
		name         string
		interrupt    bool
		marker       string
		wantStyle    string
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:         "未被打断：结尾仍是原来的完成措辞与信息样式",
			marker:       "Finished:",
			wantStyle:    "INFO",
			wantContains: []string{"Finished:", "1 switched"},
			wantAbsent:   []string{"Interrupted"},
		},
		{
			name:         "被打断：结尾改用中断措辞与告警样式",
			interrupt:    true,
			marker:       "Interrupted",
			wantStyle:    "WARNING",
			wantContains: []string{"Interrupted", "only the repositories already attempted"},
			wantAbsent:   []string{"Finished:"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if c.interrupt {
				// 与 Execute 里的 signal.NotifyContext 收到 Ctrl-C 等效：
				// gather 不再启动新任务，交给命令的结果只剩零条
				cancel()
			}

			buf := capturePtermOutput(t)

			remoteCmd := newRemoteCmd()
			remoteCmd.SetArgs([]string{"ssh", "--all"})
			remoteCmd.SetContext(ctx)
			if err := remoteCmd.Execute(); err != nil {
				t.Fatalf("执行 remote 失败：%v", err)
			}

			printed := buf.String()
			for _, want := range c.wantContains {
				if !strings.Contains(printed, want) {
					t.Errorf("输出缺少 %q，实际输出：%q", want, printed)
				}
			}
			for _, absent := range c.wantAbsent {
				if strings.Contains(printed, absent) {
					t.Errorf("输出不应包含 %q，实际输出：%q", absent, printed)
				}
			}

			line := findOutputLine(printed, c.marker)
			if line == "" {
				t.Fatalf("找不到收尾那一行（marker %q），实际输出：%q", c.marker, printed)
			}
			if !strings.Contains(line, c.wantStyle) {
				t.Errorf("收尾那一行应为 %s 样式，实际为：%q", c.wantStyle, line)
			}
		})
	}
}

// TestOwnedClosingLineFollowsInterruption 断言 owned 的收尾同样跟着中断状态换文案与样式。
//
// 本机不是 Windows，命令会在开头就提示“仅支持 Windows”然后返回，走不到收尾，
// 因此这里直接驱动选词函数与共用样式函数——两者合起来就是收尾的全部逻辑
func TestOwnedClosingLineFollowsInterruption(t *testing.T) {
	cases := []struct {
		name         string
		interrupted  bool
		marker       string
		wantStyle    string
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:         "未被打断：结尾仍是原来的完成措辞与信息样式",
			marker:       "Finished:",
			wantStyle:    "INFO",
			wantContains: []string{"Finished:", "5 succeeded", "1 failed"},
			wantAbsent:   []string{"Interrupted"},
		},
		{
			name:         "被打断：结尾改用中断措辞与告警样式",
			interrupted:  true,
			marker:       "Interrupted",
			wantStyle:    "WARNING",
			wantContains: []string{"Interrupted", "5 succeeded", "only the repositories already attempted"},
			wantAbsent:   []string{"Finished:"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			initL10n(t, locales.Default)
			buf := capturePtermOutput(t)

			SummaryMsg(ownedSummaryLine(c.interrupted, 5, 1), c.interrupted)

			printed := buf.String()
			for _, want := range c.wantContains {
				if !strings.Contains(printed, want) {
					t.Errorf("输出缺少 %q，实际输出：%q", want, printed)
				}
			}
			for _, absent := range c.wantAbsent {
				if strings.Contains(printed, absent) {
					t.Errorf("输出不应包含 %q，实际输出：%q", absent, printed)
				}
			}

			line := findOutputLine(printed, c.marker)
			if line == "" {
				t.Fatalf("找不到收尾那一行（marker %q），实际输出：%q", c.marker, printed)
			}
			if !strings.Contains(line, c.wantStyle) {
				t.Errorf("收尾那一行应为 %s 样式，实际为：%q", c.wantStyle, line)
			}
		})
	}
}
