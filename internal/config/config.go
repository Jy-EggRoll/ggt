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
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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
	// 因此 applyConfigDefaults 不需要为它补默认值——补了也是空串）
	Theme string `mapstructure:"theme" json:"theme"`
	// ThemeDark / ThemeLight 是“跟随系统”时深色与浅色各自用哪套主题，
	// 对应 VSCode 的 workbench.preferredDarkColorTheme / preferredLightColorTheme
	ThemeDark  string `mapstructure:"theme_dark" json:"theme_dark"`
	ThemeLight string `mapstructure:"theme_light" json:"theme_light"`
	// FontUI / FontMono 是页面两片区域各自的字体栈，空串表示不指定字体：
	// 页面那层只声明“特性”（界面区 sans-serif、等宽区 monospace），具体落到哪个字体由浏览器回退决定
	// （与 Theme 同理，空串是有意义的零值，applyConfigDefaults 不需要补值）
	FontUI   string `mapstructure:"font_ui" json:"font_ui"`
	FontMono string `mapstructure:"font_mono" json:"font_mono"`
	// NotifyTimeout 是网页通知自动消失的秒数。0 表示不自动消失，也就是零值即默认，
	// 因此 applyConfigDefaults 同样不需要为它补值
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

// LoadLanguage 只读取配置文件里的 language 字段，用于在命令行解析之前确定输出语言。
//
// 单独提供本函数而不是复用 LoadConfig，原因有二：
//   - 语言必须在 rootCmd.Execute() 之前确定（cobra 的 --help 不会执行 PersistentPreRunE），
//     而 LoadConfig 依赖的 GetDefaultConfigPath 在主目录不可用时会直接退出进程
//   - 只取一个字段，避免为纯展示路径做一次全量解码与默认值补全
//
// 这里刻意直接解析 JSON，不引入 viper 这类包级全局单例：语言必须能在 --help
// 路径上被读取，任何全局状态污染都会让“本次运行读到哪个语言”变得不可预测
// （本包已整体不使用 viper，原因详见 LoadConfigAt 的注释）。
//
// 配置文件不存在、不可读或格式非法时一律返回 error，由调用方回退到默认语言。
func LoadLanguage() (string, error) {
	path, err := getConfigPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var probe struct {
		Language string `json:"language"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return "", err
	}
	return strings.TrimSpace(probe.Language), nil
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
//   - 容错能力实际来自 mapstructure 的 WeaklyTypedInput，直接用 mapstructure 即可，
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

// decodeConfig 把“settings 默认值 + 传入的键值”解码成 Config，并补上非法取值的回退。
//
// LoadConfigAt 与 defaultConfig 共用这一处，默认值因此只有 DefaultRaw 一份来源。
// 早先 defaultConfig 是在零值结构体上跑 applyConfigDefaults，bool 型配置项会拿到 Go 的
// 零值 false，而 LoadConfigAt 给出 true——两条读路径对同一个键给出不同答案，
// 且只有“文件里没写这个键”时才暴露，很难在测试之外发现
func decodeConfig(raw map[string]any) (*Config, error) {
	// 取值形态先查一遍：布尔值不是任何非布尔配置项的合法形态，而弱类型解码会把它
	// 转成 "1"/"0" 悄悄接受（见 valueShapeError）。这一步必须在解码之前，
	// 否则 {"concurrency": true} 会变成并发数 1 跑起来，与 config get 的结论相反
	for key, v := range raw {
		s, ok := Lookup(key)
		if !ok {
			// 未知键不在这里管：运行期一律忽略，由 config validate 报出
			continue
		}
		if err := valueShapeError(s, v); err != nil {
			return nil, err
		}
	}

	// 默认值先取：文件里缺哪个键，哪个键就保持 settings 里的默认值
	merged := DefaultRaw()
	for k, v := range raw {
		merged[k] = v
	}

	var cfg Config
	// WeaklyTypedInput 允许已有的数字型 concurrency 配置在反序列化为 string 字段时
	// 自动转为字符串，也容忍 "true" 这类字符串形态的布尔值，
	// 避免历史配置文件（旧版把并发数存成 int）读取报错。
	// 它唯一不容忍的形态在这里拦：JSON 的布尔值（见 valueShapeError）。
	// 官方信源：https://github.com/go-viper/mapstructure/v2
	dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           &cfg,
		WeaklyTypedInput: true,
	})
	if err != nil {
		return nil, err
	}
	if err := dec.Decode(merged); err != nil {
		return nil, err
	}

	// 文件里写了非法值（如 size_bucket_low_mb: 0、空串的语言）时的回退。
	// 这些值由 ggt config validate 作为问题报出，本函数只保证“报出之前程序仍然可用”。
	// 例外是取值形态（布尔值用在非布尔项上）：那种形态已经在解码前直接报错，
	// 因为“弱转成 1 继续跑”正是本次要消除的口径分叉
	//
	// concurrency 另有一道旧值迁移：旧版存的是 CPUHalf 这类语义串，读到就按 auto
	// 处理并提示一次，见 migrateDeprecatedConcurrency
	migrateDeprecatedConcurrency(&cfg)
	applyConfigDefaults(&cfg)

	return &cfg, nil
}

// valueShapeError 判断文件里某个键的 JSON 取值形态能否被接受。
//
// 拦布尔值是这次专门加的一道：mapstructure 的 WeaklyTypedInput 会把 JSON 的 true/false
// 转成字符串 "1"/"0"，于是 {"concurrency": true} 在运行期变成并发数 1 照常跑，而
// config get 拿到布尔 true、解析失败直接报错——同一份文件在两处给出相反结论。
// 布尔值本来就不是任何非布尔配置项的合法形态（数字与数字字符串才是），因此在解码之前
// 统一拒绝；运行期加载、config get、config validate 共用这一处判定，config set 收到的
// 文本 "true" 也走同一个 invalidValueError，四处的说法因此一致。
//
// 路径列表项一并在这里判：布尔值转不成数组，交给 mapstructure 只会漏出一句
// "unconvertible type" 的原文，不如给出与体检相同的说法。
//
// 刻意只拦这两种：数字与数字字符串之间的弱转换（concurrency: 8、size_bucket_low_mb: "500"）
// 是历史配置的合法形态，字符串形态的布尔值（ignore_submodules: "true"）同理，都不能动。
func valueShapeError(s Setting, v any) error {
	// 路径项先判：它的规范形态是数组，而 Setting.Expected 对这类项是空的
	// （数组的“合法取值”无法用一句话概括），套用下面那句会渲染出“(expected )”这种缺口。
	// JSON null 与缺键同义（运行期就是空列表），不拒绝：它没有类型可写错，
	// 拒绝它只会让一份原本能跑的配置在升级后起不来
	if s.Kind == KindPaths {
		if v == nil {
			return nil
		}
		if _, isList := v.([]any); !isList {
			return fmt.Errorf("%s", l10n.T("Expected an array for {{.Key}}, got {{.Type}}",
				map[string]any{"Key": s.Key, "Type": jsonTypeName(v)}))
		}
		return nil
	}
	if _, isBool := v.(bool); isBool && s.Kind != KindBool {
		return invalidValueError(s, ValueText(v))
	}
	return nil
}

// defaultConfig 返回所有字段填好默认值的配置。
//
// 走的是“空文件 + settings 默认值”这条与 LoadConfigAt 完全相同的路径，因此两者不可能
// 对某个键给出不同答案；改回“零值结构体 + applyConfigDefaults”会让 bool 项退回 false
func defaultConfig() *Config {
	cfg, err := decodeConfig(nil)
	if err != nil {
		// 这里的错误只可能来自解码器自身的配置（在源码里固定），运行期无法触发；
		// 返回零值结构体等于在没有人知情的情况下给出另一份默认值，不如直接暴露
		panic(err)
	}
	return cfg
}

// applyConfigDefaults 对“未设置或非法”的字段补上 settings 里登记的默认值：
//   - concurrency 为空 → "auto"（语义值，运行时才按核数换算成具体数字）
//   - size_bucket_low_mb / size_bucket_high_mb <= 0 → 500 / 800
//   - size_unit 为空 → "decimal"
//   - language 为空 → locales.Default（"en"）
//   - log_level 为空 → "warn"（注意**非法取值刻意不在这里改写**：它要留给命令层去告警，
//     在这里静默替换掉，用户就再也看不到“你写的级别我没认”这条提示了）
//   - repo_paths / parent_paths 为 nil → 空切片，使序列化结果是 [] 而不是 null
//   - ignore_submodules 是 bool，零值 false 即“默认包含子模块”，无需补值
//   - notify_timeout 是 int，零值 0 即“通知不自动消失”，无需补值
//   - push_tags / prune_branches 默认 true，而 bool 的零值是 false，这里补不出这个区别；
//     它们的默认值由 decodeConfig 统一从 DefaultRaw 解码得到，本函数不碰
//
// 取值一律向 settings 注册表要，本函数不再出现任何默认值字面量：原先这里把
// 500/800/"decimal"/"en" 又抄了一遍，与 settings[].Default 分叉时没有任何测试能拦住。
// 新增配置项时在 settings 里加一条，这里按需补上对应分支即可。
func applyConfigDefaults(cfg *Config) {
	if strings.TrimSpace(cfg.Concurrency) == "" {
		cfg.Concurrency = defaultStringOf("concurrency")
	}
	if cfg.SizeBucketLowMB <= 0 {
		cfg.SizeBucketLowMB = defaultIntOf("size_bucket_low_mb")
	}
	if cfg.SizeBucketHighMB <= 0 {
		cfg.SizeBucketHighMB = defaultIntOf("size_bucket_high_mb")
	}
	if cfg.SizeUnit == "" {
		cfg.SizeUnit = defaultStringOf("size_unit")
	}
	if strings.TrimSpace(cfg.Language) == "" {
		cfg.Language = defaultStringOf("language")
	}
	if strings.TrimSpace(cfg.LogLevel) == "" {
		cfg.LogLevel = defaultStringOf("log_level")
	}
	// theme_dark / theme_light 与 theme 不同：它们的空串没有含义（跟随系统时“没有配色可渲染”），
	// 因此和上面几项一样补默认值。漏了这两个分支时，LoadConfigAt（走注册表的通用路径）与
	// 这里会给出不同答案，而 TestLoadConfigAt 的“全默认配置”断言正好抓住这个分叉
	if strings.TrimSpace(cfg.ThemeDark) == "" {
		cfg.ThemeDark = defaultStringOf("theme_dark")
	}
	if strings.TrimSpace(cfg.ThemeLight) == "" {
		cfg.ThemeLight = defaultStringOf("theme_light")
	}
	if cfg.RepoPaths == nil {
		cfg.RepoPaths = []string{}
	}
	if cfg.ParentPaths == nil {
		cfg.ParentPaths = []string{}
	}
}

// defaultStringOf 返回 settings 里登记的字符串型默认值，
// 键不存在或类型不符时返回零值——settings 与 Config 字段的对齐由
// TestSettingsMatchConfigFields 守住，这里不重复校验。
func defaultStringOf(key string) string {
	v, _ := DefaultRaw()[key].(string)
	return v
}

// defaultIntOf 返回 settings 里登记的整数型默认值，语义同 defaultStringOf。
func defaultIntOf(key string) int {
	v, _ := DefaultRaw()[key].(int)
	return v
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
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > maxConcurrency {
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
