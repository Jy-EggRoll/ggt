// ui_settings.go 是设置面板的服务端：把注册表投影发给页面，并接收页面改过的取值。
//
// 为什么单独一个文件：这两个端点与仓库、分支图那些端点没有共同前提（不读快照、不跑 git、
// 不碰仓库状态），它们只做两件事——读配置文件、按注册表写配置文件。放进 ui.go 只会让那个
// 已经上千行的文件继续变长，也让“哪些端点会动仓库”这条分界线变模糊
package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/eggokit/logger"
	"github.com/jy-eggroll/ggt/internal/config"
)

// uiSettings 是 GET /api/settings 的响应体。
type uiSettings struct {
	// Path 是配置文件路径：面板上要显示它，用户想手工改或备份时不用再去命令行问一次
	Path  string               `json:"path"`
	Items []config.SettingView `json:"items"`
	// Error 非空表示整份配置文件读不出来（语法错误、权限、大小写重复键）。
	// 此时 Items 仍然逐项给出（取值是内置默认值）：页面要能同时画出配置表并说清文件坏在哪，
	// 否则用户打开面板只看得到一片空白，连“该修哪个文件”都无从下手
	Error string `json:"error,omitempty"`
}

// uiSettingsRequest 是 POST /api/settings 的请求体。
//
// 一次可以改多项：面板在点“保存”时发一次请求，逐项往返会让“改三项”变成三次串行等待。
// Values 只包含用户真正改过的项，Unset 是要求恢复默认（把键从文件里删掉）的项——
// 与命令行的 "ggt config reset" 是同一件事：删键让内置默认值生效，而不是把默认值写进文件
type uiSettingsRequest struct {
	Values map[string]string `json:"values"`
	Unset  []string          `json:"unset"`
	// Confirm 为真表示用户已经在页面上看过确认框并同意。
	// 只有“清掉受管项里一个还有可用条目的坏列表”会用到它：那一步会连带丢掉几个本来还能用的
	// 仓库，与命令行的 reset 一样必须先让损失可见（见 uiUnsetConfirm）
	Confirm bool `json:"confirm"`
}

// uiUnsetConfirm 说明“清掉这一项会丢掉什么”。
//
// 服务端在缺确认时不出手、只把这份损失摊开交给页面：条数、会丢掉的条目。页面弹一次确认，
// 用户同意后带 confirm 重发同一个请求。计数与条目都取自宽松加载同一套抢救规则
// （config.SalvagedPathListAt），因此确认框里承诺的与实际清掉的一定是同一批
type uiUnsetConfirm struct {
	Key    string   `json:"key"`
	Total  int      `json:"total"`
	Usable int      `json:"usable"`
	Lost   []string `json:"lost"`
}

// uiSettingsResult 是 POST /api/settings 的响应体。
//
// 不回声写入后的取值：面板保存成功后会重新拉一次视图，展示的因此永远是配置文件里的真实
// 内容（恢复默认之后显示的是内置默认值）。回声一份就多了一处可能与文件不一致的真相源
type uiSettingsResult struct {
	// Applied 是写入成功的配置项（规范键名）
	Applied []string `json:"applied,omitempty"`
	// Notes 是写完之后要额外告诉用户的话，按项给：例如语言要下次运行才生效
	Notes map[string]string `json:"notes,omitempty"`
	// Errors 按项报失败原因：一项写不进去不该让整批都失败，用户改对那一项就行
	Errors map[string]string `json:"errors,omitempty"`
	// Remaining 是保存之后文件里**仍然**存在的问题（键名 → 原因），包括这次没动过的坏值
	// 与拼错的未知键。它与 Errors 的区别是责任方：Errors 是这次请求没写进去的项，
	// Remaining 是文件里本来就坏、需要继续修的地方。
	//
	// 为什么必须回传：一次保存只处理用户改过的那几项，文件里别的坏值会被原样留着，
	// 下一次启动仍然失败。此前响应只说“已保存”，用户于是以为配置已经好了
	Remaining map[string]string `json:"remaining,omitempty"`
	// Confirm 非空表示这次请求没有执行完：其中有一项要先确认（见 uiUnsetConfirm）。
	// 它不是错误——用户还没决定，页面拿到它只该弹确认框，不该报错
	Confirm *uiUnsetConfirm `json:"confirm,omitempty"`
	// Reload 为真表示这批改动里有页面已经烧进去的东西，需要刷新页面才看得到效果
	Reload bool   `json:"reload,omitempty"`
	Error  string `json:"error,omitempty"`
}

