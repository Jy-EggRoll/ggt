// worktree_attach.go 把主工作区里被 git 忽略的内容接进新工作树。
//
// 新建工作树只带得走版本库里的东西，而本地要真正跑起来还缺两类：
// 体积大、装一遍很贵的目录（依赖目录），与体积小、但必须是真文件的本地配置。
// VSCode 新建工作树时同样补这两类，这里照抄它的一条硬口径：只处理被 git 忽略的路径。
// 少了这条，任何一个被跟踪的文件都会被复制或链接成工作树里的改动，
// 用户在界面上看到的是自己没碰过的文件变成了“已修改”。
//
// 清单一律来自配置项，本文件不写死任何目录名或文件名：哪些目录该链接、哪些文件该复制
// 取决于各仓库自己的技术栈，内置一份清单既替用户做了决定，也必然漏掉他真正需要的那几种。
package git

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// AttachMode 说明把被忽略的内容接进新工作树的方式。
type AttachMode int

const (
	// AttachLink 建符号链接，用于体积大的目录：这类内容重装一遍很贵，而两棵工作树
	// 共用同一份依赖通常也没问题（区别只在于主工作区改了依赖，新工作树立刻跟着变）
	AttachLink AttachMode = iota
	// AttachCopy 复制，用于本地的小文件：这类文件会被开发工具打开甚至改写，
	// 链接过去等于让新工作树里的改动直接改写主工作区那份，用户不会预期这种事
	AttachCopy
)

// 接入过程中的失败原因。
//
// 一律做成哨兵错误，由 cmd 层翻成人话：本层不依赖 l10n，也不知道界面用哪种语言
// （与 ErrEmptyBranchName 同一口径）。
var (
	// ErrAttachInvalidPattern 表示清单里的写法不可能是仓库内的相对路径
	ErrAttachInvalidPattern = errors.New("attach pattern is not a relative path inside the repository")
	// ErrAttachListFailed 表示问不出仓库里有哪些被忽略的路径
	ErrAttachListFailed = errors.New("failed to list the ignored paths")
	// ErrAttachExists 表示新工作树里已经有同名的东西。刻意不覆盖：目录已经存在时，
	// 链接要往里逐条补，复制要决定合并策略——两者都不是这个开关能替用户决定的事
	ErrAttachExists = errors.New("the target already exists in the worktree")
	// ErrAttachSourceSymlink 表示主工作区里这一条本身就是符号链接。
	// 链过去会得到“指向链接的链接”，复制则会把链接外的目标内容抄进工作树，
	// 两种都超出“照搬主工作区”的意图，因此跳过
	ErrAttachSourceSymlink = errors.New("the source is a symbolic link")
	// ErrAttachSourceKind 表示这一条的类型与开关处理的对象不符：链接只处理目录、
	// 复制只处理普通文件。清单里把两类写混不该让整次 add 失败，但确实什么都没做成
	ErrAttachSourceKind = errors.New("the source is not the kind of entry this option handles")
	// ErrAttachLinkFailed 表示链接没建成。不退化成复制：退化的代价正是链接要避免的
	// （依赖目录可能上千个文件、几百兆），而且失败原因通常是目标文件系统不支持符号链接，
	// 在那里复制一份也未必可用
	ErrAttachLinkFailed = errors.New("failed to create the symbolic link")
	// ErrAttachCopyFailed 表示复制没完成
	ErrAttachCopyFailed = errors.New("failed to copy the file")
)

// AttachOutcome 是一条被忽略路径的处理结果。
type AttachOutcome struct {
	// Rel 是相对仓库根的路径，用 / 分隔，与清单里的写法同一形态，便于用户照抄回配置
	Rel string
	// Err 为 nil 表示已接好；否则是上面某个哨兵错误，由调用方决定怎么展示。
	// 用错误而不是布尔值，是为了让“目标已存在”与“复制失败”在界面上不是同一句话——
	// 前者是正常情况，后者要用户去看磁盘
	Err error
}

// AttachIgnoredPaths 把仓库里被忽略、且命中 patterns 的路径接进 wtPath。
//
// 调用前提：wtPath 已是一棵建好的工作树。整体失败只发生在“问不出被忽略清单”这一种情况，
// 单条路径的问题一律记进返回值，因为一次 add 不该被某一个不需要的路径拖垮。
func AttachIgnoredPaths(ctx context.Context, repoPath, wtPath string, patterns []string, mode AttachMode) ([]AttachOutcome, error) {
	cleaned := make([]string, 0, len(patterns))
	for _, p := range patterns {
		rel, ok := cleanAttachPattern(p)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrAttachInvalidPattern, strings.TrimSpace(p))
		}
		cleaned = append(cleaned, rel)
	}
	if len(cleaned) == 0 {
		return nil, nil
	}

	// 候选清单由 git 自己给出，被忽略与否因此只有一处判定：自己解析 .gitignore
	// （含仓库级 exclude、全局 core.excludesFile、父子目录里的多份 .gitignore）
	// 等于重写一遍 git 的忽略规则，迟早出现“git 说不忽略、我们说过忽略”这种分歧。
	// --directory 让整个被忽略的目录折叠成一条（node_modules/），依赖目录因此只出现一次，
	// 而不是把里面几万个文件都列出来
	out, err := RunContext(ctx, repoPath, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAttachListFailed, err)
	}

	seen := make(map[string]bool)
	var results []AttachOutcome
	// -z 让路径按 NUL 分隔且不做引号转义，含空格或中文的路径因此不会走样
	for _, candidate := range strings.Split(out, "\x00") {
		rel := strings.TrimSuffix(strings.TrimRight(candidate, "/"), "/")
		if rel == "" || !matchesAttachPattern(cleaned, rel) {
			continue
		}
		if seen[rel] {
			continue
		}
		seen[rel] = true
		results = append(results, attachOne(repoPath, wtPath, rel, mode))
	}
	return results, nil
}

