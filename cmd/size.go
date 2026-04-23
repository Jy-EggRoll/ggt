package cmd

import (
	"strconv"
	"strings"
	"sync"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

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

		cfg := GetConfig()

		type result struct {
			path string
			size int64
		}
		resultChan := make(chan result, len(repos))
		var wg sync.WaitGroup
		sem := make(chan struct{}, cfg.Concurrency)

		for _, repoPath := range repos {
			wg.Add(1)
			go func(path string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				size := getRepoSize(path)
				resultChan <- result{path: path, size: size}
			}(repoPath)
		}

		wg.Wait()
		close(resultChan)

		var totalSize int64
		for r := range resultChan {
			totalSize += r.size
		}

		pterm.Println()
		pterm.Info.Printf("总大小: %s\n", formatSize(totalSize))
	},
}

func getRepoSize(repoPath string) int64 {
	output, err := RunGitCommand(repoPath, "--no-pager", "-c", "color.status=never", "count-objects", "-vH")
	if err != nil {
		pterm.Warning.Printf("仓库 %s: 执行失败\n", repoPath)
		return 0
	}

	outStr := output
	size := parseSizeOutput(outStr)

	name := getRepoName(repoPath)
	pterm.FgCyan.Printf("[%s]\n", name)
	for _, line := range strings.Split(outStr, "\n") {
		if strings.TrimSpace(line) != "" {
			pterm.Println(line)
		}
	}

	return size
}

func parseSizeOutput(output string) int64 {
	var total int64
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "size:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				// parts[1] is "44.00", parts[2] is "KiB" - need to join them
				valueStr := strings.Join(parts[1:], " ")
				total += parseSizeValue(valueStr)
			}
		}
		if strings.HasPrefix(line, "size-pack:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				valueStr := strings.Join(parts[1:], " ")
				total += parseSizeValue(valueStr)
			}
		}
	}
	return total
}

func parseSizeValue(s string) int64 {
	// First check for multiplier BEFORE modifying the string
	mult := int64(1)
	if strings.Contains(s, "KiB") {
		mult = 1024
	} else if strings.Contains(s, "MiB") {
		mult = 1024 * 1024
	} else if strings.Contains(s, "GiB") {
		mult = 1024 * 1024 * 1024
	}

	// Now remove the unit
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

func formatSize(size int64) string {
	const unit = int64(1024)
	if size < unit {
		return pterm.FgYellow.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	if exp > 3 {
		exp = 3
	}
	return pterm.FgYellow.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

func init() {
	rootCmd.AddCommand(sizeCmd)
	sizeCmd.Aliases = []string{"sz"}
}
