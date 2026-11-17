// recover.go 是配置坏掉时的自救通道。
//
// 背景：取值解析统一走 Setting.Normalize 之后，任何一项取值非法都会让加载失败，
// 所有命令因此在跑起来之前就退出——包括本该用来修配置的 ggt config 与 ggt repo 本身。
// 实测的死锁链是 repo_paths 写成 [""] 之后，ggt repo list 与 ggt config reset repo_paths
// 一起被挡下，用户只剩手改文件或 config reset --all（丢掉全部仓库记录）。
//
// 这里提供两样东西，缺一不可：
//   - 宽松加载：只要求 JSON 语法与键名可读，坏的键退回默认值并逐条列出，供 config 与 repo
//     两个子树继续运行（允许做什么、不允许做什么见 cmd 层 loadRecoverableConfig 的说明）
//   - 可执行的修复命令：加载失败的输出必须点名坏掉的键与清掉它的命令，否则用户知道
//     “配置坏了”却不知道下一步能敲什么
package config

import (
	"errors"
	"sort"
	"strings"

	"github.com/jy-eggroll/eggokit/l10n"
)

// Problem 描述配置文件里一个键的问题，Key 一律是规范化后的键名。
//
// 单列成一个结构而不是只返回 error 列表：调用方要按键名拼出“清掉这一个键”的命令，
// 而从错误文案里反解键名既不可靠，也让文案失去改写自由。
type Problem struct {
	Key string
	Err error
}

// Error 让 Problem 自己就能当作一条可打印的问题说明。
func (p Problem) Error() string { return p.Err.Error() }

// ResetCommand 返回清掉某个键的可执行命令。
//
// 提示里必须给能直接敲的命令，而不是“请修改配置文件”：出问题的正是配置文件，
// 用户最需要的是不用先读懂它就能恢复。命令只有一个来源，报错与宽松加载的提示因此不会分叉。
func ResetCommand(key string) string {
	return "ggt config reset " + key
}

// ResetAllCommand 是连 JSON 都读不出来时唯一的命令行出路。
// 它同时会清空已登记的仓库，因此只作为最后一条提示出现。
const ResetAllCommand = "ggt config reset --all"

// WithRecoveryHint 给“配置加载失败”补上一句可直接执行的修复命令。
//
// 为什么必须补：配置一旦加载失败，除了 config 与 repo 两个子树，其它命令都起不来，
// 用户手上只剩“手改文件”与“清空全部配置”两条路。点名坏掉的键与清掉它的命令，
// 才能把这次失败变成一次可恢复的操作。重复调用不会把提示叠两遍。
func WithRecoveryHint(err error) error {
	if err == nil {
		return nil
	}
	var already *recoveryHinted
	if errors.As(err, &already) {
		return err
	}

	var keyErr *KeyError
	if errors.As(err, &keyErr) {
		return &recoveryHinted{err: err, hint: l10n.T(`Fix it with "{{.Command}}", or run "ggt config validate" to see every problem; "ggt repo" and the rest of "ggt config" still work`,
			map[string]any{"Command": ResetCommand(keyErr.Key)})}
	}
	return &recoveryHinted{err: err, hint: l10n.T(`Edit the file by hand, or run "{{.Command}}" to start over; "ggt repo" and the rest of "ggt config" still work`,
		map[string]any{"Command": ResetAllCommand})}
}

// recoveryHinted 是“已经带上修复提示”的错误。
// 它让 WithRecoveryHint 可以放心地在每一层都调用一次，而提示只出现一遍。
type recoveryHinted struct {
	err  error
	hint string
}

func (e *recoveryHinted) Error() string { return e.err.Error() + "\n" + e.hint }

func (e *recoveryHinted) Unwrap() error { return e.err }

