// remote_test 对 cmd 包中 remote 命令的纯函数做单元测试
// 覆盖范围：parseRemoteURL（URL → host/path）、buildHTTPSURL、buildSSHURL
// 以及 detectProtocol（协议判定）
//
// 这些函数此前是 0% 覆盖，且 remote 命令的核心流程 doSwitchRemote 完全依赖它们：
// 先 parseRemoteURL 解析出 host+path，再由 detectProtocol 判断当前协议是否与目标一致，
// 最后按目标协议调用两个 build* 函数重写 URL。因此这里的用例都直接对应线上行为，
// 一旦正则或判定逻辑变动，测试会立刻暴露
//
// 需要特别说明的实测结论（下述断言均以当前代码为准，不做“理应如此”的假设）：
//  1. remoteURLRegex 没有 (?i) 标志，所以只认小写的 https:// / http:// / git@ 前缀，
//     HTTPS://、Git@ 这类大小写混写会在 parseRemoteURL 处直接失败，尽管 detectProtocol
//     经 ToLower 后能正确判出 HTTPS/SSH —— 两者对大小写形态的容忍度并不一致，见
//     TestRemoteURLSchemeCaseAsymmetry
//  2. detectProtocol 只做 HasPrefix(lower, "http")，不做 trim，也不校验 URL 是否合法，
//     所以任何非 http 开头的串（含空串、本地路径、纯文本）都会落到 SSH 分支，见
//     TestDetectProtocol
//  3. 带端口的 URL 会把端口并进 path（正则的 host 分组 [^:/]+ 遇到 : 就停），见
//     TestParseRemoteURL 中 port 用例，属疑似缺陷，仅在报告中列出，不改代码
package cmd

import "testing"

