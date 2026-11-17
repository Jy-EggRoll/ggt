package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 语言串的归一化与严格校验由 github.com/jy-eggroll/eggokit/l10n 负责，用例在 l10n 包内
// （TestNormalize / TestIsSupported）。本文件覆盖 cmd 侧的三件事：参数预扫描、按注册表
// 判定两条语言来源，以及“回退要提示”这一句的渲染。

// TestScanLangFlag 验证命令行语言参数的预扫描。
// 预扫描必须支持 pflag 的全部等价写法，并正确处理“未知 flag 的取值”与“-- 终止符”
// 这两个容易误判的场景。
func TestScanLangFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"长参数等号形式", []string{"--lang=zh-CN"}, "zh-CN"},
		{"长参数空格形式", []string{"--lang", "zh-CN"}, "zh-CN"},
		{"短参数空格形式", []string{"-l", "zh-CN"}, "zh-CN"},
		{"短参数粘连形式", []string{"-lzh-CN"}, "zh-CN"},
		{"子命令之后的语言参数", []string{"size", "-l", "zh-CN"}, "zh-CN"},
		{"无语言参数", []string{"size", "--low", "200"}, ""},
		{"空参数", []string{}, ""},
		// 未知 flag 的取值不能被误读成语言：--low 的值 200 应被 pflag 剥离
		{"未知 flag 的取值不被误读", []string{"--low", "200"}, ""},
		// 独立的 -- 之后停止 flag 解析，其后的 -l 不是 flag
		{"双横线后不解析", []string{"--", "-l", "zh-CN"}, ""},
		// 重复给出时以最后一个为准，与 pflag/cobra 的“后者覆盖”语义一致
		{"重复给出取后者", []string{"-l", "en", "--lang", "zh-CN"}, "zh-CN"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := scanLangFlag(c.args); got != c.want {
				t.Errorf("scanLangFlag(%v) = %q, want %q", c.args, got, c.want)
			}
		})
	}
}

// TestChooseLanguage 验证启动路径的语言判定与“回退要提示”。
//
// 重点不是“能不能读到语言”，而是两条：判定与注册表一致（zh 归一成 zh-CN，fr 被拒），
// 以及“有值但不可用”这件事必须交回调用方——静默回退正是这一轮要消除的行为。
func TestChooseLanguage(t *testing.T) {
	writeConfig := func(t *testing.T, body string) string {
		t.Helper()
		home := t.TempDir()
		t.Setenv("HOME", home)
		dir := filepath.Join(home, ".config", "go-git-ggt")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("建配置目录失败: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "ggt-config.json"), []byte(body), 0o644); err != nil {
			t.Fatalf("写配置失败: %v", err)
		}
		return home
	}

	t.Run("没有配置文件时交由默认语言", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		lang, notice := chooseLanguage(nil)
		if lang != "" || notice != nil {
			t.Errorf("无配置时应得到空串与 nil，实际 (%q, %+v)", lang, notice)
		}
	})

	t.Run("配置里的别名归一成规范标签", func(t *testing.T) {
		writeConfig(t, `{"language":"zh"}`)
		lang, notice := chooseLanguage(nil)
		if lang != "zh-CN" || notice != nil {
			t.Errorf("zh 应归一成 zh-CN 且不提示，实际 (%q, %+v)", lang, notice)
		}
	})

	t.Run("配置文件读不出来时静默回退", func(t *testing.T) {
		writeConfig(t, `{not json`)
		lang, notice := chooseLanguage(nil)
		if lang != "" || notice != nil {
			t.Errorf("JSON 非法时应静默回退，实际 (%q, %+v)", lang, notice)
		}
	})

	t.Run("配置里写了不支持的语言要带回提示", func(t *testing.T) {
		writeConfig(t, `{"language":"fr"}`)
		lang, notice := chooseLanguage(nil)
		if lang != "" {
			t.Errorf("不支持的语言应回退默认语言，实际 %q", lang)
		}
		if notice == nil || notice.source != languageFromConfig || notice.value != "fr" {
			t.Fatalf("应带回配置来源与原值，实际 %+v", notice)
		}
	})

	t.Run("类型不符同样要带回提示", func(t *testing.T) {
		writeConfig(t, `{"language":3}`)
		_, notice := chooseLanguage(nil)
		if notice == nil || notice.value != "3" {
			t.Fatalf("类型不符时应带回文件里的原值，实际 %+v", notice)
		}
	})

	t.Run("命令行优先于配置文件", func(t *testing.T) {
		writeConfig(t, `{"language":"fr"}`)
		lang, notice := chooseLanguage([]string{"--lang", "zh"})
		if lang != "zh-CN" || notice != nil {
			t.Errorf("命令行取值应优先并同样归一，实际 (%q, %+v)", lang, notice)
		}
	})

	t.Run("命令行写了不支持的语言也要提示", func(t *testing.T) {
		writeConfig(t, `{"language":"zh-CN"}`)
		lang, notice := chooseLanguage([]string{"--lang", "fr"})
		if lang != "" || notice == nil || notice.source != languageFromCommandLine || notice.value != "fr" {
			t.Errorf("应回退默认语言并提示命令行来源，实际 (%q, %+v)", lang, notice)
		}
	})
}

// TestLanguageNoticeText 验证回退提示在语言就绪后按最终语言渲染，并说清“写了什么、回退到哪”。
func TestLanguageNoticeText(t *testing.T) {
	initL10n(t, "en")
	notice := &languageNotice{source: languageFromConfig, value: "fr"}
	text := notice.text()
	for _, want := range []string{"fr", "English", "config file"} {
		if !strings.Contains(text, want) {
			t.Errorf("英文提示应含 %q：%q", want, text)
		}
	}

	initL10n(t, "zh-CN")
	text = notice.text()
	if !strings.Contains(text, "不受支持") || !strings.Contains(text, "fr") || !strings.Contains(text, "简体中文") {
		t.Errorf("中文提示应翻译并保留原值与回退语言：%q", text)
	}

	// 空值不能把句子撕出一个缺口
	initL10n(t, "en")
	empty := (&languageNotice{source: languageFromCommandLine, value: "  "}).text()
	if !strings.Contains(empty, "(empty)") || !strings.Contains(empty, "--lang") {
		t.Errorf("空取值应显示成 (empty) 并点明来源：%q", empty)
	}
}
