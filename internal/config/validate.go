// validate.go 实现 ggt config validate 的体检逻辑。
//
// 体检与运行期加载共用注册表的 Normalize，因此“体检说没问题、运行期却拒绝”或反过来
// 都不可能发生。体检的增量职责是把运行期能容忍、却会影响结果合理性的东西也说出来
// （阈值倒置、路径不存在、重复条目等），并给每条问题标出严重级别：
// error 表示运行期加载会直接失败，warning 表示命令能跑但结果可能不是用户想要的。
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/ggt/internal/git"
)

// Level 是体检问题的严重级别。
type Level string

const (
	// LevelError 表示运行期加载会直接失败的问题：配置文件里这么写，任何命令都起不来。
	LevelError Level = "error"
	// LevelWarning 表示运行期能容忍、或只影响结果合理性的问题。
	LevelWarning Level = "warning"
)

// Issue 是一条体检结果。Message 已是当前语言的文案。
type Issue struct {
	Level   Level
	Message string
}

// utf8BOM 是 UTF-8 字节序标记。
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// ValidateAt 体检指定路径的配置文件。
//
// 返回的 error 只用于“连读都读不了”（权限不足、路径是目录等）——那种情况无从体检。
// 文件不存在返回空清单：默认配置本来就允许不存在，不算问题。
func ValidateAt(path string) ([]Issue, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var issues []Issue
	// UTF-8 BOM：Windows 记事本“另存为 UTF-8”默认会写。带 BOM 的文件 Go 的
	// encoding/json 与 viper 都会解析失败，而报错信息完全看不出是 BOM 引起的，
	// 属于极难自查的一类，必须单独指出来
	if bytes.HasPrefix(data, utf8BOM) {
		issues = append(issues, Issue{
			Level:   LevelError,
			Message: l10n.T("The config file starts with a UTF-8 BOM, which breaks JSON parsing; re-save it as UTF-8 without BOM", nil),
		})
		data = bytes.TrimPrefix(data, utf8BOM)
	}

	raw, err := parseRaw(data)
	if err != nil {
		issues = append(issues, Issue{
			Level:   LevelError,
			Message: l10n.T("Cannot parse the config file: {{.Err}}", map[string]any{"Err": err}),
		})
		return issues, nil
	}

	// 逐项规范化一次，后续检查直接使用规范值：桶阈值写成字符串 "500" 也要参与倒置判定，
	// 路径列表的元素类型与空串问题也不该在体检里再判一遍
	normalized := make(map[string]any, len(raw))
	issues = append(issues, validateKeys(raw, normalized)...)
	issues = append(issues, validateBuckets(normalized)...)
	issues = append(issues, validatePaths(normalized)...)
	return issues, nil
}

// validateKeys 逐项检查取值，并指出未知键。
//
// 每一项都走与运行期相同的 Setting.Normalize；规范化失败就是 error，因为运行期加载
// 也会在这里失败。不再有“类型不规范但还能跑”这一档：那种说法在本次改造后已经不成立。
func validateKeys(raw map[string]any, normalized map[string]any) []Issue {
	var issues []Issue

	known := make(map[string]bool, len(settings))
	for _, s := range settings {
		known[s.Key] = true
	}

	// 未知键多半是拼写错误。运行期完全静默地忽略它们，用户会以为设上了
	unknown := make([]string, 0, len(raw))
	for k := range raw {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown) // 排序保证输出稳定，便于 diff 与脚本处理
	for _, k := range unknown {
		issues = append(issues, Issue{
			Level:   LevelError,
			Message: l10n.T("Unknown config key {{.Key}} (possible typo); ggt silently ignores it", map[string]any{"Key": k}),
		})
	}

	for _, s := range settings {
		v, ok := raw[s.Key]
		if !ok {
			continue // 缺键不是问题：缺键就用默认值
		}

		value, err := normalizeValue(s, v)
		if err != nil {
			// 废弃的旧语义值是“运行期会被替换成默认值”的一类，但它并不阻断命令：
			// 报成 error 会让升级后的第一次 validate 直接失败，与运行期实际能跑的事实不符
			if errors.Is(err, errDeprecatedConcurrency) {
				issues = append(issues, Issue{
					Level: LevelWarning,
					Message: l10n.T("The concurrency value {{.Value}} is deprecated and now behaves like auto; change it to auto or an integer",
						map[string]any{"Value": ValueText(v)}),
				})
				continue
			}
			issues = append(issues, Issue{Level: LevelError, Message: err.Error()})
			continue
		}
		normalized[s.Key] = value
	}
	return issues
}

