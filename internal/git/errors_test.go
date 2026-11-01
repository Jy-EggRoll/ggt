package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode"
)

// TestCommandError_MultilineStderr 验证多行 stderr 只取首个非空行，结果保持单行。
//
// 为什么必须单行：调用方把 Error() 直接拼进一行告警，git 的 hint/advice
// 一并带进来会把这条日志撑成多行，反而盖住真正的退出码与原因。
func TestCommandError_MultilineStderr(t *testing.T) {
	err := &CommandError{
		Stderr: "\n fatal: not a git repository (or any of the parent directories): .git \nhint: see 'git help' for details\n",
		Err:    errors.New("exit status 128"),
	}

	got := err.Error()
	want := "exit status 128: fatal: not a git repository (or any of the parent directories): .git"
	if got != want {
		t.Fatalf("Error() = %q，期望 %q", got, want)
	}
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("Error() 必须是单行，实得 %q", got)
	}
}

// TestCommandError_InlineCarriageReturn 验证行内 \r 也被当作断行，且输出不含控制字符。
//
// git 的进度输出用 \r 回到行首覆写；只按 \n 分行会把“error: bad object”与
// 进度文字粘在同一行里，终端上还会用后半段覆盖前半段。
func TestCommandError_InlineCarriageReturn(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
		want   string
	}{
		{name: "行内回车", stderr: "error: bad object\rReceiving objects: 50%\n", want: "error: bad object"},
		{name: "其他 C0 控制字符", stderr: "fatal: bo\x07om\n", want: "fatal: boom"},
		{name: "CRLF", stderr: "fatal: boom\r\nhint: x\r\n", want: "fatal: boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := (&CommandError{Stderr: tc.stderr, Err: errors.New("exit status 128")}).Error()
			if want := "exit status 128: " + tc.want; got != want {
				t.Fatalf("Error() = %q，期望 %q", got, want)
			}
			for _, r := range got {
				if unicode.IsControl(r) {
					t.Fatalf("Error() 含控制字符 %q：%q", r, got)
				}
			}
		})
	}
}

// TestCommandError_EmptyStderr 验证 stderr 为空时退回底层错误文本，不凭空造原因。
func TestCommandError_EmptyStderr(t *testing.T) {
	base := errors.New("signal: killed")
	err := &CommandError{Stderr: "  \n\t\n", Err: base}
	if got, want := err.Error(), "signal: killed"; got != want {
		t.Fatalf("Error() = %q，期望 %q", got, want)
	}
}

// TestCommandError_Unwrap 验证 errors.Is/As 能取回原始错误。
func TestCommandError_Unwrap(t *testing.T) {
	base := errors.New("exit status 128")
	var err error = &CommandError{Stderr: "fatal: boom\n", Err: base}
	if !errors.Is(err, base) {
		t.Fatalf("errors.Is 无法取回原始错误：%v", err)
	}
	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) || cmdErr.Stderr != "fatal: boom\n" {
		t.Fatalf("errors.As 未取回 CommandError：%#v", err)
	}
}

// TestNewCommandError_CancelledContext 验证 ctx 已结束时回报的是取消本身，
// 而不是被杀前写了一半的 stderr。
func TestNewCommandError_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	base := errors.New("signal: killed")
	got := newCommandError(ctx, "fatal: half-written", base)

	if !errors.Is(got, context.Canceled) {
		t.Errorf("errors.Is(got, context.Canceled) 为 false：%v", got)
	}
	if !errors.Is(got, base) {
		t.Errorf("errors.Is 无法取回底层错误：%v", got)
	}
	if strings.Contains(got.Error(), "half-written") {
		t.Errorf("不应把残缺 stderr 当作失败原因：%q", got.Error())
	}
	var cmdErr *CommandError
	if errors.As(got, &cmdErr) {
		t.Errorf("ctx 已结束时不应返回 CommandError：%#v", got)
	}
}

// TestNewCommandError_DeadlineMentionsTimeout 验证超时会在告警里说清是超时。
// exec 对被杀进程只给 "signal: killed"，不带上 ctx 的原因，用户根本看不出是超时。
func TestNewCommandError_DeadlineMentionsTimeout(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	got := newCommandError(ctx, "", errors.New("signal: killed"))
	if !strings.Contains(got.Error(), "context deadline exceeded") {
		t.Fatalf("Error() = %q，期望含 context deadline exceeded", got.Error())
	}
	if !errors.Is(got, context.DeadlineExceeded) {
		t.Errorf("errors.Is(got, context.DeadlineExceeded) 为 false：%v", got)
	}
}

