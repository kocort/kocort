package browser

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSystemBrowserOpenBaidu 使用系统本地浏览器（非 headless 模式）打开 baidu.com。
// 这是一个集成测试，需要本地已安装 Chrome 或 Edge 浏览器。
// 运行方式: go test -v -run TestSystemBrowserOpenBaidu -tags=integration ./internal/browser/
//
// 默认跳过，设置环境变量 RUN_BROWSER_INTEGRATION=1 启用:
//
//	RUN_BROWSER_INTEGRATION=1 go test -v -run TestSystemBrowserOpenBaidu ./internal/browser/
func TestSystemBrowserOpenBaidu(t *testing.T) {
	if os.Getenv("RUN_BROWSER_INTEGRATION") == "" {
		t.Skip("跳过集成测试：设置 RUN_BROWSER_INTEGRATION=1 以启用")
	}

	tmpDir := t.TempDir()
	headless := false
	mgr := NewManager(Options{
		Headless:         &headless,
		UseSystemBrowser: true,
		AutoInstall:      true,
		PersistSession:   true,
		UserDataDir:      filepath.Join(tmpDir, "userdata"),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1. 安装 Playwright 驱动（如果尚未安装）
	installRes, err := mgr.Install(ctx, Request{})
	if err != nil {
		t.Fatalf("Install 失败: %v", err)
	}
	t.Logf("Install 结果: ok=%v", installRes["ok"])

	// 2. 启动浏览器
	startRes, err := mgr.Start(ctx, Request{SessionKey: "test-session"})
	if err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	if startRes["ok"] != true {
		t.Fatalf("Start 未成功: %v", startRes)
	}
	t.Logf("浏览器已启动, profile=%v", startRes["profile"])

	// 3. 打开百度
	openRes, err := mgr.Open(ctx, OpenRequest{
		Request: Request{SessionKey: "test-session"},
		URL:     "https://www.baidu.com",
	})
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	if openRes["ok"] != true {
		t.Fatalf("Open 未成功: %v", openRes)
	}
	targetID, _ := openRes["targetId"].(string)
	t.Logf("已打开 baidu.com, targetId=%s", targetID)

	// 4. 等待页面加载完毕后停留几秒，便于观察
	time.Sleep(3 * time.Second)

	// 5. 截图验证
	ssRes, err := mgr.Screenshot(ctx, ScreenshotRequest{
		TargetRequest: TargetRequest{
			Request:  Request{SessionKey: "test-session"},
			TargetID: targetID,
		},
		Type: "png",
	})
	if err != nil {
		t.Fatalf("Screenshot 失败: %v", err)
	}
	t.Logf("截图结果: ok=%v", ssRes["ok"])

	// 6. 获取页面快照（accessibility tree）
	snapRes, err := mgr.Snapshot(ctx, SnapshotRequest{
		TargetRequest: TargetRequest{
			Request:  Request{SessionKey: "test-session"},
			TargetID: targetID,
		},
	})
	if err != nil {
		t.Fatalf("Snapshot 失败: %v", err)
	}
	t.Logf("页面快照: ok=%v", snapRes["ok"])

	// 7. 获取 Tab 列表
	tabsRes, err := mgr.Tabs(ctx, Request{SessionKey: "test-session"})
	if err != nil {
		t.Fatalf("Tabs 失败: %v", err)
	}
	t.Logf("当前标签页数量: %v", tabsRes["tabCount"])

	// 8. 关闭页面
	closeRes, err := mgr.Close(ctx, TargetRequest{
		Request:  Request{SessionKey: "test-session"},
		TargetID: targetID,
	})
	if err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
	t.Logf("关闭页面: ok=%v", closeRes["ok"])

	// 9. 停止浏览器
	stopRes, err := mgr.Stop(ctx, Request{SessionKey: "test-session"})
	if err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
	if stopRes["ok"] != true {
		t.Fatalf("Stop 未成功: %v", stopRes)
	}
	t.Logf("浏览器已停止")

	// 10. 验证 persistent session: 用户数据目录应被创建
	userDataPath := filepath.Join(tmpDir, "userdata", "kocort")
	if info, err := os.Stat(userDataPath); err != nil || !info.IsDir() {
		t.Fatalf("持久化会话目录未创建: %s (err=%v)", userDataPath, err)
	}
	t.Logf("持久化会话目录已创建: %s", userDataPath)
}

// TestHeadlessOverrideViaRequest 验证通过 Request.Headless 可以覆盖 Manager 默认的 headless 模式。
func TestHeadlessOverrideViaRequest(t *testing.T) {
	if os.Getenv("RUN_BROWSER_INTEGRATION") == "" {
		t.Skip("跳过集成测试：设置 RUN_BROWSER_INTEGRATION=1 以启用")
	}

	// Manager 默认 headless=true
	mgr := NewManager(Options{
		UseSystemBrowser: true,
		AutoInstall:      true,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, _ = mgr.Install(ctx, Request{})

	// 1. 通过 Request.Headless=false 启动非 headless 浏览器
	headlessFalse := false
	startRes, err := mgr.Start(ctx, Request{
		SessionKey: "headless-test",
		Headless:   &headlessFalse,
	})
	if err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	if startRes["ok"] != true {
		t.Fatalf("Start 未成功: %v", startRes)
	}
	t.Logf("非 headless 浏览器已启动")

	// 2. 打开百度验证
	openRes, err := mgr.Open(ctx, OpenRequest{
		Request: Request{SessionKey: "headless-test"},
		URL:     "https://www.baidu.com",
	})
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	if openRes["ok"] != true {
		t.Fatalf("Open 未成功: %v", openRes)
	}
	t.Logf("已打开 baidu.com (非 headless)")

	time.Sleep(2 * time.Second)

	// 3. 停止
	stopRes, err := mgr.Stop(ctx, Request{SessionKey: "headless-test"})
	if err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
	if stopRes["ok"] != true {
		t.Fatalf("Stop 未成功: %v", stopRes)
	}
	t.Logf("浏览器已停止")
}
