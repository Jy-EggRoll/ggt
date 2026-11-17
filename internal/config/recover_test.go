// recover_test 覆盖“配置坏掉之后还能自救”的那几块：宽松加载、修复命令提示、
// 以及未知键在四条路径（运行期加载、get、set、validate）上必须同结论。
//
// 这些用例刻意断言语义而非具体措辞（除了必须出现的命令字符串）：提示文案会随翻译迭代，
// 而“报错里有没有一条能直接敲的命令”是这次改造要守住的东西。
package config

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadConfigLenientSalvagesUsablePaths 断言宽松加载把坏值换掉、但保留还能用的路径条目。
//
// 保留条目的理由：每一条路径都是用户登记过的一个仓库，整体退回空列表等于让他重新登记一遍。
func TestLoadConfigLenientSalvagesUsablePaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	writeFile(t, path, `{
		"repo_paths": ["/good/one", "", "/has\nnewline", "/good/two"],
		"concurrency": "bogus"
	}`)

	// 先确认这份文件在严格加载下确实起不来，否则后面的断言没有意义
	if _, err := LoadConfigAt(path); err == nil {
		t.Fatal("严格加载应当拒绝这份文件")
	}

	cfg, problems, err := LoadConfigLenientAt(path)
	if err != nil {
		t.Fatalf("宽松加载不应因键级问题失败: %v", err)
	}
	if want := []string{"/good/one", "/good/two"}; strings.Join(cfg.RepoPaths, ",") != strings.Join(want, ",") {
		t.Errorf("可用条目应被保留，实得 %#v，期望 %#v", cfg.RepoPaths, want)
	}
	if cfg.Concurrency != DefaultConcurrency {
		t.Errorf("坏掉的 concurrency 应退回默认 %q，实得 %q", DefaultConcurrency, cfg.Concurrency)
	}

	keys := problemKeys(problems)
	for _, want := range []string{"repo_paths", "concurrency"} {
		if !strings.Contains(keys, want) {
			t.Errorf("问题清单应含 %q，实得 %q（%v）", want, keys, problems)
		}
	}
}

// TestLoadConfigLenientRecordsUnknownKey 断言未知键在宽松加载里只记录、不阻断。
//
// 这两个子树（config 与 repo）的职责正是把它清掉，若这里也报错，自救入口等于没有开。
func TestLoadConfigLenientRecordsUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	writeFile(t, path, `{"typo_key": 1}`)

	cfg, problems, err := LoadConfigLenientAt(path)
	if err != nil {
		t.Fatalf("未知键不应让宽松加载失败: %v", err)
	}
	if cfg.Concurrency != DefaultConcurrency {
		t.Errorf("未知键不应影响其它项，concurrency = %q", cfg.Concurrency)
	}
	if !strings.Contains(problemKeys(problems), "typo_key") {
		t.Errorf("问题清单应点名未知键，实得 %v", problems)
	}
}

// TestLoadConfigLenientFailsOnBrokenJSON 断言“连 JSON 都读不出来”时宽松加载仍然失败：
// 那时没有哪一个键可清，修复只能靠手改文件或整份重置。
func TestLoadConfigLenientFailsOnBrokenJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	writeFile(t, path, `{not json`)

	if _, _, err := LoadConfigLenientAt(path); err == nil {
		t.Fatal("JSON 语法错误应让宽松加载返回 error")
	}
}

