// store.go 负责配置文件的原始读写。
//
// 为什么写不用 viper：viper 的 WriteConfig 走 AllSettings()，把内存里的全部键一次性
// 落盘，**无法表达"把某个键删掉"**——而 reset 的默认模式正是删键。另外 viper.Set 会
// 往包级全局单例里写入永不失效的 override（详见 LoadLanguage 的注释）。
//
// 读仍由 viper 承担（LoadConfig 需要它的大小写不敏感与弱类型容错），
// 写与体检则走这里的原始 map 路径。
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ggt/internal/locales"
	"ggt/pkg/jsonfile"
	"ggt/pkg/l10n"
)

// ReadRawAt 读取配置文件的原始键值。
//
// 文件不存在时返回空 map 而非错误——首次写入总要能在"还没有文件"的状态下进行。
// 但**其他任何错误（权限、目录、JSON 语法）都必须返回 error**，让调用方拒绝写入：
// 否则用户在损坏的文件上敲一次 set，就会把 repo_paths 整份丢掉。
func ReadRawAt(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}

	return parseRaw(data)
}

// parseRaw 把配置文件的字节解析为规范化键名后的原始键值。
// 单列出来是为了让体检（validate.go）能复用同一套解析与键名规范化，
// 不必各自实现一遍——两套解析迟早会分叉成"能跑但体检说不行"。
func parseRaw(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	// UseNumber 让数字保持字面量，避免未知键里的大整数被 float64 静默改写
	// （如 12345678901234567890 变成 12345678901234567000），也避免 1e400 让解析直接失败
	dec.UseNumber()

	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	// 文件内容为 null 时 Decode 得到 nil map，后面直接赋值会 panic
	if raw == nil {
		raw = map[string]any{}
	}
	return normalizeKeys(raw)
}

// WriteRawAt 以原子方式写回配置。
//
// 写法是"同目录临时文件 + rename"：直接覆写的话，进程写到一半崩溃就会留下半截 JSON，
// 而此后任何命令都因解析失败而不可用。
func WriteRawAt(path string, raw map[string]any) error {
	normalized, err := normalizeKeys(raw)
	if err != nil {
		return err
	}

	target, err := resolveSymlink(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}

	// 统一的规范形态（字典序、2 空格缩进、不转义 HTML）由 pkg/jsonfile 提供，
	// 与语言文件写入共用同一份策略
	buf, err := jsonfile.Marshal(normalized)
	if err != nil {
		return err
	}
	return writeFileAtomic(target, buf)
}

// SetKeyAt 写入单个配置项，保留文件里的其他键（含未知键）。
func SetKeyAt(path, key string, value any) error {
	raw, err := ReadRawAt(path)
	if err != nil {
		return err
	}
	raw[strings.ToLower(strings.TrimSpace(key))] = value
	return WriteRawAt(path, raw)
}

// UnsetKeyAt 删除单个配置项。键本来就不存在时是空操作（幂等）。
func UnsetKeyAt(path, key string) error {
	raw, err := ReadRawAt(path)
	if err != nil {
		return err
	}
	delete(raw, strings.ToLower(strings.TrimSpace(key)))
	return WriteRawAt(path, raw)
}

