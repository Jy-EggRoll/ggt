package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"unicode"
)

// CommandError 是 git 以非零状态退出时的错误，带出 git 写在 stderr 上的原话。
//
// 两个输出封装原先只把 *exec.ExitError 往上交，调用方拿到的文案永远只有
// "exit status 128"；而“不是 git 仓库”“safe.directory 拒绝”“网络不可达”这些
// 真正需要使用者行动的原因全在 stderr 里，被无声丢掉了。
type CommandError struct {
	// Stderr 是 git 的原始标准错误，保留未压缩的内容供调用方按需查看。
	Stderr string
	// Err 是底层错误（通常是 *exec.ExitError），供 errors.Is/As 判断。
	Err error
}

// Error 返回单行描述：底层错误文本 + stderr 的首个非空片段。
// 退出码就在底层错误文本里（"exit status 128"），不再单独存一份。
//
// 只取首个片段是因为 git 的多行 stderr 后半段通常是 hint/advice，重复的是同一个原因；
// 整段塞进一行日志既会挤掉上下文，也让这类提示更难读。
// stderr 为空时退回原错误文本，而不是凭空造一个原因。
func (e *CommandError) Error() string {
	if reason := firstLine(e.Stderr); reason != "" {
		return fmt.Sprintf("%v: %s", e.Err, reason)
	}
	return e.Err.Error()
}

// Unwrap 让 errors.Is/As 能穿透到 *exec.ExitError 等原始错误。
func (e *CommandError) Unwrap() error { return e.Err }

// firstLine 返回 s 中第一个非空片段：\r 与 \n 都算断行，控制字符一律丢弃。
//
// 进度输出会用 \r 回到行首覆写（如 "Receiving objects: 50%\r"）。只按 \n 分行的话，
// 这行告警会把终端光标拽回行首，覆盖掉前面的文案；控制字符同理会伪造出换行与清屏。
func firstLine(s string) string {
	for _, seg := range strings.FieldsFunc(s, isLineBreak) {
		if seg = cleanSegment(seg); seg != "" {
			return seg
		}
	}
	return ""
}

// isLineBreak 把 \r 与 \n 都当作断行，覆盖 git 的 \n 输出与进度输出的 \r 覆写。
func isLineBreak(r rune) bool { return r == '\n' || r == '\r' }

// cleanSegment 去掉控制字符并修剪首尾空白。
// 逐 rune 处理，中文等多字节文本不受影响。
func cleanSegment(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// newCommandError 在命令失败时构造 CommandError；ctx 已结束时改为回报 ctx 的原因。
//
// ctx 结束意味着命令是被我们终止的：stderr 要么为空，要么只是被杀前写了一半，
// 拿它当 git 的报错会把“超时/取消”说成 git 的语法错误。这里改用 ctx.Err() 作原因
// （context deadline exceeded / context canceled），同时用 %w 保住底层错误，
// 让 errors.Is/As 依旧可用。
func newCommandError(ctx context.Context, stderr string, err error) error {
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		// 命令还没来得及启动就因 ctx 失败时，err 本身就是 ctxErr，
		// 再包一层会得到“context canceled: context canceled”
		if errors.Is(err, ctxErr) {
			return err
		}
		return fmt.Errorf("%w: %w", ctxErr, err)
	}
	return &CommandError{Stderr: stderr, Err: err}
}

// stderrOf 取出 cmd.Output() 收集到的 stderr。Output 只在 cmd.Stderr 为 nil 时
// 才把它放进 ExitError，因此调用方不要自行设置 cmd.Stderr。
func stderrOf(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return string(exitErr.Stderr)
	}
	return ""
}

// stderrLimit 是流式读取那条路径为 stderr 保留的字节上限。
//
// 告警只需要首行，git 的 fatal 提示远短于这个数；而 stderr 的体量不受我们控制
// （进度输出、调试变量都能把它撑到几十 MB），设上限才符合 runWithRecordLimit
// 限制内存的初衷。
const stderrLimit = 8 * 1024

// cappedBuffer 最多保留 limit 字节，超出部分直接丢弃。
//
// 不设上限的 bytes.Buffer 会让 stderr 洪泛把内存吃光；而用“超限即报错”的写法，
// os/exec 的拷贝 goroutine 会因为 Write 返回错误而提前结束，子进程随后可能卡在
// 写满的管道上。这里始终返回完整的写入长度，只是不再累积，管道照常排空。
type cappedBuffer struct {
	buf   []byte
	limit int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - len(b.buf); room > 0 {
		if len(p) > room {
			b.buf = append(b.buf, p[:room]...)
		} else {
			b.buf = append(b.buf, p...)
		}
	}
	return len(p), nil
}

// String 返回已保留的部分；被截断与否不影响取首行。
func (b *cappedBuffer) String() string { return string(b.buf) }
