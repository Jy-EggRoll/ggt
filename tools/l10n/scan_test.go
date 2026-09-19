package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeGo 在临时仓库里写一个 Go 源文件。
func writeGo(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入 %s 失败: %v", rel, err)
	}
}

// TestScanSkipsForeignTypeParamT 断言不会把别处的类型参数 T 误当成消息函数。
//
// internal/worker 里存在 func Map[I any, T any]，其函数体内若出现 T(x)，
// 语法上与类型转换一致。提取器必须按"所在包是否定义了 func T(...)"限定作用域，
// 否则一次无关重构就会让门禁变红。
func TestScanSkipsForeignTypeParamT(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "internal/worker/worker.go", `package worker

func Map[I any, T any](items []I) []T { return nil }

func convert(x int) {
	_ = T(x)
}
`)

	res, err := scanSource(root)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if len(res.Messages) != 0 {
		t.Errorf("worker 包的 T(x) 不应被识别为消息调用，实得 %d 条: %+v", len(res.Messages), res.Messages)
	}
}

// TestScanRejectsNonLiteralArgument 断言首参不是字符串字面量时必须报错。
//
// 这是"源串即 id"模型的基础约束：id 必须能在源码里静态读出来，
// 否则无法生成语言文件。透传封装函数正是靠这条规则被拦下的。
func TestScanRejectsNonLiteralArgument(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "cmd/x.go", `package cmd

func T(msg string, data map[string]any) string { return msg }

func use(v string) {
	_ = T(v, nil)
}
`)

	_, err := scanSource(root)
	if err == nil {
		t.Fatal("首参为变量时应报错")
	}
	if !strings.Contains(err.Error(), "字符串字面量") {
		t.Errorf("错误信息应说明首参必须是字符串字面量，实得: %v", err)
	}
}

// TestScanRejectsReservedWord 断言命中 go-i18n 保留字的消息会被拒绝。
func TestScanRejectsReservedWord(t *testing.T) {
	for _, id := range []string{"Other", "hash", "translation"} {
		root := t.TempDir()
		writeGo(t, root, "cmd/x.go", `package cmd

func T(msg string, data map[string]any) string { return msg }

var _ = T("`+id+`", nil)
`)
		if _, err := scanSource(root); err == nil {
			t.Errorf("消息 id %q 命中保留字，应被拒绝", id)
		}
	}
}

// TestScanRejectsDirtyMultiLineMessage 断言多行消息不得含制表符或行尾空白。
//
// id 就是原文，源码里任何缩进或行尾空白的变化都会让 id 漂移、既有译文静默失效，
// 所以这条约束必须前移到提取阶段。
func TestScanRejectsDirtyMultiLineMessage(t *testing.T) {
	cases := map[string]string{
		"含制表符": `var _ = T("line one\n\tline two", nil)`,
		"行尾空白": `var _ = T("line one\nline two ", nil)`,
		"首尾空白": `var _ = T(" leading", nil)`,
	}
	for name, expr := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeGo(t, root, "cmd/x.go", `package cmd

func T(msg string, data map[string]any) string { return msg }

`+expr+`
`)
			if _, err := scanSource(root); err == nil {
				t.Errorf("%s 应被拒绝", name)
			}
		})
	}
}

// TestScanSeparatesWrappedAndUnwrapped 断言已迁移与未迁移文案被正确分开统计。
func TestScanSeparatesWrappedAndUnwrapped(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "cmd/x.go", `package cmd

func T(msg string, data map[string]any) string { return msg }

var wrapped = T("Already migrated", nil)
var legacy = "尚未迁移的中文"
var plain = "ascii only"
`)

	res, err := scanSource(root)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].Text != "Already migrated" {
		t.Errorf("消息集合不符: %+v", res.Messages)
	}
	if len(res.Unwrapped) != 1 || res.Unwrapped[0].Text != "尚未迁移的中文" {
		t.Errorf("待迁移集合不符: %+v", res.Unwrapped)
	}
}

// TestScanRecognizesSelectorForm 断言其他包以 i18n.T(...) 形式调用时同样能被识别。
func TestScanRecognizesSelectorForm(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "internal/config/c.go", `package config

import "ggt/internal/i18n"

var _ = i18n.T("Message from another package", nil)
`)

	res, err := scanSource(root)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].Text != "Message from another package" {
		t.Errorf("选择器形式的调用未被识别: %+v", res.Messages)
	}
}

// TestScanSkipsTestFiles 断言测试文件不参与扫描。
// 测试里会出现刻意不存在的消息 id（如 "no.such.key"），扫进来会造成误报。
func TestScanSkipsTestFiles(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "cmd/x_test.go", `package cmd

func T(msg string, data map[string]any) string { return msg }

var _ = T("test-only message", nil)
`)

	res, err := scanSource(root)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if len(res.Messages) != 0 {
		t.Errorf("测试文件不应参与扫描，实得 %+v", res.Messages)
	}
}

// TestMarshalCanonicalKeepsAngleBrackets 断言序列化不转义 < > &。
//
// 分桶标签等文案含这些字符，默认的 HTML 转义会写成 \u003c，既伤可读性也让 diff
// 无法评审；go-i18n 自身的 marshaler 同样关闭了该转义。
func TestMarshalCanonicalKeepsAngleBrackets(t *testing.T) {
	buf, err := marshalCanonical(map[string]string{"<{{.Low}}MB": "<{{.Low}}MB"})
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	got := string(buf)
	if !strings.Contains(got, "<{{.Low}}MB") {
		t.Errorf("尖括号被转义了: %s", got)
	}
	if strings.Contains(got, `\u003c`) {
		t.Errorf("不应出现 \\u003c 转义: %s", got)
	}
}

// TestMarshalCanonicalSortsKeys 断言序列化按字典序排列。
func TestMarshalCanonicalSortsKeys(t *testing.T) {
	buf, err := marshalCanonical(map[string]string{"zeta": "z", "alpha": "a", "mid": "m"})
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	iAlpha := strings.Index(string(buf), "alpha")
	iMid := strings.Index(string(buf), "mid")
	iZeta := strings.Index(string(buf), "zeta")
	if !(iAlpha < iMid && iMid < iZeta) {
		t.Errorf("键未按字典序排列: %s", buf)
	}
}
