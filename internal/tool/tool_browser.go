package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/libi/ko-browser/browser"
)

// BrowserToolOptions holds configuration for the browser tool.
type BrowserToolOptions struct {
	Headless      bool
	ArtifactDir   string
	Profile       string
	Timeout       time.Duration
	ScreenshotDir string
	DownloadPath  string
	UserAgent     string
	Proxy         string
	ProxyBypass   string
}

type BrowserTool struct {
	opts     BrowserToolOptions
	mu       sync.Mutex
	instance *browser.Browser
}

func NewBrowserTool(opts BrowserToolOptions) *BrowserTool {
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.ScreenshotDir == "" && opts.ArtifactDir != "" {
		opts.ScreenshotDir = filepath.Join(opts.ArtifactDir, "screenshots")
	}
	if opts.DownloadPath == "" && opts.ArtifactDir != "" {
		opts.DownloadPath = filepath.Join(opts.ArtifactDir, "downloads")
	}
	return &BrowserTool{opts: opts}
}

func (t *BrowserTool) Name() string { return "browser" }

func (t *BrowserTool) Description() string {
	return "Control the browser. Use snapshot to inspect page structure and get numeric element IDs, then interact using those IDs. " +
		"Workflow: open a URL → snapshot → interact (click/type/fill/press) → re-snapshot. " +
		"Numeric refs from snapshot (e.g. 3) are used for targeting elements: click 3, type 3 \"text\". " +
		"The snapshot is token-efficient and provides role+name-based element descriptions."
}

func (t *BrowserTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum": []string{
						"open", "snapshot", "screenshot", "click", "dblclick",
						"type", "fill", "press", "hover", "focus", "check", "uncheck",
						"select", "scroll", "drag", "back", "forward", "reload",
						"wait_load", "wait_text", "wait_selector", "wait_url", "wait_hidden",
						"tab_list", "tab_new", "tab_switch", "tab_close",
						"get_title", "get_url", "get_text", "get_html", "get_value", "get_attr",
						"eval", "console_start", "console_messages", "console_clear",
						"errors_list", "errors_clear",
						"upload", "download", "wait_download",
						"set_viewport", "set_device", "set_geo",
						"trace_start", "trace_stop",
						"pdf", "highlight",
						"status", "close",
					},
					"description": "The browser action to perform.",
				},
				"url": map[string]any{
					"type":        "string",
					"description": "URL for open action.",
				},
				"id": map[string]any{
					"type":        "integer",
					"description": "Numeric element ID from snapshot for interaction actions (click, type, fill, hover, focus, etc.).",
				},
				"text": map[string]any{
					"type":        "string",
					"description": "Text for type/fill actions, or text to wait for.",
				},
				"key": map[string]any{
					"type":        "string",
					"description": "Key name for press action (e.g. Enter, Tab, Escape, ArrowDown, Control+a).",
				},
				"selector": map[string]any{
					"type":        "string",
					"description": "CSS selector for wait_selector, wait_hidden, or scoped snapshot.",
				},
				"values": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Values for select action.",
				},
				"direction": map[string]any{
					"type":        "string",
					"enum":        []string{"up", "down", "left", "right"},
					"description": "Scroll direction.",
				},
				"pixels": map[string]any{
					"type":        "integer",
					"description": "Number of pixels to scroll.",
				},
				"srcId": map[string]any{
					"type":        "integer",
					"description": "Source element ID for drag action.",
				},
				"dstId": map[string]any{
					"type":        "integer",
					"description": "Destination element ID for drag action.",
				},
				"tabIndex": map[string]any{
					"type":        "integer",
					"description": "Tab index for tab_switch/tab_close.",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "File path for screenshot, pdf, trace_stop, download, etc.",
				},
				"paths": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "File paths for upload action.",
				},
				"fn": map[string]any{
					"type":        "string",
					"description": "JavaScript expression for eval action.",
				},
				"fullPage": map[string]any{
					"type":        "boolean",
					"description": "Whether to capture full page for screenshot.",
				},
				"interactive": map[string]any{
					"type":        "boolean",
					"description": "Show only interactive elements in snapshot.",
				},
				"compact": map[string]any{
					"type":        "boolean",
					"description": "Compact snapshot mode.",
				},
				"depth": map[string]any{
					"type":        "integer",
					"description": "Max depth for snapshot.",
				},
				"attr": map[string]any{
					"type":        "string",
					"description": "Attribute name for get_attr.",
				},
				"width": map[string]any{
					"type":        "integer",
					"description": "Viewport width for set_viewport.",
				},
				"height": map[string]any{
					"type":        "integer",
					"description": "Viewport height for set_viewport.",
				},
				"device": map[string]any{
					"type":        "string",
					"description": "Device name for set_device (e.g. 'iPhone 12').",
				},
				"lat": map[string]any{
					"type":        "number",
					"description": "Latitude for set_geo.",
				},
				"lon": map[string]any{
					"type":        "number",
					"description": "Longitude for set_geo.",
				},
				"level": map[string]any{
					"type":        "string",
					"description": "Console level filter.",
				},
				"pattern": map[string]any{
					"type":        "string",
					"description": "URL pattern for wait_url.",
				},
				"headless": map[string]any{
					"type":        "boolean",
					"description": "Run browser in headless mode. Only applies when browser is first launched.",
				},
			},
			"required":             []string{"action"},
			"additionalProperties": false,
		},
	}
}

