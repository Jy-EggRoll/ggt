// status.go 提供工作区状态的“机器可读”采集与解析，供终端命令与本机 WebUI 共用。
//
// 为什么要在 git 包里单独做这份结构化采集，而不是让各命令自己解析 git 文本：
//   - summary 原先用 strings.Contains(output, "[ahead") 从“给人看”的 --short 文本里
//     反推 ahead，这是对展示格式的隐式依赖。git 一旦调整措辞或格式（或用户配了
//     status.relativePaths / color.status），判断就会静默失效——最坏的表现不是报错，
//     而是“看起来一切正常，只是所有仓库都判错了”
//   - WebUI 需要把同一份状态以 JSON 输出。若在那里再写一套解析，同一语义就出现三份
//     实现，三者迟早漂移
//
// 为什么用 --porcelain=v2 -z --branch，而不是 --short，也不是 v1：
//   - --short 是面向人的格式，不构成契约：实测含空格或非 ASCII 的路径会被加引号，
//     并使用 "->" 表示重命名，解析方必须猜测与反转义
//   - -z 隐含关闭全部转义（不受 core.quotepath 影响），路径以 NUL 原样分隔，是唯一
//     不需要反转义的形态。若不传 -z，porcelain 会把非 ASCII 路径输出成八进制转义
//     （实测 "new M-dM-8M--M-fM-^VM-^G.txt"）
//   - v2 相比 v1 多出 `# branch.*` 头记录：一次调用即可拿到当前分支、上游、领先/落后
//     提交数。v1 的领先/落后只能从 "[ahead 1, behind 1]" 这段人类可读文本里正则提取，
//     且无上游时该段直接消失，判断“有没有上游”必须靠“行里有没有 ...”来间接推断
//   - 代价是 v2 的记录类型更多（普通变更 1 / 重命名 2 / 未合并 u / 未跟踪 ? / 忽略 !），
//     解析要按类型分派。这份复杂度被本文件一次性吸收，调用方只面对结构体
//
// 海量输出的处理：某个仓库若有大量未忽略的未跟踪文件（例如忘了写 .gitignore 的
// node_modules），status 输出可能达到几十 MB。上游 VSCode 的做法是流式解析、条目数超过
// statusLimit（默认 10000）就杀掉子进程，并把已经解析到的部分照常返回、把 didHitLimit
// 报到界面（git.ts:2784-2793）。本文件采用同一套做法，见 statusLimit 与 git.go 的
// runWithRecordLimit；截断会体现在 Status.LimitHit 上。
package git

import (
	"context"
	"strconv"
	"strings"
)

// StatusFile 描述一个变更文件，对应 porcelain v2 的一条变更记录。
type StatusFile struct {
	// Index 是暂存区相对 HEAD 的状态字符（v2 记录中的 XY 的 X 位）
	// 典型取值：M 修改、A 新增、D 删除、R 重命名、C 复制；未跟踪与忽略为 ? 与 !
	Index string
	// Work 是工作区相对暂存区的状态字符（XY 的 Y 位），含义同 Index
	Work string
	// Path 是相对仓库根目录的路径；重命名或复制时为变更后的新路径
	Path string
	// OrigPath 仅在重命名或复制时非空，为变更前的旧路径。
	// 注意在 -z 格式下它是紧随其后的一条独立 NUL 记录（顺序与 --short 的
	// "旧 -> 新" 相反），本文件的解析已把这一反转处理掉
	OrigPath string
	// Untracked 表示未跟踪文件（v2 中为 "? <path>" 记录）
	Untracked bool
	// Unmerged 表示处于未合并状态（v2 中为 "u ..." 记录，即存在冲突）
	Unmerged bool
	// Ignored 表示被忽略的文件（v2 中为 "! <path>" 记录）
	// 默认采集不要求列出忽略文件，因此通常为空；保留该字段是为了调用方将来需要时
	// 不必改解析逻辑
	Ignored bool
}

