// repos_discovery_test 覆盖 cmd 包“仓库发现”链路的三层函数：
//   - discoverSubmodules：解析 .gitmodules，递归发现已初始化的子模块
//   - expand：把顶层仓库展开成“顶层 + 子模块”的扁平条目
//   - GetRepoList：合并配置里的 repo_paths 与 parent_paths 的扫描结果
//
// 为什么单独测这三个：它们位于 ggt 所有遍历型命令（status/size/sync/fetch...）的入口，
// 而 repos_test.go 只固定住了它们内部依赖的两个纯函数（parseGitmodules / isSubmoduleInitialized）。
// 剩下这三层语义——“递归时相对路径怎么拼”“展开后顺序与 IsSubmodule 标记对不对”
// “父目录下什么才算仓库”——一旦写错，错误会静默扩散到每一条命令，且从终端输出很难看出
// 是哪一层错的。因此这里按层补测，不重复已被覆盖的纯函数。
//
// 三个函数都会读包级全局 cfg（经 GetConfig()），所以每个用例都必须显式替换并在结束时还原，
// 否则状态会泄漏给同包其他测试文件——cfg 是单例，即使测试串行执行也会互相污染
//
// expand 内部会调用 Concurrency()，它要求 cfg 非 nil 且 concurrency 是有效语义串，
// 因此本文件里给 cfg 的 concurrency 一律写死 "2"，不依赖本机 CPU 核数——
// 默认的 CPUHalf 会让并发数随机器变化，从而使测试结果不可复现
//
// 所有文件系统场景一律用 t.TempDir() 搭建，跑完由测试框架自动清理，不在代码目录留垃圾
package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jy-eggroll/ggt/internal/config"
)

// withConfig 在本用例期间把包级全局 cfg 换成指定配置，用例结束时还原原值
// 必须还原的原因：GetConfig()/Concurrency() 读的都是这一个全局指针，
// 用例跑完若留着被改过的配置，后续任何依赖配置的测试都会拿到脏数据而随机失败
// 用 t.Cleanup 而不是直接写 defer，是为了让表驱动里的每个 t.Run 子用例各自负责自己的还原
func withConfig(t *testing.T, c *config.Config) {
	t.Helper()
	old := cfg
	cfg = c
	t.Cleanup(func() { cfg = old })
}

// writeTestFile 写文件并自动补齐父目录，省去每个用例重复 MkdirAll
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建父目录失败: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入文件 %s 失败: %v", path, err)
	}
}

// writeGitmodules 在仓库根目录写入 .gitmodules
// discoverSubmodules 是纯文件 I/O，必须在真实文件系统上造出这个文件才能触发发现逻辑
func writeGitmodules(t *testing.T, repoPath, body string) {
	t.Helper()
	writeTestFile(t, filepath.Join(repoPath, ".gitmodules"), body)
}

// writeSubmoduleDir 构造“已初始化子模块”的最小真实形态：目录存在且非空
// 注意里面刻意把 .git 写成**文件**（内容形如 gitdir: ...）：真实子模块的 .git 就是指向
// 父仓库 .git/modules/... 的 gitdir 文件而非目录，所以这里照原样复刻——
// 这既让 isSubmoduleInitialized 判定它已初始化，也说明子模块的“已初始化”与 git.IsRepo
// 要求的“.git 必须是目录”是两套口径（后者不认这种目录）
func writeSubmoduleDir(t *testing.T, repoPath, rel string) {
	t.Helper()
	dir := filepath.Join(repoPath, rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("创建子模块目录 %s 失败: %v", dir, err)
	}
	writeTestFile(t, filepath.Join(dir, ".git"), "gitdir: ../.git/modules/"+rel+"\n")
	writeTestFile(t, filepath.Join(dir, "README.md"), "子模块内容\n")
}

// makeGitRepoDir 把 dir 造成 git.IsRepo 认可的仓库：.git 必须是**目录**
// GetRepoList 的父目录扫描正是用这个口径筛选子目录，用 .git 文件不算
func makeGitRepoDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("创建 .git 目录失败: %v", err)
	}
}

