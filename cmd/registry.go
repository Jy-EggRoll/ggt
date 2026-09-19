// registry.go 负责命令树的两段式装配：先登记构造函数，待语言就绪后再真正构造。
//
// 为什么不在包级变量里直接写命令、再在 init() 里 AddCommand：命令的描述文案
// （Short/Long/flag usage）需要经 T() 翻译，而 T() 依赖语言已加载，语言又只能等到
// cmd.Execute 才能确定（cobra 的 --help 不会执行 PersistentPreRunE，那些文案在
// 进入 Execute 时就必须是最终语言）。包级变量在 init 阶段就求值了，那时语言还没影，
// 只能再遍历命令树覆盖字段——那套机制会就地改写字段、破坏幂等，还要求提取器对
// 框架自身的代码开例外（参见本文件末尾的说明）。
//
// 改为"延迟构造"后，T() 在构造期就是一次真调用，全仓语义一致：
//   - 各命令文件只提供构造函数 newXxxCmd()，并在自己的 init() 里用 register
//     登记"把该命令挂到根命令上"的动作
//   - Execute 先 i18n.Init 加载语言，再 buildRoot() 触发全部构造函数
//   - flag 的取地址绑定发生在构造函数内部，仍早于 cobra 解析参数，不会失效
package cmd

import "github.com/spf13/cobra"

// constructors 收集各命令的装配函数，由各命令文件的 init() 通过 register 追加。
// Go 保证 init() 按文件名字典序执行，因此装配顺序固定且可复现；
// 命令在帮助里的展示顺序由 cobra 的 EnableCommandSorting 决定，与此无关。
var constructors []func(root *cobra.Command)

// register 由各命令文件的 init() 调用，登记"把本命令挂到根命令上"的动作。
// 登记的是动作而非命令实例，正是为了把构造推迟到语言加载之后。
func register(add func(root *cobra.Command)) {
	constructors = append(constructors, add)
}

// buildRoot 构造完整的命令树。
// 必须在 i18n.Init 之后调用：各构造函数会调用 T() 取当前语言的文案。
func buildRoot() *cobra.Command {
	root := newRootCmd()
	for _, add := range constructors {
		add(root)
	}
	return root
}
