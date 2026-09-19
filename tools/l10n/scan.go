// scan.go 从 Go 源码里提取 i18n 消息。
//
// 因为采用"英文源串即消息 id"的模型（见 internal/i18n 包注释），提取规则极其简单：
// T("...") 调用的第一个参数就是一条消息。不存在"某个字面量到底算不算文案"的
// 启发式判断，也因此不需要标记注释或白名单——这正是该模型相对"符号 key"方案的核心优势。
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"ggt/internal/i18n"
)

// i18nImportPath 是消息函数 T 的定义所在包。
// 除本包内的裸 T(...) 调用外，其他包需导入该路径后以选择器形式调用。
const i18nImportPath = "ggt/internal/i18n"

// scanDirs 是参与扫描的顶层目录（相对仓库根）。
var scanDirs = []string{"cmd", "internal"}

// literalRef 记录一处字符串字面量及其源码位置。
type literalRef struct {
	Text string
	Pos  token.Position
}

// scanResult 汇总一次源码扫描的结果。
type scanResult struct {
	// Messages 是被 T() 引用的消息，按文本去重、按文本排序
	Messages []literalRef
	// Unwrapped 是"含非 ASCII 字符但没被 T() 包住"的字面量，即尚未迁移的存量文案。
	// 它们是迁移进度的度量：迁移完成后应当为空
	Unwrapped []literalRef
	// Files 是实际解析的文件数，用于识别"一个文件都没扫到"这种门禁静默失效
	Files int
}

// scanSource 扫描仓库下所有非测试 Go 文件，提取消息与待迁移文案。
func scanSource(root string) (*scanResult, error) {
	paths, err := goFiles(root)
	if err != nil {
		return nil, err
	}

	fset := token.NewFileSet()
	type parsedFile struct {
		path string
		file *ast.File
	}
	files := make([]parsedFile, 0, len(paths))
	for _, p := range paths {
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("解析 %s 失败: %w", rel(root, p), err)
		}
		files = append(files, parsedFile{p, f})
	}

	// 先判定哪些目录（即包）自己定义了 func T(...)。
	// 只有这些包里的裸 T(...) 才算消息调用：worker 包里存在类型参数 T
	// （func Map[I any, T any]），不限定作用域的话 T(x) 这种类型转换会被误判成消息调用。
	declaresT := map[string]bool{}
	for _, pf := range files {
		for _, decl := range pf.file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "T" {
				declaresT[filepath.Dir(pf.path)] = true
			}
		}
	}

	res := &scanResult{Files: len(files)}
	seen := map[string]bool{}
	wrapped := map[token.Pos]bool{}

	for _, pf := range files {
		bareT := declaresT[filepath.Dir(pf.path)]
		alias := i18nAlias(pf.file)

		if err := collectMessages(root, fset, pf.file, bareT, alias, res, seen, wrapped); err != nil {
			return nil, err
		}
		collectUnwrapped(fset, pf.file, wrapped, res)
	}

	sort.Slice(res.Messages, func(i, j int) bool { return res.Messages[i].Text < res.Messages[j].Text })
	sort.Slice(res.Unwrapped, func(i, j int) bool {
		a, b := res.Unwrapped[i].Pos, res.Unwrapped[j].Pos
		if a.Filename != b.Filename {
			return a.Filename < b.Filename
		}
		return a.Line < b.Line
	})

	for i := range res.Messages {
		res.Messages[i].Pos.Filename = rel(root, res.Messages[i].Pos.Filename)
	}
	for i := range res.Unwrapped {
		res.Unwrapped[i].Pos.Filename = rel(root, res.Unwrapped[i].Pos.Filename)
	}

	return res, nil
}

