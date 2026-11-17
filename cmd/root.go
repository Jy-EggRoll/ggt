// cmd 包是 ggt 的 Cobra 命令入口层。每个文件对应一个 ggt 子命令。
// 这里定义根命令、全局配置加载、仓库列表管理、以及各命令共享的辅助函数。
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/eggokit/logger"
	"github.com/jy-eggroll/eggokit/updater"
	"github.com/jy-eggroll/ggt/internal/config"
	"github.com/jy-eggroll/ggt/internal/locales"
	"github.com/jy-eggroll/ggt/internal/worker"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

var (
	// concurrency 通过 --concurrency / -c 命令行参数传入，
	// 在 PersistentPreRunE 中覆盖配置文件的默认值。
	// 类型是 string 而不是 int：配置文件与设置面板认 auto 这个词，命令行若只收数字，
	// 同一套词汇在两处就对不上；取值校验统一交给 config.resolveConcurrency。
	concurrency string
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
		// 任何错误都只打印错误本身，不附整段用法。
		//
		// cobra 的默认行为是在 execute 返回错误后先打印该命令的 UsageString，再让调用方
		// 打印错误——启动期错误（配置读不出来、-c 越界）与参数类错误（缺参数、未知 flag）
		// 因此都会先甩一整页用法，把真正那行 ERROR 挤出视野，脚本里也难定位。
		//
		// 用法只在用户主动要求时才打印：--help / -h 与不带子命令的 ggt 都走 cobra 的
		// HelpFunc，不经过这里；SilenceUsage 挂根命令上，ExecuteC 会一并套用到各子命令，
		// 因此不需要每个命令各设一次。
		//
		// 未知子命令的报错来自 cobra 查找阶段的另一条路径，同样保持不打印整段用法：
		// 报错里已经点名了写错的那个词，cobra 还会在近似拼写时附上“Did you mean this?”。
		// 为一个拼写错误铺开整张命令表，与 --help 的职责重复，也不比那行提示更有用；
		// 想看全表敲 ggt --help。
		SilenceUsage: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return startCommand(cmd, config.LoadConfig)
		},
	}

	// -c 的默认值空串只是占位，是否生效由 cobra 的 Changed 判定（见 PersistentPreRunE）。
	// 取值集与配置文件完全一致：auto，或 1 到 64 的整数；解析与报错共用
	// config.resolveConcurrency，命令行因此不会漏出 strconv 的原文。
	root.PersistentFlags().StringVarP(&concurrency, "concurrency", "c", "",
		l10n.T("Concurrency: auto or an integer from 1 to 64 (overrides the concurrency setting; auto means half of the CPU cores capped at 8)", nil))
	// -v/--verbose 是可累加的持久化 flag：-v 提到 info（含各阶段耗时），-vv 及以上到 debug。
	// 与 flk 保持一致（两个工具的日志入口是同一套），默认级别由配置项 log_level 决定
	root.PersistentFlags().CountVarP(&verboseCount, "verbose", "v",
		l10n.T("Increase diagnostic verbosity: -v for info, -vv for debug (overrides the log_level setting)", nil))
	// --lang 持久化 flag：声明它的唯一目的是让 cobra 认可这个参数，否则命令行里
	// 出现 --lang 会被判为 unknown flag。真正生效的取值由 resolveLanguage 预扫描
	// os.Args 得到（语言必须早于 cobra 解析才能确定），所以这里刻意不绑定变量，
	// 避免出现两个互相矛盾的取值来源。
	root.PersistentFlags().StringP("lang", "l", "", l10n.T("Output language (e.g. en, zh-CN); defaults to the language config value", nil))
	// --color 与 --lang 同理：注册它只是让 cobra 认可这个参数，并把非法取值报给用户；
	// 真正生效的取值由 resolveColorMode 预扫描 os.Args 得到（着色必须早于 cobra 解析）。
	// 因此这里刻意绑定一个新的 colorMode 实例，不引入第二个取值来源——预扫描已经
	// 把结果落实到 pterm 上，这个实例解析完就没有用处
	var parsedColor colorMode
	root.PersistentFlags().Var(&parsedColor, "color",
		l10n.T("Color output: auto colors only when stdout and stderr are terminals, always forces color, never disables it", nil))

	return root
}

// configLoader 是“从哪里、以什么严格程度取配置”。
//
// 把它做成参数而不是写死在 startCommand 里，是为了让“配置坏了也要能跑”的那两个子树
// 复用同一套启动步骤（并发数、worker、日志），而不必各自复制一份（见 loadRecoverableConfig）。
type configLoader func() (*config.Config, error)