// TestParseRemoteURL 验证从各种远程 URL 形态中提取 host 与 path 的结果
// 成功用例断言 host/path 两个返回值，失败用例断言 error 非 nil（错误文案经 l10n 翻译，
// 会随语言变化，故不比对具体字符串）
func TestParseRemoteURL(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantErr  bool
		wantHost string
		wantPath string
	}{
		// 四种主流写法：HTTPS 带/不带 .git 后缀、SSH scp-like 带/不带 .git 后缀
		// 正则末尾的 (?:\.git)? 是可选的，两种都能解析出同样的 host/path
		{"https 带 .git", "https://github.com/a/b.git", false, "github.com", "a/b"},
		{"https 不带 .git", "https://github.com/a/b", false, "github.com", "a/b"},
		{"http 带 .git", "http://github.com/a/b.git", false, "github.com", "a/b"},
		{"git@ 带 .git", "git@github.com:a/b.git", false, "github.com", "a/b"},
		{"git@ 不带 .git", "git@github.com:a/b", false, "github.com", "a/b"},

		// 多级路径：组/子组/仓库，正则的 (.+?) 允许路径含斜杠，只去掉结尾的 .git
		{"多级路径", "https://github.com/group/sub/repo.git", false, "github.com", "group/sub/repo"},
		{"git@ 多级路径", "git@gitlab.com:group/sub/repo.git", false, "gitlab.com", "group/sub/repo"},
		{"gitee 主机", "git@gitee.com:a/b.git", false, "gitee.com", "a/b"},

		// 自定义主机名与子域：host 分组是 [^:/]+，域名里的点不在排除集内，可完整保留
		{"自建 gitlab 主机", "https://git.example.com/a/b.git", false, "git.example.com", "a/b"},
		{"子域主机带端口（端口并入 path）", "https://git.corp.example.com:80/a/b.git", false, "git.corp.example.com", "80/a/b"},

		// 末尾斜杠：正则的 (?:/)? 只吞掉一个结尾斜杠，.git 之后再带斜杠同样能解析
		{"末尾斜杠（无 .git）", "https://github.com/a/b/", false, "github.com", "a/b"},
		{"末尾斜杠（有 .git）", "https://github.com/a/b.git/", false, "github.com", "a/b"},

		// 首尾空白：parseRemoteURL 内部先 TrimSpace，所以 git 输出常见的换行也能正确解析
		// 这一点与 detectProtocol 不同（后者不 trim），见 TestDetectProtocol 的空白用例
		{"首尾空格", "  git@github.com:a/b.git  ", false, "github.com", "a/b"},
		{"首尾换行制表符", "\thttps://github.com/a/b.git\n", false, "github.com", "a/b"},

		// 大小写混写：正则无 (?i)，只认小写前缀，因此这些形态解析失败
		// 注意 detectProtocol 对同样的串会判成 HTTPS/SSH，两者容忍度不一致
		{"大写 HTTPS 前缀", "HTTPS://github.com/a/b.git", true, "", ""},
		{"首字母大写 Http 前缀", "Http://github.com/a/b.git", true, "", ""},
		{"大写 Git@ 前缀", "Git@github.com:a/b.git", true, "", ""},
		{"全大写 GIT@ 前缀", "GIT@github.com:a/b.git", true, "", ""},
		{"全大写域名与路径", "HTTP://GITHUB.COM/A/B.GIT", true, "", ""},

		// .git 后缀剥离是大小写敏感的：(?:\.git)? 匹配不到 ".GIT"
		// 于是 path 原样保留 ".GIT"，后续 buildHTTPSURL 会再拼一个 .git，属疑似缺陷
		{"大写 .GIT 后缀不被剥离", "https://github.com/a/b.GIT", false, "github.com", "a/b.GIT"},
		// 只剥离一层 .git：末尾是 .git.git 时留下一层
		{"双层 .git 后缀只剥一层", "https://github.com/a/b.git.git", false, "github.com", "a/b.git"},
		// 路径中间的 .git 属于目录名，不应误当后缀剥离
		{"路径中的 .git 目录", "https://github.com/a.git/b", false, "github.com", "a.git/b"},

		// 无法识别的输入：正则要求 scheme 后必须有 host 与非空的 /path
		// 下列输入 FindStringSubmatch 返回 nil，统一走 error 分支
		{"空串", "", true, "", ""},
		{"纯空白", "   ", true, "", ""},
		{"纯文本", "not a url", true, "", ""},
		{"本地绝对路径", "/home/user/repo", true, "", ""},
		{"本地相对路径", "./repo", true, "", ""},
		{"上级相对路径", "../repo", true, "", ""},
		{"Windows 路径", `C:\Users\repo`, true, "", ""},
		{"缺少 scheme 的主机路径", "github.com/a/b", true, "", ""},
		{"只有主机", "https://github.com", true, "", ""},
		{"主机后只有斜杠", "https://github.com/", true, "", ""},
		{"只有 scheme", "https://", true, "", ""},
		{"git@ 后只有冒号", "git@github.com:", true, "", ""},
		// ssh:// 与 git:// 不在正则的 scheme 白名单里（只有 https?:// 和 git@）
		// 所以标准的 ssh:// 形式反而不被支持，属疑似能力缺口，仅记录
		{"ssh:// 形式不支持", "ssh://git@github.com/a/b.git", true, "", ""},
		{"ssh:// 带端口不支持", "ssh://git@github.com:2222/a/b.git", true, "", ""},
		{"git:// 形式不支持", "git://github.com/a/b.git", true, "", ""},
		// 形似 http 但并非合法 scheme，同样要求 host 与 path 齐备，故失败
		{"非 http 的 http 前缀", "httpsssh://github.com/a/b.git", true, "", ""},

		// 含凭据的 URL 会被 host 分组在第一个冒号处截断，整体解析成
		// host=user、path=pass@github.com/a/b，偏离真实语义，属疑似缺陷
		{"含凭据的 URL 被误解析", "https://user:pass@github.com/a/b.git", false, "user", "pass@github.com/a/b"},
	}

	for _, c := range cases {
		// 用子测试命名，跑 -v 时能直接看出是哪个形态失败
		t.Run(c.name, func(t *testing.T) {
			got, err := parseRemoteURL(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("parseRemoteURL(%q) 期望返回 error，实际得到 host=%q path=%q", c.in, got.host, got.path)
				}
				// 失败时不应返回半成品信息，避免调用方误用
				if got != nil {
					t.Fatalf("parseRemoteURL(%q) 失败时应返回 nil，实际 %+v", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseRemoteURL(%q) 不应报错，实际 err=%v", c.in, err)
			}
			if got == nil {
				t.Fatalf("parseRemoteURL(%q) 成功时不应返回 nil", c.in)
			}
			if got.host != c.wantHost || got.path != c.wantPath {
				t.Errorf("parseRemoteURL(%q) = {host:%q path:%q}, 期望 {host:%q path:%q}",
					c.in, got.host, got.path, c.wantHost, c.wantPath)
			}
		})
	}
}

