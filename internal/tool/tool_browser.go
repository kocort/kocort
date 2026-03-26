package tool

import (
	"context"
	"fmt"
	"strings"
	"time"

	browserpkg "github.com/kocort/kocort/internal/browser"
	"github.com/kocort/kocort/internal/core"
)

type BrowserTool struct {
	service browserpkg.Service
}

func NewBrowserTool(service browserpkg.Service) *BrowserTool {
	return &BrowserTool{service: service}
}

func (t *BrowserTool) Name() string { return "browser" }

func (t *BrowserTool) Description() string {
	return "Control browser profiles, tabs, navigation, screenshots, PDFs, and console output."
}

func (t *BrowserTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":         map[string]any{"type": "string", "description": "One of install, status, start, stop, profiles, tabs, open, focus, close, snapshot, screenshot, navigate, console, errors, requests, trace.start, trace.stop, pdf, upload, dialog, act, download, wait.download."},
				"target":         map[string]any{"type": "string", "description": "Browser target surface. Accepts host, sandbox, or node."},
				"node":           map[string]any{"type": "string", "description": "Optional node id when target=node."},
				"profile":        map[string]any{"type": "string", "description": "Optional browser profile name."},
				"headless":       map[string]any{"type": "boolean", "description": "Whether to run the browser in headless mode. When false, a visible browser window is opened. Only takes effect on start/open when a new browser session is launched."},
				"targetUrl":      map[string]any{"type": "string", "description": "Preferred target URL for open or navigate."},
				"url":            map[string]any{"type": "string", "description": "Legacy URL field for open or navigate."},
				"targetId":       map[string]any{"type": "string", "description": "Optional browser target id."},
				"limit":          map[string]any{"type": "integer", "description": "Optional result limit."},
				"maxChars":       map[string]any{"type": "integer", "description": "Optional text cap for snapshot output."},
				"mode":           map[string]any{"type": "string"},
				"snapshotFormat": map[string]any{"type": "string"},
				"refs":           map[string]any{"type": "string"},
				"interactive":    map[string]any{"type": "boolean"},
				"compact":        map[string]any{"type": "boolean"},
				"depth":          map[string]any{"type": "integer"},
				"selector":       map[string]any{"type": "string"},
				"frame":          map[string]any{"type": "string"},
				"labels":         map[string]any{"type": "boolean"},
				"fullPage":       map[string]any{"type": "boolean"},
				"ref":            map[string]any{"type": "string"},
				"startRef":       map[string]any{"type": "string"},
				"endRef":         map[string]any{"type": "string"},
				"element":        map[string]any{"type": "string"},
				"type":           map[string]any{"type": "string", "description": "Artifact type for screenshot or act sub-type."},
				"level":          map[string]any{"type": "string", "description": "Console level filter."},
				"clear":          map[string]any{"type": "boolean"},
				"filter":         map[string]any{"type": "string"},
				"screenshots":    map[string]any{"type": "boolean"},
				"snapshots":      map[string]any{"type": "boolean"},
				"sources":        map[string]any{"type": "boolean"},
				"path":           map[string]any{"type": "string"},
				"paths":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"inputRef":       map[string]any{"type": "string"},
				"timeoutMs":      map[string]any{"type": "integer"},
				"accept":         map[string]any{"type": "boolean"},
				"promptText":     map[string]any{"type": "string"},
				"doubleClick":    map[string]any{"type": "boolean"},
				"button":         map[string]any{"type": "string"},
				"modifiers":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"delayMs":        map[string]any{"type": "integer"},
				"kind":           map[string]any{"type": "string"},
				"text":           map[string]any{"type": "string"},
				"key":            map[string]any{"type": "string"},
				"fn":             map[string]any{"type": "string"},
				"timeMs":         map[string]any{"type": "integer"},
				"textGone":       map[string]any{"type": "string"},
				"loadState":      map[string]any{"type": "string"},
				"slowly":         map[string]any{"type": "boolean"},
				"submit":         map[string]any{"type": "boolean"},
				"values":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"width":          map[string]any{"type": "integer"},
				"height":         map[string]any{"type": "integer"},
				"request":        map[string]any{"type": "object"},
			},
			"required":             []string{"action"},
			"additionalProperties": true,
		},
	}
}