// sameRepoEntries 按顺序比较两条 RepoEntry 列表的 Path/Name/IsSubmodule 三个字段
// 不复用 reflect.DeepEqual：这里要的是“逐字段相等且顺序一致”，失败时能报出具体差异
func sameRepoEntries(got, want []RepoEntry) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestDiscoverSubmodules 验证子模块发现的四类语义：
//   - 没有 .gitmodules（或其中没有条目）时返回空，命令表现为“无子模块”
//   - 未初始化的子模块被跳过（目录不存在 / 空目录 / 目录尚未 clone）
//   - 已初始化的子模块保持 .gitmodules 里的书写顺序
//   - 嵌套子模块的相对路径逐级正确拼接，且深度优先紧跟其父
//
// 复用同包的 samePaths 助手比较路径列表（故 nil 与空切片等价）：
// 对调用方而言“没有子模块”这一语义与底层用 nil 还是空切片承载无关，
// 同一函数不再重复定义一份，避免重名编译冲突与实现重复
func TestDiscoverSubmodules(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, repo string) // 在临时仓库根目录 repo 下布置场景
		want  []string
	}{
		{
			// 最普通的情况：仓库根本没有子模块，连 .gitmodules 都没有，
			// 此时绝不能报错或产出空串条目，否则调用方会拿到一个指向父仓库自身的路径
			name:  "没有 .gitmodules 文件的仓库返回空",
			setup: func(t *testing.T, repo string) {},
			want:  nil,
		},
		{
			// 反向操作后残留的空 .gitmodules：文件在但没有 [submodule] 块，
			// 与“没有文件”必须同义，否则会多出一个空路径参与 filepath.Join
			name: "只有注释的 .gitmodules 返回空",
			setup: func(t *testing.T, repo string) {
				writeGitmodules(t, repo, "# 子模块列表（当前为空）\n\n")
			},
			want: nil,
		},
		{
			// 正常已初始化的子模块：目录里已有工作区内容，必须被发现，
			// 且返回的是相对父仓库的路径（调用方据此与原路径拼接）
			name: "单个已初始化子模块返回相对路径",
			setup: func(t *testing.T, repo string) {
				writeGitmodules(t, repo, "[submodule \"lib\"]\n\tpath = lib\n\turl = ../lib.git\n")
				writeSubmoduleDir(t, repo, "lib")
			},
			want: []string{"lib"},
		},
		{
			// 只 add 了子模块但没 clone：目录根本不存在。此时若仍返回该路径，
			// 后续 git 命令会作用在不存在的目录上并报错，所以必须跳过
			name: "未初始化的子模块（目录不存在）被跳过",
			setup: func(t *testing.T, repo string) {
				writeGitmodules(t, repo, "[submodule \"missing\"]\n\tpath = missing\n\turl = ../missing.git\n")
			},
			want: nil,
		},
		{
			// clone 中断或被清理后留下的空目录：与“目录不存在”一样没有工作区，
			// 若被当成已初始化，ggt 会对空目录执行 git 操作
			name: "未初始化的子模块（空目录）被跳过",
			setup: func(t *testing.T, repo string) {
				writeGitmodules(t, repo, "[submodule \"empty\"]\n\tpath = empty\n\turl = ../empty.git\n")
				if err := os.MkdirAll(filepath.Join(repo, "empty"), 0o755); err != nil {
					t.Fatalf("创建空目录失败: %v", err)
				}
			},
			want: nil,
		},
		{
			// 混合场景最关键：跳过未初始化的条目之后，剩余条目的顺序仍必须是
			// .gitmodules 的书写顺序（这里刻意写成 b、gone、a 而非字母序），
			// 因为 ggt 的展示与批量执行顺序都直接沿用该顺序
			name: "已初始化与未初始化混合时只保留已初始化的并保持书写顺序",
			setup: func(t *testing.T, repo string) {
				writeGitmodules(t, repo, "[submodule \"b\"]\n\tpath = b\n"+
					"[submodule \"gone\"]\n\tpath = gone\n"+
					"[submodule \"a\"]\n\tpath = a\n")
				writeSubmoduleDir(t, repo, "b")
				writeSubmoduleDir(t, repo, "a")
			},
			want: []string{"b", "a"},
		},
		{
			// 嵌套子模块：sub 自身也带 .gitmodules，nested 已初始化。
			// 父层只应把 nested 的相对路径拼成 sub/nested（相对最初的父仓库），
			// 若直接返回 nested 或绝对路径，调用方 Join 后就会指向错误目录
			name: "嵌套子模块的相对路径逐级拼接",
			setup: func(t *testing.T, repo string) {
				writeGitmodules(t, repo, "[submodule \"sub\"]\n\tpath = sub\n")
				writeSubmoduleDir(t, repo, "sub")
				writeGitmodules(t, filepath.Join(repo, "sub"), "[submodule \"nested\"]\n\tpath = nested\n")
				writeSubmoduleDir(t, repo, "sub/nested")
			},
			want: []string{"sub", filepath.Join("sub", "nested")},
		},
		{
			// 嵌套的嵌套：a 里有 a/b，a/b 里还有 a/b/c。
			// 每层递归都只返回相对自身的路径，最终由各层 Join 拼成 a/b/c——
			// 这是最容易把分隔符或层级数写错的地方
			name: "多层嵌套的相对路径逐级拼接",
			setup: func(t *testing.T, repo string) {
				writeGitmodules(t, repo, "[submodule \"a\"]\n\tpath = a\n")
				writeSubmoduleDir(t, repo, "a")
				writeGitmodules(t, filepath.Join(repo, "a"), "[submodule \"b\"]\n\tpath = b\n")
				writeSubmoduleDir(t, repo, "a/b")
				writeGitmodules(t, filepath.Join(repo, "a", "b"), "[submodule \"c\"]\n\tpath = c\n")
				writeSubmoduleDir(t, repo, "a/b/c")
			},
			want: []string{"a", filepath.Join("a", "b"), filepath.Join("a", "b", "c")},
		},
		{
			// 嵌套层级未初始化时，父层仍应被返回，但不要为不存在的 nested 产出条目。
			// 这一分支能暴露“先递归再判断初始化”这类顺序写反的实现
			name: "子模块已初始化但其嵌套子模块未初始化时只返回父层",
			setup: func(t *testing.T, repo string) {
				writeGitmodules(t, repo, "[submodule \"sub\"]\n\tpath = sub\n")
				writeSubmoduleDir(t, repo, "sub")
				writeGitmodules(t, filepath.Join(repo, "sub"), "[submodule \"nested\"]\n\tpath = nested\n")
			},
			want: []string{"sub"},
		},
		{
			// 顺序契约：结果不是“先所有直接子模块、再所有嵌套子模块”，而是深度优先——
			// a 及其嵌套 a/a1、a/a2 先全部出现，然后才是同级子模块 b。
			// ggt 据此顺序批量执行，写反会导致输出与实际操作顺序不一致
			name: "嵌套条目紧跟其父之后，然后才是下一个同级子模块（深度优先）",
			setup: func(t *testing.T, repo string) {
				writeGitmodules(t, repo, "[submodule \"a\"]\n\tpath = a\n"+
					"[submodule \"b\"]\n\tpath = b\n")
				writeSubmoduleDir(t, repo, "a")
				writeGitmodules(t, filepath.Join(repo, "a"), "[submodule \"a1\"]\n\tpath = a1\n"+
					"[submodule \"a2\"]\n\tpath = a2\n")
				writeSubmoduleDir(t, repo, "a/a1")
				writeSubmoduleDir(t, repo, "a/a2")
				writeSubmoduleDir(t, repo, "b")
			},
			want: []string{"a", filepath.Join("a", "a1"), filepath.Join("a", "a2"), "b"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := t.TempDir()
			c.setup(t, repo)
			if got := discoverSubmodules(repo); !samePaths(got, c.want) {
				t.Errorf("discoverSubmodules(%q) = %#v, 期望 %#v", repo, got, c.want)
			}
		})
	}

	// samePaths 把 nil 与空切片视为等价，但这里额外锁定当前实现的真实返回值：
	// 无 .gitmodules 时返回 nil。显式记录是为了将来有人把它改成空切片时能意识到
	// 这是“返回值形态变化”而非语义变化，从而判断下游是否有依赖
	t.Run("无 .gitmodules 时底层返回 nil 而非空切片", func(t *testing.T) {
		repo := t.TempDir()
		if got := discoverSubmodules(repo); got != nil {
			t.Errorf("无 .gitmodules 时应返回 nil, 实际 %#v", got)
		}
	})
}