// TestBuildHTTPSURL 验证按 host+path 拼装 HTTPS 地址的规则
// 无论输入是 http 还是 ssh，输出一律是 https 且强制补 .git 后缀
// 这也是用户可见的最终落盘地址，必须逐字断言
func TestBuildHTTPSURL(t *testing.T) {
	cases := []struct {
		name string
		host string
		path string
		want string
	}{
		{"普通仓库", "github.com", "a/b", "https://github.com/a/b.git"},
		{"多级路径", "github.com", "group/sub/repo", "https://github.com/group/sub/repo.git"},
		{"自建主机", "git.example.com", "a/b", "https://git.example.com/a/b.git"},
		{"gitlab", "gitlab.com", "group/sub/repo", "https://gitlab.com/group/sub/repo.git"},
		{"gitee", "gitee.com", "a/b", "https://gitee.com/a/b.git"},
		// path 已带 .git 时不做去重，会拼出双后缀；此处固定住该行为，
		// 提醒调用方必须传入 parseRemoteURL 剥离后的 path，而不是原始路径
		{"path 已带 .git 会拼出双后缀", "github.com", "a/b.git", "https://github.com/a/b.git.git"},
		// 空 path 不会被本函数拦截，属调用前约定（parseRemoteURL 保证 path 非空）
		{"空 path 不做校验", "github.com", "", "https://github.com/.git"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			info := &remoteInfo{host: c.host, path: c.path}
			if got := buildHTTPSURL(info); got != c.want {
				t.Errorf("buildHTTPSURL({host:%q path:%q}) = %q, 期望 %q", c.host, c.path, got, c.want)
			}
		})
	}
}

// TestBuildSSHURL 验证按 host+path 拼装 SSH（scp-like）地址的规则
// 固定形态为 git@HOST:PATH.git，冒号分隔而非斜杠，同样强制补 .git
func TestBuildSSHURL(t *testing.T) {
	cases := []struct {
		name string
		host string
		path string
		want string
	}{
		{"普通仓库", "github.com", "a/b", "git@github.com:a/b.git"},
		{"多级路径", "github.com", "group/sub/repo", "git@github.com:group/sub/repo.git"},
		{"自建主机", "git.example.com", "a/b", "git@git.example.com:a/b.git"},
		{"gitlab", "gitlab.com", "group/sub/repo", "git@gitlab.com:group/sub/repo.git"},
		{"gitee", "gitee.com", "a/b", "git@gitee.com:a/b.git"},
		// 与 buildHTTPSURL 相同的约定：path 已带 .git 时不去重
		{"path 已带 .git 会拼出双后缀", "github.com", "a/b.git", "git@github.com:a/b.git.git"},
		{"空 path 不做校验", "github.com", "", "git@github.com:.git"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			info := &remoteInfo{host: c.host, path: c.path}
			if got := buildSSHURL(info); got != c.want {
				t.Errorf("buildSSHURL({host:%q path:%q}) = %q, 期望 %q", c.host, c.path, got, c.want)
			}
		})
	}
}

