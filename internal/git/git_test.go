// git_test 对 git 命令封装进行集成测试。
// 测试依赖系统中可用的 git 命令，并通过临时目录初始化真实仓库来验证行为。
package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newTestRepo 创建一个临时 git 仓库并返回其路径，测试结束后自动清理。
// 若该环境无 git 命令则跳过测试。
func newTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("环境未安装 git，跳过 git 包集成测试")
	}

	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("初始化测试仓库失败 %v: %s", args, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@test")
	run("config", "user.name", "test")
	// 创建一个文件并提交，确保仓库有可用的 HEAD
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0644); err != nil {
		t.Fatalf("写入测试文件失败: %v", err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")
	return dir
}

// TestRun_Success 验证正常 git 命令返回标准输出且 err 为 nil。
func TestRun_Success(t *testing.T) {
	repo := newTestRepo(t)
	out, err := Run(repo, "rev-parse", "--short", "HEAD")
	if err != nil {
		t.Fatalf("Run 返回错误: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("期望 rev-parse 返回非空 commit 短哈希")
	}
}

// TestRunContext_Cancelled 验证传入已取消的 ctx 时命令立即返回错误。
func TestRunContext_Cancelled(t *testing.T) {
	repo := newTestRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消
	_, err := RunContext(ctx, repo, "status")
	if err == nil {
		t.Error("已取消的 ctx 应当使命令失败")
	}
}

// TestRunContext_DefaultTimeoutFallback 验证未设截止时间的 ctx 仍受默认超时保护（此处用正常命令验证不超时）。
func TestRunContext_DefaultTimeoutFallback(t *testing.T) {
	repo := newTestRepo(t)
	// 背景 ctx 无截止时间，应叠加默认超时且不立刻失败
	out, err := RunContext(context.Background(), repo, "status", "--short")
	if err != nil {
		t.Fatalf("无截止时间的 ctx 不应导致正常命令失败: %v", err)
	}
	_ = out
}

// TestRun_Error 验证非法 git 命令返回错误（非零退出）。
func TestRun_Error(t *testing.T) {
	repo := newTestRepo(t)
	_, err := Run(repo, "not-a-real-subcommand")
	if err == nil {
		t.Error("非法 git 子命令应当返回错误")
	}
}

// TestRunCombined_CapturesStderr 验证 RunCombined 在命令失败时同时返回错误与 stderr 内容。
func TestRunCombined_CapturesStderr(t *testing.T) {
	repo := newTestRepo(t)
	out, err := RunCombined(repo, "not-a-real-subcommand")
	if err == nil {
		t.Error("非法命令应返回错误")
	}
	if !strings.Contains(out, "not-a-real-subcommand") {
		t.Errorf("合并输出应包含 stderr 报错信息，实际: %q", out)
	}
}

// TestRunCombined_Success 验证成功命令的合并输出包含 stdout。
func TestRunCombined_Success(t *testing.T) {
	repo := newTestRepo(t)
	out, err := RunCombined(repo, "log", "--oneline")
	if err != nil {
		t.Fatalf("正常命令不应失败: %v", err)
	}
	if !strings.Contains(out, "init") {
		t.Errorf("log 输出应包含提交信息 init，实际: %q", out)
	}
}
