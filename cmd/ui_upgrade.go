// ui_upgrade.go 是看板上的版本检查：对应命令行的 "ggt upgrade --check"。
//
// 为什么只做检查、不在网页里实现升级：这个进程正在跑的就是要被替换的那个可执行文件。
//   - 替换成功后当前服务仍然运行旧代码（Linux/macOS 上进程持有旧 inode，磁盘上的文件
//     被换掉不影响它继续跑），用户点完“升级”在页面上看不到任何变化，却多出一套
//     “到底升级成功没有、要不要重启服务”的状态需要维护
//   - Windows 上运行中的 exe 被系统占用，覆盖或改名都会失败，同一个按钮在部分平台上
//     注定失败，比没有这个按钮更糟
//   - 这个动作不可回滚，一旦把服务器自己的二进制换坏，整台机器上的 ggt 都不能用；
//     而同样的操作在终端里成本几乎为零（那时没有进程占用自身二进制，路径也更清楚）
//
// 结论：网页只提供“检查”，真正的升级留在命令行。这不是能力缺口，而是刻意保留的边界
package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/eggokit/logger"
	"github.com/jy-eggroll/eggokit/updater"
	"github.com/jy-eggroll/eggokit/upgradecli"
)

// uiUpgradeChecker 是版本检查的注入点。
//
// 检查要访问 GitHub Release，测试里不能依赖真实网络：把“怎么查”做成一个函数参数，
// 测试传入打桩实现，就能把整套 HTTP 处理流程（方法校验、字段映射、超时分支）完整跑一遍，
// 而被替换掉的正好是唯一需要联网的那一小段
type uiUpgradeChecker func(ctx context.Context, channel updater.Channel) (*updater.UpdateInfo, error)

// uiUpgradeCheckTimeout 是一次检查最多等多久再由网页给出结论。
//
// updater.Check 不接受 ctx（库内部自己发 HTTP 请求，见 eggokit/updater 的 Check），
// 因此只能在调用侧限时：到点回一条明确的错误，而不是让页面上的“正在检查”一直转下去。
// 取 20 秒是因为匿名访问 GitHub API 偶有限流重试，给足重试余量的同时仍远小于用户等待耐心
const uiUpgradeCheckTimeout = 20 * time.Second

// uiUpgradeResult 是 POST /api/upgrade-check 的响应体。
type uiUpgradeResult struct {
	// Current 是本地版本，取自发布构建注入的值（见 version.go）
	Current string `json:"current"`
	// Build 是本地构建时间
	Build string `json:"build,omitempty"`
	// Latest 是目标通道里的最高版本标签原文
	Latest string `json:"latest,omitempty"`
	// Comparable 为假表示本地版本无法参与比较（本地源码构建用时间戳充当版本号），
	// 此时 Latest 只是该通道里的最高版本，页面不能说成“有更新的版本”
	Comparable bool `json:"comparable"`
	// UpToDate 为真表示检查器确认没有可升级的目标（updater 的约定：info 为 nil 且无错误）
	UpToDate bool `json:"upToDate"`
	// Channel 是本次检查所在的通道，供页面显示
	Channel string `json:"channel,omitempty"`
	// Platform 是当前平台标签，供页面显示；本地构建没有产物时用户能据此判断原因
	Platform string `json:"platform,omitempty"`
	// Error 是失败原因（网络不可达、限流、平台没有发布产物等）的原话
	Error string `json:"error,omitempty"`
}

