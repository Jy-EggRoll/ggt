// config 包管理 ggt 的 JSON 配置文件。
// 默认路径：~/.config/go-git-ggt/ggt-config.json
//
// 配置项：
//   - repo_paths: 直接添加的仓库路径列表
//   - parent_paths: 父目录列表（自动扫描其中的 git 仓库）
//   - concurrency: 并发数，默认为 CPU 逻辑核数的一半
package config

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/pterm/pterm"
	"github.com/spf13/viper"
)

// Config 是 ggt 配置文件的 Go 结构体映射。
// 字段标签同时兼容 viper (mapstructure) 和 JSON 序列化。
type Config struct {
	ParentPaths []string `mapstructure:"parent_paths" json:"parent_paths"`
	RepoPaths   []string `mapstructure:"repo_paths" json:"repo_paths"`
	Concurrency int      `mapstructure:"concurrency" json:"concurrency"`
}

// GetDefaultConfigPath 返回配置文件的默认路径。
func GetDefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		pterm.Error.Println("获取用户主目录失败:", err)
		os.Exit(1)
	}
	return filepath.Join(home, ".config", "go-git-ggt", "ggt-config.json")
}

// LoadConfig 从默认路径加载配置。
// 如果配置文件不存在，返回含默认并发数的空配置（不报错）。
func LoadConfig() (*Config, error) {
	viper.SetConfigType("json")
	viper.SetConfigFile(GetDefaultConfigPath())

	if err := viper.ReadInConfig(); err != nil {
		if os.IsNotExist(err) {
			return &Config{
				Concurrency: getDefaultConcurrency(),
			}, nil
		}
		return nil, err
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, err
	}

	if config.Concurrency <= 0 {
		config.Concurrency = getDefaultConcurrency()
	}

	return &config, nil
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

	return viper.WriteConfig()
}

// getDefaultConcurrency 返回推荐的默认并发数：CPU 逻辑核数的一半。
// git 操作涉及大量 I/O，过高并发会吃满 CPU。
func getDefaultConcurrency() int {
	ncpu := runtime.NumCPU()
	concurrency := ncpu / 2
	if concurrency < 1 {
		concurrency = 1
	}
	return concurrency
}