// TestDetectProtocol 验证协议判定
// 历史 bug：曾用大小写敏感的 HasPrefix(raw, "http")，而 processSwitchResults 传入的是大写
// "HTTPS"/"SSH"，导致 detectProtocol("HTTPS") 匹配不到 "http" 前缀、被判成 SSH，
// 于是对外显示成 "SSH → SSH" 这种与实际切换方向相反的信息
// 修复方式是先 ToLower 再判前缀，所以本用例里“大写目标串”必须仍然判成自己本身的协议
func TestDetectProtocol(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// 回归用例：这两个串正是 processSwitchResults 内部传进来的 target 大写形式
		// 若哪天又改回大小写敏感判断，这两条会立刻失败
		{"大写 HTTPS 目标串（历史误判点）", "HTTPS", "HTTPS"},
		{"大写 SSH 目标串", "SSH", "SSH"},
		{"小写 https 目标串", "https", "HTTPS"},
		{"小写 ssh 目标串", "ssh", "SSH"},

		// 各大小写形态的 http 前缀都应判为 HTTPS
		{"标准 HTTPS URL", "https://github.com/a/b.git", "HTTPS"},
		{"小写 http URL", "http://github.com/a/b.git", "HTTPS"},
		{"大写 HTTP URL", "HTTP://github.com/a/b.git", "HTTPS"},
		{"混合大小写 HtTpS URL", "HtTpS://github.com/a/b.git", "HTTPS"},

		// 非 http 开头一律落到 SSH 分支：scp-like、ssh:// 与 git:// 都正确
		{"scp-like SSH URL", "git@github.com:a/b.git", "SSH"},
		{"大写 Git@ SSH URL", "Git@github.com:a/b.git", "SSH"},
		{"ssh:// URL", "ssh://git@github.com/a/b.git", "SSH"},
		{"ssh:// 带端口 URL", "ssh://git@github.com:2222/a/b.git", "SSH"},
		{"git:// URL", "git://github.com/a/b.git", "SSH"},

		// 判定只看前缀、不看内容是否合法，所以下列非法/空输入也不会报错
		// 而是统一返回 SSH（对 remote 命令而言，非 http 即视为 SSH 语义）
		{"空串按 SSH 处理", "", "SSH"},
		{"纯空白按 SSH 处理", "   ", "SSH"},
		{"纯文本按 SSH 处理", "not a url", "SSH"},
		{"本地路径按 SSH 处理", "/home/user/repo", "SSH"},
		{"不带协议的主机路径按 SSH 处理", "github.com/a/b", "SSH"},

		// 前导空白不会被 trim：带前导空格的 https 串判成 SSH
		// 实际调用方都会先 TrimSpace（doSwitchRemote / toggleCurrentRepo），
		// 所以线上不触发；此处固定行为，提示本函数自身不对空白负责
		{"前导空格的 https 判为 SSH", " https://github.com/a/b.git", "SSH"},
		{"制表符开头的 https 判为 SSH", "\thttps://github.com/a/b.git\n", "SSH"},

		// 前缀判定很宽松：只要以 http 开头就算 HTTPS，不校验剩余部分
		{"非法的 http 前缀仍判为 HTTPS", "httpsssh://github.com/a/b.git", "HTTPS"},
		{"只有 http 四字母判为 HTTPS", "http", "HTTPS"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := detectProtocol(c.in); got != c.want {
				t.Errorf("detectProtocol(%q) = %q, 期望 %q", c.in, got, c.want)
			}
		})
	}
}

