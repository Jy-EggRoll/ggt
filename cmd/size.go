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
// ok 标记该仓库是否成功取得大小：失败的仓库 size 为 0，
// 必须在分桶统计时排除，否则会被错误归入"<下界"桶。
type repoSizeResult struct {
	name        string
	isSubmodule bool
	output      string
	size        int64
	ok          bool
}

// sizeLow、sizeHigh、sizeUnit 是 size 命令的命令行覆盖参数。
// 约定与根命令的 -c 一致：仅当显式指定时才覆盖配置文件里的值，且不持久化。
// sizeLow/sizeHigh 默认 0 表示"未指定"；sizeUnit 默认空字符串表示"未指定"。
var (
	sizeLow  int
	sizeHigh int
	sizeUnit string
)

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

统计完成后会按大小分桶：小于下界、介于上下界之间、大于上界，
并分别列出各桶内的仓库名。分桶阈值与换算口径见下方参数说明。

使用示例:
  ggt size          显示所有仓库大小并输出分桶统计
  ggt sz          简写形式
  ggt size --low 200 --high 600 --unit binary  自定义阈值与口径`,
	Run: func(cmd *cobra.Command, args []string) {
		repos := MustGetAllRepos(context.Background(), GetConfig().IgnoreSubmodules)
		Infof("共 %d 个仓库，开始统计大小...\n", len(repos))

		width := pterm.GetTerminalWidth()
		results := worker.Map(context.Background(), repos, GetConfig().ConcurrencyValue(), func(ctx context.Context, e RepoEntry) repoSizeResult {
			return showRepoSize(ctx, e, width)
		})

		// 顺序打印各仓库的大小信息
		for _, r := range results {
			PrintRaw(r.output)
		}

		// 汇总计算总大小
		var totalSize int64
		for _, r := range results {
			totalSize += r.size
		}

		pterm.Println()
		Infof("总大小: %s", formatSize(totalSize))

		// 分桶统计：命令行 flag 优先于配置文件，未指定时取配置默认值
		low := GetConfig().SizeBucketLowMB
		if sizeLow > 0 {
			low = sizeLow
		}
		high := GetConfig().SizeBucketHighMB
		if sizeHigh > 0 {
			high = sizeHigh
		}
		unit := GetConfig().SizeUnit
		if sizeUnit != "" {
			unit = sizeUnit
		}
		if unit != "decimal" && unit != "binary" {
			Warnf("size_unit 配置无效（%q），已回退为 decimal", unit)
			unit = "decimal"
		}

		small, mid, large := classifyBySize(results, low, high, unit)
		unitLabel := "十进制 MB (1 MB = 1,000,000 字节)"
		if unit == "binary" {
			unitLabel = "二进制 MB (1 MB = 1024×1024 字节，即 MiB)"
		}
		Header("大小分桶统计（" + unitLabel + "）")
		printSizeBucket(fmt.Sprintf("<%dMB", low), small)
		printSizeBucket(fmt.Sprintf("%d~%dMB", low, high), mid)
		printSizeBucket(fmt.Sprintf(">%dMB", high), large)
	},
}

// showRepoSize 分析单个仓库的大小并返回格式化结果。
// 从 git count-objects -vH 的输出中提取关键字段，
// 主要展示"磁盘占用"和"包文件大小"两个核心指标。
// 接收上层 ctx 以便任务被整体取消时立即中断 git 调用。
func showRepoSize(ctx context.Context, e RepoEntry, width int) repoSizeResult {
	output, err := git.RunContext(ctx, e.Path, "count-objects", "-vH")
	if err != nil {
		return repoSizeResult{
			name:        e.Name,
			isSubmodule: e.IsSubmodule,
			output:      WarnS("仓库 %s: 执行失败\n", e.Path),
			size:        0,
			ok:          false,
		}
	}

	// 解析 git count-objects 的输出为键值映射
	info := parseSizeOutput(output)
	label := RepoLabel(e.Name, e.IsSubmodule)
	var b strings.Builder

	// 终端宽度分割线 + 仓库名（统一经 buildSeparator / RepoLabel 着色，子模块带 [子] 前缀）
	b.WriteString(buildSeparator(width))
	b.WriteByte('\n')
	b.WriteString(label)
	b.WriteByte('\n')

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
		b.WriteString(Muted("  " + strings.Join(parts, " | ")))
		b.WriteByte('\n')
	}

	return repoSizeResult{
		name:        e.Name,
		isSubmodule: e.IsSubmodule,
		output:      b.String(),
		size:        calcTotalBytes(info),
		ok:          true,
	}
}

// bytesToMB 将字节数按指定口径换算为 MB 数值。
// unit 为 "decimal" 时 1 MB = 1,000,000 字节；为 "binary" 时 1 MB = 1024×1024 字节（即 MiB）。
// 纯函数，不依赖任何外部状态，便于单元测试。
func bytesToMB(b int64, unit string) float64 {
	if unit == "binary" {
		return float64(b) / (1024 * 1024)
	}
	return float64(b) / 1e6
}

// classifyBySize 按大小将所有成功统计的仓库分入三桶：
//   - small: MB < lowMB
//   - mid:   lowMB <= MB <= highMB
//   - large: MB > highMB
//
// 失败的仓库（ok == false）直接跳过，不会因 size 为 0 而被误归入 small。
// 仓库名统一经 RepoLabel 着色（子模块带 [子] 前缀），保持与详情输出一致。
// 返回的切片保持 results 原有的顺序。
func classifyBySize(results []repoSizeResult, lowMB, highMB int, unit string) (small, mid, large []string) {
	for _, r := range results {
		if !r.ok {
			continue
		}
		mb := bytesToMB(r.size, unit)
		switch {
		case mb < float64(lowMB):
			small = append(small, RepoLabel(r.name, r.isSubmodule))
		case mb > float64(highMB):
			large = append(large, RepoLabel(r.name, r.isSubmodule))
		default:
			mid = append(mid, RepoLabel(r.name, r.isSubmodule))
		}
	}
	return
}

// printSizeBucket 打印单个分桶：标题（含数量）+ 缩进列出每个仓库名。
func printSizeBucket(title string, names []string) {
	Infof("%s：%d 个", title, len(names))
	for _, n := range names {
		ListItem(n)
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

	// 阈值与换算口径：flag 优先于配置文件，仅本次生效、不写入 JSON。
	// 语义与根命令 -c 相同：默认 0/空字符串表示"未指定"。
	sizeCmd.Flags().IntVar(&sizeLow, "low", 0, "分桶下界阈值（MB），省略时取配置文件 size_bucket_low_mb")
	sizeCmd.Flags().IntVar(&sizeHigh, "high", 0, "分桶上界阈值（MB），省略时取配置文件 size_bucket_high_mb")
	sizeCmd.Flags().StringVar(&sizeUnit, "unit", "", "分桶 MB 换算口径：decimal 或 binary，省略时取配置文件 size_unit")
}
