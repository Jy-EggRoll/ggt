// git 包提供统一的 git 命令执行封装，确保所有 git 调用
// 拥有一致的超时控制、环境变量和错误处理；
// status.go 在此之上提供工作区状态的机器可读采集与解析（终端与 WebUI 共用同一份）。
package git

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// IsRepo 判断指定路径是否是一个有效的 git 仓库（存在 .git 目录）。
//
// 判定要求 .git 是**目录**。子模块的 .git 往往是指向父仓库 .git/modules/xxx 的
// gitdir 文件，那种情况由子模块发现逻辑单独处理（见 cmd/repos.go）。
//
// 本函数被运行期的仓库发现与 ggt config validate 的体检共用。两处若各写一套口径，
// 就会出现"validate 说不是仓库、ggt 却能跑"这类自相矛盾的提示。
func IsRepo(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".git"))
	if err != nil {
		return false
	}
	return info.IsDir()
}

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
// 若上层 ctx 未设截止时间，命令仍受默认超时保护。
func RunContext(ctx context.Context, repoPath string, args ...string) (string, error) {
	// 仅当上层 ctx 未设置截止时间时，叠加默认超时
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
	return runWithOutputEnv(ctx, repoPath, nil, args...)
}

// runWithOutputEnv 与 runWithOutput 相同，但可追加额外环境变量（形如 "KEY=VALUE"）。
//
// 存在的意义是让特定调用在通用环境之外再收紧行为，典型是 RunStatus 需要
// GIT_OPTIONAL_LOCKS=0 来禁止 git status 写 index。把追加项做成参数、而不是让各调用点
// 自行拼 cmd.Env，是为了保住"所有 git 调用共享同一套基础环境"这条前提——
// 一旦有人绕过本函数，GIT_TERMINAL_PROMPT=0 这类防止卡死的设置就会漏掉。
func runWithOutputEnv(ctx context.Context, repoPath string, extraEnv []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoPath
	// GIT_TERMINAL_PROMPT=0 禁止 git 弹出交互式凭据提示，
	// 避免在脚本/批量操作中卡住等待用户输入。
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0"), extraEnv...)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// runWithRecordLimit 执行 git 命令，边读边数输出里的 NUL 记录，一旦记录数超过 limit 就
// 立刻杀掉子进程，只返回已经读到的那部分内容（截到完整记录为止），并报告本次是否因为
// 超限而提前终止。
//
// 为什么需要它：git status 遇到海量未跟踪文件时（典型是忘了写 .gitignore 的 node_modules）
// 输出可达几十 MB，而 runWithOutputEnv 走的是 cmd.Output()，会把整份输出一次性读进内存，
// 没有任何上限。上游 VSCode 对同一问题采用的做法就是流式解析加超限杀进程（git.ts:2784-2793，
// 默认 10000 条，limit 取 0 表示不限制），并把已经解析到的部分照常返回、把 didHitLimit
// 一路报到界面。本函数对齐的就是这套语义。
//
// 计数口径有一处刻意的简化：这里数的是 NUL 记录数，而不是像上游那样数解析之后的条目数。
// 重命名与复制（porcelain v2 的 "2" 记录）在 -z 下会多带一条旧路径记录，于是这类仓库的
// 条目数略少于记录数，截断点会比上游稍早一点。对“防止内存被撑爆”这个目的而言可以接受；
// 真要精确到条目，就得在这里内联一份解析逻辑，那等于把 ParseStatus 的解析逻辑重复写一遍。
//
// 与上游的另一处差别：上游按 limit 条做切片，这里按 limit 条记录裁掉多余的读取块——
// 一个 32KB 的读取块可能一次装进上千条短记录，不裁的话返回的条目数会明显多于上限，
// 界面上显示的数量就和“上限”这个说法对不上了。
func runWithRecordLimit(ctx context.Context, repoPath string, extraEnv []string, limit int, args ...string) (string, bool, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoPath
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0"), extraEnv...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", false, err
	}
	if err := cmd.Start(); err != nil {
		return "", false, err
	}

	var buf []byte
	records := 0
	hitLimit := false
	chunk := make([]byte, 32*1024)
	for {
		n, readErr := stdout.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			records += bytes.Count(chunk[:n], []byte{0})
			// 超限即杀：继续读完只会把正要防的那份内存读进来
			if limit > 0 && records > limit {
				hitLimit = true
				_ = cmd.Process.Kill()
				break
			}
		}
		if readErr != nil {
			break
		}
	}

	// 杀进程之后 Wait 返回的是信号错误，那正是本函数想要的路径，不能当失败上报；
	// 只有“没超限却退出异常”才是真的失败（含 ctx 取消导致的终止）
	waitErr := cmd.Wait()
	if hitLimit {
		return string(trimNulRecords(buf, limit)), true, nil
	}
	if waitErr != nil {
		return "", false, waitErr
	}
	return string(buf), false, nil
}

// trimNulRecords 把缓冲区裁到前 limit 条 NUL 记录（含每条记录结尾的那个 NUL）。
// 截断处若落在半条记录上，ParseStatus 会把它当成一条新记录解析，因此必须按 NUL 边界裁。
func trimNulRecords(buf []byte, limit int) []byte {
	if limit <= 0 {
		return buf
	}
	off := 0
	for i := 0; i < limit; i++ {
		j := bytes.IndexByte(buf[off:], 0)
		if j < 0 {
			return buf
		}
		off += j + 1
	}
	return buf[:off]
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
