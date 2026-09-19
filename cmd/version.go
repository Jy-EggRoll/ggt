// version.go 实现 "ggt version" 命令，显示版本信息。
// Version 和 BuildTime 由发布构建通过 ldflags -X 注入；
// 本地开发构建保留默认值。
package cmd

import (
	"ggt/pkg/l10n"
	"github.com/spf13/cobra"
)

// Version 由发布构建通过链接参数注入；本地开发构建保留"开发版本"。
// 格式：x.y.z（正式版）或 x.y.z.dev.n（开发版）
var Version string

// BuildTime 由发布构建通过链接参数注入；本地开发构建保留"unknown"。
// 格式：YYYY-MM-DDTHH:MM:SSZ（UTC 时间）
var BuildTime string

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
			if Version == "" {
				InfoMsg(l10n.T("Version: development build", nil))
			} else {
				InfoMsg(l10n.T("Version: {{.Version}}", map[string]any{"Version": Version}))
			}
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
