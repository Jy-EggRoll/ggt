package cmd

import (
	"context"
	"strings"

	"ggt/internal/git"
	"ggt/internal/worker"
	"ggt/pkg/l10n"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

// syncResult 保存单个仓库的同步结果，用于区分「自动完成」和「需手动处理」。
// 所有仓库的 output 会先顺序打印，最后再汇总 needsManual=true 的条目。
type syncResult struct {
	name        string // 仓库展示名（已含 [子] 前缀）
	path        string // 仓库绝对路径（用于清单展示）
	output      string // 完整的输出文本（保持原有打印行为）
	needsManual bool   // true 表示需要用户手动干预
	manualHint  string // 手动处理建议（仅 needsManual=true 时有值）
}

// syncCmd 实现 "ggt sync"。
// 自动同步所有配置仓库，决策流程：
// 1. git fetch --all --prune 拉取远程最新数据
// 2. 检查工作目录是否干净（有未提交更改 → 跳过，需手动处理）
// 3. 比较本地 HEAD、远程 upstream、共同祖先 merge-base 的 commit hash
// 4. 根据三种情况决定操作：
//   - 本地 == 远程 → 已同步，跳过
//   - 本地 == 共同祖先 → 线性落后，git pull --ff-only
//   - 远程 == 共同祖先 → 本地领先，提示手动推送
//   - 其他 → 分叉，提示手动干预
//
// 输出安全：并发收集 → 顺序打印。
func newSyncCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "sync",
		Short: l10n.T("Iterate over all repositories and sync them", nil),
		Long: l10n.T(`Iterate over all repositories and sync them automatically.

- Runs git fetch --all --prune first
- Compares the commit hashes of the local branch, the remote, and the merge base
- Then acts accordingly:
  - local same as remote -> skip
  - local behind remote (fast-forwardable) -> git pull --ff-only
  - local ahead of remote -> skip and tell the user to push manually
  - divergent history -> tell the user to resolve it manually

Examples:
  ggt sync          Sync all repositories automatically`, nil),
		Run: func(cmd *cobra.Command, args []string) {
			repos := AllRepos(context.Background())
			// 这里刻意用 InfoMsg（不带空行），而其余遍历型命令用的是 InfoLn（带空行）。
			// 原因是 sync 的输出直接就是逐仓库明细、之间没有分隔线，加空行反而把首个
			// 仓库从标题里割裂出去。这是有意为之，不要当成漏改而"顺手统一"
			InfoMsg(l10n.T("Repositories: {{.Count}} — syncing...", map[string]any{"Count": len(repos)}))

			t := NewDebugTimer(l10n.T("Sync (repositories: {{.Count}})", map[string]any{"Count": len(repos)}))
			results := worker.Map(context.Background(), repos, Concurrency(), syncRepo)
			t.Done()

			// 顺序打印所有仓库的同步结果（保持原有的输出行为）
			for _, r := range results {
				PrintRaw(r.output)
			}

			// 汇总需要手动处理的仓库清单
			var manualList []syncResult
			for _, r := range results {
				if r.needsManual {
					manualList = append(manualList, r)
				}
			}

			if len(manualList) > 0 {
				WarnMsg(l10n.T("The following repositories need manual handling:", nil))
				for _, r := range manualList {
					pterm.Printf("  %s %s\n", RepoName(r.name), Muted(r.path))
					pterm.Printf("    -> %s\n", r.manualHint)
				}
			}

			DoneBanner(l10n.T("All repositories are in sync", nil))
		},
	}
	return c
}

