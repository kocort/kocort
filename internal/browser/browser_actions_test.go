package browser

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBrowserAllActions 是一个综合集成测试，逐一测试浏览器工具的所有操作指令。
// 使用可见（非 headless）模式运行，以便观察每个指令的操作结果。
// 测试使用内嵌的 HTML 页面，包含各种交互元素，形成测试闭环。
//
// 运行方式:
//
//	RUN_BROWSER_INTEGRATION=1 go test -v -run TestBrowserAllActions -timeout 300s ./internal/browser/
func TestBrowserAllActions(t *testing.T) {
	if os.Getenv("RUN_BROWSER_INTEGRATION") == "" {
		t.Skip("跳过集成测试：设置 RUN_BROWSER_INTEGRATION=1 以启用")
	}

	// ---------------------------------------------------------------
	// 1. 启动本地 HTTP 服务器，托管测试页面
	// ---------------------------------------------------------------
	htmlPath := filepath.Join("testdata", "test_page.html")
	if _, err := os.Stat(htmlPath); err != nil {
		t.Fatalf("找不到测试页面 %s: %v", htmlPath, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, htmlPath)
	})
	// 提供一个小文件用于下载测试
	mux.HandleFunc("/download.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", "attachment; filename=download.txt")
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "hello from download")
	})

	srv := &http.Server{Addr: "127.0.0.1:0", Handler: mux}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("无法监听端口: %v", err)
	}
	serverURL := fmt.Sprintf("http://%s", ln.Addr().String())
	t.Logf("测试服务器启动: %s", serverURL)
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	// ---------------------------------------------------------------
	// 2. 创建 Manager（可见模式）
	// ---------------------------------------------------------------
	tmpDir := t.TempDir()
	headless := false
	mgr := NewManager(Options{
		Headless:         &headless,
		UseSystemBrowser: true,
		AutoInstall:      true,
		ArtifactDir:      filepath.Join(tmpDir, "artifacts"),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	sessionKey := "all-actions-test"
	baseReq := Request{SessionKey: sessionKey}

	// ---------------------------------------------------------------
	// 3. Install — 安装 Playwright 驱动
	// ---------------------------------------------------------------
	t.Run("install", func(t *testing.T) {
		res, err := mgr.Install(ctx, baseReq)
		if err != nil {
			t.Fatalf("Install 失败: %v", err)
		}
		assertOK(t, res, "install")
		t.Logf("Install: %v", res)
	})

	// ---------------------------------------------------------------
	// 4. Status — 查看当前状态（浏览器尚未启动）
	// ---------------------------------------------------------------
	t.Run("status_before_start", func(t *testing.T) {
		res, err := mgr.Status(ctx, baseReq)
		if err != nil {
			t.Fatalf("Status 失败: %v", err)
		}
		t.Logf("Status (启动前): %v", res)
	})

	// ---------------------------------------------------------------
	// 5. Start — 启动浏览器
	// ---------------------------------------------------------------
	t.Run("start", func(t *testing.T) {
		res, err := mgr.Start(ctx, baseReq)
		if err != nil {
			t.Fatalf("Start 失败: %v", err)
		}
		assertOK(t, res, "start")
		t.Logf("Start: %v", res)
	})

	// ---------------------------------------------------------------
	// 6. Status — 浏览器已启动后的状态
	// ---------------------------------------------------------------
	t.Run("status_after_start", func(t *testing.T) {
		res, err := mgr.Status(ctx, baseReq)
		if err != nil {
			t.Fatalf("Status 失败: %v", err)
		}
		assertOK(t, res, "status")
		t.Logf("Status (启动后): %v", res)
	})

	// ---------------------------------------------------------------
	// 7. Profiles — 查看浏览器 profile 列表
	// ---------------------------------------------------------------
	t.Run("profiles", func(t *testing.T) {
		res, err := mgr.Profiles(ctx, baseReq)
		if err != nil {
			t.Fatalf("Profiles 失败: %v", err)
		}
		assertOK(t, res, "profiles")
		t.Logf("Profiles: %v", res)
	})

	// ---------------------------------------------------------------
	// 8. Open — 打开测试页面
	// ---------------------------------------------------------------
	var targetID string
	t.Run("open", func(t *testing.T) {
		res, err := mgr.Open(ctx, OpenRequest{
			Request: baseReq,
			URL:     serverURL,
		})
		if err != nil {
			t.Fatalf("Open 失败: %v", err)
		}
		assertOK(t, res, "open")
		targetID, _ = res["targetId"].(string)
		t.Logf("Open: targetId=%s", targetID)
		time.Sleep(1 * time.Second)
	})

	targetReq := func() TargetRequest {
		return TargetRequest{Request: baseReq, TargetID: targetID}
	}

	// ---------------------------------------------------------------
	// 9. Tabs — 获取标签页列表
	// ---------------------------------------------------------------
	t.Run("tabs", func(t *testing.T) {
		res, err := mgr.Tabs(ctx, baseReq)
		if err != nil {
			t.Fatalf("Tabs 失败: %v", err)
		}
		assertOK(t, res, "tabs")
		t.Logf("Tabs: %v", res)
	})

	// ---------------------------------------------------------------
	// 10. Navigate — 导航到测试页面（确保在正确页面）
	// ---------------------------------------------------------------
	t.Run("navigate", func(t *testing.T) {
		res, err := mgr.Navigate(ctx, NavigateRequest{
			TargetRequest: targetReq(),
			URL:           serverURL,
		})
		if err != nil {
			t.Fatalf("Navigate 失败: %v", err)
		}
		assertOK(t, res, "navigate")
		t.Logf("Navigate: %v", res)
		time.Sleep(1 * time.Second)
	})

	// ---------------------------------------------------------------
	// 11. Snapshot — 获取页面 accessibility 快照
	// ---------------------------------------------------------------
	t.Run("snapshot", func(t *testing.T) {
		res, err := mgr.Snapshot(ctx, SnapshotRequest{
			TargetRequest: targetReq(),
		})
		if err != nil {
			t.Fatalf("Snapshot 失败: %v", err)
		}
		assertOK(t, res, "snapshot")
		t.Logf("Snapshot: ok=%v, keys=%v", res["ok"], mapKeys(res))
	})

	// ---------------------------------------------------------------
	// 12. Snapshot（带选项）
	// ---------------------------------------------------------------
	t.Run("snapshot_with_options", func(t *testing.T) {
		res, err := mgr.Snapshot(ctx, SnapshotRequest{
			TargetRequest: targetReq(),
			Interactive:   true,
			Compact:       true,
			MaxChars:      5000,
		})
		if err != nil {
			t.Fatalf("Snapshot (options) 失败: %v", err)
		}
		assertOK(t, res, "snapshot_with_options")
		t.Logf("Snapshot (options): ok=%v", res["ok"])
	})

	// ---------------------------------------------------------------
	// 13. Screenshot — 截图
	// ---------------------------------------------------------------
	t.Run("screenshot", func(t *testing.T) {
		res, err := mgr.Screenshot(ctx, ScreenshotRequest{
			TargetRequest: targetReq(),
			Type:          "png",
		})
		if err != nil {
			t.Fatalf("Screenshot 失败: %v", err)
		}
		assertOK(t, res, "screenshot")
		path, _ := res["path"].(string)
		if path == "" {
			t.Error("Screenshot 未返回 path")
		} else {
			info, err := os.Stat(path)
			if err != nil || info.Size() == 0 {
				t.Errorf("截图文件无效: %s, err=%v", path, err)
			}
			t.Logf("Screenshot: %s (%d bytes)", path, info.Size())
		}
	})

	// ---------------------------------------------------------------
	// 14. Screenshot（全页面）
	// ---------------------------------------------------------------
	t.Run("screenshot_fullpage", func(t *testing.T) {
		res, err := mgr.Screenshot(ctx, ScreenshotRequest{
			TargetRequest: targetReq(),
			Type:          "png",
			FullPage:      true,
		})
		if err != nil {
			t.Fatalf("Screenshot (fullPage) 失败: %v", err)
		}
		assertOK(t, res, "screenshot_fullpage")
		t.Logf("Screenshot fullPage: path=%v", res["path"])
	})

	// ---------------------------------------------------------------
	// 15. Act: click — 点击按钮
	// ---------------------------------------------------------------
	t.Run("act_click", func(t *testing.T) {
		res, err := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "click",
			Selector:      "#click-btn",
		})
		if err != nil {
			t.Fatalf("Act click 失败: %v", err)
		}
		assertOK(t, res, "act_click")
		t.Logf("Act click: %v", res)
		time.Sleep(500 * time.Millisecond)

		// 验证点击计数器变化
		evalRes, _ := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "evaluate",
			Fn:            "document.getElementById('click-counter').textContent",
		})
		result, _ := evalRes["result"].(string)
		if !strings.Contains(result, "1") {
			t.Errorf("点击计数器未更新，期望包含 '1'，实际: %q", result)
		}
		t.Logf("点击计数器: %s", result)
	})

	// ---------------------------------------------------------------
	// 16. Act: dblclick — 双击按钮
	// ---------------------------------------------------------------
	t.Run("act_dblclick", func(t *testing.T) {
		res, err := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "click",
			Selector:      "#dblclick-btn",
			DoubleClick:   true,
		})
		if err != nil {
			t.Fatalf("Act dblclick 失败: %v", err)
		}
		assertOK(t, res, "act_dblclick")
		time.Sleep(500 * time.Millisecond)

		evalRes, _ := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "evaluate",
			Fn:            "document.getElementById('dblclick-counter').textContent",
		})
		result, _ := evalRes["result"].(string)
		if !strings.Contains(result, "1") {
			t.Errorf("双击计数器未更新，期望包含 '1'，实际: %q", result)
		}
		t.Logf("双击计数器: %s", result)
	})

	// ---------------------------------------------------------------
	// 17. Act: type（慢速输入）
	// ---------------------------------------------------------------
	t.Run("act_type", func(t *testing.T) {
		// 先清空输入框
		mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "click",
			Selector:      "#text-input",
		})
		res, err := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "type",
			Selector:      "#text-input",
			Text:          "Hello Browser!",
			Slowly:        true,
		})
		if err != nil {
			t.Fatalf("Act type 失败: %v", err)
		}
		assertOK(t, res, "act_type")
		time.Sleep(500 * time.Millisecond)

		evalRes, _ := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "evaluate",
			Fn:            "document.getElementById('text-input').value",
		})
		result, _ := evalRes["result"].(string)
		if !strings.Contains(result, "Hello Browser!") {
			t.Errorf("输入框内容不符，期望包含 'Hello Browser!'，实际: %q", result)
		}
		t.Logf("输入框内容: %s", result)
	})

	// ---------------------------------------------------------------
	// 18. Act: fill — 填充输入框
	// ---------------------------------------------------------------
	t.Run("act_fill", func(t *testing.T) {
		res, err := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "fill",
			Selector:      "#fill-input",
			Text:          "Filled Content",
		})
		if err != nil {
			t.Fatalf("Act fill 失败: %v", err)
		}
		assertOK(t, res, "act_fill")
		time.Sleep(500 * time.Millisecond)

		evalRes, _ := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "evaluate",
			Fn:            "document.getElementById('fill-input').value",
		})
		result, _ := evalRes["result"].(string)
		if result != "Filled Content" {
			t.Errorf("填充内容不符，期望 'Filled Content'，实际: %q", result)
		}
		t.Logf("填充内容: %s", result)
	})

	// ---------------------------------------------------------------
	// 18b. Act: input (fill 别名) — 模型常用 kind=input
	// ---------------------------------------------------------------
	t.Run("act_input_alias", func(t *testing.T) {
		res, err := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "input",
			Selector:      "#fill-input",
			Text:          "Input Alias Content",
		})
		if err != nil {
			t.Fatalf("Act input 失败: %v", err)
		}
		assertOK(t, res, "act_input_alias")
		time.Sleep(500 * time.Millisecond)

		evalRes, _ := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "evaluate",
			Fn:            "document.getElementById('fill-input').value",
		})
		result, _ := evalRes["result"].(string)
		if result != "Input Alias Content" {
			t.Errorf("input 别名内容不符，期望 'Input Alias Content'，实际: %q", result)
		}
		t.Logf("input 别名内容: %s", result)
	})

	// ---------------------------------------------------------------
	// 19. Act: press — 按键操作
	// ---------------------------------------------------------------
	t.Run("act_press", func(t *testing.T) {
		// 先聚焦到 key-input
		mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "click",
			Selector:      "#key-input",
		})
		res, err := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "press",
			Selector:      "#key-input",
			Key:           "Enter",
		})
		if err != nil {
			t.Fatalf("Act press 失败: %v", err)
		}
		assertOK(t, res, "act_press")
		time.Sleep(500 * time.Millisecond)

		evalRes, _ := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "evaluate",
			Fn:            "document.getElementById('key-output').textContent",
		})
		result, _ := evalRes["result"].(string)
		t.Logf("按键输出: %s", result)
		if !strings.Contains(result, "Enter") {
			t.Errorf("按键未记录，期望包含 'Enter'，实际: %q", result)
		}
	})

	// ---------------------------------------------------------------
	// 20. Act: hover — 悬停操作
	// ---------------------------------------------------------------
	t.Run("act_hover", func(t *testing.T) {
		res, err := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "hover",
			Selector:      "#hover-zone",
		})
		if err != nil {
			t.Fatalf("Act hover 失败: %v", err)
		}
		assertOK(t, res, "act_hover")
		time.Sleep(500 * time.Millisecond)

		evalRes, _ := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "evaluate",
			Fn:            "document.getElementById('hover-zone').textContent",
		})
		result, _ := evalRes["result"].(string)
		if !strings.Contains(result, "Hovered") {
			t.Errorf("悬停效果未生效，期望包含 'Hovered'，实际: %q", result)
		}
		t.Logf("悬停文本: %s", result)
	})

	// ---------------------------------------------------------------
	// 21. Act: select — 选择下拉菜单
	// ---------------------------------------------------------------
	t.Run("act_select", func(t *testing.T) {
		res, err := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "select",
			Selector:      "#fruit-select",
			Values:        []string{"banana"},
		})
		if err != nil {
			t.Fatalf("Act select 失败: %v", err)
		}
		assertOK(t, res, "act_select")
		time.Sleep(500 * time.Millisecond)

		evalRes, _ := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "evaluate",
			Fn:            "document.getElementById('fruit-select').value",
		})
		result, _ := evalRes["result"].(string)
		if result != "banana" {
			t.Errorf("下拉选择不符，期望 'banana'，实际: %q", result)
		}
		t.Logf("下拉选择: %s", result)
	})

	// ---------------------------------------------------------------
	// 22. Act: evaluate — 执行 JavaScript
	// ---------------------------------------------------------------
	t.Run("act_evaluate", func(t *testing.T) {
		res, err := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "evaluate",
			Fn:            "document.title",
		})
		if err != nil {
			t.Fatalf("Act evaluate 失败: %v", err)
		}
		assertOK(t, res, "act_evaluate")
		result, _ := res["result"].(string)
		if result != "Browser Tool Test Page" {
			t.Errorf("evaluate 结果不符，期望 'Browser Tool Test Page'，实际: %q", result)
		}
		t.Logf("Evaluate result: %v", result)
	})

	// ---------------------------------------------------------------
	// 23. Act: evaluate — 修改 DOM 元素
	// ---------------------------------------------------------------
	t.Run("act_evaluate_modify_dom", func(t *testing.T) {
		res, err := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "evaluate",
			Fn:            `document.getElementById('eval-target').textContent = 'Modified by evaluate'; 'done'`,
		})
		if err != nil {
			t.Fatalf("Act evaluate DOM 修改失败: %v", err)
		}
		assertOK(t, res, "act_evaluate_modify_dom")
		time.Sleep(500 * time.Millisecond)

		verifyRes, _ := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "evaluate",
			Fn:            "document.getElementById('eval-target').textContent",
		})
		result, _ := verifyRes["result"].(string)
		if result != "Modified by evaluate" {
			t.Errorf("DOM 未被修改，实际: %q", result)
		}
		t.Logf("DOM 修改结果: %s", result)
	})

	// ---------------------------------------------------------------
	// 24. Act: resize — 调整视口大小
	// ---------------------------------------------------------------
	t.Run("act_resize", func(t *testing.T) {
		res, err := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "resize",
			Width:         1024,
			Height:        768,
		})
		if err != nil {
			t.Fatalf("Act resize 失败: %v", err)
		}
		assertOK(t, res, "act_resize")
		time.Sleep(500 * time.Millisecond)

		// 验证视口大小
		evalRes, _ := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "evaluate",
			Fn:            `JSON.stringify({w: window.innerWidth, h: window.innerHeight})`,
		})
		t.Logf("Resize: viewport=%v", evalRes["result"])
	})

	// ---------------------------------------------------------------
	// 25. Act: wait (timeMs) — 等待指定时间
	// ---------------------------------------------------------------
	t.Run("act_wait_time", func(t *testing.T) {
		start := time.Now()
		res, err := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "wait",
			TimeMs:        500,
		})
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("Act wait (timeMs) 失败: %v", err)
		}
		assertOK(t, res, "act_wait_time")
		if elapsed < 400*time.Millisecond {
			t.Errorf("等待时间过短: %v", elapsed)
		}
		t.Logf("Wait timeMs: elapsed=%v", elapsed)
	})

	// ---------------------------------------------------------------
	// 26. Act: wait (selector) — 等待元素出现
	// ---------------------------------------------------------------
	t.Run("act_wait_selector", func(t *testing.T) {
		// 先点击按钮触发延时显示
		mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "click",
			Selector:      "#show-delayed-btn",
		})
		res, err := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "wait",
			Selector:      "#delayed-element",
			TimeoutMs:     5000,
		})
		if err != nil {
			t.Fatalf("Act wait (selector) 失败: %v", err)
		}
		assertOK(t, res, "act_wait_selector")
		t.Logf("Wait selector: 元素已出现")
	})

	// ---------------------------------------------------------------
	// 27. Act: wait (textGone) — 等待文本消失
	// ---------------------------------------------------------------
	t.Run("act_wait_textGone", func(t *testing.T) {
		// 先点击按钮触发文本消失
		mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "click",
			Selector:      "#hide-text-btn",
		})
		res, err := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "wait",
			TextGone:      "This text will disappear",
			TimeoutMs:     5000,
		})
		if err != nil {
			t.Fatalf("Act wait (textGone) 失败: %v", err)
		}
		assertOK(t, res, "act_wait_textGone")
		t.Logf("Wait textGone: 文本已消失")
	})

	// ---------------------------------------------------------------
	// 28. Act: wait (loadState) — 等待页面加载状态
	// ---------------------------------------------------------------
	t.Run("act_wait_loadState", func(t *testing.T) {
		res, err := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "wait",
			LoadState:     "domcontentloaded",
			TimeoutMs:     5000,
		})
		if err != nil {
			t.Fatalf("Act wait (loadState) 失败: %v", err)
		}
		assertOK(t, res, "act_wait_loadState")
		t.Logf("Wait loadState: domcontentloaded")
	})

	// ---------------------------------------------------------------
	// 29. Console — 触发 console.log 并获取
	// ---------------------------------------------------------------
	t.Run("console", func(t *testing.T) {
		// 先点击按钮触发 console 输出
		mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "click",
			Selector:      "#console-log-btn",
		})
		time.Sleep(500 * time.Millisecond)

		res, err := mgr.Console(ctx, ConsoleRequest{
			TargetRequest: targetReq(),
			Limit:         10,
		})
		if err != nil {
			t.Fatalf("Console 失败: %v", err)
		}
		assertOK(t, res, "console")
		t.Logf("Console: %v", mapKeys(res))
	})

	// ---------------------------------------------------------------
	// 30. Console（带 level 过滤）
	// ---------------------------------------------------------------
	t.Run("console_warning", func(t *testing.T) {
		res, err := mgr.Console(ctx, ConsoleRequest{
			TargetRequest: targetReq(),
			Level:         "warning",
			Limit:         10,
		})
		if err != nil {
			t.Fatalf("Console (warning) 失败: %v", err)
		}
		assertOK(t, res, "console_warning")
		t.Logf("Console (warning filter): %v", mapKeys(res))
	})

	// ---------------------------------------------------------------
	// 31. Errors — 触发 JS 错误并获取
	// ---------------------------------------------------------------
	t.Run("errors", func(t *testing.T) {
		// 触发页面 JS 错误
		mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "click",
			Selector:      "#error-btn",
		})
		time.Sleep(500 * time.Millisecond)

		res, err := mgr.Errors(ctx, DebugRequest{
			TargetRequest: targetReq(),
			Limit:         10,
		})
		if err != nil {
			t.Fatalf("Errors 失败: %v", err)
		}
		assertOK(t, res, "errors")
		t.Logf("Errors: %v", res)
	})

	// ---------------------------------------------------------------
	// 32. Requests — 获取网络请求记录
	// ---------------------------------------------------------------
	t.Run("requests", func(t *testing.T) {
		res, err := mgr.Requests(ctx, RequestsRequest{
			DebugRequest: DebugRequest{
				TargetRequest: targetReq(),
				Limit:         20,
			},
		})
		if err != nil {
			t.Fatalf("Requests 失败: %v", err)
		}
		assertOK(t, res, "requests")
		t.Logf("Requests: keys=%v", mapKeys(res))
	})

	// ---------------------------------------------------------------
	// 33. Requests（带 filter）
	// ---------------------------------------------------------------
	t.Run("requests_filter", func(t *testing.T) {
		res, err := mgr.Requests(ctx, RequestsRequest{
			DebugRequest: DebugRequest{
				TargetRequest: targetReq(),
				Limit:         10,
			},
			Filter: serverURL,
		})
		if err != nil {
			t.Fatalf("Requests (filter) 失败: %v", err)
		}
		assertOK(t, res, "requests_filter")
		t.Logf("Requests (filter): keys=%v", mapKeys(res))
	})

	// ---------------------------------------------------------------
	// 34. Dialog — 预配置对话框处理（accept）
	// ---------------------------------------------------------------
	t.Run("dialog_accept", func(t *testing.T) {
		res, err := mgr.Dialog(ctx, DialogRequest{
			TargetRequest: targetReq(),
			Accept:        true,
		})
		if err != nil {
			t.Fatalf("Dialog 失败: %v", err)
		}
		assertOK(t, res, "dialog_accept")
		t.Logf("Dialog armed (accept=true)")

		// 触发 alert
		mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "click",
			Selector:      "#alert-btn",
		})
		time.Sleep(1 * time.Second)
		t.Logf("Alert 已被自动接受")
	})

	// ---------------------------------------------------------------
	// 35. Dialog — confirm 对话框
	// ---------------------------------------------------------------
	t.Run("dialog_confirm", func(t *testing.T) {
		res, err := mgr.Dialog(ctx, DialogRequest{
			TargetRequest: targetReq(),
			Accept:        true,
		})
		if err != nil {
			t.Fatalf("Dialog 失败: %v", err)
		}
		assertOK(t, res, "dialog_confirm")

		mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "click",
			Selector:      "#confirm-btn",
		})
		time.Sleep(1 * time.Second)

		evalRes, _ := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "evaluate",
			Fn:            "document.getElementById('dialog-result').textContent",
		})
		result, _ := evalRes["result"].(string)
		t.Logf("Confirm 结果: %s", result)
		if !strings.Contains(result, "true") {
			t.Errorf("Confirm 未被接受，实际: %q", result)
		}
	})

	// ---------------------------------------------------------------
	// 36. Dialog — prompt 对话框（带文本）
	// ---------------------------------------------------------------
	t.Run("dialog_prompt", func(t *testing.T) {
		res, err := mgr.Dialog(ctx, DialogRequest{
			TargetRequest: targetReq(),
			Accept:        true,
			PromptText:    "Hello from test",
		})
		if err != nil {
			t.Fatalf("Dialog 失败: %v", err)
		}
		assertOK(t, res, "dialog_prompt")

		mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "click",
			Selector:      "#prompt-btn",
		})
		time.Sleep(1 * time.Second)

		evalRes, _ := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "evaluate",
			Fn:            "document.getElementById('dialog-result').textContent",
		})
		result, _ := evalRes["result"].(string)
		t.Logf("Prompt 结果: %s", result)
		if !strings.Contains(result, "Hello from test") {
			t.Errorf("Prompt 文本不符，实际: %q", result)
		}
	})

	// ---------------------------------------------------------------
	// 37. Upload — 文件上传（通过 selector）
	// ---------------------------------------------------------------
	t.Run("upload", func(t *testing.T) {
		// 创建一个临时文件用于上传测试
		uploadFile := filepath.Join(tmpDir, "test_upload.txt")
		if err := os.WriteFile(uploadFile, []byte("upload test content"), 0o644); err != nil {
			t.Fatalf("创建上传文件失败: %v", err)
		}

		res, err := mgr.Upload(ctx, UploadRequest{
			TargetRequest: targetReq(),
			Selector:      "#file-upload",
			Paths:         []string{uploadFile},
		})
		if err != nil {
			t.Fatalf("Upload 失败: %v", err)
		}
		assertOK(t, res, "upload")
		time.Sleep(500 * time.Millisecond)

		evalRes, _ := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "evaluate",
			Fn:            "document.getElementById('upload-result').textContent",
		})
		result, _ := evalRes["result"].(string)
		t.Logf("Upload 结果: %s", result)
		if !strings.Contains(result, "test_upload.txt") {
			t.Errorf("上传文件名未显示，实际: %q", result)
		}
	})

	// ---------------------------------------------------------------
	// 38. Act: click (checkbox) — 点击复选框
	// ---------------------------------------------------------------
	t.Run("act_click_checkbox", func(t *testing.T) {
		res, err := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "click",
			Selector:      "#agree-checkbox",
		})
		if err != nil {
			t.Fatalf("Act click checkbox 失败: %v", err)
		}
		assertOK(t, res, "act_click_checkbox")
		time.Sleep(300 * time.Millisecond)

		evalRes, _ := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "evaluate",
			Fn:            "document.getElementById('agree-checkbox').checked",
		})
		t.Logf("Checkbox checked: %v", evalRes["result"])
	})

	// ---------------------------------------------------------------
	// 39. Act: click (radio) — 点击单选按钮
	// ---------------------------------------------------------------
	t.Run("act_click_radio", func(t *testing.T) {
		res, err := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "click",
			Selector:      "#radio-blue",
		})
		if err != nil {
			t.Fatalf("Act click radio 失败: %v", err)
		}
		assertOK(t, res, "act_click_radio")
		time.Sleep(300 * time.Millisecond)

		evalRes, _ := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "evaluate",
			Fn:            "document.getElementById('radio-blue').checked",
		})
		t.Logf("Radio blue checked: %v", evalRes["result"])
	})

	// ---------------------------------------------------------------
	// 40. Trace — 开启和停止 trace
	// ---------------------------------------------------------------
	t.Run("trace", func(t *testing.T) {
		startRes, err := mgr.TraceStart(ctx, TraceStartRequest{
			TargetRequest: targetReq(),
			Screenshots:   true,
			Snapshots:     true,
		})
		if err != nil {
			t.Fatalf("TraceStart 失败: %v", err)
		}
		assertOK(t, startRes, "trace_start")
		t.Logf("Trace 已开始")

		// 做一些操作让 trace 记录
		mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "click",
			Selector:      "#click-btn",
		})
		time.Sleep(500 * time.Millisecond)

		stopRes, err := mgr.TraceStop(ctx, TraceStopRequest{
			TargetRequest: targetReq(),
		})
		if err != nil {
			t.Fatalf("TraceStop 失败: %v", err)
		}
		assertOK(t, stopRes, "trace_stop")
		tracePath, _ := stopRes["path"].(string)
		t.Logf("Trace 已停止, path=%s", tracePath)
		if tracePath != "" {
			if info, err := os.Stat(tracePath); err != nil || info.Size() == 0 {
				t.Errorf("Trace 文件无效: %s", tracePath)
			}
		}
	})

	// ---------------------------------------------------------------
	// 41. Focus — 聚焦到当前标签页
	// ---------------------------------------------------------------
	t.Run("focus", func(t *testing.T) {
		res, err := mgr.Focus(ctx, targetReq())
		if err != nil {
			t.Fatalf("Focus 失败: %v", err)
		}
		assertOK(t, res, "focus")
		t.Logf("Focus: %v", res)
	})

	// ---------------------------------------------------------------
	// 42. Open 第二个标签页，然后 Close 它
	// ---------------------------------------------------------------
	t.Run("open_and_close_tab", func(t *testing.T) {
		openRes, err := mgr.Open(ctx, OpenRequest{
			Request: baseReq,
			URL:     serverURL + "/",
		})
		if err != nil {
			t.Fatalf("Open second tab 失败: %v", err)
		}
		assertOK(t, openRes, "open_second_tab")
		secondID, _ := openRes["targetId"].(string)
		t.Logf("打开第二个标签页: targetId=%s", secondID)
		time.Sleep(500 * time.Millisecond)

		// 查看 tabs 数量
		tabsRes, _ := mgr.Tabs(ctx, baseReq)
		t.Logf("标签页数量: %v", tabsRes["tabCount"])

		// 关闭第二个标签页
		closeRes, err := mgr.Close(ctx, TargetRequest{
			Request:  baseReq,
			TargetID: secondID,
		})
		if err != nil {
			t.Fatalf("Close second tab 失败: %v", err)
		}
		assertOK(t, closeRes, "close_second_tab")
		t.Logf("第二个标签页已关闭")
		time.Sleep(500 * time.Millisecond)
	})

	// ---------------------------------------------------------------
	// 43. Errors (clear) — 获取并清除错误
	// ---------------------------------------------------------------
	t.Run("errors_clear", func(t *testing.T) {
		res, err := mgr.Errors(ctx, DebugRequest{
			TargetRequest: targetReq(),
			Clear:         true,
			Limit:         50,
		})
		if err != nil {
			t.Fatalf("Errors (clear) 失败: %v", err)
		}
		assertOK(t, res, "errors_clear")
		t.Logf("Errors (clear): %v", mapKeys(res))

		// 再获取一次，应该为空
		res2, _ := mgr.Errors(ctx, DebugRequest{
			TargetRequest: targetReq(),
			Limit:         50,
		})
		errors, _ := res2["errors"].([]map[string]any)
		t.Logf("Errors 清除后数量: %d", len(errors))
	})

	// ---------------------------------------------------------------
	// 44. Requests (clear) — 获取并清除请求记录
	// ---------------------------------------------------------------
	t.Run("requests_clear", func(t *testing.T) {
		res, err := mgr.Requests(ctx, RequestsRequest{
			DebugRequest: DebugRequest{
				TargetRequest: targetReq(),
				Clear:         true,
				Limit:         50,
			},
		})
		if err != nil {
			t.Fatalf("Requests (clear) 失败: %v", err)
		}
		assertOK(t, res, "requests_clear")
		t.Logf("Requests (clear): %v", mapKeys(res))
	})

	// ---------------------------------------------------------------
	// 45. Act: wait (url) — 导航后等待 URL 包含特定内容
	// ---------------------------------------------------------------
	t.Run("act_wait_url", func(t *testing.T) {
		// 当前已在测试页面，直接验证 URL
		res, err := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "wait",
			URL:           "127.0.0.1",
			TimeoutMs:     3000,
		})
		if err != nil {
			t.Fatalf("Act wait (url) 失败: %v", err)
		}
		assertOK(t, res, "act_wait_url")
		t.Logf("Wait URL: 已匹配")
	})

	// ---------------------------------------------------------------
	// 46. Snapshot（labels 模式）
	// ---------------------------------------------------------------
	t.Run("snapshot_labels", func(t *testing.T) {
		res, err := mgr.Snapshot(ctx, SnapshotRequest{
			TargetRequest: targetReq(),
			Labels:        true,
		})
		if err != nil {
			t.Fatalf("Snapshot (labels) 失败: %v", err)
		}
		assertOK(t, res, "snapshot_labels")
		t.Logf("Snapshot (labels): labels=%v, imagePath=%v", res["labels"], res["imagePath"])
	})

	// ---------------------------------------------------------------
	// 47. Screenshot (jpeg) — JPEG 格式截图
	// ---------------------------------------------------------------
	t.Run("screenshot_jpeg", func(t *testing.T) {
		res, err := mgr.Screenshot(ctx, ScreenshotRequest{
			TargetRequest: targetReq(),
			Type:          "jpeg",
		})
		if err != nil {
			t.Fatalf("Screenshot jpeg 失败: %v", err)
		}
		assertOK(t, res, "screenshot_jpeg")
		t.Logf("Screenshot jpeg: path=%v, type=%v", res["path"], res["type"])
	})

	// ---------------------------------------------------------------
	// 48. Act: fill (with submit) — 填充并提交
	// ---------------------------------------------------------------
	t.Run("act_fill_submit", func(t *testing.T) {
		res, err := mgr.Act(ctx, ActRequest{
			TargetRequest: targetReq(),
			Kind:          "fill",
			Selector:      "#fill-input",
			Text:          "Submit Test",
			Submit:        true,
		})
		if err != nil {
			t.Fatalf("Act fill+submit 失败: %v", err)
		}
		assertOK(t, res, "act_fill_submit")
		time.Sleep(500 * time.Millisecond)
		t.Logf("Fill+Submit 完成")
	})

	// ---------------------------------------------------------------
	// 49. 最终截图留念
	// ---------------------------------------------------------------
	t.Run("final_screenshot", func(t *testing.T) {
		res, err := mgr.Screenshot(ctx, ScreenshotRequest{
			TargetRequest: targetReq(),
			Type:          "png",
			FullPage:      true,
		})
		if err != nil {
			t.Fatalf("最终截图失败: %v", err)
		}
		assertOK(t, res, "final_screenshot")
		t.Logf("最终截图: %v", res["path"])
	})

	// ---------------------------------------------------------------
	// 50. Close — 关闭标签页
	// ---------------------------------------------------------------
	t.Run("close", func(t *testing.T) {
		res, err := mgr.Close(ctx, targetReq())
		if err != nil {
			t.Fatalf("Close 失败: %v", err)
		}
		assertOK(t, res, "close")
		t.Logf("标签页已关闭")
	})

	// ---------------------------------------------------------------
	// 51. Stop — 停止浏览器
	// ---------------------------------------------------------------
	t.Run("stop", func(t *testing.T) {
		res, err := mgr.Stop(ctx, baseReq)
		if err != nil {
			t.Fatalf("Stop 失败: %v", err)
		}
		assertOK(t, res, "stop")
		t.Logf("浏览器已停止")
	})
}

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

func assertOK(t *testing.T, res map[string]any, action string) {
	t.Helper()
	if res == nil {
		t.Fatalf("[%s] 返回结果为 nil", action)
	}
	ok, _ := res["ok"].(bool)
	if !ok {
		errMsg, _ := res["error"].(string)
		t.Fatalf("[%s] 操作失败: ok=false, error=%q, full=%v", action, errMsg, res)
	}
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
