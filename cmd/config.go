// config.go 实现 "ggt config" 及其子命令：查看、按项读写、重置、体检。
//
// 本子树刻意遮蔽 root 的 PersistentPreRunE（见 newConfigCmd 的说明），因此这里
// **一律不得调用 GetConfig()**（那个全局 cfg 是 nil），所有读写都直接落到配置文件。
//
// get 与 validate 的输出面向脚本消费，**全程不走 pterm**：pterm 不检测 TTY，
// 会把 ANSI 转义写进管道，让 `ggt config show | jq` 之类的用法直接失败。
package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"ggt/internal/config"
	"ggt/internal/i18n"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// newConfigCmd 实现 "ggt config" 及其子命令。
func newConfigCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: i18n.T("Show and edit the configuration", nil),
		Long: i18n.T(`Show and edit ggt's configuration.

Examples:
  ggt config                     Show the current configuration
  ggt config get <key>           Print the effective value of one setting
  ggt config set <key> <value>   Set a value
  ggt config reset <key>         Remove a setting so its default applies
  ggt config reset --all         Delete the config file
  ggt config validate            Check the config file for problems

Repository paths are managed by "ggt repo" instead of "ggt config set".`, nil),
		RunE: showConfig,
		// 遮蔽 root 的 PersistentPreRunE。root 的实现在所有子命令前跑 LoadConfig，
		// 而它对损坏的配置文件直接返回 error —— 于是"最该报出问题的 validate"和
		// "唯一能救命的 reset --all"都会在 RunE 之前被挡下，用户只能手动 rm。
		// cobra 只执行向上找到的第一个 PersistentPreRunE，挂个空实现即可遮蔽。
		//
		// 代价是这些命令里全局 cfg 为 nil，一律不得调用 GetConfig()——
		// 这与"只读原始 JSON"的设计本就一致。
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
	}
	c.AddCommand(
		newConfigShowCmd(),
		newConfigPathCmd(),
		newConfigGetCmd(),
		newConfigSetCmd(),
		newConfigResetCmd(),
		newConfigValidateCmd(),
	)

	// 取值校验失败时不该顺带打印整段 usage：错误信息本身已经说清问题，
	// 而 get / validate 的输出面向脚本，多余的 usage 会污染管道。
	// 这里逐个设置而不是设到 root 上，避免影响其它命令的用法提示
	c.SilenceUsage = true
	for _, sub := range c.Commands() {
		sub.SilenceUsage = true
	}
	return c
}

// showConfig 以 JSON 格式打印当前生效配置。
func showConfig(cmd *cobra.Command, args []string) error {
	// 不读全局 cfg（本子树遮蔽了 PersistentPreRunE，那是 nil），直接从文件重新加载，
	// 语义与 get 保持一致：只看配置文件 + 补默认值
	cfg, err := config.LoadConfig()
	if err != nil {
		ErrorMsg(i18n.T("Cannot read the config file: {{.Err}}", map[string]any{"Err": err}))
		InfoMsg(i18n.T("Run \"ggt config validate\" to see what is wrong", nil))
		return errSilent
	}

	jsonBytes, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return errors.New(i18n.T("Failed to serialize the configuration: {{.Err}}", map[string]any{"Err": err}))
	}

	Header(i18n.T("Current configuration", nil))
	PrintRaw(string(jsonBytes))
	pterm.Println()
	InfoMsg(i18n.T("Config file: {{.Path}}", map[string]any{"Path": config.GetDefaultConfigPath()}))
	return nil
}

// newConfigShowCmd 与 configCmd 相同，提供明确的 show 子命令。
func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: i18n.T("Show the current configuration", nil),
		Args:  cobra.NoArgs,
		RunE:  showConfig,
	}
}

// newConfigPathCmd 显示配置文件路径。
func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: i18n.T("Show the path to the config file", nil),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// 走裸 fmt 而非 pterm：路径经常被脚本直接取用
			fmt.Println(config.GetDefaultConfigPath())
			return nil
		},
	}
}

// newConfigGetCmd 打印某项的配置文件生效值。
func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: i18n.T("Print the effective value of one setting", nil),
		Long: i18n.T(`Print the value of one setting as it takes effect at runtime.

The value comes from the config file with defaults filled in. Command-line
overrides such as -c are NOT taken into account, since they only apply to the
current run.

Output has no colors and is meant to be piped: strings, numbers and booleans are
printed as-is, and path lists one entry per line.

Examples:
  ggt config get concurrency
  ggt config get repo_paths`, nil),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			v, err := config.EffectiveAt(config.GetDefaultConfigPath(), args[0])
			if err != nil {
				if errors.Is(err, config.ErrUnknownKey) {
					return errUnknownKey(args[0])
				}
				return err
			}
			printConfigValue(v)
			return nil
		},
	}
}

