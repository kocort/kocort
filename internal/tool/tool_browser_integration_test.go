package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	browserpkg "github.com/kocort/kocort/internal/browser"
	"github.com/kocort/kocort/internal/core"
)

// TestBrowserToolExecute 是一个综合集成测试，通过 BrowserTool.Execute()
// 传入各种 map[string]any 参数来逐一测试所有浏览器操作指令。
// 使用可见（非 headless）模式运行，以便观察每个指令的操作结果。
//
// 运行方式:
//
//	RUN_BROWSER_INTEGRATION=1 go test -v -run TestBrowserToolExecute -timeout 300s ./internal/tool/
func TestBrowserToolExecute(t *testing.T) {
	if os.Getenv("RUN_BROWSER_INTEGRATION") == "" {
		t.Skip("跳过集成测试：设置 RUN_BROWSER_INTEGRATION=1 以启用")
	}

	// ---------------------------------------------------------------
	// 1. 启动本地 HTTP 服务器，托管测试页面
	// ---------------------------------------------------------------
	htmlPath := filepath.Join("..", "browser", "testdata", "test_page.html")
	if _, err := os.Stat(htmlPath); err != nil {
		t.Fatalf("找不到测试页面 %s: %v", htmlPath, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, htmlPath)
	})
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
	// 2. 创建 Manager 和 BrowserTool
	// ---------------------------------------------------------------
	tmpDir := t.TempDir()
	headless := false
	mgr := browserpkg.NewManager(browserpkg.Options{
		Headless:         &headless,
		UseSystemBrowser: true,
		AutoInstall:      true,
		ArtifactDir:      filepath.Join(tmpDir, "artifacts"),
	})

	bt := NewBrowserTool(mgr)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// 构造最小可用的 ToolContext
	toolCtx := ToolContext{
		Run: AgentRunContext{
			Session: core.SessionResolution{
				SessionKey: "browser-tool-test",
			},
			WorkspaceDir: tmpDir,
		},
	}

	// exec 帮助函数：调用 BrowserTool.Execute 并解析 JSON 结果
	exec := func(t *testing.T, args map[string]any) map[string]any {
		t.Helper()
		result, err := bt.Execute(ctx, toolCtx, args)
		if err != nil {
			t.Fatalf("Execute 失败 (args=%v): %v", args, err)
		}
		return parseToolResult(t, result)
	}

	// execOK 帮助函数：exec + 验证 ok=true
	execOK := func(t *testing.T, label string, args map[string]any) map[string]any {
		t.Helper()
		res := exec(t, args)
		assertToolOK(t, res, label)
		return res
	}

	// ---------------------------------------------------------------
	// 3. Install — 安装 Playwright 驱动
	// ---------------------------------------------------------------
	t.Run("install", func(t *testing.T) {
		res := execOK(t, "install", map[string]any{
			"action": "install",
		})
		t.Logf("Install: %v", res)
	})

	// ---------------------------------------------------------------
	// 4. Status — 浏览器尚未启动
	// ---------------------------------------------------------------
	t.Run("status_before_start", func(t *testing.T) {
		res := exec(t, map[string]any{
			"action": "status",
		})
		t.Logf("Status (启动前): %v", res)
	})

	// ---------------------------------------------------------------
	// 5. Start — 启动浏览器（可见模式）
	// ---------------------------------------------------------------
	t.Run("start", func(t *testing.T) {
		res := execOK(t, "start", map[string]any{
			"action":   "start",
			"headless": false,
		})
		t.Logf("Start: %v", res)
	})

	// ---------------------------------------------------------------
	// 6. Status — 启动后
	// ---------------------------------------------------------------
	t.Run("status_after_start", func(t *testing.T) {
		res := execOK(t, "status", map[string]any{
			"action": "status",
		})
		t.Logf("Status (启动后): %v", res)
	})

	// ---------------------------------------------------------------
	// 7. Profiles
	// ---------------------------------------------------------------
	t.Run("profiles", func(t *testing.T) {
		res := execOK(t, "profiles", map[string]any{
			"action": "profiles",
		})
		t.Logf("Profiles: %v", res)
	})

	// ---------------------------------------------------------------
	// 8. Open — 打开测试页面
	// ---------------------------------------------------------------
	var targetID string
	t.Run("open", func(t *testing.T) {
		res := execOK(t, "open", map[string]any{
			"action": "open",
			"url":    serverURL,
		})
		targetID, _ = res["targetId"].(string)
		t.Logf("Open: targetId=%s", targetID)
		time.Sleep(1 * time.Second)
	})

	// ---------------------------------------------------------------
	// 9. Tabs
	// ---------------------------------------------------------------
	t.Run("tabs", func(t *testing.T) {
		res := execOK(t, "tabs", map[string]any{
			"action": "tabs",
		})
		t.Logf("Tabs: %v", jsonKeys(res))
	})

	// ---------------------------------------------------------------
	// 10. Navigate
	// ---------------------------------------------------------------
	t.Run("navigate", func(t *testing.T) {
		res := execOK(t, "navigate", map[string]any{
			"action":   "navigate",
			"targetId": targetID,
			"url":      serverURL,
		})
		t.Logf("Navigate: %v", res)
		time.Sleep(1 * time.Second)
	})

	// ---------------------------------------------------------------
	// 11. Snapshot
	// ---------------------------------------------------------------
	t.Run("snapshot", func(t *testing.T) {
		res := execOK(t, "snapshot", map[string]any{
			"action":   "snapshot",
			"targetId": targetID,
		})
		t.Logf("Snapshot: ok=%v, keys=%v", res["ok"], jsonKeys(res))
	})

	// ---------------------------------------------------------------
	// 12. Snapshot（带选项）
	// ---------------------------------------------------------------
	t.Run("snapshot_with_options", func(t *testing.T) {
		res := execOK(t, "snapshot_with_options", map[string]any{
			"action":      "snapshot",
			"targetId":    targetID,
			"interactive": true,
			"compact":     true,
			"maxChars":    5000,
		})
		t.Logf("Snapshot (options): ok=%v", res["ok"])
	})

	// ---------------------------------------------------------------
	// 13. Screenshot (png)
	// ---------------------------------------------------------------
	t.Run("screenshot", func(t *testing.T) {
		res := execOK(t, "screenshot", map[string]any{
			"action":   "screenshot",
			"targetId": targetID,
			"type":     "png",
		})
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
		res := execOK(t, "screenshot_fullpage", map[string]any{
			"action":   "screenshot",
			"targetId": targetID,
			"type":     "png",
			"fullPage": true,
		})
		t.Logf("Screenshot fullPage: path=%v", res["path"])
	})

	// ---------------------------------------------------------------
	// 15. Act: click — 点击按钮
	// ---------------------------------------------------------------
	t.Run("act_click", func(t *testing.T) {
		execOK(t, "act_click", map[string]any{
			"action":   "act",
			"kind":     "click",
			"selector": "#click-btn",
			"targetId": targetID,
		})
		time.Sleep(500 * time.Millisecond)

		// 验证点击计数器
		evalRes := execOK(t, "act_click_verify", map[string]any{
			"action":   "act",
			"kind":     "evaluate",
			"fn":       "document.getElementById('click-counter').textContent",
			"targetId": targetID,
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
		execOK(t, "act_dblclick", map[string]any{
			"action":      "act",
			"kind":        "click",
			"selector":    "#dblclick-btn",
			"doubleClick": true,
			"targetId":    targetID,
		})
		time.Sleep(500 * time.Millisecond)

		evalRes := execOK(t, "act_dblclick_verify", map[string]any{
			"action":   "act",
			"kind":     "evaluate",
			"fn":       "document.getElementById('dblclick-counter').textContent",
			"targetId": targetID,
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
		// 先点击聚焦
		execOK(t, "act_type_focus", map[string]any{
			"action":   "act",
			"kind":     "click",
			"selector": "#text-input",
			"targetId": targetID,
		})
		execOK(t, "act_type", map[string]any{
			"action":   "act",
			"kind":     "type",
			"selector": "#text-input",
			"text":     "Hello Browser!",
			"slowly":   true,
			"targetId": targetID,
		})
		time.Sleep(500 * time.Millisecond)

		evalRes := execOK(t, "act_type_verify", map[string]any{
			"action":   "act",
			"kind":     "evaluate",
			"fn":       "document.getElementById('text-input').value",
			"targetId": targetID,
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
		execOK(t, "act_fill", map[string]any{
			"action":   "act",
			"kind":     "fill",
			"selector": "#fill-input",
			"text":     "Filled Content",
			"targetId": targetID,
		})
		time.Sleep(500 * time.Millisecond)

		evalRes := execOK(t, "act_fill_verify", map[string]any{
			"action":   "act",
			"kind":     "evaluate",
			"fn":       "document.getElementById('fill-input').value",
			"targetId": targetID,
		})
		result, _ := evalRes["result"].(string)
		if result != "Filled Content" {
			t.Errorf("填充内容不符，期望 'Filled Content'，实际: %q", result)
		}
		t.Logf("填充内容: %s", result)
	})

	// ---------------------------------------------------------------
	// 18b. Act: input (fill 别名)
	// ---------------------------------------------------------------
	t.Run("act_input_alias", func(t *testing.T) {
		execOK(t, "act_input_alias", map[string]any{
			"action":   "act",
			"kind":     "input",
			"selector": "#fill-input",
			"text":     "Input Alias Content",
			"targetId": targetID,
		})
		time.Sleep(500 * time.Millisecond)

		evalRes := execOK(t, "act_input_verify", map[string]any{
			"action":   "act",
			"kind":     "evaluate",
			"fn":       "document.getElementById('fill-input').value",
			"targetId": targetID,
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
		execOK(t, "act_press_focus", map[string]any{
			"action":   "act",
			"kind":     "click",
			"selector": "#key-input",
			"targetId": targetID,
		})
		execOK(t, "act_press", map[string]any{
			"action":   "act",
			"kind":     "press",
			"selector": "#key-input",
			"key":      "Enter",
			"targetId": targetID,
		})
		time.Sleep(500 * time.Millisecond)

		evalRes := execOK(t, "act_press_verify", map[string]any{
			"action":   "act",
			"kind":     "evaluate",
			"fn":       "document.getElementById('key-output').textContent",
			"targetId": targetID,
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
		execOK(t, "act_hover", map[string]any{
			"action":   "act",
			"kind":     "hover",
			"selector": "#hover-zone",
			"targetId": targetID,
		})
		time.Sleep(500 * time.Millisecond)

		evalRes := execOK(t, "act_hover_verify", map[string]any{
			"action":   "act",
			"kind":     "evaluate",
			"fn":       "document.getElementById('hover-zone').textContent",
			"targetId": targetID,
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
		execOK(t, "act_select", map[string]any{
			"action":   "act",
			"kind":     "select",
			"selector": "#fruit-select",
			"values":   []string{"banana"},
			"targetId": targetID,
		})
		time.Sleep(500 * time.Millisecond)

		evalRes := execOK(t, "act_select_verify", map[string]any{
			"action":   "act",
			"kind":     "evaluate",
			"fn":       "document.getElementById('fruit-select').value",
			"targetId": targetID,
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
		res := execOK(t, "act_evaluate", map[string]any{
			"action":   "act",
			"kind":     "evaluate",
			"fn":       "document.title",
			"targetId": targetID,
		})
		result, _ := res["result"].(string)
		if result != "Browser Tool Test Page" {
			t.Errorf("evaluate 结果不符，期望 'Browser Tool Test Page'，实际: %q", result)
		}
		t.Logf("Evaluate result: %v", result)
	})

	// ---------------------------------------------------------------
	// 23. Act: evaluate — 修改 DOM
	// ---------------------------------------------------------------
	t.Run("act_evaluate_modify_dom", func(t *testing.T) {
		execOK(t, "act_evaluate_modify_dom", map[string]any{
			"action":   "act",
			"kind":     "evaluate",
			"fn":       `document.getElementById('eval-target').textContent = 'Modified by evaluate'; 'done'`,
			"targetId": targetID,
		})
		time.Sleep(500 * time.Millisecond)

		evalRes := execOK(t, "act_evaluate_dom_verify", map[string]any{
			"action":   "act",
			"kind":     "evaluate",
			"fn":       "document.getElementById('eval-target').textContent",
			"targetId": targetID,
		})
		result, _ := evalRes["result"].(string)
		if result != "Modified by evaluate" {
			t.Errorf("DOM 未被修改，实际: %q", result)
		}
		t.Logf("DOM 修改结果: %s", result)
	})

	// ---------------------------------------------------------------
	// 24. Act: resize — 调整视口大小
	// ---------------------------------------------------------------
	t.Run("act_resize", func(t *testing.T) {
		execOK(t, "act_resize", map[string]any{
			"action":   "act",
			"kind":     "resize",
			"width":    1024,
			"height":   768,
			"targetId": targetID,
		})
		time.Sleep(500 * time.Millisecond)

		evalRes := execOK(t, "act_resize_verify", map[string]any{
			"action":   "act",
			"kind":     "evaluate",
			"fn":       `JSON.stringify({w: window.innerWidth, h: window.innerHeight})`,
			"targetId": targetID,
		})
		t.Logf("Resize: viewport=%v", evalRes["result"])
	})

	// ---------------------------------------------------------------
	// 25. Act: wait (timeMs)
	// ---------------------------------------------------------------
	t.Run("act_wait_time", func(t *testing.T) {
		start := time.Now()
		execOK(t, "act_wait_time", map[string]any{
			"action":   "act",
			"kind":     "wait",
			"timeMs":   500,
			"targetId": targetID,
		})
		elapsed := time.Since(start)
		if elapsed < 400*time.Millisecond {
			t.Errorf("等待时间过短: %v", elapsed)
		}
		t.Logf("Wait timeMs: elapsed=%v", elapsed)
	})

	// ---------------------------------------------------------------
	// 26. Act: wait (selector)
	// ---------------------------------------------------------------
	t.Run("act_wait_selector", func(t *testing.T) {
		// 先点击按钮触发延时显示
		execOK(t, "act_wait_selector_trigger", map[string]any{
			"action":   "act",
			"kind":     "click",
			"selector": "#show-delayed-btn",
			"targetId": targetID,
		})
		execOK(t, "act_wait_selector", map[string]any{
			"action":    "act",
			"kind":      "wait",
			"selector":  "#delayed-element",
			"timeoutMs": 5000,
			"targetId":  targetID,
		})
		t.Logf("Wait selector: 元素已出现")
	})

	// ---------------------------------------------------------------
	// 27. Act: wait (textGone)
	// ---------------------------------------------------------------
	t.Run("act_wait_textGone", func(t *testing.T) {
		execOK(t, "act_wait_textGone_trigger", map[string]any{
			"action":   "act",
			"kind":     "click",
			"selector": "#hide-text-btn",
			"targetId": targetID,
		})
		execOK(t, "act_wait_textGone", map[string]any{
			"action":    "act",
			"kind":      "wait",
			"textGone":  "This text will disappear",
			"timeoutMs": 5000,
			"targetId":  targetID,
		})
		t.Logf("Wait textGone: 文本已消失")
	})

	// ---------------------------------------------------------------
	// 28. Act: wait (loadState)
	// ---------------------------------------------------------------
	t.Run("act_wait_loadState", func(t *testing.T) {
		execOK(t, "act_wait_loadState", map[string]any{
			"action":    "act",
			"kind":      "wait",
			"loadState": "domcontentloaded",
			"timeoutMs": 5000,
			"targetId":  targetID,
		})
		t.Logf("Wait loadState: domcontentloaded")
	})

	// ---------------------------------------------------------------
	// 29. Console
	// ---------------------------------------------------------------
	t.Run("console", func(t *testing.T) {
		execOK(t, "console_trigger", map[string]any{
			"action":   "act",
			"kind":     "click",
			"selector": "#console-log-btn",
			"targetId": targetID,
		})
		time.Sleep(500 * time.Millisecond)

		res := execOK(t, "console", map[string]any{
			"action":   "console",
			"targetId": targetID,
			"limit":    10,
		})
		t.Logf("Console: %v", jsonKeys(res))
	})

	// ---------------------------------------------------------------
	// 30. Console（带 level 过滤）
	// ---------------------------------------------------------------
	t.Run("console_warning", func(t *testing.T) {
		res := execOK(t, "console_warning", map[string]any{
			"action":   "console",
			"targetId": targetID,
			"level":    "warning",
			"limit":    10,
		})
		t.Logf("Console (warning filter): %v", jsonKeys(res))
	})

	// ---------------------------------------------------------------
	// 31. Errors
	// ---------------------------------------------------------------
	t.Run("errors", func(t *testing.T) {
		execOK(t, "errors_trigger", map[string]any{
			"action":   "act",
			"kind":     "click",
			"selector": "#error-btn",
			"targetId": targetID,
		})
		time.Sleep(500 * time.Millisecond)

		res := execOK(t, "errors", map[string]any{
			"action":   "errors",
			"targetId": targetID,
			"limit":    10,
		})
		t.Logf("Errors: %v", res)
	})

	// ---------------------------------------------------------------
	// 32. Requests
	// ---------------------------------------------------------------
	t.Run("requests", func(t *testing.T) {
		res := execOK(t, "requests", map[string]any{
			"action":   "requests",
			"targetId": targetID,
			"limit":    20,
		})
		t.Logf("Requests: keys=%v", jsonKeys(res))
	})

	// ---------------------------------------------------------------
	// 33. Requests（带 filter）
	// ---------------------------------------------------------------
	t.Run("requests_filter", func(t *testing.T) {
		res := execOK(t, "requests_filter", map[string]any{
			"action":   "requests",
			"targetId": targetID,
			"limit":    10,
			"filter":   serverURL,
		})
		t.Logf("Requests (filter): keys=%v", jsonKeys(res))
	})

	// ---------------------------------------------------------------
	// 34. Dialog — accept alert
	// ---------------------------------------------------------------
	t.Run("dialog_accept", func(t *testing.T) {
		execOK(t, "dialog_accept", map[string]any{
			"action":   "dialog",
			"targetId": targetID,
			"accept":   true,
		})
		t.Logf("Dialog armed (accept=true)")

		execOK(t, "dialog_trigger_alert", map[string]any{
			"action":   "act",
			"kind":     "click",
			"selector": "#alert-btn",
			"targetId": targetID,
		})
		time.Sleep(1 * time.Second)
		t.Logf("Alert 已被自动接受")
	})

	// ---------------------------------------------------------------
	// 35. Dialog — confirm
	// ---------------------------------------------------------------
	t.Run("dialog_confirm", func(t *testing.T) {
		execOK(t, "dialog_confirm", map[string]any{
			"action":   "dialog",
			"targetId": targetID,
			"accept":   true,
		})

		execOK(t, "dialog_trigger_confirm", map[string]any{
			"action":   "act",
			"kind":     "click",
			"selector": "#confirm-btn",
			"targetId": targetID,
		})
		time.Sleep(1 * time.Second)

		evalRes := execOK(t, "dialog_confirm_verify", map[string]any{
			"action":   "act",
			"kind":     "evaluate",
			"fn":       "document.getElementById('dialog-result').textContent",
			"targetId": targetID,
		})
		result, _ := evalRes["result"].(string)
		t.Logf("Confirm 结果: %s", result)
		if !strings.Contains(result, "true") {
			t.Errorf("Confirm 未被接受，实际: %q", result)
		}
	})

	// ---------------------------------------------------------------
	// 36. Dialog — prompt（带文本）
	// ---------------------------------------------------------------
	t.Run("dialog_prompt", func(t *testing.T) {
		execOK(t, "dialog_prompt", map[string]any{
			"action":     "dialog",
			"targetId":   targetID,
			"accept":     true,
			"promptText": "Hello from test",
		})

		execOK(t, "dialog_trigger_prompt", map[string]any{
			"action":   "act",
			"kind":     "click",
			"selector": "#prompt-btn",
			"targetId": targetID,
		})
		time.Sleep(1 * time.Second)

		evalRes := execOK(t, "dialog_prompt_verify", map[string]any{
			"action":   "act",
			"kind":     "evaluate",
			"fn":       "document.getElementById('dialog-result').textContent",
			"targetId": targetID,
		})
		result, _ := evalRes["result"].(string)
		t.Logf("Prompt 结果: %s", result)
		if !strings.Contains(result, "Hello from test") {
			t.Errorf("Prompt 文本不符，实际: %q", result)
		}
	})

	// ---------------------------------------------------------------
	// 37. Upload — 文件上传
	// ---------------------------------------------------------------
	t.Run("upload", func(t *testing.T) {
		uploadFile := filepath.Join(tmpDir, "test_upload.txt")
		if err := os.WriteFile(uploadFile, []byte("upload test content"), 0o644); err != nil {
			t.Fatalf("创建上传文件失败: %v", err)
		}

		execOK(t, "upload", map[string]any{
			"action":   "upload",
			"targetId": targetID,
			"selector": "#file-upload",
			"paths":    []string{"test_upload.txt"},
		})
		time.Sleep(500 * time.Millisecond)

		evalRes := execOK(t, "upload_verify", map[string]any{
			"action":   "act",
			"kind":     "evaluate",
			"fn":       "document.getElementById('upload-result').textContent",
			"targetId": targetID,
		})
		result, _ := evalRes["result"].(string)
		t.Logf("Upload 结果: %s", result)
		if !strings.Contains(result, "test_upload.txt") {
			t.Errorf("上传文件名未显示，实际: %q", result)
		}
	})

	// ---------------------------------------------------------------
	// 38. Act: click (checkbox)
	// ---------------------------------------------------------------
	t.Run("act_click_checkbox", func(t *testing.T) {
		execOK(t, "act_click_checkbox", map[string]any{
			"action":   "act",
			"kind":     "click",
			"selector": "#agree-checkbox",
			"targetId": targetID,
		})
		time.Sleep(300 * time.Millisecond)

		evalRes := execOK(t, "act_click_checkbox_verify", map[string]any{
			"action":   "act",
			"kind":     "evaluate",
			"fn":       "document.getElementById('agree-checkbox').checked",
			"targetId": targetID,
		})
		t.Logf("Checkbox checked: %v", evalRes["result"])
	})

	// ---------------------------------------------------------------
	// 39. Act: click (radio)
	// ---------------------------------------------------------------
	t.Run("act_click_radio", func(t *testing.T) {
		execOK(t, "act_click_radio", map[string]any{
			"action":   "act",
			"kind":     "click",
			"selector": "#radio-blue",
			"targetId": targetID,
		})
		time.Sleep(300 * time.Millisecond)

		evalRes := execOK(t, "act_click_radio_verify", map[string]any{
			"action":   "act",
			"kind":     "evaluate",
			"fn":       "document.getElementById('radio-blue').checked",
			"targetId": targetID,
		})
		t.Logf("Radio blue checked: %v", evalRes["result"])
	})

	// ---------------------------------------------------------------
	// 40. Trace — 开启和停止
	// ---------------------------------------------------------------
	t.Run("trace", func(t *testing.T) {
		execOK(t, "trace_start", map[string]any{
			"action":      "trace.start",
			"targetId":    targetID,
			"screenshots": true,
			"snapshots":   true,
		})
		t.Logf("Trace 已开始")

		// 做一些操作让 trace 记录
		execOK(t, "trace_action", map[string]any{
			"action":   "act",
			"kind":     "click",
			"selector": "#click-btn",
			"targetId": targetID,
		})
		time.Sleep(500 * time.Millisecond)

		stopRes := execOK(t, "trace_stop", map[string]any{
			"action":   "trace.stop",
			"targetId": targetID,
		})
		tracePath, _ := stopRes["path"].(string)
		t.Logf("Trace 已停止, path=%s", tracePath)
		if tracePath != "" {
			if info, err := os.Stat(tracePath); err != nil || info.Size() == 0 {
				t.Errorf("Trace 文件无效: %s", tracePath)
			}
		}
	})

	// ---------------------------------------------------------------
	// 41. Focus
	// ---------------------------------------------------------------
	t.Run("focus", func(t *testing.T) {
		res := execOK(t, "focus", map[string]any{
			"action":   "focus",
			"targetId": targetID,
		})
		t.Logf("Focus: %v", res)
	})

	// ---------------------------------------------------------------
	// 42. Open + Close 第二个标签页
	// ---------------------------------------------------------------
	t.Run("open_and_close_tab", func(t *testing.T) {
		openRes := execOK(t, "open_second_tab", map[string]any{
			"action": "open",
			"url":    serverURL + "/",
		})
		secondID, _ := openRes["targetId"].(string)
		t.Logf("打开第二个标签页: targetId=%s", secondID)
		time.Sleep(500 * time.Millisecond)

		tabsRes := execOK(t, "tabs_count", map[string]any{
			"action": "tabs",
		})
		t.Logf("标签页数量: %v", tabsRes["tabCount"])

		execOK(t, "close_second_tab", map[string]any{
			"action":   "close",
			"targetId": secondID,
		})
		t.Logf("第二个标签页已关闭")
		time.Sleep(500 * time.Millisecond)
	})

	// ---------------------------------------------------------------
	// 43. Errors (clear)
	// ---------------------------------------------------------------
	t.Run("errors_clear", func(t *testing.T) {
		execOK(t, "errors_clear", map[string]any{
			"action":   "errors",
			"targetId": targetID,
			"clear":    true,
			"limit":    50,
		})
		t.Logf("Errors cleared")

		res2 := execOK(t, "errors_after_clear", map[string]any{
			"action":   "errors",
			"targetId": targetID,
			"limit":    50,
		})
		t.Logf("Errors after clear: %v", jsonKeys(res2))
	})

	// ---------------------------------------------------------------
	// 44. Requests (clear)
	// ---------------------------------------------------------------
	t.Run("requests_clear", func(t *testing.T) {
		execOK(t, "requests_clear", map[string]any{
			"action":   "requests",
			"targetId": targetID,
			"clear":    true,
			"limit":    50,
		})
		t.Logf("Requests cleared")
	})

	// ---------------------------------------------------------------
	// 45. Act: wait (url)
	// ---------------------------------------------------------------
	t.Run("act_wait_url", func(t *testing.T) {
		execOK(t, "act_wait_url", map[string]any{
			"action":    "act",
			"kind":      "wait",
			"url":       "127.0.0.1",
			"timeoutMs": 3000,
			"targetId":  targetID,
		})
		t.Logf("Wait URL: 已匹配")
	})

	// ---------------------------------------------------------------
	// 46. Snapshot（labels 模式）
	// ---------------------------------------------------------------
	t.Run("snapshot_labels", func(t *testing.T) {
		res := execOK(t, "snapshot_labels", map[string]any{
			"action":   "snapshot",
			"targetId": targetID,
			"labels":   true,
		})
		t.Logf("Snapshot (labels): labels=%v, imagePath=%v", res["labels"], res["imagePath"])
	})

	// ---------------------------------------------------------------
	// 47. Screenshot (jpeg)
	// ---------------------------------------------------------------
	t.Run("screenshot_jpeg", func(t *testing.T) {
		res := execOK(t, "screenshot_jpeg", map[string]any{
			"action":   "screenshot",
			"targetId": targetID,
			"type":     "jpeg",
		})
		t.Logf("Screenshot jpeg: path=%v, type=%v", res["path"], res["type"])
	})

	// ---------------------------------------------------------------
	// 48. Act: fill (with submit)
	// ---------------------------------------------------------------
	t.Run("act_fill_submit", func(t *testing.T) {
		execOK(t, "act_fill_submit", map[string]any{
			"action":   "act",
			"kind":     "fill",
			"selector": "#fill-input",
			"text":     "Submit Test",
			"submit":   true,
			"targetId": targetID,
		})
		time.Sleep(500 * time.Millisecond)
		t.Logf("Fill+Submit 完成")
	})

	// ---------------------------------------------------------------
	// 49. Act: request 嵌套参数方式（模拟模型发送嵌套 request 对象）
	// ---------------------------------------------------------------
	t.Run("act_via_request_object", func(t *testing.T) {
		execOK(t, "act_via_request_object", map[string]any{
			"action":   "act",
			"targetId": targetID,
			"request": map[string]any{
				"kind":     "fill",
				"selector": "#fill-input",
				"text":     "Via Request Object",
			},
		})
		time.Sleep(500 * time.Millisecond)

		evalRes := execOK(t, "act_via_request_verify", map[string]any{
			"action":   "act",
			"kind":     "evaluate",
			"fn":       "document.getElementById('fill-input').value",
			"targetId": targetID,
		})
		result, _ := evalRes["result"].(string)
		if result != "Via Request Object" {
			t.Errorf("request 嵌套参数不符，期望 'Via Request Object'，实际: %q", result)
		}
		t.Logf("request 嵌套参数: %s", result)
	})

	// ---------------------------------------------------------------
	// 50. 最终截图留念
	// ---------------------------------------------------------------
	t.Run("final_screenshot", func(t *testing.T) {
		res := execOK(t, "final_screenshot", map[string]any{
			"action":   "screenshot",
			"targetId": targetID,
			"type":     "png",
			"fullPage": true,
		})
		t.Logf("最终截图: %v", res["path"])
	})

	// ---------------------------------------------------------------
	// 51. Close — 关闭标签页
	// ---------------------------------------------------------------
	t.Run("close", func(t *testing.T) {
		execOK(t, "close", map[string]any{
			"action":   "close",
			"targetId": targetID,
		})
		t.Logf("标签页已关闭")
	})

	// ---------------------------------------------------------------
	// 52. Stop — 停止浏览器
	// ---------------------------------------------------------------
	t.Run("stop", func(t *testing.T) {
		execOK(t, "stop", map[string]any{
			"action": "stop",
		})
		t.Logf("浏览器已停止")
	})
}

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

// parseToolResult 从 core.ToolResult 中解析 JSON map。
func parseToolResult(t *testing.T, result core.ToolResult) map[string]any {
	t.Helper()
	data := result.JSON
	if len(data) == 0 {
		data = []byte(result.Text)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("无法解析 ToolResult JSON: %v\nText: %s", err, result.Text)
	}
	return m
}

// assertToolOK 验证解析后的 JSON map 中 ok=true。
func assertToolOK(t *testing.T, res map[string]any, action string) {
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

// jsonKeys 返回 map 的所有 key。
func jsonKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
