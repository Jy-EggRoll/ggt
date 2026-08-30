// repos.go 定义 ggt 对"仓库"的统一抽象，以及唯一一处子模块发现逻辑。
// 设计原则：子模块逻辑完全抽离到此文件，除 discoverSubmodules / expand 之外，
// 任何业务命令都不再编写子模块专属代码——子模块在 ggt 眼里就是"另一个仓库"，
// 只是带一个 IsSubmodule 标记用于展示时加 [子] 前缀、以及一个全局开关决定要不要包含它。
//
// 子模块发现策略（性能优先）：
//   - 通过解析 .gitmodules 文件获取子模块路径，而非启动 git 子进程，
//     在 Windows 上可将子模块展开从 ~10s 降至 <100ms。
//   - 递归处理嵌套子模块：对每个已初始化的子模块目录递归解析其 .gitmodules。
//   - 已初始化判断：子模块目录存在且非空即视为已初始化，
//     与 git submodule status 的语义一致（未初始化的子模块没有实际工作区）。
package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ggt/internal/worker"
)

// RepoEntry 是 ggt 遍历仓库时的统一单元。
// 无论是顶层直接仓库还是子模块，都用同一个结构表示，消除"顶层/子模块两套逻辑"的割裂。
type RepoEntry struct {
	// Path 是仓库在文件系统中的绝对（或配置给定的）根目录路径。
	// 子模块的 Path 为父仓库 Path 拼接子模块相对路径得到，可直接传给 git.RunContext。
	Path string
	// Name 是展示用的仓库名。顶层仓库为目录名；子模块为相对父仓库的路径
	// （如父仓库内有 sub/a 子模块，则 Name 为 "sub/a"）。
	Name string
	// IsSubmodule 标记该条目是否来自子模块。打印时据此决定加 [子] 前缀。
	IsSubmodule bool
}

// discoverSubmodules 是全局唯一发现子模块的地方。
// 通过解析 .gitmodules 文件获取子模块路径列表，无需启动 git 子进程。
// 仅保留已初始化的条目（目录存在且非空），未初始化的子模块跳过，
// 因为未初始化的子模块没有实际工作区，无法对其执行 git 操作。
//
// 返回值是相对父仓库根目录的子模块路径列表（如 ["sub", "sub/nested"]）。
// 父仓库自身无子模块时返回空切片（命令正常无输出）。
//
// 性能：.gitmodules 解析为纯 Go 文件 I/O，耗时 <1ms；
// 对比 git submodule status --recursive 的 ~300-500ms（含进程创建），
// 在 Windows 上可提速 300-500 倍。
func discoverSubmodules(repoPath string) []string {
	gitmodulesPath := filepath.Join(repoPath, ".gitmodules")
	data, err := os.ReadFile(gitmodulesPath)
	if err != nil {
		// .gitmodules 不存在或不可读，按"无子模块"处理。
		return nil
	}

	// 第一步：解析 .gitmodules 文件，提取所有直接子模块的相对路径。
	directPaths := parseGitmodules(data)

	// 第二步：逐个检查子模块是否已初始化，并递归发现嵌套子模块。
	var result []string
	for _, subPath := range directPaths {
		fullPath := filepath.Join(repoPath, subPath)
		if !isSubmoduleInitialized(fullPath) {
			// 未初始化的子模块（目录不存在或为空）跳过。
			continue
		}
		result = append(result, subPath)

		// 递归处理嵌套子模块：子模块自身可能也有子模块。
		nested := discoverSubmodules(fullPath)
		for _, n := range nested {
			result = append(result, filepath.Join(subPath, n))
		}
	}
	return result
}

