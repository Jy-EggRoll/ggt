// version.go 实现 "ggt version" 命令，显示版本信息。
// Version 和 BuildTime 由发布构建通过 ldflags -X 注入；
// 本地开发构建保留默认值。
package cmd

import (
	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/spf13/cobra"
)

// Version 由发布构建通过链接参数注入；本地开发构建保留“开发版本”。
// 格式：x.y.z（正式版）或 x.y.z.dev.n（开发版）
var Version string

// BuildTime 由发布构建通过链接参数注入；本地开发构建保留"unknown"。
// 格式：YYYY-MM-DDTHH:MM:SSZ（UTC 时间）
var BuildTime string

// effectiveVersion 返回本二进制对外使用的版本号：发布构建是被 ldflags 注入的 x.y.z
// （或 x.y.z.dev.n），本地开发构建没有注入值，统一用“development build”这一可读占位。
//
// 必须让展示与升级共用同一个映射：version 命令要显示它，upgrade 要拿它去与发布版本比对。
// 若只在 version 命令里做映射，升级检查就会打印出“当前版本  不是受支持的版本号”——
// 版本号位置为空的句子，用户看不出到底缺了什么（实际遇到过）。
// 占位串不是合法版本号，升级器会据此跳过版本比较，退化为“列出目标通道中的最高版本”
func effectiveVersion() string {
	if Version == "" {
		return l10n.T("development build", nil)
	}
	return Version
}

// versionCmd 实现 "ggt version"。
func newVersionCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "version",
		Short: l10n.T("Show version information", nil),
		Long: l10n.T(`Show the build version information of ggt.

Examples:
  ggt version         Show version information`, nil),
		Run: func(cmd *cobra.Command, args []string) {
			// Header 的实参是程序名，不属于文案
			Header("ggt")
			InfoMsg(l10n.T("Version: {{.Version}}", map[string]any{"Version": effectiveVersion()}))
			if BuildTime != "" && BuildTime != "unknown" {
				InfoMsg(l10n.T("Build time: {{.Time}}", map[string]any{"Time": BuildTime}))
			}
		},
	}
	return c
}

func init() {
	register(func(root *cobra.Command) { root.AddCommand(newVersionCmd()) })
}
