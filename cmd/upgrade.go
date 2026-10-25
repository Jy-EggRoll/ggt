// upgrade.go 把 "ggt upgrade" 命令接到 eggokit/upgradecli 的共享实现上。
//
// 命令行为（读 flag、选通道、组织提示语、决定确认时机、退出方式）全部在共享实现里，
// 本文件只提供 ggt 专属取值：程序名、发布目标（见 upgrade_target.go）、版本与构建时间
// 来源（见 version.go），以及界面实现（eggokit/ui 的 Reporter）。
//
// alpha 通道与另外两个通道不同：它指向一个固定标签的滚动预发布，主分支每次推送都会覆盖
// 同一份 Release，因此没有版本号可供比较，只能靠发布说明首行里的提交号判断新旧。这条约定
// 由共享实现承担，ggt 侧只需按原有 flag 语义把它暴露给用户。
package cmd

import (
	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/eggokit/ui"
	"github.com/jy-eggroll/eggokit/updater"
	"github.com/jy-eggroll/eggokit/upgradecli"
	"github.com/spf13/cobra"
)

// newUpgradeCmd 构造 "ggt upgrade" 命令。
//
// 命令树在语言加载之后才构造（见 registry.go），因此共享实现内部的 T() 都是真实翻译调用。
//
// --yes 是升级命令的局部参数（ggt 没有全局的非交互总开关）：非交互环境（CI、管道、
// stdin 接 /dev/null）下确认无法进行，必须靠它显式放行，与 config reset --all 的处理方式
// 保持一致。共享实现刻意不注册同名 flag，以免与宿主的定义冲突，因此这里注册，
// 再由 AssumeYes 闭包把它的取值交给共享实现
func newUpgradeCmd() *cobra.Command {
	var assumeYes bool

	c := upgradecli.NewCommand(upgradecli.Options{
		Program:   "ggt",
		Target:    ggtTarget,
		Version:   func() string { return versionOptions().EffectiveVersion() },
		BuildTime: func() string { return BuildTime },
		// 升级过程的界面来自 eggokit/ui，写入目标是命令的 stderr。
		//
		// 这里有一处用户可感知的输出流变化：迁移前 Info/Warn/Success 走 output.go 的
		// InfoMsg 等封装，即 pterm 的默认输出（标准输出）；共享实现按构造时传入的 writer 写入。
		// 升级过程属于过程性状态而不是业务数据，写 stderr 能让 `ggt upgrade > log` 这类重定向
		// 只剩业务结果，也与 logger 跟随 stderr、pterm.Error 指向 stderr 的既有取向一致。
		//
		// 交互询问仍写 pterm 的全局输出（即标准输出）：pterm 的确认组件没有按实例指定 writer
		// 的入口（事实：v0.12.83 的 InteractiveConfirmPrinter 只有样式类 With 方法）。
		// 要让它也跟随 stderr，需要改 pterm 的默认输出，那会连带改掉其它命令的标准输出，
		// 超出这次迁移的范围。
		Reporter:  func(cmd *cobra.Command) updater.Reporter { return ui.NewReporter(cmd.ErrOrStderr()) },
		AssumeYes: func() bool { return assumeYes },
	})

	c.Flags().BoolVar(&assumeYes, "yes", false, l10n.T("Skip the confirmation prompt (required in non-interactive environments)", nil))

	return c
}

func init() {
	register(func(root *cobra.Command) { root.AddCommand(newUpgradeCmd()) })
}
