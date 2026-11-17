// worker 包提供受控并发工具，用于在多个 git 仓库上并行执行任务，
// 同时限制并发数、支持上下文取消，并保证结果按原始顺序返回。
//
// 并发上限是进程级状态而不是每次调用的参数：看板的 /api/fetch 曾经按请求各建一份
// 信号量，多个标签页或脚本同时点“获取”时，总并发变成“请求数 × 并发数”，
// 本机保护被直接抵消。信号量因此只允许有一份，由启动流程调用 Init 装好。
package worker

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
)

// 共享信号量的状态。Init 在启动时写一次，Map 在每次调用时读一次；
// 用读写锁而不是 atomic.Value，是因为这两个值必须一起换，读到的组合不能一半新一半旧。
var (
	semaphoreMu sync.RWMutex
	semaphore   chan struct{}
	concurrency int
)

// Init 设置进程级并发上限。必须在任何 Map 调用之前执行一次。
//
// 参数小于 1 时返回错误而不是接受：容量为 0 的通道会让所有任务永久阻塞，
// 负数会让 make(chan) 直接 panic 在 Map 内部。两种表现都只像“命令卡住/崩了”，
// 看不出是并发数配错，因此在这里就把原因说清楚。
//
// 上限（64）不在这里判：它属于配置层的取值约束（见 internal/config），
// 两处各判一次就有了两个真相，日后改一个忘一个时，被放行的那个才是真正生效的。
func Init(n int) error {
	if n < 1 {
		return fmt.Errorf("worker: concurrency %d is invalid; it must be at least 1", n)
	}
	sem := make(chan struct{}, n)

	semaphoreMu.Lock()
	semaphore = sem
	concurrency = n
	semaphoreMu.Unlock()
	return nil
}

// Concurrency 返回当前生效的并发数；尚未初始化时为 0。
// 只供展示与诊断使用（如 debug 日志里那句“并发度=N”），限流本身由 Map 负责。
func Concurrency() int {
	semaphoreMu.RLock()
	defer semaphoreMu.RUnlock()
	return concurrency
}

// sharedSemaphore 返回共享信号量。未初始化时直接 panic：
// 那说明启动流程漏了 Init，是代码错误而不是用户配置错误；若在这里临时建一份
// 默认容量的信号量，进程级上限就会悄悄分成多份，正是这次要消除的问题。
func sharedSemaphore() chan struct{} {
	semaphoreMu.RLock()
	defer semaphoreMu.RUnlock()
	if semaphore == nil {
		panic("worker: concurrency is not initialized; call worker.Init before Map")
	}
	return semaphore
}

// batchState 记录一批任务里是否发生过嵌套误用。
//
// 为什么需要它：任务 goroutine 的 recover 只在“回调没有自己 recover”时才看得到
// nestedMapError。回调若自带 recover（防御性代码里并不少见），panic 会被它吞掉，
// 该位置就安静地留了零值——这与“误用必须让调用方看见”正好相反。因此嵌套调用在 panic
// 之前先把事实记进本批状态，Map 返回前再查一次：即使 panic 被吞掉，调用方也拿不到
// 一份看起来正常的结果。
type batchState struct{ nested atomic.Bool }

// failIfMisused 在本批发生过嵌套误用时以同一条说明终止进程。
func (b *batchState) failIfMisused() {
	if b.nested.Load() {
		panic(&nestedMapError{})
	}
}

// slotHeldKey 是“这个 ctx 的来源处已经持有共享信号量的一个槽位”这一事实的载体。
// 值是该批的状态：既用来判定“当前在回调里”，也用来记录误用有没有被回调吞掉。
//
// 用 ctx 携带而不是 goroutine local 存储：Go 不提供取当前 goroutine 标识的受支持手段，
// 而这个包也不该为一个诊断能力引入第三方依赖。代价是盲区——见 Map 注释里“覆盖范围”。
type slotHeldKey struct{}

// markSlotHeld 返回一个带批次状态的 ctx。
func markSlotHeld(ctx context.Context, state *batchState) context.Context {
	return context.WithValue(ctx, slotHeldKey{}, state)
}

// heldState 返回 ctx 携带的批次状态；不在 Map 回调里时返回 nil。
// 只有 Map 在调用 fn 之前打进去的这一种来源，因此非 nil 就说明“调用方是某个批次的回调”。
func heldState(ctx context.Context) *batchState {
	state, _ := ctx.Value(slotHeldKey{}).(*batchState)
	return state
}

// nestedMapError 是“在 Map 回调里再调 Map”的 panic 值。
//
// 单独成一个类型，是为了让任务内的 recover 与 Map 返回前的复查能把它与普通任务 panic
// 区分开：后者只记一行日志、该位置留零值就够了，而误用必须让调用方看见。
type nestedMapError struct{}

func (*nestedMapError) Error() string {
	return "worker: Map was called from inside a Map callback; nested batches are rejected outright because whether they hang depends on whether the outer batch happens to hold every slot. Run the nested work as items of the outer batch instead"
}

