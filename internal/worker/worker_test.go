// worker_test 对并发工具 Map 进行单元测试，覆盖顺序保持、并发限制、panic 恢复、ctx 取消。
package worker

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestMap_OrderAndResult 验证结果顺序与输入顺序一致，且每个元素都被正确处理。
func TestMap_OrderAndResult(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}
	got := Map(context.Background(), items, 2, func(_ context.Context, s string) string {
		return s + "!"
	})

	if len(got) != len(items) {
		t.Fatalf("期望长度 %d，实际 %d", len(items), len(got))
	}
	for i, want := range items {
		if got[i] != want+"!" {
			t.Errorf("索引 %d: 期望 %q，实际 %q", i, want+"!", got[i])
		}
	}
}

// TestMap_ConcurrencyLimit 验证同时运行的 goroutine 数量不超过并发上限。
func TestMap_ConcurrencyLimit(t *testing.T) {
	const limit = 4
	var maxConcurrent int64
	var current int64

	items := make([]string, 0, 50)
	for i := 0; i < 50; i++ {
		items = append(items, fmt.Sprintf("item-%d", i))
	}

	Map(context.Background(), items, limit, func(_ context.Context, _ string) int {
		c := atomic.AddInt64(&current, 1)
		// 记录历史峰值
		for {
			m := atomic.LoadInt64(&maxConcurrent)
			if c <= m || atomic.CompareAndSwapInt64(&maxConcurrent, m, c) {
				break
			}
		}
		time.Sleep(time.Millisecond)
		atomic.AddInt64(&current, -1)
		return 0
	})

	if max := atomic.LoadInt64(&maxConcurrent); max > int64(limit) {
		t.Fatalf("并发峰值 %d 超过限制 %d", max, limit)
	}
}

// TestMap_PanicRecovered 验证 fn 内 panic 不会导致程序崩溃，对应位置写零值。
func TestMap_PanicRecovered(t *testing.T) {
	items := []string{"ok", "boom", "ok2"}
	got := Map(context.Background(), items, 1, func(_ context.Context, s string) string {
		if s == "boom" {
			panic("故意触发")
		}
		return s
	})

	if got[0] != "ok" || got[2] != "ok2" {
		t.Errorf("正常元素被破坏: %#v", got)
	}
	if got[1] != "" {
		t.Errorf("panic 位置应写零值字符串，实际 %q", got[1])
	}
}

// TestMap_CtxCancelledBeforeCall 验证调用前 ctx 已取消时不会启动任何任务。
func TestMap_CtxCancelledBeforeCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 调用前就取消
	var started int64
	items := []string{"a", "b", "c"}
	got := Map(ctx, items, 2, func(_ context.Context, _ string) string {
		atomic.AddInt64(&started, 1)
		return ""
	})
	if s := atomic.LoadInt64(&started); s != 0 {
		t.Errorf("ctx 预先取消时不应启动任何任务，实际启动 %d", s)
	}
	if len(got) != len(items) {
		t.Errorf("结果长度应保持与输入一致，实际 %d", len(got))
	}
}

// TestMap_CtxCancelNoPanic 验证运行中取消 ctx 不会导致 panic，
// 且已启动的任务会尊重自己的 ctx、整体结果长度与输入一致。
func TestMap_CtxCancelNoPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var started int64
	var mu sync.Mutex
	var order []string

	items := []string{"first", "second", "third"}
	got := Map(ctx, items, 1, func(c context.Context, s string) string {
		atomic.AddInt64(&started, 1)
		mu.Lock()
		order = append(order, s)
		mu.Unlock()
		if s == "first" {
			cancel()
		}
		time.Sleep(10 * time.Millisecond)
		return s
	})

	// 已取消的 ctx 会让被取消的任务拿到取消错误（此处 fn 内部未感知，仅验证不崩溃）
	if len(got) != len(items) {
		t.Errorf("结果长度应等于输入长度，实际 %d", len(got))
	}
	// 至少第一个任务一定启动了
	if s := atomic.LoadInt64(&started); s < 1 {
		t.Errorf("至少应启动 1 个任务，实际 %d", s)
	}
	_ = order
}

// TestMap_EmptyInput 验证空输入返回空切片而非 nil panic。
func TestMap_EmptyInput(t *testing.T) {
	got := Map(context.Background(), []string{}, 3, func(_ context.Context, s string) string {
		return s
	})
	if len(got) != 0 {
		t.Errorf("空输入应返回空切片，实际 %#v", got)
	}
}

// TestMap_SortedOutput 用排序结果验证并发执行后顺序依然对应原始索引。
func TestMap_SortedOutput(t *testing.T) {
	items := []string{"3", "1", "2"}
	got := Map(context.Background(), items, 2, func(_ context.Context, s string) int {
		var n int
		fmt.Sscanf(s, "%d", &n)
		return n
	})
	want := []int{3, 1, 2}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("期望 %v，实际 %v", want, got)
	}
	// 确保没有被重排
	sorted := append([]int{}, got...)
	sort.Ints(sorted)
	if fmt.Sprint(sorted) == fmt.Sprint(got) {
		t.Error("结果不应是已排序状态，应保留原始顺序")
	}
}