// Status 是一个仓库的工作区状态快照。
//
// 它是纯数据：不含仓库路径与展示名。仓库身份由调用方持有的 RepoEntry 表达，
// 这样 git 包不必反向依赖命令层对“仓库”的抽象。
type Status struct {
	// Branch 是当前分支名；游离 HEAD 或仓库尚无提交时为空字符串，需结合
	// Detached / NoCommits 判断具体情形
	Branch string
	// Upstream 是上游分支名（如 origin/main）；无上游时为空
	Upstream string
	// Detached 表示 HEAD 处于游离状态（v2 头记录为 "# branch.head (detached)"）
	Detached bool
	// NoCommits 表示仓库尚无任何提交（v2 头记录为 "# branch.oid (initial)"）
	NoCommits bool
	// Ahead 是相对上游领先的提交数（v2 头记录 "# branch.ab +N -M" 中的 N）
	Ahead int
	// Behind 是相对上游落后的提交数（同一条头记录中的 M，已转为非负值）
	Behind int
	// Files 是全部变更条目，顺序与 git 输出一致（git 自身按路径排序）
	Files []StatusFile
	// LimitHit 表示本次采集因为条目数超过 statusLimit 而被提前截断，Files 只是前面一段。
	// 之所以要把这件事报出来：截断本身是静默的，界面若不提示，用户会以为"这个仓库就这么多
	// 改动"。上游同样把 didHitLimit 一路报到界面（git.ts:2793）
	LimitHit bool
}

// statusLimit 是单次 status 采集允许的条目上限，超过就终止 git 子进程并截断结果。
//
// 取值与上游一致（VSCode 的 git.statusLimit 默认 10000，见 git.ts:2784 与
// repository.ts:2964），语义也一致：0 表示不限制。这里先做成常数而不是接进配置文件：
// ggt 现有的同类上限（看板的分页大小、泳道图的最大条数）也都是常数，配置项留到真有人要
// 调整时再加，免得为一个从没被改过的旋钮多维护一份配置。
const statusLimit = 10000

// RunStatus 采集指定仓库的工作区状态快照。
//
// 参数说明与取舍：
//   - --porcelain=v2 -z：机器可读契约，路径不做任何转义，详见文件头
//   - --branch：输出 # branch.* 头记录，一次拿到分支、上游与领先/落后
//   - --untracked-files=all：逐文件列出未跟踪文件。必须显式给出 all，因为 -z 与
//     v2 下若用默认的 normal，git 会把整个未跟踪目录折叠成一条 "dir/" 记录，页面
//     上就会少掉大量文件。这一取值也与 ggt 其余命令的口径一致（--untracked-files
//     不带值时 git 的默认即为 all，见 git status -h）
//   - GIT_OPTIONAL_LOCKS=0：禁止 git 在 status 期间写入 index（刷新 stat 缓存）。
//     批量轮询几十个仓库时，若允许写 index，就可能与用户自己的 git add/commit 抢
//     index.lock，表现为偶发的 "Unable to create ... index.lock: File exists"。
//     这是上游 VSCode 对 status 采用的同一做法（git.ts:2745）
//
// 并发由调用方决定：本函数只负责一次采集，不做任何缓存。WebUI 的轮询接口需要在
// 自己那层做缓存与并发控制，否则每个页面请求都会重跑全部仓库。
//
// 条目数超过 statusLimit 时会被截断，截断结果照常返回，同时把 LimitHit 置为 true
func RunStatus(ctx context.Context, repoPath string) (*Status, error) {
	return runStatus(ctx, repoPath, statusLimit)
}