// syncRepo 同步单个仓库（含子模块）：检查脏状态 → fetch → 分析 commit 关系 → 自动拉取或给出建议。
// 返回 syncResult 而非纯字符串，以便主流程区分「自动完成」和「需手动处理」的仓库。
// 接收上层 ctx 以便任务被整体取消时立即中断 git 调用。
func syncRepo(ctx context.Context, e RepoEntry) syncResult {
	label := RepoLabel(e.Name, e.IsSubmodule)
	result := syncResult{name: e.Name, path: e.Path}

	// 辅助函数：统一构建 syncResult 并设置默认 manualHint
	warn := func(output string, hint string) syncResult {
		result.output = output
		result.needsManual = true
		result.manualHint = hint
		return result
	}
	info := func(output string) syncResult {
		result.output = output
		return result
	}

	// 第一步：检查工作目录是否干净（本地操作，快速返回）
	status, err := git.RunContext(ctx, e.Path, "status", "--porcelain")
	if err != nil {
		return warn(WarnStrLn(l10n.T("{{.Label}}: failed to check status: {{.Err}}",
			map[string]any{"Label": label, "Err": err})), l10n.T("Check the repository state", nil))
	}

	if strings.TrimSpace(status) != "" {
		return warn(WarnStrLn(l10n.T("{{.Label}}: uncommitted changes present, manual handling required",
			map[string]any{"Label": label})), l10n.T("Commit or stash your changes first", nil))
	}

	// 第二步：拉取远程最新数据，修剪已删除的远程分支
	_, err = git.RunContext(ctx, e.Path, "fetch", "--all", "--prune")
	if err != nil {
		return warn(WarnStrLn(l10n.T("{{.Label}}: fetch failed: {{.Err}}",
			map[string]any{"Label": label, "Err": err})), l10n.T("Check your network or the remote repository permissions", nil))
	}

	// 第三步：获取三个关键 commit hash
	local, err := git.RunContext(ctx, e.Path, "rev-parse", "HEAD")
	if err != nil {
		return warn(WarnStrLn(l10n.T("{{.Label}}: failed to resolve local HEAD", map[string]any{"Label": label})),
			l10n.T("Check the repository state", nil))
	}
	local = strings.TrimSpace(local)

	remote, err := git.RunContext(ctx, e.Path, "rev-parse", "@{upstream}")
	if err != nil {
		// 通常是该分支未设置上游跟踪（@{upstream} 不存在），明确告知根因而非泛化的"获取失败"，
		// 避免用户误以为是网络或权限问题。
		return warn(WarnStrLn(l10n.T("{{.Label}}: no upstream tracking branch (@{upstream} does not exist), skipping",
			map[string]any{"Label": label})),
			l10n.T("Run: git branch --set-upstream-to=<remote>/<branch>", nil))
	}
	remote = strings.TrimSpace(remote)

	base, err := git.RunContext(ctx, e.Path, "merge-base", "HEAD", "@{upstream}")
	if err != nil {
		return warn(WarnStrLn(l10n.T("{{.Label}}: failed to find the merge base", map[string]any{"Label": label})),
			l10n.T("Check the repository state", nil))
	}
	base = strings.TrimSpace(base)

	// 第四步：比较决策
	if local == remote {
		return info(InfoStrLn(l10n.T("{{.Label}}: already up to date with the remote", map[string]any{"Label": label})))
	} else if local == base {
		// 本地落后于远程，且历史线性 → 可以用 fast-forward
		output := WarnStrLn(l10n.T("{{.Label}}: fast-forward available, pulling...", map[string]any{"Label": label}))
		_, err := git.RunContext(ctx, e.Path, "pull", "--ff-only")
		if err != nil {
			return warn(output+ErrorStrLn(l10n.T("{{.Label}}: pull failed: {{.Err}}",
				map[string]any{"Label": label, "Err": err})), l10n.T("Run git pull manually", nil))
		}
		return info(output + SuccessStrLn(l10n.T("{{.Label}}: pulled successfully", map[string]any{"Label": label})))
	} else if remote == base {
		return warn(WarnStrLn(l10n.T("{{.Label}}: local branch is ahead of the remote, push manually",
			map[string]any{"Label": label})), l10n.T("Run git push manually", nil))
	} else {
		return warn(ErrorStrLn(l10n.T("{{.Label}}: divergent history, manual handling required",
			map[string]any{"Label": label})), l10n.T("Merge or rebase manually", nil))
	}
}

func init() {
	register(func(root *cobra.Command) { root.AddCommand(newSyncCmd()) })
}