// ensureBrowser lazily creates a browser instance if one does not exist.
func (t *BrowserTool) ensureBrowser(headlessOverride *bool) (*browser.Browser, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.instance != nil {
		return t.instance, nil
	}

	headless := t.opts.Headless
	if headlessOverride != nil {
		headless = *headlessOverride
	}

	opts := browser.Options{
		Headless:      headless,
		Timeout:       t.opts.Timeout,
		Profile:       t.opts.Profile,
		DownloadPath:  t.opts.DownloadPath,
		ScreenshotDir: t.opts.ScreenshotDir,
		UserAgent:     t.opts.UserAgent,
		Proxy:         t.opts.Proxy,
		ProxyBypass:   t.opts.ProxyBypass,
	}

	b, err := browser.New(opts)
	if err != nil {
		return nil, fmt.Errorf("launch browser: %w", err)
	}
	t.instance = b
	return b, nil
}

func (t *BrowserTool) Execute(ctx context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	action, err := ReadStringParam(args, "action", true)
	if err != nil {
		return core.ToolResult{}, err
	}
	action = strings.ToLower(strings.TrimSpace(action))

	// status doesn't need a browser instance
	if action == "status" {
		return t.executeStatus()
	}
	if action == "close" {
		return t.executeClose()
	}

	// Parse optional headless override
	var headless *bool
	if raw, ok := args["headless"]; ok && raw != nil {
		if v, ok := raw.(bool); ok {
			headless = &v
		}
	}

	b, err := t.ensureBrowser(headless)
	if err != nil {
		return core.ToolResult{}, err
	}

	switch action {
	case "open":
		return t.executeOpen(b, args)
	case "snapshot":
		return t.executeSnapshot(b, args)
	case "screenshot":
		return t.executeScreenshot(b, args)
	case "click":
		return t.executeClick(b, args)
	case "dblclick":
		return t.executeDblClick(b, args)
	case "type":
		return t.executeType(b, args)
	case "fill":
		return t.executeFill(b, args)
	case "press":
		return t.executePress(b, args)
	case "hover":
		return t.executeHover(b, args)
	case "focus":
		return t.executeFocus(b, args)
	case "check":
		return t.executeCheck(b, args)
	case "uncheck":
		return t.executeUncheck(b, args)
	case "select":
		return t.executeSelect(b, args)
	case "scroll":
		return t.executeScroll(b, args)
	case "drag":
		return t.executeDrag(b, args)
	case "back":
		return wrapBrowserErr(b.Back(), "back")
	case "forward":
		return wrapBrowserErr(b.Forward(), "forward")
	case "reload":
		return wrapBrowserErr(b.Reload(), "reload")
	case "wait_load":
		return wrapBrowserErr(b.WaitLoad(), "wait_load")
	case "wait_text":
		text := readOptionalString(args, "text")
		if text == "" {
			return core.ToolResult{}, ToolInputError{Message: `wait_text requires "text"`}
		}
		return wrapBrowserErr(b.WaitText(text), "wait_text")
	case "wait_selector":
		sel := readOptionalString(args, "selector")
		if sel == "" {
			return core.ToolResult{}, ToolInputError{Message: `wait_selector requires "selector"`}
		}
		return wrapBrowserErr(b.WaitSelector(sel), "wait_selector")
	case "wait_url":
		pat := readOptionalString(args, "pattern")
		if pat == "" {
			pat = readOptionalString(args, "url")
		}
		if pat == "" {
			return core.ToolResult{}, ToolInputError{Message: `wait_url requires "pattern" or "url"`}
		}
		return wrapBrowserErr(b.WaitURL(pat), "wait_url")
	case "wait_hidden":
		sel := readOptionalString(args, "selector")
		if sel == "" {
			return core.ToolResult{}, ToolInputError{Message: `wait_hidden requires "selector"`}
		}
		return wrapBrowserErr(b.WaitHidden(sel), "wait_hidden")
	case "tab_list":
		return t.executeTabList(b)
	case "tab_new":
		url := readOptionalString(args, "url")
		return wrapBrowserErr(b.TabNew(url), "tab_new")
	case "tab_switch":
		idx, err := ReadOptionalIntParam(args, "tabIndex")
		if err != nil {
			return core.ToolResult{}, err
		}
		return wrapBrowserErr(b.TabSwitch(idx), "tab_switch")
	case "tab_close":
		idx, err := ReadOptionalIntParam(args, "tabIndex")
		if err != nil {
			return core.ToolResult{}, err
		}
		if idx == 0 {
			idx = -1 // default: close current
		}
		return wrapBrowserErr(b.TabClose(idx), "tab_close")
	case "get_title":
		return t.executeGetString(func() (string, error) { return b.GetTitle() }, "title")
	case "get_url":
		return t.executeGetString(func() (string, error) { return b.GetURL() }, "url")
	case "get_text":
		id, err := readRequiredIntParam(args, "id")
		if err != nil {
			return core.ToolResult{}, err
		}
		return t.executeGetString(func() (string, error) { return b.GetText(id) }, "text")
	case "get_html":
		id, err := readRequiredIntParam(args, "id")
		if err != nil {
			return core.ToolResult{}, err
		}
		return t.executeGetString(func() (string, error) { return b.GetHTML(id) }, "html")
	case "get_value":
		id, err := readRequiredIntParam(args, "id")
		if err != nil {
			return core.ToolResult{}, err
		}
		return t.executeGetString(func() (string, error) { return b.GetValue(id) }, "value")
	case "get_attr":
		id, err := readRequiredIntParam(args, "id")
		if err != nil {
			return core.ToolResult{}, err
		}
		attr := readOptionalString(args, "attr")
		if attr == "" {
			return core.ToolResult{}, ToolInputError{Message: `get_attr requires "attr"`}
		}
		return t.executeGetString(func() (string, error) { return b.GetAttr(id, attr) }, "attr")
	case "eval":
		fn := readOptionalString(args, "fn")
		if fn == "" {
			return core.ToolResult{}, ToolInputError{Message: `eval requires "fn"`}
		}
		return t.executeGetString(func() (string, error) { return b.Eval(fn) }, "eval")
	case "console_start":
		return wrapBrowserErr(b.ConsoleStart(), "console_start")
	case "console_messages":
		return t.executeConsoleMessages(b, args)
	case "console_clear":
		b.ConsoleClear()
		return JSONResult(map[string]any{"ok": true, "action": "console_clear"})
	case "errors_list":
		return t.executeErrorsList(b)
	case "errors_clear":
		b.PageErrorsClear()
		return JSONResult(map[string]any{"ok": true, "action": "errors_clear"})
	case "upload":
		return t.executeUpload(toolCtx, b, args)
	case "download":
		return t.executeDownload(b, args)
	case "wait_download":
		return t.executeWaitDownload(b, args)
	case "set_viewport":
		return t.executeSetViewport(b, args)
	case "set_device":
		device := readOptionalString(args, "device")
		if device == "" {
			return core.ToolResult{}, ToolInputError{Message: `set_device requires "device"`}
		}
		return wrapBrowserErr(b.SetDevice(device), "set_device")
	case "set_geo":
		return t.executeSetGeo(b, args)
	case "trace_start":
		return wrapBrowserErr(b.TraceStart(), "trace_start")
	case "trace_stop":
		path := readOptionalString(args, "path")
		if path == "" && t.opts.ArtifactDir != "" {
			path = filepath.Join(t.opts.ArtifactDir, fmt.Sprintf("trace-%d.json", time.Now().UnixMilli()))
		}
		return wrapBrowserErr(b.TraceStop(path), "trace_stop")
	case "pdf":
		return t.executePDF(b, args)
	case "highlight":
		id, err := readRequiredIntParam(args, "id")
		if err != nil {
			return core.ToolResult{}, err
		}
		return wrapBrowserErr(b.Highlight(id), "highlight")
	default:
		return JSONResult(map[string]any{"ok": false, "error": fmt.Sprintf("unsupported action %q", action)})
	}
}