// runStatus 是 RunStatus 的实现。limit 之所以做成参数，是为了让测试能注入一个很小的值
// （否则要构造一万个文件才能覆盖到截断这条路径）；外部的默认上限见 statusLimit。
//
// limit 取 0 表示不限制，与上游 git.ts:2788 的 "limit !== 0" 判断同义。
func runStatus(ctx context.Context, repoPath string, limit int) (*Status, error) {
	output, hitLimit, err := runWithRecordLimit(ctx, repoPath, []string{"GIT_OPTIONAL_LOCKS=0"}, limit,
		"status", "--porcelain=v2", "-z", "--branch", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	st := ParseStatus(output)
	st.LimitHit = hitLimit
	return st, nil
}

// ParseStatus 解析 `git status --porcelain=v2 -z --branch` 的输出。
//
// 记录以 NUL 分隔，分两类：
//   - 头记录（以 "# " 开头）：branch.oid / branch.head / branch.upstream / branch.ab
//   - 变更记录，首字符是记录类型（下列段数均不含这个类型首字符）：
//     "1" 普通变更：XY sub mH mI mW hH hI path（8 段）
//     "2" 重命名/复制：在 1 的字段之后多一段相似度 X<score>，且 -z 下旧路径是紧随
//     其后的另一条 NUL 记录（9 段 + 1 条后续记录）
//     "u" 未合并：XY sub m1 m2 m3 mW h1 h2 h3 path（10 段）
//     "?" 未跟踪：path
//     "!" 忽略：path
//
// 路径一律取各类型记录的最后一段，且必须用“限制段数的切分”（SplitN）：路径本身
// 可以包含空格，按全部空格切分会把一个路径拆成多段，从而截断成错误的名字。
// 这也正是 -z 存在的意义——换成非 -z 的 porcelain，还要额外处理引号与八进制转义
//
// 本函数是纯函数、不做 I/O，便于用固定样本覆盖各种形态；格式不符预期的记录一律
// 跳过而非猜测，避免把异常内容当作路径展示给用户。
func ParseStatus(output string) *Status {
	st := &Status{}
	records := strings.Split(output, "\x00")
	for i := 0; i < len(records); i++ {
		rec := records[i]
		if rec == "" {
			continue
		}

		if header, ok := strings.CutPrefix(rec, "# "); ok {
			parseV2Header(header, st)
			continue
		}

		kind, rest, _ := strings.Cut(rec, " ")
		switch kind {
		case "1":
			f, ok := parseV2Change(rest, 8, 7)
			if ok {
				st.Files = append(st.Files, f)
			}
		case "2":
			f, ok := parseV2Change(rest, 9, 8)
			if !ok {
				continue
			}
			// 旧路径是紧随其后的另一条 NUL 记录。若它缺失（输出被截断），
			// 保留条目但 OrigPath 为空，宁可少一个字段也不要丢掉整个变更
			if i+1 < len(records) {
				i++
				f.OrigPath = records[i]
			}
			st.Files = append(st.Files, f)
		case "u":
			f, ok := parseV2Change(rest, 10, 9)
			if ok {
				f.Unmerged = true
				st.Files = append(st.Files, f)
			}
		case "?":
			st.Files = append(st.Files, StatusFile{Index: "?", Work: "?", Path: rest, Untracked: true})
		case "!":
			st.Files = append(st.Files, StatusFile{Index: "!", Work: "!", Path: rest, Ignored: true})
		}
	}
	return st
}

// parseV2Change 从 "1" / "2" / "u" 这类记录的剩余部分里取出 XY 与路径。
//
// fields 是记录的期望段数，pathIndex 是路径所在段的下标（从 0 起）；
// 两者都按“去掉记录类型首字符后、以空格分隔”的段来算——例如普通变更记录去掉开头的
// "1 " 之后是 8 段，路径即第 8 段（下标 7）。git 保证路径是最后一段，所以按 fields 段
// 切分后，末尾那一段就是完整路径（含空格也完整）。XY 段必须恰好两个字符，否则视为格式不符。
func parseV2Change(rest string, fields, pathIndex int) (StatusFile, bool) {
	parts := strings.SplitN(rest, " ", fields)
	if len(parts) < fields || len(parts[0]) < 2 {
		return StatusFile{}, false
	}
	return StatusFile{
		Index: parts[0][:1],
		Work:  parts[0][1:2],
		Path:  parts[pathIndex],
	}, true
}

// parseV2Header 解析 v2 的头记录（已去掉 "# " 前缀）。
//
// 实测 git 2.x 的四种头记录：
//   - branch.oid <sha> 或 branch.oid (initial)   后者表示仓库尚无提交
//   - branch.head <name> 或 branch.head (detached)
//   - branch.upstream <name>                     仅在有上游时出现
//   - branch.ab +N -M                            仅在有上游时出现
//
// 未知键直接忽略：git 将来新增头记录字段时，本解析不应因此失效。
func parseV2Header(header string, st *Status) {
	key, value, _ := strings.Cut(header, " ")
	switch key {
	case "branch.head":
		// 游离 HEAD 时 value 是字面量 "(detached)"，直接读 .git/HEAD 或另跑
		// rev-parse 也能判断，但既然 v2 已经在同一次输出里给出，就不再多跑一次 git
		if value == "(detached)" {
			st.Detached = true
			return
		}
		st.Branch = value
	case "branch.oid":
		if value == "(initial)" {
			st.NoCommits = true
		}
	case "branch.upstream":
		st.Upstream = value
	case "branch.ab":
		// 形如 "+1 -2"：分别表示领先与落后上游的提交数。落后数带负号，展示时用非负值
		parts := strings.Fields(value)
		if len(parts) == 2 {
			st.Ahead = parseSignedCount(parts[0])
			st.Behind = parseSignedCount(parts[1])
		}
	}
}

// parseSignedCount 把 branch.ab 里的 "+3" / "-3" 解析为非负整数。
// 无法解析时返回 0——计数属于展示信息，缺失不应影响整次状态采集。
func parseSignedCount(s string) int {
	s = strings.TrimLeft(s, "+-")
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