func (t *BrowserTool) Execute(ctx context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	if t.service == nil {
		return JSONResult(map[string]any{"ok": false, "status": "unavailable", "error": "browser service is not configured"})
	}
	action, err := ReadStringParam(args, "action", true)
	if err != nil {
		return core.ToolResult{}, err
	}
	action = strings.ToLower(strings.TrimSpace(action))
	req, err := readBrowserRequest(toolCtx, args)
	if err != nil {
		return core.ToolResult{}, err
	}
	switch action {
	case "install":
		return t.executeMap(ctx, func() (map[string]any, error) { return t.service.Install(ctx, req) })
	case "status":
		return t.executeMap(ctx, func() (map[string]any, error) { return t.service.Status(ctx, req) })
	case "start":
		return t.executeMap(ctx, func() (map[string]any, error) { return t.service.Start(ctx, req) })
	case "stop":
		return t.executeMap(ctx, func() (map[string]any, error) { return t.service.Stop(ctx, req) })
	case "profiles":
		return t.executeMap(ctx, func() (map[string]any, error) { return t.service.Profiles(ctx, req) })
	case "tabs":
		return t.executeBrowserWrapped(ctx, "tabs", func() (map[string]any, error) { return t.service.Tabs(ctx, req) })
	case "open":
		return t.executeMap(ctx, func() (map[string]any, error) {
			openReq := browserpkg.OpenRequest{
				Request:   req,
				URL:       readOptionalString(args, "url"),
				TargetURL: readOptionalString(args, "targetUrl"),
			}
			return t.service.Open(ctx, openReq)
		})
	case "focus":
		return t.executeMap(ctx, func() (map[string]any, error) {
			return t.service.Focus(ctx, browserpkg.TargetRequest{Request: req, TargetID: readOptionalString(args, "targetId")})
		})
	case "close":
		return t.executeMap(ctx, func() (map[string]any, error) {
			return t.service.Close(ctx, browserpkg.TargetRequest{Request: req, TargetID: readOptionalString(args, "targetId")})
		})
	case "navigate":
		return t.executeMap(ctx, func() (map[string]any, error) {
			return t.service.Navigate(ctx, browserpkg.NavigateRequest{
				TargetRequest: browserpkg.TargetRequest{Request: req, TargetID: readOptionalString(args, "targetId")},
				URL:           readOptionalString(args, "url"),
				TargetURL:     readOptionalString(args, "targetUrl"),
			})
		})
	case "snapshot":
		limit, err := ReadOptionalIntParam(args, "limit")
		if err != nil {
			return core.ToolResult{}, err
		}
		maxChars, err := ReadOptionalIntParam(args, "maxChars")
		if err != nil {
			return core.ToolResult{}, err
		}
		depth, err := ReadOptionalIntParam(args, "depth")
		if err != nil {
			return core.ToolResult{}, err
		}
		interactive, err := ReadBoolParam(args, "interactive")
		if err != nil {
			return core.ToolResult{}, err
		}
		compact, err := ReadBoolParam(args, "compact")
		if err != nil {
			return core.ToolResult{}, err
		}
		labels, err := ReadBoolParam(args, "labels")
		if err != nil {
			return core.ToolResult{}, err
		}
		return t.executeBrowserSnapshot(ctx, func() (map[string]any, error) {
			return t.service.Snapshot(ctx, browserpkg.SnapshotRequest{
				TargetRequest: browserpkg.TargetRequest{Request: req, TargetID: readOptionalString(args, "targetId")},
				Format:        readOptionalString(args, "snapshotFormat"),
				Refs:          readOptionalString(args, "refs"),
				Selector:      readOptionalString(args, "selector"),
				Frame:         readOptionalString(args, "frame"),
				Mode:          readOptionalString(args, "mode"),
				Limit:         limit,
				MaxChars:      maxChars,
				Depth:         depth,
				Interactive:   interactive,
				Compact:       compact,
				Labels:        labels,
			})
		})
	case "screenshot":
		fullPage, err := ReadBoolParam(args, "fullPage")
		if err != nil {
			return core.ToolResult{}, err
		}
		return t.executeBrowserImage(ctx, "screenshot", func() (map[string]any, error) {
			return t.service.Screenshot(ctx, browserpkg.ScreenshotRequest{
				TargetRequest: browserpkg.TargetRequest{Request: req, TargetID: readOptionalString(args, "targetId")},
				Type:          readOptionalString(args, "type"),
				FullPage:      fullPage,
			})
		})
	case "pdf":
		return t.executeMap(ctx, func() (map[string]any, error) {
			return t.service.PDF(ctx, browserpkg.TargetRequest{Request: req, TargetID: readOptionalString(args, "targetId")})
		})
	case "console":
		limit, err := ReadOptionalIntParam(args, "limit")
		if err != nil {
			return core.ToolResult{}, err
		}
		return t.executeBrowserWrapped(ctx, "console", func() (map[string]any, error) {
			return t.service.Console(ctx, browserpkg.ConsoleRequest{
				TargetRequest: browserpkg.TargetRequest{Request: req, TargetID: readOptionalString(args, "targetId")},
				Level:         readOptionalString(args, "level"),
				Limit:         limit,
			})
		})
	case "errors":
		limit, err := ReadOptionalIntParam(args, "limit")
		if err != nil {
			return core.ToolResult{}, err
		}
		clearValue, err := ReadBoolParam(args, "clear")
		if err != nil {
			return core.ToolResult{}, err
		}
		return t.executeBrowserWrapped(ctx, "errors", func() (map[string]any, error) {
			return t.service.Errors(ctx, browserpkg.DebugRequest{
				TargetRequest: browserpkg.TargetRequest{Request: req, TargetID: readOptionalString(args, "targetId")},
				Clear:         clearValue,
				Limit:         limit,
			})
		})
	case "requests":
		limit, err := ReadOptionalIntParam(args, "limit")
		if err != nil {
			return core.ToolResult{}, err
		}
		clearValue, err := ReadBoolParam(args, "clear")
		if err != nil {
			return core.ToolResult{}, err
		}
		return t.executeBrowserWrapped(ctx, "requests", func() (map[string]any, error) {
			return t.service.Requests(ctx, browserpkg.RequestsRequest{
				DebugRequest: browserpkg.DebugRequest{
					TargetRequest: browserpkg.TargetRequest{Request: req, TargetID: readOptionalString(args, "targetId")},
					Clear:         clearValue,
					Limit:         limit,
				},
				Filter: readOptionalString(args, "filter"),
			})
		})
	case "trace.start":
		screenshotsValue, err := ReadBoolParam(args, "screenshots")
		if err != nil {
			return core.ToolResult{}, err
		}
		snapshotsValue, err := ReadBoolParam(args, "snapshots")
		if err != nil {
			return core.ToolResult{}, err
		}
		sourcesValue, err := ReadBoolParam(args, "sources")
		if err != nil {
			return core.ToolResult{}, err
		}
		return t.executeMap(ctx, func() (map[string]any, error) {
			return t.service.TraceStart(ctx, browserpkg.TraceStartRequest{
				TargetRequest: browserpkg.TargetRequest{Request: req, TargetID: readOptionalString(args, "targetId")},
				Screenshots:   screenshotsValue,
				Snapshots:     snapshotsValue,
				Sources:       sourcesValue,
			})
		})
	case "trace.stop":
		return t.executeMap(ctx, func() (map[string]any, error) {
			return t.service.TraceStop(ctx, browserpkg.TraceStopRequest{
				TargetRequest: browserpkg.TargetRequest{Request: req, TargetID: readOptionalString(args, "targetId")},
				Path:          readOptionalString(args, "path"),
			})
		})
	case "act":
		return t.executeMap(ctx, func() (map[string]any, error) {
			actReq, actErr := readBrowserActRequest(req, args)
			if actErr != nil {
				return nil, actErr
			}
			return t.service.Act(ctx, actReq)
		})
	case "upload":
		return t.executeMap(ctx, func() (map[string]any, error) {
			paths, pathErr := resolveUploadPaths(toolCtx, args)
			if pathErr != nil {
				return nil, pathErr
			}
			return t.service.Upload(ctx, browserpkg.UploadRequest{
				TargetRequest: browserpkg.TargetRequest{Request: req, TargetID: readOptionalString(args, "targetId")},
				Ref:           readOptionalString(args, "ref"),
				InputRef:      readOptionalString(args, "inputRef"),
				Element:       readOptionalString(args, "element"),
				Selector:      readOptionalString(args, "selector"),
				Paths:         paths,
			})
		})
	case "dialog":
		accept, acceptErr := ReadBoolParam(args, "accept")
		if acceptErr != nil {
			return core.ToolResult{}, acceptErr
		}
		return t.executeMap(ctx, func() (map[string]any, error) {
			return t.service.Dialog(ctx, browserpkg.DialogRequest{
				TargetRequest: browserpkg.TargetRequest{Request: req, TargetID: readOptionalString(args, "targetId")},
				Accept:        accept,
				PromptText:    readOptionalString(args, "promptText"),
			})
		})
	case "download":
		return t.executeMap(ctx, func() (map[string]any, error) {
			return t.service.Download(ctx, browserpkg.DownloadRequest{
				TargetRequest: browserpkg.TargetRequest{Request: req, TargetID: readOptionalString(args, "targetId")},
				Ref:           readOptionalString(args, "ref"),
				Path:          readOptionalString(args, "path"),
			})
		})
	case "wait.download":
		return t.executeMap(ctx, func() (map[string]any, error) {
			return t.service.WaitDownload(ctx, browserpkg.WaitDownloadRequest{
				TargetRequest: browserpkg.TargetRequest{Request: req, TargetID: readOptionalString(args, "targetId")},
				Path:          readOptionalString(args, "path"),
			})
		})
	default:
		return JSONResult(map[string]any{"ok": false, "status": "error", "error": fmt.Sprintf("unsupported action %q", action)})
	}
}