// uiReloadKeys 列出“改完必须刷新页面才生效”的配置项，由注册表的 ApplyAt 派生。
//
// 这几个键的值由服务端在渲染首页时注入（见 runUI 里的 renderIndex：主题解析成 CSS 写进
// __GGT_THEME_CSS__，字体拼成 CSS 写进 __GGT_FONT_CSS__，其余设置整份写进 __GGT_SETTINGS__
// 供页面行为读用）。页面拿到的那份 HTML 已经是旧值，只改配置文件不会反映到当前页面上，
// 所以保存后要提示刷新。逐项的理由现在写在注册表各自 ApplyAt 标注的旁边，这里不再复述
//
// 这份清单曾经手写在本文件里，与注册表分处两个文件：那时它是唯一的真相来源，配置项改名或
// 删除后只会留下悬空的名字（不报错，只是刷新提示永远不出现）。改为从 ApplyAt 派生之后，
// 清单只随注册表走。语言那一档（需重启）刻意不在这里：刷新页面也还是旧语言
var uiReloadKeys = config.KeysWithApplyAt(config.ApplyReload)

// uiSettingsJSON 把设置面板那份视图序列化进首页，供页面行为读用。
//
// json.Marshal 默认把 < > & 转义成 \u003c 这类写法，因此内容里即使出现 "</script>"
// 也不会截断脚本标签；序列化失败时回退成空数组——页面拿不到配置就按各自的默认行为走
// （例如通知不自动消失），这比让整个首页渲染失败轻得多
func uiSettingsJSON(path string) []byte {
	views, readErr := config.SettingsViewAt(path)
	if readErr != nil {
		// 文件读不出来不是这里能修的事：面板会把它显示在顶部，首页快照照常给出默认值，
		// 页面行为（例如通知不自动消失）因此仍然有确定的取值
		logger.Warn(l10n.T("Failed to read the configuration for the settings snapshot", nil), "err", readErr)
	}
	b, err := json.Marshal(views)
	if err != nil {
		logger.Warn(l10n.T("Failed to serialize the settings snapshot", nil), "err", err)
		return []byte("[]")
	}
	return b
}

// uiReloadsPage 判断某个配置项改完之后是否需要刷新页面。
func uiReloadsPage(key string) bool {
	for _, k := range uiReloadKeys {
		if k == key {
			return true
		}
	}
	return false
}

// handleSettings 返回 /api/settings 的处理函数：GET 读、POST 写。
//
// path 由调用方传入，而不是在这里现算 config.GetDefaultConfigPath()：写入的落点必须显式，
// 否则测试只能去动开发者自己的真实配置，而“测试不许碰真实配置”是这个项目一直守着的底线
//
// 写挂在 POST 上而不是 GET：基座的 writeGuard 只对非 GET/HEAD 做同源校验，
// 把写动作挂在 GET 上等于自己把 CSRF 那道防线绕掉（与仓库那几个写端点同一条理由）
func handleSettings(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			items, readErr := config.SettingsViewAt(path)
			out := uiSettings{Path: path, Items: items}
			if readErr != nil {
				// 状态码仍是 200：整份视图照常返回，页面要能一边画出配置表、一边把这句话
				// 放在面板顶部。改用 5xx 的话前端只剩一条“加载失败”，连配置表都看不到，
				// 而这个面板恰恰是用户唯一能就地看清文件状况的地方。
				//
				// 修复方式直接复用 config.WithRecoveryHint：文件读不出来时面板自己无能为力，
				// 用户要的正是“下一步敲什么”，而那句话只有命令行那一处在维护
				out.Error = l10n.T("Cannot read the config file: {{.Err}}", map[string]any{"Err": config.WithRecoveryHintFor(readErr, false, config.HintForDashboard)})
			}
			writeUIJSON(w, http.StatusOK, out)
		case http.MethodPost:
			applySettings(w, r, path)
		default:
			writeUIJSON(w, http.StatusMethodNotAllowed,
				uiSettings{Error: l10n.T("Only GET and POST are allowed", nil)})
		}
	}
}

