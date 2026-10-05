// upgrade_target_test 守护“发布清单”与“升级器的平台表”之间的一致性。
//
// 这两处必须描述同一批平台：release.json 决定构建出哪些产物、发布说明里给哪些下载链接，
// supportedPlatforms 决定升级器认哪些平台。任一处单独改动都可能造成静默故障——
// 清单多一个平台而升级器不认，对应平台的用户会被判为“没有可用资产”；升级器多一个而
// 清单没有，用户会被引导去下载一个并不存在的产物。本测试是两者之间唯一的自动防线
package cmd

import (
	"testing"

	"github.com/jy-eggroll/eggokit/release"
)

// TestReleaseManifestMatchesSupportedPlatforms 校验 release.json 与 supportedPlatforms 完全一致
//
// 清单路径用 ../release.json：go test 的工作目录是包目录（cmd/），而清单在仓库根
func TestReleaseManifestMatchesSupportedPlatforms(t *testing.T) {
	m, err := release.Load("../release.json")
	if err != nil {
		t.Fatalf("加载 release.json 失败: %v", err)
	}

	// 把 Go 侧的平台表摊平成 "os/arch" 集合，便于与清单做双向比对
	goSide := make(map[string]bool)
	for goos, arches := range supportedPlatforms {
		for arch := range arches {
			goSide[goos+"/"+arch] = true
		}
	}

	manifestSide := make(map[string]bool)
	for _, p := range m.Platforms {
		manifestSide[p.OS+"/"+p.Arch] = true
	}

	// 双向差集：清单多一个或少一个平台都必须失败，任何单侧改动都会在这里被拦下
	for key := range manifestSide {
		if !goSide[key] {
			t.Errorf("release.json 声明了平台 %s，但 supportedPlatforms 未收录", key)
		}
	}
	for key := range goSide {
		if !manifestSide[key] {
			t.Errorf("supportedPlatforms 收录了平台 %s，但 release.json 未声明", key)
		}
	}

	// 资产名一致：升级器按 ggtAssetName 拼出前缀去 Release 里找产物，构建按 AssetName 命名产物，
	// 两者一旦不一致，升级就会“找不到任何资产”（windows 的 .exe 后缀属于命名的一部分）
	for _, p := range m.Platforms {
		name, ok := ggtAssetName(p.OS, p.Arch)
		if !ok {
			// 平台集合本身已在上面报过，这里不重复报错
			continue
		}
		if want := m.AssetName(p); name != want {
			t.Errorf("平台 %s 的资产名不一致：升级器 %q，构建 %q", p.String(), name, want)
		}
	}
}
