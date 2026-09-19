// validate.go 实现 ggt config validate 的体检逻辑。
//
// 体检与运行期加载的容错策略刻意不同：LoadConfig 力求"永远能跑"，遇到非法值会静默
// 回退默认；体检力求"把会被静默忽略的东西说出来"。因此每条问题都标明严重级别，
// 并说明运行期会发生什么，用户才不会困惑于"为什么它说有问题但命令照样能跑"。
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"ggt/internal/git"
	"ggt/internal/i18n"
)

// Level 是体检问题的严重级别。
type Level string

const (
	// LevelError 表示运行期会被静默忽略、替换成默认值、或直接导致命令不可用的问题。
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
// 返回的 error 只用于"连读都读不了"（权限不足、路径是目录等）——那种情况无从体检。
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
	// UTF-8 BOM：Windows 记事本"另存为 UTF-8"默认会写。带 BOM 的文件 Go 的
	// encoding/json 与 viper 都会解析失败，而报错信息完全看不出是 BOM 引起的，
	// 属于极难自查的一类，必须单独指出来
	if bytes.HasPrefix(data, utf8BOM) {
		issues = append(issues, Issue{
			Level:   LevelError,
			Message: i18n.T("The config file starts with a UTF-8 BOM, which breaks JSON parsing; re-save it as UTF-8 without BOM", nil),
		})
		data = bytes.TrimPrefix(data, utf8BOM)
	}

	raw, err := parseRaw(data)
	if err != nil {
		issues = append(issues, Issue{
			Level:   LevelError,
			Message: i18n.T("Cannot parse the config file: {{.Err}}", map[string]any{"Err": err}),
		})
		return issues, nil
	}

	issues = append(issues, validateKeys(raw)...)
	issues = append(issues, validateBuckets(raw)...)
	issues = append(issues, validatePaths(raw)...)
	return issues, nil
}

// validateKeys 逐项检查取值，并指出未知键。
func validateKeys(raw map[string]any) []Issue {
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
	sort.Strings(unknown) // 排序保证输出稳定，便于 diff 与脚本消费
	for _, k := range unknown {
		issues = append(issues, Issue{
			Level:   LevelError,
			Message: i18n.T("Unknown config key {{.Key}} (possible typo); ggt silently ignores it", map[string]any{"Key": k}),
		})
	}

	for _, s := range settings {
		v, ok := raw[s.Key]
		if !ok {
			continue // 缺键不是问题：缺键就用默认值
		}

		// 数组项的类型不对是硬错误：运行期无法把它当成路径列表用
		if s.Kind == KindPaths {
			if _, isList := v.([]any); !isList {
				issues = append(issues, Issue{
					Level: LevelError,
					Message: i18n.T("Expected an array for {{.Key}}, got {{.Type}}", map[string]any{
						"Key": s.Key, "Type": jsonTypeName(v),
					}),
				})
			}
			continue
		}

		// 标量的类型不规范属警告：运行期靠 mapstructure 的 WeaklyTypedInput 容错，
		// 能跑，但不规范
		if !scalarTypeMatches(s.Kind, v) {
			issues = append(issues, Issue{
				Level: LevelWarning,
				Message: i18n.T("Unexpected type for {{.Key}}: got {{.Type}}, expected {{.Kind}}; it still works but is better written as the expected type",
					map[string]any{"Key": s.Key, "Type": jsonTypeName(v), "Kind": string(s.Kind)}),
			})
			continue
		}

		if s.Parse == nil {
			continue
		}
		text := ValueText(v)
		if _, err := s.Parse(text); err != nil {
			issues = append(issues, Issue{
				Level: LevelError,
				Message: i18n.T("Invalid value for {{.Key}}: {{.Value}} (expected {{.Expected}})",
					map[string]any{"Key": s.Key, "Value": text, "Expected": s.Expected}),
			})
		}
	}
	return issues
}

