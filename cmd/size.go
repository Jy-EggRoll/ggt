package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"ggt/internal/git"
	"ggt/internal/worker"
	"ggt/pkg/l10n"
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
func newSizeCmd() *cobra.Command {
	c := &cobra.Command{
		Use: "size",
		// 描述直接写英文原文：它同时也是 i18n 的消息 id，缺失中文译文时回退为英文原文。
		// 命令树在语言加载之后才构造（见 registry.go），所以这里的 T() 是真调用，
		// 不存在"包级变量求值过早、语言尚未确定"的问题。
		//
		// Long 这类多行原文必须保持"干净"：不能含制表符、行尾空白或首尾空行，
		// 否则源码重排会让 id 跟着变，既有译文会静默失效。提取器会强制这一点。
		Short: l10n.T("Show size statistics for all repositories", nil),
		Long: l10n.T(`Iterate over all configured repositories and show each one's size statistics.

When finished, repositories are bucketed by size: below the lower bound, between
the bounds, and above the upper bound, with repository names listed per bucket.
Thresholds and the conversion unit are described in the flags below.

Examples:
  ggt size          Show sizes and bucket statistics for all repositories
  ggt sz            Short form
  ggt size --low 200 --high 600 --unit binary  Custom thresholds and unit`, nil),
		Run: func(cmd *cobra.Command, args []string) {
			repos := AllRepos(context.Background())
			// 这里刻意保留常量格式串 "%s\n" 而不是改用 InfoMsg：pterm 的 Sprintfln 会在
			// 渲染结果之后再加一个换行，而 InfoMsg 走的 Println 会把结尾换行折叠掉，
			// 两者视觉上相差一个空行。用常量格式串可与改造前的输出保持完全一致
			InfoLn(l10n.T("Repositories: {{.Count}} — gathering sizes...", map[string]any{"Count": len(repos)}))

			width := pterm.GetTerminalWidth()
			t := NewDebugTimer(l10n.T("Size stats (repositories: {{.Count}})", map[string]any{"Count": len(repos)}))
			results := worker.Map(context.Background(), repos, Concurrency(), func(ctx context.Context, e RepoEntry) repoSizeResult {
				return showRepoSize(ctx, e, width)
			})
			t.Done()

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
			InfoMsg(l10n.T("Total size: {{.Size}}", map[string]any{"Size": formatSize(totalSize)}))

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
				// 走 Msg 系列而非 Warnf：译文已由 T 渲染完毕，套 "%s" 只是多余的间接层。
				// 译文里的 "decimal" 是配置枚举值，刻意保留原文，便于用户对照配置文件里的 size_unit
				WarnMsg(l10n.T(`Invalid size_unit value ("{{.Unit}}"), falling back to "decimal"`, map[string]any{"Unit": unit}))
				unit = "decimal"
			}

			small, mid, large := classifyBySize(results, low, high, unit)
			// 用 switch 而不是在消息 id 上做拼接（如 T("unit_"+unit)）：语义更明确，
			// 也不会因将来新增配置取值而取到意料之外的文案
			var unitLabel string
			switch unit {
			case "binary":
				unitLabel = l10n.T("binary MB (1 MB = 1024×1024 bytes, i.e. MiB)", nil)
			default:
				unitLabel = l10n.T("decimal MB (1 MB = 1,000,000 bytes)", nil)
			}
			// 单位说明与标题合成单一完整模板，让译者能调整括号形态
			Header(l10n.T("Size buckets ({{.Unit}})", map[string]any{"Unit": unitLabel}))
			// 不同分桶使用不同视觉级别：小仓库信息展示，中等仓库黄色警告，大仓库红色警告
			// 标题先经 T 渲染，故这里传 Msg 系列（纯文本通道）而非 f 系列
			printSizeBucket(l10n.T("<{{.Low}}MB", map[string]any{"Low": low}), small, InfoMsg)
			printSizeBucket(l10n.T("{{.Low}}~{{.High}}MB", map[string]any{"Low": low, "High": high}), mid, WarnMsg)
			printSizeBucket(l10n.T(">{{.High}}MB", map[string]any{"High": high}), large, ErrorMsg)
		},
	}

	c.Aliases = []string{"sz"}

	// 阈值与换算口径：flag 优先于配置文件，仅本次生效、不写入 JSON。
	// 语义与根命令 -c 相同：默认 0/空字符串表示"未指定"
	c.Flags().IntVar(&sizeLow, "low", 0, l10n.T("Lower bucket bound in MB (defaults to the size_bucket_low_mb config value)", nil))
	c.Flags().IntVar(&sizeHigh, "high", 0, l10n.T("Upper bucket bound in MB (defaults to the size_bucket_high_mb config value)", nil))
	c.Flags().StringVar(&sizeUnit, "unit", "", l10n.T("MB conversion unit used for buckets: decimal or binary (defaults to the size_unit config value)", nil))

	return c
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
			// 入参以 \n 结尾：pterm 会把结尾换行折叠为单个换行，于是这行就是普通的单行告警
			output: WarnStrLn(l10n.T("Repository {{.Path}}: command failed", map[string]any{"Path": e.Path})),
			size:   0,
			ok:     false,
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
	// 制表符留在 Go 侧而不写进文案：它是排版手段而非文案内容，混进消息里既会让译者
	// 困惑，也会让这个不可见字符成为消息 id 的一部分（提取器会拒绝含 \t 的消息）。
	// 注意制表位对齐依赖标签的显示宽度，中文"磁盘占用"（8 列）与英文 "Disk usage"
	// （10 列）恰好都落在同一制表位上；若将来新增语言导致错位，需改为显式列宽填充
	if v, ok := info["size"]; ok {
		b.WriteString("  ")
		b.WriteString(pterm.FgGreen.Sprint(l10n.T("Disk usage", nil) + "\t"))
		b.WriteString(v)
		b.WriteByte('\n')
	}
	if v, ok := info["size-pack"]; ok {
		b.WriteString("  ")
		b.WriteString(pterm.FgGreen.Sprint(l10n.T("Package size", nil) + "\t"))
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
// title 必须是调用方经 T 渲染好的纯文本，本函数不再做任何格式化。
//
// printer 决定标题的视觉级别：InfoMsg 浅蓝信息、WarnMsg 黄色警告、ErrorMsg 红色错误，
// 由调用方根据分桶的严重程度传入，保持列表项样式统一。
// 这里传 Msg 系列而非 f 系列：标题已由 go-i18n 渲染完毕，若再走 Sprintf 通道，
// 译文里出现的字面 % 会被 fmt 当成格式动词解析成 %!?(MISSING)。
func printSizeBucket(title string, names []string, printer func(string)) {
	printer(l10n.T("{{.Title}}: {{.Count}}", map[string]any{"Title": title, "Count": len(names)}))
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
// 支持的单位（参考 git count-objects -vH 官方输出，前缀 i 为二进制、无 i 为十进制）：
// bytes、KiB/KB、MiB/MB、GiB/GB。
func parseSizeValue(s string) int64 {
	// 先检测单位，再去除单位字符。二进制（KiB/MiB/GiB）按 1024 进位，
	// 十进制（KB/MB/GB）按 1000 进位，兼容 git 未来改用无 i 单位的输出格式。
	mult := int64(1)
	switch {
	case strings.Contains(s, "KiB"):
		mult = 1024
	case strings.Contains(s, "MiB"):
		mult = 1024 * 1024
	case strings.Contains(s, "GiB"):
		mult = 1024 * 1024 * 1024
	case strings.Contains(s, "KB"):
		mult = 1000
	case strings.Contains(s, "MB"):
		mult = 1000 * 1000
	case strings.Contains(s, "GB"):
		mult = 1000 * 1000 * 1000
	}

	s = strings.ReplaceAll(s, "bytes", "")
	s = strings.ReplaceAll(s, "KiB", "")
	s = strings.ReplaceAll(s, "MiB", "")
	s = strings.ReplaceAll(s, "GiB", "")
	s = strings.ReplaceAll(s, "KB", "")
	s = strings.ReplaceAll(s, "MB", "")
	s = strings.ReplaceAll(s, "GB", "")
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
	register(func(root *cobra.Command) { root.AddCommand(newSizeCmd()) })
}
