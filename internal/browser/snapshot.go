package browser

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	playwright "github.com/playwright-community/playwright-go"
)

type snapshotNode struct {
	Ref      string `json:"ref"`
	Selector string `json:"selector"`
	Tag      string `json:"tag,omitempty"`
	Role     string `json:"role,omitempty"`
	Aria     string `json:"aria,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Title    string `json:"title,omitempty"`
	Nth      int    `json:"nth,omitempty"`
	Depth    int    `json:"depth,omitempty"`
}

type snapshotState struct {
	Profile  string
	TargetID string
	Format   string
	RefsMode string
	Frame    string
	Nodes    []snapshotNode
	SavedAt  time.Time
}

func (m *Manager) captureSnapshotWithLabels(page playwright.Page, state snapshotState, imagePath string) (int, int, error) {
	labels := make([]map[string]any, 0, len(state.Nodes))
	targetID := strings.TrimSpace(state.TargetID)
	for _, node := range state.Nodes {
		if strings.TrimSpace(node.Ref) == "" {
			continue
		}
		locator, _, err := m.resolveRefLocator(state.Profile, targetID, page, node.Ref)
		if err != nil {
			continue
		}
		box, err := locator.BoundingBox()
		if err != nil || box == nil || box.Width <= 0 || box.Height <= 0 {
			continue
		}
		labels = append(labels, map[string]any{
			"ref": node.Ref,
			"x":   box.X,
			"y":   box.Y,
			"w":   box.Width,
			"h":   box.Height,
		})
	}
	if len(labels) == 0 {
		_, err := page.Screenshot(playwright.PageScreenshotOptions{
			Path: imagePathString(imagePath),
			Type: playwright.ScreenshotTypePng,
		})
		return 0, len(state.Nodes), err
	}
	raw, _ := page.Evaluate(`(labels) => {
		const cleanupKey = '__kocort_browser_labels__';
		const existing = window[cleanupKey];
		if (typeof existing === 'function') existing();
		const overlays = [];
		const viewport = {
			scrollX: window.scrollX || 0,
			scrollY: window.scrollY || 0,
			width: window.innerWidth || 0,
			height: window.innerHeight || 0,
		};
		let applied = 0;
		let skipped = 0;
		const clamp = (value, min, max) => Math.min(max, Math.max(min, value));
		for (const item of labels) {
			try {
				const x0 = Number(item.x || 0);
				const y0 = Number(item.y || 0);
				const w = Number(item.w || 0);
				const h = Number(item.h || 0);
				const x1 = x0 + w;
				const y1 = y0 + h;
				const vx0 = viewport.scrollX;
				const vy0 = viewport.scrollY;
				const vx1 = viewport.scrollX + viewport.width;
				const vy1 = viewport.scrollY + viewport.height;
				if (x1 < vx0 || x0 > vx1 || y1 < vy0 || y0 > vy1 || !w || !h) {
					skipped++;
					continue;
				}
				const left = x0 - viewport.scrollX;
				const top = y0 - viewport.scrollY;
				const badge = document.createElement('div');
				badge.textContent = item.ref;
				badge.style.position = 'absolute';
				badge.style.left = String(left) + 'px';
				badge.style.top = String(clamp(top - 18, 0, 20000)) + 'px';
				badge.style.zIndex = '2147483647';
				badge.style.background = '#ffb020';
				badge.style.color = '#1a1a1a';
				badge.style.font = '12px/14px monospace';
				badge.style.padding = '1px 4px';
				badge.style.borderRadius = '3px';
				badge.style.boxShadow = '0 1px 2px rgba(0,0,0,.35)';
				badge.style.pointerEvents = 'none';
				const outline = document.createElement('div');
				outline.style.position = 'absolute';
				outline.style.left = String(left) + 'px';
				outline.style.top = String(top) + 'px';
				outline.style.width = String(Math.max(1, w)) + 'px';
				outline.style.height = String(Math.max(1, h)) + 'px';
				outline.style.border = '2px solid #ffb020';
				outline.style.boxSizing = 'border-box';
				outline.style.pointerEvents = 'none';
				outline.style.zIndex = '2147483647';
				document.documentElement.appendChild(outline);
				document.documentElement.appendChild(badge);
				overlays.push(outline);
				overlays.push(badge);
				applied++;
			} catch {
				skipped++;
			}
		}
		window[cleanupKey] = () => {
			for (const el of overlays) el.remove();
			delete window[cleanupKey];
		};
		return { applied, skipped };
	}`, labels)
	_, err := page.Screenshot(playwright.PageScreenshotOptions{
		Path: imagePathString(imagePath),
		Type: playwright.ScreenshotTypePng,
	})
	_, _ = page.Evaluate(`() => {
		const cleanup = window.__kocort_browser_labels__;
		if (typeof cleanup === 'function') cleanup();
	}`)
	applied := 0
	skipped := len(state.Nodes)
	if stats, ok := raw.(map[string]any); ok {
		if value, ok := stats["applied"].(float64); ok {
			applied = int(value)
		}
		if value, ok := stats["skipped"].(float64); ok {
			skipped = int(value)
		}
	}
	return applied, skipped, err
}

func buildAISnapshot(page playwright.Page, req SnapshotRequest) (map[string]any, snapshotState, error) {
	format := strings.ToLower(strings.TrimSpace(req.Format))
	if format == "" {
		format = "ai"
	}
	if format != "ai" {
		return nil, snapshotState{}, fmt.Errorf("snapshot format %q is not implemented yet", format)
	}
	refs := strings.ToLower(strings.TrimSpace(req.Refs))
	if refs == "" {
		refs = "aria"
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 40
	}
	maxChars := req.MaxChars
	if maxChars <= 0 {
		maxChars = 12000
	}
	selector := strings.TrimSpace(req.Selector)
	if selector == "" {
		selector = "body"
	}
	raw, err := page.Evaluate(`([selector, limit]) => {
		const root = document.querySelector(selector) || document.body;
		if (!root) return { url: location.href, title: document.title, nodes: [] };
		const nodes = [];
		const interactiveSelector = [
			'a[href]','button','input','textarea','select','summary',
			'[role="button"]','[role="link"]','[role="textbox"]','[role="menuitem"]',
			'[tabindex]','[contenteditable="true"]'
		].join(',');
		const candidates = Array.from(root.querySelectorAll(interactiveSelector));
		const seen = new Set();
		for (const el of candidates) {
			if (!el || seen.has(el)) continue;
			seen.add(el);
			const text = (el.innerText || el.textContent || '').replace(/\s+/g, ' ').trim();
			const aria = (el.getAttribute('aria-label') || el.getAttribute('aria-labelledby') || '').trim();
			const tag = (el.tagName || '').toLowerCase();
			const role = (el.getAttribute('role') || '').trim();
			const type = (el.getAttribute('type') || '').trim();
			const title = (aria || text || el.getAttribute('title') || el.getAttribute('placeholder') || '').trim();
			if (!title && !tag) continue;
			let css = tag || 'node';
			if (el.id) css += '#' + CSS.escape(el.id);
			else if (el.getAttribute('name')) css += '[name="' + el.getAttribute('name').replace(/"/g, '\\"') + '"]';
			else if (el.classList && el.classList.length) css += '.' + Array.from(el.classList).slice(0, 2).map(v => CSS.escape(v)).join('.');
			nodes.push({ tag, role, aria, kind: type, title, selector: css, depth: 1 });
			if (nodes.length >= limit) break;
		}
		if (nodes.length === 0) {
			const text = (root.innerText || root.textContent || '').replace(/\s+/g, ' ').trim();
			if (text) nodes.push({ tag: 'body', title: text.slice(0, 500), selector: selector, depth: 0 });
		}
		return { url: location.href, title: document.title, nodes };
	}`, []any{selector, limit})
	if err != nil {
		return nil, snapshotState{}, err
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return nil, snapshotState{}, err
	}
	var doc struct {
		URL   string `json:"url"`
		Title string `json:"title"`
		Nodes []struct {
			Selector string `json:"selector"`
			Tag      string `json:"tag"`
			Role     string `json:"role"`
			Aria     string `json:"aria"`
			Kind     string `json:"kind"`
			Title    string `json:"title"`
			Depth    int    `json:"depth"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		return nil, snapshotState{}, err
	}
	lines := make([]string, 0, len(doc.Nodes))
	nodes := make([]snapshotNode, 0, len(doc.Nodes))
	refMap := map[string]map[string]any{}
	roleNameCounts := map[string]int{}
	truncated := false
	currentChars := 0
	for i, node := range doc.Nodes {
		key := strings.ToLower(strings.TrimSpace(node.Role)) + "\x00" + strings.TrimSpace(node.Title)
		nth := roleNameCounts[key]
		roleNameCounts[key] = nth + 1
		ref := stableSnapshotRef("ai", refs, node.Selector, node.Role, node.Title, i)
		entry := snapshotNode{
			Ref:      ref,
			Selector: strings.TrimSpace(node.Selector),
			Tag:      strings.TrimSpace(node.Tag),
			Role:     strings.TrimSpace(node.Role),
			Aria:     strings.TrimSpace(node.Aria),
			Kind:     strings.TrimSpace(node.Kind),
			Title:    strings.TrimSpace(node.Title),
			Nth:      nth,
			Depth:    node.Depth,
		}
		nodes = append(nodes, entry)
		refInfo := map[string]any{}
		if strings.TrimSpace(entry.Role) != "" {
			refInfo["role"] = entry.Role
		}
		if strings.TrimSpace(entry.Title) != "" {
			refInfo["name"] = entry.Title
		}
		if entry.Nth > 0 {
			refInfo["nth"] = entry.Nth
		}
		if len(refInfo) > 0 {
			refMap[entry.Ref] = refInfo
		}
		line := snapshotLine(entry)
		if line == "" {
			continue
		}
		if currentChars+len(line)+1 > maxChars {
			truncated = true
			break
		}
		lines = append(lines, line)
		currentChars += len(line) + 1
	}
	state := snapshotState{
		Format:   format,
		RefsMode: "role",
		Frame:    strings.TrimSpace(req.Frame),
		Nodes:    nodes,
		SavedAt:  time.Now().UTC(),
	}
	result := map[string]any{
		"format":        format,
		"snapshot":      strings.Join(lines, "\n"),
		"truncated":     truncated,
		"refs":          refMap,
		"refsMode":      refs,
		"labels":        false,
		"labelsCount":   0,
		"labelsSkipped": 0,
		"stats": map[string]any{
			"nodes":       len(nodes),
			"lines":       len(lines),
			"chars":       currentChars,
			"refs":        len(refMap),
			"interactive": len(nodes),
		},
		"title": doc.Title,
	}
	return result, state, nil
}

