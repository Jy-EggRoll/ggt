// 本文件采集提交历史，供看板的分支图使用。
//
// 字段与语义对齐 VSCode 源码管理里那份 ISCMHistoryItem
// （src/vs/workbench/contrib/scm/common/history.ts 的 ISCMHistoryItem）；泳道分配见 graph.go。
// 两个文件都只依赖 git 命令的输出，不碰任何页面概念——页面要什么由 cmd 侧决定
package git

import (
	"context"
	"strconv"
	"strings"
)

// HistoryRef 是一个指向某提交的引用（本地分支 / 远程分支 / tag）
type HistoryRef struct {
	// Name 是短名（main、origin/main、v1.2.0），不带 refs/ 前缀
	Name string `json:"name"`
	// Kind 取 head / remote / tag，配色与排序按它区分（见 graph.go）
	Kind string `json:"kind"`
	// Color 是配色（VSCode 的颜色 id），由 LayoutHistory 填；只有“当前分支”与“它的上游”有值，
	// 其余留空由页面用默认引用色
	Color string `json:"color,omitempty"`
}

// HistoryItem 是一个提交
type HistoryItem struct {
	Hash        string   `json:"hash"`
	Parents     []string `json:"parents"`
	Author      string   `json:"author"`
	AuthorEmail string   `json:"authorEmail"`
	// Timestamp 是提交时间，epoch 秒（与 git 的 %at 一致）。格式化交给页面，按它自己的时区
	Timestamp int64        `json:"timestamp"`
	Subject   string       `json:"subject"`
	Message   string       `json:"message"`
	Refs      []HistoryRef `json:"refs"`
}

// HistoryOptions 是采集参数
type HistoryOptions struct {
	Limit int
	// All 为真时跨全部分支与 tag（git log --all），否则只看当前分支
	All bool
}

// historyLogFormat 是 git log 的输出格式。
//
// 字段之间用 NUL 分隔、提交之间用 RS(0x1e) 分隔：提交信息正文可以出现任何字符，
// 只有 NUL 不会出现在 git 的输出里；RS 是惯用的记录分隔符，正文里同样不会出现。
// 用换行或制表符分隔都会被正文搅乱（正文是多行的），这是“看起来能用、遇到带正文的提交就错行”的典型问题
const historyLogFormat = "%H%x00%P%x00%an%x00%ae%x00%at%x00%s%x00%b%x1e"

// LogHistory 采集一段提交历史，从新到旧。
//
// 排序用 --topo-order（git log --graph 自身的默认排序）：父提交绝不会排在它的子提交之前，
// 泳道因此只需要向下画。--date-order 不保证这一点，遇到时钟回拨或跨时区的仓库会画出往回走的连线
func LogHistory(ctx context.Context, repoPath string, opts HistoryOptions) ([]HistoryItem, error) {
	args := []string{"log", "--topo-order", "--pretty=format:" + historyLogFormat}
	if opts.All {
		args = append(args, "--all")
	}
	if opts.Limit > 0 {
		args = append(args, "-n", strconv.Itoa(opts.Limit))
	}

	out, err := RunContext(ctx, repoPath, args...)
	if err != nil {
		return nil, err
	}

	// 引用读不出来不该让整张图没有内容：refs 只是标签，图本身仍然可读，因此降级成空表
	refs, err := historyRefs(ctx, repoPath)
	if err != nil {
		refs = map[string][]HistoryRef{}
	}
	return parseHistoryLog(out, refs), nil
}

// parseHistoryLog 把 git log 的输出切成提交列表，并挂上每个提交的引用
func parseHistoryLog(out string, refs map[string][]HistoryRef) []HistoryItem {
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return []HistoryItem{}
	}

	items := make([]HistoryItem, 0, 64)
	for _, record := range strings.Split(out, "\x1e") {
		if record == "" {
			continue
		}
		fields := strings.SplitN(record, "\x00", 7)
		if len(fields) < 7 {
			// 字段数不对说明输出被截断或格式被改动，宁可跳过这一条，也不要把半个提交写进图里
			continue
		}
		ts, _ := strconv.ParseInt(strings.TrimSpace(fields[4]), 10, 64)

		var parents []string
		if p := strings.TrimSpace(fields[1]); p != "" {
			parents = strings.Fields(p)
		}

		item := HistoryItem{
			Hash:        strings.TrimSpace(fields[0]),
			Parents:     parents,
			Author:      fields[2],
			AuthorEmail: fields[3],
			Timestamp:   ts,
			Subject:     fields[5],
			Message:     strings.TrimRight(fields[6], "\n"),
			Refs:        refs[strings.TrimSpace(fields[0])],
		}
		if item.Parents == nil {
			item.Parents = []string{}
		}
		if item.Refs == nil {
			item.Refs = []HistoryRef{}
		}
		items = append(items, item)
	}
	return items
}

