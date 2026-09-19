package cmd

import "testing"

// 语言串的归一化与严格校验由 pkg/l10n 负责，用例在 l10n 包内
// （TestNormalize / TestIsSupported）。本文件只覆盖 cmd 侧的预扫描。

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
