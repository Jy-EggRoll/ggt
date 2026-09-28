// repos_test 覆盖 cmd 包中 repos.go 的两个纯函数：
// parseGitmodules（纯内存解析 .gitmodules 文本）与 isSubmoduleInitialized（纯文件系统判断）。
//
// 为什么单独测这两个函数：子模块发现是 ggt 的辐射能力所在，而 discoverSubmodules
// 除递归外只做两件事——解析 .gitmodules 得到路径、判断该路径是否已初始化。
// 这两个函数原先 0% 覆盖，一旦解析写错（如漏掉文件末尾的子模块、把注释当路径），
// 或初始化判断写宽（把空目录/普通文件当成已初始化），错误会静默传播到所有遍历型命令，
// 因此这里用表驱动把它们的行为边界钉死。
//
// 说明：本文件只测纯函数，不触碰全局配置、不起 git 子进程；
// 涉及文件系统的用例一律用 t.TempDir() 建立隔离的临时目录，跑完由测试框架自动清理。
package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// samePaths 判断解析结果与期望的路径列表内容是否一致。
// 这里把 nil 与空切片视为等价：对调用方（discoverSubmodules 里的 for range）而言
// "没有子模块"这一语义与底层用 nil 还是空切片承载无关，
// 这样断言只锁语义、不锁实现细节，将来函数改用空切片返回也不会误报失败。
func samePaths(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestParseGitmodules 验证 .gitmodules 解析器在各书写形态下的取路径行为。
// 每种形态都对应真实仓库里会遇到的情况：
//   - 空文件/只有换行：仓库没有子模块时 .gitmodules 可能仍存在（甚至是空文件），不能报错或产出空串
//   - 单个/多个子模块：顺序必须与文件书写顺序一致，ggt 展示与执行顺序都依赖它
//   - 块里没有 path：只写了 url 的残缺块应被忽略，否则会产生空路径去 Join
//   - path 前后空格与 CRLF：编辑器对齐、Windows 换行都会带来多余空白，必须收敛
//   - # 注释行：注释里常出现 path = xxx 的示例，不能被误当成子模块
//   - 引号包裹的 path：锁定当前实现的实际行为（见用例内注释）
//   - 文件末尾没有换行：最后一次循环外补写，防止丢掉最后一个子模块
func TestParseGitmodules(t *testing.T) {
	cases := []struct {
		name string // 用例名，说明这一形态为什么值得测
		in   []byte // 传给 parseGitmodules 的 .gitmodules 原始内容
		want []string
	}{
		{
			name: "空输入不应 panic 也不产出空串",
			in:   []byte(""),
			want: nil,
		},
		{
			name: "只有空行与换行的文件同样视为无子模块",
			in:   []byte("\n\n   \n\t\n"),
			want: nil,
		},
		{
			// 最常见的规范写法：块头 + path + url（+ 可选 branch）
			name: "单个子模块只取 path，忽略 url 与 branch",
			in: []byte("[submodule \"lib\"]\n" +
				"\tpath = lib\n" +
				"\turl = https://example.com/lib.git\n" +
				"\tbranch = main\n"),
			want: []string{"lib"},
		},
		{
			// 多子模块时结果顺序决定 ggt 的输出与操作顺序，必须与文件顺序一致
			name: "多个子模块按文件书写顺序全部取出",
			in: []byte("[submodule \"a\"]\n" +
				"\tpath = vendor/a\n" +
				"\turl = ../a.git\n" +
				"[submodule \"b\"]\n" +
				"\tpath = vendor/b\n" +
				"\turl = ../b.git\n"),
			want: []string{"vendor/a", "vendor/b"},
		},
		{
			// 只有 url 的残缺块若被当成子模块，会产生空路径参与 filepath.Join，
			// 指向父仓库自身，后续 git 操作就会作用在错误的目录上
			name: "块里没有 path 字段则整块忽略",
			in: []byte("[submodule \"broken\"]\n" +
				"\turl = https://example.com/broken.git\n" +
				"[submodule \"ok\"]\n" +
				"\tpath = ok\n"),
			want: []string{"ok"},
		},
		{
			// path = 空值时同样不能产出空串条目，语义与缺字段一致
			name: "path 值为空时不产出条目",
			in: []byte("[submodule \"empty\"]\n" +
				"\tpath =\n" +
				"\turl = https://example.com/empty.git\n"),
			want: nil,
		},
		{
			// 对齐书写（path   =   x）很常见，若不去掉值两侧空白，
			// filepath.Join 得到的目录名会带空格，与真实目录不匹配
			name: "path 键与值前后空白都被收敛",
			in: []byte("[submodule \"spaced\"]\n" +
				"    path   =   vendor/spaced   \n"),
			want: []string{"vendor/spaced"},
		},
		{
			// 注释里常写示例路径，若不做 # 判断就会被当成本文件声明的子模块；
			// 这里同时覆盖行首直接 # 与缩进后的 #
			name: "带 # 的注释行被跳过",
			in: []byte("# 这是说明\n" +
				"   # path = commented/out\n" +
				"[submodule \"real\"]\n" +
				"\t# path = also/ignored\n" +
				"\tpath = real\n"),
			want: []string{"real"},
		},
		{
			// Windows 上 .gitmodules 可能是 CRLF 换行。TrimSpace 会吃掉行尾的 \r，
			// 否则路径会变成 \"real\\r\"，在 Windows 之外的文件系统上必然找不到目录
			name: "CRLF 行尾的 \\r 不应残留在路径里",
			in: []byte("[submodule \"crlf\"]\r\n" +
				"\tpath = crlf\r\n" +
				"\turl = ../crlf.git\r\n"),
			want: []string{"crlf"},
		},
		{
			// 注意：这里锁定的是代码的实际行为，而非直觉。
			// git 官方 config 解析会把 "a b" 两侧的引号去掉得到 a b，
			// 但本函数只做 TrimSpace、不处理引号，于是返回带引号的 "a b"。
			// 该差异会在子模块路径含空格时让 filepath.Join 拼出名为 "a b"（含引号）的目录，
			// 因此测试按现状断言，并在报告中作为疑似问题单独列出，此处不改代码
			name: "path 值用双引号包裹时引号被原样保留（与 git 官方解析不一致）",
			in: []byte("[submodule \"quoted\"]\n" +
				"\tpath = \"a b\"\n"),
			want: []string{`"a b"`},
		},
		{
			// 注意：这里锁定实际行为。函数只跳过整行以 # 开头的注释，
			// 不剥除行尾注释，于是 path = foo # 说明 会原样返回 "foo # 说明"。
			// git 官方解析在值不带引号时会把 # 及其后内容视为注释、返回 foo；
			// 两者不一致会让带行尾注释的仓库漏掉子模块，故按现状记录并单独报告
			name: "path 行尾的行内注释不被剥除（与 git 官方解析不一致）",
			in: []byte("[submodule \"inline-comment\"]\n" +
				"\tpath = foo # 说明文字\n"),
			want: []string{"foo # 说明文字"},
		},
		{
			// 无关字段的值里出现 path 字样、或键名只是包含 path（如 urlpath），
			// 都不能被误判为 path 字段——键名比较是全等，值内容一律不看
			name: "url/branch/update 等无关字段不干扰",
			in: []byte("[submodule \"x\"]\n" +
				"\turl = https://example.com/path/to/x.git\n" +
				"\tbranch = path-branch\n" +
				"\tupdate = checkout\n" +
				"\tignore = all\n" +
				"\turlpath = not-a-path-key\n" +
				"\tpath = x\n"),
			want: []string{"x"},
		},
		{
			// 文件末尾没有换行时，最后一条 path 只能靠循环外的补写保存；
			// 若少了这段补写，仓库里最后一个子模块会被静默丢掉
			name: "文件末尾没有换行时最后一个子模块仍被取出",
			in: []byte("[submodule \"a\"]\n" +
				"\tpath = a\n" +
				"[submodule \"b\"]\n" +
				"\tpath = b"),
			want: []string{"a", "b"},
		},
		{
			// 注意：这里同样锁定实际行为。函数不记录"当前是否处于某个
			// [submodule] 块内"，任何一行的 path = 都会被收下，
			// 所以写在所有块之前的孤儿 path 也会成为一个条目。
			// 真实 .gitmodules 不会这样写，但畸形文件下这一行为值得明确记录
			name: "块外的孤儿 path 也会被收集（解析器不跟踪块边界）",
			in: []byte("path = stray\n" +
				"[submodule \"a\"]\n" +
				"\tpath = a\n"),
			want: []string{"stray", "a"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseGitmodules(c.in)
			if !samePaths(got, c.want) {
				t.Errorf("parseGitmodules(%q) = %#v, 期望 %#v", c.in, got, c.want)
			}
		})
	}

	// 空输入这一具体路径额外断言返回 nil：discoverSubmodules 直接把它当结果用，
	// nil 是当前实现的真实返回值，这里做一次显式记录（不影响上面的等价判断）
	t.Run("空输入的底层返回值是 nil", func(t *testing.T) {
		if got := parseGitmodules(nil); got != nil {
			t.Errorf("空输入应返回 nil, 实际 %#v", got)
		}
	})
}

// TestIsSubmoduleInitialized 验证"子模块目录存在且非空"这一判断的边界。
// 这个判断决定了未初始化的子模块会不会被 ggt 当成可操作仓库：
// 判宽了（把空目录或普通文件算作已初始化）会让后续 git 命令作用在不存在的仓库上；
// 判严了（把非空目录算作未初始化）会让已 clone 的子模块被漏掉。
// 四类边界分别对应：没 clone 过、同名普通文件占位、clone 失败留下空目录、正常已初始化。
func TestIsSubmoduleInitialized(t *testing.T) {
	cases := []struct {
		name string
		// setup 在独立的临时根目录下布置场景，返回交给被测函数的路径
		setup func(t *testing.T, root string) string
		want  bool
	}{
		{
			// 未初始化的子模块典型形态：.gitmodules 有记录但目录尚未 clone，
			// 此时 os.Stat 报错，必须返回 false 而不是 panic 或当成已初始化
			name: "路径不存在（未 clone 的子模块）",
			setup: func(t *testing.T, root string) string {
				return filepath.Join(root, "absent")
			},
			want: false,
		},
		{
			// 名字撞车的普通文件：stat 能成功但不是目录，必须靠 IsDir 挡掉，
			// 否则会对一个文件执行 git 命令
			name: "路径是普通文件而非目录",
			setup: func(t *testing.T, root string) string {
				p := filepath.Join(root, "not-a-dir")
				if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
					t.Fatalf("准备文件失败: %v", err)
				}
				return p
			},
			want: false,
		},
		{
			// clone 中途失败或被清理后剩下的空目录：目录存在但没有任何工作区内容，
			// 与"未初始化"等价，必须返回 false
			name: "空目录",
			setup: func(t *testing.T, root string) string {
				p := filepath.Join(root, "empty")
				if err := os.Mkdir(p, 0o755); err != nil {
					t.Fatalf("准备目录失败: %v", err)
				}
				return p
			},
			want: false,
		},
		{
			// 正常已初始化的子模块：目录内至少有一个条目（真实场景是 .git 文件与源码）
			name: "非空目录",
			setup: func(t *testing.T, root string) string {
				p := filepath.Join(root, "initialized")
				if err := os.Mkdir(p, 0o755); err != nil {
					t.Fatalf("准备目录失败: %v", err)
				}
				if err := os.WriteFile(filepath.Join(p, ".git"), []byte("gitdir: ../.git/modules/x"), 0o644); err != nil {
					t.Fatalf("准备文件失败: %v", err)
				}
				return p
			},
			want: true,
		},
		{
			// 注意：这里锁定实际行为——判断只看目录项数量，不递归看内容，
			// 所以"只含一个空子目录"也算非空。真实仓库中内容不会全空，
			// 但明确记录该口径可以避免日后误以为函数会做深度校验
			name: "只含空子目录也算非空",
			setup: func(t *testing.T, root string) string {
				p := filepath.Join(root, "only-empty-child")
				if err := os.MkdirAll(filepath.Join(p, "child"), 0o755); err != nil {
					t.Fatalf("准备目录失败: %v", err)
				}
				return p
			},
			want: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			path := c.setup(t, root)
			if got := isSubmoduleInitialized(path); got != c.want {
				t.Errorf("isSubmoduleInitialized(%q) = %v, 期望 %v", path, got, c.want)
			}
		})
	}
	// 每个用例都在各自的 t.TempDir() 里布置场景，用例之间不共享目录，
	// 也不依赖任何执行顺序
}
