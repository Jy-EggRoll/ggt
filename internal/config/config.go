// config 包管理 ggt 的 JSON 配置文件。
// 配置文件路径的唯一出处是 GetDefaultConfigPath，别处不要复述这个字面量，
// 否则改默认位置时必然漏改其中几处，而漏掉的那处不会报错、只会让人对着旧路径排查。
//
// 配置项：
//   - repo_paths: 直接添加的仓库路径列表
//   - parent_paths: 父目录列表（自动扫描其中的 git 仓库）
//   - concurrency: 并发数，只接受 auto（运行时按 CPU 逻辑核数算，最多 8）
//     或 1 到 64 的整数串（如 "8"），默认 auto，详见 resolveConcurrency
//   - ignore_submodules: 是否在所有功能中忽略子模块，默认 false（即默认包含子模块）
//   - size_bucket_low_mb: size 命令分桶的下界阈值（MB），默认 500
//   - size_bucket_high_mb: size 命令分桶的上界阈值（MB），默认 800
//   - size_unit: size 命令分桶时 MB 的换算口径，"decimal"(1 MB = 1,000,000 字节)
//     或 "binary"(1 MB = 1024*1024 字节，即 MiB)，默认 "decimal"
//   - language: 输出语言（如 "en"、"zh-CN"），默认 "en"；命令行 --lang 优先级更高
//   - log_level: 诊断日志级别（debug/info/warn/error），默认 "warn"；
//     命令行 -v/-vv 优先级更高。它只影响诊断日志，不影响面向用户的正常输出
//   - push_tags: 网页看板推送时是否附带 --follow-tags，默认 true
//   - prune_branches: fetch / pull / sync 时是否加 --prune 修剪远程跟踪分支，默认 true；
//     它只管分支，本地标签一律不自动修剪
//
// 取值不合法会让加载失败，所有命令因此在启动前退出。为保留修复路径，三处改用宽松加载
// （见 recover.go 与 cmd 层的 loadRecoverableConfig / loadUIStartupConfig）：
// ggt config 与 ggt repo 两个子树只读写配置本身、不按配置执行 git 操作，坏的键按默认值处理
// 并提示怎么修；ggt ui 放宽的只有“启动”这一步，好让设置面板能把坏掉的项显示出来，
// 它上面的 git 操作在配置恢复合法之前一律拒绝。
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/go-viper/mapstructure/v2"
	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/eggokit/logger"
	"github.com/pterm/pterm"
)

// DefaultConcurrency 是并发数的默认值：auto。
// 配置里存的是这个词而不是某个具体数字——一旦落盘成数字，同一份 dotfiles 换到
// 另一台机器上就会带上原来那台机器的核数假设，而那台机器有几个核与这台无关。
const DefaultConcurrency = "auto"

// autoConcurrencyCeiling 是 auto 换算结果的上限。
// 核数再多也不提高默认并发：超过这个数之后，瓶颈已经从 CPU 转到磁盘与内存带宽，
// 再多的子进程只会互相争抢 I/O，对本地响应没有帮助，反而让机器更热。
const autoConcurrencyCeiling = 8

