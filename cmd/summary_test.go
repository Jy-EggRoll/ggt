// summary_test.go 覆盖 summary 交互流程里可离线断言的部分：给用户看的提示文本怎么组装。
//
// “Processing ...” 这行在交互式确认之后才打印，端到端验证要喂进一次确认、还要等真实
// 提交与推送，代价远大于它验证的那一行字；因此这里直接断言消息的组装结果。
package cmd

import (
	"strings"
	"testing"

	"github.com/jy-eggroll/eggokit/l10n"
	"github.com/jy-eggroll/ggt/internal/locales"
)

// TestSummaryProcessingMsg 验证“正在处理哪个仓库”这一行：子模块带 [sub] 前缀，
// 顶层仓库仍只显示名字（与改动前逐字节一致）。
//
// 名字部分正是曾经丢掉子模块身份的地方：标题行 RepoLine 带着 [子]，紧挨着的那句
// 却是裸名，子模块与同名顶层仓库在终端里看起来一模一样。
func TestSummaryProcessingMsg(t *testing.T) {
	// [sub] 的译文随全局语言变化，显式初始化成英文，免得受同包其它测试切换语言的影响
	if err := l10n.Init(locales.Default, locales.Options()); err != nil {
		t.Fatalf("初始化 l10n 失败: %v", err)
	}

	cases := []struct {
		name        string
		isSubmodule bool
		want        string
	}{
		{name: "repo-a", want: "Processing repo-a ..."},
		{name: "sub/a", isSubmodule: true, want: "Processing [sub] sub/a ..."},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := summaryProcessingMsg(&dirtyRepo{name: c.name, isSubmodule: c.isSubmodule})
			if got != c.want {
				t.Errorf("summaryProcessingMsg = %q, 期望 %q", got, c.want)
			}
			if strings.Contains(got, "\x1b") {
				t.Errorf("%q 里出现了 ANSI 转义，这行提示不该着色", got)
			}
		})
	}
}
