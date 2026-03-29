package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	browserpkg "github.com/kocort/kocort/internal/browser"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
)

type gatewayRuntimeStub struct {
	webRuntimeStub
	cfg          config.AppConfig
	applyCount   int
	persistCount int
	lastMain     bool
	lastModels   bool
	lastChannels bool
	lastApplied  config.AppConfig
}

func (s *gatewayRuntimeStub) GatewayConfigSnapshot() (config.AppConfig, string, error) {
	hash, err := configHash(s.cfg)
	return s.cfg, hash, err
}

func (s *gatewayRuntimeStub) GatewayApplyConfig(_ context.Context, cfg config.AppConfig) error {
	s.applyCount++
	s.lastApplied = cfg
	s.cfg = cfg
	return nil
}

func (s *gatewayRuntimeStub) GatewayPersistConfig(_ context.Context, mainChanged bool, modelsChanged bool, channelsChanged bool) error {
	s.persistCount++
	s.lastMain = mainChanged
	s.lastModels = modelsChanged
	s.lastChannels = channelsChanged
	return nil
}

func TestGatewayToolConfigGetAndPatch(t *testing.T) {
	rt := &gatewayRuntimeStub{
		cfg: config.AppConfig{
			Gateway: config.GatewayConfig{Enabled: true, Port: 18789},
		},
	}
	tool := NewGatewayTool()
	getResult, err := tool.Execute(context.Background(), ToolContext{Runtime: rt}, map[string]any{
		"action": "config.get",
	})
	if err != nil {
		t.Fatalf("config.get: %v", err)
	}
	if !strings.Contains(getResult.Text, `"action":"config.get"`) || !strings.Contains(getResult.Text, `"port":18789`) {
		t.Fatalf("unexpected config.get result: %s", getResult.Text)
	}
	patchResult, err := tool.Execute(context.Background(), ToolContext{Runtime: rt}, map[string]any{
		"action": "config.patch",
		"raw":    `{"gateway":{"port":18890}}`,
	})
	if err != nil {
		t.Fatalf("config.patch: %v", err)
	}
	if rt.applyCount != 1 || rt.persistCount != 1 {
		t.Fatalf("expected apply/persist once, got apply=%d persist=%d", rt.applyCount, rt.persistCount)
	}
	if rt.cfg.Gateway.Port != 18890 {
		t.Fatalf("expected patched port, got %+v", rt.cfg.Gateway)
	}
	if !rt.lastMain || !rt.lastModels || !rt.lastChannels {
		t.Fatalf("expected persist all sections, got main=%v models=%v channels=%v", rt.lastMain, rt.lastModels, rt.lastChannels)
	}
	if !strings.Contains(patchResult.Text, `"action":"config.patch"`) {
		t.Fatalf("unexpected config.patch result: %s", patchResult.Text)
	}
}

func TestGatewayToolRestart(t *testing.T) {
	rt := &gatewayRuntimeStub{cfg: config.AppConfig{Gateway: config.GatewayConfig{Port: 18789}}}
	tool := NewGatewayTool()
	result, err := tool.Execute(context.Background(), ToolContext{Runtime: rt}, map[string]any{
		"action": "restart",
		"note":   "reload now",
	})
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if rt.applyCount != 1 {
		t.Fatalf("expected one hot reload apply, got %d", rt.applyCount)
	}
	if !strings.Contains(result.Text, `"mode":"hot-reload"`) {
		t.Fatalf("unexpected restart result: %s", result.Text)
	}
}

type browserServiceStub struct{}

