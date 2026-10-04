package git

import (
	"strings"
	"testing"
)

// TestParseHistoryLog 用一份手工样本验证解析：字段切分、正文里的换行、引用的挂载。
// 样本的形态与 `git log --topo-order --pretty=format:<historyLogFormat>` 完全一致
func TestParseHistoryLog(t *testing.T) {
	rec := func(fields ...string) string { return strings.Join(fields, "\x00") }
	sample := rec("aaa1", "bbb2 ccc3", "张三", "z@example.com", "1700000000", "合并分支", "正文第一行\n正文第二行\n") + "\x1e" +
		rec("bbb2", "", "李四", "l@example.com", "1699999999", "只有标题", "") + "\x1e"

	refs := map[string][]HistoryRef{"aaa1": {{Name: "main", Kind: "head"}}}

	items := parseHistoryLog(sample, refs)
	if len(items) != 2 {
		t.Fatalf("应解析出 2 条提交，实得 %d", len(items))
	}

	first := items[0]
	if first.Hash != "aaa1" || first.Author != "张三" || first.Timestamp != 1700000000 {
		t.Errorf("首条提交字段不对：%+v", first)
	}
	if len(first.Parents) != 2 || first.Parents[0] != "bbb2" || first.Parents[1] != "ccc3" {
		t.Errorf("父提交应解析成两个：%v", first.Parents)
	}
	if first.Message != "正文第一行\n正文第二行" {
		t.Errorf("正文应保留内部的换行、只去掉结尾那个：%q", first.Message)
	}
	if len(first.Refs) != 1 || first.Refs[0].Name != "main" {
		t.Errorf("引用没有挂到提交上：%v", first.Refs)
	}

	second := items[1]
	if len(second.Parents) != 0 {
		t.Errorf("没有父提交时应当是空切片：%v", second.Parents)
	}
	if second.Refs == nil {
		t.Errorf("没有引用的提交也应当是空切片而不是 nil（序列化成 [] 而不是 null）")
	}
	if second.Message != "" {
		t.Errorf("空正文应解析成空串：%q", second.Message)
	}
}

// TestParseHistoryLog_SkipsBrokenRecords 字段数不足的记录要跳过：整条输出被截断时，
// 宁可少一行，也不要把半个提交塞进图里（半个提交会让泳道错位且难以察觉）
func TestParseHistoryLog_SkipsBrokenRecords(t *testing.T) {
	good := strings.Join([]string{"aaa1", "", "a", "a@x", "1", "s", "m"}, "\x00")
	broken := strings.Join([]string{"bbb2", "", "b"}, "\x00") // 只有三个字段
	items := parseHistoryLog(good+"\x1e"+broken+"\x1e", nil)
	if len(items) != 1 || items[0].Hash != "aaa1" {
		t.Fatalf("应只保留完整的那条：%+v", items)
	}
	if items := parseHistoryLog("", nil); len(items) != 0 {
		t.Errorf("空输出应给空列表：%+v", items)
	}
}