// Config 是 ggt 配置文件的 Go 结构体映射。
// 字段标签同时兼容 viper (mapstructure) 和 JSON 序列化。
type Config struct {
	ParentPaths      []string `mapstructure:"parent_paths" json:"parent_paths"`
	RepoPaths        []string `mapstructure:"repo_paths" json:"repo_paths"`
	Concurrency      string   `mapstructure:"concurrency" json:"concurrency"`
	IgnoreSubmodules bool     `mapstructure:"ignore_submodules" json:"ignore_submodules"`
	SizeBucketLowMB  int      `mapstructure:"size_bucket_low_mb" json:"size_bucket_low_mb"`
	SizeBucketHighMB int      `mapstructure:"size_bucket_high_mb" json:"size_bucket_high_mb"`
	SizeUnit         string   `mapstructure:"size_unit" json:"size_unit"`
	Language         string   `mapstructure:"language" json:"language"`
	LogLevel         string   `mapstructure:"log_level" json:"log_level"`
	// Theme 是网页看板选中的主题。空串表示跟随系统深浅（那是本节唯一的“有意义的零值”，
	// 因此默认值就是空串，不需要额外补值）
	Theme string `mapstructure:"theme" json:"theme"`
	// ThemeDark / ThemeLight 是“跟随系统”时深色与浅色各自用哪套主题，
	// 对应 VSCode 的 workbench.preferredDarkColorTheme / preferredLightColorTheme
	ThemeDark  string `mapstructure:"theme_dark" json:"theme_dark"`
	ThemeLight string `mapstructure:"theme_light" json:"theme_light"`
	// FontUI / FontMono 是页面两片区域各自的字体栈，空串表示不指定字体：
	// 页面那层只声明“特性”（界面区 sans-serif、等宽区 monospace），具体落到哪个字体由浏览器回退决定
	// （与 Theme 同理，空串是有意义的零值，默认值就是空串）
	FontUI   string `mapstructure:"font_ui" json:"font_ui"`
	FontMono string `mapstructure:"font_mono" json:"font_mono"`
	// NotifyTimeout 是网页通知自动消失的秒数。0 表示不自动消失，也就是零值即默认
	NotifyTimeout int `mapstructure:"notify_timeout" json:"notify_timeout"`
	// PushTags 决定网页看板推送时是否附带 --follow-tags。它默认 true，而 bool 的零值是
	// false，因此这个默认值只能来自 settings 注册表经 DefaultRaw 的解码（见 defaultConfig）
	PushTags bool `mapstructure:"push_tags" json:"push_tags"`
	// PruneBranches 决定 fetch / pull / sync 时是否加 --prune。理由同上：默认 true，
	// 不能靠零值表达。它只修剪远程跟踪分支，不要把它扩展成“连本地标签也修剪”
	PruneBranches bool `mapstructure:"prune_branches" json:"prune_branches"`
}

// getConfigPath 计算配置文件的默认路径，失败时返回 error 而不终止进程。
// 供 LoadLanguage 这类“必须永远可用”的路径使用——它们可能在 --help 之前被调用，
// 此时直接退出进程会导致连帮助都打印不出来。
func getConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "go-git-ggt", "ggt-config.json"), nil
}

// GetDefaultConfigPath 返回配置文件的默认路径。
// 主目录不可用时打印错误并退出进程——这是命令运行期的合理处理，
// 但不要把它用在 --help 之前会被触发的路径上（那种场合用 getConfigPath）。
func GetDefaultConfigPath() string {
	path, err := getConfigPath()
	if err != nil {
		// 这里的告警可能出现在 l10n.Init 之前（例如语言解析阶段），
		// 那时 l10n.T 会安全地回退为英文原文，不会 panic
		pterm.Error.Println(l10n.T("Failed to get the user home directory: {{.Err}}", map[string]any{"Err": err}))
		os.Exit(1)
	}
	return path
}

// ThemeDirs 返回用户主题可能存放的目录：配置目录本身，以及它的 themes 子目录。
// 两个都看——“把主题文件丢进配置目录”是最自然的用法，主题多了之后又需要一个地方归置
//
// 为什么放在本包而不是看板那一层：可用主题同时决定配置项 theme 的候选取值（见 settings.go），
// 两处各算一遍目录，迟早出现“命令行认得的主题、页面上选不到”这种错位
func ThemeDirs() []string {
	dir := filepath.Dir(GetDefaultConfigPath())
	return []string{dir, filepath.Join(dir, "themes")}
}

