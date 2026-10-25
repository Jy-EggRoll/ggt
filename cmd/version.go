// version.go 汇总 ggt 的版本取值，并装配 "ggt version" 命令。
//
// 命令本身（文案、别名、三行输出格式）已迁到 eggokit/ui，本文件只回答“版本号与构建时间从哪里取”。
// Version 与 BuildTime 必须留在这个包：发布构建用 ldflags -X 注入
// github.com/jy-eggroll/ggt/cmd.Version 与 cmd.BuildTime（见 Taskfile.yml 的 LDFLAGS 与
// release.json），换包或改名都会让注入静默失效，二进制只会自报开发版本。
package cmd

import (
	"github.com/jy-eggroll/eggokit/ui"
	"github.com/spf13/cobra"
)

// Version 由发布构建通过链接参数注入；本地开发构建为空串，由共享实现显示为开发版本。
// 格式：x.y.z（正式版）或 x.y.z.dev.n（开发版）
var Version string

// BuildTime 由发布构建通过链接参数注入；本地开发构建为空串，由共享实现显示为 unknown。
// 格式：YYYY-MM-DDTHH:MM:SSZ（UTC 时间）
var BuildTime string

// versionOptions 返回 ggt 的版本取值，version 命令、upgrade 命令与看板的版本检查共用。
//
// 每次调用现构造，不用包级变量：字段只是对注入变量的闭包，构造没有开销，也就不必再定
// “什么时候初始化它”这条规则。
//
// 空版本回退为开发版本这一步交给共享实现的 EffectiveVersion，这里不要再写一份：
// 展示与升级比对必须共用同一个映射，若只在某处补占位，升级检查会打印出“当前版本  不是受支持的版本号”
// 这种版本位置为空的句子（实际遇到过）。占位串不是合法版本号，升级器据此跳过版本比较。
func versionOptions() ui.VersionOptions {
	return ui.VersionOptions{
		Program:   "ggt",
		Version:   func() string { return Version },
		BuildTime: func() string { return BuildTime },
	}
}

// newVersionCmd 实现 "ggt version"，别名为 ver，由共享包提供。
func newVersionCmd() *cobra.Command {
	return ui.NewVersionCommand(versionOptions())
}

func init() {
	register(func(root *cobra.Command) { root.AddCommand(newVersionCmd()) })
}
