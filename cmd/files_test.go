// files_test.go 覆盖 `ggt files -o` 的落盘时机：被打断时不写文件，未被打断时照常写。
//
// 两种结局都不能只靠读代码确认——前者的关键证据是“文件确实不存在”，后者的关键证据是
// “文件内容确实是完整的清单”。所以这里走完整命令：造真实仓库、装配真实命令对象、
// 用 context 模拟 Ctrl-C（Execute 交给各命令的正是这样一个会被取消的 ctx），
// 再去检查磁盘与终端输出。替身绕过 AllRepos 的话，仓库发现到输出这一段就没被覆盖。
package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jy-eggroll/ggt/internal/config"
	"github.com/jy-eggroll/ggt/internal/locales"
)

// TestFilesOutputFileFollowsInterruption 断言中断与正常两条路径的文件落盘行为。
//
// files 与 sync 的中断行为共用 initGitRepoWithFile 造的真实仓库：两个命令的输入都来自
// AllRepos，只有真的走仓库发现、子模块展开这些步骤，测试才和用户实际跑的那条路径一致。
func TestFilesOutputFileFollowsInterruption(t *testing.T) {
	initL10n(t, locales.Default)

	// outputFile 是包级全局量、绑在命令的 -o 上，用例之间会互相污染，用完复位
	t.Cleanup(func() { outputFile = "" })

	repo := initGitRepoWithFile(t, "alpha.txt")
	withConfig(t, &config.Config{Concurrency: "2", RepoPaths: []string{repo}})

	cases := []struct {
		name         string
		interrupt    bool
		wantFile     bool
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:         "未被打断：照常写出文件，提示文案与样式与改动前一致",
			wantFile:     true,
			wantContains: []string{"SUCCESS", "File list written to"},
			wantAbsent:   []string{"Interrupted", "WARNING"},
		},
		{
			name:         "被打断：不落盘，用告警说明没写文件与清单不完整",
			interrupt:    true,
			wantFile:     false,
			wantContains: []string{"WARNING", "Interrupted", "incomplete"},
			wantAbsent:   []string{"File list written to"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "files.txt")

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if c.interrupt {
				// 模拟 Execute 里的 signal.NotifyContext 已经收到 Ctrl-C：gather 因此
				// 不再启动任何 git 调用，交给命令的结果是空的
				cancel()
			}

			buf := capturePtermOutput(t)

			filesCmd := newFilesCmd()
			filesCmd.SetArgs([]string{"-o", out})
			filesCmd.SetContext(ctx)
			if err := filesCmd.Execute(); err != nil {
				t.Fatalf("执行 files 失败：%v", err)
			}

			content, statErr := os.ReadFile(out)
			switch {
			case c.wantFile && statErr != nil:
				t.Fatalf("期望写出 %s，实际读取失败：%v", out, statErr)
			case !c.wantFile && statErr == nil:
				t.Fatalf("被打断时不应写出 %s，实际写出了 %d 字节", out, len(content))
			}

			// 写出时内容要是真正的清单，而不只是一个空文件
			if c.wantFile && !strings.Contains(string(content), filepath.Join(repo, "alpha.txt")) {
				t.Errorf("写出的清单缺少仓库文件路径，实际内容：%q", content)
			}

			printed := buf.String()
			for _, want := range c.wantContains {
				if !strings.Contains(printed, want) {
					t.Errorf("终端输出缺少 %q，实际输出：%q", want, printed)
				}
			}
			for _, absent := range c.wantAbsent {
				if strings.Contains(printed, absent) {
					t.Errorf("终端输出不应包含 %q，实际输出：%q", absent, printed)
				}
			}
			// 告警必须指名到底哪个文件没被写出，否则用户不知道去哪找
			if c.interrupt && !strings.Contains(printed, out) {
				t.Errorf("告警未提到未写出的路径 %q，实际输出：%q", out, printed)
			}
		})
	}
}