// startCommand 是所有子命令执行前的统一启动步骤：取配置 → 解析并发数 → 建立日志。
//
// 并发数在这里一次性解析并装进 worker 的共享信号量。放在启动流程而不是各命令里，
// 是因为它同时是“上限校验”的唯一入口：-c 与配置文件两条来源都在此汇合，
// 越界时让命令以非 0 退出码失败，而不是截断成上限继续跑。
//
// 命令行与配置文件必须分开解析：-c 明确给出空串（脚本里 -c "$VAR" 而 VAR 未设值）
// 要报错，而配置文件里的空串仍按 auto 处理，见各自函数的注释。
func startCommand(cmd *cobra.Command, load configLoader) error {
	started := time.Now()

	loaded, err := load()
	if err != nil {
		// 错误包装保持 Go 侧拼接：go-i18n 模板没有 %w 等价物，写进模板会丢掉错误链。
		// 修复提示附在同一个错误里：配置加载失败时，用户唯一能敲的就是那几条命令。
		// 是否交互决定提示里那条 reset --all 带不带 --yes（非交互时缺 --yes 会被拒绝）
		return config.WithRecoveryHint(
			fmt.Errorf("%s: %w", l10n.T("Failed to load the configuration", nil), err), !isTerminal(os.Stdin))
	}
	cfg = loaded

	var n int
	if cmd.Flags().Changed("concurrency") {
		n, err = config.ValidateCommandLineConcurrency(concurrency)
		if err != nil {
			return err
		}
		cfg.Concurrency = concurrency
	} else {
		n, err = cfg.ConcurrencyValue()
		if err != nil {
			return err
		}
	}
	if err := worker.Init(n); err != nil {
		return err
	}

	// 日志必须在读完配置之后、任何业务代码之前建立：级别要由配置与 -v/-vv 共同决定，
	// 而业务命令在 RunE 里就会打诊断日志（见 DebugTimer）
	initLogger(cmd, started)

	return nil
}

// loadRecoverableConfig 先按严格规则加载配置，失败时退回宽松加载：
// 坏掉的键按默认值处理并逐条打印“怎么修”，命令本身继续跑。
//
// 只有 ggt config 与 ggt repo 两个子树使用它，理由是这样一条死锁链（已实测）：
// repo_paths 写成 [""] 之后，ggt repo list（它要先读配置）与
// ggt config reset repo_paths（受管项被拒绝）一起失败，用户只剩手改文件或
// config reset --all（丢掉全部仓库记录）。这两个子树恰好是命令行修复入口，
// 因此必须能在“配置已经坏了”的状态下运行。
//
// 看板（ggt ui）另走 loadUIStartupConfig：它同样需要宽松加载，但放宽的范围只有“启动”
// 这一步，理由与边界见那个函数。
//
// 为什么只放开这三处：其它命令的职责都建立在配置之上（按仓库列表跑 git、
// 按 size_unit 分桶、按 language 输出），配置坏了它们本来也无事可做，让它们带着
// 一半默认值继续跑只会给出与用户配置无关的结果。这三处的职责则只是配置本身，
// 因此各自的权限也被收在这里：
//
//   - ggt config：只读写配置文件（show / get / set / reset / validate / path）。
//     允许在配置非法时打印当前配置与体检结果；不允许把宽松加载出来的值当成“生效配置”
//     写回文件，也不按配置执行任何 git 操作。
//   - ggt repo：只读写仓库路径列表（list / add / remove / add-parent）。
//     允许在配置非法时列出仍然可用的条目，并把修好的列表写回文件；不允许遍历仓库采集
//     状态、跑并发任务，也不依据其它配置项做判断。
//   - ggt ui：允许启动、允许设置面板报告并修复配置；页面上的 git 操作按“当前配置能否
//     严格加载”现判，配置非法时一律拒绝（见 uiCache.configNow）。
//
// 共同的边界：坏掉的键一律按默认值处理（并逐条提示），所以这三处的输出不能
// 当作“运行期会怎么表现”的证据；写文件只发生在用户显式执行 set / reset / repo 增删时。
func loadRecoverableConfig() (*config.Config, error) {
	loaded, problems, err := recoverConfig(config.HintForCLI)
	if err != nil {
		return nil, err
	}

	if len(problems) > 0 {
		WarnMsg(l10n.T(`The configuration has problems, so ggt runs with built-in defaults for the affected entries; only "ggt config" and "ggt repo" work in this state`, nil))
		for _, line := range configProblemLines(problems) {
			WarnMsg(line)
		}
	}
	return loaded, nil
}

