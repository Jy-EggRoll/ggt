package cmd

import (
	"github.com/spf13/cobra"
)

// Version 在构建时通过 ldflags -X 注入（格式：yyyy-mm-dd-HH-MM-SS）。
// 未注入时显示"开发版本"。
var Version string

// versionCmd 实现 "ggt version"。
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "显示版本号",
	Long: `显示 ggt 的构建版本号。

版本号格式为构建时间：yyyy-mm-dd-HH-MM-SS。

使用示例:
  ggt version          显示版本号`,
	Run: func(cmd *cobra.Command, args []string) {
		Header("ggt")
		if Version == "" {
			InfoMsg("开发版本")
		} else {
			Infof("版本: %s", Version)
		}
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
