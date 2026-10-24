// upgrade_target.go 定义 ggt 自升级的发布目标：仓库坐标、产物命名与备用下载源。
//
// 这些取值来自 ggt 自身的发布约定，与升级器的通用能力无关，因此留在命令层
// 而不是共享实现内部，使共享实现可以不修改地服务于别的项目（flk 也用它）。
package cmd

import "github.com/jy-eggroll/eggokit/upgradecli"

// ggtTarget 是 ggt 的发布目标，同时交给升级命令与网页版本检查使用。
//
// AssetPrefix 与 Platforms 必须与 Taskfile 的构建输出名、release.json 保持同步：
// 前缀与构建输出名不一致时，升级器拼出的资产名找不到任何产物，会误报“已是最新”；
// 平台表少列组合会让对应平台的用户收到“不提供该平台产物”的提示，多列不存在的组合
// 则会被判为无可用资产并继续向前寻找旧版本——两种偏差都会让用户拿不到本该能装上的新版本
var ggtTarget = upgradecli.Target{
	// Owner 与 Repo 指向承载 Release 的仓库，必须是实际发布产物的仓库
	Owner: "Jy-EggRoll",
	Repo:  "ggt",

	// ggt 各 build 任务的产物形如 build/ggt-<os>-<arch>[.exe]，因此前缀取 "ggt"
	AssetPrefix: "ggt",

	Platforms: map[string]map[string]bool{
		"windows": {"386": true, "amd64": true, "arm64": true},
		"linux":   {"386": true, "amd64": true, "arm": true, "arm64": true},
		"darwin":  {"amd64": true, "arm64": true},
		"freebsd": {"amd64": true, "arm64": true},
	},

	// ProxyPrefix 是直连不稳定时供用户选择的备用下载代理前缀，与资产原始地址直接拼接。
	// 是否启用完全由用户在升级过程中的确认决定，程序不会静默换源——
	// 下载结果随后会被直接执行，用户必须清楚二进制来自哪个镜像
	ProxyPrefix: "https://gh-proxy.org/",

	UserAgent: "ggt-updater",
}
