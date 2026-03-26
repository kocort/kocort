package browser

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	playwright "github.com/playwright-community/playwright-go"
)

func (m *Manager) Errors(ctx context.Context, req DebugRequest) (map[string]any, error) {
	profileName, unavailable := m.resolveProfile(req.Request)
	if unavailable != nil {
		unavailable["action"] = "errors"
		return unavailable, nil
	}
	page, _, targetID, err := m.resolvePage(ctx, profileName, req.TargetRequest)
	if err != nil {
		return map[string]any{"ok": false, "action": "errors", "profile": profileName, "error": err.Error()}, nil
	}
	state := ensureObservedPageState(page)
	state.mu.Lock()
	errorsList := cloneRecords(state.Errors)
	if req.Clear {
		state.Errors = nil
	}
	state.mu.Unlock()
	if req.Limit > 0 && len(errorsList) > req.Limit {
		errorsList = errorsList[len(errorsList)-req.Limit:]
	}
	return map[string]any{
		"ok":       true,
		"action":   "errors",
		"profile":  profileName,
		"targetId": targetID,
		"errors":   errorsList,
	}, nil
}

func (m *Manager) Requests(ctx context.Context, req RequestsRequest) (map[string]any, error) {
	profileName, unavailable := m.resolveProfile(req.Request)
	if unavailable != nil {
		unavailable["action"] = "requests"
		return unavailable, nil
	}
	page, _, targetID, err := m.resolvePage(ctx, profileName, req.TargetRequest)
	if err != nil {
		return map[string]any{"ok": false, "action": "requests", "profile": profileName, "error": err.Error()}, nil
	}
	state := ensureObservedPageState(page)
	state.mu.Lock()
	items := filterRequestRecords(state.Requests, req.Filter)
	if req.Clear {
		state.Requests = nil
		state.RequestIDs = sync.Map{}
	}
	state.mu.Unlock()
	if req.Limit > 0 && len(items) > req.Limit {
		items = items[len(items)-req.Limit:]
	}
	return map[string]any{
		"ok":       true,
		"action":   "requests",
		"profile":  profileName,
		"targetId": targetID,
		"requests": items,
	}, nil
}

func (m *Manager) TraceStart(ctx context.Context, req TraceStartRequest) (map[string]any, error) {
	profileName, unavailable := m.resolveProfile(req.Request)
	if unavailable != nil {
		unavailable["action"] = "trace.start"
		return unavailable, nil
	}
	page, state, targetID, err := m.resolvePage(ctx, profileName, req.TargetRequest)
	if err != nil {
		return map[string]any{"ok": false, "action": "trace.start", "profile": profileName, "error": err.Error()}, nil
	}
	_ = page
	ctxState := ensureObservedContextState(state.context)
	ctxState.mu.Lock()
	defer ctxState.mu.Unlock()
	if ctxState.TraceActive {
		return map[string]any{"ok": false, "action": "trace.start", "profile": profileName, "targetId": targetID, "error": "Trace already running. Stop the current trace before starting a new one."}, nil
	}
	if err := state.context.Tracing().Start(playwright.TracingStartOptions{
		Screenshots: playwright.Bool(req.Screenshots),
		Snapshots:   playwright.Bool(req.Snapshots),
		Sources:     playwright.Bool(req.Sources),
	}); err != nil {
		return map[string]any{"ok": false, "action": "trace.start", "profile": profileName, "targetId": targetID, "error": err.Error()}, nil
	}
	ctxState.TraceActive = true
	return map[string]any{
		"ok":          true,
		"action":      "trace.start",
		"profile":     profileName,
		"targetId":    targetID,
		"screenshots": req.Screenshots,
		"snapshots":   req.Snapshots,
		"sources":     req.Sources,
	}, nil
}

func (m *Manager) TraceStop(ctx context.Context, req TraceStopRequest) (map[string]any, error) {
	profileName, unavailable := m.resolveProfile(req.Request)
	if unavailable != nil {
		unavailable["action"] = "trace.stop"
		return unavailable, nil
	}
	page, state, targetID, err := m.resolvePage(ctx, profileName, req.TargetRequest)
	if err != nil {
		return map[string]any{"ok": false, "action": "trace.stop", "profile": profileName, "error": err.Error()}, nil
	}
	_ = page
	ctxState := ensureObservedContextState(state.context)
	ctxState.mu.Lock()
	defer ctxState.mu.Unlock()
	if !ctxState.TraceActive {
		return map[string]any{"ok": false, "action": "trace.stop", "profile": profileName, "targetId": targetID, "error": "No active trace. Start a trace before stopping it."}, nil
	}
	pathValue, err := m.resolveOutputPath(m.traceDir(), req.Path, fmt.Sprintf("browser-trace-%s-%d.zip", sanitizeFileSegment(targetID), time.Now().UTC().UnixNano()))
	if err != nil {
		return map[string]any{"ok": false, "action": "trace.stop", "profile": profileName, "targetId": targetID, "error": err.Error()}, nil
	}
	if err := state.context.Tracing().Stop(pathValue); err != nil {
		return map[string]any{"ok": false, "action": "trace.stop", "profile": profileName, "targetId": targetID, "error": err.Error()}, nil
	}
	ctxState.TraceActive = false
	return map[string]any{
		"ok":       true,
		"action":   "trace.stop",
		"profile":  profileName,
		"targetId": targetID,
		"path":     pathValue,
	}, nil
}

