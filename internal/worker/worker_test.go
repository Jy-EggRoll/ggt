// worker_test 对并发工具 Map 进行单元测试，覆盖顺序保持、并发限制、panic 恢复、
// ctx 取消、共享信号量、“未初始化/非法并发数”这两条守卫，以及回调内再调 Map 的拒绝。
package worker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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

// TestMapCallbackCtxIsMarked 验证 fn 收到的 ctx 带批次状态，而调用方自己的 ctx 没有
// ——嵌套判定就是靠这一条成立的。正常跑完的一批不该被记成“发生过误用”。
func TestMapCallbackCtxIsMarked(t *testing.T) {
	if err := Init(2); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}

	outer := context.Background()
	if heldState(outer) != nil {
		t.Fatal("调用方的 ctx 不该带批次状态")
	}

	var marked, unmarked int64
	var misused atomic.Bool
	Map(outer, []int{1, 2, 3}, func(ctx context.Context, _ int) int {
		if st := heldState(ctx); st != nil {
			atomic.AddInt64(&marked, 1)
			if st.nested.Load() {
				misused.Store(true)
			}
		} else {
			atomic.AddInt64(&unmarked, 1)
		}
		return 0
	})

	if got := atomic.LoadInt64(&marked); got != 3 {
		t.Errorf("每个回调都应收到带批次状态的 ctx，实得 %d/3", got)
	}
	if got := atomic.LoadInt64(&unmarked); got != 0 {
		t.Errorf("不该有回调收到不带批次状态的 ctx，实得 %d", got)
	}
	if misused.Load() {
		t.Error("正常跑完的一批不该被记成发生过嵌套误用")
	}
	// 状态只写在派生出去的 ctx 上，不该反过来污染调用方的 ctx
	if heldState(outer) != nil {
		t.Error("Map 返回后调用方的 ctx 仍不该带批次状态")
	}
}

// TestMapRejectsNestedCall 验证“回调里再调 Map”在入口就被拒绝，而不是等在信号量上。
//
// 这里直接用一个带批次状态的 ctx 模拟“正处于回调内”，因此能在本进程内 recover；
// 真实嵌套（回调里真的再调一次 Map）会让 panic 发生在 Map 派生的 goroutine 上、
// 无法在本进程内 recover，那条路径由 TestMapNestedCallFailsFast 以子进程方式覆盖。
func TestMapRejectsNestedCall(t *testing.T) {
	if err := Init(1); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}

	state := &batchState{}
	var called atomic.Bool
	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("在回调内再调 Map 应当立刻 panic")
			}
			nerr, ok := r.(*nestedMapError)
			if !ok {
				t.Fatalf("panic 值应是 *nestedMapError，实得 %T", r)
			}
			if !strings.Contains(nerr.Error(), "inside a Map callback") {
				t.Errorf("panic 说明应点明误用，实得 %q", nerr.Error())
			}
			if !strings.Contains(nerr.Error(), "items of the outer batch") {
				t.Errorf("panic 说明应指出正确做法，实得 %q", nerr.Error())
			}
		}()
		Map(markSlotHeld(context.Background(), state), []int{1, 2}, func(_ context.Context, n int) int {
			called.Store(true)
			return n
		})
	}()

	if called.Load() {
		t.Error("被拒绝的嵌套调用不应执行任何任务")
	}
	// panic 之前先把事实记进批次状态：这是回调自己 recover 时的唯一证据
	if !state.nested.Load() {
		t.Error("拒绝时应在批次状态里留下记录")
	}
}

// TestMapNestedMisuseSurvivesCallbackRecover 验证回调自己写了 recover 也吞不掉这次误用。
//
// 递归调用在 panic 之前把事实记进本批状态，Map 返回前的复查会再抛出一次：否则
// 这段防御性 recover 会让误用“安静地留一个零值”，与 Map 注释里的承诺相反。
// 代价是这种情况要等本批任务跑完才失败——非被吞路径仍然是立刻失败（见子进程用例）。
func TestMapNestedMisuseSurvivesCallbackRecover(t *testing.T) {
	if err := Init(1); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}

	var swallowed atomic.Bool
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("回调吞掉 panic 后，Map 仍应在返回前抛出 nestedMapError")
		}
		if _, ok := r.(*nestedMapError); !ok {
			t.Fatalf("panic 值应是 *nestedMapError，实得 %T", r)
		}
		if !swallowed.Load() {
			t.Error("用例没有走到目标路径：回调应当确实吞掉了那次 panic")
		}
	}()

	// 这里的返回值必须被丢弃：正常返回就说明误用被吞掉了
	_ = Map(context.Background(), []int{1}, func(ctx context.Context, n int) int {
		defer func() {
			if r := recover(); r != nil {
				swallowed.Store(true)
			}
		}()
		_ = Map(ctx, []int{n}, func(_ context.Context, m int) int { return m })
		return 0
	})
}

