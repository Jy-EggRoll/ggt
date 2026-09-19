// settings.go 定义 ggt 配置项的注册表。
//
// get / set / reset / validate 四个操作共用这一份声明，避免四处各写一套
// "这个键叫什么、什么类型、默认值是多少、什么算合法"——那种重复迟早会漂移成
// "set 能写进去、validate 说它非法"这类自相矛盾。
//
// 新增配置项时：在这里加一条，并在 Config 结构体上加同名的 json tag。
// 两处不一致会被 TestSettingsMatchConfigFields 的反射断言拦下。
package config

import (
	"errors"
	"strconv"
	"strings"

	"ggt/internal/locales"
	"ggt/pkg/l10n"
)

// ErrInvalidValue 表示用户输入的值不合法。
//
// 这里刻意不携带具体原因文案：合法取值的人类可读描述由 Setting.Expected 提供，
// 由调用方用统一模板渲染（见 cmd/config.go）。若让每个 Parse 各返回一句散文，
// 用户可见文案的数量会随校验规则数线性增长，中英双语都得跟着维护。
var ErrInvalidValue = errors.New("invalid value")

// Kind 描述配置项的取值类型。
type Kind string

const (
	KindString Kind = "string"
	KindBool   Kind = "bool"
	KindInt    Kind = "int"
	KindPaths  Kind = "paths" // 字符串数组
)

// 数值上限不是洁癖，而是跨平台安全边界：
//   - concurrency 会一路传到 worker.Map 的 make(chan struct{}, concurrency)，
//     本项目发布 386 目标，若允许填 20 亿就会直接 OOM
//   - 分桶阈值若超过 2^31-1，64 位机器能写进去，386 二进制却解码失败，
//     会导致该平台上所有命令不可用
const (
	maxConcurrency = 1024
	maxBucketMB    = 1_000_000
)

// Setting 描述一个配置项。
type Setting struct {
	// Key 与配置文件里的 JSON 键完全一致（snake_case），不引入第二套命名。
	Key string
	// Kind 决定 get 的输出形态与体检时的类型检查。
	Kind Kind
	// Default 是 reset --defaults 写入的值，也是配置文件缺该键时的生效值。
	Default any
	// Expected 是合法取值的人类可读描述，用于拼装错误信息。
	Expected string
	// Parse 把命令行字符串转成待写入的 JSON 值，失败时返回 ErrInvalidValue。
	// 它同时是 set 的校验器与 validate 的校验器。
	Parse func(string) (any, error)
	// ManagedBy 非空表示该项由别的命令管理：get 可读，set/reset 拒绝。
	// 仓库列表就属于这种——增删要走 ggt repo，那里有去重、git 仓库校验、路径规范化。
	ManagedBy string
}

// settings 是全部配置项的注册表，顺序即 ggt config validate 与帮助里的展示顺序。
var settings = []Setting{
	{
		Key:      "concurrency",
		Kind:     KindString,
		Default:  DefaultConcurrency,
		Expected: "CPUHalf, CPUFull, CPUQuarter, or a positive integer (max 1024)",
		Parse:    parseConcurrency,
	},
	{
		Key:      "ignore_submodules",
		Kind:     KindBool,
		Default:  false,
		Expected: "true or false",
		Parse:    parseBool,
	},
	{
		Key:      "size_bucket_low_mb",
		Kind:     KindInt,
		Default:  500,
		Expected: "a positive integer (max 1000000)",
		Parse:    parsePositiveInt(maxBucketMB),
	},
	{
		Key:      "size_bucket_high_mb",
		Kind:     KindInt,
		Default:  800,
		Expected: "a positive integer (max 1000000)",
		Parse:    parsePositiveInt(maxBucketMB),
	},
	{
		Key:      "size_unit",
		Kind:     KindString,
		Default:  "decimal",
		Expected: "decimal or binary",
		Parse:    parseSizeUnit,
	},
	{
		Key:      "language",
		Kind:     KindString,
		Default:  locales.Default,
		Expected: "a supported language tag (see ggt --help for the current list)",
		Parse:    parseLanguage,
	},
	{
		Key:       "repo_paths",
		Kind:      KindPaths,
		Default:   []string{},
		ManagedBy: "ggt repo",
	},
	{
		Key:       "parent_paths",
		Kind:      KindPaths,
		Default:   []string{},
		ManagedBy: "ggt repo",
	},
}

// Settings 返回全部配置项（副本，调用方改动不影响注册表）。
func Settings() []Setting {
	out := make([]Setting, len(settings))
	copy(out, settings)
	return out
}

// Lookup 按键名查找配置项。键名比较忽略大小写与首尾空白，
// 因为用户手敲时大小写很难记得准，而配置文件里的键本身不区分大小写。
func Lookup(key string) (Setting, bool) {
	k := strings.ToLower(strings.TrimSpace(key))
	for _, s := range settings {
		if s.Key == k {
			return s, true
		}
	}
	return Setting{}, false
}

// parseConcurrency 解析并发数：三种 CPU 相对语义（大小写不敏感）或正整数。
// 语义串统一存成规范大小写，避免文件里出现 cpuhalf/CPUHALF 这类同义异形写法。
func parseConcurrency(s string) (any, error) {
	v := strings.TrimSpace(s)
	switch strings.ToLower(v) {
	case "cpuhalf":
		return "CPUHalf", nil
	case "cpufull":
		return "CPUFull", nil
	case "cpuquarter":
		return "CPUQuarter", nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 || n > maxConcurrency {
		return nil, ErrInvalidValue
	}
	return strconv.Itoa(n), nil
}

// parseBool 解析布尔项，接受 true/false 与 1/0，大小写不敏感。
func parseBool(s string) (any, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	}
	return nil, ErrInvalidValue
}

// parsePositiveInt 返回一个"正整数且不超过 max"的解析器。
func parsePositiveInt(max int) func(string) (any, error) {
	return func(s string) (any, error) {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n <= 0 || n > max {
			return nil, ErrInvalidValue
		}
		return n, nil
	}
}

// parseSizeUnit 解析 MB 换算口径。
func parseSizeUnit(s string) (any, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "decimal":
		return "decimal", nil
	case "binary":
		return "binary", nil
	}
	return nil, ErrInvalidValue
}

// parseLanguage 解析输出语言。
//
// 必须用 l10n.IsSupported 而不是 l10n.Normalize：后者对不认识的输入回退默认语言，
// 照搬会把用户输入的 fr 静默改写成 en——而"我要法语"和"我要英语"显然不是一回事。
// 校验通过后存入归一化结果，使文件里只有规范形态（zh 与 zh-Hans 都存成 zh-CN）。
func parseLanguage(s string) (any, error) {
	v := strings.TrimSpace(s)
	if !l10n.IsSupported(v, locales.Supported()) {
		return nil, ErrInvalidValue
	}
	return l10n.Normalize(v, locales.Supported(), locales.Default), nil
}
