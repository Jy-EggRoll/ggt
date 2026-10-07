// repos.go 定义 ggt 对“仓库”的统一抽象，以及唯一一处子模块与工作树的展开逻辑。
// 设计原则：子模块与工作树逻辑完全抽离到此文件，除 discoverSubmodules / appendWorktrees /
// expand 之外，任何业务命令都不再编写它们专属的代码——在 ggt 眼里两者都是“另一个仓库”，
// 只是带标记用于展示与归属：子模块加 [子] 前缀，工作树跟随宿主。
//
// 子模块与工作树的区别，决定了它们在后端的表示方式不同：
//   - 子模块是**归属关系**：父仓库的一个组成部分，嵌在父仓库内部，长期存在。
//   - 工作树是**副本关系**：同一个仓库的另一份工作区，通常在仓库外部，随时增删。
//
// 共同点在于两者都有自己的工作区与索引，所以都必须作为独立条目参与采集。
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
	"os"
	"path/filepath"
	"strings"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/ggt/internal/git"
	"github.com/jy-eggroll/ggt/internal/worker"
	"github.com/pterm/pterm"
)

// RepoEntry 是 ggt 遍历仓库时的统一单元。
// 无论是顶层直接仓库还是子模块，都用同一个结构表示，消除“顶层/子模块两套逻辑”的割裂。
type RepoEntry struct {
	// Path 是仓库在文件系统中的绝对（或配置给定的）根目录路径。
	// 子模块的 Path 为父仓库 Path 拼接子模块相对路径得到，可直接传给 git.RunContext。
	Path string
	// Name 是展示用的仓库名。顶层仓库为目录名；子模块为相对父仓库的路径
	// （如父仓库内有 sub/a 子模块，则 Name 为 "sub/a"）。
	Name string
	// IsSubmodule 标记该条目是否来自子模块。打印时据此决定加 [子] 前缀。
	IsSubmodule bool
	// WorktreeOf 非空时表示这个条目是一棵工作树，值为宿主（主工作区）的路径。
	//
	// 工作树以**独立条目**进入列表，而不是挂成宿主的子字段：ggt 的每个接口都按路径
	// 定位，并把该路径直接当作 git 工作目录，所以工作树用自己的真实路径进来之后，
	// diff / 暂存 / 提交 / 推送这些路径一行都不用改。挂成子字段则要为每个接口
	// 重新实现一遍“先找宿主、再找子项”，而且每个工作区都有自己的索引，
	// 跨路径的批量操作语义也会跟着变复杂。
	WorktreeOf string
	// WorktreeName 是工作树的展示名：在分支上取分支名，游离 HEAD 取短 SHA。
	WorktreeName string
	// WorktreeDetached 表示这棵工作树处于游离 HEAD。
	// 采集层面它与普通条目没有区别，此字段只供展示层区分身份。
	WorktreeDetached bool
	// WorktreeState 描述这棵工作树是否还可用，取值见 worktreeState* 常量。
	// 空字符串表示正常。已失效的条目不应当再被采集状态，否则每轮都失败，
	// 页面上一片“采集失败”，而真实含义是“这棵工作树已经失效”。
	WorktreeState string
}

// 工作树的可用状态。空字符串表示正常，其余取值由 worktreeState 判定。
const (
	// worktreeStateMissing 表示这棵工作树的目录已经不在。
	// git 对“目录被删掉且没加锁”的工作树会报 prunable；但加锁之后删掉目录时，
	// 实测 git 仍然只报 locked、不报 prunable，所以判定不能只看 prunable
	worktreeStateMissing = "missing"
	// worktreeStateLocked 表示被 git worktree lock 锁住。
	// 锁住的工作树不会被自动清理，因此它的目录删掉之后仍会长期留在列表里
	worktreeStateLocked = "locked"
)

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
		// .gitmodules 不存在或不可读，按“无子模块”处理。
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