// parseGitmodules 从 .gitmodules 文件内容中解析出所有子模块的相对路径。
// .gitmodules 格式为 INI 风格：每个 [submodule "name"] 块包含 path = ... 字段。
// 本函数仅提取 path 字段，忽略 url、branch 等其他配置。
func parseGitmodules(data []byte) []string {
	var paths []string
	var currentPath string

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// 遇到新的 [submodule ...] 块时，保存上一个条目的 path。
		if strings.HasPrefix(line, "[submodule ") {
			if currentPath != "" {
				paths = append(paths, currentPath)
				currentPath = ""
			}
			continue
		}

		// 解析 key = value，仅关注 path 字段。
		if idx := strings.Index(line, "="); idx >= 0 {
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			if key == "path" {
				currentPath = val
			}
		}
	}
	// 保存最后一个条目。
	if currentPath != "" {
		paths = append(paths, currentPath)
	}
	return paths
}

// isSubmoduleInitialized 检查子模块目录是否已初始化。
// 已初始化的子模块目录存在且非空（有实际文件），未初始化的子模块目录不存在或为空。
func isSubmoduleInitialized(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) == 0 {
		return false
	}
	return true
}

// expand 把"顶层仓库路径列表"展开为"顶层 + 子模块"的扁平条目列表。
// ignoreSubmodules 为 true 时完全跳过子模块，是全局唯一控制"是否忽略子模块"的接口
// （对应配置项 ignore_submodules）。
// 返回的切片中，顶层仓库 IsSubmodule=false，子模块 IsSubmodule=true。
//
// 子模块发现通过 worker.Map 并发执行（并发度取配置的 concurrency 值），
// 尽管 .gitmodules 解析本身很快（<1ms），但递归发现嵌套子模块涉及文件系统 I/O，
// 并发可进一步缩短总耗时。worker.Map 保证结果按原始顺序返回。
func expand(ctx context.Context, topPaths []string, ignoreSubmodules bool) []RepoEntry {
	// 第一步：收集所有顶层仓库条目（顺序与配置一致）
	entries := make([]RepoEntry, 0, len(topPaths))
	for _, top := range topPaths {
		entries = append(entries, RepoEntry{
			Path:        top,
			Name:        filepath.Base(top),
			IsSubmodule: false,
		})
	}

	if ignoreSubmodules {
		return entries
	}

	// 第二步：并发发现所有顶层仓库的子模块。
	subsForEach := worker.Map(ctx, topPaths, GetConfig().ConcurrencyValue(),
		func(_ context.Context, top string) []string {
			return discoverSubmodules(top)
		})

	// 第三步：按顺序将子模块条目追加到 entries 中。
	for i, subs := range subsForEach {
		for _, subRel := range subs {
			entries = append(entries, RepoEntry{
				Path:        filepath.Join(topPaths[i], subRel),
				Name:        subRel,
				IsSubmodule: true,
			})
		}
	}

	return entries
}

// GetAllRepos 返回"顶层仓库 + 子模块"的全部条目（按 ignore 决定是否含子模块）。
// 各遍历型命令应调用本函数取代 MustGetRepoList，从而自动获得子模块辐射能力。
func GetAllRepos(ctx context.Context, ignore bool) []RepoEntry {
	return expand(ctx, GetRepoList(), ignore)
}

// MustGetAllRepos 同 GetAllRepos，但顶层仓库为空时打印提示并退出（正常无任务）。
// 所有需要仓库集合的命令都应调用本函数而非 MustGetRepoList。
// --debug 模式下分别输出仓库发现和子模块展开的耗时。
func MustGetAllRepos(ctx context.Context, ignore bool) []RepoEntry {
	t1 := NewDebugTimer("仓库发现")
	top := GetRepoList()
	if len(top) == 0 {
		WarnMsg("未配置任何仓库路径，请先使用 'ggt repo add <path>' 或 'ggt repo add-parent <path>' 添加")
		// 空列表属于"正常无任务可做"而非错误，因此退出码 0。
		os.Exit(0)
	}
	t1.Done()

	t2 := NewDebugTimer(fmt.Sprintf("子模块展开 (%d 个仓库, 并发度=%d)", len(top), GetConfig().ConcurrencyValue()))
	result := expand(ctx, top, ignore)
	t2.Done()

	return result
}
