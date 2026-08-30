// files.go 实现 "ggt files" 命令，列出所有已入库仓库的文件列表。
// 使用 worker.Map 并发获取每个仓库的 git ls-files 输出，
// 支持控制台输出（默认）或写入文件（-o 参数）。
package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"ggt/internal/git"
	"ggt/internal/worker"
	"github.com/spf13/cobra"
)

// filesOutput 保存单个仓库的文件列表结果。
// files 是该仓库的所有文件路径列表（相对仓库根目录），
// err 记录获取失败时的错误信息，用于跳过失败仓库。
type filesOutput struct {
	name        string
	isSubmodule bool
	files       []string
	err         error
}

// filesCmd 实现 "ggt files"（简写 ggt fl）。
// 并发获取所有仓库的文件列表，支持输出到控制台或文件。
// 输出格式统一为 "[仓库名] 文件路径"，每个文件一行。
var filesCmd = &cobra.Command{
	Use:   "files",
	Short: "显示所有仓库的文件列表",
	Long: `遍历所有已配置的仓库，列出每个仓库的文件列表。

使用示例:
  ggt files              显示所有仓库的文件列表
  ggt fl                 简写形式
  ggt files -o out.txt   将文件列表写入 out.txt`,
	Run: func(cmd *cobra.Command, args []string) {
		repos := MustGetAllRepos(context.Background(), GetConfig().IgnoreSubmodules)
		Infof("共 %d 个仓库，开始获取文件列表...\n", len(repos))

		// 使用 worker.Map 并发获取每个仓库的文件列表
		t := NewDebugTimer(fmt.Sprintf("文件列表 (%d 个仓库)", len(repos)))
		results := worker.Map(context.Background(), repos, GetConfig().ConcurrencyValue(), showRepoFiles)
		t.Done()

		// 构建输出内容：格式为 "[仓库名] 文件路径"
		var output strings.Builder
		for _, r := range results {
			if r.err != nil {
				Warnf("仓库 %s: 获取文件列表失败 - %v\n", r.name, r.err)
				continue
			}
			for _, file := range r.files {
				output.WriteString(fmt.Sprintf("[%s] %s\n", r.name, file))
			}
		}

		// 根据 -o 参数决定输出到文件或控制台
		if outputFile != "" {
			if err := os.WriteFile(outputFile, []byte(output.String()), 0644); err != nil {
				Errorf("写入文件失败: %v\n", err)
				return
			}
			Successf("文件列表已写入: %s\n", outputFile)
		} else {
			fmt.Print(output.String())
		}
	},
}

// showRepoFiles 获取单个仓库的文件列表。
// 使用 git ls-files --cached --others --exclude-standard 命令，
// 包含已跟踪文件、未跟踪文件（排除 .gitignore 中的文件）。
// 接收上层 ctx 以便任务被整体取消时立即中断 git 调用。
func showRepoFiles(ctx context.Context, e RepoEntry) filesOutput {
	output, err := git.RunContext(ctx, e.Path, "ls-files", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return filesOutput{
			name:        e.Name,
			isSubmodule: e.IsSubmodule,
			files:       nil,
			err:         err,
		}
	}

	// 处理空输出（仓库无文件）
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return filesOutput{
			name:        e.Name,
			isSubmodule: e.IsSubmodule,
			files:       []string{},
			err:         nil,
		}
	}

	files := strings.Split(trimmed, "\n")
	return filesOutput{
		name:        e.Name,
		isSubmodule: e.IsSubmodule,
		files:       files,
		err:         nil,
	}
}

// outputFile 是 -o 参数指定的输出文件路径。
// 默认为空字符串表示输出到控制台。
var outputFile string

func init() {
	rootCmd.AddCommand(filesCmd)
	filesCmd.Aliases = []string{"fl"}
	filesCmd.Flags().StringVarP(&outputFile, "output", "o", "", "将文件列表写入指定文件（省略时输出到控制台）")
}