func (t *BrowserTool) executeMap(ctx context.Context, fn func() (map[string]any, error)) (core.ToolResult, error) {
	result, err := fn()
	if err != nil {
		if ctx.Err() != nil {
			return core.ToolResult{}, ctx.Err()
		}
		return core.ToolResult{}, err
	}
	return JSONResult(result)
}

func (t *BrowserTool) executeBrowserWrapped(ctx context.Context, kind string, fn func() (map[string]any, error)) (core.ToolResult, error) {
	result, err := fn()
	if err != nil {
		if ctx.Err() != nil {
			return core.ToolResult{}, ctx.Err()
		}
		return core.ToolResult{}, err
	}
	return BrowserWrappedJSONResult(kind, result)
}

func (t *BrowserTool) executeBrowserImage(ctx context.Context, kind string, fn func() (map[string]any, error)) (core.ToolResult, error) {
	result, err := fn()
	if err != nil {
		if ctx.Err() != nil {
			return core.ToolResult{}, ctx.Err()
		}
		return core.ToolResult{}, err
	}
	path, _ := result["path"].(string)
	return BrowserImageResult(kind, path, result)
}

func (t *BrowserTool) executeBrowserSnapshot(ctx context.Context, fn func() (map[string]any, error)) (core.ToolResult, error) {
	result, err := fn()
	if err != nil {
		if ctx.Err() != nil {
			return core.ToolResult{}, ctx.Err()
		}
		return core.ToolResult{}, err
	}
	imagePath, _ := result["imagePath"].(string)
	if strings.TrimSpace(imagePath) != "" {
		return BrowserImageResult("snapshot", imagePath, result)
	}
	return BrowserWrappedJSONResult("snapshot", result)
}

