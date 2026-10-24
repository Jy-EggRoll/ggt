// upgrade.go 把 "ggt upgrade" 命令接到 eggokit/upgradecli 的共享实现上。
//
// 命令行为（读 flag、选通道、组织提示语、决定确认时机、退出方式）全部在共享实现里，
// 本文件只提供 ggt 专属取值：程序名、发布目标（见 upgrade_target.go）、版本与构建时间
// 来源（见 version.go），以及界面适配层（见 upgrade_reporter.go）。
//
// alpha 通道与另外两个通道不同：它指向一个固定标签的滚动预发布，主分支每次推送都会覆盖
// 同一份 Release，因此没有版本号可供比较，只能靠发布说明首行里的提交号判断新旧。这条约定
// 由共享实现承担，ggt 侧只需按原有 flag 语义把它暴露给用户。
package cmd

import (
	"github.com/jy-eggroll/eggokit/l10n"
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
		Version:   effectiveVersion,
		BuildTime: func() string { return BuildTime },
		// 界面实现在命令执行时才确定，因此以函数形式注入；ggt 的输出目标由 output.go
		// 统一管理，不随命令的输出流变化
		Reporter:  func(*cobra.Command) updater.Reporter { return &ptermReporter{} },
		AssumeYes: func() bool { return assumeYes },
	})

	c.Flags().BoolVar(&assumeYes, "yes", false, l10n.T("Skip the confirmation prompt (required in non-interactive environments)", nil))

	return c
}

func init() {
	register(func(root *cobra.Command) { root.AddCommand(newUpgradeCmd()) })
}