// validateBuckets 检查分桶阈值区间的合理性。
//
// 阈值倒置时 classifyBySize 的三段区间会自相矛盾（low=1000、high=800 时 900MB 落中桶），
// 不崩溃但结果毫无意义。
func validateBuckets(raw map[string]any) []Issue {
	low, okLow := intValue(raw["size_bucket_low_mb"])
	high, okHigh := intValue(raw["size_bucket_high_mb"])
	if !okLow || !okHigh {
		return nil // 缺键或类型不对时前面已报过
	}
	if low >= high {
		return []Issue{{
			Level: LevelWarning,
			Message: i18n.T("size_bucket_low_mb ({{.Low}}) is not less than size_bucket_high_mb ({{.High}}), so the buckets are meaningless",
				map[string]any{"Low": low, "High": high}),
		}}
	}
	return nil
}

// validatePaths 检查仓库路径与父目录的可达性。
//
// 判定口径必须与运行期一致（git.IsRepo），否则会出现"体检说不合法、ggt 却能跑"的错位。
func validatePaths(raw map[string]any) []Issue {
	var issues []Issue
	issues = append(issues, checkPathList("repo_paths", raw["repo_paths"], true)...)
	issues = append(issues, checkPathList("parent_paths", raw["parent_paths"], false)...)
	return issues
}

// checkPathList 检查一个路径列表。mustBeRepo 为 true 时还要求每项是 git 仓库。
func checkPathList(key string, v any, mustBeRepo bool) []Issue {
	list, ok := v.([]any)
	if !ok {
		return nil // 类型不对时前面已报过
	}

	var issues []Issue
	seen := make(map[string]bool, len(list))
	for _, item := range list {
		p, ok := item.(string)
		if !ok {
			issues = append(issues, Issue{
				Level:   LevelError,
				Message: i18n.T("Expected a path string in {{.Key}}, got {{.Type}}", map[string]any{"Key": key, "Type": jsonTypeName(item)}),
			})
			continue
		}
		if seen[p] {
			issues = append(issues, Issue{
				Level:   LevelWarning,
				Message: i18n.T("Duplicate entry in {{.Key}}: {{.Path}}", map[string]any{"Key": key, "Path": p}),
			})
			continue
		}
		seen[p] = true

		if !filepath.IsAbs(p) {
			issues = append(issues, Issue{
				Level: LevelWarning,
				Message: i18n.T("Relative path in {{.Key}}: {{.Path}} — it is resolved against the current directory at runtime, so results vary",
					map[string]any{"Key": key, "Path": p}),
			})
			continue
		}

		info, err := os.Stat(p)
		if err != nil {
			issues = append(issues, Issue{
				Level:   LevelWarning,
				Message: i18n.T("Path in {{.Key}} does not exist: {{.Path}}", map[string]any{"Key": key, "Path": p}),
			})
			continue
		}

		switch {
		case mustBeRepo && !git.IsRepo(p):
			issues = append(issues, Issue{
				Level:   LevelWarning,
				Message: i18n.T("Path in repo_paths is not a git repository: {{.Path}}", map[string]any{"Path": p}),
			})
		case !mustBeRepo && !info.IsDir():
			issues = append(issues, Issue{
				Level:   LevelWarning,
				Message: i18n.T("Path in parent_paths is not a directory: {{.Path}}", map[string]any{"Path": p}),
			})
		}
	}
	return issues
}

// ValueText 把配置值（JSON 解码后的形态）转成裸文本，去掉 JSON 的引号与类型包装。
//
// 有两个用途：validate 里复用 Setting.Parse 校验文件中的取值（Parse 接收字符串），
// 以及 ggt config get 打印标量。两处共用一份实现，避免"校验时认得的写法"与
// "展示出来的写法"不一致。
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

// scalarTypeMatches 判断标量值的 JSON 类型是否符合配置项的规范类型。
func scalarTypeMatches(kind Kind, v any) bool {
	switch kind {
	case KindString:
		_, ok := v.(string)
		return ok
	case KindBool:
		_, ok := v.(bool)
		return ok
	case KindInt:
		switch v.(type) {
		case json.Number, float64:
			return true
		}
		return false
	}
	return true
}

// intValue 把 JSON 数值取成 int，取不到时返回 false。
func intValue(v any) (int, bool) {
	switch t := v.(type) {
	case json.Number:
		n, err := t.Int64()
		return int(n), err == nil
	case float64:
		return int(t), true
	}
	return 0, false
}
