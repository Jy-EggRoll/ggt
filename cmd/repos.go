// repos.go 定义 ggt 对"仓库"的统一抽象，以及唯一一处子模块发现逻辑。
// 设计原则：子模块逻辑完全抽离到此文件，除 listSubmodulePaths / expand 之外，
// 任何业务命令都不再编写子模块专属代码——子模块在 ggt 眼里就是"另一个仓库"，
// 只是带一个 IsSubmodule 标记用于展示时加 [子] 前缀、以及一个全局开关决定要不要包含它。
package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"ggt/internal/git"
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

// listSubmodulePaths 是全局唯一发现子模块的地方。
// 通过 `git submodule status --recursive` 取得所有子模块（含嵌套子模块）的列表，
// 仅保留已初始化的条目（行首为空格或 '+'），未初始化（行首 '-'）的子模块跳过，
// 因为未初始化的子模块没有实际工作区，无法对其执行 git 操作。
// 返回值是相对父仓库根目录的子模块路径列表（如 ["sub", "sub/nested"]）。
// 父仓库自身无子模块时返回空切片（命令正常无输出）。
// 官方信源（行首状态字符语义）：https://git-scm.com/docs/git-submodule
func listSubmodulePaths(ctx context.Context, repoPath string) []string {
	out, err := git.RunContext(ctx, repoPath, "submodule", "status", "--recursive")
	if err != nil {
		// 非 git 仓库或命令失败都按"无子模块"处理，不影响主仓库逻辑。
		return nil
	}

	var paths []string
	for _, line := range strings.Split(out, "\n") {
		// 仅去除行尾换行/回车，绝不能 TrimSpace 左侧：行首第一个字符就是子模块状态位，
		// 对"已初始化"的子模块该行首为空格，TrimSpace 会把状态位一并抹掉导致误判为未初始化。
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		// 行首第一个字符为子模块状态位：空格=已初始化且干净，+=已初始化但脏，
		// -=未初始化，U=合并冲突。仅保留已初始化（空格/+）的条目。
		switch line[0] {
		case ' ', '+':
		default:
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// fields[1] 是子模块相对父仓库的路径（可能含空格极少见，Field 已按空白切分，
		// 子模块路径实际不含空白，可安全取第二个字段）。
		paths = append(paths, fields[1])
	}
	return paths
}

// expand 把"顶层仓库路径列表"展开为"顶层 + 子模块"的扁平条目列表。
// ignoreSubmodules 为 true 时完全跳过子模块，是全局唯一控制"是否忽略子模块"的接口
// （对应配置项 ignore_submodules）。
// 返回的切片中，顶层仓库 IsSubmodule=false，子模块 IsSubmodule=true。
func expand(ctx context.Context, topPaths []string, ignoreSubmodules bool) []RepoEntry {
	var entries []RepoEntry
	for _, top := range topPaths {
		// 顶层仓库：名称取其目录名。
		entries = append(entries, RepoEntry{
			Path:        top,
			Name:        filepath.Base(top),
			IsSubmodule: false,
		})
		if ignoreSubmodules {
			continue
		}
		for _, subRel := range listSubmodulePaths(ctx, top) {
			entries = append(entries, RepoEntry{
				Path:        filepath.Join(top, subRel),
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
func MustGetAllRepos(ctx context.Context, ignore bool) []RepoEntry {
	top := GetRepoList()
	if len(top) == 0 {
		WarnMsg("未配置任何仓库路径，请先使用 'ggt repo add <path>' 或 'ggt repo add-parent <path>' 添加")
		// 空列表属于"正常无任务可做"而非错误，因此退出码 0。
		os.Exit(0)
	}
	return expand(ctx, top, ignore)
}
