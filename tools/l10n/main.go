// tools/l10n 是 ggt 的文案提取工具，对应 VSCode 的 @vscode/l10n-dev。
//
//	go run ./tools/l10n export   # 扫描源码，生成并覆盖 en.json；重排各译文文件；打印报告
//	go run ./tools/l10n check    # 只读校验，供 CI 门禁使用（task l10n:check）
//
// 模型见 internal/i18n 的包注释：**英文源串本身就是消息 id**。因此 en.json 是生成物，
// 每次 export 整体覆盖；其余语言的译文文件由人工维护，本工具只做排序与校验，不增删内容。
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"ggt/internal/i18n"
	"ggt/internal/jsonfile"
)

// localesRelDir 是语言文件所在目录（相对仓库根）。
const localesRelDir = "internal/i18n/locales"

// repoRootMarker 用于从当前目录向上定位仓库根。
// 依赖 cwd 而不是向上找根，一旦从子目录运行就会扫不到任何文件而静默通过，
// 那是最坏的门禁失效形态，所以这里显式定位。
const repoRootMarker = "go.mod"

func main() {
	if len(os.Args) != 2 {
		usage()
	}

	root, err := findRepoRoot()
	if err != nil {
		fatal(err)
	}

	switch os.Args[1] {
	case "export":
		err = cmdExport(root)
	case "check":
		err = cmdCheck(root)
	case "-h", "--help", "help":
		usage()
		return
	default:
		usage()
	}
	if err != nil {
		fatal(err)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `用法: go run ./tools/l10n <命令>

命令:
  export   扫描源码提取消息，生成并覆盖 en.json，重排译文文件，打印待翻译/待迁移清单
  check    只读校验（语言文件与源码一致、已按字典序排列、可被 go-i18n 正常解析）

消息 id 即英文原文字符串，详见 internal/i18n 的包注释。
`)
	os.Exit(2)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "错误: "+err.Error())
	os.Exit(1)
}

// cmdExport 重新生成语言文件并打印报告。
func cmdExport(root string) error {
	res, err := scanSource(root)
	if err != nil {
		return err
	}
	if len(res.Messages) == 0 {
		return fmt.Errorf("未扫描到任何消息（已解析 %d 个文件）：扫描目录或识别规则可能已失效", res.Files)
	}

	// en.json 是生成物：内容为 { 源串: 源串 } 的自映射，整体覆盖
	en := make(map[string]string, len(res.Messages))
	for _, m := range res.Messages {
		en[m.Text] = m.Text
	}
	if err := writeFlat(localePath(root, i18n.DefaultLanguage), en); err != nil {
		return err
	}

	// 其余语言是人工维护的译文：只重新排序，不增删任何条目。
	// 先解析成功才写回，解析失败就退出——否则会把译者手改的文件覆盖成损坏内容
	for _, lang := range i18n.Supported() {
		if lang == i18n.DefaultLanguage {
			continue
		}
		path := localePath(root, lang)
		entries, err := readFlat(path)
		if err != nil {
			return err
		}
		if err := writeFlat(path, entries); err != nil {
			return err
		}
		reportTranslations(lang, path, en, entries)
	}

	reportUnwrapped(root, res)
	return nil
}

