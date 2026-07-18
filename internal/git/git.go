// git 包提供统一的 git 命令执行封装，确保所有 git 调用
// 拥有一致的超时控制、环境变量和错误处理。
package git

import (
	"context"
	"os"
	"os/exec"
	"time"
)

// 默认超时时间：120 秒。网络操作（fetch/push）需要较长等待，
// 本地操作（status/rev-parse）也能在超时前完成。
const defaultTimeout = 120 * time.Second

// Run 在指定仓库路径下执行 git 命令，返回标准输出。
// 参数 repoPath 为仓库根目录，args 为 git 子命令及其参数。
// 调用方只需关心字符串输出，无需处理 Context 和超时；
// 内部使用默认 120s 超时，即使上层无 ctx 也能自我保护。
func Run(repoPath string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	return runWithOutput(ctx, repoPath, args...)
}

// RunContext 与 Run 相同，但使用调用方传入的 ctx 控制超时与取消。
// 当上层 ctx 被取消（如 worker.Map 的并发整体取消）时，正在执行的
// git 命令会立即收到信号而中断，避免无谓等待到默认 120s 超时。
// 若上层 ctx 未设截止时间，命令仍受默认超时兜底保护。
func RunContext(ctx context.Context, repoPath string, args ...string) (string, error) {
	// 仅当上层 ctx 未设置截止时间时，叠加默认超时作为兜底
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}
	return runWithOutput(ctx, repoPath, args...)
}

// RunCombined 与 Run 相同，但返回标准输出+标准错误的合并结果。
// 用于需要捕获 stderr 的场景（如 push 失败时显示具体原因）。
func RunCombined(repoPath string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	return runWithCombinedOutput(ctx, repoPath, args...)
}

// RunCombinedContext 与 RunCombined 相同，但使用调用方传入的 ctx
// 控制超时与取消，语义同 RunContext。
func RunCombinedContext(ctx context.Context, repoPath string, args ...string) (string, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}
	return runWithCombinedOutput(ctx, repoPath, args...)
}

// runWithOutput 执行 git 命令并捕获 stdout。
func runWithOutput(ctx context.Context, repoPath string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoPath
	// GIT_TERMINAL_PROMPT=0 禁止 git 弹出交互式凭据提示，
	// 避免在脚本/批量操作中卡住等待用户输入。
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// runWithCombinedOutput 执行 git 命令并捕获 stdout + stderr。
func runWithCombinedOutput(ctx context.Context, repoPath string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoPath
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	// 即使 err != nil 也返回 output（CombinedOutput 在非零退出时仍返回内容），
	// 让调用方能够看到 stderr 的具体错误信息（如 push 失败原因）。
	return string(output), err
}
