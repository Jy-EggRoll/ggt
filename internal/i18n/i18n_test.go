package i18n

import "testing"

// TestInitAllSupportedLanguages 断言每份内置语言文件都能被加载。
// 加载失败在运行期是致命的（进程直接退出），必须在测试里就暴露。
func TestInitAllSupportedLanguages(t *testing.T) {
	for _, lang := range Supported() {
		if err := Init(lang); err != nil {
			t.Errorf("加载语言 %s 失败: %v", lang, err)
		}
		if Current() != lang {
			t.Errorf("Current() = %q, want %q", Current(), lang)
		}
	}
	_ = Init(DefaultLanguage)
}

// TestTranslateFollowsLanguage 验证同一个消息 id 在不同语言下返回对应译文。
// 消息 id 就是英文原文本身，这是本包最核心的约定。
func TestTranslateFollowsLanguage(t *testing.T) {
	const id = "Show size statistics for all repositories"

	if err := Init("en"); err != nil {
		t.Fatal(err)
	}
	if got := T(id, nil); got != "Show size statistics for all repositories" {
		t.Errorf("英文下 T(%q) = %q", id, got)
	}

	if err := Init("zh-CN"); err != nil {
		t.Fatal(err)
	}
	if got := T(id, nil); got != "显示所有仓库的大小统计信息" {
		t.Errorf("中文下 T(%q) = %q", id, got)
	}

	_ = Init(DefaultLanguage)
}

// TestTranslateInterpolatesTemplateData 验证模板变量插值。
func TestTranslateInterpolatesTemplateData(t *testing.T) {
	if err := Init(DefaultLanguage); err != nil {
		t.Fatal(err)
	}
	if got := T("<{{.Low}}MB", map[string]any{"Low": 500}); got != "<500MB" {
		t.Errorf("插值结果 = %q, want %q", got, "<500MB")
	}

	if err := Init("zh-CN"); err != nil {
		t.Fatal(err)
	}
	if got := T("{{.Title}}: {{.Count}}", map[string]any{"Title": ">800MB", "Count": 3}); got != ">800MB：3 个" {
		t.Errorf("中文插值结果 = %q", got)
	}

	_ = Init(DefaultLanguage)
}

// TestTranslateFallsBackToSourceText 验证两类降级都回落到英文原文。
//
// 这是"源串即 id"模型的关键性质：**任何漏翻都不会产生空串或裸 key**，
// 最差情况只是显示英文。因此这里断言的不是"报错"，而是"等于源串"。
func TestTranslateFallsBackToSourceText(t *testing.T) {
	if err := Init(DefaultLanguage); err != nil {
		t.Fatal(err)
	}

	// 语言文件里不存在的消息（例如忘了跑 l10n:export）
	const unknown = "This message does not exist in any locale file"
	if got := T(unknown, nil); got != unknown {
		t.Errorf("未知消息应回退为源串，实得 %q", got)
	}

	// 模板变量漏传：missingkey=error 会让渲染失败，同样回退为源串
	const needData = "<{{.Low}}MB"
	if got := T(needData, nil); got != needData {
		t.Errorf("漏传模板变量应回退为源串，实得 %q", got)
	}

	_ = Init(DefaultLanguage)
}

// TestTranslateBeforeInit 断言未初始化时 T 安全返回源串而不是 panic。
// 命令树在 Init 之后才构造，但仍要保证任何调用路径都不会崩。
func TestTranslateBeforeInit(t *testing.T) {
	saved := localizer
	localizer = nil
	defer func() { localizer = saved }()

	const id = "Show size statistics for all repositories"
	if got := T(id, nil); got != id {
		t.Errorf("未初始化时应回退为源串，实得 %q", got)
	}
}

// TestValidateMessageIDRejectsReservedWords 验证保留字拦截。
//
// go-i18n 会把 other/one/hash 等词当作消息字段名（忽略大小写），若某条消息 id 恰好
// 等于这些词，整份语言文件会解析失败或条目被丢弃——两种后果都是译文静默全失。
// 因此必须在生成阶段就拒绝，而不是等到运行期。
func TestValidateMessageIDRejectsReservedWords(t *testing.T) {
	reserved := []string{"other", "Other", "ONE", "hash", "id", "description", "translation", "zero", "few"}
	for _, id := range reserved {
		if err := ValidateMessageID(id); err == nil {
			t.Errorf("保留字 %q 应被拒绝", id)
		}
	}

	normal := []string{
		"Show size statistics for all repositories",
		"Total size: {{.Size}}",
		"Disk usage",
		"{{.Title}}: {{.Count}}",
		"Others", // 只是包含 other，不是保留字本身
		"description of the repo",
	}
	for _, id := range normal {
		if err := ValidateMessageID(id); err != nil {
			t.Errorf("正常消息 %q 不应被拒绝: %v", id, err)
		}
	}
}

// TestSupportedReturnsCopy 断言 Supported 返回副本，调用方改动不会污染包内状态。
func TestSupportedReturnsCopy(t *testing.T) {
	got := Supported()
	if len(got) == 0 {
		t.Fatal("Supported 不应为空")
	}
	got[0] = "mutated"
	if Supported()[0] == "mutated" {
		t.Error("Supported 必须返回副本，否则调用方可以污染内部语言列表")
	}
}
