package browser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	playwright "github.com/playwright-community/playwright-go"
)

func (m *Manager) Screenshot(ctx context.Context, req ScreenshotRequest) (map[string]any, error) {
	profileName, unavailable := m.resolveProfile(req.Request)
	if unavailable != nil {
		unavailable["action"] = "screenshot"
		return unavailable, nil
	}
	page, _, targetID, err := m.resolvePage(ctx, profileName, req.TargetRequest)
	if err != nil {
		return map[string]any{"ok": false, "action": "screenshot", "profile": profileName, "error": err.Error()}, nil
	}
	imageType := strings.ToLower(strings.TrimSpace(req.Type))
	if imageType == "" {
		imageType = "png"
	}
	if imageType != "png" && imageType != "jpeg" {
		return map[string]any{"ok": false, "action": "screenshot", "error": "type must be png or jpeg"}, nil
	}
	if err := os.MkdirAll(m.artifactDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(m.artifactDir, fmt.Sprintf("browser-%s-%d.%s", sanitizeFileSegment(targetID), time.Now().UTC().UnixNano(), imageType))
	opts := playwright.PageScreenshotOptions{
		FullPage: playwright.Bool(req.FullPage),
		Path:     playwright.String(path),
	}
	if imageType == "jpeg" {
		opts.Type = playwright.ScreenshotTypeJpeg
	} else {
		opts.Type = playwright.ScreenshotTypePng
	}
	if _, err := page.Screenshot(opts); err != nil {
		return map[string]any{"ok": false, "action": "screenshot", "profile": profileName, "targetId": targetID, "error": err.Error()}, nil
	}
	return map[string]any{
		"ok":       true,
		"action":   "screenshot",
		"profile":  profileName,
		"targetId": targetID,
		"path":     path,
		"type":     imageType,
	}, nil
}

func (m *Manager) PDF(ctx context.Context, req TargetRequest) (map[string]any, error) {
	profileName, unavailable := m.resolveProfile(req.Request)
	if unavailable != nil {
		unavailable["action"] = "pdf"
		return unavailable, nil
	}
	page, _, targetID, err := m.resolvePage(ctx, profileName, req)
	if err != nil {
		return map[string]any{"ok": false, "action": "pdf", "profile": profileName, "error": err.Error()}, nil
	}
	if err := os.MkdirAll(m.artifactDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(m.artifactDir, fmt.Sprintf("browser-%s-%d.pdf", sanitizeFileSegment(targetID), time.Now().UTC().UnixNano()))
	if _, err := page.PDF(playwright.PagePdfOptions{Path: playwright.String(path)}); err != nil {
		return map[string]any{"ok": false, "action": "pdf", "profile": profileName, "targetId": targetID, "error": err.Error()}, nil
	}
	return map[string]any{
		"ok":       true,
		"action":   "pdf",
		"profile":  profileName,
		"targetId": targetID,
		"path":     path,
	}, nil
}

func (m *Manager) Console(ctx context.Context, req ConsoleRequest) (map[string]any, error) {
	profileName, unavailable := m.resolveProfile(req.Request)
	if unavailable != nil {
		unavailable["action"] = "console"
		return unavailable, nil
	}
	page, _, targetID, err := m.resolvePage(ctx, profileName, req.TargetRequest)
	if err != nil {
		return map[string]any{"ok": false, "action": "console", "profile": profileName, "error": err.Error()}, nil
	}
	msgs, err := page.ConsoleMessages()
	if err != nil {
		return map[string]any{"ok": false, "action": "console", "profile": profileName, "targetId": targetID, "error": err.Error()}, nil
	}
	state := ensureObservedPageState(page)
	level := strings.ToLower(strings.TrimSpace(req.Level))
	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	items := make([]map[string]any, 0, limit)
	minPriority := -1
	if level != "" {
		minPriority = consolePriority(level)
	}
	appendMessage := func(entry map[string]any) bool {
		msgType, _ := entry["type"].(string)
		if minPriority >= 0 && consolePriority(msgType) < minPriority {
			return false
		}
		items = append(items, entry)
		return len(items) >= limit
	}
	if len(msgs) > 0 {
		for i := len(msgs) - 1; i >= 0; i-- {
			msg := msgs[i]
			entry := map[string]any{
				"type": msg.Type(),
				"text": msg.Text(),
			}
			if location := msg.Location(); location != nil {
				entry["location"] = map[string]any{
					"url":          location.URL,
					"lineNumber":   location.LineNumber,
					"columnNumber": location.ColumnNumber,
				}
			}
			if appendMessage(entry) {
				break
			}
		}
	} else {
		state.mu.Lock()
		cached := cloneRecords(state.Console)
		state.mu.Unlock()
		for i := len(cached) - 1; i >= 0; i-- {
			if appendMessage(cached[i]) {
				break
			}
		}
	}
	return map[string]any{
		"ok":       true,
		"action":   "console",
		"profile":  profileName,
		"targetId": targetID,
		"messages": items,
	}, nil
}
