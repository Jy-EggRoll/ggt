// worker 包提供通用的并发执行模式，用于批量管理多个 git 仓库。
//
// 所有操作通过信号量（semaphore channel）控制并发数，
// 避免同时启动过多 git 进程导致 CPU 和 I/O 过载。
//
// 输出安全原则：
//   - 如果并发函数需要产生终端输出，必须使用 Map 返回字符串，
//     由调用方顺序打印，防止 goroutine 间输出交错。
//   - ForEach 仅适合纯副作用操作（如写文件、发请求），
//     函数内不应调用任何打印方法。
package worker

import "sync"

// ForEach 对 items 中的每个元素并发执行 fn，用 sem 限制并发数。
//
// 警告：fn 内禁止直接打印终端 — 并发打印会导致输出错位。
// 如果 fn 需要产生输出，请改用 Map，在调用方顺序打印结果。
func ForEach(items []string, concurrency int, fn func(string)) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	for _, item := range items {
		wg.Add(1)
		go func(it string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			fn(it)
		}(item)
	}
	wg.Wait()
}

// Map 对 items 中的每个元素并发执行 fn，用 sem 限制并发数，
// 收集结果并保持原始顺序返回。
//
// 当 fn 产生终端输出时，应返回字符串而不是直接打印：
//
//	results := worker.Map(items, 并发数, func(item string) string {
//	    return 构建输出(item)  // 构建字符串，不要直接打印
//	})
//	for _, r := range results {
//	    fmt.Print(r)  // 顺序打印，不会错位
//	}
//
// T 可以是任意类型，常用于返回结构体同时携带输出内容和数据。
func Map[T any](items []string, concurrency int, fn func(string) T) []T {
	results := make([]T, len(items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	for i, item := range items {
		wg.Add(1)
		go func(idx int, it string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[idx] = fn(it)
		}(i, item)
	}
	wg.Wait()
	return results
}
