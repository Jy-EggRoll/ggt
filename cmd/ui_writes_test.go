// ui_writes_test 盯的是“配置非法时页面上的动作一律停用”这条承诺。
//
// 看板在配置坏掉时仍会启动（好让设置面板能报告并修复问题），而它给用户的说法只有一句
// “页面上的 git 操作被拒绝”。这句承诺必须在每一个写端点上成立——漏掉一个，用户就会在点某个
// 按钮时得到与那句话相反的结局，而这正是这次要消除的不一致。
package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jy-eggroll/ggt/internal/config"
)

// TestPageWritesAreRefusedWhileConfigIsBroken 断言每一个写端点在配置非法时都以 409 拒答。
//
// 请求体刻意给空对象：闸门排在解析请求体、查找仓库之前，因此这些请求不需要合法内容；
// 反过来说，若某个端点先查仓库再查配置，这条测试会因为 404 而不是 409 失败——那正是它该
// 抓到的顺序问题
func TestPageWritesAreRefusedWhileConfigIsBroken(t *testing.T) {
	initL10n(t, "en")
	path := brokenConfig(t, `{"log_level":"trace"}`)
	cache := &uiCache{ctx: context.Background(), configPath: path}

	handlers := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"stage", cache.handleStage},
		{"unstage", cache.handleUnstage},
		{"stage-all", cache.handleStageAll},
		{"unstage-all", cache.handleUnstageAll},
		{"commit", cache.handleCommit},
		{"tag", cache.handleTag},
		{"push", cache.handlePush},
		{"pull", cache.handlePull},
		{"sync", cache.handleSync},
		{"checkout", cache.handleCheckout},
		{"fetch", cache.handleFetch},
		{"remote", cache.handleRemote},
	}
	for _, tc := range handlers {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.handler(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)))
			if rec.Code != http.StatusConflict {
				t.Fatalf("配置非法时 %s 应返回 409，实得 %d：%s", tc.name, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "config file has problems") {
				t.Errorf("%s 的拒答应说明是配置文件的问题，实得 %s", tc.name, rec.Body.String())
			}
		})
	}
}

// TestPageWritesRunWhenConfigIsValid 断言闸门不会误伤正常配置。
//
// 只挑一个不需要仓库的端点（获取全部）：仓库列表为空时它是 200 加一句“取了 0 个仓库”，
// 若闸门把合法配置也拦下，这里会变成 409。
// 快照用到的仓库列表来自全局 cfg，这里必须先用 withConfig 装一份空的：直接调处理函数时
// 它是 nil，会在采集阶段空指针崩溃，掩盖掉这条测试真正想验证的东西
func TestPageWritesRunWhenConfigIsValid(t *testing.T) {
	initL10n(t, "en")
	path := brokenConfig(t, `{}`)
	withConfig(t, &config.Config{Concurrency: config.DefaultConcurrency})
	cache := &uiCache{ctx: context.Background(), configPath: path}

	rec := httptest.NewRecorder()
	cache.handleFetch(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("配置合法时不该被闸门拦下，实得 %d：%s", rec.Code, rec.Body.String())
	}
}