// --- Action implementations ---

func (t *BrowserTool) executeStatus() (core.ToolResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	running := t.instance != nil
	return JSONResult(map[string]any{
		"ok":      true,
		"action":  "status",
		"running": running,
	})
}

func (t *BrowserTool) executeClose() (core.ToolResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.instance != nil {
		t.instance.Close()
		t.instance = nil
	}
	return JSONResult(map[string]any{"ok": true, "action": "close"})
}

func (t *BrowserTool) executeOpen(b *browser.Browser, args map[string]any) (core.ToolResult, error) {
	url := readOptionalString(args, "url")
	if url == "" {
		return core.ToolResult{}, ToolInputError{Message: `open requires "url"`}
	}
	if err := b.Open(url); err != nil {
		return core.ToolResult{}, fmt.Errorf("open %s: %w", url, err)
	}
	// Auto-start console listening
	_ = b.ConsoleStart()
	title, _ := b.GetTitle()
	currentURL, _ := b.GetURL()
	return JSONResult(map[string]any{
		"ok":     true,
		"action": "open",
		"title":  title,
		"url":    currentURL,
	})
}

func (t *BrowserTool) executeSnapshot(b *browser.Browser, args map[string]any) (core.ToolResult, error) {
	interactive, _ := ReadBoolParam(args, "interactive")
	compact, _ := ReadBoolParam(args, "compact")
	depth, _ := ReadOptionalIntParam(args, "depth")
	selector := readOptionalString(args, "selector")

	snap, err := b.Snapshot(browser.SnapshotOptions{
		InteractiveOnly: interactive,
		Compact:         compact,
		MaxDepth:        depth,
		Selector:        selector,
	})
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("snapshot: %w", err)
	}
	payload := map[string]any{
		"ok":       true,
		"action":   "snapshot",
		"snapshot": snap.Text,
		"count":    snap.RawCount,
	}
	return BrowserWrappedJSONResult("snapshot", payload)
}