// TestRemoteURLRoundTrip 验证 SSH ↔ HTTPS 互转的往返一致性
// doSwitchRemote 的切换路径是“解析当前 URL → 按目标协议重建 URL”，
// 因此对任一可识别的形态都必须满足：
//   - parse → buildHTTPSURL → parse 得到同一份 host/path
//   - parse → buildSSHURL  → parse 得到同一份 host/path
//
// 也就是说往返不丢信息，反复切换不会让地址逐次劣化
func TestRemoteURLRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"https 带 .git", "https://github.com/a/b.git"},
		{"https 不带 .git", "https://github.com/a/b"},
		{"http 带 .git", "http://github.com/a/b.git"},
		{"git@ 带 .git", "git@github.com:a/b.git"},
		{"git@ 不带 .git", "git@github.com:a/b"},
		{"多级路径", "https://github.com/group/sub/repo.git"},
		{"自建主机", "git@git.example.com:a/b.git"},
		{"末尾斜杠", "https://github.com/a/b.git/"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base, err := parseRemoteURL(c.in)
			if err != nil {
				t.Fatalf("前置解析 %q 失败: %v", c.in, err)
			}

			// 转 HTTPS：重建后重新解析，host/path 必须与原始一致
			httpsURL := buildHTTPSURL(base)
			viaHTTPS, err := parseRemoteURL(httpsURL)
			if err != nil {
				t.Fatalf("buildHTTPSURL 产出 %q 无法回解析: %v", httpsURL, err)
			}
			if *viaHTTPS != *base {
				t.Errorf("HTTPS 往返后信息改变: 原始 %+v, 回解析 %+v (URL=%q)", *base, *viaHTTPS, httpsURL)
			}

			// 转 SSH：同上，保证反方向也不丢信息
			sshURL := buildSSHURL(base)
			viaSSH, err := parseRemoteURL(sshURL)
			if err != nil {
				t.Fatalf("buildSSHURL 产出 %q 无法回解析: %v", sshURL, err)
			}
			if *viaSSH != *base {
				t.Errorf("SSH 往返后信息改变: 原始 %+v, 回解析 %+v (URL=%q)", *base, *viaSSH, sshURL)
			}

			// 两次切换应回到出发形态（对已规范化的输入而言地址逐字相同）
			// 若原始输入不规范（不带 .git、带尾斜杠），则回到的是规范化形态，
			// 这里只要求再次往返稳定，不要求与原始串逐字相等
			back, err := parseRemoteURL(buildHTTPSURL(viaSSH))
			if err != nil {
				t.Fatalf("二次切换回解析失败: %v", err)
			}
			if *back != *base {
				t.Errorf("二次往返信息漂移: 原始 %+v, 二次往返 %+v", *base, *back)
			}

			// 协议判定要与构建出的形态自洽：这是 doSwitchRemote 判“已一致、无需切换”的依据，
			// 若这里不自洽，用户会看到同一个仓库被判成两种协议
			if got := detectProtocol(httpsURL); got != "HTTPS" {
				t.Errorf("detectProtocol(%q) = %q, 期望 HTTPS", httpsURL, got)
			}
			if got := detectProtocol(sshURL); got != "SSH" {
				t.Errorf("detectProtocol(%q) = %q, 期望 SSH", sshURL, got)
			}
		})
	}
}

// TestRemoteURLSchemeCaseAsymmetry 固定 parseRemoteURL 与 detectProtocol 对大小写形态的
// 不一致处理，作为疑似缺陷的回归记录（只断言现状，不改代码）
// 现象：detectProtocol 经 ToLower 后认为 "HTTPS://github.com/a/b.git" 是 HTTPS，
// 但 parseRemoteURL 因正则无 (?i) 而报错
// 影响：doSwitchRemote 先解析、后判协议，遇到这种 origin 会直接走 error 分支并提示
// “无法解析 URL”，而不会因为“已经是 HTTPS”而跳过；反之 Git@ 形态同理
// 若后续给正则加上 (?i)（并注意 .git 后缀的匹配），本测试会失败，届时应当连同注释一起更新
func TestRemoteURLSchemeCaseAsymmetry(t *testing.T) {
	cases := []struct {
		name string
		in   string
		// probe 期望的 detectProtocol 结果
		detect string
	}{
		{"大写 HTTPS 前缀", "HTTPS://github.com/a/b.git", "HTTPS"},
		{"混合大小写 Http 前缀", "Http://github.com/a/b.git", "HTTPS"},
		{"大写 Git@ 前缀", "Git@github.com:a/b.git", "SSH"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := detectProtocol(c.in); got != c.detect {
				t.Fatalf("detectProtocol(%q) = %q, 期望 %q（协议判定本身应大小写不敏感）", c.in, got, c.detect)
			}
			// 现状：协议判得出，但 URL 解析不了，形成能力落差
			if _, err := parseRemoteURL(c.in); err == nil {
				t.Errorf("parseRemoteURL(%q) 当前应因缺少 (?i) 而报错；若已支持大小写混写，请同步更新本用例与源码注释", c.in)
			}
		})
	}
}
