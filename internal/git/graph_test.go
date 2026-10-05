package git

import (
	"reflect"
	"testing"
)

// 造一个提交：只填泳道分配用得到的字段（哈希、父提交、引用）
func graphItem(hash string, parents ...string) HistoryItem {
	if parents == nil {
		parents = []string{}
	}
	return HistoryItem{Hash: hash, Parents: parents, Refs: []HistoryRef{}}
}

// TestLayoutHistory_LinearKeepsOneLane 线性历史只有一条泳道，且颜色沿泳道继承下来：
// 中间那行不能重新轮转取色，否则同一条线会在中途换色
func TestLayoutHistory_LinearKeepsOneLane(t *testing.T) {
	vms := LayoutHistory([]HistoryItem{
		graphItem("c", "b"),
		graphItem("b", "a"),
		graphItem("a"),
	}, "main", "", "c")

	if len(vms) != 3 {
		t.Fatalf("应有 3 行，实得 %d", len(vms))
	}
	if vms[0].Kind != "HEAD" {
		t.Errorf("HEAD 指向的那行应当标成 HEAD，实得 %q", vms[0].Kind)
	}
	for i, vm := range vms {
		if vm.Index != 0 {
			t.Errorf("第 %d 行的圆点应在第 0 条泳道，实得 %d", i, vm.Index)
		}
	}
	if len(vms[0].Output) != 1 || vms[0].Output[0].ID != "b" {
		t.Fatalf("首行输出泳道应指向 b：%+v", vms[0].Output)
	}
	if vms[1].Output[0].Color != vms[0].Output[0].Color {
		t.Errorf("颜色应沿泳道继承：%q vs %q", vms[1].Output[0].Color, vms[0].Output[0].Color)
	}
	if vms[2].Output == nil || len(vms[2].Output) != 0 {
		t.Errorf("没有父提交的行之后不该还有泳道：%+v", vms[2].Output)
	}
}

// TestLayoutHistory_MergeOpensSecondLane 合并提交的第二个父提交要另开一条泳道，
// 且两条泳道不同色；到共同祖先处两条泳道汇合
func TestLayoutHistory_MergeOpensSecondLane(t *testing.T) {
	vms := LayoutHistory([]HistoryItem{
		graphItem("m", "a", "b"),
		graphItem("a", "c"),
		graphItem("b", "c"),
		graphItem("c"),
	}, "main", "", "m")

	if len(vms[0].Output) != 2 {
		t.Fatalf("合并提交应当开出两条泳道：%+v", vms[0].Output)
	}
	if vms[0].Output[0].Color == vms[0].Output[1].Color {
		t.Errorf("两条泳道应当不同色，实得同为 %q", vms[0].Output[0].Color)
	}
	if vms[2].Index != 1 {
		t.Errorf("b 在第二条泳道上，圆点索引应为 1，实得 %d", vms[2].Index)
	}
	if len(vms[3].Output) != 0 {
		t.Errorf("共同祖先之后泳道应全部汇合：%+v", vms[3].Output)
	}
}

// TestLayoutHistory_OctopusOpensThreeLanes 三个父提交（octopus merge）要开出三条泳道
func TestLayoutHistory_OctopusOpensThreeLanes(t *testing.T) {
	vms := LayoutHistory([]HistoryItem{
		graphItem("m", "a", "b", "c"),
		graphItem("a"),
		graphItem("b"),
		graphItem("c"),
	}, "", "", "m")

	if len(vms[0].Output) != 3 {
		t.Fatalf("三个父提交应开出三条泳道：%+v", vms[0].Output)
	}
	seen := map[string]bool{}
	for _, node := range vms[0].Output {
		if seen[node.Color] {
			t.Errorf("三条泳道出现了重复配色：%+v", vms[0].Output)
		}
		seen[node.Color] = true
	}
}

// TestLayoutHistory_HeadKindOnlyForHeadHash HEAD 标记只给哈希相等的那一行
func TestLayoutHistory_HeadKindOnlyForHeadHash(t *testing.T) {
	vms := LayoutHistory([]HistoryItem{graphItem("a"), graphItem("b")}, "main", "", "zzz")
	for i, vm := range vms {
		if vm.Kind != "node" {
			t.Errorf("第 %d 行不该是 HEAD：%q", i, vm.Kind)
		}
	}
}

