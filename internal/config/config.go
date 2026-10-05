// config 包管理 ggt 的 JSON 配置文件。
// 默认路径：~/.config/go-git-ggt/ggt-config.json
//
// 配置项：
//   - repo_paths: 直接添加的仓库路径列表
//   - parent_paths: 父目录列表（自动扫描其中的 git 仓库）
//   - concurrency: 并发数，存为语义串（如 "CPUHalf"）或显式数字串（如 "8"），
//     未设置时默认 "CPUHalf"（CPU 逻辑核数的一半），详见 resolveConcurrency
//   - ignore_submodules: 是否在所有功能中忽略子模块，默认 false（即默认包含子模块）
//   - size_bucket_low_mb: size 命令分桶的下界阈值（MB），默认 500
//   - size_bucket_high_mb: size 命令分桶的上界阈值（MB），默认 800
//   - size_unit: size 命令分桶时 MB 的换算口径，"decimal"(1 MB = 1,000,000 字节)
//     或 "binary"(1 MB = 1024*1024 字节，即 MiB)，默认 "decimal"
//   - language: 输出语言（如 "en"、"zh-CN"），默认 "en"；命令行 --lang 优先级更高
//   - log_level: 诊断日志级别（debug/info/warn/error），默认 "warn"；
//     命令行 -v/-vv 优先级更高。它只影响诊断日志，不影响面向用户的正常输出
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/pterm/pterm"
)

// DefaultConcurrency 是并发数的默认语义值：取 CPU 逻辑核数的一半。
// 配置文件中未显式设置 concurrency 时，存储该语义串而非具体数字。
const DefaultConcurrency = "CPUHalf"

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
// 这里刻意只做裸 JSON 解析，不引入 viper 这类包级全局单例：语言必须能在 --help
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

	// 默认值先取：文件里缺哪个键，哪个键就保持 settings 里的默认值
	merged := DefaultRaw()
	for k, v := range raw {
		merged[k] = v
	}

	var cfg Config
	// WeaklyTypedInput 允许已有的数字型 concurrency 配置在反序列化为 string 字段时
	// 自动转为字符串，也容忍 "true" 这类字符串形态的布尔值，
	// 避免历史配置文件（旧版把并发数存成 int）读取报错。
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
	// 这些值由 ggt config validate 作为问题报出，本函数只保证“报出之前程序仍然可用”
	applyConfigDefaults(&cfg)

	return &cfg, nil
}

// defaultConfig 返回所有字段填好默认值的配置。
// 与 LoadConfigAt 共用 applyConfigDefaults，默认值同源，不存在第二份默认值逻辑。
func defaultConfig() *Config {
	cfg := &Config{}
	applyConfigDefaults(cfg)
	return cfg
}

// applyConfigDefaults 对“未设置或非法”的字段补上 settings 里登记的默认值：
//   - concurrency 为空 → "CPUHalf"（语义串，而非具体数字）
//   - size_bucket_low_mb / size_bucket_high_mb <= 0 → 500 / 800
//   - size_unit 为空 → "decimal"
//   - language 为空 → locales.Default（"en"）
//   - log_level 为空 → "warn"（注意**非法取值刻意不在这里改写**：它要留给命令层去告警，
//     在这里静默替换掉，用户就再也看不到“你写的级别我没认”这条提示了）
//   - repo_paths / parent_paths 为 nil → 空切片，使序列化结果是 [] 而不是 null
//   - ignore_submodules 是 bool，零值 false 即“默认包含子模块”，无需补值
//   - notify_timeout 是 int，零值 0 即“通知不自动消失”，无需补值
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

// resolveConcurrency 把配置里读到的并发语义串解析为可直接用于 worker 的实际并发数。
// 支持三种 CPU 相对语义（官方信源：https://pkg.go.dev/runtime#NumCPU）：
//   - "CPUHalf"   （默认）CPU 逻辑核数的一半
//   - "CPUFull"   全部 CPU 逻辑核数
//   - "CPUQuarter" CPU 逻辑核数四分之一
//
// 也支持直接写正整数串（如 "8"）；空串或不合法串回退到 CPUHalf 语义。
// 解析结果至少为 1，避免 0 或负数导致 worker 无法启动。
// 官方信源：https://pkg.go.dev/builtin#max
func resolveConcurrency(raw string) int {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "cpuhalf":
		return max(1, runtime.NumCPU()/2)
	case "cpufull":
		return max(1, runtime.NumCPU())
	case "cpuquarter":
		return max(1, runtime.NumCPU()/4)
	default:
		if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && n > 0 {
			return n
		}
		return max(1, runtime.NumCPU()/2)
	}
}

// ConcurrencyValue 返回配置当前生效的实际并发数（已解析为 int）。
// 各命令在启动 worker 时应统一调用本方法，而非自行读取 Concurrency 字符串。
func (c *Config) ConcurrencyValue() int {
	return resolveConcurrency(c.Concurrency)
}