// cmdCheck 只读校验，任一问题都返回错误（退出码 1）。
func cmdCheck(root string) error {
	var problems []string

	res, err := scanSource(root)
	if err != nil {
		// 首参不是字面量之类的结构性问题，本身就是必须修的错误
		return err
	}
	if len(res.Messages) == 0 {
		problems = append(problems, "未扫描到任何消息，扫描目录或识别规则可能已失效")
	}

	// 复用运行期加载校验：平铺结构、保留字、消息数一致性都在 Init 里，
	// 不在这里重复实现一套，避免两处规则漂移
	if err := i18n.Init(i18n.DefaultLanguage); err != nil {
		problems = append(problems, "语言文件无法被 go-i18n 加载："+err.Error())
	}

	enPath := localePath(root, i18n.DefaultLanguage)
	en, err := readFlat(enPath)
	if err != nil {
		return err
	}

	expected := make(map[string]string, len(res.Messages))
	for _, m := range res.Messages {
		expected[m.Text] = m.Text
	}
	for _, m := range res.Messages {
		if _, ok := en[m.Text]; !ok {
			problems = append(problems, fmt.Sprintf("en.json 缺少消息 %q（%s）", m.Text, m.Pos))
		}
	}
	for k := range en {
		if _, ok := expected[k]; !ok {
			problems = append(problems, fmt.Sprintf("en.json 存在源码中已不存在的消息 %q，请运行 task l10n:export", k))
		}
	}

	// 规范形态校验：重新序列化后应与磁盘内容完全一致，否则说明未排序或格式不规范
	if err := checkCanonical(enPath); err != nil {
		problems = append(problems, err.Error())
	}

	for _, lang := range i18n.Supported() {
		if lang == i18n.DefaultLanguage {
			continue
		}
		path := localePath(root, lang)
		entries, err := readFlat(path)
		if err != nil {
			return err
		}
		if err := checkCanonical(path); err != nil {
			problems = append(problems, err.Error())
		}
		for k := range entries {
			if _, ok := en[k]; !ok {
				problems = append(problems, fmt.Sprintf("%s.json 存在失效译文 %q——英文原文已改动或该消息已删除，请同步删除该条目",
					lang, k))
			}
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return errors.New(strings.Join(problems, "\n       "))
	}
	// 成功时也输出一行：静默退出容易被误认为"门禁没跑"
	fmt.Printf("✓ 语言文件校验通过：%d 条消息，扫描 %d 个文件\n", len(res.Messages), res.Files)
	return nil
}

// reportTranslations 打印某个语言相对英文的翻译缺口。
// 缺失只作警告不阻断：翻译允许滞后于源码。
func reportTranslations(lang, path string, en, entries map[string]string) {
	var missing []string
	for k := range en {
		if _, ok := entries[k]; !ok {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)

	if len(missing) == 0 {
		fmt.Printf("✓ %s：%d 条译文，无缺口\n", lang, len(entries))
		return
	}
	fmt.Printf("! %s：%d 条译文，尚有 %d 条未翻译\n", lang, len(entries), len(missing))
	for _, k := range missing {
		fmt.Printf("    未翻译: %s\n", oneLine(k))
	}
}

// reportUnwrapped 打印尚未迁移的存量文案。
// 这不是错误而是迁移进度指标：模型迁移完成后应当归零。
func reportUnwrapped(root string, res *scanResult) {
	if len(res.Unwrapped) == 0 {
		fmt.Printf("✓ 源码中已无非 ASCII 字面量，迁移完成\n")
		return
	}
	byFile := map[string]int{}
	for _, u := range res.Unwrapped {
		byFile[u.Pos.Filename]++
	}
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)

	fmt.Printf("! 尚有 %d 处未被 T() 包裹的中文文案，分布在 %d 个文件（迁移完成后应归零）:\n",
		len(res.Unwrapped), len(files))
	for _, f := range files {
		fmt.Printf("    %s: %d 处\n", f, byFile[f])
	}
}

// localePath 返回某语言文件的绝对路径。
func localePath(root, lang string) string {
	return filepath.Join(root, localesRelDir, lang+".json")
}

// readFlat 读取一份语言文件为扁平映射。
// 文件不存在时返回空映射（首次 export 会创建它）。
func readFlat(path string) (map[string]string, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(buf, &raw); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s 的 %q 取值不是字符串：语言文件必须是平铺的「源串 -> 译文」对象", path, k)
		}
		out[k] = s
	}
	return out, nil
}

// writeFlat 以规范形态写入语言文件：字典序、2 空格缩进、不做 HTML 转义。
// 规范形态由 internal/jsonfile 提供，与配置文件写入共用同一份策略。
func writeFlat(path string, entries map[string]string) error {
	buf, err := jsonfile.Marshal(entries)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, buf, 0644)
}

// checkCanonical 校验文件已是规范形态（排序正确、缩进统一）。
func checkCanonical(path string) error {
	onDisk, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	entries, err := readFlat(path)
	if err != nil {
		return err
	}
	want, err := jsonfile.Marshal(entries)
	if err != nil {
		return err
	}
	if !bytes.Equal(onDisk, want) {
		return fmt.Errorf("%s 未按字典序排列或格式不规范，请运行 task l10n:export", path)
	}
	return nil
}

// findRepoRoot 从当前目录向上查找含 go.mod 的目录。
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, repoRootMarker)); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("未找到 %s，请在本仓库内运行", repoRootMarker)
		}
		dir = parent
	}
}

// oneLine 把多行消息压成单行，便于在报告里显示。
func oneLine(s string) string {
	return strings.ReplaceAll(s, "\n", " ⏎ ")
}
