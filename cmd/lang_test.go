package cmd

import "testing"

// TestNormalizeLanguage 验证语言串归一化。
// 关注点是"受支持的语言必须能被宽松写法命中，其余一律回退默认语言"——
// 回退而非报错是刻意的：语言只影响展示，传错不该让命令失败。
func TestNormalizeLanguage(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "en"},
		{"en", "en"},
		{"EN", "en"},
		{"en-US", "en"},
		{"  en  ", "en"},
		{"zh-CN", "zh-CN"},
		{"zh-cn", "zh-CN"},
		{"ZH-CN", "zh-CN"},
		// 同语种的地区/字形变体一律收敛到已发布的那一个
		{"zh", "zh-CN"},
		{"zh-Hans", "zh-CN"},
		{"zh-TW", "zh-CN"},
		// 未发布的语言、以及被误抓成语言的数值，都回退默认语言
		{"fr", "en"},
		{"200", "en"},
		{"-l", "en"},
	}

	for _, c := range cases {
		if got := normalizeLanguage(c.in); got != c.want {
			t.Errorf("normalizeLanguage(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestScanLangFlag 验证命令行语言参数的预扫描。
// 预扫描必须支持 pflag 的全部等价写法，并正确处理"未知 flag 的取值"与"-- 终止符"
// 这两个容易误判的场景。
func TestScanLangFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"长参数等号形式", []string{"--lang=zh-CN"}, "zh-CN"},
		{"长参数空格形式", []string{"--lang", "zh-CN"}, "zh-CN"},
		{"短参数空格形式", []string{"-l", "zh-CN"}, "zh-CN"},
		{"短参数粘连形式", []string{"-lzh-CN"}, "zh-CN"},
		{"子命令之后的语言参数", []string{"size", "-l", "zh-CN"}, "zh-CN"},
		{"无语言参数", []string{"size", "--low", "200"}, ""},
		{"空参数", []string{}, ""},
		// 未知 flag 的取值不能被误读成语言：--low 的值 200 应被 pflag 剥离
		{"未知 flag 的取值不被误读", []string{"--low", "200"}, ""},
		// 独立的 -- 之后停止 flag 解析，其后的 -l 不是 flag
		{"双横线后不解析", []string{"--", "-l", "zh-CN"}, ""},
		// 重复给出时以最后一个为准，与 pflag/cobra 的"后者覆盖"语义一致
		{"重复给出取后者", []string{"-l", "en", "--lang", "zh-CN"}, "zh-CN"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := scanLangFlag(c.args); got != c.want {
				t.Errorf("scanLangFlag(%v) = %q, want %q", c.args, got, c.want)
			}
		})
	}
}