func buildSnapshot(page playwright.Page, req SnapshotRequest) (map[string]any, snapshotState, error) {
	format := strings.ToLower(strings.TrimSpace(req.Format))
	if format == "" || format == "ai" {
		return buildAISnapshot(page, req)
	}
	if format != "aria" {
		return nil, snapshotState{}, fmt.Errorf("snapshot format %q is not implemented yet", format)
	}
	selector := strings.TrimSpace(req.Selector)
	if selector == "" {
		selector = "body"
	}
	locator := page.Locator(selector)
	yaml, err := locator.AriaSnapshot()
	if err != nil {
		return nil, snapshotState{}, err
	}
	lines := strings.Split(strings.TrimSpace(yaml), "\n")
	nodes := make([]map[string]any, 0, len(lines))
	stateNodes := make([]snapshotNode, 0, len(lines))
	roleNameCounts := map[string]int{}
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		ref := stableSnapshotRef("aria", selector, trimmed, i)
		role, name := parseAriaLine(trimmed)
		key := strings.ToLower(strings.TrimSpace(role)) + "\x00" + strings.TrimSpace(name)
		nth := roleNameCounts[key]
		roleNameCounts[key] = nth + 1
		nodes = append(nodes, map[string]any{
			"ref":   ref,
			"role":  role,
			"name":  name,
			"nth":   nth,
			"depth": leadingIndent(line),
		})
		stateNodes = append(stateNodes, snapshotNode{
			Ref:      ref,
			Selector: selector,
			Role:     role,
			Title:    name,
			Nth:      nth,
			Depth:    leadingIndent(line),
		})
	}
	return map[string]any{
			"format": "aria",
			"nodes":  nodes,
		}, snapshotState{
			Format:   "aria",
			RefsMode: "role",
			Frame:    strings.TrimSpace(req.Frame),
			Nodes:    stateNodes,
			SavedAt:  time.Now().UTC(),
		}, nil
}

