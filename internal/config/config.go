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
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/pterm/pterm"
	"github.com/spf13/viper"
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
}

// getConfigPath 计算配置文件的默认路径，失败时返回 error 而不终止进程。
// 供 LoadLanguage 这类"必须永远可用"的路径使用——它们可能在 --help 之前被调用，
// 此时直接退出进程会导致连帮助都打印不出来。
func getConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "go-git-ggt", "ggt-config.json"), nil
}

// GetDefaultConfigPath 返回配置文件的默认路径。
// 主目录不可用时打印错误并退出进程——这是命令运行期的合理兜底，
// 但不要把它用在 --help 之前会被触发的路径上（那种场合用 getConfigPath）。
func GetDefaultConfigPath() string {
	path, err := getConfigPath()
	if err != nil {
		pterm.Error.Println("获取用户主目录失败:", err)
		os.Exit(1)
	}
	return path
}

// LoadLanguage 只读取配置文件里的 language 字段，用于在命令行解析之前确定输出语言。
//
// 单独提供本函数而不是复用 LoadConfig，原因有二：
//   - 语言必须在 rootCmd.Execute() 之前确定（cobra 的 --help 不会执行 PersistentPreRunE），
//     而 LoadConfig 依赖的 GetDefaultConfigPath 在主目录不可用时会直接退出进程
//   - 只取一个字段，避免为纯展示路径做一次全量解码与默认值补全
//
// 这里刻意只做裸 JSON 解析而不走 viper：viper 是包级全局单例，用它会往全局状态里
// 写入本条配置，且 viper.Set 产生的 override 不会在下次 ReadInConfig 时清除。
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

// LoadConfig 从默认路径加载配置。
// 如果配置文件不存在，返回含默认并发数的空配置（不报错）。
func LoadConfig() (*Config, error) {
	viper.SetConfigType("json")
	viper.SetConfigFile(GetDefaultConfigPath())

	if err := viper.ReadInConfig(); err != nil {
		if os.IsNotExist(err) {
			return defaultConfig(), nil
		}
		return nil, err
	}

	var config Config
	// WeaklyTypedInput 允许已有的数字型 concurrency 配置在反序列化为 string 字段时
	// 自动转为字符串，避免历史配置文件（旧版存 int）读取报错。
	// 官方信源：https://github.com/spf13/viper 与 https://github.com/go-viper/mapstructure/v2
	if err := viper.Unmarshal(&config, func(dc *mapstructure.DecoderConfig) {
		dc.WeaklyTypedInput = true
	}); err != nil {
		return nil, err
	}

	applyConfigDefaults(&config)

	return &config, nil
}

// defaultConfig 返回所有字段填好默认值的配置。
// 配置文件不存在时直接返回此结构，避免与 applyConfigDefaults 出现两处默认值逻辑。
func defaultConfig() *Config {
	cfg := &Config{}
	applyConfigDefaults(cfg)
	return cfg
}

// applyConfigDefaults 对未显式设置的字段补默认值：
//   - concurrency 为空时取 DefaultConcurrency（"CPUHalf"，而非具体数字）
//   - size_bucket_low_mb / size_bucket_high_mb <= 0 时取 500 / 800
//   - size_unit 为空时取 "decimal"
//   - language 为空时取 "en"（英文是默认语言，中文为兼容层）
//   - ignore_submodules 为 bool，零值 false 即"默认包含子模块"，无需补默认值
func applyConfigDefaults(cfg *Config) {
	if strings.TrimSpace(cfg.Concurrency) == "" {
		cfg.Concurrency = DefaultConcurrency
	}
	if cfg.SizeBucketLowMB <= 0 {
		cfg.SizeBucketLowMB = 500
	}
	if cfg.SizeBucketHighMB <= 0 {
		cfg.SizeBucketHighMB = 800
	}
	if cfg.SizeUnit == "" {
		cfg.SizeUnit = "decimal"
	}
	if strings.TrimSpace(cfg.Language) == "" {
		cfg.Language = "en"
	}
}

// SaveConfig 将配置写入默认路径的 JSON 文件。
// 自动创建父目录。
func SaveConfig(cfg *Config) error {
	configPath := GetDefaultConfigPath()
	dir := filepath.Dir(configPath)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	viper.SetConfigFile(configPath)
	viper.Set("parent_paths", cfg.ParentPaths)
	viper.Set("repo_paths", cfg.RepoPaths)
	viper.Set("concurrency", cfg.Concurrency)
	viper.Set("ignore_submodules", cfg.IgnoreSubmodules)
	viper.Set("size_bucket_low_mb", cfg.SizeBucketLowMB)
	viper.Set("size_bucket_high_mb", cfg.SizeBucketHighMB)
	viper.Set("size_unit", cfg.SizeUnit)
	viper.Set("language", cfg.Language)

	return viper.WriteConfig()
}

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