func (t *BrowserTool) executeScreenshot(b *browser.Browser, args map[string]any) (core.ToolResult, error) {
	fullPage, _ := ReadBoolParam(args, "fullPage")
	path := readOptionalString(args, "path")
	if path == "" {
		dir := t.opts.ScreenshotDir
		if dir == "" {
			dir = os.TempDir()
		}
		_ = os.MkdirAll(dir, 0755)
		path = filepath.Join(dir, fmt.Sprintf("screenshot-%d.png", time.Now().UnixMilli()))
	}

	err := b.Screenshot(path, browser.ScreenshotOptions{FullPage: fullPage})
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("screenshot: %w", err)
	}
	return BrowserImageResult("screenshot", path, map[string]any{
		"ok":     true,
		"action": "screenshot",
		"path":   path,
	})
}

func (t *BrowserTool) executeClick(b *browser.Browser, args map[string]any) (core.ToolResult, error) {
	id, err := readRequiredIntParam(args, "id")
	if err != nil {
		return core.ToolResult{}, err
	}
	return wrapBrowserErr(b.Click(id), "click")
}

func (t *BrowserTool) executeDblClick(b *browser.Browser, args map[string]any) (core.ToolResult, error) {
	id, err := readRequiredIntParam(args, "id")
	if err != nil {
		return core.ToolResult{}, err
	}
	return wrapBrowserErr(b.DblClick(id), "dblclick")
}