// handleUpgradeCheck 是 /api/upgrade-check 的处理函数。
//
// 只认 POST、且必须由用户显式触发：这条路绝不能被挂进页面的 5 秒轮询——查 Release
// 要出网，慢的时候几秒到十几秒，挂进轮询就等于每 5 秒发一次外网请求，既拖垮页面，
// 也会把匿名配额快速耗尽。页面侧只在点击时发一次请求（见 app.js 的绑定）
func handleUpgradeCheck(check uiUpgradeChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeUIUpgrade(w, http.StatusMethodNotAllowed, uiUpgradeResult{Error: l10n.T("Only POST is allowed", nil)})
			return
		}

		channel := updater.ChannelStable
		result := uiUpgradeResult{
			Current:  versionOptions().EffectiveVersion(),
			Build:    BuildTime,
			Channel:  l10n.T("stable", nil),
			Platform: upgradecli.PlatformLabel(),
		}

		// 限时与取消都靠这个带缓冲的通道：缓冲为 1 保证检查 goroutine 即使在超时之后
		// 才返回也能把自己写进通道并退出，不会永久阻塞在发送上
		type checkOutcome struct {
			info *updater.UpdateInfo
			err  error
		}
		done := make(chan checkOutcome, 1)
		go func() {
			info, err := check(r.Context(), channel)
			done <- checkOutcome{info: info, err: err}
		}()

		timer := time.NewTimer(uiUpgradeCheckTimeout)
		defer timer.Stop()

		var outcome checkOutcome
		select {
		case outcome = <-done:
		case <-timer.C:
			// 超时后不再等待：检查可能仍在后台跑完，但结果已经没人要，页面需要的是明确结论
			result.Error = l10n.T("The version check timed out; GitHub may be unreachable from this machine", nil)
			writeUIUpgrade(w, http.StatusGatewayTimeout, result)
			return
		case <-r.Context().Done():
			// 用户关掉页面或请求被中断：没有接收方了，直接结束
			return
		}

		if outcome.err != nil {
			result.Error = outcome.err.Error()
			writeUIUpgrade(w, http.StatusBadGateway, result)
			return
		}

		// info 为 nil 表示没有可升级的目标，这是成功结论而不是错误（updater.Check 的约定）
		if outcome.info == nil {
			result.UpToDate = true
			writeUIUpgrade(w, http.StatusOK, result)
			return
		}

		result.Comparable = outcome.info.CurrentComparable
		result.Latest = outcome.info.LatestVersion
		writeUIUpgrade(w, http.StatusOK, result)
	}
}

// uiUpgradeCheckerFromEnv 构造网页版本检查实际使用的检查器。
//
// 每次点击都新建一个 updater，而不是建一个跨请求复用：这个动作由用户点击驱动、频率极低，
// 复用省下的只有一次令牌查找，却会把上一次的失败状态与内部缓存带进下一次。命令行那一份
// （见 runUpgrade）建一次是因为它一个进程只跑一次升级流程
func uiUpgradeCheckerFromEnv() uiUpgradeChecker {
	return func(ctx context.Context, channel updater.Channel) (*updater.UpdateInfo, error) {
		u, err := updater.New(updater.Config{
			Owner:     ggtTarget.Owner,
			Repo:      ggtTarget.Repo,
			AssetName: ggtTarget.AssetName,
			// 静默报告器：这条路径只把结论回给网页。用终端报告器会把过程输出混进
			// `ggt ui` 的服务日志，而用户正看着页面，看不到那个终端
			Reporter:      updater.DiscardReporter(),
			TokenProvider: updater.EnvTokenProvider(""),
			ProxyPrefix:   ggtTarget.ProxyPrefix,
			UserAgent:     "ggt-updater",
		})
		if err != nil {
			return nil, err
		}
		// 只调用 Check：它只查 Release 并比版本，不下载、不触碰可执行文件。
		// Apply 在这条路径上刻意不提供调用点
		return u.Check(versionOptions().EffectiveVersion(), BuildTime, channel)
	}
}

// writeUIUpgrade 输出版本检查端点的响应，与其它端点一样关掉 HTML 转义
func writeUIUpgrade(w http.ResponseWriter, status int, out uiUpgradeResult) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// 检查结果取决于外部 Release 与网络，缓存留不得
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		// 响应头已发出，改不了状态码，只能记日志
		logger.Error(l10n.T("Failed to encode the upgrade response", nil), "error", err)
	}
}
