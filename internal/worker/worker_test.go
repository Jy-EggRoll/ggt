// worker_test 对并发工具 Map 进行单元测试，覆盖顺序保持、并发限制、panic 恢复、
// ctx 取消、共享信号量，以及“未初始化/非法并发数”这两条守卫。
package worker

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestMain 先给共享信号量装一个初始值：Map 用的是进程级状态，
// 测试进程若不先 Init，第一次 Map 就会按约定 panic。需要别的上限的用例再自行 Init。
func TestMain(m *testing.M) {
	if err := Init(4); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// clearSemaphoreForTest 把共享状态清回“未初始化”，供“没 Init 就调 Map”的用例使用。
// 生产路径没有理由清空它，因此这个助手只放在测试文件里。
func clearSemaphoreForTest() {
	semaphoreMu.Lock()
	semaphore = nil
	concurrency = 0
	semaphoreMu.Unlock()
}

// TestInitRejectsNonPositive 验证并发数小于 1 时立刻报错而不是被接受。
// 0 会让信号量永久阻塞、负数会让 make(chan) panic 在 Map 内部，两者都看不出是配置错了。
func TestInitRejectsNonPositive(t *testing.T) {
	// 先装一份已知值，再验证失败调用不会把它改掉：
	// 若实现写成“先清空、再校验”，只断言 Init(3) 成功是发现不了的
	if err := Init(5); err != nil {
		t.Fatalf("预置初始化失败: %v", err)
	}
	for _, n := range []int{0, -1, -100} {
		if err := Init(n); err == nil {
			t.Errorf("Init(%d) 应返回错误，实际通过", n)
		}
	}
	if got := Concurrency(); got != 5 {
		t.Errorf("被拒绝的 Init 不应改变已装好的并发数：Concurrency() = %d，期望 5", got)
	}

	if err := Init(3); err != nil {
		t.Fatalf("重新初始化失败: %v", err)
	}
	if got := Concurrency(); got != 3 {
		t.Errorf("Concurrency() = %d，期望 3", got)
	}
}

// TestMapPanicsBeforeInit 验证未初始化时 Map 立刻 panic 并说明先调 Init，
// 而不是拿到一份容量为 0 的通道、让所有任务永久阻塞。
func TestMapPanicsBeforeInit(t *testing.T) {
	clearSemaphoreForTest()
	t.Cleanup(func() {
		// 用 Errorf 而不是 Fatalf：Cleanup 里 Fatalf 会中断后续清理，
		// 而这里只是给下一个用例恢复一份可用的信号量
		if err := Init(4); err != nil {
			t.Errorf("恢复初始化失败: %v", err)
		}
	})

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("未初始化时 Map 应当 panic")
		}
		if msg := fmt.Sprint(r); !strings.Contains(msg, "worker.Init") {
			t.Errorf("panic 信息应提示先调用 worker.Init，实际 %q", msg)
		}
	}()

	Map(context.Background(), []int{1}, func(_ context.Context, n int) int { return n })
}

// TestMap_OrderAndResult 验证结果顺序与输入顺序一致，且每个元素都被正确处理。
func TestMap_OrderAndResult(t *testing.T) {
	if err := Init(2); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}

	items := []string{"a", "b", "c", "d", "e"}
	got := Map(context.Background(), items, func(_ context.Context, s string) string {
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

// peakTracker 记录一段并发执行里“同时在跑的任务数”的历史峰值。
// 用原子操作而不是互斥锁：峰值统计本身不该成为被观测的并发瓶颈。
type peakTracker struct {
	current int64
	peak    int64
}

func (p *peakTracker) enter() {
	c := atomic.AddInt64(&p.current, 1)
	for {
		m := atomic.LoadInt64(&p.peak)
		if c <= m || atomic.CompareAndSwapInt64(&p.peak, m, c) {
			return
		}
	}
}

func (p *peakTracker) leave() { atomic.AddInt64(&p.current, -1) }

func (p *peakTracker) max() int64 { return atomic.LoadInt64(&p.peak) }

// TestMap_ConcurrencyLimit 验证同时运行的 goroutine 数量不超过并发上限。
func TestMap_ConcurrencyLimit(t *testing.T) {
	const limit = 4
	if err := Init(limit); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}

	var tracker peakTracker
	items := make([]string, 0, 50)
	for i := 0; i < 50; i++ {
		items = append(items, fmt.Sprintf("item-%d", i))
	}

	Map(context.Background(), items, func(_ context.Context, _ string) int {
		tracker.enter()
		time.Sleep(time.Millisecond)
		tracker.leave()
		return 0
	})

	if max := tracker.max(); max > int64(limit) {
		t.Fatalf("并发峰值 %d 超过限制 %d", max, limit)
	}
}

// TestMapSharesOneSemaphoreAcrossCalls 验证两次并发调用 Map 时，总量仍受同一份信号量约束。
//
// 这是本次改造的核心：过去每次调用各 make 一份信号量，看板的多个 /api/fetch 请求叠加后
// 总并发是“请求数 × 并发数”，本机保护被抵消。断言只能靠“同时跑两次 Map 再量峰值”，
// 分别跑两次的话两次都各自不超限，测不出共享与否。
func TestMapSharesOneSemaphoreAcrossCalls(t *testing.T) {
	const limit = 4
	if err := Init(limit); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}

	var tracker peakTracker
	fn := func(_ context.Context, _ int) int {
		tracker.enter()
		time.Sleep(2 * time.Millisecond)
		tracker.leave()
		return 0
	}

	items := make([]int, 20)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Map(context.Background(), items, fn)
		}()
	}
	wg.Wait()

	if max := tracker.max(); max > int64(limit) {
		t.Fatalf("两次 Map 的总并发峰值 %d 超过共享上限 %d", max, limit)
	}
}

// TestMap_PanicRecovered 验证 fn 内 panic 不会导致程序崩溃，对应位置写零值。
func TestMap_PanicRecovered(t *testing.T) {
	if err := Init(1); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}

	items := []string{"ok", "boom", "ok2"}
	got := Map(context.Background(), items, func(_ context.Context, s string) string {
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
	if err := Init(2); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 调用前就取消
	var started int64
	items := []string{"a", "b", "c"}
	got := Map(ctx, items, func(_ context.Context, _ string) string {
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
	if err := Init(1); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var started int64
	var mu sync.Mutex
	var order []string

	items := []string{"first", "second", "third"}
	got := Map(ctx, items, func(c context.Context, s string) string {
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
	if err := Init(3); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}

	got := Map(context.Background(), []string{}, func(_ context.Context, s string) string {
		return s
	})
	if len(got) != 0 {
		t.Errorf("空输入应返回空切片，实际 %#v", got)
	}
}

// TestMap_SortedOutput 用排序结果验证并发执行后顺序依然对应原始索引。
func TestMap_SortedOutput(t *testing.T) {
	if err := Init(2); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}

	items := []string{"3", "1", "2"}
	got := Map(context.Background(), items, func(_ context.Context, s string) int {
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
