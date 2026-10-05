// upgrade_target.go 定义 ggt 自升级的发布目标：仓库坐标、产物命名与备用下载源。
//
// 这些取值来自 ggt 自身的发布约定，与 eggokit/updater 的通用能力无关，因此留在命令层
// 而不是升级库内部，使升级器可以不加修改地被别的项目复用（flk 也用同一套库）。
package cmd

import "fmt"

const (
	// upstreamOwner 与 upstreamRepo 指向承载 Release 的仓库，必须是实际发布产物的仓库
	upstreamOwner = "Jy-EggRoll"
	upstreamRepo  = "ggt"

	// assetNamePrefix 是发布产物的固定文件名前缀，必须与 Taskfile 的构建输出名保持一致。
	// ggt 各 build 任务的产物形如 build/ggt-<os>-<arch>[.exe]，因此这里取 "ggt"；
	// 改名前缀或改名 Taskfile 时两处要一起改，否则升级器会找不到任何资产并误报“已是最新”
	assetNamePrefix = "ggt"

	// downloadProxyPrefix 是直连不稳定时供用户选择的备用下载代理前缀，与资产原始地址直接拼接。
	// 是否启用完全由用户在升级过程中的确认决定，程序不会静默换源——
	// 下载结果随后会被直接执行，用户必须清楚二进制来自哪个镜像
	downloadProxyPrefix = "https://gh-proxy.org/"
)

// supportedPlatforms 声明 ggt 实际发布产物的系统与架构组合，必须与 Taskfile 的 build-all 目标保持同步。
// 少列组合会让对应平台的用户收到“尚未提供发布产物”的报错，多列则不存在的组合会被判为无可用资产
// 并继续向前寻找旧版本——两种偏差都会让用户拿不到本该能装上的新版本
var supportedPlatforms = map[string]map[string]bool{
	"windows": {"386": true, "amd64": true, "arm64": true},
	"linux":   {"386": true, "amd64": true, "arm": true, "arm64": true},
	"darwin":  {"amd64": true, "arm64": true},
	"freebsd": {"amd64": true, "arm64": true},
}

// ggtAssetName 返回指定平台对应的资产文件名前缀。
// ok 为 false 表示 ggt 不为该平台发布产物，升级器会据此给出明确提示而不会误报“已是最新”
func ggtAssetName(goos, goarch string) (string, bool) {
	architectures, known := supportedPlatforms[goos]
	if !known || !architectures[goarch] {
		return "", false
	}

	prefix := fmt.Sprintf("%s-%s-%s", assetNamePrefix, goos, goarch)
	if goos == "windows" {
		// 把扩展名纳入前缀，使匹配结果精确到 Windows 产物本身，
		// 避免将来同时存在带与不带扩展名的同类文件时命中错误的目标
		prefix += ".exe"
	}
	return prefix, true
}
