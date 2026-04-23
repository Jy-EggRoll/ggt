package config

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/pterm/pterm"
	"github.com/spf13/viper"
)

type Config struct {
	ParentPaths []string `mapstructure:"parent_paths" json:"parent_paths"`
	RepoPaths   []string `mapstructure:"repo_paths" json:"repo_paths"`
	Concurrency int      `mapstructure:"concurrency" json:"concurrency"`
}

func GetDefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		pterm.Error.Println("获取用户主目录失败:", err)
		os.Exit(1)
	}
	return filepath.Join(home, ".config", "go-git-ggt", "ggt-config.json")
}

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

func getDefaultConcurrency() int {
	// 获取逻辑 CPU 数的一半
	ncpu := runtime.NumCPU()
	concurrency := ncpu / 2
	if concurrency < 1 {
		concurrency = 1
	}
	return concurrency
}