// ResetAllAt 重置全部配置，两种模式：
//
//	writeDefaults=false → 删除配置文件，回到"从未配置过"的状态
//	writeDefaults=true  → 写入一份全默认值的配置文件
//
// 写默认值这条路径**刻意不是"删了再写"**：那样会留下"删成功、写失败"的窗口，
// 配置与仓库记录会一起消失。直接从空 map 起步做一次原子写，中途失败则原文件完好。
func ResetAllAt(path string, writeDefaults bool) error {
	if !writeDefaults {
		target, err := resolveSymlink(path)
		if err != nil {
			return err
		}
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return WriteRawAt(path, DefaultRaw())
}

// DefaultRaw 返回一份全默认值的配置（键值形态）。
// 切片显式初始化成空切片：`defaultConfig()` 的切片是 nil，直接序列化会写出
// `"repo_paths": null`，而"一份默认配置"应当是 `[]`。
func DefaultRaw() map[string]any {
	raw := make(map[string]any, len(settings))
	for _, s := range settings {
		raw[s.Key] = s.Default
	}
	return raw
}

// normalizeKeys 把 map 的键统一小写，并拒绝大小写变体冲突。
//
// 小写是必需的：viper 读文件时会把键**就地小写**（insensitiviseMap），而本文件的
// 写路径不会。两边不一致的话，文件里已有的 {"Language": ...} 会在一次写入后变成
// Language + language 两份，viper 用随机迭代序合并 → 每次运行选到的语言都可能不同。
func normalizeKeys(raw map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		lk := strings.ToLower(strings.TrimSpace(k))
		if _, dup := out[lk]; dup {
			return nil, fmt.Errorf("%s",
				l10n.T("The config file has multiple keys that differ only in case ({{.Key}}), so which one wins is undefined; please keep just one",
					map[string]any{"Key": lk}))
		}
		out[lk] = v
	}
	return out, nil
}

// resolveSymlink 把符号链接解析到真实路径。
//
// 用户的配置文件可能是指向 dotfiles 仓库的软链。rename 会把软链本身替换成普通文件，
// 于是用户后续对 dotfiles 仓库的编辑不再生效——那等于悄悄改掉了他实际编辑的目标。
func resolveSymlink(path string) (string, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return path, nil
		}
		return "", err
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return path, nil
	}
	return filepath.EvalSymlinks(path)
}

// writeFileAtomic 通过"同目录临时文件 + rename"原子替换目标文件。
//
// 同目录是必须的：跨卷 rename 会失败。
// 刻意不 fsync 目录：Windows 不支持打开目录做 Sync，加了会导致跨平台运行失败。
func writeFileAtomic(target string, data []byte) error {
	// 沿用原有权限：os.CreateTemp 建出来是 0600，直接 rename 会让原本 0644 的
	// 配置文件变成只有属主可读
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(target); err == nil {
		mode = fi.Mode().Perm()
	}

	tmp, err := os.CreateTemp(filepath.Dir(target), ".ggt-config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// rename 成功后这个文件已不存在，Remove 返回的 error 可以忽略
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, target)
}

// ErrUnknownKey 表示请求了一个未注册的配置项。
var ErrUnknownKey = errors.New("unknown config key")

// EffectiveAt 返回某个配置项"只看配置文件"意义上的生效值：
// 文件里有就用文件的值，没有就用默认值。
//
// 刻意不采纳命令行 -c 这类临时覆盖——那只是本次运行的参数，不属于配置。若混进来，
// `ggt -c 4 config get concurrency` 会打印 4，让脚本作者以为配置被改过。
func EffectiveAt(path, key string) (any, error) {
	s, ok := Lookup(key)
	if !ok {
		return nil, ErrUnknownKey
	}
	raw, err := ReadRawAt(path)
	if err != nil {
		return nil, err
	}
	v, ok := raw[s.Key]
	if !ok {
		return s.Default, nil
	}
	// 语言要归一化：文件里可能写着 zh 或 zh-Hans，而运行期实际生效的是 zh-CN，
	// get 的输出应当与运行期一致
	if s.Key == "language" {
		if text, isStr := v.(string); isStr && l10n.IsSupported(text, locales.Supported()) {
			return l10n.Normalize(text, locales.Supported(), locales.Default), nil
		}
	}
	return v, nil
}

// SetKey / UnsetKey / ResetAll 是使用默认配置路径的便捷封装，供命令层调用。
// 核心逻辑一律接受显式路径（上面的 At 变体），测试才能用临时目录而不碰 HOME。
func SetKey(key string, value any) error {
	return SetKeyAt(GetDefaultConfigPath(), key, value)
}

func UnsetKey(key string) error {
	return UnsetKeyAt(GetDefaultConfigPath(), key)
}

func ResetAll(writeDefaults bool) error {
	return ResetAllAt(GetDefaultConfigPath(), writeDefaults)
}
