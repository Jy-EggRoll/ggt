// worktree.go 提供 git worktree（工作树）的发现能力。
//
// 为什么需要单独一层：一棵工作树有自己的 HEAD 与索引，它的工作区改动只体现在它自己身上，
// 主仓库的 status 完全看不见。所以 ggt 必须把每棵工作树当成独立的采集对象，
// 而不是主仓库的一个属性——否则“Agent 在另一个工作区里改的东西”永远不出现在看板上。
//
// 发现方式用 `git worktree list --porcelain`，一条命令拿到全部工作树的路径、HEAD、
// 分支以及附加状态（detached / locked / prunable / bare）。不自行解析 .git/worktrees
// 目录树：那种做法拿不到 locked 与 prunable，区分不了“正常挂着”和“已经失效”，
// 而失效的工作树一旦被当成正常对象采集，就会每轮都报一次错。
//
// 性能：调用前先用 HasWorktrees 探测 .git/worktrees 是否存在。实测一批仓库里往往
// 只有个别几个建过工作树，这一步让其余仓库完全零开销（连 git 进程都不启动）。
package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// Worktree 是一棵工作树的元信息。
type Worktree struct {
	// Path 是工作区根目录，取自 git 输出。主工作区也在列表里，其 Path 等于仓库根目录。
	Path string
	// Branch 是短分支名（如 "main"、"feat/x"）。Detached 为真时为空。
	Branch string
	// Head 是当前提交的 SHA。detached 时这是它唯一的“我在哪”信息。
	Head string
	// Detached 表示 HEAD 没有指向任何分支。
	// 这是 Agent 建工作树的常见形态（临时试一下就走），必须与“采集失败”区别对待。
	Detached bool
	// Locked 表示被 git worktree lock 锁住。锁住的工作树不会被自动 prune，
	// 所以即使它的目录已经不存在，git 仍然会把它列出来。
	Locked bool
	// LockReason 是加锁时给出的原因，可为空。
	LockReason string
	// Prunable 表示 git 认为这棵工作树的目录已经不在、可以清理。
	// 这种工作树不能再去采集状态，否则每轮都失败。
	Prunable bool
	// PruneReason 是 git 给出的原因，可为空。
	PruneReason string
	// Bare 表示这是裸工作树（没有工作区）。
	Bare bool
}

// HasWorktrees 判断一个仓库是否可能有额外的工作树，用来避免无谓的 git 调用。
//
// 只做一次 os.Stat：`.git/worktrees` 目录存在才说明这个仓库建过工作树。
// 它不存在有两种情况——从没建过，或 .git 本身是个文件（子模块、以及工作树自身），
// 两种情况都应当跳过，所以不必区分，Stat 失败即返回 false。
//
// 注意措辞是“可能”：工作树被删掉后 git 会顺手清掉这个目录，所以不存在就是真的没有；
// 反过来目录存在也只说明建过，仍要跑一次 list 才能确定现在还剩几棵。
func HasWorktrees(repoPath string) bool {
	info, err := os.Stat(filepath.Join(repoPath, ".git", "worktrees"))
	return err == nil && info.IsDir()
}

// ListWorktrees 列出仓库的全部工作树，主工作区排在第一位（git 的输出顺序如此）。
//
// 不做缓存：调用方每轮本来就是全量采集，而工作树恰恰是随时增删的东西，
// 缓存只会让“刚建好的那棵”迟迟不出现。
func ListWorktrees(ctx context.Context, repoPath string) ([]Worktree, error) {
	out, err := runWithOutput(ctx, repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parseWorktreeList(out), nil
}

// parseWorktreeList 解析 `git worktree list --porcelain` 的输出。
//
// 输出是一串以空行分隔的记录，每条形如：
//
//	worktree <路径>
//	HEAD <sha>
//	branch refs/heads/<名字>       （detached 时这一行换成 detached）
//	locked [<原因>]                （可选）
//	prunable [<原因>]              （可选）
//	bare                           （可选）
//
// 按行首的关键字切分，不按空格拆字段：路径里可以有空格（实测本机的工作树就放在
// /tmp/xxx-wt/ 这类目录下），按空格拆会把路径截断。
func parseWorktreeList(out string) []Worktree {
	var result []Worktree
	var cur *Worktree

	flush := func() {
		if cur != nil {
			result = append(result, *cur)
			cur = nil
		}
	}

	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			flush()
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			// 新记录开始。这里也 flush 一次，防止 git 在最后一条之后没有空行。
			flush()
			cur = &Worktree{Path: value}
		case "HEAD":
			if cur != nil {
				cur.Head = value
			}
		case "branch":
			if cur != nil {
				cur.Branch = strings.TrimPrefix(value, "refs/heads/")
			}
		case "detached":
			if cur != nil {
				cur.Detached = true
			}
		case "locked":
			if cur != nil {
				cur.Locked = true
				cur.LockReason = value
			}
		case "prunable":
			if cur != nil {
				cur.Prunable = true
				cur.PruneReason = value
			}
		case "bare":
			if cur != nil {
				cur.Bare = true
			}
		}
	}
	flush()
	return result
}