func (t *BrowserTool) executeType(b *browser.Browser, args map[string]any) (core.ToolResult, error) {
	id, err := readRequiredIntParam(args, "id")
	if err != nil {
		return core.ToolResult{}, err
	}
	text := readOptionalString(args, "text")
	return wrapBrowserErr(b.Type(id, text), "type")
}

func (t *BrowserTool) executeFill(b *browser.Browser, args map[string]any) (core.ToolResult, error) {
	id, err := readRequiredIntParam(args, "id")
	if err != nil {
		return core.ToolResult{}, err
	}
	text := readOptionalString(args, "text")
	return wrapBrowserErr(b.Fill(id, text), "fill")
}

func (t *BrowserTool) executePress(b *browser.Browser, args map[string]any) (core.ToolResult, error) {
	key := readOptionalString(args, "key")
	if key == "" {
		return core.ToolResult{}, ToolInputError{Message: `press requires "key"`}
	}
	return wrapBrowserErr(b.Press(key), "press")
}

func (t *BrowserTool) executeHover(b *browser.Browser, args map[string]any) (core.ToolResult, error) {
	id, err := readRequiredIntParam(args, "id")
	if err != nil {
		return core.ToolResult{}, err
	}
	return wrapBrowserErr(b.Hover(id), "hover")
}

func (t *BrowserTool) executeFocus(b *browser.Browser, args map[string]any) (core.ToolResult, error) {
	id, err := readRequiredIntParam(args, "id")
	if err != nil {
		return core.ToolResult{}, err
	}
	return wrapBrowserErr(b.Focus(id), "focus")
}

func (t *BrowserTool) executeCheck(b *browser.Browser, args map[string]any) (core.ToolResult, error) {
	id, err := readRequiredIntParam(args, "id")
	if err != nil {
		return core.ToolResult{}, err
	}
	return wrapBrowserErr(b.Check(id), "check")
}

func (t *BrowserTool) executeUncheck(b *browser.Browser, args map[string]any) (core.ToolResult, error) {
	id, err := readRequiredIntParam(args, "id")
	if err != nil {
		return core.ToolResult{}, err
	}
	return wrapBrowserErr(b.Uncheck(id), "uncheck")
}

func (t *BrowserTool) executeSelect(b *browser.Browser, args map[string]any) (core.ToolResult, error) {
	id, err := readRequiredIntParam(args, "id")
	if err != nil {
		return core.ToolResult{}, err
	}
	values, err := readOptionalStringSlice(args, "values")
	if err != nil {
		return core.ToolResult{}, err
	}
	if len(values) == 0 {
		return core.ToolResult{}, ToolInputError{Message: `select requires "values"`}
	}
	return wrapBrowserErr(b.Select(id, values...), "select")
}

func (t *BrowserTool) executeScroll(b *browser.Browser, args map[string]any) (core.ToolResult, error) {
	direction := readOptionalString(args, "direction")
	if direction == "" {
		direction = "down"
	}
	pixels, err := ReadOptionalIntParam(args, "pixels")
	if err != nil {
		return core.ToolResult{}, err
	}
	if pixels <= 0 {
		pixels = 300
	}
	return wrapBrowserErr(b.Scroll(direction, pixels), "scroll")
}

func (t *BrowserTool) executeDrag(b *browser.Browser, args map[string]any) (core.ToolResult, error) {
	srcId, err := readRequiredIntParam(args, "srcId")
	if err != nil {
		return core.ToolResult{}, err
	}
	dstId, err := readRequiredIntParam(args, "dstId")
	if err != nil {
		return core.ToolResult{}, err
	}
	return wrapBrowserErr(b.Drag(srcId, dstId), "drag")
}

