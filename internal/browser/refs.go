package browser

import (
	"errors"
	"fmt"
	"strings"

	playwright "github.com/playwright-community/playwright-go"
)

func (m *Manager) resolveLocator(profileName, targetID string, page playwright.Page, ref, element, selector string) (playwright.Locator, string, error) {
	resolvedSelector := strings.TrimSpace(selector)
	if resolvedSelector == "" {
		resolvedSelector = strings.TrimSpace(element)
	}
	if strings.TrimSpace(ref) != "" {
		locator, locatorDesc, err := m.resolveRefLocator(profileName, targetID, page, ref)
		if err == nil {
			return locator, locatorDesc, nil
		}
		if resolvedSelector == "" {
			return nil, "", err
		}
	}
	if resolvedSelector == "" {
		return nil, "", errors.New("ref, element, or selector is required")
	}
	return page.Locator(resolvedSelector), resolvedSelector, nil
}

func (m *Manager) resolveRefLocator(profileName, targetID string, page playwright.Page, ref string) (playwright.Locator, string, error) {
	normalizedRef := normalizeBrowserRef(ref)
	if normalizedRef == "" {
		return nil, "", errors.New("ref is required")
	}
	m.mu.Lock()
	snapshot := m.snapshots[snapshotKey(profileName, targetID)]
	m.mu.Unlock()
	for _, node := range snapshot.Nodes {
		if !strings.EqualFold(strings.TrimSpace(node.Ref), normalizedRef) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(snapshot.RefsMode), "aria") {
			locator := m.pageScopeLocator(page, strings.TrimSpace(snapshot.Frame), "aria-ref="+normalizedRef)
			return locator, "aria-ref=" + normalizedRef, nil
		}
		if strings.TrimSpace(node.Role) != "" {
			locator, desc := m.roleScopeLocator(page, strings.TrimSpace(snapshot.Frame), node)
			return locator, desc, nil
		}
		if strings.TrimSpace(node.Selector) != "" {
			return m.pageScopeLocator(page, strings.TrimSpace(snapshot.Frame), node.Selector), node.Selector, nil
		}
		break
	}
	if strings.HasPrefix(normalizedRef, "e") {
		return page.Locator("aria-ref=" + normalizedRef), "aria-ref=" + normalizedRef, nil
	}
	return nil, "", fmt.Errorf("snapshot ref %q not found for target %s", normalizedRef, targetID)
}

func (m *Manager) ensureDialogHook(profileName, targetID string, page playwright.Page) {
	key := snapshotKey(profileName, targetID)
	m.mu.Lock()
	if m.dialogWired[key] {
		m.mu.Unlock()
		return
	}
	m.dialogWired[key] = true
	m.mu.Unlock()
	page.OnDialog(func(dialog playwright.Dialog) {
		m.mu.Lock()
		arm, ok := m.dialogArms[key]
		if ok {
			delete(m.dialogArms, key)
		}
		m.mu.Unlock()
		if !ok {
			_ = dialog.Dismiss()
			return
		}
		if arm.Accept {
			if strings.TrimSpace(arm.PromptText) != "" {
				_ = dialog.Accept(arm.PromptText)
				return
			}
			_ = dialog.Accept()
			return
		}
		_ = dialog.Dismiss()
	})
}

func (m *Manager) ensureFileChooserHook(profileName, targetID string, page playwright.Page) {
	key := snapshotKey(profileName, targetID)
	m.mu.Lock()
	if m.fileChooserWired[key] {
		m.mu.Unlock()
		return
	}
	m.fileChooserWired[key] = true
	m.mu.Unlock()
	page.OnFileChooser(func(fc playwright.FileChooser) {
		m.mu.Lock()
		paths, ok := m.fileChooserArms[key]
		if ok {
			delete(m.fileChooserArms, key)
		}
		m.mu.Unlock()
		if !ok || len(paths) == 0 {
			return
		}
		_ = fc.SetFiles(paths)
	})
}

func normalizeBrowserRef(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "@") {
		return strings.TrimSpace(trimmed[1:])
	}
	if strings.HasPrefix(trimmed, "ref=") {
		return strings.TrimSpace(trimmed[4:])
	}
	return trimmed
}

func (m *Manager) pageScopeLocator(page playwright.Page, frameSelector, selector string) playwright.Locator {
	if strings.TrimSpace(frameSelector) != "" {
		return page.FrameLocator(strings.TrimSpace(frameSelector)).Locator(selector)
	}
	return page.Locator(selector)
}

func (m *Manager) roleScopeLocator(page playwright.Page, frameSelector string, node snapshotNode) (playwright.Locator, string) {
	role := playwright.AriaRole(strings.TrimSpace(node.Role))
	name := strings.TrimSpace(node.Title)
	if strings.TrimSpace(frameSelector) != "" {
		options := playwright.FrameLocatorGetByRoleOptions{Exact: playwright.Bool(true)}
		if name != "" {
			options.Name = name
		}
		locator := page.FrameLocator(strings.TrimSpace(frameSelector)).GetByRole(role, options)
		if node.Nth > 0 {
			locator = locator.Nth(node.Nth)
		}
		return locator, fmt.Sprintf("role=%s name=%q nth=%d", node.Role, name, node.Nth)
	}
	options := playwright.PageGetByRoleOptions{Exact: playwright.Bool(true)}
	if name != "" {
		options.Name = name
	}
	locator := page.GetByRole(role, options)
	if node.Nth > 0 {
		locator = locator.Nth(node.Nth)
	}
	return locator, fmt.Sprintf("role=%s name=%q nth=%d", node.Role, name, node.Nth)
}
