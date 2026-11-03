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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

// TestFilesOutputFileNotWrittenWhenInterruptedMidway 覆盖真正的故障形态：
// 一部分仓库已经列出文件、ctx 才被取消，手里因此是一份非空但缺条的清单。
//
// 上面那条用例在启动命令之前就取消 ctx，gather 一个仓库都没开始，只能固定“没有 0 字节
// 文件被创建”。这里让第一个仓库真的跑完，再靠受控的假 git 让第二次调用卡住：用例看到
// 它卡住才取消，于是 gather 交回的结果非空（一条成功、一条失败），落盘守卫要拦的正是
// 这份半成品。取消时机不靠 sleep 猜，靠假 git 落下的标记文件；取消之后卡住的那个进程
// 由 git 包按 ctx 杀掉，因此不会留下等待中的子进程。
//
// 并发固定为 1：调用顺序因此确定，否则哪个仓库先跑取决于 goroutine 抢信号量的次序
func TestFilesOutputFileNotWrittenWhenInterruptedMidway(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("假 git 用 POSIX shell 脚本")
	}
	initL10n(t, locales.Default)
	t.Cleanup(func() { outputFile = "" })

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("环境里没有 git，跳过")
	}

	repos := []string{
		initGitRepoWithFile(t, "alpha.txt"),
		initGitRepoWithFile(t, "beta.txt"),
		initGitRepoWithFile(t, "gamma.txt"),
	}
	withConfig(t, &config.Config{Concurrency: "1", RepoPaths: repos})

	// 假 git 第一次被调用时记下工作目录与参数、然后执行真 git；之后每次调用都先落下
	// blocked 标记再阻塞，于是“第一个仓库已经列出文件”与“第二个仓库才开始”这两件事
	// 都不会被时序搅乱
	stateDir := t.TempDir()
	shimDir := writeFakeGitScript(t, `#!/bin/sh
state="$GGT_FAKE_GIT_STATE"
if [ -e "$state/block" ]; then
	: > "$state/blocked"
	exec sleep 300
fi
: > "$state/block"
printf '%s %s\n' "$(/bin/pwd)" "$*" >> "$state/calls"
exec "$GGT_FAKE_GIT_REAL" "$@"
`)
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GGT_FAKE_GIT_REAL", realGit)
	t.Setenv("GGT_FAKE_GIT_STATE", stateDir)

	out := filepath.Join(t.TempDir(), "files.txt")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	buf := capturePtermOutput(t)

	filesCmd := newFilesCmd()
	filesCmd.SetArgs([]string{"-o", out})
	filesCmd.SetContext(ctx)

	done := make(chan error, 1)
	go func() { done <- filesCmd.Execute() }()

	waitForPath(t, filepath.Join(stateDir, "blocked"), 10*time.Second)
	cancel()

	select {
	case execErr := <-done:
		if execErr != nil {
			t.Fatalf("执行 files 失败：%v", execErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("取消之后命令没有结束")
	}

	// 先把“确实跑到一半、手里的清单非空”固定下来：假 git 只被真正执行过一次，那次是
	// 某个仓库的 ls-files 且成功了（记录里有该仓库的工作目录），而每个临时仓库都至少
	// 有一个已提交的文件，因此那一条的清单一定不为空
	calls, err := os.ReadFile(filepath.Join(stateDir, "calls"))
	if err != nil {
		t.Fatalf("读取假 git 调用记录失败：%v", err)
	}
	if lines := strings.Count(string(calls), "\n"); lines != 1 || !strings.Contains(string(calls), "ls-files") {
		t.Fatalf("期望假 git 只被真正执行过一次、且参数是 ls-files，实际调用记录：%q", calls)
	}
	listedRepo := ""
	if fields := strings.Fields(string(calls)); len(fields) > 0 {
		// 比较前解析符号链接：临时目录在部分平台上要经过一层链接，直接比字符串会漏
		if listedDir, evalErr := filepath.EvalSymlinks(fields[0]); evalErr == nil {
			for _, repo := range repos {
				if resolved, repoErr := filepath.EvalSymlinks(repo); repoErr == nil && resolved == listedDir {
					listedRepo = repo
				}
			}
		}
	}
	if listedRepo == "" {
		t.Fatalf("认不出成功列出文件的是哪个仓库，调用记录：%q", calls)
	}

	// 这条用例存在的理由：拼好的清单非空，守卫必须仍然拒绝落盘
	if content, statErr := os.ReadFile(out); statErr == nil {
		t.Fatalf("被打断时不应写出 %s，实际写入了 %d 字节：%q", out, len(content), content)
	}

	printed := buf.String()
	if !strings.Contains(printed, "Interrupted") {
		t.Errorf("终端输出缺少中断告警，实际输出：%q", printed)
	}
	// 第二个仓库的 git 是被取消杀掉的，它会以“获取文件列表失败”出现，
	// 这也是 gather 交回的结果里既有成功又有失败的直接证据
	if !strings.Contains(printed, "failed to list files") {
		t.Errorf("期望看到被取消那个仓库的失败提示，实际输出：%q", printed)
	}
}
