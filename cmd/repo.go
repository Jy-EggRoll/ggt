package cmd

import (
	"os"
	"path/filepath"

	"ggt/internal/config"
	"ggt/internal/git"
	"ggt/pkg/l10n"
	"github.com/spf13/cobra"
)

// repoCmd 实现 "ggt repo" 及其子命令，管理仓库路径配置。
// 配置存储在 ~/.config/go-git-ggt/ggt-config.json 中。
func newRepoCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "repo",
		Short: l10n.T("Manage repository path configuration", nil),
		Long: l10n.T(`Manage the list of configured repository paths.

Examples:
  ggt repo list              List all repositories
  ggt repo add <path>        Add a repository path
  ggt repo remove <path>     Remove a repository path
  ggt repo add-parent <path> Add a parent directory (auto-discovers git repositories inside)`, nil),
	}
	c.AddCommand(newRepoListCmd(), newRepoAddCmd(), newRepoRemoveCmd(), newRepoAddParentCmd())
	return c
}

// repoListCmd 列出所有已配置的仓库路径。
// 包括直接添加的仓库和从父目录扫描到的仓库。
func newRepoListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "list",
		Short: l10n.T("List all configured repository paths", nil),
		Long: l10n.T(`List all configured repository paths.

Includes repositories added directly and those discovered under parent directories.`, nil),
		Run: func(cmd *cobra.Command, args []string) {
			repos := GetRepoList()
			if len(repos) == 0 {
				WarnMsg(l10n.T("No repositories configured", nil))
				return
			}
			PrintRepoList(repos)
		},
	}
	return c
}

// repoAddCmd 添加一个仓库路径到配置文件。
// 路径必须指向一个有效的 git 仓库（存在 .git 目录）。
func newRepoAddCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "add <path>",
		Short: l10n.T("Add a repository path to the config file", nil),
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			path := args[0]
			absPath, err := filepath.Abs(path)
			if err != nil {
				ErrorMsg(l10n.T("Failed to resolve path: {{.Err}}", map[string]any{"Err": err}))
				return
			}

			if !git.IsRepo(absPath) {
				ErrorMsg(l10n.T("Not a git repository: {{.Path}}", map[string]any{"Path": absPath}))
				return
			}

			cfg := GetConfig()
			for _, existing := range cfg.RepoPaths {
				if existing == absPath {
					ErrorMsg(l10n.T("Path already exists: {{.Path}}", map[string]any{"Path": absPath}))
					return
				}
			}

			cfg.RepoPaths = append(cfg.RepoPaths, absPath)
			if err := config.SaveConfig(cfg); err != nil {
				ErrorMsg(l10n.T("Failed to save the configuration: {{.Err}}", map[string]any{"Err": err}))
				return
			}

			SuccessMsg(l10n.T("Repository added: {{.Path}}", map[string]any{"Path": absPath}))
		},
	}
	return c
}

// repoRemoveCmd 从配置文件中移除一个仓库路径。
func newRepoRemoveCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "remove <path>",
		Short: l10n.T("Remove a repository path from the config file", nil),
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			path := args[0]
			absPath, err := filepath.Abs(path)
			if err != nil {
				ErrorMsg(l10n.T("Failed to resolve path: {{.Err}}", map[string]any{"Err": err}))
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
				ErrorMsg(l10n.T("Path does not exist: {{.Path}}", map[string]any{"Path": absPath}))
				return
			}

			cfg.RepoPaths = newPaths
			if err := config.SaveConfig(cfg); err != nil {
				ErrorMsg(l10n.T("Failed to save the configuration: {{.Err}}", map[string]any{"Err": err}))
				return
			}

			SuccessMsg(l10n.T("Repository removed: {{.Path}}", map[string]any{"Path": absPath}))
		},
	}
	return c
}

// repoAddParentCmd 添加一个父目录到配置文件。
// ggt 运行时会扫描该目录下的所有子目录，自动识别其中包含 .git 的仓库。
func newRepoAddParentCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "add-parent <path>",
		Short: l10n.T("Add a parent directory and auto-discover all git repositories inside", nil),
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			path := args[0]
			absPath, err := filepath.Abs(path)
			if err != nil {
				ErrorMsg(l10n.T("Failed to resolve path: {{.Err}}", map[string]any{"Err": err}))
				return
			}

			if _, err := os.Stat(absPath); os.IsNotExist(err) {
				ErrorMsg(l10n.T("Directory does not exist: {{.Path}}", map[string]any{"Path": absPath}))
				return
			}

			cfg := GetConfig()
			for _, existing := range cfg.ParentPaths {
				if existing == absPath {
					ErrorMsg(l10n.T("Parent directory already exists: {{.Path}}", map[string]any{"Path": absPath}))
					return
				}
			}

			cfg.ParentPaths = append(cfg.ParentPaths, absPath)
			if err := config.SaveConfig(cfg); err != nil {
				ErrorMsg(l10n.T("Failed to save the configuration: {{.Err}}", map[string]any{"Err": err}))
				return
			}

			SuccessMsg(l10n.T("Parent directory added: {{.Path}}", map[string]any{"Path": absPath}))
		},
	}
	return c
}

func init() {
	register(func(root *cobra.Command) { root.AddCommand(newRepoCmd()) })
}
