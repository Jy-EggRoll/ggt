// ui_settings.go 是设置面板的服务端：把注册表投影发给页面，并接收页面改过的取值。
//
// 为什么单独一个文件：这两个端点与仓库、分支图那些端点没有共同前提（不读快照、不跑 git、
// 不碰仓库状态），它们只做两件事——读配置文件、按注册表写配置文件。放进 ui.go 只会让那个
// 已经上千行的文件继续变长，也让“哪些端点会动仓库”这条分界线变模糊
package cmd

import (
	"encoding/json"
	"net/http"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/eggokit/logger"
	"github.com/jy-eggroll/ggt/internal/config"
)

// uiSettings 是 GET /api/settings 的响应体。
type uiSettings struct {
	// Path 是配置文件路径：面板上要显示它，用户想手工改或备份时不用再去命令行问一次
	Path  string               `json:"path"`
	Items []config.SettingView `json:"items"`
	Error string               `json:"error,omitempty"`
}

// uiSettingsRequest 是 POST /api/settings 的请求体。
//
// 一次可以改多项：面板在点“保存”时发一次请求，逐项往返会让“改三项”变成三次串行等待。
// Values 只包含用户真正改过的项，Unset 是要求恢复默认（把键从文件里删掉）的项——
// 与命令行的 "ggt config reset" 是同一件事：删键让内置默认值生效，而不是把默认值写进文件
type uiSettingsRequest struct {
	Values map[string]string `json:"values"`
	Unset  []string          `json:"unset"`
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
	// Reload 为真表示这批改动里有页面已经烧进去的东西，需要刷新页面才看得到效果
	Reload bool   `json:"reload,omitempty"`
	Error  string `json:"error,omitempty"`
}

// uiReloadKeys 列出“改完必须刷新页面才生效”的配置项。
//
// 这几个键的值由服务端在渲染首页时注入（见 runUI 里的 renderIndex：主题解析成 CSS 写进
// __GGT_THEME_CSS__，字体拼成 CSS 写进 __GGT_FONT_CSS__，其余设置整份写进 __GGT_SETTINGS__
// 供页面行为读用）。页面拿到的那份 HTML 已经是旧值，只改配置文件不会反映到当前页面上，
// 所以要在保存后提示刷新
//
// notify_timeout 也在这里，理由不那么直观：页面把它读成一个常量（通知倒计时的时长），
// 常量在页面加载时就定下了，改完不刷新的话，用户接下来看到的通知仍按旧时长消失
//
// font_ui / font_mono 在列表末尾，理由与主题一模一样：它们是两条 <style> 里现算出来的变量值，
// 改完只改得动配置文件，页面上那两套字体栈要等下一次请求才会跟着变
//
// 语言刻意不在这里：Go 进程的语言在启动时由 l10n.Init 定下，刷新页面也还是旧语言，
// 它需要的是重启 ggt。那件事由 settingNote 用一句话说清，不需要页面做任何动作
//
// 这是一份“页面已经烧进去”的键名清单，看起来与注册表分家了。之所以不放进注册表：
// 它描述的不是配置项自身的性质，而是本页面的渲染方式（哪些值被写死进了 HTML）。
// 测试断言这里每个键都真实存在，删配置项不会留下悬空的名字
var uiReloadKeys = []string{"theme", "theme_dark", "theme_light", "notify_timeout", "font_ui", "font_mono"}

// uiSettingsJSON 把设置面板那份视图序列化进首页，供页面行为读用。
//
// json.Marshal 默认把 < > & 转义成 \u003c 这类写法，因此内容里即使出现 "</script>"
// 也不会截断脚本标签；序列化失败时回退成空数组——页面拿不到配置就按各自的默认行为走
// （例如通知不自动消失），这比让整个首页渲染失败轻得多
func uiSettingsJSON(path string) []byte {
	b, err := json.Marshal(config.SettingsViewAt(path))
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
			writeUISettingsJSON(w, http.StatusOK, uiSettings{
				Path:  path,
				Items: config.SettingsViewAt(path),
			})
		case http.MethodPost:
			applySettings(w, r, path)
		default:
			writeUISettingsJSON(w, http.StatusMethodNotAllowed,
				uiSettings{Error: l10n.T("Only GET and POST are allowed", nil)})
		}
	}
}

// applySettings 按注册表写入面板提交的取值。
//
// 校验、解析、写入全部走 config.SetFromTextAt，与命令行的 "ggt config set" 是同一份实现；
// 这里只负责把多项请求拆开、把结果按项归拢。严格模式（取值必须在候选之内）是给网页的：
// 面板的控件只会给出候选，收到候选之外的取值说明请求不是页面发出来的
func applySettings(w http.ResponseWriter, r *http.Request, path string) {
	var req uiSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeUISettingsJSON(w, http.StatusBadRequest, uiSettingsResult{Error: l10n.T("Invalid request body", nil)})
		return
	}

	out := uiSettingsResult{
		Notes:  map[string]string{},
		Errors: map[string]string{},
	}

	// 按注册表的顺序写，而不是按 map 的遍历顺序：Go 里 map 的顺序是随机的，
	// 而“同时改两项、其中一项失败”时用户看到的报错顺序会一次一个样，问题很难复现
	for _, s := range config.Settings() {
		text, ok := req.Values[s.Key]
		if !ok {
			continue
		}
		if _, err := config.SetFromTextAt(path, s.Key, text, true); err != nil {
			out.Errors[s.Key] = err.Error()
			continue
		}
		out.Applied = append(out.Applied, s.Key)
		if note := settingNote(s.Key); note != "" {
			out.Notes[s.Key] = note
		}
		out.Reload = out.Reload || uiReloadsPage(s.Key)
	}

	for _, key := range req.Unset {
		s, ok := config.Lookup(key)
		if !ok {
			out.Errors[key] = errUnknownKey(key).Error()
			continue
		}
		if err := config.NotWritableError(s); err != nil {
			out.Errors[s.Key] = err.Error()
			continue
		}
		// 删键而不是写入默认值：与命令行 reset 的默认模式一致，文件里因此只留下
		// 用户真正改过的项，不会被一堆等于默认值的键塞满
		if err := config.UnsetKeyAt(path, s.Key); err != nil {
			out.Errors[s.Key] = err.Error()
			continue
		}
		out.Applied = append(out.Applied, s.Key)
		out.Reload = out.Reload || uiReloadsPage(s.Key)
	}

	// 一个键都没动（例如用户点保存时什么都没改）也回 200：这不是错误，
	// 报错反而会让面板弹一个用户无法处理的提示
	if len(out.Notes) == 0 {
		out.Notes = nil
	}
	if len(out.Errors) == 0 {
		out.Errors = nil
	}
	writeUISettingsJSON(w, http.StatusOK, out)
}

// writeUISettingsJSON 输出设置端点响应。与 diff 端点一样关掉 HTML 转义：
// 取值里有路径与主题名，转义后排查接口时看到的 JSON 无法阅读
func writeUISettingsJSON(w http.ResponseWriter, status int, out any) {
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