// TestCappedBuffer_StopsAtLimit 验证 stderr 缓冲到上限就停止累积，且写入不报错、不阻塞。
func TestCappedBuffer_StopsAtLimit(t *testing.T) {
	b := &cappedBuffer{limit: 8}
	if n, err := b.Write([]byte("0123456789")); n != 10 || err != nil {
		t.Fatalf("Write 应接受全部字节，实得 n=%d err=%v", n, err)
	}
	if got := b.String(); got != "01234567" {
		t.Fatalf("超限后只应保留前 8 字节，实得 %q", got)
	}
	// 超限后的写入必须照常返回全部长度：os/exec 的拷贝 goroutine 靠它继续排空管道
	if n, err := b.Write([]byte("more")); n != 4 || err != nil {
		t.Fatalf("超限后的 Write 应返回 n=4 err=nil，实得 n=%d err=%v", n, err)
	}
	if got := b.String(); got != "01234567" {
		t.Fatalf("超限后不应再累积，实得 %q", got)
	}
}

// TestRun_ErrorCarriesStderr 验证 runWithOutputEnv 把 cmd.Output() 收进 ExitError 的
// stderr 带进了错误里，而不只是一个退出码。
func TestRun_ErrorCarriesStderr(t *testing.T) {
	repo := newTestRepo(t)
	_, err := Run(repo, "rev-parse", "--verify", "refs/heads/definitely-not-a-branch")
	if err == nil {
		t.Fatal("期望 rev-parse 对不存在的分支返回错误")
	}
	assertCommandError(t, err, "fatal:")
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Errorf("errors.As 未能穿透到 *exec.ExitError：%v", err)
	}
}

// TestRunWithRecordLimit_ErrorCarriesStderr 验证流式读取那条路径也接了 stderr。
// 它不走 cmd.Output()，少接一次缓冲就会退化为只有退出码。
func TestRunWithRecordLimit_ErrorCarriesStderr(t *testing.T) {
	repo := newTestRepo(t)
	_, _, err := runWithRecordLimit(context.Background(), repo, nil, 0,
		"rev-parse", "--verify", "refs/heads/definitely-not-a-branch")
	if err == nil {
		t.Fatal("期望 rev-parse 对不存在的分支返回错误")
	}
	assertCommandError(t, err, "fatal:")
}

// TestRunContext_CancelledMentionsContext 是取消语义的端到端检查：
// 用户看到的必须是“被取消”，不能是信号名或 git 的报错。
func TestRunContext_CancelledMentionsContext(t *testing.T) {
	repo := newTestRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := RunContext(ctx, repo, "status")
	if err == nil {
		t.Fatal("期望已取消的 ctx 返回错误")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("Error() = %q，期望含 context canceled", err.Error())
	}
}

// assertCommandError 断言 err 是带 stderr 原话的 CommandError，且 Error() 可读为单行。
func assertCommandError(t *testing.T, err error, wantSubstr string) {
	t.Helper()
	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) {
		t.Fatalf("期望 *CommandError，实得 %T: %v", err, err)
	}
	if !strings.Contains(cmdErr.Stderr, wantSubstr) {
		t.Errorf("Stderr = %q，期望包含 %q", cmdErr.Stderr, wantSubstr)
	}
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Errorf("Error() = %q，期望包含 %q", err.Error(), wantSubstr)
	}
	if strings.ContainsAny(err.Error(), "\r\n") {
		t.Errorf("Error() 必须是单行，实得 %q", err.Error())
	}
}

// TestRunContext_TimeoutMentionsDeadline 用假 git 验证超时的端到端文案。
// 真 git 没有可控的慢命令，而 exec 对被杀进程只给 "signal: killed"，
// 用户看不出这是超时。
func TestRunContext_TimeoutMentionsDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("假 git 用 POSIX shell 脚本")
	}
	dir := writeFakeGit(t, "#!/bin/sh\nexec sleep 5\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := RunContext(ctx, dir, "status")
	if err == nil {
		t.Fatal("期望超时返回错误")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("Error() = %q，期望含 context deadline exceeded", err.Error())
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("超时后应立即返回，实际耗时 %v", elapsed)
	}
}

// TestRunWithRecordLimit_StderrCapped 用假 git 灌入远超上限的 stderr，
// 验证缓冲停在 stderrLimit，且子进程不会卡在写满的管道上。
func TestRunWithRecordLimit_StderrCapped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("假 git 用 POSIX shell 脚本")
	}
	dir := writeFakeGit(t, "#!/bin/sh\ni=0\nwhile [ $i -lt 64 ]; do\n\tprintf '%01024d' 1 >&2\n\ti=$((i+1))\ndone\nexit 1\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, _, err := runWithRecordLimit(context.Background(), dir, nil, 0, "status")
	if err == nil {
		t.Fatal("期望假 git 非零退出返回错误")
	}
	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) {
		t.Fatalf("期望 *CommandError，实得 %T: %v", err, err)
	}
	if got := len(cmdErr.Stderr); got != stderrLimit {
		t.Errorf("Stderr 长度 = %d，期望上限 %d", got, stderrLimit)
	}
}

// writeFakeGit 在临时目录里写入名为 git 的脚本并返回目录，供 PATH 注入使用。
func writeFakeGit(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatalf("写入假 git 失败: %v", err)
	}
	return dir
}