// applySettings 按注册表写入面板提交的取值。
//
// 校验、解析、写入全部走 config.SetFromTextAt，与命令行的 "ggt config set" 是同一份实现：
// 两处的严格度因此不可能分叉（此前面板按候选清单拦、命令行只看 Normalize，于是 language
// 写 zh、自定义主题 id、theme_dark 的空串三分之二被面板拒绝而命令行接受）
//
// 这里负责三件事：把请求里的键按注册表的规则归一、把多项结果按项归拢、写完之后对整份文件
// 再校验一次并把仍然存在的问题回传
func applySettings(w http.ResponseWriter, r *http.Request, path string) {
	var req uiSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeUIJSON(w, http.StatusBadRequest, uiSettingsResult{Error: l10n.T("Invalid request body", nil)})
		return
	}

	out := uiSettingsResult{
		Notes:  map[string]string{},
		Errors: map[string]string{},
	}

	values := requestedValues(req.Values, out.Errors)

	// 按注册表的顺序写，而不是按请求或 map 的遍历顺序：Go 里 map 的顺序是随机的，
	// 而“同时改两项、其中一项失败”时用户看到的报错顺序会一次一个样，问题很难复现
	for _, s := range config.Settings() {
		text, ok := values[s.Key]
		if !ok {
			continue
		}
		if _, err := config.SetFromTextAt(path, s.Key, text); err != nil {
			out.Errors[s.Key] = err.Error()
			continue
		}
		out.Applied = append(out.Applied, s.Key)
		if note := settingNote(s.Key); note != "" {
			out.Notes[s.Key] = note
		}
		out.Reload = out.Reload || uiReloadsPage(s.Key)
	}

	// 恢复默认走的是“把键从文件里删掉”，与命令行 reset 的默认模式一致：文件里因此只留下
	// 用户真正改过的项，不会被一堆等于默认值的键塞满。
	// 删不删得掉由 config.PlanUnsetAt 判定，与命令行共用同一份规则（含受管项的例外放行）
	for _, key := range req.Unset {
		plan, err := config.PlanUnsetAt(path, key)
		if err != nil {
			if errors.Is(err, config.ErrUnknownKey) {
				out.Errors[key] = errUnknownKey(key).Error()
			} else {
				out.Errors[key] = err.Error()
			}
			continue
		}
		// 受管项的坏列表里还留着可用条目时先要确认：清掉它就是删掉几个本来还能用的仓库。
		// 判定与命令行 reset 共用 config.SalvagedPathListAt（计数与条目同源），
		// 差别只在把“整段命令 + --yes”换成页面上的一次确认
		if plan.Managed && !req.Confirm {
			total, lost, countErr := config.SalvagedPathListAt(path, plan.Key)
			if countErr != nil {
				out.Errors[plan.Key] = countErr.Error()
				continue
			}
			if len(lost) > 0 {
				out.Confirm = &uiUnsetConfirm{Key: plan.Key, Total: total, Usable: len(lost), Lost: lost}
				continue
			}
		}
		if note := unsetNote(plan); note != "" {
			out.Notes[plan.Key] = note
		}
		if err := config.UnsetKeyAt(path, plan.Key); err != nil {
			out.Errors[plan.Key] = err.Error()
			continue
		}
		out.Applied = append(out.Applied, plan.Key)
		if s, ok := config.Lookup(plan.Key); ok {
			out.Reload = out.Reload || uiReloadsPage(s.Key)
		}
	}

	// 写完之后对整份文件再校验一次：这一次保存只处理用户改过的那几项，文件里别的坏值
	// （或一个拼错的键）会被原样留着，下一次启动仍然失败。不把这件事说出来，
	// 用户看到“已保存”就以为配置好了
	remaining, readErr := config.ProblemsAt(path)
	switch {
	case readErr != nil:
		// 刚写完却读不出来，只可能是别处同时改坏了它（另一个终端、编辑器）。这不是这次
		// 写入的失败，因此不写进 Errors，而是在顶层说明“现在连文件都读不出来”，
		// 并附上命令行那一处维护的修复方式（见 WithRecoveryHint）
		out.Error = l10n.T("Cannot read the config file: {{.Err}}", map[string]any{"Err": config.WithRecoveryHintFor(readErr, false, config.HintForDashboard)})
	case len(remaining) > 0:
		out.Remaining = make(map[string]string, len(remaining))
		for _, p := range remaining {
			out.Remaining[p.Key] = p.Error()
		}
	}

	// 一个键都没动（例如用户点保存时什么都没改）也回 200：这不是错误，
	// 报错反而会让面板弹一个用户无法处理的提示
	if len(out.Notes) == 0 {
		out.Notes = nil
	}
	if len(out.Errors) == 0 {
		out.Errors = nil
	}
	writeUIJSON(w, http.StatusOK, out)
}

