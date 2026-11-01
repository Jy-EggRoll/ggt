package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// CommandError 是 git 以非零状态退出时的错误，带出 git 写在 stderr 上的原话。
//
// 两个输出封装原先只把 *exec.ExitError 往上交，调用方拿到的文案永远只有
// "exit status 128"；而“不是 git 仓库”“safe.directory 拒绝”“网络不可达”这些
// 真正需要使用者行动的原因全在 stderr 里，被无声丢掉了。
type CommandError struct {
	// Stderr 是 git 的原始标准错误，保留未压缩的内容供调用方按需查看。
	Stderr string
	// ExitCode 是 git 的退出码；进程被信号终止时为 -1。
	ExitCode int
	// Err 是底层错误（通常是 *exec.ExitError），供 errors.Is/As 判断。
	Err error
}

// Error 返回单行描述：退出码 + stderr 的首个非空行。
//
// 只取首行是因为 git 的多行 stderr 后半段通常是 hint/advice，重复的是同一个原因；
// 整段塞进一行日志既会挤掉上下文，也让这类提示更难读。
// stderr 为空时退回原错误文本，ctx 取消或超时导致的终止因此仍显示为终止本身，
// 而不是伪装成 git 的语法错误。
func (e *CommandError) Error() string {
	if reason := firstLine(e.Stderr); reason != "" {
		return fmt.Sprintf("%v: %s", e.Err, reason)
	}
	return e.Err.Error()
}

// Unwrap 让 errors.Is/As 能穿透到 *exec.ExitError 等原始错误。
func (e *CommandError) Unwrap() error { return e.Err }

// firstLine 返回 s 中第一个非空行并去掉首尾空白，空串表示没有任何可用的原因。
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// newCommandError 把命令失败包装成 CommandError。
//
// ctx 已经结束时原样返回底层错误：此时 stderr 通常为空，但被杀之前也可能
// 已经写了半行，那段残缺输出会把“超时/取消”说成 git 的报错，而调用方
// 真正需要知道的是命令被终止了。
func newCommandError(ctx context.Context, stderr string, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return err
	}
	exitCode := -1
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode = exitErr.ExitCode()
	}
	return &CommandError{Stderr: stderr, ExitCode: exitCode, Err: err}
}

// stderrOf 取出 cmd.Output() 收集到的 stderr。Output 只在 cmd.Stderr 为 nil 时
// 才把它放进 ExitError，因此调用方不要自行设置 cmd.Stderr。
func stderrOf(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return string(exitErr.Stderr)
	}
	return ""
}
