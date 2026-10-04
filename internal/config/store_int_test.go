package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIntAt 覆盖四种来源：文件里写着的数、缺键时用注册表默认值、值类型不对、键没登记。
//
// 这条测试的存在理由是一次真实缺陷：parseRaw 用 json.Number 保留数字字面量，
// 而调用方按 float64/int 做类型断言，于是"配置文件里写着 380、读出来却是默认值 340"，
// 全程不报错。任何一处再自己写类型断言，都应该先有这里的覆盖
func TestIntAt(t *testing.T) {
	dir := t.TempDir()

	t.Run("文件里写着就用文件里的", func(t *testing.T) {
		path := filepath.Join(dir, "c.json")
		if err := os.WriteFile(path, []byte(`{"graph_detail_width": 380}`), 0644); err != nil {
			t.Fatalf("写配置失败: %v", err)
		}
		got, err := IntAt(path, "graph_detail_width")
		if err != nil {
			t.Fatalf("读取失败: %v", err)
		}
		if got != 380 {
			t.Errorf("应读到 380，实得 %d（json.Number 那一支是否漏了？）", got)
		}
	})

	t.Run("缺键时用注册表默认值", func(t *testing.T) {
		path := filepath.Join(dir, "absent.json")
		got, err := IntAt(path, "graph_detail_width")
		if err != nil {
			t.Fatalf("读取失败: %v", err)
		}
		want := DefaultRaw()["graph_detail_width"]
		if got != want {
			t.Errorf("缺键时应给默认值 %v，实得 %d", want, got)
		}
	})

	t.Run("值类型不对时报错", func(t *testing.T) {
		path := filepath.Join(dir, "bad.json")
		if err := os.WriteFile(path, []byte(`{"graph_detail_width": "wide"}`), 0644); err != nil {
			t.Fatalf("写配置失败: %v", err)
		}
		if _, err := IntAt(path, "graph_detail_width"); err == nil {
			t.Errorf("类型不对应当报错，而不是给一个默认值")
		}
	})

	t.Run("键没登记时报错", func(t *testing.T) {
		if _, err := IntAt(filepath.Join(dir, "absent.json"), "no_such_key"); err == nil {
			t.Errorf("未登记的键应当报错")
		}
	})
}
