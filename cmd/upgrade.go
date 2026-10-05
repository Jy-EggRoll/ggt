// upgrade.go 实现 "ggt upgrade" 命令：检查并升级到最新版本。
//
// 升级器的通用能力（查 Release、比版本、下载、校验 SHA-256、原子替换可执行文件）全部来自
// eggokit/updater，本文件只负责 ggt 专属的三件事：仓库坐标与资产命名（见 upgrade_target.go）、
// 命令行的 flag 语义、以及把升级器的输出接到 ggt 的统一输出（见 upgrade_reporter.go）。
package cmd

import (
	"fmt"
	"runtime"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/eggokit/updater"
	"github.com/spf13/cobra"
)

// newUpgradeCmd 构造 "ggt upgrade" 命令。
//
// 命令树在语言加载之后才构造（见 registry.go），因此这里的 T() 都是真实翻译调用。
// 采用 RunE 而非 Run，是为了让 flag 读取失败、升级失败等错误沿 cobra 的错误链返回，
// 由 Execute 在唯一边界统一打印并决定退出码，命令内部绝不调用 os.Exit
func newUpgradeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "upgrade",
		Aliases: []string{"update", "up"},
		Short:   l10n.T("Check for and upgrade to the latest version", nil),
		Long:    l10n.T("Check for and upgrade ggt to the latest version", nil),
		RunE:    runUpgrade,
	}

	c.Flags().Bool("check", false, l10n.T("Only check the version, do not upgrade", nil))
	c.Flags().Bool("force", false, l10n.T("Force upgrade", nil))
	c.Flags().Bool("dev", false, l10n.T("Check for development versions", nil))
	// --yes 是本命令的局部 flag（ggt 没有全局的非交互总开关）：
	// 非交互环境（CI、管道、stdin 接 /dev/null）下确认无法进行，必须靠它显式放行，
	// 与 config reset --all 的处理方式保持一致
	c.Flags().Bool("yes", false, l10n.T("Skip the confirmation prompt (required in non-interactive environments)", nil))

	return c
}

// platformLabel 产出面向用户展示的当前平台标签，形如 linux-amd64。
//
// 它只用于展示，不要用它去替换 upgrade_target.go 中按 GOOS/GOARCH 匹配发布资产名的逻辑：
// 那里是“选哪个发布产物”的构建坐标，与这里“给用户看的名字”语义不同，合并会破坏资产匹配。
// 当前只有 upgrade 一处使用，因此就近放在本文件，不单独抽到别处
func platformLabel() string {
	platform := fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		platform += " (exe)"
	}
	return platform
}

