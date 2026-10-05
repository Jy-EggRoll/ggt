// cmd 包的测试入口：把所有测试放进一个与使用者本机 git 配置隔开的环境里。
//
// 本包的 ui_diff_test.go 用真实 git 造仓库，而被测代码（diffText、DiffNumstat）自己也会
// exec git——两者读的都是测试进程的环境。开发者本机若设了 core.quotepath=false 这类配置，
// 测试就在一个与 CI 不同的世界里跑，曾经出现过本机全绿、CI 全红。
// 两个变量指向空文件后，使用者的 git 配置不再参与，跑的就是 git 的默认行为
package cmd

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	os.Exit(m.Run())
}
