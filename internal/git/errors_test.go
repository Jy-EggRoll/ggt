package git

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// TestCommandError_MultilineStderr 验证多行 stderr 只取首个非空行，结果保持单行。
//
// 为什么必须单行：调用方把 Error() 直接拼进一行告警，git 的 hint/advice
// 一并带进来会把这条日志撑成多行，反而盖住真正的退出码与原因。
func TestCommandError_MultilineStderr(t *testing.T) {
	base := errors.New("exit status 128")
	err := &CommandError{
		Stderr:   "\n fatal: not a git repository (or any of the parent directories): .git \nhint: see 'git help' for details\n",
		ExitCode: 128,
		Err:      base,
	}

	got := err.Error()
	want := "exit status 128: fatal: not a git repository (or any of the parent directories): .git"
	if got != want {
		t.Fatalf("Error() = %q，期望 %q", got, want)
	}
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("Error() 必须是单行，实得 %q", got)
	}
	if err.ExitCode != 128 {
		t.Errorf("ExitCode = %d，期望 128", err.ExitCode)
	}
}

// TestCommandError_EmptyStderr 验证 stderr 为空时退回底层错误文本。
// 典型场景是 ctx 超时杀进程：此时报错必须仍然是终止本身，不能伪装成 git 的语法错误。
func TestCommandError_EmptyStderr(t *testing.T) {
	base := errors.New("signal: killed")
	err := &CommandError{Stderr: "  \n\t\n", ExitCode: -1, Err: base}
	if got, want := err.Error(), "signal: killed"; got != want {
		t.Fatalf("Error() = %q，期望 %q", got, want)
	}
}

// TestCommandError_Unwrap 验证 errors.Is/As 能取回原始错误。
func TestCommandError_Unwrap(t *testing.T) {
	base := errors.New("exit status 128")
	var err error = &CommandError{Stderr: "fatal: boom\n", ExitCode: 128, Err: base}
	if !errors.Is(err, base) {
		t.Fatalf("errors.Is 无法取回原始错误：%v", err)
	}
	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) || cmdErr.Stderr != "fatal: boom\n" {
		t.Fatalf("errors.As 未取回 CommandError：%#v", err)
	}
}

// TestNewCommandError_CancelledContext 验证 ctx 已结束时不做包装，避免半行 stderr
// 把“被终止”说成 git 的报错。
func TestNewCommandError_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	base := errors.New("signal: killed")
	got := newCommandError(ctx, "fatal: half-written", base)
	if got != base {
		t.Fatalf("期望原样返回底层错误，实得 %#v", got)
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
