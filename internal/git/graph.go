// 本文件是分支图的泳道分配，逐行照搬 VSCode 的 toISCMHistoryItemViewModelArray 与
// getHistoryItemIndex（src/vs/workbench/contrib/scm/browser/scmHistory.ts，MIT 许可）。
//
// 为什么连颜色轮转都照抄：泳道配色由分配顺序决定，同一份历史必须每次得到同一套颜色，
// 否则刷新一次整张图就换色；自己发明一套规则更不可能和 VSCode 的观感一致
//
// 与上游的三处差异（都是“上游有、这个项目没有”的东西，不是算法改动）：
//   - 上游的 incoming / outgoing changes 两个虚拟行（未拉取 / 未推送的占位）没有搬：
//     它们依赖 upstream 与 merge base 的判定，是另一件事
//   - 上游的 base ref（merge base 高亮）同理没有搬
//   - 颜色用 VSCode 的颜色 id（scmGraph.foreground1 这类），由 cmd 侧翻成页面上的 CSS 变量名：
//     这个包不认 CSS，就像 eggokit/theme 不认页面概念一样
package git

import "sort"

// GraphNode 是泳道上的一个节点：ID 是“这条泳道正等着哪个提交”，Color 是 VSCode 的颜色 id
type GraphNode struct {
	ID    string `json:"id"`
	Color string `json:"color"`
}

// 颜色 id，与 VSCode 的 scmGraph.* 令牌一一对应，取值见 eggokit/theme/defaults.json
const (
	ColorForeground1 = "scmGraph.foreground1"
	ColorForeground2 = "scmGraph.foreground2"
	ColorForeground3 = "scmGraph.foreground3"
	ColorForeground4 = "scmGraph.foreground4"
	ColorForeground5 = "scmGraph.foreground5"
	ColorRef         = "scmGraph.historyItemRefColor"
	ColorRemoteRef   = "scmGraph.historyItemRemoteRefColor"
)

// laneColors 是泳道配色的轮转表，顺序与上游 colorRegistry 一致
var laneColors = []string{
	ColorForeground1,
	ColorForeground2,
	ColorForeground3,
	ColorForeground4,
	ColorForeground5,
}

// HistoryViewModel 对齐上游的 ISCMHistoryItemViewModel
type HistoryViewModel struct {
	Item HistoryItem `json:"item"`
	// Kind 取 HEAD 或 node（上游还有 incoming-changes / outgoing-changes，本实现不产生）
	Kind string `json:"kind"`
	// Input 是这个提交“上方”的泳道，Output 是“下方”的泳道
	Input  []GraphNode `json:"inputSwimlanes"`
	Output []GraphNode `json:"outputSwimlanes"`
	// Index 是圆点在第几条泳道（对齐 getHistoryItemIndex），前端画线时要用
	Index int `json:"index"`
}

