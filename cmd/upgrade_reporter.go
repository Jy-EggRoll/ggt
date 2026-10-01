// upgrade_reporter.go 把 eggokit/updater 的 Reporter 接口接到 ggt 的统一输出上。
//
// 升级器内部不引用任何终端库（见 eggokit/updater 的包注释），所有状态展示、进度绘制与
// 交互确认都经 Reporter 接口上抛，由这里决定如何呈现。它是升级器与 ggt 界面之间的唯一适配层：
// 升级命令本身只写 l10n.T(...) 文案，绝不直调 pterm.*，全部经由 output.go 的封装出口
package cmd

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/eggokit/updater"
	"github.com/pterm/pterm"
)

// ptermReporter 是升级器与 ggt 用户界面之间的唯一适配层
type ptermReporter struct {
	// assumeYes 对应命令的 --yes：为真时所有确认直接同意且不读取终端
	assumeYes bool

	// paused 在交互确认期间置位，使仍在下载 goroutine 中运行的进度刷新停止绘制，
	// 否则问题文本会被进度行的回车覆盖。读写分别发生在下载 goroutine 与命令主 goroutine 上，
	// 必须用原子类型而不是普通布尔
	paused atomic.Bool
}

// Info 输出过程性状态，走 output.go 的统一信息样式（含 pterm 的 INFO 前缀）。
//
// Reporter 的形参是「格式串 + 参数」，而 ggt 的输出封装只接收已渲染好的纯文本，
// 因此在这里用 Sprintf 收口一次。格式串由升级器传入（恒为 "%s"），译文只作为参数参与渲染，
// 不会被当作格式串解析，不存在 ggt 那条「译文里字面 % 被 fmt 吃掉」的隐患
func (r *ptermReporter) Info(format string, args ...any) {
	InfoMsg(fmt.Sprintf(format, args...))
}

// Warn 输出需要用户知晓但不阻断流程的问题（见 Reporter 接口说明）
func (r *ptermReporter) Warn(format string, args ...any) {
	WarnMsg(fmt.Sprintf(format, args...))
}

// Success 输出成功结论
func (r *ptermReporter) Success(format string, args ...any) {
	SuccessMsg(fmt.Sprintf(format, args...))
}

// Confirm 提出一个问题并等待用户决定，返回 false 表示用户拒绝。
//
// --yes 模式下直接同意，无需触碰终端；非交互环境下 pterm 的确认会读到 EOF 或直接挂住，
// 因此先用 stdinIsTerminal（与 config reset --all 同一判定）拒绝并向上升级器返回错误，
// 让升级器走「用户未能确认」的保守分支，而不是把命令永久挂起在等待输入上
func (r *ptermReporter) Confirm(question string) (bool, error) {
	if r.assumeYes {
		return true, nil
	}
	if !stdinIsTerminal() {
		return false, errors.New(l10n.T("Cannot ask for confirmation in a non-interactive environment; re-run with --yes", nil))
	}

	// 暂停在弹出前完成、恢复在返回后执行，保证问题文本不会被进度行的 \r 覆盖
	r.paused.Store(true)
	defer r.paused.Store(false)

	return pterm.DefaultInteractiveConfirm.WithDefaultValue(false).WithDefaultText(question).Show()
}

// Progress 开启一次下载进度展示
func (r *ptermReporter) Progress(label string) updater.Progress {
	return &downloadProgress{reporter: r, label: label}
}

// progressRefreshInterval 是进度重绘的最小间隔。
// 每收到一块数据都重绘会让终端闪烁，也会让速率与剩余时间的读数跳动到无法阅读
const progressRefreshInterval = 100 * time.Millisecond

// downloadProgress 用就地覆盖的单行文本展示下载进度、已传输量与剩余时间。
//
// 这里刻意不用 pterm 自带的进度条组件：它会自行接管光标与刷新节奏，
// 与确认弹窗同时出现时会争夺同一片终端区域，而此处的绘制完全由 reporter 的暂停标志统一调度；
// 实际的「就地重绘一行」动作则交给 output.go 的 ProgressLine 收口
type downloadProgress struct {
	reporter *ptermReporter
	label    string

	// drawn 记录是否真正绘制过：总长未知时不会绘制，结束时也就无需补换行
	drawn atomic.Bool

	// mu 串行化节流字段的读写：直连 goroutine 在被取消的瞬间仍可能刷新一次进度，
	// 而主流程此时可能已在启动新的下载
	mu          sync.Mutex
	startedAt   time.Time
	lastDrawnAt time.Time
}

// Update 汇报下载进度，总长未知或正处于暂停期时直接忽略。
// total 小于等于 0 表示总长未知（分块传输），此时只记录不绘制
func (p *downloadProgress) Update(done, total int64) {
	if total <= 0 || p.reporter.paused.Load() {
		return
	}

	now := time.Now()

	p.mu.Lock()
	if p.startedAt.IsZero() {
		p.startedAt = now
	}
	// 传输完成时必须强制绘制一次：否则受节流影响进度会停在 99% 之类的数字上，
	// 用户会以为下载没有收尾
	if done < total && now.Sub(p.lastDrawnAt) < progressRefreshInterval {
		p.mu.Unlock()
		return
	}
	p.lastDrawnAt = now
	startedAt := p.startedAt
	p.mu.Unlock()

	percent := float64(done) / float64(total) * 100
	// 最后一块数据到达前不能显示 100.0%：浮点除法会把 99.99% 四舍五入成 100.0%，
	// 让用户以为下载已经收尾
	if done < total && percent >= 100 {
		percent = 99.9
	}

	line := fmt.Sprintf("%s: %.1f%% (%s/%s)", p.label, percent, updater.FormatSize(done), updater.FormatSize(total))

	// 仅在未完成时给出速率与剩余时间，完成时这两项已无意义
	if done < total {
		// 速率取整体平均值而非瞬时值：瞬时值在抖动网络下会让剩余时间剧烈跳动
		if elapsed := now.Sub(startedAt); elapsed > 0 {
			if speed := float64(done) / elapsed.Seconds(); speed > 0 {
				line += fmt.Sprintf("  %s/s", updater.FormatSize(int64(speed)))
				// 不足一秒的剩余时间显示出来只是噪音，反而让人以为卡住了
				if remaining := time.Duration(float64(total-done) / speed * float64(time.Second)); remaining >= time.Second {
					line += "  " + l10n.T("remaining {{.Time}}", map[string]any{"Time": updater.FormatDuration(remaining)})
				}
			}
		}
	}

	ProgressLine(line)
	p.drawn.Store(true)
}

// Done 在绘制过进度后补一个换行，使后续输出从新行开始
func (p *downloadProgress) Done() {
	if p.drawn.Load() {
		ProgressLineEnd()
	}
}
