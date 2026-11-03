// testhelpers_test.go 收纳 cmd 包多个测试文件共用的几个小工具：造临时仓库、接管 pterm 输出、
// 切换语言、在输出里定位某一行。
//
// 这些工具被三处以上用例使用，放在某一个业务测试文件里会让人以为它只服务于那个文件；
// 更重要的是它们各自都要“用完还原全局状态”，集中在这里才能只写一遍还原逻辑。
package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/ggt/internal/locales"
	"github.com/pterm/pterm"
)

// initGitRepoWithFile 造一个只有一次提交的临时仓库，返回仓库路径。
//
// 用真实 git 而不是伪造目录：仓库发现、子模块展开与 git 命令本身都是被测路径的一部分，
// 假目录会在第一步就被判成“不是仓库”而静默跳过。
// 提交人的身份只写进命令环境，不动使用者的 git 配置（TestMain 已把全局配置指向空文件）。
func initGitRepoWithFile(t *testing.T, name string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("环境里没有 git，跳过")
	}

	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=ggt", "GIT_AUTHOR_EMAIL=ggt@example.com",
			"GIT_COMMITTER_NAME=ggt", "GIT_COMMITTER_EMAIL=ggt@example.com",
		)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v 失败：%v（%s）", args, err, out)
		}
	}

	git("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, name), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("写入 %s 失败：%v", name, err)
	}
	git("add", "-A")
	git("commit", "-qm", "init")
	return dir
}

// capturePtermOutput 把 pterm 的输出接到返回的缓冲区，用例结束时还原成接管前的值。
//
// pterm 没有读取当前默认输出的接口，但 Printer 的 Writer 在未显式设置时会回退到它，
// 所以经 GetWriter 取到的就是原值。不用硬编码 os.Stdout 还原：那样一旦外层已经接管过
// 输出（或者将来有别的入口改过默认值），用例收尾就会把别人的设置一起覆盖掉。
func capturePtermOutput(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	original := pterm.Info.GetWriter()
	pterm.SetDefaultOutput(&buf)
	t.Cleanup(func() { pterm.SetDefaultOutput(original) })

	return &buf
}

// initL10n 把全局语言切成 lang，用例结束时还原成切换前的语言。
//
// 必须还原而不是“切回英文”：语言是包级全局状态，写死英文同样会让结果取决于用例顺序，
// 将来把用例改成并行执行就会立刻串味。从未初始化过时 Current 返回空串，
// 而 Init 会把无法识别的标签收敛成默认语言，因此空串也能安全还原。
func initL10n(t *testing.T, lang string) {
	t.Helper()

	previous := l10n.Current()
	if err := l10n.Init(lang, locales.Options()); err != nil {
		t.Fatalf("初始化 l10n(%s) 失败: %v", lang, err)
	}
	t.Cleanup(func() {
		if err := l10n.Init(previous, locales.Options()); err != nil {
			t.Fatalf("还原 l10n(%s) 失败: %v", previous, err)
		}
	})
}

// findOutputLine 返回输出里包含 marker 的第一行，找不到时返回空串。
//
// 收尾汇总那一行要同时断言文案与样式，而同一段输出里还有别的 WARNING/INFO 行，
// 因此只能先定位到这一行，再在这一行里看前缀是哪一种。
func findOutputLine(out, marker string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, marker) {
			return line
		}
	}
	return ""
}
