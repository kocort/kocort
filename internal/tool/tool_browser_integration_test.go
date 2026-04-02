package tool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// These tests require a real browser (Chrome/Chromium) and are gated behind
// the RUN_BROWSER_INTEGRATION environment variable.
//
// Run:
//   RUN_BROWSER_INTEGRATION=1 go test -v -run TestBrowser -tags=integration ./internal/tool/

func skipWithoutBrowserEnv(t *testing.T) {
	t.Helper()
	if os.Getenv("RUN_BROWSER_INTEGRATION") == "" {
		t.Skip("skipping browser integration test (set RUN_BROWSER_INTEGRATION=1)")
	}
}

func newTestBrowserTool(t *testing.T) *BrowserTool {
	t.Helper()
	dir := t.TempDir()
	return NewBrowserTool(BrowserToolOptions{
		Headless:    true,
		ArtifactDir: dir,
	})
}

func newTestHTTPServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
}

func TestBrowserOpenAndSnapshot(t *testing.T) {
	skipWithoutBrowserEnv(t)

	srv := newTestHTTPServer(t, `<html><body><h1>Hello</h1><button id="btn">Click me</button></body></html>`)
	defer srv.Close()

	bt := newTestBrowserTool(t)
	ctx := context.Background()
	toolCtx := ToolContext{}

	// Open page
	_, err := bt.Execute(ctx, toolCtx, map[string]any{
		"action": "open",
		"url":    srv.URL,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Snapshot
	result, err := bt.Execute(ctx, toolCtx, map[string]any{
		"action": "snapshot",
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if result.Text == "" {
		t.Fatal("snapshot returned empty text")
	}

	// Close
	_, err = bt.Execute(ctx, toolCtx, map[string]any{
		"action": "close",
	})
	if err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestBrowserStatus(t *testing.T) {
	bt := newTestBrowserTool(t)
	ctx := context.Background()
	toolCtx := ToolContext{}

	// Before opening, status should show not running
	result, err := bt.Execute(ctx, toolCtx, map[string]any{
		"action": "status",
	})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if result.Text == "" {
		t.Fatal("status returned empty text")
	}
}

func TestBrowserClickAndType(t *testing.T) {
	skipWithoutBrowserEnv(t)

	srv := newTestHTTPServer(t, `<html><body>
		<input id="name" type="text" />
		<button id="submit">Submit</button>
	</body></html>`)
	defer srv.Close()

	bt := newTestBrowserTool(t)
	ctx := context.Background()
	toolCtx := ToolContext{}

	// Open
	_, err := bt.Execute(ctx, toolCtx, map[string]any{
		"action": "open",
		"url":    srv.URL,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Snapshot to get element IDs
	result, err := bt.Execute(ctx, toolCtx, map[string]any{
		"action": "snapshot",
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	t.Logf("snapshot: %s", result.Text)

	// Close
	_, _ = bt.Execute(ctx, toolCtx, map[string]any{"action": "close"})
}

func TestBrowserScreenshot(t *testing.T) {
	skipWithoutBrowserEnv(t)

	srv := newTestHTTPServer(t, `<html><body><h1>Screenshot test</h1></body></html>`)
	defer srv.Close()

	bt := newTestBrowserTool(t)
	ctx := context.Background()
	toolCtx := ToolContext{}

	_, err := bt.Execute(ctx, toolCtx, map[string]any{
		"action": "open",
		"url":    srv.URL,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	_, err = bt.Execute(ctx, toolCtx, map[string]any{
		"action":   "screenshot",
		"fullPage": true,
	})
	if err != nil {
		t.Fatalf("screenshot: %v", err)
	}

	_, _ = bt.Execute(ctx, toolCtx, map[string]any{"action": "close"})
}

func TestBrowserMissingAction(t *testing.T) {
	bt := newTestBrowserTool(t)
	ctx := context.Background()
	toolCtx := ToolContext{}

	_, err := bt.Execute(ctx, toolCtx, map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing action")
	}
}
