// files_test.go 覆盖 `ggt files -o` 的落盘时机：被打断时不写文件，未被打断时照常写。
//
// 两种结局都不能只靠读代码确认——前者的关键证据是“文件确实不存在”，后者的关键证据是
// “文件内容确实是完整的清单”。所以这里走完整命令：造真实仓库、装配真实命令对象、
// 用 context 模拟 Ctrl-C（Execute 交给各命令的正是这样一个会被取消的 ctx），
// 再去检查磁盘与终端输出。替身绕过 AllRepos 的话，仓库发现到输出这一段就没被覆盖。
package cmd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/ggt/internal/config"
	"github.com/jy-eggroll/ggt/internal/locales"
	"github.com/pterm/pterm"
)

// TestFilesOutputFileFollowsInterruption 断言中断与正常两条路径的文件落盘行为。
//
// files 与 sync 的中断行为共用 initGitRepoWithFile 造的真实仓库：两个命令的输入都来自
// AllRepos，只有真的走仓库发现、子模块展开这些步骤，测试才和用户实际跑的那条路径一致。
func TestFilesOutputFileFollowsInterruption(t *testing.T) {
	initEnglishL10n(t)

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
			name:         "未被打断：照常写出文件，提示文案与改动前一致",
			wantFile:     true,
			wantContains: []string{"File list written to"},
			wantAbsent:   []string{"Interrupted"},
		},
		{
			name:         "被打断：不落盘，并说明没写文件与清单不完整",
			interrupt:    true,
			wantFile:     false,
			wantContains: []string{"Interrupted", "incomplete"},
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

			var buf bytes.Buffer
			pterm.SetDefaultOutput(&buf)
			t.Cleanup(func() { pterm.SetDefaultOutput(os.Stdout) })

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

// initEnglishL10n 把全局语言固定成英文源串。
//
// 断言的是英文文案，而同包其它用例会切到中文；显式初始化一遍，免得测试结果取决于
// 谁先跑。t.Cleanup 里再初始化一次是给后续用例留下英文这个已知状态
func initEnglishL10n(t *testing.T) {
	t.Helper()
	if err := l10n.Init(locales.Default, locales.Options()); err != nil {
		t.Fatalf("初始化 l10n 失败: %v", err)
	}
	t.Cleanup(func() {
		if err := l10n.Init(locales.Default, locales.Options()); err != nil {
			t.Fatalf("还原 l10n 失败: %v", err)
		}
	})
}

// initGitRepoWithFile 造一个只有一次提交的临时仓库，返回仓库路径。
//
// 用真实 git 而不是伪造目录：仓库发现、子模块展开与 git ls-files 都是被测路径的一部分，
// 假目录会在第一步就被判成“不是仓库”而静默跳过。
// 提交人的身份只在命令环境里给，不动使用者的 git 配置（TestMain 已把全局配置指向空文件）
func initGitRepoWithFile(t *testing.T, name string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("环境里没有 git，跳过")
	}

	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=ggt", "GIT_AUTHOR_EMAIL=ggt@example.com",
			"GIT_COMMITTER_NAME=ggt", "GIT_COMMITTER_EMAIL=ggt@example.com",
		)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v 失败：%v（%s）", args, err, out)
		}
	}

	git("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, name), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("写入 %s 失败：%v", name, err)
	}
	git("add", "-A")
	git("commit", "-qm", "init")
	return dir
}
