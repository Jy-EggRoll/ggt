package cmd

import "testing"

// TestUseColor 固定着色口径的每一种组合。
//
// 重点在两处：显式取值必须压过环境与终端（--color=always 就是“这次一定要上色”），
// 以及 auto 档必须取交集——任一路不是终端就不上色，管道里的数据因此不会混进转义
func TestUseColor(t *testing.T) {
	cases := []struct {
		name                           string
		mode                           colorMode
		noColorSet                     bool
		stdoutTerminal, stderrTerminal bool
		want                           bool
	}{
		{name: "auto 两路都是终端", mode: colorAuto, stdoutTerminal: true, stderrTerminal: true, want: true},
		{name: "auto 只有 stdout 是终端", mode: colorAuto, stdoutTerminal: true, want: false},
		{name: "auto 只有 stderr 是终端", mode: colorAuto, stderrTerminal: true, want: false},
		{name: "auto 两路都不是终端", mode: colorAuto, want: false},
		{name: "auto 且设了 NO_COLOR", mode: colorAuto, noColorSet: true, stdoutTerminal: true, stderrTerminal: true, want: false},
		{name: "always 无视终端", mode: colorAlways, want: true},
		{name: "always 覆盖 NO_COLOR", mode: colorAlways, noColorSet: true, want: true},
		{name: "never 即使在终端里", mode: colorNever, stdoutTerminal: true, stderrTerminal: true, want: false},
		{name: "never 在管道里", mode: colorNever, stdoutTerminal: false, stderrTerminal: false, want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := useColor(testCase.mode, testCase.noColorSet, testCase.stdoutTerminal, testCase.stderrTerminal)
			if got != testCase.want {
				t.Fatalf("useColor(%v, noColor=%v, stdout=%v, stderr=%v) = %v，期望 %v",
					testCase.mode, testCase.noColorSet, testCase.stdoutTerminal, testCase.stderrTerminal, got, testCase.want)
			}
		})
	}
}

// TestResolveColorMode 固定 --color 的预扫描结果。
//
// 预扫描发生在 cobra 解析之前，四种等价写法都要认得出来；非法值不在这一层报错，
// 而是回退 auto 留给 cobra 提示，因此这里期望的是 auto
func TestResolveColorMode(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want colorMode
	}{
		{name: "没有 --color", want: colorAuto},
		{name: "等号写法", args: []string{"--color=never", "status"}, want: colorNever},
		{name: "空格写法", args: []string{"--color", "always", "status"}, want: colorAlways},
		{name: "大小写与首尾空白不敏感", args: []string{"--color= ALWAYS "}, want: colorAlways},
		{name: "子命令的未知 flag 不影响取值", args: []string{"size", "--low", "200", "--color=never"}, want: colorNever},
		{name: "-- 之后不再解析", args: []string{"--", "--color=never"}, want: colorAuto},
		{name: "非法取值回退 auto", args: []string{"--color=sometimes"}, want: colorAuto},
		{name: "缺取值回退 auto", args: []string{"--color"}, want: colorAuto},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := resolveColorMode(testCase.args); got != testCase.want {
				t.Fatalf("resolveColorMode(%v) = %v，期望 %v", testCase.args, got, testCase.want)
			}
		})
	}
}