// expand 把“顶层仓库路径列表”展开为“顶层 + 子模块 + 工作树”的扁平条目列表。
// ignoreSubmodules 为 true 时完全跳过子模块，是全局唯一控制“是否忽略子模块”的接口
// （对应配置项 ignore_submodules）。它**不**影响工作树——那是另一件事，该由别的开关管。
// 返回的切片中，顶层仓库与工作树的 IsSubmodule=false，子模块为 true；
// 工作树额外带 WorktreeOf / WorktreeName 表示归属。
//
// 子模块与工作树的发现都通过 worker.Map 并发执行（并发度取配置的 concurrency 值），
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

	if !ignoreSubmodules {
		// 第二步：并发发现所有顶层仓库的子模块。
		subsForEach := worker.Map(ctx, topPaths, Concurrency(),
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
	}

	// 第四步：把各仓库的工作树追加进来。
	return appendWorktrees(ctx, entries, topPaths)
}

// appendWorktrees 把各顶层仓库的工作树追加为独立条目，紧跟在自己的宿主之后。
//
// 紧跟宿主不是排版偏好，而是看板渲染的前提：看板按数组顺序铺卡片，卡片边界按
// “同一个宿主的行”计算。工作树一旦排到远处，就会与宿主变成两张卡片，中间还会夹进
// 别的仓库的行，看起来像平白多出一个仓库。
//
// 只对顶层仓库展开、不碰子模块：子模块里的工作树在 git 输出里报的第一条路径是
// 父仓库的 .git/modules/... 而不是真实工作区路径，按字符串推不出归属——
// 宁可不展开，也不能把归属展错。
func appendWorktrees(ctx context.Context, entries []RepoEntry, topPaths []string) []RepoEntry {
	// 探测与展开合成一个并发任务：先用一次 os.Stat 把绝大多数仓库挡掉（实测一批仓库里
	// 通常只有个别几个建过工作树），探不到的连 git 进程都不启动
	wtsForEach := worker.Map(ctx, topPaths, Concurrency(),
		func(ctx context.Context, top string) []RepoEntry {
			if !git.HasWorktrees(top) {
				return nil
			}
			wts, err := git.ListWorktrees(ctx, top)
			if err != nil {
				// 工作树只是附加信息，拿不到就当这个仓库没有工作树，
				// 不该让整个仓库发现流程失败
				return nil
			}
			return worktreeEntries(top, wts)
		})

	// 按宿主归拢，避免在 entries 里边遍历边查找。
	// 用 Path 而不是下标当键：下标会在后面重建列表时失效
	byHost := make(map[string][]RepoEntry, len(topPaths))
	for i, wts := range wtsForEach {
		if len(wts) > 0 {
			byHost[topPaths[i]] = wts
		}
	}
	if len(byHost) == 0 {
		return entries
	}

	// 重建列表顺便去重：配置里重复写同一个仓库时（GetRepoList 本身不去重），
	// 展开会把它名下的工作树也重复一遍，而按路径定位的接口只认第一个匹配
	result := make([]RepoEntry, 0, len(entries)+len(byHost))
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if seen[entry.Path] {
			continue
		}
		seen[entry.Path] = true
		result = append(result, entry)
		for _, wt := range byHost[entry.Path] {
			if seen[wt.Path] {
				continue
			}
			seen[wt.Path] = true
			result = append(result, wt)
		}
	}
	return result
}

// worktreeEntries 把 git 报出的工作树转成仓库条目，跳过宿主自己。
func worktreeEntries(host string, wts []git.Worktree) []RepoEntry {
	var result []RepoEntry
	for _, wt := range wts {
		// 主工作区就是宿主自己（git 保证它排在第一位），不是额外的工作树。
		// 比路径而不是比下标：语义更明确，也不依赖 git 的输出顺序
		if filepath.Clean(wt.Path) == filepath.Clean(host) {
			continue
		}
		name := worktreeDisplayName(wt)
		result = append(result, RepoEntry{
			Path:             wt.Path,
			Name:             name,
			WorktreeOf:       host,
			WorktreeName:     name,
			WorktreeDetached: wt.Detached,
			WorktreeState:    worktreeState(wt),
		})
	}
	return result
}