// LoadConfigLenientAt 是自救用的宽松加载：只要求 JSON 语法与键名可读，
// 不要求每个取值都通过 Normalize。
//
// 坏掉的键退回该键的默认值；路径列表额外保留仍然可用的条目——那里面每一条都代表用户
// 登记过的一个仓库，整体退回空列表等于让他重新登记一遍。未知键在宽松加载里只记录不报错：
// 这两个子树的职责正是把它清掉。
//
// 返回的问题清单供调用方逐条打印“怎么修”；error 只在连 JSON 或键名都读不出来时返回
// （权限、语法错误、仅大小写不同的重复键），那时任何键级修复都无从下手。
//
// 宽松加载看到的配置与正常运行期可能不一致（坏的键是默认值），因此调用方的权限必须收窄：
// 见 cmd 层 loadRecoverableConfig 的说明。
func LoadConfigLenientAt(path string) (*Config, []Problem, error) {
	raw, err := ReadRawAt(path)
	if err != nil {
		return nil, nil, err
	}

	merged := DefaultRaw()
	var problems []Problem
	for _, key := range sortedRawKeys(raw) {
		v := raw[key]
		s, ok := Lookup(key)
		if !ok {
			problems = append(problems, Problem{Key: key, Err: unknownKeyError(key)})
			continue
		}
		normalized, err := normalizeValue(s, v)
		if err != nil {
			// 废弃的并发语义值运行期仍按 auto 跑，不算坏值（与严格加载同一口径）
			if errors.Is(err, errDeprecatedConcurrency) {
				merged[s.Key] = v
				continue
			}
			// 路径列表：保留仍然可用的条目，别把用户登记过的仓库一起丢掉。
			// 不是列表形态（标量、对象、null）时 salvage 返回 nil，此时保持默认值
			if s.Kind == KindPaths {
				if salvaged := salvagePathList(v); salvaged != nil {
					merged[s.Key] = salvaged
				}
			}
			problems = append(problems, Problem{Key: s.Key, Err: err})
			continue
		}
		merged[s.Key] = normalized
	}

	cfg, err := decodeMerged(merged)
	if err != nil {
		return nil, problems, err
	}
	return cfg, problems, nil
}

// LoadConfigLenient 是使用默认配置路径的便捷封装。
func LoadConfigLenient() (*Config, []Problem, error) {
	return LoadConfigLenientAt(GetDefaultConfigPath())
}

// salvagePathList 从一份非法路径列表里挑出还能用的条目：元素是字符串、去掉空白后非空、
// 且不含换行与控制字符（后两条与 normalize 的判定同源，见 pathsValueNormalizer）。
//
// 不是列表形态（标量、对象、null）时返回 nil，表示无从抢救，由调用方退回默认值。
// 返回空切片是合法的抢救结果：列表里一条可用的都没有，于是等价于清空。
func salvagePathList(v any) []string {
	var items []any
	switch list := v.(type) {
	case []any:
		items = list
	case []string:
		items = make([]any, len(list))
		for i, item := range list {
			items[i] = item
		}
	default:
		return nil
	}

	out := make([]string, 0, len(items))
	for _, item := range items {
		path, ok := item.(string)
		if !ok || strings.TrimSpace(path) == "" || hasControlChar(path) {
			continue
		}
		out = append(out, path)
	}
	return out
}

// sortedRawKeys 让遍历配置键的顺序固定。
//
// 加载失败时要报“哪一个键坏了”，map 的随机迭代顺序会让同一份文件每次报出不同的键，
// 用户与测试都无法复现；宽松加载的问题清单也需要稳定顺序，才便于阅读与 diff。
func sortedRawKeys(raw map[string]any) []string {
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ValueErrorAt 判断配置文件里某个键的当前值能否通过 Normalize。
//
// bad 为真表示取值非法（错误文案与运行期加载同源），err 非空表示连文件都读不出来。
// 两者分开是因为后果不同：取值非法时 reset 应当放行（见 cmd 的 resetOneKey），
// 而读不出文件时应当把真实原因报给用户，不能伪装成“这一项坏了”。
// 键不存在、取值合法、以及废弃的并发语义值（运行期照常按 auto 跑）都返回 (false, nil)。
func ValueErrorAt(path, key string) (bad bool, err error) {
	s, ok := Lookup(key)
	if !ok {
		return false, ErrUnknownKey
	}
	raw, err := ReadRawAt(path)
	if err != nil {
		return false, err
	}
	v, ok := raw[s.Key]
	if !ok {
		return false, nil
	}
	_, normalizeErr := normalizeValue(s, v)
	if normalizeErr == nil || errors.Is(normalizeErr, errDeprecatedConcurrency) {
		return false, nil
	}
	return true, nil
}