// Map 对 items 中的每个元素并发执行 fn，用进程级共享信号量限制并发数，
// 收集结果并保持原始顺序返回。
//
// ctx 用于取消/超时控制：若 ctx 已取消则停止启动新 goroutine，
// 且 ctx 会被透传给 fn，使正在执行的任务（如 git 命令）也能感知取消。
// fn 内 panic 会被 recover，对应位置写入 T 的零值，并通过标准 log 记录
// （嵌套误用是例外，见下）。
//
// 信号量在函数入口取一次并在整批任务中复用：即使中途有人再调 Init，
// 这一批的上限也不会前后不一致（Init 本就只该在启动时调用一次）。
//
// # 嵌套调用
//
// 不要在 fn 里再调 Map：共享信号量是进程级的，没有重入额度。嵌套会不会卡住取决于
// 外层是否已经占满全部槽位——外层项目数少于并发上限时，内层的任务还能拿到空槽位、
// 照常跑完；一旦外层把槽位占满，外层等内层返回、内层等外层释放，双方永久互等。
// 同一段代码会随仓库数量变化在“能跑”和“卡死”之间摇摆，这种不确定性本身就是缺陷。
//
// 因此本函数一律拒绝嵌套调用，哪怕当时还有空槽位：直接 panic(&nestedMapError{})。
// 这是一次行为收紧——此前靠空槽位侥幸跑通的嵌套调用，现在会失败；换来的是误用必现、
// 且失败点指向误用的那一行，而不是只在特定仓库数量下偶发挂住。
//
// 拒绝的代价是整进程退出（panic 不会被回收）：已经打印的输出会停在半截，同时在跑的
// 其他任务也随之终止。这是有意的取舍——把“偶发挂住、只能 Ctrl-C”换成“立刻失败、
// 栈里直接指出误用”；宁可让整次运行失败，也不交出一份缺项却看起来正常的结果。
//
// 失败不会因为回调自己 recover 而消失：嵌套调用在 panic 之前把事实记进本批状态，
// Map 在返回前（defer）再查一次并重新抛出，见 batchState 的说明。也就是说回调可以
// 吞掉 panic，但吞不掉这次失败的结论；那种情况下要等这一批任务跑完才抛出。
//
// 不用“等槽位超过 N 秒就算误用”来兜底：仓库多、网络慢时排队等槽位是正常状态，
// 任何时限都会把合法等待误判成误用，而误判的代价是整批任务被判失败。
//
// 覆盖范围（这是真实保证的边界，不要当成完备的死锁检测）：
//   - fn 直接用收到的 ctx 再调 Map：能拦住；
//   - fn 用收到的 ctx 派生（WithCancel / WithTimeout / WithValue）后再调：能拦住，
//     状态随 ctx 一起派生；
//   - fn 把收到的 ctx 交给自己的子 goroutine 后再调：能拦住，状态跟着 ctx 走；
//   - fn 转而用 context.Background() / context.TODO() 或另一条不经过该 ctx 的链去调：
//     拦不住，状态不在那条 ctx 里。这种调用与“另一个业务请求恰好也在调 Map”在进程内
//     无法区分（拿不到 goroutine 标识），仍会退回原先的等待行为。
//
// 因此这条检查的正确定位是“把常见误用立刻变成一条可读的失败”，不是死锁检测器：
// 跨 context 链的嵌套依旧要靠代码评审与测试发现。
func Map[I any, T any](ctx context.Context, items []I, fn func(ctx context.Context, item I) T) []T {
	if state := heldState(ctx); state != nil {
		// 先记事实再 panic：回调可能自己 recover 掉这个 panic，那就靠返回前的复查
		state.nested.Store(true)
		panic(&nestedMapError{})
	}

	results := make([]T, len(items))
	var wg sync.WaitGroup
	sem := sharedSemaphore()
	state := &batchState{}
	// 回调吞掉 panic 时的兜底：无论从哪条路径返回，返回前都按本批状态复查一次
	defer state.failIfMisused()
	// 整批共用一个带状态的 ctx：context 不可变，状态对每个任务都成立。
	// 它仍然派生自调用方的 ctx，取消与超时语义不变
	taskCtx := markSlotHeld(ctx, state)

	for i, item := range items {
		select {
		case <-ctx.Done():
			wg.Wait()
			return results
		default:
		}

		wg.Add(1)
		go func(idx int, it I) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					// 嵌套 Map 的 panic 不能被当成“某个任务失败”收下：那会让整批结果
					// 变成一份看起来正常的零值，而误用本身是代码错误，必须让调用方看见。
					// 原样抛回去，由运行时打印这条说明与该 goroutine 的栈
					if _, nested := r.(*nestedMapError); nested {
						panic(r)
					}
					// panic 不应让整个程序崩溃，记录后该位置保留零值
					log.Printf("worker: recovered from panic: %v", r)
				}
			}()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[idx] = fn(taskCtx, it)
		}(i, item)
	}

	wg.Wait()
	return results
}
