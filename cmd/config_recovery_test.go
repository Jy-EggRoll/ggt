// config_recovery_test 覆盖“配置已经坏掉”之后仍能自救的那几条命令层路径：
// 可恢复加载、受管项在取值非法时放行 reset、以及未知键的删除。
//
// 判定“当前值是否非法”要读真实文件，而“放行还是拒绝”的分支在 cmd 层（配置层只提供
// ValueErrorAt 这一份判定），因此这些用例放在这里而不是 internal/config。
package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jy-eggroll/ggt/internal/config"
)

// brokenConfig 在隔离的 HOME 下写入一份指定内容的配置文件，返回其路径。
//
// HOME 与 USERPROFILE 都改写：GetDefaultConfigPath 走 os.UserHomeDir，
// 两个平台上读的变量不同，只改一个会让用例在另一平台上读到真实配置。
func brokenConfig(t *testing.T, content string) string {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	path := config.GetDefaultConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建配置目录失败: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	return path
}

// TestLoadRecoverableConfigFallsBackToDefaults 断言宽松加载把坏值换成默认值，
// 并把“坏在哪、怎么修”逐条告诉用户。
func TestLoadRecoverableConfigFallsBackToDefaults(t *testing.T) {
	initL10n(t, "en")
	brokenConfig(t, `{"repo_paths":[""],"concurrency":"bogus"}`)

	// 严格加载必须失败，否则这条路径根本没被走到，后面的断言证明不了任何事
	if _, err := config.LoadConfig(); err == nil {
		t.Fatal("严格加载应拒绝这份配置")
	}

	buf := capturePtermOutput(t)
	got, err := loadRecoverableConfig()
	if err != nil {
		t.Fatalf("宽松加载不应失败: %v", err)
	}
	if len(got.RepoPaths) != 0 {
		t.Errorf("非法路径条目应被丢掉，实得 %#v", got.RepoPaths)
	}
	if got.Concurrency != config.DefaultConcurrency {
		t.Errorf("非法并发数应退回默认 %q，实得 %q", config.DefaultConcurrency, got.Concurrency)
	}

	out := buf.String()
	for _, want := range []string{"ggt config reset repo_paths", "ggt config reset concurrency"} {
		if !strings.Contains(out, want) {
			t.Errorf("提示应给出可执行的修复命令 %q，实得 %q", want, out)
		}
	}
}

// TestLoadRecoverableConfigFailsOnBrokenJSON 断言连 JSON 都读不出来时可恢复加载仍然失败，
// 且提示里给出唯一的命令行出路（整份重置）。
func TestLoadRecoverableConfigFailsOnBrokenJSON(t *testing.T) {
	initL10n(t, "en")
	brokenConfig(t, `{not json`)

	_, err := loadRecoverableConfig()
	if err == nil {
		t.Fatal("JSON 语法错误应让可恢复加载也失败")
	}
	if !strings.Contains(err.Error(), config.ResetAllCommand) {
		t.Errorf("提示应给出整份重置命令 %q，实得 %q", config.ResetAllCommand, err)
	}
}

// TestResetManagedKeyAllowedOnlyWhenInvalid 断言受管项的 reset 只在“当前值非法”时放行。
//
// 受管约束的是“怎么设值”（去重、git 仓库校验、路径规范化），不该拦住“怎么清除一个
// 已经坏掉的值”：值非法时所有命令都起不来，reset 是唯一能落地的修复。
func TestResetManagedKeyAllowedOnlyWhenInvalid(t *testing.T) {
	initL10n(t, "en")

	t.Run("当前值非法时放行并说明这次是清理", func(t *testing.T) {
		path := brokenConfig(t, `{"repo_paths":[""]}`)

		buf := capturePtermOutput(t)
		if err := resetOneKey("repo_paths", false); err != nil {
			t.Fatalf("受管项的当前值非法时应允许 reset: %v", err)
		}
		raw, err := config.ReadRawAt(path)
		if err != nil {
			t.Fatalf("读取配置失败: %v", err)
		}
		if _, ok := raw["repo_paths"]; ok {
			t.Errorf("reset 后文件里不应还有 repo_paths: %#v", raw)
		}
		if out := buf.String(); !strings.Contains(out, "repo_paths") || !strings.Contains(out, "invalid") {
			t.Errorf("应说明这次是清理非法值，实得 %q", out)
		}
		// 坏值清掉之后，严格加载必须恢复正常——这正是这条自救路径的目的
		if _, err := config.LoadConfig(); err != nil {
			t.Errorf("清掉坏值后严格加载应恢复: %v", err)
		}
	})

	t.Run("当前值合法时仍按受管拒绝", func(t *testing.T) {
		path := brokenConfig(t, `{"repo_paths":["/tmp/somewhere"]}`)

		err := resetOneKey("repo_paths", false)
		if err == nil {
			t.Fatal("受管项的值合法时不该被 reset 掉")
		}
		if !strings.Contains(err.Error(), "managed by") {
			t.Errorf("拒绝时应说明它由 ggt repo 管理，实得 %v", err)
		}
		raw, readErr := config.ReadRawAt(path)
		if readErr != nil {
			t.Fatalf("读取配置失败: %v", readErr)
		}
		if _, ok := raw["repo_paths"]; !ok {
			t.Error("被拒绝时不应改动文件")
		}
	})
}

// TestResetUnknownKeyRemovesOnlyWhenPresent 断言未知键的 reset 只在文件里确实写着它时放行。
//
// 未知键现在会让运行期加载失败，而 reset 若不认它，用户就只剩手改文件或 reset --all，
// 那正是这次要消除的死锁；反过来，文件里没有这个键时仍按未知键报错，拼写错误当场暴露。
func TestResetUnknownKeyRemovesOnlyWhenPresent(t *testing.T) {
	initL10n(t, "en")

	t.Run("文件里有该键时删除", func(t *testing.T) {
		path := brokenConfig(t, `{"typo_key":1}`)

		if err := resetOneKey("typo_key", false); err != nil {
			t.Fatalf("清掉文件里存在的未知键应被允许: %v", err)
		}
		raw, err := config.ReadRawAt(path)
		if err != nil {
			t.Fatalf("读取配置失败: %v", err)
		}
		if _, ok := raw["typo_key"]; ok {
			t.Errorf("reset 后文件里不应还有 typo_key: %#v", raw)
		}
		if _, err := config.LoadConfig(); err != nil {
			t.Errorf("清掉未知键后严格加载应恢复: %v", err)
		}
	})

	t.Run("文件里没有该键时仍报未知键", func(t *testing.T) {
		brokenConfig(t, `{}`)

		err := resetOneKey("typo_key", false)
		if err == nil || !strings.Contains(err.Error(), "Unknown config key") {
			t.Errorf("文件里没有该键时应报未知键，实得 %v", err)
		}
	})

	t.Run("未知键没有默认值可写", func(t *testing.T) {
		path := brokenConfig(t, `{"typo_key":1}`)

		err := resetOneKey("typo_key", true)
		if err == nil || !strings.Contains(err.Error(), "no default value") {
			t.Errorf("未知键的 --defaults 应说明它没有默认值，实得 %v", err)
		}
		raw, readErr := config.ReadRawAt(path)
		if readErr != nil {
			t.Fatalf("读取配置失败: %v", readErr)
		}
		if _, ok := raw["typo_key"]; !ok {
			t.Error("报错时不应改动文件")
		}
	})
}
