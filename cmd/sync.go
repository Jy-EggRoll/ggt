package cmd

import (
	"context"
	"strings"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/ggt/internal/git"
	"github.com/jy-eggroll/ggt/internal/worker"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

// syncResult 保存单个仓库的同步结果，用于区分“自动完成”和“需手动处理”。
// 所有仓库的 output 会先顺序打印，末尾按结果类别汇总数量，并列出需要手动处理的条目。
type syncResult struct {
	name       string      // 仓库展示名（已含 [子] 前缀）
	path       string      // 仓库绝对路径（用于清单展示）
	output     string      // 完整的输出文本（保持原有打印行为）
	outcome    syncOutcome // 最终状态，末尾的汇总按它计数
	manualHint string      // 处理建议（仅需要手动处理的条目有值）
}

// syncOutcome 是单个仓库同步走完之后的最终状态，末尾的分类汇总按它计数。
//
// 它与 syncAction 的职责不同，不能合并成一套取值：syncAction 只表达“三方 commit 关系”
// 得出的决策，而工作区脏、没有上游跟踪分支、某一步命令失败这几种结局在
// decideSyncAction 的输入里根本不存在，却同样决定用户要不要动手处理
type syncOutcome int

const (
	// outcomeUpToDate：本地与远程一致，无需处理
	outcomeUpToDate syncOutcome = iota
	// outcomePulled：本地线性落后，已 fast-forward 拉取成功
	outcomePulled
	// outcomeManual：需要用户手动处理——领先远程、历史分叉、工作区有未提交更改、没有上游跟踪分支
	outcomeManual
	// outcomeFailed：流程中某一步命令出错——状态检查、fetch、rev-parse、merge-base、pull
	outcomeFailed
)

// needsManual 判断该类结果是否要列进“需要手动处理”的清单。
// 失败与需要人工决策都会让用户停下手上的事，因此一并列入
func (o syncOutcome) needsManual() bool {
	return o == outcomeManual || o == outcomeFailed
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
// 输出安全：并发收集 → 顺序打印，末尾按结果类别汇总。
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
			// 仓库从标题里割裂出去。这是有意为之，不要当成漏改而“顺手统一”
			InfoMsg(l10n.T("Repositories: {{.Count}} — syncing...", map[string]any{"Count": len(repos)}))

			t := NewDebugTimer(l10n.T("Sync (repositories: {{.Count}})", map[string]any{"Count": len(repos)}))
			results := worker.Map(context.Background(), repos, Concurrency(), syncRepo)
			t.Done()

			// 一次遍历同时做三件事：打印结果、按类别计数、收集需要手动处理的条目。
			// 合并成一次而不是各遍历一遍，是为了保证“打印顺序”和“计数依据”必然来自同一份结果
			var manualList []syncResult
			var pulled, upToDate, manual, failed int
			for _, r := range results {
				PrintRaw(r.output)
				switch r.outcome {
				case outcomePulled:
					pulled++
				case outcomeUpToDate:
					upToDate++
				case outcomeManual:
					manual++
				case outcomeFailed:
					failed++
				}
				if r.outcome.needsManual() {
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

			// 末尾只留这一行收尾，它同时覆盖“全部同步完成”和“有仓库需要处理”两种结局。
			// 此前收尾另有一句“All repositories are in sync”，在存在待处理条目时与清单直接矛盾
			pterm.Println()
			InfoMsg(l10n.T("Finished: {{.Pulled}} pulled, {{.UpToDate}} already up to date, {{.Manual}} need manual handling, {{.Failed}} failed",
				map[string]any{"Pulled": pulled, "UpToDate": upToDate, "Manual": manual, "Failed": failed}))
		},
	}
	return c
}

// syncRepo 同步单个仓库（含子模块）：检查脏状态 → fetch → 分析 commit 关系 → 自动拉取或给出建议。
// 返回 syncResult 而非纯字符串，以便主流程按 outcome 分类汇总、找出需要手动处理的仓库。
// 接收上层 ctx 以便任务被整体取消时立即中断 git 调用。
func syncRepo(ctx context.Context, e RepoEntry) syncResult {
	label := RepoLabel(e.Name, e.IsSubmodule)
	result := syncResult{name: e.Name, path: e.Path}

	// 辅助函数：统一构建 syncResult。
	// warn 收的是“需要用户动手”的两类结局（需人工决策或某一步失败），
	// 它们的处理建议由调用点给出，汇总时会被一并列进清单
	warn := func(outcome syncOutcome, output string, hint string) syncResult {
		result.outcome = outcome
		result.output = output
		result.manualHint = hint
		return result
	}
	info := func(outcome syncOutcome, output string) syncResult {
		result.outcome = outcome
		result.output = output
		return result
	}

	// 第一步：检查工作目录是否干净（本地操作，快速返回）
	status, err := git.RunContext(ctx, e.Path, "status", "--porcelain")
	if err != nil {
		return warn(outcomeFailed, WarnStrLn(l10n.T("{{.Label}}: failed to check status: {{.Err}}",
			map[string]any{"Label": label, "Err": err})), l10n.T("Check the repository state", nil))
	}

	if strings.TrimSpace(status) != "" {
		return warn(outcomeManual, WarnStrLn(l10n.T("{{.Label}}: uncommitted changes present, manual handling required",
			map[string]any{"Label": label})), l10n.T("Commit or stash your changes first", nil))
	}

	// 第二步：拉取远程最新数据，修剪已删除的远程分支
	_, err = git.RunContext(ctx, e.Path, "fetch", "--all", "--prune")
	if err != nil {
		return warn(outcomeFailed, WarnStrLn(l10n.T("{{.Label}}: fetch failed: {{.Err}}",
			map[string]any{"Label": label, "Err": err})), l10n.T("Check your network or the remote repository permissions", nil))
	}

	// 第三步：获取三个关键 commit hash
	local, err := git.RunContext(ctx, e.Path, "rev-parse", "HEAD")
	if err != nil {
		return warn(outcomeFailed, WarnStrLn(l10n.T("{{.Label}}: failed to resolve local HEAD", map[string]any{"Label": label})),
			l10n.T("Check the repository state", nil))
	}
	local = strings.TrimSpace(local)

	remote, err := git.RunContext(ctx, e.Path, "rev-parse", "@{upstream}")
	if err != nil {
		// 通常是该分支未设置上游跟踪（@{upstream} 不存在），明确告知根因而非泛化的“获取失败”，
		// 避免用户误以为是网络或权限问题。
		return warn(outcomeManual, WarnStrLn(l10n.T("{{.Label}}: no upstream tracking branch (@{upstream} does not exist), skipping",
			map[string]any{"Label": label})),
			l10n.T("Run: git branch --set-upstream-to=<remote>/<branch>", nil))
	}
	remote = strings.TrimSpace(remote)

	base, err := git.RunContext(ctx, e.Path, "merge-base", "HEAD", "@{upstream}")
	if err != nil {
		return warn(outcomeFailed, WarnStrLn(l10n.T("{{.Label}}: failed to find the merge base", map[string]any{"Label": label})),
			l10n.T("Check the repository state", nil))
	}
	base = strings.TrimSpace(base)

	// 第四步：比较三方 commit 关系并执行对应动作。
	// 判定逻辑单独抽成 decideSyncAction——它是本命令唯一的纯决策部分，
	// 抽出来才能在不建真实仓库的前提下用表驱动测试覆盖全部分支
	switch decideSyncAction(local, remote, base) {
	case syncUpToDate:
		return info(outcomeUpToDate, InfoStrLn(l10n.T("{{.Label}}: already up to date with the remote", map[string]any{"Label": label})))
	case syncFastForward:
		// 本地落后于远程，且历史线性 → 可以用 fast-forward
		output := WarnStrLn(l10n.T("{{.Label}}: fast-forward available, pulling...", map[string]any{"Label": label}))
		_, err := git.RunContext(ctx, e.Path, "pull", "--ff-only")
		if err != nil {
			return warn(outcomeFailed, output+ErrorStrLn(l10n.T("{{.Label}}: pull failed: {{.Err}}",
				map[string]any{"Label": label, "Err": err})), l10n.T("Run git pull manually", nil))
		}
		return info(outcomePulled, output+SuccessStrLn(l10n.T("{{.Label}}: pulled successfully", map[string]any{"Label": label})))
	case syncAhead:
		return warn(outcomeManual, WarnStrLn(l10n.T("{{.Label}}: local branch is ahead of the remote, push manually",
			map[string]any{"Label": label})), l10n.T("Run git push manually", nil))
	default:
		return warn(outcomeManual, ErrorStrLn(l10n.T("{{.Label}}: divergent history, manual handling required",
			map[string]any{"Label": label})), l10n.T("Merge or rebase manually", nil))
	}
}

// syncAction 是“三方 commit 关系分析”得出结论后应当采取的动作。
type syncAction int

const (
	// syncUpToDate：本地与远程指向同一个 commit，无需任何操作
	syncUpToDate syncAction = iota
	// syncFastForward：本地落后且历史线性，可以 git pull --ff-only
	syncFastForward
	// syncAhead：本地领先远程，快进没有意义，只能由用户手动推送
	syncAhead
	// syncDiverged：两边各有对方没有的提交，必须人工合并或变基
	syncDiverged
)

// decideSyncAction 依据本地 HEAD、远程 upstream、共同祖先三个 commit hash 决定动作。
//
// 判定顺序即优先级，不能调换：
//   - 本地 == 远程：完全一致（现实中三者通常也相等，先判它可省去两次比较）
//   - 本地 == 共同祖先：远程在本地之前且没有分叉 → 线性落后，可安全快进
//   - 远程 == 共同祖先：本地在远程之前，快进没有意义 → 只能手动推送
//   - 三者互不相等：历史分叉，任何自动操作都可能丢改动，必须人工处理
//
// 纯函数：不接触 git、不产生输出，因此可以脱离真实仓库用表驱动测试覆盖全部分支
func decideSyncAction(local, remote, base string) syncAction {
	switch {
	case local == remote:
		return syncUpToDate
	case local == base:
		return syncFastForward
	case remote == base:
		return syncAhead
	default:
		return syncDiverged
	}
}

func init() {
	register(func(root *cobra.Command) { root.AddCommand(newSyncCmd()) })
}