// collectMessages 遍历单个文件，把所有 T() 调用的首参登记为消息。
func collectMessages(root string, fset *token.FileSet, file *ast.File, bareT bool, alias string,
	res *scanResult, seen map[string]bool, wrapped map[token.Pos]bool) error {

	var scanErr error
	ast.Inspect(file, func(n ast.Node) bool {
		if scanErr != nil {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok || !isMessageCall(call, bareT, alias) {
			return true
		}
		if len(call.Args) == 0 {
			scanErr = fmt.Errorf("%s: T() 调用缺少参数", position(root, fset, call.Pos()))
			return false
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			scanErr = fmt.Errorf("%s: T() 的首个参数必须是字符串字面量——消息 id 即英文原文，不能是变量、常量或拼接结果",
				position(root, fset, call.Args[0].Pos()))
			return false
		}
		text, err := strconv.Unquote(lit.Value)
		if err != nil {
			scanErr = fmt.Errorf("%s: 解析字符串字面量失败: %w", position(root, fset, lit.Pos()), err)
			return false
		}
		if err := validateMessage(text, position(root, fset, lit.Pos())); err != nil {
			scanErr = err
			return false
		}
		wrapped[lit.Pos()] = true
		if !seen[text] {
			seen[text] = true
			res.Messages = append(res.Messages, literalRef{Text: text, Pos: fset.Position(lit.Pos())})
		}
		return true
	})
	return scanErr
}

// collectUnwrapped 收集没有被 T() 包住的非 ASCII 字面量，即尚未迁移的存量文案。
func collectUnwrapped(fset *token.FileSet, file *ast.File, wrapped map[token.Pos]bool, res *scanResult) {
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING || wrapped[lit.Pos()] {
			return true
		}
		text, err := strconv.Unquote(lit.Value)
		if err != nil || !hasNonASCII(text) {
			return true
		}
		res.Unwrapped = append(res.Unwrapped, literalRef{Text: text, Pos: fset.Position(lit.Pos())})
		return true
	})
}

// isMessageCall 判断一次调用是否是消息调用。
//
// 允许两种形态：
//   - 裸 T(...)，仅当所在包自己定义了 T 时成立（避免命中别处的类型参数或同名函数）
//   - <别名>.T(...)，别名来自对本项目 i18n 包的导入
func isMessageCall(call *ast.CallExpr, bareT bool, alias string) bool {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return bareT && fun.Name == "T"
	case *ast.SelectorExpr:
		if alias == "" || fun.Sel.Name != "T" {
			return false
		}
		x, ok := fun.X.(*ast.Ident)
		return ok && x.Name == alias
	default:
		return false
	}
}

// i18nAlias 返回本文件引用 i18n 包时使用的本地名；未导入返回空串。
func i18nAlias(file *ast.File) string {
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != i18nImportPath {
			continue
		}
		if imp.Name != nil {
			// 点导入与空导入不会以选择器形式调用，视为没有可用别名
			if imp.Name.Name == "_" || imp.Name.Name == "." {
				return ""
			}
			return imp.Name.Name
		}
		return "i18n"
	}
	return ""
}

// validateMessage 校验一条消息能否安全地用作 id。
//
// 除保留字（会让整份语言文件解析失败）外，还强制多行原文保持"干净"：
// id 就是原文本身，源码里任何缩进或行尾空白的变化都会让 id 漂移，
// 使既有译文静默失效。把这条约束前移到提取阶段，比事后排查便宜得多。
func validateMessage(text string, pos string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("%s: 消息不能是空串或纯空白", pos)
	}
	if err := i18n.ValidateMessageID(text); err != nil {
		return fmt.Errorf("%s: %w", pos, err)
	}
	if strings.Contains(text, "\t") {
		return fmt.Errorf("%s: 消息含制表符，id 会随源码缩进变化，请改用空格", pos)
	}
	if strings.TrimSpace(text) != text {
		return fmt.Errorf("%s: 消息首尾有多余空白", pos)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimRight(line, " ") != line {
			return fmt.Errorf("%s: 消息存在行尾空白", pos)
		}
	}
	return nil
}

// hasNonASCII 判断字符串是否含非 ASCII **字母**。
//
// 判据刻意用 unicode.IsLetter 而不是"非 ASCII 字符"：消息 id 一律是英文，所以
// 非 ASCII 字母就是还没迁移的存量文案；而制表符之外的排版符号——分隔线 "─"、
// 箭头 "→"、"×" 等——是界面骨架而非文字，既不该翻译也不该被列为待办。
func hasNonASCII(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII && unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// goFiles 列出参与扫描的 Go 文件（跳过测试文件）。
// 扫描目录不存在时直接跳过：仓库未必同时拥有全部目录，缺失不是错误；
// 但"一个文件都没扫到"会在调用方被当成错误拦下，避免门禁静默失效。
func goFiles(root string) ([]string, error) {
	var out []string
	for _, dir := range scanDirs {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := d.Name()
			if d.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			out = append(out, path)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

// position 返回相对仓库根的源码位置，便于在报告中直接点击跳转。
func position(root string, fset *token.FileSet, pos token.Pos) string {
	p := fset.Position(pos)
	return fmt.Sprintf("%s:%d", rel(root, p.Filename), p.Line)
}

// rel 把绝对路径转成相对仓库根的路径，失败时原样返回。
func rel(root, path string) string {
	if path == "" {
		return path
	}
	r, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return r
}
