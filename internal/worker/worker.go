// worker 包提供受控并发工具，用于在多个 git 仓库上并行执行任务，
// 同时限制并发数、支持上下文取消，并保证结果按原始顺序返回。
package worker

import (
	"context"
	"log"
	"sync"
)

// Map 对 items 中的每个元素并发执行 fn，用 sem 限制并发数，
// 收集结果并保持原始顺序返回。
//
// ctx 用于取消/超时控制：若 ctx 已取消则停止启动新 goroutine，
// 且 ctx 会被透传给 fn，使正在执行的任务（如 git 命令）也能感知取消。
// fn 内 panic 会被 recover，对应位置写入 T 的零值，并通过标准 log 记录。
func Map[I any, T any](ctx context.Context, items []I, concurrency int, fn func(ctx context.Context, item I) T) []T {
	results := make([]T, len(items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)

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
					log.Printf("worker: panic 恢复 — %v", r)
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