// nestedHelperEnv 是子进程助手的开关：只有它等于 "1" 时，
// TestNestedMapHelperProcess 才执行真实的嵌套调用。
const nestedHelperEnv = "GGT_WORKER_NESTED_HELPER"

// 两个子进程助手各代表一种嵌套场景，开关是各自的 env。
// 分两个是因为它们钉住不同的契约：有空槽位仍拒绝（行为收紧）、槽位占满也不挂住。
const (
	nestedFreeSlotEnv  = "GGT_WORKER_NESTED_FREESLOT_HELPER"
	nestedSaturatedEnv = "GGT_WORKER_NESTED_SATURATED_HELPER"
	nestedSwallowedEnv = "GGT_WORKER_NESTED_SWALLOWED_HELPER"
)

// nestedCallOnce 在回调里再调一次 Map——这正是应当被拒绝的误用。
func nestedCallOnce(ctx context.Context, n int) int {
	_ = Map(ctx, []int{n}, func(_ context.Context, m int) int { return m })
	return n
}

// TestNestedMapFreeSlotHelperProcess 在并发上限还空着大半时做一次嵌套调用。
// 老版本这里能正常跑完（内层拿得到空槽位），现在必须拒绝。
func TestNestedMapFreeSlotHelperProcess(t *testing.T) {
	if os.Getenv(nestedFreeSlotEnv) != "1" {
		t.Skip("仅作为 TestMapNestedCallFailsFast 的子进程助手运行")
	}
	if err := Init(4); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}

	Map(context.Background(), []int{1}, nestedCallOnce)
	t.Fatal("嵌套 Map 应当 panic，不应正常返回")
}

// TestNestedMapSaturatedHelperProcess 在外层占满全部槽位时做一次嵌套调用。
// 老版本在这里会内外互等、永久挂住。
func TestNestedMapSaturatedHelperProcess(t *testing.T) {
	if os.Getenv(nestedSaturatedEnv) != "1" {
		t.Skip("仅作为 TestMapNestedCallFailsFast 的子进程助手运行")
	}
	if err := Init(1); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}

	Map(context.Background(), []int{1, 2}, nestedCallOnce)
	t.Fatal("嵌套 Map 应当 panic，不应正常返回")
}

// TestNestedMapSwallowedHelperProcess 在回调里自己 recover 掉嵌套 panic。
// 进程仍然必须失败：Map 返回前的复查会把结论重新抛出。
func TestNestedMapSwallowedHelperProcess(t *testing.T) {
	if os.Getenv(nestedSwallowedEnv) != "1" {
		t.Skip("仅作为 TestMapNestedCallFailsFast 的子进程助手运行")
	}
	if err := Init(2); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}

	Map(context.Background(), []int{1}, func(ctx context.Context, n int) int {
		defer func() { _ = recover() }()
		return nestedCallOnce(ctx, n)
	})
	t.Fatal("回调吞掉 panic 后，Map 仍应在返回前抛出")
}

// TestMapNestedCallFailsFast 验证三种真实的嵌套场景都会让进程立刻失败：
// 有空槽位时（老版本能跑完）、槽位占满时（老版本会挂住），以及回调自己 recover
// 吞掉 panic 时（靠 Map 返回前的复查兜底）。
//
// 必须在子进程里跑：未被子进程 recover 的 nestedMapError 落在 Map 派生的 goroutine 上，
// 本进程内无法 recover。断言三件事：子进程以非 0 退出、输出里能读到原因、没有等到超时。
func TestMapNestedCallFailsFast(t *testing.T) {
	cases := []struct {
		name   string
		helper string
		env    string
	}{
		{"有空槽位也拒绝", "^TestNestedMapFreeSlotHelperProcess$", nestedFreeSlotEnv},
		{"槽位占满时立刻失败", "^TestNestedMapSaturatedHelperProcess$", nestedSaturatedEnv},
		{"回调自己 recover 也失败", "^TestNestedMapSwallowedHelperProcess$", nestedSwallowedEnv},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run="+c.helper)
			cmd.Env = append(os.Environ(), c.env+"=1")

			started := time.Now()
			out, err := cmd.CombinedOutput()
			elapsed := time.Since(started)

			// 超时被杀 = 这次改造要消除的行为又回来了
			if ctx.Err() != nil {
				t.Fatalf("嵌套 Map 在 %s 内没有返回，说明它又挂住了", elapsed.Round(time.Millisecond))
			}
			if err == nil {
				t.Fatalf("嵌套 Map 应让子进程以非 0 退出，实际成功：%s", out)
			}
			if !strings.Contains(string(out), "inside a Map callback") {
				t.Errorf("子进程输出应说明误用原因，实得：%s", out)
			}
		})
	}
}