// recoverConfig 先按严格规则加载配置，失败后退回宽松加载，把逐键问题交回调用方。
//
// 问题清单不在这里打印：写到哪里、讲什么由命令决定。config show 的提示必须进 stderr
// （stdout 只留 JSON），而 ggt config / ggt repo 两个子树在配置非法时仍能读写配置文件、
// 看板则仍能起来并报告问题——三种处境要说的话并不相同
//
// 连 JSON 语法、键名可读性都不成立时宽松加载也没有意义：那时没有“哪一个键坏了”可言，
// 只能手改文件或整份重置，因此这里返回带修复提示的错误
func recoverConfig(scope config.HintScope) (*config.Config, []config.Problem, error) {
	loaded, err := config.LoadConfig()
	if err == nil {
		return loaded, nil, nil
	}

	lenient, problems, lenientErr := config.LoadConfigLenient()
	if lenientErr != nil {
		// 连 JSON 语法、键名可读性都不成立时宽松加载也没有意义：那时没有“哪一个键坏了”
		// 可言，只能手改文件或整份重置。提示在这里补上，调用方（含 config show）拿到的
		// error 就是可直接照做的；WithRecoveryHint 幂等，startCommand 再调用一次也只会有一份。
		// 非交互环境决定整份重置的命令带不带 --yes：那种环境下确认提示读不到，命令会直接报错
		return nil, nil, config.WithRecoveryHintFor(err, !isTerminal(os.Stdin), scope)
	}
	return lenient, problems, nil
}

// configProblemLines 把逐键问题渲染成一行行可直接照做的说明。
//
// 每条都点名坏在哪、给出清掉它的命令：用户知道“配置坏了”却不知道下一步能敲什么，
// 这次失败就仍然只能靠手改文件解决（见 config.WithRecoveryHint 的说明）
func configProblemLines(problems []config.Problem) []string {
	lines := make([]string, 0, len(problems))
	for _, p := range problems {
		// 修复命令单独占一行、不加引号：提示要能被整段复制去执行（见 config.WithRecoveryHint）
		lines = append(lines, l10n.T("{{.Problem}}\n  Clear the broken value with:\n    {{.Command}}",
			map[string]any{"Problem": p.Error(), "Command": config.ResetCommand(p.Key)}))
	}
	return lines
}

// printConfigProblems 把逐键问题写到指定输出流，供面向脚本的命令使用（当前是 config show）。
//
// 用 WarnStr 而不是 WarnMsg：后者的目标是标准输出，写进管道就会让 JSON 解析失败
func printConfigProblems(w io.Writer, problems []config.Problem) {
	for _, line := range configProblemLines(problems) {
		fmt.Fprintln(w, WarnStr(line))
	}
}

// loadUIStartupConfig 让看板在配置坏掉时也能起来。
//
// 看板是唯一能在图形界面里报告并修复配置的入口，而它此前走的是严格加载：配置文件里有一项
// 非法，ggt ui 就在启动阶段退出——面板打不开，也就修不了。这里因此允许宽松加载，
// 但只放宽“启动”这一步：
//   - 坏掉的项按默认值处理，并逐条打印（终端里看得见）
//   - 设置面板按文件现读，逐项标出原值、坏在哪（见 /api/settings）
//   - 页面上的 git 写操作在配置非法时一律拒绝（见 uiCache.configNow）：它们的参数来自配置
//     （push_tags / prune_branches），替用户按默认值执行等于让坏配置静默生效
//   - 连 JSON 都读不出来时以默认配置起服务：此时没有仓库可看，但设置面板能把“文件读不出来”
//     显示出来，用户至少知道该去改哪个文件
func loadUIStartupConfig() (*config.Config, error) {
	loaded, problems, err := recoverConfig(config.HintForDashboard)
	if err != nil {
		WarnMsg(l10n.T("The config file cannot be read, so the dashboard starts with built-in defaults and git actions from the page stay disabled until it is fixed", nil))
		WarnMsg(err.Error())
		return config.DefaultConfig(), nil
	}
	if len(problems) > 0 {
		WarnMsg(l10n.T("The configuration has problems, so the dashboard runs with built-in defaults for the affected entries and git actions from the page stay disabled until the file is valid", nil))
		for _, line := range configProblemLines(problems) {
			WarnMsg(line)
		}
	}
	return loaded, nil
}

