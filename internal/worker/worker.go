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

// Map 对 items 中的每个元素并发执行 fn，用进程级共享信号量限制并发数，
// 收集结果并保持原始顺序返回。
//
// ctx 用于取消/超时控制：若 ctx 已取消则停止启动新 goroutine，
// 且 ctx 会被透传给 fn，使正在执行的任务（如 git 命令）也能感知取消。
// fn 内 panic 会被 recover，对应位置写入 T 的零值，并通过标准 log 记录。
//
// 信号量在函数入口取一次并在整批任务中复用：即使中途有人再调 Init，
// 这一批的上限也不会前后不一致（Init 本就只该在启动时调用一次）。
//
// 不要在 fn 里再调 Map：共享信号量没有重入额度，外层占满全部额度时，内层会永远等在
// 信号量上，而外层又在等内层返回。需要嵌套并发时应当把内层展开到同一批任务里。
func Map[I any, T any](ctx context.Context, items []I, fn func(ctx context.Context, item I) T) []T {
	results := make([]T, len(items))
	var wg sync.WaitGroup
	sem := sharedSemaphore()

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
					// panic 不应让整个程序崩溃，记录后该位置保留零值
					log.Printf("worker: recovered from panic: %v", r)
				}
			}()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[idx] = fn(ctx, it)
		}(i, item)
	}

	wg.Wait()
	return results
}
