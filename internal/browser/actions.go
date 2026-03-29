package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	playwright "github.com/playwright-community/playwright-go"
)

func (m *Manager) Snapshot(ctx context.Context, req SnapshotRequest) (map[string]any, error) {
	profileName, unavailable := m.resolveProfile(req.Request)
	if unavailable != nil {
		unavailable["action"] = "snapshot"
		return unavailable, nil
	}
	page, _, targetID, err := m.resolvePage(ctx, profileName, req.TargetRequest)
	if err != nil {
		return map[string]any{"ok": false, "action": "snapshot", "profile": profileName, "error": err.Error()}, nil
	}
	result, state, err := buildSnapshot(page, req)
	if err != nil {
		return map[string]any{"ok": false, "action": "snapshot", "profile": profileName, "targetId": targetID, "error": err.Error()}, nil
	}
	state.TargetID = targetID
	state.Profile = profileName
	if state.Frame == "" {
		state.Frame = strings.TrimSpace(req.Frame)
	}
	m.mu.Lock()
	m.snapshots[snapshotKey(profileName, targetID)] = state
	m.mu.Unlock()
	if req.Labels {
		if err := os.MkdirAll(m.artifactDir, 0o755); err != nil {
			return nil, err
		}
		imagePath := filepath.Join(m.artifactDir, fmt.Sprintf("browser-snapshot-%s-%d.png", sanitizeFileSegment(targetID), time.Now().UTC().UnixNano()))
		if applied, skipped, shotErr := m.captureSnapshotWithLabels(page, state, imagePath); shotErr == nil {
			result["labels"] = true
			result["labelsCount"] = applied
			result["labelsSkipped"] = skipped
			result["imagePath"] = imagePath
			result["imageType"] = "png"
		}
	}
	result["ok"] = true
	result["action"] = "snapshot"
	result["profile"] = profileName
	result["targetId"] = targetID
	result["url"] = page.URL()
	return result, nil
}