// exitCodeInterrupted 是“被 SIGINT 打断”的退出码。
//
// 依据是 shell 的通行约定：进程被信号 N 终止时退出码记为 128+N，SIGINT 的编号是 2，
// 128+2 即 130（bash 对前台收到 Ctrl-C 的命令同样给出 130）。用它而不是笼统的 1，
// 是为了让脚本能区分“被打断、结果不完整”与“命令真的失败了”。
const exitCodeInterrupted = 130

// Execute 是程序的入口，由 main.go 调用。
//
// 三步顺序是有意为之，不能调换：
//  1. 先确定并加载语言。cobra 的 --help 路径不会执行 PersistentPreRunE，而命令描述
//     在构造时就要用到，所以语言解析必须走在最前面
//  2. 再构造命令树。各命令的构造函数会调用 T()，此时语言已就绪
//  3. 最后才交给 cobra 解析参数并执行
//
// 退出码分三档：正常返回 0；命令报错 1；被 Ctrl-C/SIGTERM 打断 130。
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

	// 着色开关必须落在最前面：它影响此后所有经 pterm 的输出，而启动期就有输出
	// （语言回退提示、配置读不出来时的报错）。判定口径（终端判定、NO_COLOR 与
	// --color 的优先级）统一收在 color.go，这里只负责落实
	applyColorMode(resolveColorMode(os.Args[1:]))

	// 语言文件是 //go:embed 进 locales 包的，加载失败属于构建期错误，必须显式暴露。
	// 静默降级只会表现为“界面语言不对”，没有任何报错，极难排查
	lang, langNotice := chooseLanguage(os.Args[1:])
	if err := l10n.Init(lang, locales.Options()); err != nil {
		ErrorMsg(err.Error())
		os.Exit(1)
	}
	// 提示必须等到 Init 之后：那时才知道本次真正采用的语言，文案也才能跟着它渲染。
	// 写在这里而不是 chooseLanguage 里，是因为后者可能在语言就绪之前就被调用
	printLanguageNotice(langNotice)

	// 信号根 ctx：Ctrl-C（os.Interrupt）与 SIGTERM 都取消它，各命令把 cmd.Context()
	// 一路传给 worker.Map 与 git.RunContext，正在跑的 git 子进程因此会被 context 杀掉，
	// 而不是只剩本进程被硬杀、留下满屏 git 子进程继续跑。
	//
	// 为什么把 SIGTERM 也登记进来：Unix 上 kill 与服务管理器走的是 SIGTERM，
	// 与 Ctrl-C 归到同一条“收尾后退出”的路径，脚本行为才一致。
	// Windows 上只有 os.Interrupt 会真正投递（Go 的 syscall 包虽定义了 SIGTERM 常量，
	// 但 Windows 没有 POSIX 信号，os.Process.Signal 也只支持 Kill 与 Interrupt），
	// 登记它等于空操作，不影响 Windows 上的 Ctrl-C。
	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	root := buildRoot()
	// 取消信号必须挂在 root 上：子命令的 cmd.Context() 继承它，这是七个遍历型命令
	// 能感知 Ctrl-C 的唯一来源
	root.SetContext(signalCtx)
	// 错误一律走 stderr：脚本靠 stdout 取数据、靠退出码判断成败，把错误混进 stdout
	// 会污染管道（pterm 默认写 stdout，这里显式改掉）
	pterm.Error.Writer = os.Stderr
	// 关掉 cobra 自己的错误输出。否则同一条错误会被 cobra（"Error: ..." 到 stderr）
	// 和下面的分支（“ERROR 执行失败: ...”）各打印一次
	root.SilenceErrors = true

	// 第二次信号恢复默认处置：首个信号取消 ctx 后立刻注销处理器，用户再按一次 Ctrl-C
	// 就按系统默认行为立即终止进程。需要一个兜底的原因是并非每一步都理会 ctx——
	// 例如 Windows 上的 takeown 是独立进程，取消 ctx 并不会让它停下
	go func() {
		<-signalCtx.Done()
		stopSignals()
	}()

	err := root.Execute()

	// 被打断时以 130 退出：各命令已经把已尝试仓库的结果顺序打印完，这里只负责退出码。
	// 判在错误分支之前，是因为“被打断”比“某个命令报错”更能解释这次运行的结局，
	// 退出码也该优先反映它
	if signalCtx.Err() != nil {
		os.Exit(exitCodeInterrupted)
	}

	if err != nil {
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
// 这不是风格偏好，而是提取工具的硬性要求：github.com/jy-eggroll/eggokit/l10n/scan 规定“消息调用的首个参数必须是
// 字符串字面量才能确定消息 id”。任何一层透传封装都必然以变量为参转发
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

// Concurrency 返回当前生效的并发数。
// 唯一真相在 worker 的共享信号量里（启动时由 PersistentPreRunE 装入），这里只做转发：
// 若改成每次重新解析配置，看板重读配置之后就与正在生效的限流值对不上号了。
func Concurrency() int {
	return worker.Concurrency()
}

// initLogger 建立分级日志，级别来源链：命令行 -v/-vv > 配置项 log_level > 内置默认（warn）。
//
// 配置加载已经由 Setting.Normalize 保证 cfg.LogLevel 是合法级别；这里仍做一次
// 防御性解析，以防将来有别的路径直接构造 Config。级别非法时退回默认并提示，
// 而不是让日志初始化本身失败——但那属于兜底，配置文件里的非法级别会先被加载期拒绝。
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
// 这也是它取代原先“直调 pterm 打印灰色计时行”的原因——那版把展示策略硬编码在了
// 业务命令里，还要靠一个全局布尔决定要不要打
func (t *DebugTimer) Done() {
	logger.Debug(t.label, "duration", time.Since(t.start))
}

// gather 是遍历型命令共用的采集骨架：起计时、按原序并发采集、取消后剔除没跑过的条目、
// 收尾计时。抬头、逐项打印、末尾汇总、错误语义一律留在调用点——它们各命令差别很大，
// 收进来就等于把七份互不相同的展示逻辑塞进一个函数，改一处会波及全部命令。
//
// 拦截与过滤这两件事只能待在这里，因为调用点拿不到它们依赖的信息，二者都源于
// worker.Map 的取消行为：
//   - Map 只在派生下一个任务之前看 ctx，已经派生、正排在信号量上等位的 goroutine
//     在取消后仍会执行 fn，因此“取消后不再启动新任务”只能在 fn 入口拦一道；
//   - Map 按原始下标写回结果、未启动的位置留零值，而“谁先启动”取决于各 goroutine
//     抢信号量的次序、并不保证是前 k 个，所以调用点拿到的那份切片既分不清哪条没跑过，
//     也无法靠截断长度来猜，只能在这里记下真正进入过 fn 的下标再过滤。
//
// label 必须由调用点用 l10n.T(...) 渲染后传入：l10n 提取器要求消息调用的首参是
// 字符串字面量，若在这里代调 l10n.T，传进来的是变量，译文会静默失效。
//
// 计时走 defer，即使 fn panic（worker.Map 会 recover 并让该位置留零值）也一定会收尾。
//
// 代价是每次调用固定多分配两个与仓库数同阶的切片（tasks 与 started），取消路径还要
// 再分配一次过滤结果；正常路径只是多这两次分配，返回值与未引入取消过滤时完全一致。
func gather[T any](ctx context.Context, label string, repos []RepoEntry, fn func(context.Context, RepoEntry) T) []T {
	t := NewDebugTimer(label)
	defer t.Done()

	// worker.Map 按原始下标写回结果，但被 ctx 取消时它只保证“不再启动新任务”：
	// 未启动的条目会留在零值上，而且“谁先启动”取决于各 goroutine 抢信号量的次序，
	// 并不保证是前 k 个。若把整份结果交给打印，中断时就会把从没跑过的仓库
	// 当成“干净/成功”打出来。因此这里记下真正进入过 fn 的下标，只在取消后按它过滤；
	// 未取消时原样返回，分配的那点开销换的是取消路径上的正确性。
	type gatherTask struct {
		idx  int
		repo RepoEntry
	}
	started := make([]bool, len(repos))
	tasks := make([]gatherTask, len(repos))
	for i, e := range repos {
		tasks[i] = gatherTask{idx: i, repo: e}
	}

	results := worker.Map(ctx, tasks, func(ctx context.Context, task gatherTask) T {
		var zero T
		// worker.Map 只在“派生下一个任务”之前看 ctx：已经派生、正排在信号量上等位的
		// goroutine 在取消之后仍会执行 fn。这里再拦一道，取消后不再启动任何新任务——
		// 否则 takeown 这类本就不理会 ctx 的步骤，会在 Ctrl-C 之后把整批任务继续跑完
		if ctx.Err() != nil {
			return zero
		}
		// 各 goroutine 只写自己的下标，互不重叠，无需加锁
		started[task.idx] = true
		return fn(ctx, task.repo)
	})

	if ctx.Err() == nil {
		return results
	}

	attempted := make([]T, 0, len(results))
	for i, r := range results {
		if started[i] {
			attempted = append(attempted, r)
		}
	}
	return attempted
}