// newConfigSetCmd 校验并写入单个配置项。
func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: i18n.T("Set a configuration value", nil),
		Long: i18n.T(`Validate and write one setting into the config file.

Other keys in the file are left untouched, including keys ggt does not know about.

Examples:
  ggt config set concurrency CPUFull
  ggt config set size_unit binary
  ggt config set language zh-CN`, nil),
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]

			s, ok := config.Lookup(key)
			if !ok {
				return errUnknownKey(key)
			}
			if s.ManagedBy != "" {
				return errors.New(i18n.T("{{.Key}} is managed by \"{{.Command}}\" and cannot be set here",
					map[string]any{"Key": s.Key, "Command": s.ManagedBy}))
			}

			parsed, err := s.Parse(value)
			if err != nil {
				return errInvalidValue(s.Key, value, s.Expected)
			}
			if err := config.SetKey(s.Key, parsed); err != nil {
				return err
			}

			SuccessMsg(i18n.T("{{.Key}} = {{.Value}}", map[string]any{"Key": s.Key, "Value": config.ValueText(parsed)}))
			if s.Key == "language" {
				// 语言在进程启动时就由 i18n.Init 定下了，改配置不会影响当前这次输出。
				// 刻意不在这里重新 Init：那会违反 i18n 包"Init 之后状态只读"的契约
				InfoMsg(i18n.T("The new language takes effect on the next run", nil))
			}
			return nil
		},
	}
}

// newConfigResetCmd 重置单项或全部配置。
func newConfigResetCmd() *cobra.Command {
	var (
		all      bool
		defaults bool
		yes      bool
	)

	c := &cobra.Command{
		Use:   "reset [key]",
		Short: i18n.T("Reset one setting, or the whole configuration", nil),
		Long: i18n.T(`Reset configuration.

By default the value is REMOVED from the file, so the setting falls back to its
built-in default. With --defaults the default value is written explicitly instead.

--all applies the same choice to everything: it deletes the config file, or with
--defaults writes a file containing only default values. Either way the registered
repositories are cleared — manage them one by one with "ggt repo" if that is not
what you want.

Examples:
  ggt config reset concurrency              Remove the key from the file
  ggt config reset concurrency --defaults   Write the default value instead
  ggt config reset --all                    Delete the config file
  ggt config reset --all --defaults         Write a defaults-only config file
  ggt config reset --all --yes              Skip the confirmation prompt`, nil),
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// key 与 --all 必须恰有其一。两者都给或都不给都属语义不明，直接拒绝，
			// 避免"我明明指定了 key，怎么把整个配置删了"这类误解
			if all == (len(args) == 1) {
				return errors.New(i18n.T("Specify either a key or --all, but not both", nil))
			}
			if all {
				return resetAllConfig(defaults, yes)
			}
			return resetOneKey(args[0], defaults)
		},
	}

	c.Flags().BoolVar(&all, "all", false,
		i18n.T("Reset the whole configuration instead of a single key", nil))
	c.Flags().BoolVar(&defaults, "defaults", false,
		i18n.T("Write the default value instead of removing the setting", nil))
	c.Flags().BoolVar(&yes, "yes", false,
		i18n.T("Skip the confirmation prompt (only used with --all)", nil))
	return c
}

// resetOneKey 重置单个配置项。
func resetOneKey(key string, writeDefault bool) error {
	s, ok := config.Lookup(key)
	if !ok {
		return errUnknownKey(key)
	}
	if s.ManagedBy != "" {
		return errors.New(i18n.T("{{.Key}} is managed by \"{{.Command}}\"; use that command to change it",
			map[string]any{"Key": s.Key, "Command": s.ManagedBy}))
	}

	defaultText := config.ValueText(s.Default)
	if writeDefault {
		if err := config.SetKey(s.Key, s.Default); err != nil {
			return err
		}
		SuccessMsg(i18n.T("{{.Key}} was reset to its default ({{.Value}})",
			map[string]any{"Key": s.Key, "Value": defaultText}))
		return nil
	}

	if err := config.UnsetKey(s.Key); err != nil {
		return err
	}
	SuccessMsg(i18n.T("{{.Key}} was removed from the config file; the default ({{.Value}}) now applies",
		map[string]any{"Key": s.Key, "Value": defaultText}))
	return nil
}

// resetAllConfig 重置整份配置：删除文件，或写入一份全默认值的文件。
func resetAllConfig(writeDefaults, yes bool) error {
	path := config.GetDefaultConfigPath()

	// 删除是不可逆的，且会丢掉全部仓库记录，必须先让损失可见并取得确认。
	// 写默认值这条路径是安全操作（结果与"从未配置过"等价），不需要确认
	if !writeDefaults {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			InfoMsg(i18n.T("There is no config file to delete: {{.Path}}", map[string]any{"Path": path}))
			return nil
		}
		if !yes {
			// 非交互环境下 pterm 的确认会读到 EOF 或直接挂住，明确报错让用户加 --yes
			if !stdinIsTerminal() {
				return errors.New(i18n.T("Refusing to delete {{.Path}} without confirmation; re-run with --yes",
					map[string]any{"Path": path}))
			}
			WarnMsg(i18n.T("This will delete {{.Path}} and forget every registered repository ({{.Count}} entries)",
				map[string]any{"Path": path, "Count": registeredEntryCount(path)}))
			confirmed, err := pterm.DefaultInteractiveConfirm.WithDefaultValue(false).Show()
			if err != nil {
				return err
			}
			if !confirmed {
				InfoMsg(i18n.T("Aborted; nothing was changed", nil))
				return nil
			}
		}
	}

	if err := config.ResetAll(writeDefaults); err != nil {
		return err
	}

	if writeDefaults {
		SuccessMsg(i18n.T("Wrote a defaults-only config file: {{.Path}}", map[string]any{"Path": path}))
		InfoMsg(i18n.T("Registered repositories were cleared; add them again with \"ggt repo add\"", nil))
		return nil
	}
	SuccessMsg(i18n.T("Deleted the config file: {{.Path}}", map[string]any{"Path": path}))
	return nil
}

