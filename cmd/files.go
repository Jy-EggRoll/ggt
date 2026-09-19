// files.go 实现 "ggt files" 命令，列出所有已入库仓库的文件列表。
// 使用 worker.Map 并发获取每个仓库的 git ls-files 输出，
// 支持控制台输出（默认）或写入文件（-o 参数）。
package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ggt/internal/git"
	"ggt/internal/i18n"
	"ggt/internal/worker"
	"github.com/spf13/cobra"
)

// filesOutput 保存单个仓库的文件列表结果。
// path 是仓库的绝对路径，用于拼接完整文件路径；
// files 是该仓库的所有文件路径列表（相对仓库根目录），
// err 记录获取失败时的错误信息，用于跳过失败仓库。
type filesOutput struct {
	path  string
	files []string
	err   error
}

// filesCmd 实现 "ggt files"（简写 ggt fl）。
// 并发获取所有仓库的文件列表，支持输出到控制台或文件。
// 输出格式为每行一个完整路径（仓库绝对路径文件路径）。
func newFilesCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "files",
		Short: i18n.T("Show the file list of all repositories", nil),
		Long: i18n.T(`Iterate over all configured repositories and list the files of each.

Examples:
  ggt files              Show the file list of all repositories
  ggt fl                 Short form
  ggt files -o out.txt   Write the file list to out.txt`, nil),
		Run: func(cmd *cobra.Command, args []string) {
			repos := MustGetAllRepos(context.Background(), GetConfig().IgnoreSubmodules)
			// 保留常量格式串 "%s\n" 以维持改造前的尾部空行
			Infof("%s\n", i18n.T("Repositories: {{.Count}} — gathering file lists...", map[string]any{"Count": len(repos)}))

			// 使用 worker.Map 并发获取每个仓库的文件列表
			t := NewDebugTimer(i18n.T("File lists (repositories: {{.Count}})", map[string]any{"Count": len(repos)}))
			results := worker.Map(context.Background(), repos, GetConfig().ConcurrencyValue(), showRepoFiles)
			t.Done()

			// 构建输出内容：每行为仓库完整路径与文件路径拼接的合法路径
			var output strings.Builder
			for _, r := range results {
				if r.err != nil {
					// 原文案以 \n 结尾且走 Printfln，会多出一个空行；用常量格式串 "%s\n" 保持等价
					Warnf("%s\n", i18n.T("Repository {{.Path}}: failed to list files - {{.Err}}",
						map[string]any{"Path": r.path, "Err": r.err}))
					continue
				}
				for _, file := range r.files {
					output.WriteString(filepath.Join(r.path, file) + "\n")
				}
			}

			// 根据 -o 参数决定输出到文件或控制台
			if outputFile != "" {
				if err := os.WriteFile(outputFile, []byte(output.String()), 0644); err != nil {
					Errorf("%s\n", i18n.T("Failed to write file: {{.Err}}", map[string]any{"Err": err}))
					return
				}
				Successf("%s\n", i18n.T("File list written to: {{.Path}}", map[string]any{"Path": outputFile}))
			} else {
				fmt.Print(output.String())
			}
		},
	}
	c.Aliases = []string{"fl"}
	c.Flags().StringVarP(&outputFile, "output", "o", "",
		i18n.T("Write the file list to this file (prints to the console when omitted)", nil))
	return c
}

// showRepoFiles 获取单个仓库的文件列表。
// 使用 git ls-files --cached --others --exclude-standard 命令，
// 包含已跟踪文件、未跟踪文件（排除 .gitignore 中的文件）。
// 接收上层 ctx 以便任务被整体取消时立即中断 git 调用。
func showRepoFiles(ctx context.Context, e RepoEntry) filesOutput {
	output, err := git.RunContext(ctx, e.Path, "ls-files", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return filesOutput{
			path:  e.Path,
			files: nil,
			err:   err,
		}
	}

	// 处理空输出（仓库无文件）
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return filesOutput{
			path:  e.Path,
			files: []string{},
			err:   nil,
		}
	}

	files := strings.Split(trimmed, "\n")
	return filesOutput{
		path:  e.Path,
		files: files,
		err:   nil,
	}
}

// outputFile 是 -o 参数指定的输出文件路径。
// 默认为空字符串表示输出到控制台。
var outputFile string

func init() {
	register(func(root *cobra.Command) { root.AddCommand(newFilesCmd()) })
}
