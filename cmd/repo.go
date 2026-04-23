package cmd

import (
	"os"
	"path/filepath"

	"ggt/internal/config"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

var repoCmd = &cobra.Command{
	Use:   "repo",
	Short: "管理仓库路径配置",
	Long: `管理已配置的仓库路径列表。
	
使用示例:
  ggt repo list              列出所有仓库
  ggt repo add <path>       添加仓库路径
  ggt repo remove <path>  移除仓库路径
  ggt repo add-parent <path> 添加父目录（自动扫描其中的 git 仓库）`,
}

var repoListCmd = &cobra.Command{
	Use:   "list",
	Short: "列出所有已配置的仓库路径",
	Long: `列出所有已配置的仓库路径。
	
包括直接添加的仓库和从父目录扫描到的仓库。`,
	Run: func(cmd *cobra.Command, args []string) {
		repos := GetRepoList()
		if len(repos) == 0 {
			pterm.Warning.Println("未配置任何仓库")
			return
		}
		PrintRepoList(repos)
	},
}

var repoAddCmd = &cobra.Command{
	Use:   "add <path>",
	Short: "添加一个仓库路径到配置文件",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		path := args[0]
		absPath, err := filepath.Abs(path)
		if err != nil {
			ErrorMsg("解析路径失败: " + err.Error())
			return
		}

		if !isGitRepo(absPath) {
			ErrorMsg("该路径不是 git 仓库: " + absPath)
			return
		}

		cfg := GetConfig()
		for _, existing := range cfg.RepoPaths {
			if existing == absPath {
				ErrorMsg("该路径已存在: " + absPath)
				return
			}
		}

		cfg.RepoPaths = append(cfg.RepoPaths, absPath)
		if err := config.SaveConfig(cfg); err != nil {
			ErrorMsg("保存配置失败: " + err.Error())
			return
		}

		SuccessMsg("已添加仓库: " + absPath)
	},
}

var repoRemoveCmd = &cobra.Command{
	Use:   "remove <path>",
	Short: "从配置文件移除一个仓库路径",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		path := args[0]
		absPath, err := filepath.Abs(path)
		if err != nil {
			ErrorMsg("解析路径失败: " + err.Error())
			return
		}

		cfg := GetConfig()
		found := false
		newPaths := make([]string, 0)
		for _, existing := range cfg.RepoPaths {
			if existing == absPath {
				found = true
				continue
			}
			newPaths = append(newPaths, existing)
		}

		if !found {
			ErrorMsg("该路径不存在: " + absPath)
			return
		}

		cfg.RepoPaths = newPaths
		if err := config.SaveConfig(cfg); err != nil {
			ErrorMsg("保存配置失败: " + err.Error())
			return
		}

		SuccessMsg("已移除仓库: " + absPath)
	},
}

var repoAddParentCmd = &cobra.Command{
	Use:   "add-parent <path>",
	Short: "添加一个父目录，自动扫描其中的所有 git 仓库路径",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		path := args[0]
		absPath, err := filepath.Abs(path)
		if err != nil {
			ErrorMsg("解析路径失败: " + err.Error())
			return
		}

		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			ErrorMsg("目录不存在: " + absPath)
			return
		}

		cfg := GetConfig()
		for _, existing := range cfg.ParentPaths {
			if existing == absPath {
				ErrorMsg("该父目录已存在: " + absPath)
				return
			}
		}

		cfg.ParentPaths = append(cfg.ParentPaths, absPath)
		if err := config.SaveConfig(cfg); err != nil {
			ErrorMsg("保存配置失败: " + err.Error())
			return
		}

		SuccessMsg("已添加父目录: " + absPath)
	},
}

func init() {
	rootCmd.AddCommand(repoCmd)
	repoCmd.AddCommand(repoListCmd)
	repoCmd.AddCommand(repoAddCmd)
	repoCmd.AddCommand(repoRemoveCmd)
	repoCmd.AddCommand(repoAddParentCmd)
}