func (t *BrowserTool) executeTabList(b *browser.Browser) (core.ToolResult, error) {
	tabs, err := b.TabList()
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("tab_list: %w", err)
	}
	tabsJSON := make([]map[string]any, len(tabs))
	for i, tab := range tabs {
		tabsJSON[i] = map[string]any{
			"index":  tab.Index,
			"url":    tab.URL,
			"title":  tab.Title,
			"active": tab.Active,
		}
	}
	return BrowserWrappedJSONResult("tabs", map[string]any{
		"ok":     true,
		"action": "tab_list",
		"tabs":   tabsJSON,
	})
}

func (t *BrowserTool) executeGetString(fn func() (string, error), field string) (core.ToolResult, error) {
	val, err := fn()
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("%s: %w", field, err)
	}
	return BrowserWrappedJSONResult(field, map[string]any{
		"ok":     true,
		"action": "get_" + field,
		field:    val,
	})
}

func (t *BrowserTool) executeConsoleMessages(b *browser.Browser, args map[string]any) (core.ToolResult, error) {
	level := readOptionalString(args, "level")
	var msgs []browser.ConsoleMessage
	var err error
	if level != "" {
		msgs, err = b.ConsoleMessagesByLevel(level)
	} else {
		msgs, err = b.ConsoleMessages()
	}
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("console_messages: %w", err)
	}
	msgsJSON := make([]map[string]any, len(msgs))
	for i, m := range msgs {
		msgsJSON[i] = map[string]any{
			"level": m.Level,
			"text":  m.Text,
		}
	}
	return BrowserWrappedJSONResult("console", map[string]any{
		"ok":       true,
		"action":   "console_messages",
		"messages": msgsJSON,
	})
}

func (t *BrowserTool) executeErrorsList(b *browser.Browser) (core.ToolResult, error) {
	errs, err := b.PageErrors()
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("errors_list: %w", err)
	}
	errsJSON := make([]map[string]any, len(errs))
	for i, e := range errs {
		errsJSON[i] = map[string]any{
			"message": e.Message,
			"url":     e.URL,
			"line":    e.Line,
			"column":  e.Column,
		}
	}
	return BrowserWrappedJSONResult("errors", map[string]any{
		"ok":     true,
		"action": "errors_list",
		"errors": errsJSON,
	})
}

func (t *BrowserTool) executeUpload(toolCtx ToolContext, b *browser.Browser, args map[string]any) (core.ToolResult, error) {
	id, err := readRequiredIntParam(args, "id")
	if err != nil {
		return core.ToolResult{}, err
	}
	paths, err := resolveUploadPaths(toolCtx, args)
	if err != nil {
		return core.ToolResult{}, err
	}
	return wrapBrowserErr(b.Upload(id, paths...), "upload")
}

func (t *BrowserTool) executeDownload(b *browser.Browser, args map[string]any) (core.ToolResult, error) {
	id, err := readRequiredIntParam(args, "id")
	if err != nil {
		return core.ToolResult{}, err
	}
	saveDir := readOptionalString(args, "path")
	if saveDir == "" {
		saveDir = t.opts.DownloadPath
	}
	if saveDir == "" {
		saveDir = os.TempDir()
	}
	downloadPath, downloadErr := b.Download(id, saveDir)
	if downloadErr != nil {
		return core.ToolResult{}, fmt.Errorf("download: %w", downloadErr)
	}
	return JSONResult(map[string]any{
		"ok":     true,
		"action": "download",
		"path":   downloadPath,
	})
}

func (t *BrowserTool) executeWaitDownload(b *browser.Browser, args map[string]any) (core.ToolResult, error) {
	saveDir := readOptionalString(args, "path")
	if saveDir == "" {
		saveDir = t.opts.DownloadPath
	}
	if saveDir == "" {
		saveDir = os.TempDir()
	}
	downloadPath, err := b.WaitDownload(saveDir)
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("wait_download: %w", err)
	}
	return JSONResult(map[string]any{
		"ok":     true,
		"action": "wait_download",
		"path":   downloadPath,
	})
}

