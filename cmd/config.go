package cmd

import (
	"encoding/json"

	"ggt/internal/config"
	"ggt/internal/i18n"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

// configCmd 实现 "ggt config" 及其子命令，查看配置信息。
func newConfigCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: i18n.T("Show configuration information", nil),
		Long: i18n.T(`Show the current configuration of ggt and the path to the config file.

Examples:
  ggt config          Show the current configuration
  ggt config show     Show the current configuration
  ggt config path     Show the path to the config file`, nil),
		Run: showConfig,
	}
	c.AddCommand(newConfigShowCmd(), newConfigPathCmd())
	return c
}

// configShowCmd 与 configCmd 相同，提供明确的 show 子命令。
func newConfigShowCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "show",
		Short: i18n.T("Show the current configuration", nil),
		Run:   showConfig,
	}
	return c
}

// configPathCmd 显示配置文件路径。
func newConfigPathCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "path",
		Short: i18n.T("Show the path to the config file", nil),
		Run: func(cmd *cobra.Command, args []string) {
			pterm.Println(Muted(config.GetDefaultConfigPath()))
		},
	}
	return c
}

// showConfig 以 JSON 格式打印当前配置。
func showConfig(cmd *cobra.Command, args []string) {
	cfg := GetConfig()
	jsonBytes, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		ErrorMsg(i18n.T("Failed to serialize the configuration: {{.Err}}", map[string]any{"Err": err}))
		return
	}

	Header(i18n.T("Current configuration", nil))
	PrintRaw(string(jsonBytes))
	pterm.Println()
	InfoMsg(i18n.T("Config file: {{.Path}}", map[string]any{"Path": config.GetDefaultConfigPath()}))
}

func init() {
	register(func(root *cobra.Command) { root.AddCommand(newConfigCmd()) })
}
