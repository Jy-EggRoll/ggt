package cmd

import (
	"context"
	"fmt"
	"strings"

	"ggt/internal/git"
	"ggt/internal/worker"
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
var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "遍历所有仓库，自动同步",
	Long: `遍历所有仓库，自动同步。

- 先执行 git fetch --all --prune
- 比较本地、远程、共同祖先的 commit hash
- 根据情况执行:
  - 本地与远程一致 → 跳过
  - 本地落后于远程（线性更新）→ git pull --ff-only
  - 本地领先于远程 → 跳过（提示用户手动推送）
  - 非线性更新 → 提示用户手动干预
	
使用示例:
  ggt sync          自动同步所有仓库`,
	Run: func(cmd *cobra.Command, args []string) {
		repos := MustGetAllRepos(context.Background(), GetConfig().IgnoreSubmodules)
		Infof("共 %d 个仓库，开始同步...\n", len(repos))

		t := NewDebugTimer(fmt.Sprintf("同步 (%d 个仓库)", len(repos)))
		results := worker.Map(context.Background(), repos, GetConfig().ConcurrencyValue(), syncRepo)
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
			pterm.Warning.Println("以下仓库需要手动处理：")
			for _, r := range manualList {
				pterm.Printf("  %s %s\n", RepoName(r.name), Muted(r.path))
				pterm.Printf("    -> %s\n", r.manualHint)
			}
		}

		DoneBanner("所有仓库同步完成")
	},
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
		return warn(WarnS("%s 检查状态失败: %s\n", label, err), "请检查仓库状态")
	}

	if strings.TrimSpace(status) != "" {
		return warn(WarnS("%s 本地有未提交的更改，必须手动处理\n", label), "请先 commit 或 stash")
	}

	// 第二步：拉取远程最新数据，修剪已删除的远程分支
	_, err = git.RunContext(ctx, e.Path, "fetch", "--all", "--prune")
	if err != nil {
		return warn(WarnS("%s fetch 失败: %s\n", label, err), "请检查网络或远程仓库权限")
	}

	// 第三步：获取三个关键 commit hash
	local, err := git.RunContext(ctx, e.Path, "rev-parse", "HEAD")
	if err != nil {
		return warn(WarnS("%s 获取本地 HEAD 失败\n", label), "请检查仓库状态")
	}
	local = strings.TrimSpace(local)

	remote, err := git.RunContext(ctx, e.Path, "rev-parse", "@{upstream}")
	if err != nil {
		// 通常是该分支未设置上游跟踪（@{upstream} 不存在），明确告知根因而非泛化的"获取失败"，
		// 避免用户误以为是网络或权限问题。
		return warn(WarnS("%s 未设置上游跟踪分支（@{upstream} 不存在），跳过同步\n", label),
			"请执行 git branch --set-upstream-to=<remote>/<branch>")
	}
	remote = strings.TrimSpace(remote)

	base, err := git.RunContext(ctx, e.Path, "merge-base", "HEAD", "@{upstream}")
	if err != nil {
		return warn(WarnS("%s 获取共同祖先失败\n", label), "请检查仓库状态")
	}
	base = strings.TrimSpace(base)

	// 第四步：比较决策
	if local == remote {
		return info(InfoS("%s 本地与远程一致，无需处理\n", label))
	} else if local == base {
		// 本地落后于远程，且历史线性 → 可以用 fast-forward
		output := WarnS("%s 检测到线性更新，正在拉取...\n", label)
		_, err := git.RunContext(ctx, e.Path, "pull", "--ff-only")
		if err != nil {
			return warn(output+ErrorS("%s 拉取失败: %s\n", label, err), "请手动 git pull")
		}
		return info(output + SuccessS("%s 拉取成功\n", label))
	} else if remote == base {
		return warn(WarnS("%s 本地领先于远程，请手动推送\n", label), "请手动 git push")
	} else {
		return warn(ErrorS("%s 非线性更新，必须手动处理\n", label), "请手动合并或 rebase")
	}
}

func init() {
	rootCmd.AddCommand(syncCmd)
}
