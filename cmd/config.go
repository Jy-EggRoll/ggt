// config.go 实现 "ggt config" 及其子命令：查看、按项读写、重置、体检。
//
// 本子树刻意遮蔽 root 的 PersistentPreRunE（见 newConfigCmd 的说明），因此这里
// **一律不得调用 GetConfig()**（那个全局 cfg 是 nil），所有读写都直接落到配置文件。
//
// get 与 validate 的输出面向脚本处理，**全程不走 pterm**：pterm 不检测 TTY，
// 会把 ANSI 转义写进管道，让 `ggt config show | jq` 之类的用法直接失败。
package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/ggt/internal/config"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// newConfigCmd 实现 "ggt config" 及其子命令。
func newConfigCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: l10n.T("Show and edit the configuration", nil),
		Long: l10n.T(`Show and edit ggt's configuration.

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
		// 而它对损坏的配置文件直接返回 error —— 于是“最该报出问题的 validate”和
		// “唯一能救命的 reset --all”都会在 RunE 之前被挡下，用户只能手动 rm。
		// cobra 只执行向上找到的第一个 PersistentPreRunE，挂个空实现即可遮蔽。
		//
		// 代价是这些命令里全局 cfg 为 nil，一律不得调用 GetConfig()——
		// 这与“只读原始 JSON”的设计本就一致。
		//
		// 遮蔽掉了 root 的钩子，也就连带跳过了它对 -c 的校验。用户既然在 config 命令上
		// 写了 -c，非法值（含空串与纯空白）就不该被静默吞掉，因此这里按命令行那一套
		// 边界单独校验一次；合法值不产生副作用——config 子树不跑并发任务，-c 本来就没有含义
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("concurrency") {
				if _, err := config.ValidateCommandLineConcurrency(concurrency); err != nil {
					return err
				}
			}
			return nil
		},
	}
	c.AddCommand(
		newConfigShowCmd(),
		newConfigPathCmd(),
		newConfigGetCmd(),
		newConfigSetCmd(),
		newConfigResetCmd(),
		newConfigValidateCmd(),
	)

	// 报错时不打印整段用法：本子树的 get / validate 输出面向脚本，更不该混进 usage。
	// 这条策略统一挂在根命令上（见 newRootCmd），子树不必再各设一次
	return c
}

// showConfig 以纯 JSON 打印当前生效配置。
//
// stdout 只有那份 JSON：`ggt config show | jq` 是文档里写着的用法，而它最需要成立的时刻
// 恰恰是“配置正好坏着、要靠输出核对文件里到底读到了什么”。因此问题清单与文件路径一律写
// stderr——它们对交互使用照常可见，又不会混进管道
func showConfig(cmd *cobra.Command, args []string) error {
	// 不读全局 cfg（本子树遮蔽了 PersistentPreRunE，那是 nil），直接从文件重新加载，
	// 语义与 get 保持一致：只看配置文件 + 补默认值。
	//
	// 走可恢复加载而不是严格加载：配置坏了的时候，光看一遍当前配置正是排查的第一步。
	// 坏掉的键按默认值显示，并逐条说明坏在哪、怎么修
	cfg, problems, err := recoverConfig()
	if err != nil {
		ErrorMsg(l10n.T("Cannot read the config file: {{.Err}}", map[string]any{"Err": err}))
		return errSilent
	}

	jsonBytes, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return errors.New(l10n.T("Failed to serialize the configuration: {{.Err}}", map[string]any{"Err": err}))
	}

	printConfigProblems(cmd.ErrOrStderr(), problems)
	// 用 fmt 而不是 PrintRaw：这里要的是“纯文本 JSON”，PrintRaw 是给带 ANSI 的 git 输出
	// 准备的透传通道，混用会让这条命令的意图变模糊
	fmt.Println(string(jsonBytes))
	fmt.Fprintln(cmd.ErrOrStderr(),
		l10n.T("Config file: {{.Path}}", map[string]any{"Path": config.GetDefaultConfigPath()}))
	return nil
}

// newConfigShowCmd 与 configCmd 相同，提供明确的 show 子命令。
//
// 帮助里写明“stdout 只有 JSON”：这条命令最常见的用法是接 jq，而坏键提示恰好也在它的
// 输出里出现过——用户需要知道提示已经让到了 stderr，管道里不会混进不是 JSON 的行
func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: l10n.T("Show the current configuration", nil),
		Long: l10n.T(`Print the effective configuration as JSON.

JSON goes to stdout, so the output can be piped (for example into jq). Anything else
this command has to say — a value that is invalid in the file, the path of the config
file — goes to stderr.`, nil),
		Args: cobra.NoArgs,
		RunE: showConfig,
	}
}

// newConfigPathCmd 显示配置文件路径。
func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: l10n.T("Show the path to the config file", nil),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// 直接用 fmt 而非 pterm：路径经常被脚本直接取用
			fmt.Println(config.GetDefaultConfigPath())
			return nil
		},
	}
}

// newConfigGetCmd 打印某项的配置文件生效值。
func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: l10n.T("Print the effective value of one setting", nil),
		Long: l10n.T(`Print the value of one setting as it takes effect at runtime.

The value comes from the config file with defaults filled in. Command-line
overrides such as -c are NOT taken into account, since they only apply to the
current run.

Output has no colors and is meant to be piped: strings, numbers and booleans are
printed as-is, and path lists one entry per line.

A value that is invalid at runtime (for example an out-of-range concurrency) is
not printed: the command exits non-zero with the reason, so a script never reads
a value that the next ggt run would reject.

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
		Short: l10n.T("Set a configuration value", nil),
		Long: l10n.T(`Validate and write one setting into the config file.

Other keys in the file are left untouched.

An empty value is rejected where it would silently mean a default: write "auto"
for concurrency instead of an empty string.

Examples:
  ggt config set concurrency auto
  ggt config set concurrency 8
  ggt config set size_unit binary
  ggt config set language zh-CN`, nil),
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]

			s, ok := config.Lookup(key)
			if !ok {
				return errUnknownKey(key)
			}

			// 校验与写入走 config.SetFromTextAt，与网页设置面板是同一份实现：合法取值
			// 只由注册表的 Normalize 判定，候选清单只是页面能直接点的几个（因此命令行与
			// 面板对同一个取值不可能给出不同结论）
			written, err := config.SetFromTextAt(config.GetDefaultConfigPath(), s.Key, value)
			if err != nil {
				return err
			}

			SuccessMsg(l10n.T("{{.Key}} = {{.Value}}", map[string]any{"Key": s.Key, "Value": written}))
			if note := settingNote(s.Key); note != "" {
				InfoMsg(note)
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
		Short: l10n.T("Reset one setting, or the whole configuration", nil),
		Long: l10n.T(`Reset configuration.

By default the value is REMOVED from the file, so the setting falls back to its
built-in default. With --defaults the default value is written explicitly instead.

The repository lists are normally managed by "ggt repo", but when their current
value is invalid this command is allowed once to clear the broken value. Clearing
one reports how many entries it held and how many were still usable; when any of
them are usable, the removal has to be confirmed first (--yes skips the prompt, and
is required when stdin is not a terminal).

A key that ggt does not know is removed from the file if it is present there.

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
			// 避免“我明明指定了 key，怎么把整个配置删了”这类误解
			if all == (len(args) == 1) {
				return errors.New(l10n.T("Specify either a key or --all, but not both", nil))
			}
			if all {
				return resetAllConfig(defaults, yes)
			}
			return resetOneKey(args[0], defaults, yes)
		},
	}

	c.Flags().BoolVar(&all, "all", false,
		l10n.T("Reset the whole configuration instead of a single key", nil))
	c.Flags().BoolVar(&defaults, "defaults", false,
		l10n.T("Write the default value instead of removing the setting", nil))
	c.Flags().BoolVar(&yes, "yes", false,
		l10n.T("Skip the confirmation prompt (used with --all, and when reset would drop usable entries of a broken repository list)", nil))
	return c
}

// resetOneKey 重置单个配置项，或清掉文件里一个未知的键。
//
// 能否删除由 config.PlanUnsetAt 判定，与设置面板的“恢复默认”共用同一份规则：
// 受管项平时不许动，但它的取值非法时放行一次——受管项坏掉之后所有命令都起不来，
// 唯一能落地的修复就是清掉它。未知键只在“文件里确实写着它”时放行，
// 拼错键名因此当场暴露，而不是回一句“已删除”让人以为清掉了什么。
//
// yes 只影响“清理坏掉的仓库列表”这一条路径：列表里还有可用条目时，清掉它就等于删掉
// 几个本来能用的仓库，必须先确认（与 reset --all 同一套处理，非交互时同样需要 --yes）。
func resetOneKey(key string, writeDefault, yes bool) error {
	plan, err := config.PlanUnsetAt(config.GetDefaultConfigPath(), key)
	if err != nil {
		if errors.Is(err, config.ErrUnknownKey) {
			return errUnknownKey(key)
		}
		return err
	}

	if !plan.Known {
		if writeDefault {
			// 未知键没有默认值可写；这里要说清该用哪一种 reset，而不是让用户以为命令没生效
			return errors.New(l10n.T("{{.Key}} is not a known setting, so it has no default value; run {{.Command}} to remove it",
				map[string]any{"Key": plan.Key, "Command": config.ResetCommand(plan.Key)}))
		}
		if err := config.UnsetKey(plan.Key); err != nil {
			return err
		}
		SuccessMsg(l10n.T("{{.Key}} is not a known setting; removed it from the config file",
			map[string]any{"Key": plan.Key}))
		return nil
	}

	s, _ := config.Lookup(plan.Key)
	if plan.Managed {
		proceed, err := confirmClearingBrokenList(s, yes)
		if err != nil {
			return err
		}
		if !proceed {
			return nil
		}
	}

	defaultText := config.ValueText(s.Default)
	if writeDefault {
		if err := config.SetKey(s.Key, s.Default); err != nil {
			return err
		}
		SuccessMsg(l10n.T("{{.Key}} was reset to its default ({{.Value}})",
			map[string]any{"Key": s.Key, "Value": defaultText}))
		return nil
	}

	if err := config.UnsetKey(s.Key); err != nil {
		return err
	}
	SuccessMsg(l10n.T("{{.Key}} was removed from the config file; the default ({{.Value}}) now applies",
		map[string]any{"Key": s.Key, "Value": defaultText}))
	return nil
}

// confirmClearingBrokenList 说清这次是清理非法值，并在会连带丢掉可用仓库时要求确认。
//
// 为什么必须报条数：宽松加载已经丢掉坏条目，“清掉一个坏值”听起来很轻，实际可能连同
// 几个本来还能用的仓库一起消失。用户需要先知道损失，再决定要不要继续。
//
// 非交互环境缺 --yes 时直接拒绝而不是卡在确认上：脚本与 CI 里没有人回答提示，
// 这与 reset --all 的处理是同一条约定。
func confirmClearingBrokenList(s config.Setting, yes bool) (bool, error) {
	WarnMsg(managedResetNote(s.Key, s.ManagedBy))

	total, usable, err := config.PathListCounts(config.GetDefaultConfigPath(), s.Key)
	if err != nil {
		return false, err
	}
	if total > 0 {
		// 条数用“总数 / 可用数”两个独立数值表达，避开英文单复数的配合问题：
		// 写成 “{{.Total}} entries” 时总数为 1 就会打印 “1 entries”
		WarnMsg(l10n.T("Current value: total {{.Total}}, usable {{.Usable}}; clearing removes all of them",
			map[string]any{"Total": total, "Usable": usable}))
	}
	if usable == 0 || yes {
		return true, nil
	}
	if !stdinIsTerminal() {
		return false, errors.New(l10n.T("Refusing to clear {{.Key}} without confirmation: usable entries ({{.Usable}}) would be lost; re-run with --yes",
			map[string]any{"Usable": usable, "Key": s.Key}))
	}
	confirmed, err := pterm.DefaultInteractiveConfirm.WithDefaultValue(false).Show()
	if err != nil {
		return false, err
	}
	if !confirmed {
		InfoMsg(l10n.T("Aborted; nothing was changed", nil))
		return false, nil
	}
	return true, nil
}

// managedResetNote 说明“这次清理的是受管项里一个已经坏掉的值”。
//
// 命令行 reset 与设置面板共用这一句：受管约束的是“怎么设值”，不该拦住“怎么清除一个坏值”，
// 因此这次放行要讲清楚——否则用户会以为 ggt repo 的管理规则被绕过去了
func managedResetNote(key, command string) string {
	return l10n.T("{{.Key}} is normally managed by \"{{.Command}}\", but its current value is invalid, so it is cleared to recover the config file",
		map[string]any{"Key": key, "Command": command})
}

// resetAllConfig 重置整份配置：删除文件，或写入一份全默认值的文件。
func resetAllConfig(writeDefaults, yes bool) error {
	path := config.GetDefaultConfigPath()

	// 删除是不可逆的，且会丢掉全部仓库记录，必须先让损失可见并取得确认。
	// 写默认值这条路径是安全操作（结果与“从未配置过”等价），不需要确认
	if !writeDefaults {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			InfoMsg(l10n.T("There is no config file to delete: {{.Path}}", map[string]any{"Path": path}))
			return nil
		}
		if !yes {
			// 非交互环境下 pterm 的确认会读到 EOF 或直接挂住，明确报错让用户加 --yes
			if !stdinIsTerminal() {
				return errors.New(l10n.T("Refusing to delete {{.Path}} without confirmation; re-run with --yes",
					map[string]any{"Path": path}))
			}
			WarnMsg(l10n.T("This will delete {{.Path}} and forget every registered repository ({{.Count}} entries)",
				map[string]any{"Path": path, "Count": registeredEntryCount(path)}))
			confirmed, err := pterm.DefaultInteractiveConfirm.WithDefaultValue(false).Show()
			if err != nil {
				return err
			}
			if !confirmed {
				InfoMsg(l10n.T("Aborted; nothing was changed", nil))
				return nil
			}
		}
	}

	if err := config.ResetAll(writeDefaults); err != nil {
		return err
	}

	if writeDefaults {
		SuccessMsg(l10n.T("Wrote a defaults-only config file: {{.Path}}", map[string]any{"Path": path}))
		InfoMsg(l10n.T("Registered repositories were cleared; add them again with \"ggt repo add\"", nil))
		return nil
	}
	SuccessMsg(l10n.T("Deleted the config file: {{.Path}}", map[string]any{"Path": path}))
	return nil
}

// newConfigValidateCmd 体检配置文件。
func newConfigValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: l10n.T("Check the config file for problems", nil),
		Long: l10n.T(`Check the config file and report anything that makes it unreadable or
unusable at runtime.

Output has no colors so it can be consumed by scripts. The exit code is 1 when at
least one error is found.

Examples:
  ggt config validate`, nil),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := config.GetDefaultConfigPath()

			// 文件不存在不是问题：默认配置本来就允许不存在
			if _, err := os.Stat(path); os.IsNotExist(err) {
				fmt.Println(l10n.T("No config file at {{.Path}}; ggt is running on built-in defaults",
					map[string]any{"Path": path}))
				return nil
			}

			issues, err := config.ValidateAt(path)
			if err != nil {
				return err
			}
			if len(issues) == 0 {
				fmt.Println(l10n.T("No problems found in {{.Path}}", map[string]any{"Path": path}))
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
			fmt.Println(l10n.T("{{.Errors}} error(s), {{.Warnings}} warning(s)",
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

// errSilent 表示“错误已经打印过，不要再包一层”。
// Execute 会识别它并跳过 "Execution failed:" 前缀，避免同一件事提示两遍。
var errSilent = errors.New("error already reported")

// settingNote 返回某一项写入之后要额外告诉用户的一句话，没有就返回空串。
//
// 放在这里而不是各调用方自己写：命令行的 set 与网页设置面板都要说这句话，
// 各写一份就会出现“命令行提示了、页面却没提示”这种不对称，
// 而缺了这句话的用户会反复刷新页面等一个永远不会自己生效的改动
func settingNote(key string) string {
	// 重启档的项（当前是 language 与 log_level）改完不影响本次进程，刷新页面也没用。
	// 判断走注册表的 ApplyAt，而不是把键名硬编码在这里：这样日后加一项“需重启”的配置时，
	// 命令行 set 与网页设置面板都会自动带上提示，不会漏
	s, ok := config.Lookup(key)
	if ok && s.ApplyAt == config.ApplyRestart {
		// 刻意不在这里重新 Init：那会违反 i18n 包“Init 之后状态只读”的契约
		//
		// 文案刻意不点名具体哪一项：这一档的判断是通用的，若把“语言”写死在这里，
		// 日后新增的项（如 log_level）就会弹出与它无关的一句提示。逐项的理由
		// 由注册表各自 ApplyAt 标注旁的注释承担，页面另有按项列出的生效时机
		return l10n.T("The new value takes effect on the next run", nil)
	}
	return ""
}

// errUnknownKey 生成“未知键”的统一提示，顺带告诉用户怎么列出全部键。
func errUnknownKey(key string) error {
	return errors.New(l10n.T("Unknown config key: {{.Key}} (run \"ggt config --help\" to see the available keys)",
		map[string]any{"Key": key}))
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

// printConfigValue 以脚本友好的形式打印配置值：标量原始值、数组每行一个、空数组不输出。
// 全程直接用 fmt，不经 pterm 着色。
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