// InvalidLanguageError 表示配置文件里的 language 项写了一个不被接受的取值。
//
// 语言是启动最早的一步，这条路径上的失败不能像其它配置项那样让命令退出——连 --help
// 都会打不出来，只能回退默认语言。但“文件里写了什么”必须原样交回调用方：等语言就绪
// 之后要提示一次，否则用户看到的只是“界面语言不对”，得不到任何线索。
//
// 单独成类型而不是用普通 error 区分，是因为调用方需要同时拿到两件事：这是“语言取值
// 不被接受”（要提示），还是“文件读不出来”（语言从未配置或无从读取，静默回退即可）。
type InvalidLanguageError struct {
	// Raw 是文件里写下的原值（JSON 已渲染成文本），用于原样回显给用户
	Raw string
	// Err 是注册表给出的原因，保留错误链
	Err error
}

func (e *InvalidLanguageError) Error() string { return e.Err.Error() }
func (e *InvalidLanguageError) Unwrap() error { return e.Err }

// LoadLanguage 读取配置文件里的 language 项并按注册表的规则归一化，供启动最早的一步使用。
//
// 单独提供本函数而不是复用 LoadConfig，原因有二：
//   - 语言必须在 rootCmd.Execute() 之前确定（cobra 的 --help 不会执行 PersistentPreRunE），
//     而 LoadConfig 依赖的 GetDefaultConfigPath 在主目录不可用时会直接退出进程
//   - 只取一个键，避免为纯展示路径做一次全量解码与默认值补全：其它键坏掉时语言仍要能读出来
//
// 判定规则只有一份：走注册表里 language 项的 Normalize（与 config get / set / validate
// 完全相同），因此 zh、zh-Hans 归一成 zh-CN，不认识的取值在这里就被拦下。此前这条路径
// 直接读原始 JSON、由 l10n 层宽松回退，同一个取值在 config validate 里是 error、
// 在“实际界面语言”上却悄悄变成英文。
//
// 返回值：文件不存在、文件里没有这个键时返回空串与 nil（由调用方用默认语言）；
// 文件读不出来或 JSON 非法时返回 error；键存在但取值不被接受时返回
// *InvalidLanguageError。三种情况都由调用方回退默认语言——语言只影响展示，
// 不该让命令整体失败。
func LoadLanguage() (string, error) {
	path, err := getConfigPath()
	if err != nil {
		return "", err
	}
	raw, err := ReadRawAt(path)
	if err != nil {
		return "", err
	}
	value, ok := raw["language"]
	if !ok {
		return "", nil
	}
	lang, err := NormalizeLanguage(value)
	if err != nil {
		return "", &InvalidLanguageError{Raw: ValueText(value), Err: err}
	}
	return lang, nil
}

// NormalizeLanguage 把配置文件里的原始取值（JSON 解码后的形态）交给 language 项的
// Normalize，返回规范语言标签。
//
// 单独成函数是为了让“从 JSON 值来”与“从文本（--lang）来”两条入口共用同一份判定：
// 两条各写一遍，就会重演“config validate 拒收、启动路径却悄悄放行”的分叉。
func NormalizeLanguage(value any) (string, error) {
	s, ok := Lookup("language")
	if !ok || s.Normalize == nil {
		// 注册表是编译期常量，缺项属于代码写错而不是用户输入问题
		return "", fmt.Errorf("config: setting %q has no normalizer", "language")
	}
	normalized, err := s.Normalize(value)
	if err != nil {
		return "", err
	}
	lang, ok := normalized.(string)
	if !ok {
		return "", ErrInvalidValue
	}
	return lang, nil
}

// ParseLanguage 把一段文本按 language 项的规则归一化，供命令行 --lang 与启动路径使用。
//
// 与 NormalizeLanguage 分开是因为入口形态不同：命令行给的是文本，配置文件给的是
// JSON 原始值。判断“受不受支持”与“归一到哪个标签”仍然只在 language 项的 Normalize 里做。
func ParseLanguage(text string) (string, error) {
	return NormalizeLanguage(strings.TrimSpace(text))
}

// LoadConfig 从默认路径加载配置，是 LoadConfigAt 的便捷封装。
func LoadConfig() (*Config, error) {
	return LoadConfigAt(GetDefaultConfigPath())
}

