// cmd 包是 ggt 的 Cobra 命令入口层。每个文件对应一个 ggt 子命令。
// 这里定义根命令、全局配置加载、仓库列表管理、以及各命令共享的辅助函数。
package cmd

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/eggokit/logger"
	"github.com/jy-eggroll/eggokit/updater"
	"github.com/jy-eggroll/ggt/internal/config"
	"github.com/jy-eggroll/ggt/internal/locales"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

var (
	// concurrency 通过 --concurrency / -c 命令行参数传入，
	// 在 PersistentPreRunE 中覆盖配置文件的默认值。
	concurrency int
	// verboseCount 由 -v/--verbose 累加得到：-v 为 info，-vv 及以上为 debug。
	// 它与配置文件里的 log_level 共同决定诊断日志详略，命令行优先级更高。
	verboseCount int
	// cfg 是全局配置实例，在 PersistentPreRunE 中初始化。
	cfg *config.Config
)

// newRootCmd 构造根命令并注册全局持久化 flag。
//
// 命令树在语言加载之后才构造（见 registry.go），因此这里出现的 T() 都是真实翻译调用。
// PersistentPreRunE 在每个子命令执行前自动运行，用于加载配置。
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "ggt",
		Short: l10n.T("ggt - Git repository manager", nil),
		Long: l10n.T(`A CLI tool for managing multiple git repositories, with concurrent operations.

Help:
  ggt --help  Show detailed help

Config file: ~/.config/go-git-ggt/ggt-config.json`, nil),
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			started := time.Now()
			var err error
			cfg, err = config.LoadConfig()
			if err != nil {
				// 错误包装保持 Go 侧拼接：go-i18n 模板没有 %w 等价物，写进模板会丢掉错误链
				return fmt.Errorf("%s: %w", l10n.T("Failed to load the configuration", nil), err)
			}

			// 命令行的 -c 参数优先级高于配置文件；-c 传的是具体数字，
			// 覆盖为数字串（如 "8"），语义串常量（CPUHalf 等）仅在配置文件未显式设置时生效。
			if concurrency > 0 {
				cfg.Concurrency = strconv.Itoa(concurrency)
			}

			// 日志必须在读完配置之后、任何业务代码之前建立：级别要由配置与 -v/-vv 共同决定，
			// 而业务命令在 RunE 里就会打诊断日志（见 DebugTimer）
			initLogger(cmd, started)

			return nil
		},
	}

	// -c 的默认值为 0，表示“未显式指定”；在 PersistentPreRunE 中仅当 >0 时才
	// 覆盖配置文件里的并发数。真正生效的默认值（CPU 核心数的一半）由
	// config 包的 resolveConcurrency 统一计算，避免出现两处默认值逻辑不一致。
	root.PersistentFlags().IntVarP(&concurrency, "concurrency", "c", 0,
		l10n.T("Concurrency (defaults to the concurrency config value; when unset uses the CPUHalf semantic value, i.e. half of the CPU cores; CPUFull/CPUQuarter or an explicit number are also accepted)", nil))
	// -v/--verbose 是可累加的持久化 flag：-v 提到 info（含各阶段耗时），-vv 及以上到 debug。
	// 与 flk 保持一致（两个工具的日志入口是同一套），默认级别由配置项 log_level 决定
	root.PersistentFlags().CountVarP(&verboseCount, "verbose", "v",
		l10n.T("Increase diagnostic verbosity: -v for info, -vv for debug (overrides the log_level setting)", nil))
	// --lang 持久化 flag：声明它的唯一目的是让 cobra 认可这个参数，否则命令行里
	// 出现 --lang 会被判为 unknown flag。真正生效的取值由 resolveLanguage 预扫描
	// os.Args 得到（语言必须早于 cobra 解析才能确定），所以这里刻意不绑定变量，
	// 避免出现两个互相矛盾的取值来源。
	root.PersistentFlags().StringP("lang", "l", "", l10n.T("Output language (e.g. en, zh-CN); defaults to the language config value", nil))

	return root
}

// Execute 是程序的入口，由 main.go 调用。
//
// 三步顺序是有意为之，不能调换：
//  1. 先确定并加载语言。cobra 的 --help 路径不会执行 PersistentPreRunE，而命令描述
//     在构造时就要用到，所以语言解析必须走在最前面
//  2. 再构造命令树。各命令的构造函数会调用 T()，此时语言已就绪
//  3. 最后才交给 cobra 解析参数并执行
func Execute() {
	// 清理上一次升级留下的文件，放在最前面：它与语言、命令树都无关，也不该被后面的提前返回跳过。
	// 为什么只能等到“下一次运行”才清：升级成功的那一刻，旧版本正是当前进程自己的映像，
	// 正在运行的进程删不掉自己的映像，只能由下一次运行（旧进程已退出）来收拾。
	//
	// 失败一律忽略，也不打印任何东西：清理受阻（文件仍被占用、安装目录只读）不该影响这次正常使用，
	// 用户也不需要看到一条与自己无关的启动提示；清不掉的文件会在下次运行再试
	if execPath, err := os.Executable(); err == nil {
		updater.CleanupLeftovers(execPath)
	}

	// NO_COLOR 是跨工具约定（https://no-color.org）：该环境变量存在即关闭着色。
	// pterm 自己不检测 TTY，不处理的话管道里会混进 ANSI 转义，
	// 让 `ggt config show | jq` 这类用法直接失败
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		pterm.DisableColor()
	}

	// 语言文件是 //go:embed 进 locales 包的，加载失败属于构建期错误，必须显式暴露。
	// 静默降级只会表现为“界面语言不对”，没有任何报错，极难排查
	if err := l10n.Init(chooseLanguage(), locales.Options()); err != nil {
		ErrorMsg(err.Error())
		os.Exit(1)
	}

	root := buildRoot()
	// 错误一律走 stderr：脚本靠 stdout 取数据、靠退出码判断成败，把错误混进 stdout
	// 会污染管道（pterm 默认写 stdout，这里显式改掉）
	pterm.Error.Writer = os.Stderr
	// 关掉 cobra 自己的错误输出。否则同一条错误会被 cobra（"Error: ..." 到 stderr）
	// 和下面的分支（"ERROR 执行失败: ..."）各打印一次
	root.SilenceErrors = true

	if err := root.Execute(); err != nil {
		// errSilent 表示命令自己已经把错误打印过了（如 config show 在报错后还给了修复提示），
		// 再包一层 "Execution failed:" 只会重复
		if !errors.Is(err, errSilent) {
			ErrorMsg(l10n.T("Execution failed: {{.Err}}", map[string]any{"Err": err}))
		}
		os.Exit(1)
	}
}