// runUpgrade 完成版本检查、用户确认与升级执行。
//
// 业务失败只返回带上下文的错误交由根命令统一打印一次，命令内部绝不调用 os.Exit，
// 以免绕过 Execute 的退出码边界
func runUpgrade(cmd *cobra.Command, args []string) error {
	// GetBool 读取 flag 的真实布尔值，避免 --check=false 这类显式 false 被 Changed 误判为启用；
	// 任一读取失败都必须沿 RunE 返回，不能以零值继续执行而掩盖命令定义或测试注入错误
	checkOnly, err := cmd.Flags().GetBool("check")
	if err != nil {
		return fmt.Errorf("%s: %w", l10n.T("Failed to read the --check argument", nil), err)
	}
	forceUpdate, err := cmd.Flags().GetBool("force")
	if err != nil {
		return fmt.Errorf("%s: %w", l10n.T("Failed to read the --force argument", nil), err)
	}
	checkDev, err := cmd.Flags().GetBool("dev")
	if err != nil {
		return fmt.Errorf("%s: %w", l10n.T("Failed to read the --dev argument", nil), err)
	}
	assumeYes, err := cmd.Flags().GetBool("yes")
	if err != nil {
		return fmt.Errorf("%s: %w", l10n.T("Failed to read the --yes argument", nil), err)
	}

	// 通道决定候选版本的取舍：正式版用户永远不会被投递开发版，开发版通道供希望提前验证的用户主动选择
	channel := updater.ChannelStable
	channelLabel := l10n.T("stable", nil)
	if checkDev {
		channel = updater.ChannelDev
		channelLabel = l10n.T("dev", nil)
	}

	// reporter 承接升级器的全部用户可见输出与确认；令牌查找结果被升级器与下面的提示共用，只会真正查找一次
	reporter := &ptermReporter{assumeYes: assumeYes}
	tokenProvider := updater.EnvTokenProvider("")

	// 升级器只接收注入的仓库坐标、资产规则与界面，不感知 ggt 的任何发布约定
	upgrader, err := updater.New(updater.Config{
		Owner:         upstreamOwner,
		Repo:          upstreamRepo,
		AssetName:     ggtAssetName,
		Reporter:      reporter,
		TokenProvider: tokenProvider,
		ProxyPrefix:   downloadProxyPrefix,
		UserAgent:     "ggt-updater",
	})
	if err != nil {
		return fmt.Errorf("%s: %w", l10n.T("Failed to initialize the upgrader", nil), err)
	}

	reporter.Info("%s", l10n.T("Checking for updates ({{.Channel}})...", map[string]any{"Channel": channelLabel}))
	reporter.Info("%s", l10n.T("Current platform: {{.Platform}}", map[string]any{"Platform": platformLabel()}))

	// 令牌状态提示属于 ggt 的运维建议而非升级器职责，因此留在命令层；
	// 只在缺失时提示，配置正确的用户无需被无关信息打扰
	if tokenProvider() == "" {
		reporter.Info("%s", l10n.T("No GitHub token configured; API requests are subject to anonymous rate limits. Set GITHUB_TOKEN to raise the quota", nil))
	}

	// 版本与构建时间由发布构建注入（见 version.go）；本地开发构建下没有注入值，
	// 由 effectiveVersion 补一个可读占位，避免出现版本号位置为空的提示。
	// 此时升级器无法解析版本，会跳过比较，退化为“列出目标通道中的最高版本”，
	// 由展示层避免使用“更新”这类断言
	info, err := upgrader.Check(effectiveVersion(), BuildTime, channel)
	if err != nil {
		return fmt.Errorf("%s: %w", l10n.T("Failed to check for updates", nil), err)
	}
	if info == nil {
		reporter.Success("%s", l10n.T("Already on the latest version", nil))
		return nil
	}

	reporter.Info("%s", l10n.T("Current version: {{.Version}} (build: {{.Build}})", map[string]any{"Version": info.CurrentVersion, "Build": info.CurrentBuildTime}))
	if info.CurrentComparable {
		reporter.Info("%s", l10n.T("Latest version: {{.Version}}", map[string]any{"Version": info.LatestVersion}))
	} else {
		// 本地版本无法比较时不能宣称“最新”：这里给出的只是通道内的最高版本，
		// 是否比本地构建新需要用户自行判断
		reporter.Info("%s", l10n.T("Highest version in the {{.Channel}} channel: {{.Version}}", map[string]any{"Channel": channelLabel, "Version": info.LatestVersion}))
	}

	// 只检查模式到此为止：不触碰可执行文件，也不进入确认环节
	if checkOnly {
		return nil
	}

	if !forceUpdate {
		confirmed, confirmErr := reporter.Confirm(l10n.T("Upgrade to {{.Version}}?", map[string]any{"Version": info.LatestVersion}))
		if confirmErr != nil {
			return fmt.Errorf("%s: %w", l10n.T("Failed to read the upgrade confirmation", nil), confirmErr)
		}
		if !confirmed {
			reporter.Info("%s", l10n.T("Upgrade cancelled", nil))
			return nil
		}
	}

	reporter.Info("%s", l10n.T("Starting upgrade...", nil))
	if err := upgrader.Apply(info); err != nil {
		return fmt.Errorf("%s: %w", l10n.T("Upgrade failed", nil), err)
	}

	// 替换完成后当前进程仍运行旧版本，必须明确告知用户何时生效，
	// 避免用户在当前会话中反复执行命令却看不到版本变化
	reporter.Success("%s", l10n.T("Upgrade complete; it takes effect after restarting ggt", nil))
	return nil
}

func init() {
	register(func(root *cobra.Command) { root.AddCommand(newUpgradeCmd()) })
}