// LoadConfigAt 从指定路径加载配置。核心逻辑一律接受显式路径，测试才能用临时目录
// 而不碰真实 HOME（与 store.go 的 At 系列保持同一约定）。
//
// 实现刻意不走 viper，原因有三：
//   - viper 是包级全局单例，SetConfigFile/Unmarshal 会在多次调用之间互相污染，
//     且无法指向临时路径，导致这条最常用的读路径根本没法测
//   - 它带进来的那套默认值（viper.SetDefault）会成为 settings 之外的第二份真相
//   - 取值判定由注册表的 Normalize 负责，直接用 mapstructure 做字段映射即可，
//     不必为此引入一整层配置框架
//
// 取值规则是“以 settings 登记的默认值为准，再用文件里的键覆盖”，于是默认值只有
// settings 一处真相，与 config show / config get 的取值完全同源。
//
// 配置文件不存在时返回全默认配置（不报错，首次运行属正常状态）；
// 存在但 JSON 语法非法时返回 error，由调用方决定如何呈现。
func LoadConfigAt(path string) (*Config, error) {
	raw, err := ReadRawAt(path)
	if err != nil {
		return nil, err
	}
	return decodeConfig(raw)
}

// decodeConfig 把“settings 默认值 + 传入的键值”解码成 Config。
//
// LoadConfigAt 与 defaultConfig 共用这一处，默认值因此只有 DefaultRaw 一份来源。
// 文件里出现的键先经 Setting.Normalize 收敛成规范类型与规范形态，再交给 mapstructure；
// 规范化失败即返回错误，不再“回退默认值继续跑”——那种回退会让同一份文件在
// config validate 里是 error、在运行期却照常工作，正是这次要消除的口径分叉。
//
// mapstructure 的 WeaklyTypedInput 必须关闭：类型判定已经由 Normalize 做完，
// 再开弱转换等于给同一条规则留了第二份、更宽松的实现。
func decodeConfig(raw map[string]any) (*Config, error) {
	merged := DefaultRaw()
	for _, key := range sortedRawKeys(raw) {
		v := raw[key]
		s, ok := Lookup(key)
		if !ok {
			// 未知键在运行期也判失败：它几乎总是拼写错误，而静默忽略会让用户以为设置生效了。
			// 报错必须带上键名，才能给出“清掉这一个键”的命令（见 recover.go）
			return nil, &KeyError{Key: key, Err: unknownKeyError(key)}
		}
		normalized, err := normalizeValue(s, v)
		if err != nil {
			// 旧语义值仍按 auto 继续跑，并保留原值交给 migrateDeprecatedConcurrency
			// 统一提示。这是迁移策略，不是取值解析规则，因此不在这里判失败
			if errors.Is(err, errDeprecatedConcurrency) {
				merged[s.Key] = v
				continue
			}
			// 带上键名：加载失败的输出要据此指明该清掉哪一个键
			return nil, &KeyError{Key: s.Key, Err: err}
		}
		merged[s.Key] = normalized
	}

	return decodeMerged(merged)
}

// decodeMerged 把“已经规范化的键值”解码成 Config。
//
// 宽松加载（recover.go）与严格加载（decodeConfig）共用这一处：两者对“规范值怎么变成
// 结构体”必须给出同一个结论，否则同一份文件的同一个键会在两条路径上得到不同结果。
func decodeMerged(merged map[string]any) (*Config, error) {
	var cfg Config
	dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           &cfg,
		WeaklyTypedInput: false,
	})
	if err != nil {
		return nil, err
	}
	if err := dec.Decode(merged); err != nil {
		return nil, err
	}

	migrateDeprecatedConcurrency(&cfg)

	return &cfg, nil
}

// KeyError 表示某个键让配置加载失败。
//
// 它存在的唯一目的是让调用方知道“该清掉哪个键”：加载失败的输出必须给出可直接执行的
// 修复命令，而取值错误的文案里只有取值与期望，没有下一步能敲的东西（见 recover.go 的
// WithRecoveryHint）。错误链保持完整，errors.Is 仍能穿透到具体原因。
type KeyError struct {
	Key string
	Err error
}