// 关于文案：本包不提供 T 的薄封装，各命令一律直接调用 l10n.T(“英文原文”, data)。
//
// 这不是风格偏好，而是提取工具的硬性要求：github.com/jy-eggroll/eggokit/l10n/scan 规定"消息调用的首个参数必须是
// 字符串字面量才能确定消息 id"。任何一层透传封装都必然以变量为参转发
// （func T(msg string, ...) { return l10n.T(msg, ...) }），会被提取器判为违规。
// 去掉封装后规则全仓一致、无需任何例外，代价只是调用点多写一个包名前缀。
//
// 另注意 l10n.T 的返回值是已渲染好的纯文本，**不要**再当作 printf 的格式串送进
// Sprintf 通道（pterm 的 Printf/Printfln，或 fmt.Sprintf），否则译文里出现的
// 字面 %（如“完成度 100%”）会被 fmt 解析成 %!?(MISSING)。
// 需要输出时请使用 Msg 系列（InfoMsg 等）或 Str 系列（InfoStr 等）。

// GetConfig 返回全局配置实例。
func GetConfig() *config.Config {
	return cfg
}

// Concurrency 返回当前生效的并发数（已把配置里的语义串解析为具体整数）。
// 各命令启动 worker 时统一调用它，避免每处都写 GetConfig().ConcurrencyValue()。
func Concurrency() int {
	return GetConfig().ConcurrencyValue()
}

// initLogger 建立分级日志，级别来源链：命令行 -v/-vv > 配置项 log_level > 内置默认（warn）。
//
// 配置读不出来或取值非法时**不中止命令**，而是退回默认级别并提示：
// 设置文件的问题只该影响“多打或少打日志”，不该让整个 CLI 不可用；
// 想弄清文件到底哪里不对，入口是 ggt config validate
func initLogger(cmd *cobra.Command, configLoadedAt time.Time) {
	errWriter := cmd.ErrOrStderr()

	logConfig, levelErr := logger.FromLevelText(cfg.LogLevel)
	if levelErr != nil {
		logConfig = logger.DefaultConfig()
	}
	if verboseCount > 0 {
		logConfig = logger.ApplyVerbose(logConfig, verboseCount)
	}
	// 日志一律跟随 Cobra 注入的 stderr：脚本靠 stdout 取数据、靠退出码判成败，
	// 把诊断信息混进 stdout 会污染管道（与 pterm.Error.Writer 的取向一致）
	logConfig.Writer = errWriter
	logger.Init(logConfig)

	// 级别非法这件事必须说出来：静默降级会让用户以为设置生效了，而“日志怎么变少了”极难自查。
	//
	// 走 pterm 面向用户输出而不是 logger：它与同屏的信息行同属“命令的正常输出”，
	// 用 logger 会渲染成 level=WARN msg=... 的 logfmt 行，一屏上并存两种风格
	// （约定见 output.go：用户可见输出走 pterm，诊断与审计走 logger）
	if levelErr != nil {
		WarnMsg(l10n.T("Ignoring an invalid log_level in the config file: {{.Err}}", map[string]any{"Err": levelErr.Error()}))
	}

	logger.Debug(l10n.T("Loading configuration", nil), "duration", time.Since(configLoadedAt))
}

// DebugTimer 记录阶段耗时并交给分级日志输出（仅 debug 级别可见）。
// 用法：t := NewDebugTimer(“阶段名”) ... defer t.Done() 或 t.Done()
type DebugTimer struct {
	label string
	start time.Time
}

// NewDebugTimer 创建计时器并记录起始时间。
func NewDebugTimer(label string) *DebugTimer {
	return &DebugTimer{label: label, start: time.Now()}
}

// Done 以 debug 级日志输出本阶段耗时。
//
// 调用点无需判级：logger 自己按当前级别过滤，未启用 debug 时这里几乎零开销。
// 这也是它取代原先"直调 pterm 打印灰色计时行"的原因——那版把展示策略硬编码在了
// 业务命令里，还要靠一个全局布尔决定要不要打
func (t *DebugTimer) Done() {
	logger.Debug(t.label, "duration", time.Since(t.start))
}