func readBrowserRequest(toolCtx ToolContext, args map[string]any) (browserpkg.Request, error) {
	timeout, err := ReadOptionalPositiveDurationParam(args, "timeoutMs", time.Millisecond)
	if err != nil {
		return browserpkg.Request{}, err
	}
	var headless *bool
	if raw, ok := args["headless"]; ok && raw != nil {
		switch v := raw.(type) {
		case bool:
			headless = &v
		default:
			// ignore non-bool values
		}
	}
	return browserpkg.Request{
		SessionKey: strings.TrimSpace(toolCtx.Run.Session.SessionKey),
		Target:     readOptionalString(args, "target"),
		Profile:    readOptionalString(args, "profile"),
		Node:       readOptionalString(args, "node"),
		TimeoutMs:  int(timeout / time.Millisecond),
		Headless:   headless,
	}, nil
}

func readOptionalString(args map[string]any, key string) string {
	value, _ := ReadStringParam(args, key, false)
	return strings.TrimSpace(value)
}

func readBrowserActRequest(req browserpkg.Request, args map[string]any) (browserpkg.ActRequest, error) {
	requestMap, err := readOptionalObjectParam(args, "request")
	if err != nil {
		return browserpkg.ActRequest{}, err
	}
	timeout, err := ReadOptionalPositiveDurationParam(args, "timeoutMs", time.Millisecond)
	if err != nil {
		return browserpkg.ActRequest{}, err
	}
	timeMs, err := readOptionalIntFromSources(args, requestMap, "timeMs")
	if err != nil {
		return browserpkg.ActRequest{}, err
	}
	delayMs, err := readOptionalIntFromSources(args, requestMap, "delayMs")
	if err != nil {
		return browserpkg.ActRequest{}, err
	}
	slowly, err := readOptionalBoolFromSources(args, requestMap, "slowly")
	if err != nil {
		return browserpkg.ActRequest{}, err
	}
	submit, err := readOptionalBoolFromSources(args, requestMap, "submit")
	if err != nil {
		return browserpkg.ActRequest{}, err
	}
	doubleClick, err := readOptionalBoolFromSources(args, requestMap, "doubleClick")
	if err != nil {
		return browserpkg.ActRequest{}, err
	}
	width, err := readOptionalIntFromSources(args, requestMap, "width")
	if err != nil {
		return browserpkg.ActRequest{}, err
	}
	height, err := readOptionalIntFromSources(args, requestMap, "height")
	if err != nil {
		return browserpkg.ActRequest{}, err
	}
	values, err := readOptionalStringSliceFromSources(args, requestMap, "values")
	if err != nil {
		return browserpkg.ActRequest{}, err
	}
	modifiers, err := readOptionalStringSliceFromSources(args, requestMap, "modifiers")
	if err != nil {
		return browserpkg.ActRequest{}, err
	}
	actReq := browserpkg.ActRequest{
		TargetRequest: browserpkg.TargetRequest{
			Request:  req,
			TargetID: firstNonEmptyValue(requestMap, args, "targetId"),
		},
		Kind:        firstNonEmptyValue(requestMap, args, "kind"),
		Ref:         firstNonEmptyValue(requestMap, args, "ref"),
		StartRef:    firstNonEmptyValue(requestMap, args, "startRef"),
		EndRef:      firstNonEmptyValue(requestMap, args, "endRef"),
		Element:     firstNonEmptyValue(requestMap, args, "element"),
		Selector:    firstNonEmptyValue(requestMap, args, "selector"),
		Text:        firstNonEmptyValue(requestMap, args, "text"),
		Key:         firstNonEmptyValue(requestMap, args, "key"),
		Fn:          firstNonEmptyValue(requestMap, args, "fn"),
		TimeoutMs:   int(timeout / time.Millisecond),
		TimeMs:      timeMs,
		LoadState:   firstNonEmptyValue(requestMap, args, "loadState"),
		URL:         firstNonEmptyValue(requestMap, args, "url"),
		TextGone:    firstNonEmptyValue(requestMap, args, "textGone"),
		Slowly:      slowly,
		Submit:      submit,
		Values:      values,
		Width:       width,
		Height:      height,
		DoubleClick: doubleClick,
		Button:      firstNonEmptyValue(requestMap, args, "button"),
		Modifiers:   modifiers,
		DelayMs:     delayMs,
	}
	if actReq.Kind == "" {
		return browserpkg.ActRequest{}, ToolInputError{Message: `browser act requires "kind" or request.kind`}
	}
	return actReq, nil
}

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