func (t *BrowserTool) executeSetViewport(b *browser.Browser, args map[string]any) (core.ToolResult, error) {
	width, err := readRequiredIntParam(args, "width")
	if err != nil {
		return core.ToolResult{}, err
	}
	height, err := readRequiredIntParam(args, "height")
	if err != nil {
		return core.ToolResult{}, err
	}
	return wrapBrowserErr(b.SetViewport(width, height), "set_viewport")
}

func (t *BrowserTool) executeSetGeo(b *browser.Browser, args map[string]any) (core.ToolResult, error) {
	lat, err := readRequiredFloatParam(args, "lat")
	if err != nil {
		return core.ToolResult{}, err
	}
	lon, err := readRequiredFloatParam(args, "lon")
	if err != nil {
		return core.ToolResult{}, err
	}
	return wrapBrowserErr(b.SetGeo(lat, lon), "set_geo")
}

func (t *BrowserTool) executePDF(b *browser.Browser, args map[string]any) (core.ToolResult, error) {
	path := readOptionalString(args, "path")
	if path == "" {
		dir := t.opts.ArtifactDir
		if dir == "" {
			dir = os.TempDir()
		}
		_ = os.MkdirAll(dir, 0755)
		path = filepath.Join(dir, fmt.Sprintf("page-%d.pdf", time.Now().UnixMilli()))
	}
	err := b.PDF(path)
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("pdf: %w", err)
	}
	return JSONResult(map[string]any{
		"ok":     true,
		"action": "pdf",
		"path":   path,
	})
}

// --- Helper functions ---

// readOptionalString reads an optional string parameter, returning "" if absent.
func readOptionalString(args map[string]any, key string) string {
	val, _ := ReadStringParam(args, key, false)
	return val
}

func wrapBrowserErr(err error, action string) (core.ToolResult, error) {
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("%s: %w", action, err)
	}
	return JSONResult(map[string]any{"ok": true, "action": action})
}

func readRequiredIntParam(args map[string]any, key string) (int, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return 0, ToolInputError{Message: fmt.Sprintf("parameter %q is required", key)}
	}
	switch v := raw.(type) {
	case float64:
		return int(v), nil
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0, ToolInputError{Message: fmt.Sprintf("parameter %q must be an integer", key)}
		}
		return int(n), nil
	default:
		return 0, ToolInputError{Message: fmt.Sprintf("parameter %q must be an integer", key)}
	}
}

func readRequiredFloatParam(args map[string]any, key string) (float64, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return 0, ToolInputError{Message: fmt.Sprintf("parameter %q is required", key)}
	}
	switch v := raw.(type) {
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case json.Number:
		return v.Float64()
	default:
		return 0, ToolInputError{Message: fmt.Sprintf("parameter %q must be a number", key)}
	}
}

func readOptionalStringSlice(args map[string]any, key string) ([]string, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil, nil
	}
	switch typed := raw.(type) {
	case []string:
		return typed, nil
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, ToolInputError{Message: fmt.Sprintf("parameter %q must be an array of strings", key)}
			}
			if strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out, nil
	default:
		return nil, ToolInputError{Message: fmt.Sprintf("parameter %q must be an array of strings", key)}
	}
}

// resolveUploadPaths resolves workspace-relative upload paths to absolute paths.
func resolveUploadPaths(toolCtx ToolContext, args map[string]any) ([]string, error) {
	raw, ok := args["paths"]
	if !ok || raw == nil {
		return nil, ToolInputError{Message: `upload requires "paths"`}
	}
	var input []string
	switch typed := raw.(type) {
	case []string:
		input = typed
	case []any:
		input = make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, ToolInputError{Message: `paths must be an array of strings`}
			}
			input = append(input, text)
		}
	default:
		return nil, ToolInputError{Message: `paths must be an array of strings`}
	}
	out := make([]string, 0, len(input))
	for _, value := range input {
		_, _, absPath, err := resolveWorkspaceToolPath(toolCtx, value)
		if err != nil {
			return nil, err
		}
		out = append(out, absPath)
	}
	if len(out) == 0 {
		return nil, ToolInputError{Message: `upload requires at least one path`}
	}
	return out, nil
}
