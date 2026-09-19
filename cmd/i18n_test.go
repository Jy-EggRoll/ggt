package cmd

import (
	"testing"
	"unicode"

	"ggt/internal/i18n"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TestBuildRootRegistersAllCommands 断言 buildRoot 装配出完整的命令树。
//
// 各命令通过自己的 init() 调用 register 登记装配动作，漏登记的命令会从帮助里
// 静默消失（不报错、不影响编译），所以需要这条测试兜住。
func TestBuildRootRegistersAllCommands(t *testing.T) {
	if err := i18n.Init(i18n.DefaultLanguage); err != nil {
		t.Fatalf("初始化 i18n 失败: %v", err)
	}
	root := buildRoot()

	wantTop := []string{"config", "files", "owned", "remote", "repo", "size", "status", "summary", "sync", "version"}
	got := map[string]bool{}
	for _, c := range root.Commands() {
		got[c.Name()] = true
	}
	for _, name := range wantTop {
		if !got[name] {
			t.Errorf("命令 %q 未注册到根命令", name)
		}
	}

	// 容器命令的子命令同样要到位
	subCommands := map[string][]string{
		"repo":   {"add", "add-parent", "list", "remove"},
		"remote": {"https", "ssh", "toggle"},
		"config": {"get", "path", "reset", "set", "show", "validate"},
	}
	for parent, children := range subCommands {
		pc, _, err := root.Find([]string{parent})
		if err != nil {
			t.Errorf("找不到命令 %q: %v", parent, err)
			continue
		}
		childNames := map[string]bool{}
		for _, c := range pc.Commands() {
			childNames[c.Name()] = true
		}
		for _, child := range children {
			if !childNames[child] {
				t.Errorf("命令 %q 的子命令 %q 未注册", parent, child)
			}
		}
	}

	// --lang 必须注册为持久化 flag，否则命令行传 --lang 会被 cobra 判为未知参数
	if root.PersistentFlags().Lookup("lang") == nil {
		t.Error("根命令缺少 --lang 持久化 flag")
	}
}

// TestDescriptionsFollowLanguage 验证「i18n.Init → 命令构造函数 → i18n.T」整条链路：
// 命令描述与 flag 说明都应随语言切换。
//
// 这是重构后最关键的一条集成测试：命令树改为在语言加载之后构造，若哪一步的时序被
// 改坏（例如把 buildRoot 挪到 Init 之前），这里会立刻失败。
func TestDescriptionsFollowLanguage(t *testing.T) {
	cases := []struct {
		lang      string
		wantShort string
		wantLow   string
	}{
		{
			lang:      "en",
			wantShort: "Show size statistics for all repositories",
			wantLow:   "Lower bucket bound in MB (defaults to the size_bucket_low_mb config value)",
		},
		{
			lang:      "zh-CN",
			wantShort: "显示所有仓库的大小统计信息",
			wantLow:   "分桶下界阈值（MB），省略时取配置文件 size_bucket_low_mb",
		},
	}

	for _, c := range cases {
		if err := i18n.Init(c.lang); err != nil {
			t.Fatalf("初始化 %s 失败: %v", c.lang, err)
		}
		sizeCmd, _, err := buildRoot().Find([]string{"size"})
		if err != nil {
			t.Fatalf("找不到 size 命令: %v", err)
		}
		if sizeCmd.Short != c.wantShort {
			t.Errorf("%s 下 size 的 Short = %q, want %q", c.lang, sizeCmd.Short, c.wantShort)
		}
		low := sizeCmd.Flags().Lookup("low")
		if low == nil {
			t.Fatalf("size 命令缺少 --low flag")
		}
		if low.Usage != c.wantLow {
			t.Errorf("%s 下 --low 的说明 = %q, want %q", c.lang, low.Usage, c.wantLow)
		}
	}

	// 复位，避免影响同包其他测试
	if err := i18n.Init(i18n.DefaultLanguage); err != nil {
		t.Fatal(err)
	}
}

// TestNoUntranslatedTextInCommandTree 断言命令树里不再残留中文描述。
//
// 全仓文案迁移完成后，所有命令描述与 flag 说明都必须经 i18n.T()。若有人新写一个命令
// 却忘了包裹，英文环境下它的描述仍会是中文；这条测试是运行期的兜底。
// 与之互补的是 l10n:check，它在源码层面做同样的判定并给出文件行号。
func TestNoUntranslatedTextInCommandTree(t *testing.T) {
	if err := i18n.Init(i18n.DefaultLanguage); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}

	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		fields := map[string]string{"Short": cmd.Short, "Long": cmd.Long, "Example": cmd.Example}
		for field, value := range fields {
			if hasNonASCIILetter(value) {
				t.Errorf("%s 的 %s 仍含非 ASCII 文字，说明该文案没有经过 i18n.T(): %q",
					cmd.CommandPath(), field, value)
			}
		}
		cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if hasNonASCIILetter(f.Usage) {
				t.Errorf("%s 的 flag --%s 说明仍含非 ASCII 文字: %q", cmd.CommandPath(), f.Name, f.Usage)
			}
		})
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(buildRoot())
}

// hasNonASCIILetter 判断字符串是否含非 ASCII 字母。
// 判据用 IsLetter 而非"非 ASCII 字符"：分隔线 "─"、"↔" 这类排版符号是界面骨架，
// 不属于需要翻译的文字。
func hasNonASCIILetter(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII && unicode.IsLetter(r) {
			return true
		}
	}
	return false
}
