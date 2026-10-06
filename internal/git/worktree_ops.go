// worktree_ops.go 提供工作树的创建、删除与清理。
//
// 与 worktree.go 的分工：那个文件只负责“看”（发现与解析），这个文件负责“动”。
// 分开是为了让发现那一路保持只读——它每 5 秒就跑一次，写操作不该混在里面。
package git

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// 分支名校验的两种失败。
//
// 拆成哨兵错误而不是在这里拼一句人话：本层不该知道界面用哪种语言，
// 文案由 cmd 层根据 errors.Is 翻译。先前的写法直接返回中文，
// 结果是迁移清单里多出两条“未被翻译机制覆盖”的文案
var (
	ErrEmptyBranchName   = errors.New("branch name is empty")
	ErrInvalidBranchName = errors.New("git rejected the branch name")
)

// ValidateBranchName 用 git 自己的规则校验分支名。
//
// 不自己写正则：git 允许与禁止的形态相当琐碎（不能以点开头、不能有连续的点、
// 不能以 .lock 结尾、不能含 ~ ^ : ? * [ 等等），自己实现迟早与 git 不一致，
// 而不一致的后果是“界面放行了，git 却拒绝”，用户看到的是一句看不懂的 git 报错。
//
// 这里同时挡住了路径穿越：分支名里带 ../ 的形态过不了 check-ref-format，
// 而分支名随后会被拼进工作树的目标路径，放过去就等于允许写到仓库外面。
func ValidateBranchName(ctx context.Context, repoPath, branch string) error {
	if strings.TrimSpace(branch) == "" {
		return ErrEmptyBranchName
	}
	if _, err := runWithCombinedOutput(ctx, repoPath, "check-ref-format", "--branch", branch); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidBranchName, strings.TrimSpace(branch))
	}
	return nil
}

// DefaultWorktreePath 给出一棵新工作树应当放在哪里。
//
// 位置照 VSCode 的默认：<仓库的父目录>/<仓库名>.worktrees/<分支名>，例如
// /home/me/repo 上建分支 feat/x，落在 /home/me/repo.worktrees/feat/x。
//
// 放在仓库外面而不是里面，有两个原因：建在里面的话主仓库会把它当成一大片未跟踪内容
// 显示出来，同一份改动在界面上出现两次；而且 git status 每轮都要多扫一整棵树。
//
// 分支名里的斜杠原样保留成子目录，不替换成连字符：替换之后 feat/x 与 feat-x 会撞到
// 同一个路径，而它们是完全不同的分支。
//
// branch 必须先过 ValidateBranchName，否则这里拼出来的路径不可信。
func DefaultWorktreePath(repoPath, branch string) string {
	repoPath = filepath.Clean(repoPath)
	return filepath.Join(
		filepath.Dir(repoPath),
		filepath.Base(repoPath)+".worktrees",
		filepath.FromSlash(branch),
	)
}

// AddWorktree 在仓库上新建一棵工作树。
//
// createBranch 为真时用 -b 从当前 HEAD 新建分支，为假时检出已有分支——
// 这两件事 git 用的是不同的参数形态，不能靠“分支存不存在”去猜：检出已有分支时
// 如果分支被别的目录占着，git 会拒绝，而我们猜错的话报出来的错与真实原因无关。
//
// 返回 git 的原话（成功时通常为空），失败时由调用方决定怎么展示。
func AddWorktree(ctx context.Context, repoPath, path, branch string, createBranch bool) (string, error) {
	if err := ValidateBranchName(ctx, repoPath, branch); err != nil {
		return "", err
	}

	// 先记下这个分支原本在不在。建分支的 add 一旦失败（目标目录被占住是最常见的
	// 原因），git 并不会把刚建出来的分支收回去，于是留下一条没挂在任何工作树上的
	// 孤儿分支；用户改用新建分支重试同名时，拿到的是一句
	// "A branch named 'x' already exists"，原因与他的操作看起来毫无关系
	existed := createBranch && branchExists(ctx, repoPath, branch)

	args := []string{"worktree", "add"}
	if createBranch {
		args = append(args, "-b", branch, path)
	} else {
		args = append(args, path, branch)
	}
	out, err := RunCombinedContext(ctx, repoPath, args...)
	if err != nil && createBranch && !existed {
		// 尽力回滚，回滚失败也不替换主错误：用户要看的是 add 为什么没成。
		// 用 -d 而不是 -D：这个分支刚建出来、还没有独立提交，
		// 万一它其实带着内容，宁可留着也不要强删
		_, _ = RunCombinedContext(ctx, repoPath, "branch", "-d", "--", branch)
	}
	return out, err
}

// branchExists 判断本地是否已有这个分支。
//
// 用 rev-parse --verify 而不是 branch --list：后者要解析输出文本，
// 而分支名可以包含空格或前缀相似的写法，按行比对迟早出错。
func branchExists(ctx context.Context, repoPath, branch string) bool {
	_, err := runWithCombinedOutput(ctx, repoPath, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// RemoveWorktree 删掉一棵工作树。
//
// force 为真时加 --force：工作树里还有未提交改动或未跟踪文件时，git 默认拒绝删除，
// 而这个拒绝是对的——那些内容不在任何提交里，删掉就真的没了。因此调用方必须把
// force 做成一次明确的确认，不能默认打开。
func RemoveWorktree(ctx context.Context, repoPath, wtPath string, force bool) (string, error) {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, "--", wtPath)
	return RunCombinedContext(ctx, repoPath, args...)
}

// PruneWorktrees 清掉 git 认为目录已经不在了的工作树记录。
//
// 只清理元信息，不碰任何目录：目录既然已经不在了，这条记录留着只会让看板上
// 一直挂着一个采集失败的行。加锁的工作树不会被清理，那是 git 的既定行为。
func PruneWorktrees(ctx context.Context, repoPath string) (string, error) {
	return RunCombinedContext(ctx, repoPath, "worktree", "prune")
}