// TestWithRecoveryHintNamesExecutableCommand 断言加载失败的输出里有可直接敲的命令。
//
// 只说“配置有问题”时，用户手上只剩手改 JSON 与 reset --all（会丢掉全部仓库记录）；
// 点名键与命令，才能把这次失败变成一次可恢复的操作。
func TestWithRecoveryHintNamesExecutableCommand(t *testing.T) {
	t.Run("键级问题点名该键的 reset 命令", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "c.json")
		writeFile(t, path, `{"repo_paths": [""]}`)

		_, err := LoadConfigAt(path)
		if err == nil {
			t.Fatal("严格加载应拒绝这条路径")
		}
		wrapped := WithRecoveryHint(err, false)
		hinted := wrapped.Error()
		// 键名带 shell 引号：提示里的命令要能整段粘进终端
		if !strings.Contains(hinted, "ggt config reset 'repo_paths'") {
			t.Errorf("提示应给出可执行的修复命令，实得 %q", hinted)
		}
		// 键级失败时 config 与 repo 两个子树确实还能跑，提示可以这么说
		if !strings.Contains(hinted, `"ggt repo" and the rest of "ggt config" still work`) {
			t.Errorf("键级失败时提示应说明这两个子树仍可用，实得 %q", hinted)
		}
		// 提示只加一次：每一层都调用 WithRecoveryHint 也不该叠成两遍
		twice := WithRecoveryHint(wrapped, false).Error()
		if strings.Count(twice, "ggt config reset 'repo_paths'") != strings.Count(hinted, "ggt config reset 'repo_paths'") {
			t.Errorf("重复调用不应把提示叠两遍: %q", twice)
		}
		// 错误链仍要保留，调用方还能判断具体原因
		if !errors.Is(wrapped, err) {
			t.Error("提示包装不应切断错误链")
		}
	})

	t.Run("读不出文件时只给整份重置，且按交互性决定 --yes", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "c.json")
		writeFile(t, path, `{not json`)

		_, err := LoadConfigAt(path)
		if err == nil {
			t.Fatal("语法错误应让严格加载失败")
		}

		interactive := WithRecoveryHint(err, false).Error()
		if !strings.Contains(interactive, "ggt config reset --all") {
			t.Errorf("提示应给出整份重置命令，实得 %q", interactive)
		}
		if strings.Contains(interactive, "--yes") {
			t.Errorf("交互环境下不该让用户跳过确认，实得 %q", interactive)
		}
		// 语法错误时 ggt repo 同样加载不了，提示不能把它说成可用
		if strings.Contains(interactive, "still work") {
			t.Errorf("文件级失败时不该声称 repo/config 仍可用，实得 %q", interactive)
		}

		// 非交互环境（管道、CI）缺 --yes 会被拒绝，提示必须给一条当场能跑的命令
		nonInteractive := WithRecoveryHint(err, true).Error()
		if !strings.Contains(nonInteractive, "ggt config reset --all --yes") {
			t.Errorf("非交互环境应给出带 --yes 的命令，实得 %q", nonInteractive)
		}
	})

	t.Run("键名含空格或引号时命令仍然可执行", func(t *testing.T) {
		key := "weird key'name"
		cmd := ResetCommand(key)
		if want := `ggt config reset 'weird key'\''name'`; cmd != want {
			t.Errorf("ResetCommand(%q) = %q，期望 %q", key, cmd, want)
		}
	})
}

// TestUnknownKeySameVerdictOnAllPaths 断言未知键在四条路径上给出同一个结论。
//
// 此前 validate 说它是 error 并退出 1，运行期却静默忽略、照常启动，用户看到的是
// “校验说文件有问题，实际又一切正常”。现在四条路径一致判失败：未知键几乎总是拼写错误，
// 忽略它只会让用户以为配置生效了。
func TestUnknownKeySameVerdictOnAllPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	writeFile(t, path, `{"typo_key": 1}`)

	if _, err := LoadConfigAt(path); err == nil {
		t.Error("运行期加载应因未知键失败")
	} else if !strings.Contains(err.Error(), "typo_key") {
		t.Errorf("加载报错应点名未知键，实得 %q", err)
	}

	if _, err := EffectiveAt(path, "typo_key"); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("config get 应报未知键，实得 %v", err)
	}

	if _, err := SetFromTextAt(path, "typo_key", "1", false); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("config set 应报未知键，实得 %v", err)
	}

	issues, err := ValidateAt(path)
	if err != nil {
		t.Fatalf("体检本身不应失败: %v", err)
	}
	found := false
	for _, issue := range issues {
		if issue.Level == LevelError && strings.Contains(issue.Message, "typo_key") {
			found = true
		}
	}
	if !found {
		t.Errorf("config validate 应把未知键报成 error，实得 %v", issues)
	}
}