func (m *Manager) Download(ctx context.Context, req DownloadRequest) (map[string]any, error) {
	profileName, unavailable := m.resolveProfile(req.Request)
	if unavailable != nil {
		unavailable["action"] = "download"
		return unavailable, nil
	}
	page, _, targetID, err := m.resolvePage(ctx, profileName, req.TargetRequest)
	if err != nil {
		return map[string]any{"ok": false, "action": "download", "profile": profileName, "error": err.Error()}, nil
	}
	if strings.TrimSpace(req.Ref) == "" {
		return map[string]any{"ok": false, "action": "download", "profile": profileName, "targetId": targetID, "error": "ref is required"}, nil
	}
	if strings.TrimSpace(req.Path) == "" {
		return map[string]any{"ok": false, "action": "download", "profile": profileName, "targetId": targetID, "error": "path is required"}, nil
	}
	locator, _, err := m.resolveRefLocator(profileName, targetID, page, req.Ref)
	if err != nil {
		return map[string]any{"ok": false, "action": "download", "profile": profileName, "targetId": targetID, "error": err.Error()}, nil
	}
	pathValue, err := m.resolveOutputPath(m.downloadsDir(), req.Path, filepath.Base(strings.TrimSpace(req.Path)))
	if err != nil {
		return map[string]any{"ok": false, "action": "download", "profile": profileName, "targetId": targetID, "error": err.Error()}, nil
	}
	download, err := page.ExpectDownload(func() error {
		return locator.Click(playwright.LocatorClickOptions{Timeout: timeoutFloat(req.TimeoutMs)})
	}, playwright.PageExpectDownloadOptions{Timeout: timeoutFloat(req.TimeoutMs)})
	if err != nil {
		return map[string]any{"ok": false, "action": "download", "profile": profileName, "targetId": targetID, "error": err.Error()}, nil
	}
	if err := download.SaveAs(pathValue); err != nil {
		return map[string]any{"ok": false, "action": "download", "profile": profileName, "targetId": targetID, "error": err.Error()}, nil
	}
	return map[string]any{
		"ok":       true,
		"action":   "download",
		"profile":  profileName,
		"targetId": targetID,
		"download": map[string]any{
			"path":              pathValue,
			"url":               download.URL(),
			"suggestedFilename": download.SuggestedFilename(),
		},
	}, nil
}

func (m *Manager) WaitDownload(ctx context.Context, req WaitDownloadRequest) (map[string]any, error) {
	profileName, unavailable := m.resolveProfile(req.Request)
	if unavailable != nil {
		unavailable["action"] = "wait.download"
		return unavailable, nil
	}
	page, _, targetID, err := m.resolvePage(ctx, profileName, req.TargetRequest)
	if err != nil {
		return map[string]any{"ok": false, "action": "wait.download", "profile": profileName, "error": err.Error()}, nil
	}
	state := ensureObservedPageState(page)
	timeoutMs := req.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 120000
	}
	state.mu.Lock()
	state.DownloadArmID++
	armID := state.DownloadArmID
	waiter := make(chan playwright.Download, 1)
	state.DownloadWait = waiter
	state.mu.Unlock()
	defer func() {
		state.mu.Lock()
		if state.DownloadArmID == armID {
			state.DownloadWait = nil
		}
		state.mu.Unlock()
	}()
	timer := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
	defer timer.Stop()
	var download playwright.Download
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return map[string]any{"ok": false, "action": "wait.download", "profile": profileName, "targetId": targetID, "error": "Timeout waiting for download"}, nil
	case download = <-waiter:
	}
	pathValue, err := m.resolveOutputPath(m.downloadsDir(), req.Path, fmt.Sprintf("%d-%s", time.Now().UTC().UnixNano(), sanitizeFileSegment(download.SuggestedFilename())))
	if err != nil {
		return map[string]any{"ok": false, "action": "wait.download", "profile": profileName, "targetId": targetID, "error": err.Error()}, nil
	}
	if err := download.SaveAs(pathValue); err != nil {
		return map[string]any{"ok": false, "action": "wait.download", "profile": profileName, "targetId": targetID, "error": err.Error()}, nil
	}
	result := map[string]any{
		"ok":       true,
		"action":   "wait.download",
		"profile":  profileName,
		"targetId": targetID,
		"download": map[string]any{
			"url":               download.URL(),
			"suggestedFilename": download.SuggestedFilename(),
		},
	}
	result["download"].(map[string]any)["path"] = pathValue
	return result, nil
}