func snapshotLine(node snapshotNode) string {
	label := strings.TrimSpace(node.Title)
	if label == "" {
		label = strings.TrimSpace(node.Aria)
	}
	if label == "" {
		label = strings.TrimSpace(node.Tag)
	}
	parts := []string{"[" + node.Ref + "]"}
	if strings.TrimSpace(node.Role) != "" {
		parts = append(parts, node.Role)
	} else if strings.TrimSpace(node.Tag) != "" {
		parts = append(parts, node.Tag)
	}
	if strings.TrimSpace(node.Kind) != "" {
		parts = append(parts, "("+node.Kind+")")
	}
	if label != "" {
		parts = append(parts, strconvQuote(label))
	}
	return strings.Join(parts, " ")
}

func stableSnapshotRef(parts ...any) string {
	h := fnv.New32a()
	for _, part := range parts {
		_, _ = h.Write([]byte(fmt.Sprint(part)))
		_, _ = h.Write([]byte{0})
	}
	return fmt.Sprintf("e%x", h.Sum32())
}

func parseAriaLine(line string) (string, string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", ""
	}
	trimmed = strings.TrimLeft(trimmed, "-*0123456789. ")
	trimmed = strings.TrimSpace(trimmed)
	if trimmed == "" {
		return "", ""
	}
	if idx := strings.Index(trimmed, `"`); idx >= 0 {
		role := strings.TrimSpace(trimmed[:idx])
		name := strings.TrimSpace(trimmed[idx:])
		name = strings.Trim(name, `"`)
		return role, name
	}
	if idx := strings.Index(trimmed, ":"); idx >= 0 {
		role := strings.TrimSpace(trimmed[:idx])
		name := strings.TrimSpace(trimmed[idx+1:])
		return role, name
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return "", ""
	}
	if len(fields) == 1 {
		return fields[0], ""
	}
	return fields[0], strings.TrimSpace(trimmed[len(fields[0]):])
}

func leadingIndent(line string) int {
	count := 0
	for _, ch := range line {
		if ch == ' ' {
			count++
			continue
		}
		break
	}
	return count
}

func strconvQuote(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return `""`
	}
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(value) + `"`
}
