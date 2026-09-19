package cmd

import (
	"testing"

	"ggt/internal/i18n"
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
		"config": {"path", "show"},
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

// TestUnmigratedCommandsKeepOriginalText 验证渐进迁移：尚未迁移的命令其描述仍是中文原文，
// 不会因为语言切换而变成别的内容或裸 key。
//
// 这是"源串即 id"模型相较"符号 key"方案的一个直接好处——不需要任何"是否命中"的判定，
// 没被 T() 包住的字段原样保留即可。
func TestUnmigratedCommandsKeepOriginalText(t *testing.T) {
	if err := i18n.Init("en"); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}
	statusCmd, _, err := buildRoot().Find([]string{"status"})
	if err != nil {
		t.Fatalf("找不到 status 命令: %v", err)
	}
	const want = "显示所有仓库的 git 状态"
	if statusCmd.Short != want {
		t.Errorf("未迁移命令的描述应保持原样，实得 %q", statusCmd.Short)
	}
}