func (e *KeyError) Error() string { return e.Err.Error() }

func (e *KeyError) Unwrap() error { return e.Err }

// unknownKeyError 是“未知键”的唯一文案，运行期加载与 config validate 共用。
//
// 两条路径必须同结论：此前 validate 把未知键判为 error 并退出 1，而运行期静默忽略、
// 照常启动，用户看到的是“校验说文件有问题，实际又一切正常”。现在两边都判失败，
// 因为未知键几乎总是拼写错误，忽略它只会让用户以为配置生效了。
func unknownKeyError(key string) error {
	return fmt.Errorf("%s", l10n.T("Unknown config key {{.Key}} (possible typo); remove it from the config file",
		map[string]any{"Key": key}))
}

// defaultConfig 返回所有字段填好默认值的配置。
//
// 走的是“空文件 + settings 默认值”这条与 LoadConfigAt 完全相同的路径，因此两者不可能
// 对某个键给出不同答案；改回“零值结构体 + 补默认值”会让 bool 项退回 false
func defaultConfig() *Config {
	cfg, err := decodeConfig(nil)
	if err != nil {
		// 这里的错误只可能来自解码器自身的配置（在源码里固定），运行期无法触发；
		// 返回零值结构体等于在没有人知情的情况下给出另一份默认值，不如直接暴露
		panic(err)
	}
	return cfg
}

// DefaultConfig 返回一份全默认值的配置。
//
// 只有一种场景需要它：配置文件连读都读不出来、但进程仍要起来（当前是 ggt ui——设置面板
// 正是用来报告并修复这种文件的）。其余任何路径都必须走 LoadConfig 或宽松加载，
// 直接拿默认配置跑命令等于假装用户没有配置。
func DefaultConfig() *Config {
	return defaultConfig()
}

// 关于“把整份 Config 写回文件”的能力：本项目刻意不提供。
// 键名清单在 json/mapstructure tag、settings[].Key 之外本就已经足够多，再让一个
// Config→raw 的转换器抄一遍键名，就会出现“加了字段却永远写不进文件”且无人报错的死角。
// 需要落盘时一律走 store.go 的单键写入（SetKeyAt / UnsetKeyAt），只动调用方真正
// 关心的那个键，文件里其他内容（含用户手写的未知键）原样保留。

// resolveConcurrency 把配置里的并发数解析成可直接交给 worker 的实际并发数。
//
// 只接受两种形态（大小写不敏感、去首尾空白）：auto，或 1 到 maxConcurrency 的整数。
// 空串按 auto 处理：缺键与“手工构造的零值 Config”都会走到这里，若在这里另设一套
// 默认值，auto 的含义就有了第二份实现，两处迟早会对不上。
//
// 越界与无法解析一律返回 error 而不是回退默认值：并发数是本机资源保护参数，
// 静默回退会让用户以为 -c 200 生效了，实际跑的却是另一个数字。
// 官方信源：https://pkg.go.dev/runtime#NumCPU 与 https://pkg.go.dev/builtin#max
func resolveConcurrency(raw string) (int, error) {
	v := strings.TrimSpace(raw)
	if v == "" || strings.EqualFold(v, DefaultConcurrency) {
		return autoConcurrency(), nil
	}
	// 与配置文件、config set 共用同一套规范十进制解析：Atoi 会接受 "010" 与 "+8"，
	// 那会让同一条规则在 -c 与配置文件两个入口给出不同结论
	n, ok := decimalStringToInt(v)
	if !ok || n < 1 || n > maxConcurrency {
		return 0, invalidConcurrencyError(raw)
	}
	return n, nil
}

// autoConcurrency 按本机逻辑核数计算 auto 的取值。
func autoConcurrency() int {
	return autoConcurrencyFor(runtime.NumCPU())
}