// newConfigValidateCmd 体检配置文件。
func newConfigValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: i18n.T("Check the config file for problems", nil),
		Long: i18n.T(`Check the config file and report anything that would be silently ignored,
silently replaced by a default, or that makes the file unreadable.

Output has no colors so it can be consumed by scripts. The exit code is 1 when at
least one error is found.

Examples:
  ggt config validate`, nil),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := config.GetDefaultConfigPath()

			// 文件不存在不是问题：默认配置本来就允许不存在
			if _, err := os.Stat(path); os.IsNotExist(err) {
				fmt.Println(i18n.T("No config file at {{.Path}}; ggt is running on built-in defaults",
					map[string]any{"Path": path}))
				return nil
			}

			issues, err := config.ValidateAt(path)
			if err != nil {
				return err
			}
			if len(issues) == 0 {
				fmt.Println(i18n.T("No problems found in {{.Path}}", map[string]any{"Path": path}))
				return nil
			}

			errorCount := 0
			for _, issue := range issues {
				if issue.Level == config.LevelError {
					errorCount++
				}
				fmt.Printf("%s %s\n", levelTag(issue.Level), issue.Message)
			}
			fmt.Println()
			fmt.Println(i18n.T("{{.Errors}} error(s), {{.Warnings}} warning(s)",
				map[string]any{"Errors": errorCount, "Warnings": len(issues) - errorCount}))

			if errorCount > 0 {
				// 直接退出而不是返回 error：root 的 Execute 会把错误再包一层
				// "Execution failed: ..."，对面向脚本的输出是噪音
				os.Exit(1)
			}
			return nil
		},
	}
}

// errSilent 表示"错误已经打印过，不要再包一层"。
// Execute 会识别它并跳过 "Execution failed:" 前缀，避免同一件事提示两遍。
var errSilent = errors.New("error already reported")

// errUnknownKey 生成"未知键"的统一提示，顺带告诉用户怎么列出全部键。
func errUnknownKey(key string) error {
	return errors.New(i18n.T("Unknown config key: {{.Key}} (run \"ggt config --help\" to see the available keys)",
		map[string]any{"Key": key}))
}

// errInvalidValue 生成"值非法"的统一提示。
// 所有键的值错误都收敛到这一条模板，中英双语各只需一条文案，
// 否则文案数量会随校验规则数线性增长。
func errInvalidValue(key, value, expected string) error {
	return errors.New(i18n.T("Invalid value for {{.Key}}: {{.Value}} (expected {{.Expected}})",
		map[string]any{"Key": key, "Value": value, "Expected": expected}))
}

// levelTag 返回体检条目的级别标记。
// 刻意用固定 ASCII 且不翻译：validate 面向脚本，可 grep 的稳定性比本地化更重要。
func levelTag(level config.Level) string {
	if level == config.LevelError {
		return "[error]  "
	}
	return "[warning]"
}

// registeredEntryCount 统计配置文件里登记的仓库与父目录条目数，用于在删除前展示损失。
func registeredEntryCount(path string) int {
	raw, err := config.ReadRawAt(path)
	if err != nil {
		return 0
	}
	count := 0
	for _, key := range []string{"repo_paths", "parent_paths"} {
		if list, ok := raw[key].([]any); ok {
			count += len(list)
		}
	}
	return count
}

// printConfigValue 以脚本友好的形式打印配置值：标量裸值、数组每行一个、空数组不输出。
// 全程走裸 fmt，不经 pterm 着色。
func printConfigValue(v any) {
	switch list := v.(type) {
	case []any:
		for _, item := range list {
			fmt.Println(config.ValueText(item))
		}
	case []string:
		for _, item := range list {
			fmt.Println(item)
		}
	default:
		fmt.Println(config.ValueText(v))
	}
}

// stdinIsTerminal 判断标准输入是否为真正的终端。
// 用于在非交互环境下拒绝需要确认的破坏性操作，而不是让 pterm 读到 EOF 或挂住。
//
// 必须用 x/term 的 ioctl 检测，不能用 `os.Stdin.Stat()` 的 ModeCharDevice：
// **/dev/null 也是字符设备**，脚本里 `ggt config reset --all`（stdin 接 /dev/null）
// 会被误判成交互环境，然后卡在确认提示上永远等不到输入。
func stdinIsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

func init() {
	register(func(root *cobra.Command) { root.AddCommand(newConfigCmd()) })
}
