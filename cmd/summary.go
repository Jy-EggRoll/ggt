package cmd

import (
	"fmt"
	"strings"
	"time"

	"ggt/internal/git"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

// summaryCmd 实现 "ggt summary"（简写 ggt sum）。
// 遍历所有已配置的仓库，对有变更的仓库：
// 1. 显示 git status 和 diff stat
// 2. 交互式询问是否一键提交并推送
// 3. 执行 git add -A → git commit → git push
// 4. 显示推送后的仓库大小变化
//
// 关键修复：使用 git.RunCombined 捕获 stderr，推送失败时显示具体错误原因。
// 注意：此命令是顺序执行的（交互式确认需要等待用户输入），没有并发问题。
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

		for _, repoPath := range repos {
			// 检查是否有未提交的变更
			statusOutput, err := git.Run(repoPath, "-c", "color.status=always", "status", "--short", "--branch", "--untracked-files")
			if err != nil {
				continue
			}

			// 过滤空行，判断除了分支信息外是否有实际变更
			lines := strings.Split(strings.TrimRight(statusOutput, "\n"), "\n")
			var nonEmpty []string
			for _, line := range lines {
				if strings.TrimSpace(line) != "" {
					nonEmpty = append(nonEmpty, line)
				}
			}
			// 只有分支行（## main...origin/main）说明没有变更
			if len(nonEmpty) <= 1 {
				continue
			}

			// 显示变更概览
			name := getRepoName(repoPath)
			pterm.FgLightYellow.Println(strings.Repeat("─", pterm.GetTerminalWidth()))
			pterm.FgCyan.Printfln("%s 检测到变动", name)
			fmt.Print(statusOutput)

			// 显示详细的 diff 统计
			fmt.Printf("%s\n", pterm.FgCyan.Sprint("变动详情："))
			diffOutput, _ := git.Run(repoPath, "diff", "--color=always", "--stat")
			if diffOutput != "" {
				fmt.Print(diffOutput)
			}

			// 交互式确认：是否一键提交并推送
			result, _ := pterm.DefaultInteractiveConfirm.WithDefaultValue(false).WithDefaultText("是否一键提交所有更改并推送？").Show()
			if !result {
				continue
			}

			pterm.FgYellow.Printfln("正在处理 %s ...", name)

			// git add -A：暂存所有更改
			if out, err := git.RunCombined(repoPath, "add", "-A"); err != nil {
				pterm.Error.Printf("git add 失败:\n%s\n", out)
				continue
			}

			// git commit：自动生成提交信息
			msg := fmt.Sprintf("🔨 chore: 终端推送更新 %s", time.Now().Format("2006-01-02 15:04:05"))
			if out, err := git.RunCombined(repoPath, "commit", "-m", msg); err != nil {
				pterm.Error.Printf("git commit 失败:\n%s\n", out)
				continue
			}

			// git push：推送到远程，使用 RunCombined 确保捕获 stderr 错误信息
			if out, err := git.RunCombined(repoPath, "push"); err != nil {
				pterm.Error.Printf("推送失败:\n%s\n", out)
			} else {
				pterm.Success.Println("推送完成！")
			}

			// 显示提交后的仓库大小信息
			fmt.Print("大小信息：\n")
			countOutput, _ := git.Run(repoPath, "count-objects", "-vH")
			if countOutput != "" {
				fmt.Print(countOutput)
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(summaryCmd)
	summaryCmd.Aliases = []string{"sum"}
}