// LayoutHistory 把提交列表变成“每个提交 + 它上下两侧的泳道”。
//
// branch / upstream 是当前分支与它的上游引用名，只有这两个引用有固定配色（对齐上游那套
// colorMap）；headHash 是 HEAD 指向的提交，用来把那一行标成 HEAD
func LayoutHistory(items []HistoryItem, branch, upstream, headHash string) []HistoryViewModel {
	colorMap := map[string]string{}
	if branch != "" {
		colorMap[branch] = ColorRef
	}
	if upstream != "" {
		colorMap[upstream] = ColorRemoteRef
	}

	byID := make(map[string]HistoryItem, len(items))
	for _, item := range items {
		byID[item.Hash] = item
	}

	colorIndex := -1
	viewModels := make([]HistoryViewModel, 0, len(items))

	for _, item := range items {
		kind := "node"
		if headHash != "" && item.Hash == headHash {
			kind = "HEAD"
		}

		input := []GraphNode{}
		if n := len(viewModels); n > 0 {
			input = cloneNodes(viewModels[n-1].Output)
		}
		output := []GraphNode{}

		// 把第一个 parent 顶进“这个提交所在的那条泳道”：其余泳道原样传给下一行。
		// 找不到自己那条泳道时 firstParentAdded 保持为假，第一个 parent 会在下面按“新泳道”补上
		firstParentAdded := false
		if len(item.Parents) > 0 {
			for _, node := range input {
				if node.ID == item.Hash {
					if !firstParentAdded {
						color := labelColor(item, colorMap)
						if color == "" {
							color = node.Color
						}
						output = append(output, GraphNode{ID: item.Parents[0], Color: color})
						firstParentAdded = true
					}
					continue
				}
				output = append(output, node)
			}
		}

		// 其余 parent（合并提交的第二个及以后）各开一条新泳道。颜色优先取它们自己那一行的
		// 引用配色，取不到才轮到轮转表——上游也是这个顺序
		start := 0
		if firstParentAdded {
			start = 1
		}
		for i := start; i < len(item.Parents); i++ {
			color := ""
			if i == 0 {
				color = labelColor(item, colorMap)
			} else if parent, ok := byID[item.Parents[i]]; ok {
				color = labelColor(parent, colorMap)
			}
			if color == "" {
				colorIndex = (colorIndex + 1) % len(laneColors)
				color = laneColors[colorIndex]
			}
			output = append(output, GraphNode{ID: item.Parents[i], Color: color})
		}

		refs := cloneRefs(item.Refs)
		for i := range refs {
			if c, ok := colorMap[refs[i].Name]; ok {
				refs[i].Color = c
			}
		}
		// 排序对齐上游 compareHistoryItemRefs：当前分支 > 上游 > 有配色 > 其余。
		// 上游用的是 JS 的 Array.sort（稳定排序），这里同样用稳定排序，否则同名不同色的
		// 两个引用会在每次渲染时换位置
		sort.SliceStable(refs, func(a, b int) bool {
			return refOrder(refs[a], branch, upstream) < refOrder(refs[b], branch, upstream)
		})

		viewItem := item
		viewItem.Refs = refs

		viewModels = append(viewModels, HistoryViewModel{
			Item:   viewItem,
			Kind:   kind,
			Input:  input,
			Output: output,
			Index:  circleIndex(input, item),
		})
	}

	return viewModels
}

// circleIndex 对齐上游 getHistoryItemIndex：这个提交在它的输入泳道里的位置，
// 找不到就放在末尾（首个提交、或翻页后的第一行都是这种情况）
func circleIndex(input []GraphNode, item HistoryItem) int {
	for i, node := range input {
		if node.ID == item.Hash {
			return i
		}
	}
	return len(input)
}

// labelColor 对齐上游 getLabelColorIdentifier：这个提交的第一个“有配色”的引用用的是什么颜色
func labelColor(item HistoryItem, colorMap map[string]string) string {
	for _, ref := range item.Refs {
		if c, ok := colorMap[ref.Name]; ok {
			return c
		}
	}
	return ""
}

// refOrder 对齐上游 getHistoryItemRefOrder：1 当前分支、2 上游、4 有配色（其余引用）、99 都没有
func refOrder(ref HistoryRef, branch, upstream string) int {
	switch {
	case branch != "" && ref.Name == branch:
		return 1
	case upstream != "" && ref.Name == upstream:
		return 2
	case ref.Color != "":
		return 4
	}
	return 99
}

// cloneNodes 深拷贝泳道切片：上游用 deepClone，因为每一行都会改动自己那几条泳道的 id 与颜色，
// 浅拷贝会让上一行的结果被下一行改掉（表现为历史往回看时泳道突然错位）
func cloneNodes(nodes []GraphNode) []GraphNode {
	out := make([]GraphNode, len(nodes))
	copy(out, nodes)
	return out
}

func cloneRefs(refs []HistoryRef) []HistoryRef {
	out := make([]HistoryRef, len(refs))
	copy(out, refs)
	return out
}