// TestExpand 验证“顶层 + 子模块”展开后的条目内容与顺序：
//   - ignoreSubmodules=true 时只返回顶层条目，且 IsSubmodule 全为 false
//   - 顶层条目 Name 为目录名，子模块条目 Name 为相对父仓库的路径
//   - 顶层条目全部在前，子模块按父仓库的配置顺序依次追加
//   - 子模块的 Path 为父仓库路径与相对路径拼接的结果
//
// 每个用例显式给 cfg 设 concurrency="2"：expand 会调用 Concurrency() 读取 cfg，
// 若沿用本机默认的 CPUHalf，并发数会随机器变化，一旦并发路径有问题就难以复现
func TestExpand(t *testing.T) {
	cases := []struct {
		name string
		// setup 在临时根目录下布置仓库，返回传给 expand 的顶层路径列表（即配置顺序）
		// 以及该用例期望的完整条目列表（需要用到临时目录的真实路径，故一并返回）
		setup            func(t *testing.T, root string) (tops []string, want []RepoEntry)
		ignoreSubmodules bool
	}{
		{
			// 全局开关打开时，expand 必须在发现子模块之前就返回，
			// 结果里顶层条目的 IsSubmodule 必须全为 false，否则打印会误加 [子] 前缀
			name: "ignoreSubmodules=true 时只返回顶层条目且标记全为 false",
			setup: func(t *testing.T, root string) ([]string, []RepoEntry) {
				alpha := filepath.Join(root, "alpha")
				writeGitmodules(t, alpha, "[submodule \"a1\"]\n\tpath = a1\n")
				writeSubmoduleDir(t, alpha, "a1")
				return []string{alpha}, []RepoEntry{{Path: alpha, Name: "alpha", IsSubmodule: false}}
			},
			ignoreSubmodules: true,
		},
		{
			// 开关关闭时的基本契约：子模块被追加为独立条目，
			// Name 用相对路径（含层级时形如 vendor/lib，用于展示）、Path 用拼接后的完整路径、
			// IsSubmodule 为 true（用于加 [子] 前缀）
			name: "ignoreSubmodules=false 时子模块被追加且 Name 为相对路径",
			setup: func(t *testing.T, root string) ([]string, []RepoEntry) {
				alpha := filepath.Join(root, "alpha")
				writeGitmodules(t, alpha, "[submodule \"vendor/lib\"]\n\tpath = vendor/lib\n")
				writeSubmoduleDir(t, alpha, "vendor/lib")
				return []string{alpha}, []RepoEntry{
					{Path: alpha, Name: "alpha", IsSubmodule: false},
					{Path: filepath.Join(alpha, "vendor", "lib"), Name: "vendor/lib", IsSubmodule: true},
				}
			},
		},
		{
			// 顺序契约：所有顶层条目先整体出现，然后才是子模块；
			// 子模块之间按父仓库的配置顺序排列（alpha 的 a1/a2 先于 beta 的 b1）。
			// 若实现改成“每个顶层后面紧跟自己的子模块”，批量输出顺序会与仓库列表不一致
			name: "多个顶层仓库时顶层全部在前、子模块按父仓库顺序追加",
			setup: func(t *testing.T, root string) ([]string, []RepoEntry) {
				alpha := filepath.Join(root, "alpha")
				writeGitmodules(t, alpha, "[submodule \"a1\"]\n\tpath = a1\n"+
					"[submodule \"a2\"]\n\tpath = a2\n")
				writeSubmoduleDir(t, alpha, "a1")
				writeSubmoduleDir(t, alpha, "a2")
				beta := filepath.Join(root, "beta")
				writeGitmodules(t, beta, "[submodule \"b1\"]\n\tpath = b1\n")
				writeSubmoduleDir(t, beta, "b1")
				return []string{alpha, beta}, []RepoEntry{
					{Path: alpha, Name: "alpha", IsSubmodule: false},
					{Path: beta, Name: "beta", IsSubmodule: false},
					{Path: filepath.Join(alpha, "a1"), Name: "a1", IsSubmodule: true},
					{Path: filepath.Join(alpha, "a2"), Name: "a2", IsSubmodule: true},
					{Path: filepath.Join(beta, "b1"), Name: "b1", IsSubmodule: true},
				}
			},
		},
		{
			// 没有子模块的顶层仓库不该贡献任何额外条目，
			// 也不能因为 worker.Map 返回 nil 而多出空条目
			name: "顶层仓库没有子模块时不产生额外条目",
			setup: func(t *testing.T, root string) ([]string, []RepoEntry) {
				alpha := filepath.Join(root, "alpha")
				if err := os.MkdirAll(alpha, 0o755); err != nil {
					t.Fatalf("创建目录失败: %v", err)
				}
				beta := filepath.Join(root, "beta")
				writeGitmodules(t, beta, "[submodule \"b1\"]\n\tpath = b1\n")
				writeSubmoduleDir(t, beta, "b1")
				return []string{alpha, beta}, []RepoEntry{
					{Path: alpha, Name: "alpha", IsSubmodule: false},
					{Path: beta, Name: "beta", IsSubmodule: false},
					{Path: filepath.Join(beta, "b1"), Name: "b1", IsSubmodule: true},
				}
			},
		},
		{
			// 嵌套子模块在 expand 结果里同样保持深度优先：alpha/a 与 alpha/a/n 并排出现，
			// 因为 expand 直接沿用 discoverSubmodules 的顺序，不再二次排序
			name: "嵌套子模块条目紧跟其父之后且 Name 为完整相对路径",
			setup: func(t *testing.T, root string) ([]string, []RepoEntry) {
				alpha := filepath.Join(root, "alpha")
				writeGitmodules(t, alpha, "[submodule \"a\"]\n\tpath = a\n")
				writeSubmoduleDir(t, alpha, "a")
				writeGitmodules(t, filepath.Join(alpha, "a"), "[submodule \"n\"]\n\tpath = n\n")
				writeSubmoduleDir(t, filepath.Join(alpha, "a"), "n")
				return []string{alpha}, []RepoEntry{
					{Path: alpha, Name: "alpha", IsSubmodule: false},
					{Path: filepath.Join(alpha, "a"), Name: "a", IsSubmodule: true},
					{Path: filepath.Join(alpha, "a", "n"), Name: filepath.Join("a", "n"), IsSubmodule: true},
				}
			},
		},
		{
			// 空输入是 MustGetAllRepos 之外的调用方可能传入的边界（它自己会先拦截空列表），
			// expand 不能 panic，返回空结果即可
			name: "空顶层列表返回空结果且不 panic",
			setup: func(t *testing.T, root string) ([]string, []RepoEntry) {
				return nil, nil
			},
		},
		{
			// 注意：这里锁定实际行为——expand 不对顶层路径去重。
			// 若配置中 repo_paths 与 parent_paths 扫描结果出现同一个仓库（GetRepoList 不去重），
			// 该仓库会被展开两次，后续批量命令就会对它执行两遍。
			// 真实影响与修复建议在测试报告中单独列出，此处只按现状固定行为
			name: "重复的顶层路径会各自展开产生重复条目（不去重）",
			setup: func(t *testing.T, root string) ([]string, []RepoEntry) {
				alpha := filepath.Join(root, "alpha")
				if err := os.MkdirAll(alpha, 0o755); err != nil {
					t.Fatalf("创建目录失败: %v", err)
				}
				return []string{alpha, alpha}, []RepoEntry{
					{Path: alpha, Name: "alpha", IsSubmodule: false},
					{Path: alpha, Name: "alpha", IsSubmodule: false},
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withConfig(t, &config.Config{Concurrency: "2"})
			root := t.TempDir()
			tops, want := c.setup(t, root)
			got := expand(context.Background(), tops, c.ignoreSubmodules)
			if !sameRepoEntries(got, want) {
				t.Errorf("expand(ctx, %#v, %v) = %#v, 期望 %#v", tops, c.ignoreSubmodules, got, want)
			}
		})
	}

	// expand 只读顶层列表，不应就地修改调用方传入的切片
	// 这一点值得单独固定下来：调用方（AllRepos 等）之后可能还要复用同一个列表，
	// 若 expand 往里追加子模块路径，配置层的数据就被悄悄污染了
	t.Run("不修改调用方传入的顶层路径切片", func(t *testing.T) {
		withConfig(t, &config.Config{Concurrency: "2"})
		root := t.TempDir()
		alpha := filepath.Join(root, "alpha")
		writeGitmodules(t, alpha, "[submodule \"a1\"]\n\tpath = a1\n")
		writeSubmoduleDir(t, alpha, "a1")

		tops := []string{alpha}
		expand(context.Background(), tops, false)
		if !samePaths(tops, []string{alpha}) {
			t.Errorf("expand 修改了传入的顶层切片: %#v", tops)
		}
	})
}

// assertRepoList 校验 GetRepoList 的结果：前 len(wantPrefix) 个元素必须逐位等于 wantPrefix
// （repo_paths 必须原样保留且排在最前），其余元素与 wantRest 按**集合**（含重复计数）相等
//
// 剩余部分不比较顺序，是因为它们来自 os.ReadDir 的字典序遍历，顺序属于实现细节；
// 用集合比较足以锁定“哪些子目录被认定为仓库”，同时避免把遍历顺序一起焊死
func assertRepoList(t *testing.T, got, wantPrefix, wantRest []string) {
	t.Helper()
	if len(got) < len(wantPrefix) {
		t.Fatalf("结果长度 %d 小于 repo_paths 的 %d: 实际 %#v", len(got), len(wantPrefix), got)
	}
	for i := range wantPrefix {
		if got[i] != wantPrefix[i] {
			t.Errorf("第 %d 个路径应原样来自 repo_paths 且排在最前: 期望 %q, 实际 %q（完整结果 %#v）",
				i, wantPrefix[i], got[i], got)
		}
	}

	rest := got[len(wantPrefix):]
	if len(rest) != len(wantRest) {
		t.Fatalf("扫描到的仓库数量不符: 实际 %#v, 期望集合 %#v", rest, wantRest)
	}
	counts := make(map[string]int, len(wantRest))
	for _, p := range wantRest {
		counts[p]++
	}
	for _, p := range rest {
		counts[p]--
	}
	for p, n := range counts {
		if n != 0 {
			t.Errorf("扫描结果与期望集合不一致: 路径 %q 计数差 %d, 实际 %#v, 期望 %#v", p, n, rest, wantRest)
		}
	}
}

// TestGetRepoList 验证仓库列表的合并语义：
//   - 只有 repo_paths 时原样返回（不校验存在性、不打乱顺序）
//   - 只有 parent_paths 时扫描其**直接**子目录，只有 .git 是目录的子目录才算仓库
//   - parent_paths 不存在或无法读取时静默跳过，不能 panic 也不能中断其余来源
//   - 两个来源的结果都保留，且 repo_paths 在前
//
// 这里必须显式设置 cfg：GetRepoList 完全依赖全局配置，且它不做任何 nil 检查
// setup 直接返回配置与期望值：期望路径依赖运行时创建的 root，无法在表字面量里提前写死
func TestGetRepoList(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, root string) (conf *config.Config, wantPrefix, wantRest []string)
	}{
		{
			// repo_paths 是用户显式添加的仓库，属于“无条件信任”的输入：
			// 这里刻意不创建这些目录，以锁定“不校验存在性”的实际行为——
			// 校验发生在 git.IsRepo（父目录扫描）那一侧，而不是这里
			name: "只有 repo_paths 时原样返回且顺序不变",
			setup: func(t *testing.T, root string) (*config.Config, []string, []string) {
				r1 := filepath.Join(root, "r1")
				r2 := filepath.Join(root, "r2")
				conf := &config.Config{RepoPaths: []string{r1, r2}, ParentPaths: []string{}}
				return conf, []string{r1, r2}, nil
			},
		},
		{
			// 父目录扫描的核心口径：逐个直接子目录看有没有 .git **目录**。
			// 同时放入无 .git 的目录与普通文件，确保它们都不被误收
			name: "只有 parent_paths 时只收直接子目录里含 .git 目录的仓库",
			setup: func(t *testing.T, root string) (*config.Config, []string, []string) {
				parent := filepath.Join(root, "parent")
				repoA := filepath.Join(parent, "repoA")
				makeGitRepoDir(t, repoA)
				if err := os.MkdirAll(filepath.Join(parent, "plain"), 0o755); err != nil {
					t.Fatalf("创建普通目录失败: %v", err)
				}
				writeTestFile(t, filepath.Join(parent, "notes.txt"), "不是目录\n")
				conf := &config.Config{RepoPaths: []string{}, ParentPaths: []string{parent}}
				return conf, nil, []string{repoA}
			},
		},
		{
			// 配置里写了一个被删掉的父目录：必须静默跳过。
			// 这直接关系到真实使用——用户换了机器、路径失效时，ggt 不能整体崩溃，
			// 也不能因为一个坏路径就丢掉其他来源的仓库
			name: "parent_paths 指向不存在的目录时静默跳过",
			setup: func(t *testing.T, root string) (*config.Config, []string, []string) {
				conf := &config.Config{
					RepoPaths:   []string{},
					ParentPaths: []string{filepath.Join(root, "not-exist")},
				}
				return conf, nil, nil
			},
		},
		{
			// 覆盖 os.ReadDir 失败这一分支。用“父目录其实是个普通文件”来稳定触发：
			// 真正把目录 chmod 000 的做法在 root 用户下无效（root 无视权限位），
			// 会让用例在特权环境里假通过，所以改用必然失败的文件路径
			name: "parent_paths 指向普通文件（读取失败）时静默跳过",
			setup: func(t *testing.T, root string) (*config.Config, []string, []string) {
				notDir := filepath.Join(root, "i-am-a-file")
				writeTestFile(t, notDir, "内容\n")
				conf := &config.Config{RepoPaths: []string{}, ParentPaths: []string{notDir}}
				return conf, nil, nil
			},
		},
		{
			// 两个来源并存时的合并顺序：repo_paths 是用户显式指定的，必须排在最前，
			// 扫描结果追加在后。顺序会影响 ggt 的输出与批量执行顺序，是对外契约
			name: "repo_paths 与 parent_paths 的结果都出现且 repo_paths 在前",
			setup: func(t *testing.T, root string) (*config.Config, []string, []string) {
				parent := filepath.Join(root, "parent")
				repoA := filepath.Join(parent, "repoA")
				repoB := filepath.Join(parent, "repoB")
				makeGitRepoDir(t, repoA)
				makeGitRepoDir(t, repoB)
				explicit := filepath.Join(root, "explicit")
				conf := &config.Config{RepoPaths: []string{explicit}, ParentPaths: []string{parent}}
				return conf, []string{explicit}, []string{repoA, repoB}
			},
		},
		{
			// 子模块工作区的 .git 是指向父仓库 .git/modules/... 的 gitdir **文件**。
			// git.IsRepo 要求 .git 是目录，所以这种目录不算独立仓库；
			// 这正是设计意图——子模块由 discoverSubmodules/expand 负责，不能在这里重复收一遍
			name: "子目录里的 .git 是文件时不算仓库",
			setup: func(t *testing.T, root string) (*config.Config, []string, []string) {
				parent := filepath.Join(root, "parent")
				writeSubmoduleDir(t, parent, "submod")
				conf := &config.Config{RepoPaths: []string{}, ParentPaths: []string{parent}}
				return conf, nil, nil
			},
		},
		{
			// 只扫一层：parent/group/project/.git 存在，但 group 本身不是仓库，
			// 所以不递归下探、不把 project 收进来。
			// 若实现改成递归，parent_paths 指向大目录时会瞬间展开出海量仓库
			name: "只扫描直接子目录不递归更深层级",
			setup: func(t *testing.T, root string) (*config.Config, []string, []string) {
				parent := filepath.Join(root, "parent")
				makeGitRepoDir(t, filepath.Join(parent, "group", "project"))
				conf := &config.Config{RepoPaths: []string{}, ParentPaths: []string{parent}}
				return conf, nil, nil
			},
		},
		{
			// 注意：这里锁定实际行为——不跟随符号链接。
			// os.ReadDir 返回的 DirEntry.IsDir() 基于 lstat，符号链接目录会返回 false，
			// 于是指向仓库的软链接不会被收进列表。这一行为此前无测试记录，
			// 若有用户用软链接组织仓库目录会遇到，报告中单独列出
			name: "指向仓库的符号链接子目录不被收录（不跟随软链接）",
			setup: func(t *testing.T, root string) (*config.Config, []string, []string) {
				parent := filepath.Join(root, "parent")
				target := filepath.Join(root, "real-repo")
				makeGitRepoDir(t, target)
				if err := os.MkdirAll(parent, 0o755); err != nil {
					t.Fatalf("创建父目录失败: %v", err)
				}
				if err := os.Symlink(target, filepath.Join(parent, "link-repo")); err != nil {
					// Windows 或受限环境可能不允许建软链接，此时跳过用例而不是失败
					t.Skipf("当前环境无法创建符号链接，跳过: %v", err)
				}
				conf := &config.Config{RepoPaths: []string{}, ParentPaths: []string{parent}}
				return conf, nil, nil
			},
		},
		{
			// 注意：这里锁定实际行为——parentPath 用字符串拼接而非 filepath.Join，
			// 配置末尾若带分隔符，产出的路径会保留双分隔符（如 /tmp/p//repoA）。
			// 在 Linux 上该路径仍可解析、仓库也能被正确识别，但返回给用户的路径不规范；
			// 报告中单独列出，此处按现状断言以免测试与实现脱节
			name: "parent_paths 末尾带分隔符时结果保留双分隔符",
			setup: func(t *testing.T, root string) (*config.Config, []string, []string) {
				parentDir := filepath.Join(root, "parent")
				repoA := filepath.Join(parentDir, "repoA")
				makeGitRepoDir(t, repoA)
				parentWithSep := parentDir + string(os.PathSeparator)
				// 期望值刻意按实现的拼接方式（parentPath + 分隔符 + 目录名）构造，
				// 于是父路径自带的分隔符和实现补的分隔符叠加成双分隔符，锁住这一现状
				wantRepoA := parentWithSep + string(os.PathSeparator) + "repoA"
				conf := &config.Config{RepoPaths: []string{}, ParentPaths: []string{parentWithSep}}
				return conf, nil, []string{wantRepoA}
			},
		},
		{
			// 注意：这里锁定实际行为——GetRepoList 不去重。
			// 同一个仓库既被显式添加、又落在某个 parent_paths 下时会出现两次，
			// 后续批量命令会对它执行两遍。报告中作为疑似问题列出，此处只记录现状
			name: "同一仓库同时来自两个来源时产生重复条目（不去重）",
			setup: func(t *testing.T, root string) (*config.Config, []string, []string) {
				parent := filepath.Join(root, "parent")
				repoA := filepath.Join(parent, "repoA")
				makeGitRepoDir(t, repoA)
				conf := &config.Config{RepoPaths: []string{repoA}, ParentPaths: []string{parent}}
				return conf, []string{repoA}, []string{repoA}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			conf, wantPrefix, wantRest := c.setup(t, root)
			withConfig(t, conf)
			assertRepoList(t, GetRepoList(), wantPrefix, wantRest)
		})
	}
}
