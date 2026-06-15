package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ggt/internal/git"
	"ggt/internal/worker"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

// dirtyRepo 记录检测到变更有待处理的仓库。
// hasUncommitted 标记是否存在未提交的文件变更（false 表示仅是本地 ahead 未 push）。
type dirtyRepo struct {
	path           string
	name           string
	statusOutput   string
	hasUncommitted bool
}

// summaryCmd 实现 "ggt summary"（简写 ggt sum）。
// 先并发检查所有仓库的状态，再对有变更的仓库进行交互式提交流程。
var summaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "遍历所有仓库，显示变更，询问一键提交",
	Long: `遍历所有仓库，显示变更，询问一键提交。

使用示例:
  ggt summary          查看变更并提交
  ggt sum             简写形式`,
	Run: func(cmd *cobra.Command, args []string) {
		repos := MustGetRepoList()
		pterm.Info.Printf("共 %d 个仓库，开始检查变更...\n\n", len(repos))

		// 第一阶段：并发检查所有仓库的 git 状态
		results := worker.Map(context.Background(), repos, GetConfig().Concurrency, func(repoPath string) *dirtyRepo {
			statusOutput, err := git.Run(repoPath, "-c", "color.status=always", "status", "--short", "--branch", "--untracked-files")
			if err != nil {
				return nil
			}

			lines := strings.Split(strings.TrimRight(statusOutput, "\n"), "\n")
			nonEmptyCount := 0
			for _, line := range lines {
				if strings.TrimSpace(line) != "" {
					nonEmptyCount++
				}
			}
			// 只有分支行说明没有文件变更，但需检查是否为 ahead（已 commit 未 push）
			if nonEmptyCount <= 1 {
				if !strings.Contains(statusOutput, "[ahead") {
					return nil
				}
				// 仅有 ahead，无未提交的文件变更
				return &dirtyRepo{
					path:           repoPath,
					name:           getRepoName(repoPath),
					statusOutput:   statusOutput,
					hasUncommitted: false,
				}
			}

			return &dirtyRepo{
				path:           repoPath,
				name:           getRepoName(repoPath),
				statusOutput:   statusOutput,
				hasUncommitted: true,
			}
		})

		// 第二阶段：顺序处理每个有变更的仓库（交互+提交需等待用户输入）
		for _, d := range results {
			if d == nil {
				continue
			}

			pterm.FgLightYellow.Println(strings.Repeat("─", pterm.GetTerminalWidth()))
			pterm.FgCyan.Printfln("%s 检测到变动", d.name)
			fmt.Print(d.statusOutput)

			// 显示详细的 diff 统计
			fmt.Printf("%s\n", pterm.FgCyan.Sprint("变动详情："))
			diffOutput, err := git.Run(d.path, "diff", "--color=always", "--stat")
			if err != nil {
				pterm.Warning.Printfln("获取 diff 失败: %v", err)
			} else if diffOutput != "" {
				fmt.Print(diffOutput)
			}

			// 交互式确认：根据仓库状态动态调整提示文案
			promptText := "是否一键提交所有更改并推送？"
			if !d.hasUncommitted {
				promptText = "是否推送已提交的更改？"
			}
			result, _ := pterm.DefaultInteractiveConfirm.WithDefaultValue(false).WithDefaultText(promptText).Show()
			if !result {
				continue
			}

			pterm.FgYellow.Printfln("正在处理 %s ...", d.name)

			// 仅在存在未提交的文件变更时执行 add + commit
			if d.hasUncommitted {
				// git add -A：暂存所有更改
				if out, err := git.RunCombined(d.path, "add", "-A"); err != nil {
					pterm.Error.Printf("git add 失败:\n%s\n", out)
					continue
				}

				// git commit：自动生成提交信息
				msg := fmt.Sprintf("🔨 chore: 终端推送更新 %s", time.Now().Format("2006-01-02 15:04:05"))
				if out, err := git.RunCombined(d.path, "commit", "-m", msg); err != nil {
					pterm.Error.Printf("git commit 失败:\n%s\n", out)
					continue
				}
			}

			// git push：推送到远程，使用 RunCombined 确保捕获 stderr 错误信息
			if out, err := git.RunCombined(d.path, "push"); err != nil {
				pterm.Error.Printf("推送失败:\n%s\n", out)
			} else {
				pterm.Success.Println("推送完成！")
			}

			// 显示提交后的仓库大小信息
			fmt.Print("大小信息：\n")
			countOutput, err := git.Run(d.path, "count-objects", "-vH")
			if err != nil {
				pterm.Warning.Printfln("获取大小信息失败: %v", err)
			} else if countOutput != "" {
				fmt.Print(countOutput)
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(summaryCmd)
	summaryCmd.Aliases = []string{"sum"}
}