func readOptionalObjectParam(args map[string]any, key string) (map[string]any, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil, nil
	}
	typed, ok := raw.(map[string]any)
	if !ok {
		return nil, ToolInputError{Message: fmt.Sprintf("parameter %q must be an object", key)}
	}
	return typed, nil
}

func firstNonEmptyValue(requestMap map[string]any, args map[string]any, key string) string {
	if requestMap != nil {
		if raw, ok := requestMap[key]; ok {
			if text, ok := raw.(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	return readOptionalString(args, key)
}

func readOptionalBoolFromSources(args map[string]any, requestMap map[string]any, key string) (bool, error) {
	if requestMap != nil {
		if raw, ok := requestMap[key]; ok && raw != nil {
			value, ok := raw.(bool)
			if !ok {
				return false, ToolInputError{Message: fmt.Sprintf("parameter %q must be a boolean", key)}
			}
			return value, nil
		}
	}
	return ReadBoolParam(args, key)
}

func readOptionalIntFromSources(args map[string]any, requestMap map[string]any, key string) (int, error) {
	if requestMap != nil {
		if raw, ok := requestMap[key]; ok && raw != nil {
			switch value := raw.(type) {
			case float64:
				return int(value), nil
			case int:
				return value, nil
			case int64:
				return int(value), nil
			default:
				return 0, ToolInputError{Message: fmt.Sprintf("parameter %q must be an integer", key)}
			}
		}
	}
	return ReadOptionalIntParam(args, key)
}

func readOptionalStringSliceFromSources(args map[string]any, requestMap map[string]any, key string) ([]string, error) {
	if requestMap != nil {
		if raw, ok := requestMap[key]; ok && raw != nil {
			typed, ok := raw.([]any)
			if !ok {
				if typedStrings, ok := raw.([]string); ok {
					return typedStrings, nil
				}
				return nil, ToolInputError{Message: fmt.Sprintf("parameter %q must be an array of strings", key)}
			}
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
		}
	}
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil, nil
	}
	typed, ok := raw.([]any)
	if !ok {
		if typedStrings, ok := raw.([]string); ok {
			return typedStrings, nil
		}
		return nil, ToolInputError{Message: fmt.Sprintf("parameter %q must be an array of strings", key)}
	}
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
}
