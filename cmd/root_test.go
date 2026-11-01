// root_test 覆盖根命令这一层的纯函数逻辑：输出封装层的字符串构造（分隔线、仓库名、
// 次要文本）与 gather 的采集/取消语义，都不依赖终端。
package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/jy-eggroll/ggt/internal/config"
)

// TestBuildSeparator 验证分隔线由 width 个 "─" 组成（ANSI 转义不影响可见字符数）。
func TestBuildSeparator(t *testing.T) {
	cases := []int{0, 1, 10, 80}
	for _, w := range cases {
		got := buildSeparator(w)
		if n := strings.Count(got, "─"); n != w {
			t.Errorf("buildSeparator(%d) 含 %d 个 '─', 期望 %d", w, n, w)
		}
	}
}

// TestRepoName 验证仓库名被青色包裹为 "[name]" 形式且包含原名。
func TestRepoName(t *testing.T) {
	got := RepoName("my-repo")
	if !strings.Contains(got, "[my-repo]") {
		t.Errorf("RepoName 应含 [my-repo], 实际 %q", got)
	}
}

// TestMuted 验证次要文本被包裹且原文本可见。
func TestMuted(t *testing.T) {
	got := Muted("变动详情：")
	if !strings.Contains(got, "变动详情：") {
		t.Errorf("Muted 应保留原文本, 实际 %q", got)
	}
}

// TestGatherKeepsAllOrdered 验证未取消时 gather 返回全部条目，且顺序与输入一致。
func TestGatherKeepsAllOrdered(t *testing.T) {
	withConfig(t, &config.Config{Concurrency: "3"})
	repos := []RepoEntry{{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}, {Name: "e"}}

	got := gather(context.Background(), "test", repos, func(_ context.Context, e RepoEntry) string {
		return "got:" + e.Name
	})

	if len(got) != len(repos) {
		t.Fatalf("返回 %d 条, 期望 %d 条", len(got), len(repos))
	}
	for i, e := range repos {
		if want := "got:" + e.Name; got[i] != want {
			t.Errorf("第 %d 条 = %q, 期望 %q（顺序应与输入一致）", i, got[i], want)
		}
	}
}

// TestGatherDropsNotStartedAfterCancel 验证取消后没真正进入 fn 的条目不出现在返回值里。
//
// 用 channel 而不是 sleep 来同步：并发度取 1，保证同一时刻只有一个任务在跑；
// 等到第一个任务确实开始后再取消，然后才放它返回。此后排队的任务会在 fn 入口被拦掉，
// 因此返回值必须只剩这一条——这正是“中断时别把没跑过的仓库打成干净/成功”的依据。
func TestGatherDropsNotStartedAfterCancel(t *testing.T) {
	withConfig(t, &config.Config{Concurrency: "1"})
	repos := []RepoEntry{{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan string, len(repos))
	release := make(chan struct{})
	result := make(chan []string, 1)

	go func() {
		result <- gather(ctx, "test", repos, func(_ context.Context, e RepoEntry) string {
			started <- e.Name
			<-release
			return e.Name
		})
	}()

	// 并发度为 1 时谁先抢到信号量由调度器决定，因此从 channel 里取实际开始的那一个，
	// 而不是假定一定是 repos[0]
	first := <-started
	cancel()
	close(release)

	got := <-result
	if len(got) != 1 {
		t.Fatalf("取消后返回 %d 条 %v, 期望只剩已经跑过的 1 条", len(got), got)
	}
	if got[0] != first {
		t.Errorf("返回 %q, 期望是真正跑过的那条 %q", got[0], first)
	}
	select {
	case extra := <-started:
		t.Errorf("取消后又启动了 %q，应当不再启动新任务", extra)
	default:
	}
}