// validateBuckets 检查分桶阈值区间的合理性。
//
// 阈值倒置时 classifyBySize 的三段区间会自相矛盾（low=1000、high=800 时 900MB 落中桶），
// 不崩溃但结果毫无意义。
func validateBuckets(normalized map[string]any) []Issue {
	low, okLow := normalized["size_bucket_low_mb"].(int)
	high, okHigh := normalized["size_bucket_high_mb"].(int)
	if !okLow || !okHigh {
		return nil // 缺键或取值非法时前面已报过
	}
	if low >= high {
		return []Issue{{
			Level: LevelWarning,
			Message: l10n.T("size_bucket_low_mb ({{.Low}}) is not less than size_bucket_high_mb ({{.High}}), so the buckets are meaningless",
				map[string]any{"Low": low, "High": high}),
		}}
	}
	return nil
}

// validatePaths 检查仓库路径与父目录的可达性。
//
// 判定口径必须与运行期一致（git.IsRepo），否则会出现“体检说不合法、ggt 却能跑”的错位。
func validatePaths(normalized map[string]any) []Issue {
	var issues []Issue
	issues = append(issues, checkPathList("repo_paths", normalized["repo_paths"], true)...)
	issues = append(issues, checkPathList("parent_paths", normalized["parent_paths"], false)...)
	return issues
}

// checkPathList 检查一个路径列表。mustBeRepo 为 true 时还要求每项是 git 仓库。
func checkPathList(key string, v any, mustBeRepo bool) []Issue {
	list, ok := v.([]string)
	if !ok {
		return nil // 缺键或取值非法时前面已报过
	}

	var issues []Issue
	seen := make(map[string]bool, len(list))
	for _, p := range list {
		if seen[p] {
			issues = append(issues, Issue{
				Level:   LevelWarning,
				Message: l10n.T("Duplicate entry in {{.Key}}: {{.Path}}", map[string]any{"Key": key, "Path": p}),
			})
			continue
		}
		seen[p] = true

		if !filepath.IsAbs(p) {
			issues = append(issues, Issue{
				Level: LevelWarning,
				Message: l10n.T("Relative path in {{.Key}}: {{.Path}} — it is resolved against the current directory at runtime, so results vary",
					map[string]any{"Key": key, "Path": p}),
			})
			continue
		}

		info, err := os.Stat(p)
		if err != nil {
			issues = append(issues, Issue{
				Level:   LevelWarning,
				Message: l10n.T("Path in {{.Key}} does not exist: {{.Path}}", map[string]any{"Key": key, "Path": p}),
			})
			continue
		}

		switch {
		case mustBeRepo && !git.IsRepo(p):
			issues = append(issues, Issue{
				Level:   LevelWarning,
				Message: l10n.T("Path in repo_paths is not a git repository: {{.Path}}", map[string]any{"Path": p}),
			})
		case !mustBeRepo && !info.IsDir():
			issues = append(issues, Issue{
				Level:   LevelWarning,
				Message: l10n.T("Path in parent_paths is not a directory: {{.Path}}", map[string]any{"Path": p}),
			})
		}
	}
	return issues
}

// ValueText 把配置值（JSON 解码后的形态）转成纯文本，去掉 JSON 的引号与类型包装。
//
// 用于把规范值或原始值转成可读文本：config get 的标量输出、设置面板的取值展示，
// 以及“取值非法”报错里回显用户写下的内容。校验本身直接吃原始值，不再经过文本，
// 因此类型信息不会在转换中丢失。
func ValueText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case json.Number:
		return t.String()
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// jsonTypeName 返回值的 JSON 类型名，用于提示用户。
func jsonTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "bool"
	case json.Number, float64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}