// TestLayoutHistory_FirstParentTakesRefColor 有引用配色的提交，它的第一个父提交继承这个配色——
// 这样“当前分支那条线”整条都是同一种颜色，一眼能认出来
func TestLayoutHistory_FirstParentTakesRefColor(t *testing.T) {
	items := []HistoryItem{
		{Hash: "x", Parents: []string{"y"}, Refs: []HistoryRef{{Name: "main", Kind: "head"}}},
		graphItem("y"),
	}
	vms := LayoutHistory(items, "main", "", "x")
	if got := vms[0].Output[0].Color; got != ColorRef {
		t.Errorf("首个父提交应继承当前分支的配色 %q，实得 %q", ColorRef, got)
	}
}

// TestLayoutHistory_RefColorsAndOrder 引用的配色与顺序对齐上游 compareHistoryItemRefs：
// 当前分支 > 上游 > 有配色 > 其余，且只有前两者有固定配色
func TestLayoutHistory_RefColorsAndOrder(t *testing.T) {
	items := []HistoryItem{{
		Hash:    "x",
		Parents: []string{},
		Refs: []HistoryRef{
			{Name: "v1", Kind: "tag"},
			{Name: "origin/main", Kind: "remote"},
			{Name: "main", Kind: "head"},
		},
	}}
	vms := LayoutHistory(items, "main", "origin/main", "x")

	refs := vms[0].Item.Refs
	wantOrder := []string{"main", "origin/main", "v1"}
	wantColor := map[string]string{"main": ColorRef, "origin/main": ColorRemoteRef, "v1": ""}
	if len(refs) != len(wantOrder) {
		t.Fatalf("引用数不对：%+v", refs)
	}
	for i, name := range wantOrder {
		if refs[i].Name != name {
			t.Errorf("第 %d 个引用应为 %q，实得 %q（顺序：%+v）", i, name, refs[i].Name, refs)
		}
		if refs[i].Color != wantColor[name] {
			t.Errorf("%s 的配色应为 %q，实得 %q", name, wantColor[name], refs[i].Color)
		}
	}
}

// TestLayoutHistoryDeterministic 同一份输入两次布局必须逐字段相同。
// 泳道配色由分配顺序决定，一旦不确定，刷新一次整张图就换色
func TestLayoutHistoryDeterministic(t *testing.T) {
	items := []HistoryItem{
		graphItem("m", "a", "b", "d"),
		graphItem("a", "c"),
		graphItem("b", "c"),
		graphItem("d", "e"),
		graphItem("c", "e"),
		graphItem("e"),
	}
	first := LayoutHistory(items, "main", "", "m")
	second := LayoutHistory(items, "main", "", "m")
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("两次布局结果不同：\n%+v\n%+v", first, second)
	}
}

// TestLayoutHistoryPrefixStable 分页的做法是"把 limit 加大再取一次"，因此前 N 行的泳道必须与
// 只取 N 行时完全一致——否则用户一滚到底部，上面已经画好的线会整体错位
func TestLayoutHistoryPrefixStable(t *testing.T) {
	items := []HistoryItem{
		graphItem("m", "a", "b"),
		graphItem("a", "c"),
		graphItem("b", "c"),
		graphItem("c"),
	}
	full := LayoutHistory(items, "main", "", "m")
	prefix := LayoutHistory(items[:2], "main", "", "m")
	if !reflect.DeepEqual(full[:2], prefix) {
		t.Fatalf("前缀不稳定：\n完整布局前两行 %+v\n只取两行 %+v", full[:2], prefix)
	}
}

// TestLayoutHistory_UnknownParent 父提交不在这一段历史里（浅克隆、或翻页边界）时不报错：
// 泳道照常指向那个未知的哈希，只是不会再有行去使用它
func TestLayoutHistory_UnknownParent(t *testing.T) {
	vms := LayoutHistory([]HistoryItem{graphItem("a", "missing")}, "main", "", "a")
	if len(vms[0].Output) != 1 || vms[0].Output[0].ID != "missing" {
		t.Errorf("未知父提交也应占一条泳道：%+v", vms[0].Output)
	}
}