func (s *browserServiceStub) Install(context.Context, browserpkg.Request) (map[string]any, error) {
	return map[string]any{"ok": true, "action": "install", "runtime": map[string]any{"ready": true}}, nil
}
func (s *browserServiceStub) Status(context.Context, browserpkg.Request) (map[string]any, error) {
	return map[string]any{"ok": true, "action": "status", "running": false, "profile": "kocort"}, nil
}
func (s *browserServiceStub) Start(context.Context, browserpkg.Request) (map[string]any, error) {
	return map[string]any{"ok": true, "action": "start", "running": true, "profile": "kocort"}, nil
}
func (s *browserServiceStub) Stop(context.Context, browserpkg.Request) (map[string]any, error) {
	return map[string]any{"ok": true, "action": "stop", "running": false, "profile": "kocort"}, nil
}
func (s *browserServiceStub) Profiles(context.Context, browserpkg.Request) (map[string]any, error) {
	return map[string]any{"ok": true, "action": "profiles", "profiles": []map[string]any{{"name": "kocort", "running": true}}}, nil
}
func (s *browserServiceStub) Tabs(context.Context, browserpkg.Request) (map[string]any, error) {
	return map[string]any{
		"ok":          true,
		"action":      "tabs",
		"targetId":    "page-1",
		"tabs":        []map[string]any{{"targetId": "page-1", "title": "Atlas", "url": "https://example.com/atlas", "type": "page"}},
		"tabCount":    1,
		"sessionTabs": []string{"page-1"},
	}, nil
}
func (s *browserServiceStub) Open(context.Context, browserpkg.OpenRequest) (map[string]any, error) {
	return map[string]any{
		"ok":          true,
		"action":      "open",
		"targetId":    "page-1",
		"tab":         map[string]any{"targetId": "page-1", "title": "Atlas", "url": "https://example.com/atlas", "type": "page"},
		"sessionTabs": []string{"page-1"},
	}, nil
}
func (s *browserServiceStub) Focus(context.Context, browserpkg.TargetRequest) (map[string]any, error) {
	return map[string]any{"ok": true, "action": "focus", "targetId": "page-1", "sessionTabs": []string{"page-1"}}, nil
}
func (s *browserServiceStub) Close(context.Context, browserpkg.TargetRequest) (map[string]any, error) {
	return map[string]any{"ok": true, "action": "close", "targetId": "page-1", "activeTabId": "", "sessionTabs": []string{}}, nil
}
func (s *browserServiceStub) Navigate(context.Context, browserpkg.NavigateRequest) (map[string]any, error) {
	return map[string]any{
		"ok":          true,
		"action":      "navigate",
		"targetId":    "page-1",
		"tab":         map[string]any{"targetId": "page-1", "title": "Next", "url": "https://example.com/next", "type": "page"},
		"sessionTabs": []string{"page-1"},
	}, nil
}
func (s *browserServiceStub) Snapshot(context.Context, browserpkg.SnapshotRequest) (map[string]any, error) {
	return map[string]any{
		"ok":       true,
		"action":   "snapshot",
		"targetId": "page-1",
		"format":   "ai",
		"snapshot": `[a1] button "Launch"`,
		"refs":     "aria",
	}, nil
}
func (s *browserServiceStub) Act(_ context.Context, req browserpkg.ActRequest) (map[string]any, error) {
	return map[string]any{
		"ok":       true,
		"action":   "act",
		"targetId": "page-1",
		"kind":     req.Kind,
		"selector": "button.launch",
	}, nil
}
func (s *browserServiceStub) Upload(_ context.Context, req browserpkg.UploadRequest) (map[string]any, error) {
	return map[string]any{
		"ok":       true,
		"action":   "upload",
		"targetId": "page-1",
		"paths":    req.Paths,
	}, nil
}
func (s *browserServiceStub) Dialog(_ context.Context, req browserpkg.DialogRequest) (map[string]any, error) {
	return map[string]any{
		"ok":         true,
		"action":     "dialog",
		"targetId":   "page-1",
		"accept":     req.Accept,
		"promptText": req.PromptText,
	}, nil
}
func (s *browserServiceStub) Screenshot(context.Context, browserpkg.ScreenshotRequest) (map[string]any, error) {
	return map[string]any{"ok": true, "action": "screenshot", "targetId": "page-1", "path": "/tmp/browser-1.png"}, nil
}
func (s *browserServiceStub) PDF(context.Context, browserpkg.TargetRequest) (map[string]any, error) {
	return map[string]any{"ok": true, "action": "pdf", "targetId": "page-1", "path": "/tmp/browser-1.pdf"}, nil
}
func (s *browserServiceStub) Console(context.Context, browserpkg.ConsoleRequest) (map[string]any, error) {
	return map[string]any{"ok": true, "action": "console", "targetId": "page-1", "messages": []map[string]any{{"type": "log", "text": "READY"}}}, nil
}
func (s *browserServiceStub) Errors(context.Context, browserpkg.DebugRequest) (map[string]any, error) {
	return map[string]any{"ok": true, "action": "errors", "targetId": "page-1", "errors": []map[string]any{{"message": "boom"}}}, nil
}
func (s *browserServiceStub) Requests(context.Context, browserpkg.RequestsRequest) (map[string]any, error) {
	return map[string]any{"ok": true, "action": "requests", "targetId": "page-1", "requests": []map[string]any{{"id": "r1", "url": "https://example.com/api"}}}, nil
}
func (s *browserServiceStub) TraceStart(context.Context, browserpkg.TraceStartRequest) (map[string]any, error) {
	return map[string]any{"ok": true, "action": "trace.start", "targetId": "page-1"}, nil
}
func (s *browserServiceStub) TraceStop(context.Context, browserpkg.TraceStopRequest) (map[string]any, error) {
	return map[string]any{"ok": true, "action": "trace.stop", "targetId": "page-1", "path": "/tmp/browser-trace.zip"}, nil
}
func (s *browserServiceStub) Download(context.Context, browserpkg.DownloadRequest) (map[string]any, error) {
	return map[string]any{"ok": true, "action": "download", "targetId": "page-1", "download": map[string]any{"path": "/tmp/file.txt"}}, nil
}
func (s *browserServiceStub) WaitDownload(context.Context, browserpkg.WaitDownloadRequest) (map[string]any, error) {
	return map[string]any{"ok": true, "action": "wait.download", "targetId": "page-1", "download": map[string]any{"path": "/tmp/file.txt"}}, nil
}