// autoConcurrencyFor 把核数换算成 auto 的取值。
// 单列出来是为了让测试用固定核数断言“至少为 1”与“封顶 8”这两条边界，
// 不必依赖跑测试的机器有几个核。
func autoConcurrencyFor(ncpu int) int {
	return min(max(1, ncpu/2), autoConcurrencyCeiling)
}

// invalidConcurrencyError 生成并发数越界或无法解析时的报错。
// 必须同时给出实际值与允许范围：只说“取值非法”时，用户分不清是 -c 写错了，
// 还是配置文件里那份旧值没改。来源也点出来，因为运行期只有这两个入口。
func invalidConcurrencyError(raw string) error {
	return fmt.Errorf("%s", l10n.T("Invalid concurrency value {{.Value}}: expected auto or an integer from 1 to {{.Max}} (set with -c or the concurrency setting)",
		map[string]any{"Value": strings.TrimSpace(raw), "Max": maxConcurrency}))
}

// ValidateCommandLineConcurrency 解析并校验 -c 的取值，返回实际并发数。
//
// 与配置文件刻意不同的一点：命令行没有“空串表示默认”这一形态。脚本里 `-c "$VAR"`
// 而 VAR 未设值时，空串若按 auto 处理就又是一次静默忽略；它还会让一份写着非法值的
// 配置文件借 -c "" 绕过启动校验。配置文件里的空串仍按 auto 处理（缺键与空值是文件的
// 正常形态），两条入口因此各有自己的规则，不要合并成一处。
//
// 空值在报错里显示成 (empty)：直接塞空串会让模板渲染出“value : expected”这种缺口。
func ValidateCommandLineConcurrency(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, invalidConcurrencyError("(empty)")
	}
	return resolveConcurrency(raw)
}

// deprecatedConcurrencyValues 是旧版支持的 CPU 相对语义。
// 保留这份清单只为识别旧配置：它们的含义已并入 auto，新版永远不会写入这些词。
var deprecatedConcurrencyValues = map[string]bool{
	"cpuhalf":    true,
	"cpufull":    true,
	"cpuquarter": true,
}

// isDeprecatedConcurrencyValue 判断取值是否落在旧语义清单里。
// 迁移、体检分级与 config get 的归一化都走这一处，三处各写一份大小写处理迟早会分叉。
func isDeprecatedConcurrencyValue(raw string) bool {
	return deprecatedConcurrencyValues[strings.ToLower(strings.TrimSpace(raw))]
}

// deprecatedConcurrencyWarned 让迁移提示每个进程只出现一次。
// 看板会周期性重读配置（见 cmd/ui.go 的 configNow），每读一次提示一遍会把终端刷满，
// 而用户只需要知道“旧值要改写”这一件事。
var deprecatedConcurrencyWarned atomic.Bool

// migrateDeprecatedConcurrency 把旧语义值改写成 auto，并在首次遇到时提示。
//
// 刻意不换算成某个数字：CPUFull 原本表示“全部核数”，换算成今天这台机器的核数
// 再落盘，换机器后就是错的；auto 保持了“跟着本机走”的原意。
func migrateDeprecatedConcurrency(cfg *Config) {
	raw := strings.TrimSpace(cfg.Concurrency)
	if !isDeprecatedConcurrencyValue(raw) {
		return
	}
	cfg.Concurrency = DefaultConcurrency
	if deprecatedConcurrencyWarned.CompareAndSwap(false, true) {
		logger.Warn(l10n.T("The concurrency value {{.Value}} is deprecated and now behaves like auto; change it to auto or an integer",
			map[string]any{"Value": raw}))
	}
}

// ConcurrencyValue 返回配置当前生效的实际并发数（auto 已按本机核数换算）。
// 各命令在启动 worker 前统一调用本方法，自行读取 Concurrency 字符串会漏掉上限校验；
// 返回 error 而不是回退默认值，越界的并发数必须让命令以非 0 退出码失败。
func (c *Config) ConcurrencyValue() (int, error) {
	return resolveConcurrency(c.Concurrency)
}
