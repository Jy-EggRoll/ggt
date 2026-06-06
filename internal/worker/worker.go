package worker

import (
	"context"
	"fmt"
	"sync"
)

// Map 对 items 中的每个元素并发执行 fn，用 sem 限制并发数，
// 收集结果并保持原始顺序返回。
//
// ctx 用于取消/超时控制，若 ctx 已取消则停止启动新 goroutine。
// fn 内 panic 会被 recover，对应位置写入 T 的零值。
func Map[T any](ctx context.Context, items []string, concurrency int, fn func(string) T) []T {
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
		go func(idx int, it string) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					results[idx] = *new(T)
					fmt.Printf("worker: panic 恢复 — %v\n", r)
				}
			}()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[idx] = fn(it)
		}(i, item)
	}

	wg.Wait()
	return results
}
