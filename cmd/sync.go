package cmd

import (
	"context"
	"strings"

	"ggt/internal/git"
	"ggt/internal/worker"
	"github.com/spf13/cobra"
)

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
		repos := MustGetRepoList()
		Infof("共 %d 个仓库，开始同步...\n", len(repos))

		results := worker.Map(context.Background(), repos, GetConfig().Concurrency, syncRepo)
		for _, r := range results {
			PrintRaw(r)
		}
		DoneBanner("所有仓库同步完成")
	},
}

// syncRepo 同步单个仓库：检查脏状态 → fetch → 分析 commit 关系 → 自动拉取或给出建议。
// 接收上层 ctx 以便任务被整体取消时立即中断 git 调用。
func syncRepo(ctx context.Context, repoPath string) string {
	name := getRepoName(repoPath)

	// 第一步：检查工作目录是否干净（本地操作，快速返回）
	status, err := git.RunContext(ctx, repoPath, "status", "--porcelain")
	if err != nil {
		return WarnS("[%s] 检查状态失败: %s\n", name, err)
	}

	if strings.TrimSpace(status) != "" {
		return WarnS("[%s] 本地有未提交的更改，必须手动处理\n", name)
	}

	// 第二步：拉取远程最新数据，修剪已删除的远程分支
	_, err = git.RunContext(ctx, repoPath, "fetch", "--all", "--prune")
	if err != nil {
		return WarnS("[%s] fetch 失败: %s\n", name, err)
	}

	// 第三步：获取三个关键 commit hash
	local, err := git.RunContext(ctx, repoPath, "rev-parse", "HEAD")
	if err != nil {
		return WarnS("[%s] 获取本地 HEAD 失败\n", name)
	}
	local = strings.TrimSpace(local)

	remote, err := git.RunContext(ctx, repoPath, "rev-parse", "@{upstream}")
	if err != nil {
		return WarnS("[%s] 获取远程分支失败\n", name)
	}
	remote = strings.TrimSpace(remote)

	base, err := git.RunContext(ctx, repoPath, "merge-base", "HEAD", "@{upstream}")
	if err != nil {
		return WarnS("[%s] 获取共同祖先失败\n", name)
	}
	base = strings.TrimSpace(base)

	// 第四步：比较决策
	if local == remote {
		return InfoS("[%s] 本地与远程一致，无需处理\n", name)
	} else if local == base {
		// 本地落后于远程，且历史线性 → 可以用 fast-forward
		output := WarnS("[%s] 检测到线性更新，正在拉取...\n", name)
		_, err := git.RunContext(ctx, repoPath, "pull", "--ff-only")
		if err != nil {
			output += ErrorS("[%s] 拉取失败: %s\n", name, err)
		} else {
			output += SuccessS("[%s] 拉取成功\n", name)
		}
		return output
	} else if remote == base {
		return WarnS("[%s] 本地领先于远程，请手动推送\n", name)
	} else {
		return ErrorS("[%s] 非线性更新，必须手动处理\n", name)
	}
}

func init() {
	rootCmd.AddCommand(syncCmd)
}