func (m *Manager) Act(ctx context.Context, req ActRequest) (map[string]any, error) {
	profileName, unavailable := m.resolveProfile(req.Request)
	if unavailable != nil {
		unavailable["action"] = "act"
		return unavailable, nil
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if kind == "" {
		return map[string]any{"ok": false, "action": "act", "profile": profileName, "error": "act kind is required"}, nil
	}
	page, _, targetID, err := m.resolvePage(ctx, profileName, req.TargetRequest)
	if err != nil {
		return map[string]any{"ok": false, "action": "act", "profile": profileName, "error": err.Error()}, nil
	}
	switch kind {
	case "click":
		locator, selector, err := m.resolveLocator(profileName, targetID, page, req.Ref, req.Element, req.Selector)
		if err != nil {
			return m.actError(profileName, targetID, kind, err), nil
		}
		if req.DoubleClick {
			// Use page.Dblclick instead of locator.Dblclick to work around
			// a Playwright Go v0.5700.x bug where LocatorDblclickOptions
			// contains a Steps field not present in FrameDblclickOptions,
			// causing assignStructFields to fail with "extra field Steps in src".
			if err := page.Dblclick(selector, playwright.PageDblclickOptions{
				Timeout:   timeoutFloat(req.TimeoutMs),
				Button:    parseMouseButton(req.Button),
				Delay:     timeoutFloat(req.DelayMs),
				Modifiers: parseKeyboardModifiers(req.Modifiers),
				Strict:    playwright.Bool(true),
			}); err != nil {
				return m.actError(profileName, targetID, kind, err), nil
			}
		} else {
			if err := locator.Click(playwright.LocatorClickOptions{
				Timeout:   timeoutFloat(req.TimeoutMs),
				Button:    parseMouseButton(req.Button),
				Delay:     timeoutFloat(req.DelayMs),
				Modifiers: parseKeyboardModifiers(req.Modifiers),
			}); err != nil {
				return m.actError(profileName, targetID, kind, err), nil
			}
		}
		return m.actResult(profileName, targetID, kind, map[string]any{"selector": selector}), nil
	case "type":
		locator, selector, err := m.resolveLocator(profileName, targetID, page, req.Ref, req.Element, req.Selector)
		if err != nil {
			return m.actError(profileName, targetID, kind, err), nil
		}
		if strings.TrimSpace(req.Text) == "" {
			return m.actError(profileName, targetID, kind, errors.New("text is required for act kind=type")), nil
		}
		if req.Slowly {
			if err := locator.PressSequentially(req.Text); err != nil {
				return m.actError(profileName, targetID, kind, err), nil
			}
		} else {
			if err := locator.Type(req.Text, playwright.LocatorTypeOptions{Timeout: timeoutFloat(req.TimeoutMs)}); err != nil {
				return m.actError(profileName, targetID, kind, err), nil
			}
		}
		if req.Submit {
			if err := locator.Press("Enter", playwright.LocatorPressOptions{Timeout: timeoutFloat(req.TimeoutMs)}); err != nil {
				return m.actError(profileName, targetID, kind, err), nil
			}
		}
		return m.actResult(profileName, targetID, kind, map[string]any{"selector": selector, "text": req.Text}), nil
	case "fill", "input":
		locator, selector, err := m.resolveLocator(profileName, targetID, page, req.Ref, req.Element, req.Selector)
		if err != nil {
			return m.actError(profileName, targetID, kind, err), nil
		}
		if strings.TrimSpace(req.Text) == "" {
			return m.actError(profileName, targetID, kind, errors.New("text is required for act kind=fill/input")), nil
		}
		if err := locator.Fill(req.Text, playwright.LocatorFillOptions{Timeout: timeoutFloat(req.TimeoutMs)}); err != nil {
			return m.actError(profileName, targetID, kind, err), nil
		}
		if req.Submit {
			if err := locator.Press("Enter", playwright.LocatorPressOptions{Timeout: timeoutFloat(req.TimeoutMs)}); err != nil {
				return m.actError(profileName, targetID, kind, err), nil
			}
		}
		return m.actResult(profileName, targetID, kind, map[string]any{"selector": selector, "text": req.Text}), nil
	case "press":
		locator, selector, err := m.resolveLocator(profileName, targetID, page, req.Ref, req.Element, req.Selector)
		if err != nil {
			return m.actError(profileName, targetID, kind, err), nil
		}
		if strings.TrimSpace(req.Key) == "" {
			return m.actError(profileName, targetID, kind, errors.New("key is required for act kind=press")), nil
		}
		if err := m.performPress(page, locator, req); err != nil {
			return m.actError(profileName, targetID, kind, err), nil
		}
		return m.actResult(profileName, targetID, kind, map[string]any{"selector": selector, "key": req.Key}), nil
	case "hover":
		locator, selector, err := m.resolveLocator(profileName, targetID, page, req.Ref, req.Element, req.Selector)
		if err != nil {
			return m.actError(profileName, targetID, kind, err), nil
		}
		if err := locator.Hover(playwright.LocatorHoverOptions{Timeout: timeoutFloat(req.TimeoutMs)}); err != nil {
			return m.actError(profileName, targetID, kind, err), nil
		}
		return m.actResult(profileName, targetID, kind, map[string]any{"selector": selector}), nil
	case "select":
		locator, selector, err := m.resolveLocator(profileName, targetID, page, req.Ref, req.Element, req.Selector)
		if err != nil {
			return m.actError(profileName, targetID, kind, err), nil
		}
		if len(req.Values) == 0 {
			return m.actError(profileName, targetID, kind, errors.New("values is required for act kind=select")), nil
		}
		values := append([]string(nil), req.Values...)
		selected, err := locator.SelectOption(playwright.SelectOptionValues{Values: &values}, playwright.LocatorSelectOptionOptions{Timeout: timeoutFloat(req.TimeoutMs)})
		if err != nil {
			return m.actError(profileName, targetID, kind, err), nil
		}
		return m.actResult(profileName, targetID, kind, map[string]any{"selector": selector, "values": selected}), nil
	case "resize":
		if req.Width <= 0 || req.Height <= 0 {
			return m.actError(profileName, targetID, kind, errors.New("width and height are required for act kind=resize")), nil
		}
		if err := page.SetViewportSize(req.Width, req.Height); err != nil {
			return m.actError(profileName, targetID, kind, err), nil
		}
		return m.actResult(profileName, targetID, kind, map[string]any{"width": req.Width, "height": req.Height}), nil
	case "drag":
		startRef := strings.TrimSpace(req.StartRef)
		if startRef == "" {
			startRef = strings.TrimSpace(req.Ref)
		}
		source, sourceSelector, err := m.resolveLocator(profileName, targetID, page, startRef, req.Element, req.Selector)
		if err != nil {
			return m.actError(profileName, targetID, kind, err), nil
		}
		target, targetSelector, err := m.resolveLocator(profileName, targetID, page, req.EndRef, "", "")
		if err != nil {
			return m.actError(profileName, targetID, kind, err), nil
		}
		if err := source.DragTo(target, playwright.LocatorDragToOptions{Timeout: timeoutFloat(req.TimeoutMs)}); err != nil {
			return m.actError(profileName, targetID, kind, err), nil
		}
		return m.actResult(profileName, targetID, kind, map[string]any{
			"sourceSelector": sourceSelector,
			"targetSelector": targetSelector,
		}), nil
	case "wait":
		if err := m.performWait(ctx, page, profileName, targetID, req); err != nil {
			return m.actError(profileName, targetID, kind, err), nil
		}
		return m.actResult(profileName, targetID, kind, nil), nil
	case "evaluate":
		script := strings.TrimSpace(req.Fn)
		if script == "" {
			return m.actError(profileName, targetID, kind, errors.New("fn is required for act kind=evaluate")), nil
		}
		value, err := page.Evaluate(script)
		if err != nil {
			return m.actError(profileName, targetID, kind, err), nil
		}
		return m.actResult(profileName, targetID, kind, map[string]any{"result": value}), nil
	case "close":
		return m.Close(ctx, req.TargetRequest)
	default:
		return map[string]any{
			"ok":       false,
			"action":   "act",
			"profile":  profileName,
			"targetId": targetID,
			"kind":     kind,
			"status":   "unavailable",
			"error":    fmt.Sprintf("act kind %q is not implemented yet", kind),
		}, nil
	}
}

func (m *Manager) Upload(ctx context.Context, req UploadRequest) (map[string]any, error) {
	profileName, unavailable := m.resolveProfile(req.Request)
	if unavailable != nil {
		unavailable["action"] = "upload"
		return unavailable, nil
	}
	if len(req.Paths) == 0 {
		return map[string]any{"ok": false, "action": "upload", "profile": profileName, "error": "paths is required"}, nil
	}
	page, _, targetID, err := m.resolvePage(ctx, profileName, req.TargetRequest)
	if err != nil {
		return map[string]any{"ok": false, "action": "upload", "profile": profileName, "error": err.Error()}, nil
	}
	ref := firstNonEmpty(req.InputRef, req.Ref)
	if strings.TrimSpace(ref) == "" && strings.TrimSpace(req.Element) == "" && strings.TrimSpace(req.Selector) == "" {
		m.ensureFileChooserHook(profileName, targetID, page)
		m.mu.Lock()
		m.fileChooserArms[snapshotKey(profileName, targetID)] = append([]string(nil), req.Paths...)
		m.mu.Unlock()
		return map[string]any{
			"ok":       true,
			"action":   "upload",
			"profile":  profileName,
			"targetId": targetID,
			"mode":     "armed",
			"paths":    req.Paths,
		}, nil
	}
	locator, selector, err := m.resolveLocator(profileName, targetID, page, ref, req.Element, req.Selector)
	if err != nil {
		return map[string]any{"ok": false, "action": "upload", "profile": profileName, "targetId": targetID, "error": err.Error()}, nil
	}
	if err := locator.SetInputFiles(req.Paths, playwright.LocatorSetInputFilesOptions{Timeout: timeoutFloat(req.TimeoutMs)}); err != nil {
		return map[string]any{"ok": false, "action": "upload", "profile": profileName, "targetId": targetID, "error": err.Error()}, nil
	}
	return map[string]any{
		"ok":       true,
		"action":   "upload",
		"profile":  profileName,
		"targetId": targetID,
		"selector": selector,
		"mode":     "direct",
		"paths":    req.Paths,
	}, nil
}

func (m *Manager) Dialog(ctx context.Context, req DialogRequest) (map[string]any, error) {
	profileName, unavailable := m.resolveProfile(req.Request)
	if unavailable != nil {
		unavailable["action"] = "dialog"
		return unavailable, nil
	}
	page, _, targetID, err := m.resolvePage(ctx, profileName, req.TargetRequest)
	if err != nil {
		return map[string]any{"ok": false, "action": "dialog", "profile": profileName, "error": err.Error()}, nil
	}
	m.ensureDialogHook(profileName, targetID, page)
	m.mu.Lock()
	m.dialogArms[snapshotKey(profileName, targetID)] = dialogArm{
		Accept:     req.Accept,
		PromptText: req.PromptText,
	}
	m.mu.Unlock()
	return map[string]any{
		"ok":         true,
		"action":     "dialog",
		"profile":    profileName,
		"targetId":   targetID,
		"accept":     req.Accept,
		"promptText": req.PromptText,
	}, nil
}

func (m *Manager) performWait(ctx context.Context, page playwright.Page, profileName, targetID string, req ActRequest) error {
	timeoutMs := req.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 30000
	}
	if req.TimeMs > 0 {
		timer := time.NewTimer(time.Duration(req.TimeMs) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}
	if strings.TrimSpace(req.Selector) != "" || strings.TrimSpace(req.Ref) != "" || strings.TrimSpace(req.Element) != "" {
		locator, _, err := m.resolveLocator(profileName, targetID, page, req.Ref, req.Element, req.Selector)
		if err != nil {
			return err
		}
		return locator.WaitFor(playwright.LocatorWaitForOptions{
			State:   playwright.WaitForSelectorStateVisible,
			Timeout: timeoutFloat(timeoutMs),
		})
	}
	if strings.TrimSpace(req.LoadState) != "" {
		return page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
			State:   parseLoadState(req.LoadState),
			Timeout: timeoutFloat(timeoutMs),
		})
	}
	if strings.TrimSpace(req.URL) != "" {
		deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
		for {
			if strings.Contains(page.URL(), strings.TrimSpace(req.URL)) {
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for url containing %q", req.URL)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	if strings.TrimSpace(req.TextGone) != "" {
		body := page.Locator("body")
		deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
		for {
			text, err := body.InnerText()
			if err == nil && !strings.Contains(text, req.TextGone) {
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for text to disappear: %q", req.TextGone)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	return errors.New("wait requires one of timeMs, selector/ref, loadState, url, or textGone")
}

func (m *Manager) actResult(profileName, targetID, kind string, extra map[string]any) map[string]any {
	result := map[string]any{
		"ok":       true,
		"action":   "act",
		"profile":  profileName,
		"targetId": targetID,
		"kind":     kind,
	}
	for key, value := range extra {
		result[key] = value
	}
	return result
}

func (m *Manager) actError(profileName, targetID, kind string, err error) map[string]any {
	return map[string]any{
		"ok":       false,
		"action":   "act",
		"profile":  profileName,
		"targetId": targetID,
		"kind":     kind,
		"error":    err.Error(),
	}
}

func (m *Manager) performPress(page playwright.Page, locator playwright.Locator, req ActRequest) error {
	if err := locator.Focus(); err != nil {
		return err
	}
	if len(req.Modifiers) == 0 {
		return locator.Press(req.Key, playwright.LocatorPressOptions{
			Timeout: timeoutFloat(req.TimeoutMs),
			Delay:   timeoutFloat(req.DelayMs),
		})
	}
	keys := normalizeModifierKeys(req.Modifiers)
	kb := page.Keyboard()
	for _, key := range keys {
		if err := kb.Down(key); err != nil {
			for i := len(keys) - 1; i >= 0; i-- {
				_ = kb.Up(keys[i])
			}
			return err
		}
	}
	pressErr := kb.Press(req.Key, playwright.KeyboardPressOptions{Delay: timeoutFloat(req.DelayMs)})
	for i := len(keys) - 1; i >= 0; i-- {
		_ = kb.Up(keys[i])
	}
	return pressErr
}

func normalizeModifierKeys(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "alt":
			out = append(out, "Alt")
		case "control", "ctrl":
			out = append(out, "Control")
		case "meta", "cmd", "command":
			out = append(out, "Meta")
		case "shift":
			out = append(out, "Shift")
		}
	}
	return out
}

func parseMouseButton(value string) *playwright.MouseButton {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "right":
		return playwright.MouseButtonRight
	case "middle":
		return playwright.MouseButtonMiddle
	default:
		return playwright.MouseButtonLeft
	}
}

func parseKeyboardModifiers(values []string) []playwright.KeyboardModifier {
	if len(values) == 0 {
		return nil
	}
	out := make([]playwright.KeyboardModifier, 0, len(values))
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "alt":
			if playwright.KeyboardModifierAlt != nil {
				out = append(out, *playwright.KeyboardModifierAlt)
			}
		case "control", "ctrl":
			if playwright.KeyboardModifierControl != nil {
				out = append(out, *playwright.KeyboardModifierControl)
			}
		case "meta", "cmd", "command":
			if playwright.KeyboardModifierMeta != nil {
				out = append(out, *playwright.KeyboardModifierMeta)
			}
		case "shift":
			if playwright.KeyboardModifierShift != nil {
				out = append(out, *playwright.KeyboardModifierShift)
			}
		}
	}
	return out
}

func parseLoadState(value string) *playwright.LoadState {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "domcontentloaded":
		return playwright.LoadStateDomcontentloaded
	case "networkidle":
		return playwright.LoadStateNetworkidle
	default:
		return playwright.LoadStateLoad
	}
}
