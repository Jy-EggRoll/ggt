// version.go 实现 "ggt version" 命令，显示版本信息。
// Version 和 BuildTime 由发布构建通过 ldflags -X 注入；
// 本地开发构建保留默认值。
package cmd

import (
	"github.com/spf13/cobra"
)

// Version 由发布构建通过链接参数注入；本地开发构建保留"开发版本"。
// 格式：x.y.z（正式版）或 x.y.z.dev.n（开发版）
var Version string

// BuildTime 由发布构建通过链接参数注入；本地开发构建保留"unknown"。
// 格式：YYYY-MM-DDTHH:MM:SSZ（UTC 时间）
var BuildTime string

// versionCmd 实现 "ggt version"。
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "显示版本信息",
	Long: `显示 ggt 的构建版本信息。

使用示例:
  ggt version          显示版本信息`,
	Run: func(cmd *cobra.Command, args []string) {
		Header("ggt")
		if Version == "" {
			InfoMsg("版本: 开发版本")
		} else {
			Infof("版本: %s", Version)
		}
		if BuildTime != "" && BuildTime != "unknown" {
			Infof("构建时间: %s", BuildTime)
		}
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