// TestValueErrorAt 断言“这一项现在是不是坏的”只有一份判定，且废弃的并发语义值不算坏。
func TestValueErrorAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	writeFile(t, path, `{
		"repo_paths": [""],
		"size_unit": "decimal",
		"concurrency": "CPUHalf"
	}`)

	if bad, err := ValueErrorAt(path, "repo_paths"); err != nil || !bad {
		t.Errorf("空路径应被判为非法值，实得 (bad=%v, err=%v)", bad, err)
	}
	if bad, err := ValueErrorAt(path, "size_unit"); err != nil || bad {
		t.Errorf("合法值不应报错，实得 (bad=%v, err=%v)", bad, err)
	}
	// 废弃值运行期按 auto 继续跑（见 migrateDeprecatedConcurrency），因此不算“坏值”：
	// 否则 reset 会以“值非法”为由绕开 managed 限制的同一套判断
	if bad, err := ValueErrorAt(path, "concurrency"); err != nil || bad {
		t.Errorf("废弃的并发语义值不算坏值，实得 (bad=%v, err=%v)", bad, err)
	}
	// 键不存在时无从谈起“当前值非法”
	if bad, err := ValueErrorAt(path, "parent_paths"); err != nil || bad {
		t.Errorf("缺键不应算坏值，实得 (bad=%v, err=%v)", bad, err)
	}
}

// TestParseSizeUnitAcceptsConfigSpelling 断言命令行 --unit 与配置文件共用同一份归一规则。
func TestParseSizeUnitAcceptsConfigSpelling(t *testing.T) {
	ok := map[string]string{
		"binary":   SizeUnitBinary,
		"BINARY":   SizeUnitBinary,
		"  Binary": SizeUnitBinary,
		"decimal":  SizeUnitDecimal,
	}
	for in, want := range ok {
		got, err := ParseSizeUnit(in)
		if err != nil {
			t.Errorf("ParseSizeUnit(%q) 失败: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseSizeUnit(%q) = %q，期望 %q", in, got, want)
		}
	}

	// 非法值必须报错而不是回退 decimal：静默回退会让用户以为口径设上了
	_, err := ParseSizeUnit("kb")
	if err == nil {
		t.Fatal("非法口径应报错")
	}
	if !strings.Contains(err.Error(), "size_unit") || !strings.Contains(err.Error(), "binary") {
		t.Errorf("报错应点名键与候选取值，实得 %q", err)
	}
}

// problemKeys 把问题清单里的键名拼成一行，便于断言“哪个键被点名了”。
func problemKeys(problems []Problem) string {
	keys := make([]string, 0, len(problems))
	for _, p := range problems {
		keys = append(keys, p.Key)
	}
	return strings.Join(keys, ",")
}

// TestPathListCounts 断言 reset 在清掉坏列表之前能说清“几条、其中几条还能用”。
//
// 宽松加载会把坏条目丢掉，用户看到“已清除”时不会意识到几个本来能用的仓库一起没了。
func TestPathListCounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	writeFile(t, path, `{"repo_paths": ["/good/one", "", "/has\nnewline", "/good/two"]}`)

	total, usable, err := PathListCounts(path, "repo_paths")
	if err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if total != 4 || usable != 2 {
		t.Errorf("应为 4 条、其中 2 条可用，实得 %d / %d", total, usable)
	}

	// 不是列表形态时不报条数：没有“几条”可言，调用方据此跳过这句话
	writeFile(t, path, `{"repo_paths": "/a"}`)
	if total, usable, err := PathListCounts(path, "repo_paths"); err != nil || total != 0 || usable != 0 {
		t.Errorf("非列表形态应返回 (0,0,nil)，实得 (%d,%d,%v)", total, usable, err)
	}

	// 缺键同样没有条目
	writeFile(t, path, `{}`)
	if total, usable, err := PathListCounts(path, "repo_paths"); err != nil || total != 0 || usable != 0 {
		t.Errorf("缺键应返回 (0,0,nil)，实得 (%d,%d,%v)", total, usable, err)
	}
}
