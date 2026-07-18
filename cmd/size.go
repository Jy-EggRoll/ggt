package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"ggt/internal/git"
	"ggt/internal/worker"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

// repoSizeResult 保存单个仓库的大小分析结果。
// output 是格式化后的终端输出字符串，size 是总字节数（用于最终汇总）。
type repoSizeResult struct {
	name   string
	output string
	size   int64
}

// sizeCmd 实现 "ggt size"（简写 ggt sz）。
// 并发统计所有仓库的 git 对象存储大小，突出显示两个关键指标：
//   - 磁盘占用（size）：git 对象的总磁盘占用量
//   - 包文件大小（size-pack）：打包后的大小
//
// 输出安全：worker.Map 并发收集 → 主 goroutine 顺序打印，无交错。
var sizeCmd = &cobra.Command{
	Use:   "size",
	Short: "显示所有仓库的大小统计信息",
	Long: `遍历所有已配置的仓库，显示每个仓库的大小统计信息。

使用示例:
  ggt size          显示所有仓库大小
  ggt sz          简写形式`,
	Run: func(cmd *cobra.Command, args []string) {
		repos := MustGetRepoList()
		pterm.Info.Printf("共 %d 个仓库，开始统计大小...\n\n", len(repos))

		width := pterm.GetTerminalWidth()
		results := worker.Map(context.Background(), repos, GetConfig().Concurrency, func(ctx context.Context, repoPath string) repoSizeResult {
			return showRepoSize(ctx, repoPath, width)
		})

		// 顺序打印各仓库的大小信息
		for _, r := range results {
			fmt.Print(r.output)
		}

		// 汇总计算总大小
		var totalSize int64
		for _, r := range results {
			totalSize += r.size
		}

		pterm.Println()
		Infof("总大小: %s", formatSize(totalSize))
	},
}

// showRepoSize 分析单个仓库的大小并返回格式化结果。
// 从 git count-objects -vH 的输出中提取关键字段，
// 主要展示"磁盘占用"和"包文件大小"两个核心指标。
// 接收上层 ctx 以便任务被整体取消时立即中断 git 调用。
func showRepoSize(ctx context.Context, repoPath string, width int) repoSizeResult {
	output, err := git.RunContext(ctx, repoPath, "count-objects", "-vH")
	if err != nil {
		return repoSizeResult{
			name:   getRepoName(repoPath),
			output: pterm.Warning.Sprintf("仓库 %s: 执行失败\n", repoPath),
			size:   0,
		}
	}

	// 解析 git count-objects 的输出为键值映射
	info := parseSizeOutput(output)
	name := getRepoName(repoPath)
	var b strings.Builder

	// 终端宽度分割线 + 仓库名
	b.WriteString(pterm.FgLightYellow.Sprint(strings.Repeat("─", width)))
	b.WriteByte('\n')
	b.WriteString(pterm.FgCyan.Sprintf("[%s]\n", name))

	// 主要指标：磁盘占用（size）和包文件大小（size-pack）
	if v, ok := info["size"]; ok {
		b.WriteString("  ")
		b.WriteString(pterm.FgGreen.Sprint("磁盘占用\t"))
		b.WriteString(v)
		b.WriteByte('\n')
	}
	if v, ok := info["size-pack"]; ok {
		b.WriteString("  ")
		b.WriteString(pterm.FgGreen.Sprint("包文件大小\t"))
		b.WriteString(v)
		b.WriteByte('\n')
	}

	// 次要指标：对象数、包内对象、包数、可裁剪、垃圾
	var parts []string
	for _, k := range []string{"count", "in-pack", "packs", "prune-packable", "garbage"} {
		if v, ok := info[k]; ok {
			parts = append(parts, fmt.Sprintf("%s: %s", k, v))
		}
	}
	if len(parts) > 0 {
		b.WriteString(pterm.FgGray.Sprint("  " + strings.Join(parts, " | ")))
		b.WriteByte('\n')
	}

	return repoSizeResult{
		name:   name,
		output: b.String(),
		size:   calcTotalBytes(info),
	}
}

// parseSizeOutput 将 git count-objects -vH 的输出解析为键值映射。
// 输入形如 "size: 80.85 MiB\nsize-pack: 65.98 MiB\ncount: 221\n..."
func parseSizeOutput(output string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, ":"); idx >= 0 {
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			if val != "" {
				result[key] = val
			}
		}
	}
	return result
}

// calcTotalBytes 从解析结果中计算磁盘占用的总字节数。
// 将 size 和 size-pack 的值相加，用于最终的总大小统计。
func calcTotalBytes(info map[string]string) int64 {
	var total int64
	for _, key := range []string{"size", "size-pack"} {
		if v, ok := info[key]; ok {
			total += parseSizeValue(v)
		}
	}
	return total
}

// parseSizeValue 将带单位的大小字符串转为字节数。
// 支持的单位: bytes, KiB, MiB, GiB
func parseSizeValue(s string) int64 {
	// 先检测单位，再去除单位字符
	mult := int64(1)
	if strings.Contains(s, "KiB") {
		mult = 1024
	} else if strings.Contains(s, "MiB") {
		mult = 1024 * 1024
	} else if strings.Contains(s, "GiB") {
		mult = 1024 * 1024 * 1024
	}

	s = strings.ReplaceAll(s, "bytes", "")
	s = strings.ReplaceAll(s, "KiB", "")
	s = strings.ReplaceAll(s, "MiB", "")
	s = strings.ReplaceAll(s, "GiB", "")
	s = strings.TrimSpace(s)

	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int64(val * float64(mult))
}

// formatSize 将字节数转为人类可读的大小字符串（如 985.7 MB）。
// 仅做数值格式化，不含颜色；颜色由调用处的 Infof 统一处理，
// 便于对纯文本结果做单元测试，也符合"格式化与展示分离"的原则。
func formatSize(size int64) string {
	const unit = int64(1024)
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	if exp > 3 {
		exp = 3
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

func init() {
	rootCmd.AddCommand(sizeCmd)
	sizeCmd.Aliases = []string{"sz"}
}