func TestBrowserToolLifecycle(t *testing.T) {
	tool := NewBrowserTool(&browserServiceStub{})
	workspaceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceDir, "README.md"), []byte("browser upload fixture"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	toolCtx := ToolContext{
		Runtime: &webRuntimeStub{},
		Run: AgentRunContext{
			Session:      core.SessionResolution{SessionKey: "agent:main:main"},
			WorkspaceDir: workspaceDir,
		},
	}
	openResult, err := tool.Execute(context.Background(), toolCtx, map[string]any{
		"action": "open",
		"url":    "https://example.com/atlas",
	})
	if err != nil {
		t.Fatalf("browser open: %v", err)
	}
	if !strings.Contains(openResult.Text, `"action":"open"`) || !strings.Contains(openResult.Text, `"targetId":"page-1"`) {
		t.Fatalf("unexpected browser open result: %s", openResult.Text)
	}
	if !strings.Contains(openResult.Text, `"sessionTabs":["page-1"]`) {
		t.Fatalf("expected session tabs in open result: %s", openResult.Text)
	}
	installResult, err := tool.Execute(context.Background(), toolCtx, map[string]any{
		"action": "install",
	})
	if err != nil {
		t.Fatalf("browser install: %v", err)
	}
	if !strings.Contains(installResult.Text, `"action":"install"`) {
		t.Fatalf("unexpected install result: %s", installResult.Text)
	}
	tabsResult, err := tool.Execute(context.Background(), toolCtx, map[string]any{
		"action": "tabs",
	})
	if err != nil {
		t.Fatalf("browser tabs: %v", err)
	}
	if !strings.Contains(tabsResult.Text, `"count":1`) {
		if !strings.Contains(tabsResult.Text, `"tabCount":1`) {
			t.Fatalf("unexpected tabs result: %s", tabsResult.Text)
		}
	}
	navResult, err := tool.Execute(context.Background(), toolCtx, map[string]any{
		"action":   "navigate",
		"targetId": "page-1",
		"url":      "https://example.com/next",
	})
	if err != nil {
		t.Fatalf("browser navigate: %v", err)
	}
	if !strings.Contains(navResult.Text, `"title":"Next"`) {
		t.Fatalf("unexpected navigate result: %s", navResult.Text)
	}
	snapshotResult, err := tool.Execute(context.Background(), toolCtx, map[string]any{
		"action":         "snapshot",
		"targetId":       "page-1",
		"snapshotFormat": "ai",
	})
	if err != nil {
		t.Fatalf("browser snapshot: %v", err)
	}
	if !strings.Contains(snapshotResult.Text, `"snapshot":"[a1] button`) {
		t.Fatalf("unexpected snapshot result: %s", snapshotResult.Text)
	}
	if !strings.Contains(snapshotResult.Text, "EXTERNAL_UNTRUSTED_CONTENT") {
		t.Fatalf("expected wrapped snapshot result: %s", snapshotResult.Text)
	}
	ariaResult, err := tool.Execute(context.Background(), toolCtx, map[string]any{
		"action":         "snapshot",
		"targetId":       "page-1",
		"snapshotFormat": "aria",
	})
	if err != nil {
		t.Fatalf("browser aria snapshot: %v", err)
	}
	if !strings.Contains(ariaResult.Text, `"action":"snapshot"`) {
		t.Fatalf("unexpected aria snapshot result: %s", ariaResult.Text)
	}
	actResult, err := tool.Execute(context.Background(), toolCtx, map[string]any{
		"action": "act",
		"request": map[string]any{
			"kind": "click",
			"ref":  "a1",
		},
	})
	if err != nil {
		t.Fatalf("browser act: %v", err)
	}
	if !strings.Contains(actResult.Text, `"action":"act"`) || !strings.Contains(actResult.Text, `"kind":"click"`) {
		t.Fatalf("unexpected act result: %s", actResult.Text)
	}
	fillResult, err := tool.Execute(context.Background(), toolCtx, map[string]any{
		"action": "act",
		"request": map[string]any{
			"kind": "fill",
			"ref":  "a1",
			"text": "hello",
		},
	})
	if err != nil {
		t.Fatalf("browser act fill: %v", err)
	}
	if !strings.Contains(fillResult.Text, `"kind":"fill"`) {
		t.Fatalf("unexpected fill result: %s", fillResult.Text)
	}
	uploadResult, err := tool.Execute(context.Background(), toolCtx, map[string]any{
		"action":   "upload",
		"targetId": "page-1",
		"paths":    []any{"README.md"},
		"inputRef": "a1",
	})
	if err != nil {
		t.Fatalf("browser upload: %v", err)
	}
	if !strings.Contains(uploadResult.Text, `"action":"upload"`) {
		t.Fatalf("unexpected upload result: %s", uploadResult.Text)
	}
	dialogResult, err := tool.Execute(context.Background(), toolCtx, map[string]any{
		"action":     "dialog",
		"targetId":   "page-1",
		"accept":     true,
		"promptText": "Atlas",
	})
	if err != nil {
		t.Fatalf("browser dialog: %v", err)
	}
	if !strings.Contains(dialogResult.Text, `"action":"dialog"`) {
		t.Fatalf("unexpected dialog result: %s", dialogResult.Text)
	}
	consoleResult, err := tool.Execute(context.Background(), toolCtx, map[string]any{
		"action": "console",
	})
	if err != nil {
		t.Fatalf("browser console: %v", err)
	}
	if !strings.Contains(consoleResult.Text, `READY`) {
		t.Fatalf("unexpected console result: %s", consoleResult.Text)
	}
	if !strings.Contains(consoleResult.Text, "EXTERNAL_UNTRUSTED_CONTENT") {
		t.Fatalf("expected wrapped console result: %s", consoleResult.Text)
	}
	errorsResult, err := tool.Execute(context.Background(), toolCtx, map[string]any{
		"action": "errors",
	})
	if err != nil {
		t.Fatalf("browser errors: %v", err)
	}
	if !strings.Contains(errorsResult.Text, `"message":"boom"`) || !strings.Contains(errorsResult.Text, "EXTERNAL_UNTRUSTED_CONTENT") {
		t.Fatalf("unexpected errors result: %s", errorsResult.Text)
	}
	requestsResult, err := tool.Execute(context.Background(), toolCtx, map[string]any{
		"action": "requests",
	})
	if err != nil {
		t.Fatalf("browser requests: %v", err)
	}
	if !strings.Contains(requestsResult.Text, `"url":"https://example.com/api"`) || !strings.Contains(requestsResult.Text, "EXTERNAL_UNTRUSTED_CONTENT") {
		t.Fatalf("unexpected requests result: %s", requestsResult.Text)
	}
	traceStartResult, err := tool.Execute(context.Background(), toolCtx, map[string]any{
		"action":      "trace.start",
		"screenshots": true,
		"snapshots":   true,
	})
	if err != nil {
		t.Fatalf("browser trace.start: %v", err)
	}
	if !strings.Contains(traceStartResult.Text, `"action":"trace.start"`) {
		t.Fatalf("unexpected trace.start result: %s", traceStartResult.Text)
	}
	traceStopResult, err := tool.Execute(context.Background(), toolCtx, map[string]any{
		"action": "trace.stop",
	})
	if err != nil {
		t.Fatalf("browser trace.stop: %v", err)
	}
	if !strings.Contains(traceStopResult.Text, `"path":"/tmp/browser-trace.zip"`) {
		t.Fatalf("unexpected trace.stop result: %s", traceStopResult.Text)
	}
	downloadResult, err := tool.Execute(context.Background(), toolCtx, map[string]any{
		"action": "download",
		"ref":    "a1",
		"path":   "downloads/file.txt",
	})
	if err != nil {
		t.Fatalf("browser download: %v", err)
	}
	if !strings.Contains(downloadResult.Text, `"action":"download"`) {
		t.Fatalf("unexpected download result: %s", downloadResult.Text)
	}
	waitDownloadResult, err := tool.Execute(context.Background(), toolCtx, map[string]any{
		"action": "wait.download",
	})
	if err != nil {
		t.Fatalf("browser wait.download: %v", err)
	}
	if !strings.Contains(waitDownloadResult.Text, `"action":"wait.download"`) {
		t.Fatalf("unexpected wait.download result: %s", waitDownloadResult.Text)
	}
	screenshotResult, err := tool.Execute(context.Background(), toolCtx, map[string]any{
		"action": "screenshot",
	})
	if err != nil {
		t.Fatalf("browser screenshot: %v", err)
	}
	if screenshotResult.MediaURL != "file:///tmp/browser-1.png" {
		t.Fatalf("unexpected screenshot media url: %#v", screenshotResult.MediaURL)
	}
	closeResult, err := tool.Execute(context.Background(), toolCtx, map[string]any{
		"action":   "close",
		"targetId": "page-1",
	})
	if err != nil {
		t.Fatalf("browser close: %v", err)
	}
	if !strings.Contains(closeResult.Text, `"status":"ok"`) {
		if !strings.Contains(closeResult.Text, `"ok":true`) {
			t.Fatalf("unexpected close result: %s", closeResult.Text)
		}
	}
}