// requestedValues 把请求里的取值归一成“规范键名 → 文本”。
//
// 键名匹配与命令行同一规则（config.Lookup：忽略大小写与首尾空白），因此 "Language" 与
// " language " 落到同一个键上。认不出来的键进 errors：此前只有注册表在驱动遍历，请求里
// 多出来的键被静默丢掉、响应仍是 200 空结果，用户以为那一项写进去了
//
// 同一项被写成两个取值不同的键时无法判断用户要哪个，按冲突报错而不是任选一个：
// 任选一个的后果是“保存成功”却写进了用户没打算用的值
func requestedValues(req map[string]string, errs map[string]string) map[string]string {
	out := make(map[string]string, len(req))
	conflict := make(map[string]bool)
	rawKeys := make([]string, 0, len(req))
	for k := range req {
		rawKeys = append(rawKeys, k)
	}
	sort.Strings(rawKeys)

	for _, rawKey := range rawKeys {
		s, ok := config.Lookup(rawKey)
		if !ok {
			errs[strings.TrimSpace(rawKey)] = errUnknownKey(rawKey).Error()
			continue
		}
		text := req[rawKey]
		prev, dup := out[s.Key]
		if !dup {
			out[s.Key] = text
			continue
		}
		if prev != text {
			conflict[s.Key] = true
			errs[s.Key] = l10n.T("Received two different values for {{.Key}} in the same request, so it was not saved: {{.First}} and {{.Second}}",
				map[string]any{"Key": s.Key, "First": prev, "Second": text})
		}
	}
	for key := range conflict {
		delete(out, key)
	}
	return out
}

// unsetNote 返回“这次恢复默认有什么要额外说明的”，没有就返回空串。
//
// 两种情况需要说明：清掉的是受管项的坏值（平时它不让在这里改），以及清掉的是一个
// 注册表里没有的键。两者都是命令行 reset 会讲的话，面板不该少讲
func unsetNote(plan config.UnsetPlan) string {
	if plan.Managed {
		s, _ := config.Lookup(plan.Key)
		return managedResetNote(plan.Key, s.ManagedBy)
	}
	if !plan.Known {
		return l10n.T("{{.Key}} is not a known setting; removed it from the config file",
			map[string]any{"Key": plan.Key})
	}
	return ""
}

// writeUIJSON 输出只读端点的 JSON 响应（设置面板与仓库大小/文件清单两处共用）。
// 与 diff 端点一样关掉 HTML 转义：取值里有路径与主题名，转义后排查接口时看到的 JSON 无法阅读。
// 收 any 而不是各自的结构体：这几个响应都是“一个结构体加一个可选错误字段”，
// 每加一个端点就复制一遍这五行没有意义
func writeUIJSON(w http.ResponseWriter, status int, out any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// 配置随时可能在另一个终端被 ggt config set 改掉，缓存留不得
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		// 响应头已发出，改不了状态码，只能记日志
		logger.Error(l10n.T("Failed to encode the settings response", nil), "error", err)
	}
}