// worktreeState 判定一棵工作树是否还可用。
//
// 顺序上先看 git 报的 prunable，再用“目录是否存在”作为回退判据：
// 加锁之后把目录删掉时，实测 git 仍然只报 locked，不会报 prunable。
// 少了这层回退判据，那个已不存在的路径每轮都会被拿去跑一次 git status 并失败，
// 页面显示成“采集失败”，而用户需要知道的其实是“这棵工作树已经失效、可以清掉了”。
func worktreeState(wt git.Worktree) string {
	if wt.Prunable {
		return worktreeStateMissing
	}
	if _, err := os.Stat(wt.Path); err != nil {
		return worktreeStateMissing
	}
	if wt.Locked {
		return worktreeStateLocked
	}
	return ""
}

// worktreeDisplayName 取工作树的展示名。
//
// 在分支上用分支名而不是目录名：Agent 常把工作树建在 /tmp/xxx-wt/<随手起的名字> 这种
// 与分支对不上的位置，目录名说明不了“它在做哪件事”。游离 HEAD 没有分支名可用，
// 退回短 SHA，至少能跟提交对上。
func worktreeDisplayName(wt git.Worktree) string {
	if wt.Branch != "" {
		return wt.Branch
	}
	if len(wt.Head) > 7 {
		return wt.Head[:7]
	}
	return wt.Head
}

// AllRepos 按当前配置取全部仓库条目（含子模块，除非配置要求忽略）。
//
// 各遍历型命令统一用它，避免每处都写 GetConfig().IgnoreSubmodules——那是配置细节，
// 不该泄漏到每个命令里；真要改“是否含子模块”的语义时也只需改这一处。
func AllRepos(ctx context.Context) []RepoEntry {
	return MustGetAllRepos(ctx, GetConfig().IgnoreSubmodules)
}

// ExpandRepos 按当前配置展开给定的顶层仓库，语义同 AllRepos，
// 但顶层列表由调用方提供（如 remote 用当前目录）。
func ExpandRepos(ctx context.Context, top []string) []RepoEntry {
	return expand(ctx, top, GetConfig().IgnoreSubmodules)
}

// MustGetAllRepos 返回“顶层仓库 + 子模块”的全部条目，顶层仓库为空时打印提示并退出
// （空列表属于“正常无任务可做”而非错误，所以退出码 0）。
// 需要仓库集合的命令一律调用它（或其封装 AllRepos），不要直接用 GetRepoList——
// 只有经它展开才能获得子模块辐射能力。
// --debug 模式下分别输出仓库发现和子模块展开的耗时。
func MustGetAllRepos(ctx context.Context, ignore bool) []RepoEntry {
	t1 := NewDebugTimer(l10n.T("Repository discovery", nil))
	top := GetRepoList()
	if len(top) == 0 {
		WarnMsg(l10n.T("No repositories configured; add one with 'ggt repo add <path>' or 'ggt repo add-parent <path>'", nil))
		// 空列表属于“正常无任务可做”而非错误，因此退出码 0。
		os.Exit(0)
	}
	t1.Done()

	t2 := NewDebugTimer(l10n.T("Submodule expansion (repositories: {{.Count}}, concurrency: {{.Concurrency}})",
		map[string]any{"Count": len(top), "Concurrency": Concurrency()}))
	result := expand(ctx, top, ignore)
	t2.Done()

	return result
}

// ——— 仓库列表管理 ———

// GetRepoList 返回所有有效仓库路径的列表。
// 合并直接添加的仓库（RepoPaths）和从父目录扫描到的仓库。
// 父目录扫描会检查每个子目录是否包含 .git 目录。
func GetRepoList() []string {
	repos := GetConfig().RepoPaths

	for _, parentPath := range GetConfig().ParentPaths {
		entries, err := os.ReadDir(parentPath)
		if err != nil {
			// 父目录不存在或无权访问，跳过
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			repoPath := parentPath + string(os.PathSeparator) + entry.Name()
			if git.IsRepo(repoPath) {
				repos = append(repos, repoPath)
			}
		}
	}

	return repos
}

// PrintRepoList 打印仓库列表的标题和所有路径。
func PrintRepoList(repos []string) {
	Header(l10n.T("Repositories", nil))
	for _, repo := range repos {
		PrintPath(repo)
	}
	pterm.Println()
	InfoMsg(l10n.T("Total repositories: {{.Count}}", map[string]any{"Count": len(repos)}))
}