// LocalBranches 列出现在本地全部分支名，按名字升序（git 的 for-each-ref 默认就按 refname 排）。
//
// 分支选择器用它：只列本地分支——切到远程分支得先建跟踪分支，那是另一件事，
// 替用户决定“要不要顺手建一个”代价很高（建错对象要手动收拾）
func LocalBranches(ctx context.Context, repoPath string) ([]string, error) {
	out, err := RunContext(ctx, repoPath, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, line := range strings.Split(out, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// CommitFile 是一个提交里某个文件的改动量
type CommitFile struct {
	// Path 是改动后的路径；重命名时 OrigPath 是旧路径
	Path     string `json:"path"`
	OrigPath string `json:"origPath,omitempty"`
	Adds     int    `json:"adds"`
	Dels     int    `json:"dels"`
	// Binary 为真表示 git 没给出行数（二进制文件），此时 Adds/Dels 都是 0
	Binary bool `json:"binary"`
}

// CommitFiles 返回一个提交改了哪些文件、各增减多少行。
//
// 用 --numstat -z：-z 让路径以 NUL 结尾，重命名因此能把新旧两个路径分开给。不带 -z 时 git 会把
// 它们拼成 "old => new" 这样一个只能靠猜的字符串（路径里本来就可以出现 " => "）。
// 实测三种记录的形态（--format= 置空是为了不打印提交本身）：
//
//	普通   0\t1\ta.txt\0
//	二进制 -\t-\tbin.dat\0
//	重命名 1\t0\t\0big.txt\0moved.txt\0   （第三个字段为空，紧跟旧、新两个路径）
func CommitFiles(ctx context.Context, repoPath, hash string) ([]CommitFile, error) {
	// 三个“为了让输出可解析”的选项与 diffText 同源：--no-color 去转义、--no-ext-diff 挡住
	// 用户配置的外部 diff 工具、--no-textconv 挡住 textconv 过滤器（它会把这个文件当文本，
	// git 于是不再报 "-"，二进制文件会被算出行数）
	out, err := RunContext(ctx, repoPath, "show", "--numstat", "-z", "--format=",
		"--no-color", "--no-ext-diff", "--no-textconv", hash)
	if err != nil {
		return nil, err
	}
	return parseNumstat(out), nil
}

// DiffNumstat 与 CommitFiles 同义，只是问的是工作区或暂存区（staged 为真取 index vs HEAD，
// 否则取工作区 vs index），paths 非空时只看这几条路径。
//
// 与 CommitFiles 共用解析：两者的输出格式完全一样（都是 --numstat -z），差别只在前面
// 跑的是 diff 还是 show——解析写成两份，正是日后分叉的起点
func DiffNumstat(ctx context.Context, repoPath string, staged bool, paths []string) ([]CommitFile, error) {
	args := []string{"diff", "--numstat", "-z", "--no-color", "--no-ext-diff", "--no-textconv"}
	if staged {
		args = append(args, "--cached")
	}
	if len(paths) > 0 {
		// "--" 之前是选项、之后是路径：少了它，以 - 开头的文件名会被当成选项
		args = append(args, "--")
		args = append(args, paths...)
	}

	out, err := RunContext(ctx, repoPath, args...)
	if err != nil {
		return nil, err
	}
	return parseNumstat(out), nil
}

// parseNumstat 解析 --numstat -z 的输出
func parseNumstat(out string) []CommitFile {
	files := []CommitFile{}
	fields := strings.Split(out, "\x00")

	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if field == "" {
			continue
		}
		parts := strings.SplitN(field, "\t", 3)
		if len(parts) != 3 {
			// 不是 numstat 记录：跳过而不是猜
			continue
		}

		file := CommitFile{}
		if parts[0] == "-" && parts[1] == "-" {
			file.Binary = true
		} else {
			file.Adds, _ = strconv.Atoi(parts[0])
			file.Dels, _ = strconv.Atoi(parts[1])
		}

		if parts[2] != "" {
			file.Path = parts[2]
			files = append(files, file)
			continue
		}

		// 重命名：第三个字段为空，后面紧跟旧路径与新路径。
		// 两个路径都必须存在且非空——输出被截断时宁可丢这一条，也不要拿一个空路径当新路径
		if i+2 >= len(fields) || fields[i+2] == "" {
			break
		}
		file.OrigPath = fields[i+1]
		file.Path = fields[i+2]
		i += 2
		files = append(files, file)
	}
	return files
}

// CountHistory 返回按同样范围能采集到的提交总数，页面用它显示"已显示 X / 共 N"
func CountHistory(ctx context.Context, repoPath string, all bool) (int, error) {
	args := []string{"rev-list", "--count"}
	if all {
		args = append(args, "--all")
	} else {
		args = append(args, "HEAD")
	}
	out, err := RunContext(ctx, repoPath, args...)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, err
	}
	return n, nil
}

// historyRefs 收集“哪个提交上有哪些引用”，供提交行显示分支与 tag 标签。
//
// 用 for-each-ref 而不是 git log --decorate：decorate 把标签拼进提交信息那一行，
// 还得再把括号、颜色转义与"HEAD -> "这类前缀解析回来；for-each-ref 直接给一张表。
//
// 三个字段之间用 NUL 分隔（引用名里可以有空格、感叹号等，用空白分隔会切错）。
// tag 取剥开后的对象名：annotated tag 指向的是 tag 对象而不是提交，只有 %(*objectname) 才是提交
func historyRefs(ctx context.Context, repoPath string) (map[string][]HistoryRef, error) {
	out, err := RunContext(ctx, repoPath, "for-each-ref",
		"--format=%(objectname)%00%(*objectname)%00%(refname)")
	if err != nil {
		return nil, err
	}

	refs := map[string][]HistoryRef{}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.SplitN(line, "\x00", 3)
		if len(fields) != 3 {
			continue
		}
		obj, peeled, name := fields[0], fields[1], fields[2]

		var kind, short string
		switch {
		case strings.HasPrefix(name, "refs/heads/"):
			kind, short = "head", strings.TrimPrefix(name, "refs/heads/")
		case strings.HasPrefix(name, "refs/remotes/"):
			// 远程的 HEAD 是个符号引用（指向该远程的默认分支），它不是“一个分支”，
			// 列出来只会让标签栏多一个 origin/HEAD
			if strings.HasSuffix(name, "/HEAD") {
				continue
			}
			kind, short = "remote", strings.TrimPrefix(name, "refs/remotes/")
		case strings.HasPrefix(name, "refs/tags/"):
			kind, short = "tag", strings.TrimPrefix(name, "refs/tags/")
		default:
			continue
		}

		hash := obj
		if peeled != "" {
			hash = peeled
		}
		refs[hash] = append(refs[hash], HistoryRef{Name: short, Kind: kind})
	}
	return refs, nil
}

// CurrentRefs 返回当前分支名、它的上游引用名与 HEAD 指向的提交。
//
// 上游取不到是常态（新分支还没 -u、detached HEAD），因此不报错：泳道配色只把它当作“有没有”
// 的信息用（见 graph.go 的色表），拿不到就少一种颜色而已
func CurrentRefs(ctx context.Context, repoPath string) (branch, upstream, head string) {
	if out, err := RunContext(ctx, repoPath, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		branch = strings.TrimSpace(out)
	}
	if branch == "HEAD" {
		// detached HEAD：--abbrev-ref 直接给 "HEAD"，那不是分支名
		branch = ""
	}
	if out, err := RunContext(ctx, repoPath, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); err == nil {
		upstream = strings.TrimSpace(out)
	}
	if out, err := RunContext(ctx, repoPath, "rev-parse", "HEAD"); err == nil {
		head = strings.TrimSpace(out)
	}
	return branch, upstream, head
}
