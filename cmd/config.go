package cmd

import (
	"encoding/json"

	"ggt/internal/config"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

// configCmd 实现 "ggt config" 及其子命令，查看配置信息。
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "查看配置信息",
	Long: `查看 ggt 的当前配置和配置文件路径。

使用示例:
  ggt config          显示当前配置
  ggt config show    显示当前配置
  ggt config path    显示配置文件路径`,
	Run: showConfig,
}

// configShowCmd 与 configCmd 相同，提供明确的 show 子命令。
var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "显示当前配置",
	Run:   showConfig,
}

// configPathCmd 显示配置文件路径。
var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "显示配置文件路径",
	Run: func(cmd *cobra.Command, args []string) {
		pterm.Println(Muted(config.GetDefaultConfigPath()))
	},
}

// showConfig 以 JSON 格式打印当前配置。
func showConfig(cmd *cobra.Command, args []string) {
	cfg := GetConfig()
	jsonBytes, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		ErrorMsg("序列化配置失败: " + err.Error())
		return
	}

	Header("当前配置")
	PrintRaw(string(jsonBytes))
	pterm.Println()
	Infof("配置文件: %s", config.GetDefaultConfigPath())
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configPathCmd)
}