// cleanAttachPattern 把清单里的写法规整成相对仓库根的路径，并挡住路径穿越。
//
// 清单是用户手写的（也常从别处粘贴而来），而它会被拼进工作树里的目标路径：
// 绝对路径与 ../ 放过去就等于允许写到仓库外面，而“把 /etc/passwd 复制进工作树”
// 这种事没有任何提示，用户只会看到一次成功的 add。
func cleanAttachPattern(p string) (string, bool) {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" || filepath.IsAbs(trimmed) || filepath.IsAbs(filepath.FromSlash(trimmed)) {
		return "", false
	}
	// Windows 上的盘符写法（C:foo）在 Linux 上不是绝对路径，但它同样不属于仓库内的相对路径
	if strings.Contains(trimmed, ":") {
		return "", false
	}
	rel := strings.Trim(strings.TrimPrefix(filepath.ToSlash(trimmed), "./"), "/")
	if rel == "" || rel == "." {
		return "", false
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return "", false
		}
	}
	return rel, true
}

// matchesAttachPattern 判定一条被忽略路径是否命中清单里的某一条。
//
// 认三种写法：整条相等（node_modules）、目录前缀（dist 命中 dist/cache）、
// 以及单层通配（*.log）。刻意不实现 ** 与花括号那套完整 glob 语法：
// 那等于把一门模式语言引进来，而这里真正要表达的只有“某个目录”或“某类文件”两种意思。
func matchesAttachPattern(patterns []string, rel string) bool {
	for _, p := range patterns {
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
		if ok, err := filepath.Match(p, rel); err == nil && ok {
			return true
		}
	}
	return false
}

// attachOne 处理单条路径，路径已经过 cleanAttachPattern 与命中判定。
func attachOne(repoPath, wtPath, rel string, mode AttachMode) AttachOutcome {
	fail := func(sentinel error) AttachOutcome {
		return AttachOutcome{Rel: rel, Err: sentinel}
	}
	src := filepath.Join(repoPath, filepath.FromSlash(rel))
	info, err := os.Lstat(src)
	if err != nil {
		return fail(fmt.Errorf("%w: %v", ErrAttachCopyFailed, err))
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fail(ErrAttachSourceSymlink)
	}
	if mode == AttachLink && !info.IsDir() {
		return fail(ErrAttachSourceKind)
	}
	if mode == AttachCopy && !info.Mode().IsRegular() {
		return fail(ErrAttachSourceKind)
	}

	dst := filepath.Join(wtPath, filepath.FromSlash(rel))
	if _, err := os.Lstat(dst); err == nil {
		return fail(ErrAttachExists)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fail(fmt.Errorf("%w: %v", ErrAttachCopyFailed, err))
	}

	if mode == AttachLink {
		// 用符号链接而不是硬链接：硬链接建不起目录，而且它会跟着 inode 走，
		// 主工作区删掉文件时新工作树里那份会静默变成“还有内容的孤儿”
		if err := os.Symlink(src, dst); err != nil {
			return fail(fmt.Errorf("%w: %v", ErrAttachLinkFailed, err))
		}
		return AttachOutcome{Rel: rel}
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fail(fmt.Errorf("%w: %v", ErrAttachCopyFailed, err))
	}
	if err := copyFile(src, dst); err != nil {
		return fail(fmt.Errorf("%w: %v", ErrAttachCopyFailed, err))
	}
	return AttachOutcome{Rel: rel}
}

// copyFile 复制普通文件并保留可执行位。
//
// 权限刻意对齐源文件而不是写死 0o644：清单里可能包含本地脚本（被忽略的 *.sh），
// 丢了可执行位之后它在新工作树里跑不起来，而那种症状看起来与这次复制毫无关系
func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		// 半截文件比没有文件更糟：它看起来是一份可用的配置，实际少了一半内容。
		// 删掉失败残留，让用户下次重试时不会被“目标已存在”挡住
		os.Remove(dst)
		return err
	}
	return out.Close()
}

// AttachSummary 汇总一次接入的结果，供调用方判断有没有需要告诉用户的内容。
//
// 单独给出计数而不是让调用方自己遍历：成功与失败的分界只有一处定义，
// 免得两处对“什么算失败”各判一次。
func AttachSummary(outcomes []AttachOutcome) (done int, problems []AttachOutcome) {
	for _, o := range outcomes {
		if o.Err == nil {
			done++
			continue
		}
		problems = append(problems, o)
	}
	return done, problems
}
