package browser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	playwright "github.com/playwright-community/playwright-go"
)

const (
	DefaultProfileName = "kocort"
	defaultTarget      = "host"
)

type Manager struct {
	mu                  sync.Mutex
	artifactDir         string
	defaultProfile      string
	driverDir           string
	autoInstall         bool
	headless            bool
	useSystemBrowser    bool
	channel             string
	skipInstallBrowsers bool
	persistSession      bool
	userDataDir         string
	profiles            map[string]*profileState
	sessions            map[string]*sessionState
	snapshots           map[string]snapshotState
	dialogArms          map[string]dialogArm
	dialogWired         map[string]bool
	fileChooserArms     map[string][]string
	fileChooserWired    map[string]bool
}

type profileState struct {
	name      string
	pw        *playwright.Playwright
	browser   playwright.Browser
	context   playwright.BrowserContext
	attachErr string
	startedAt time.Time
	headless  bool
}

type sessionState struct {
	ActiveByProfile map[string]string
	TabsByProfile   map[string][]string
	// OwnedTabs tracks tab IDs that were explicitly created by the AI (via Open).
	// Tabs not in this set are considered user-owned and protected from implicit close.
	OwnedTabs map[string]struct{}
}

type dialogArm struct {
	Accept     bool
	PromptText string
}

func NewManager(opts Options) *Manager {
	profile := strings.TrimSpace(opts.DefaultProfile)
	if profile == "" {
		profile = DefaultProfileName
	}
	artifactDir := strings.TrimSpace(opts.ArtifactDir)
	if artifactDir == "" {
		artifactDir = filepath.Join(os.TempDir(), "kocort-browser")
	}
	userDataDir := strings.TrimSpace(opts.UserDataDir)
	if userDataDir == "" && opts.PersistSession {
		userDataDir = filepath.Join(artifactDir, "userdata")
	}
	return &Manager{
		artifactDir:         artifactDir,
		defaultProfile:      profile,
		driverDir:           strings.TrimSpace(opts.DriverDir),
		autoInstall:         opts.AutoInstall,
		headless:            opts.Headless == nil || *opts.Headless,
		useSystemBrowser:    opts.UseSystemBrowser,
		channel:             strings.TrimSpace(opts.Channel),
		skipInstallBrowsers: opts.SkipInstallBrowsers || opts.UseSystemBrowser,
		persistSession:      opts.PersistSession,
		userDataDir:         userDataDir,
		profiles:            map[string]*profileState{},
		sessions:            map[string]*sessionState{},
		snapshots:           map[string]snapshotState{},
		dialogArms:          map[string]dialogArm{},
		dialogWired:         map[string]bool{},
		fileChooserArms:     map[string][]string{},
		fileChooserWired:    map[string]bool{},
	}
}

func (m *Manager) Install(ctx context.Context, req Request) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runOptions := m.runOptions()
	if err := playwright.Install(runOptions); err != nil {
		return map[string]any{
			"ok":      false,
			"action":  "install",
			"profile": strings.TrimSpace(req.Profile),
			"error":   err.Error(),
			"runtime": m.runtimeHealthLocked(),
		}, nil
	}
	return map[string]any{
		"ok":      true,
		"action":  "install",
		"profile": strings.TrimSpace(req.Profile),
		"runtime": m.runtimeHealthLocked(),
	}, nil
}

func (m *Manager) Status(ctx context.Context, req Request) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	profileName, unavailable := m.resolveProfile(req)
	if unavailable != nil {
		return unavailable, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.profiles[profileName]
	result := m.statusResultLocked(profileName, state)
	result["action"] = "status"
	m.applyRuntimeHealthLocked(result)
	return result, nil
}

func (m *Manager) Start(ctx context.Context, req Request) (map[string]any, error) {
	profileName, unavailable := m.resolveProfile(req)
	if unavailable != nil {
		unavailable["action"] = "start"
		return unavailable, nil
	}
	state, err := m.ensureProfileWithHeadless(ctx, profileName, req.TimeoutMs, req.Headless)
	if err != nil {
		return map[string]any{
			"ok":      false,
			"action":  "start",
			"profile": profileName,
			"error":   err.Error(),
		}, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	result := m.statusResultLocked(profileName, state)
	result["action"] = "start"
	result["ok"] = true
	m.applyRuntimeHealthLocked(result)
	return result, nil
}

func (m *Manager) Stop(ctx context.Context, req Request) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	profileName, unavailable := m.resolveProfile(req)
	if unavailable != nil {
		unavailable["action"] = "stop"
		return unavailable, nil
	}
	m.mu.Lock()
	state := m.profiles[profileName]
	delete(m.profiles, profileName)
	for _, session := range m.sessions {
		delete(session.ActiveByProfile, profileName)
	}
	for key := range m.snapshots {
		if strings.HasPrefix(key, strings.TrimSpace(profileName)+"::") {
			delete(m.snapshots, key)
		}
	}
	m.mu.Unlock()
	if state != nil {
		_ = state.context.Close()
		if state.browser != nil {
			_ = state.browser.Close()
		}
		_ = state.pw.Stop()
	}
	return map[string]any{
		"ok":      true,
		"action":  "stop",
		"profile": profileName,
		"running": false,
	}, nil
}

func (m *Manager) Profiles(ctx context.Context, req Request) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	profileName, unavailable := m.resolveProfile(req)
	if unavailable != nil {
		unavailable["action"] = "profiles"
		return unavailable, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	profiles := []map[string]any{m.profileSummaryLocked(profileName, m.profiles[profileName], true)}
	runtimeHealth := m.runtimeHealthLocked()
	return map[string]any{
		"ok":       true,
		"action":   "profiles",
		"profile":  profileName,
		"profiles": profiles,
		"runtime":  runtimeHealth,
	}, nil
}

func (m *Manager) Tabs(ctx context.Context, req Request) (map[string]any, error) {
	profileName, unavailable := m.resolveProfile(req)
	if unavailable != nil {
		unavailable["action"] = "tabs"
		return unavailable, nil
	}
	state, err := m.ensureProfile(ctx, profileName, req.TimeoutMs)
	if err != nil {
		return map[string]any{"ok": false, "action": "tabs", "profile": profileName, "error": err.Error()}, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sessionTabs := m.trackedTabsLocked(req.SessionKey, profileName)
	tabs := m.tabsLocked(state)
	return map[string]any{
		"ok":          true,
		"action":      "tabs",
		"profile":     profileName,
		"targetId":    m.activeTargetLocked(req.SessionKey, profileName),
		"tabs":        tabs,
		"tabCount":    len(tabs),
		"sessionTabs": sessionTabs,
		"sessionKey":  normalizeSessionKey(req.SessionKey),
	}, nil
}

func (m *Manager) Open(ctx context.Context, req OpenRequest) (map[string]any, error) {
	profileName, unavailable := m.resolveProfile(req.Request)
	if unavailable != nil {
		unavailable["action"] = "open"
		return unavailable, nil
	}
	state, err := m.ensureProfile(ctx, profileName, req.TimeoutMs)
	if err != nil {
		return map[string]any{"ok": false, "action": "open", "profile": profileName, "error": err.Error()}, nil
	}
	url := firstNonEmpty(req.TargetURL, req.URL)
	if err := validateNavigationURL(url); err != nil {
		return map[string]any{"ok": false, "action": "open", "error": err.Error()}, nil
	}
	page, err := state.context.NewPage()
	if err != nil {
		return map[string]any{"ok": false, "action": "open", "profile": profileName, "error": err.Error()}, nil
	}
	if _, err := page.Goto(url, gotoOptions(req.TimeoutMs)); err != nil {
		_ = page.Close()
		return map[string]any{"ok": false, "action": "open", "profile": profileName, "error": err.Error()}, nil
	}
	tab := pageToTab(page)
	openedTargetID := tab["targetId"].(string)
	m.mu.Lock()
	m.setActiveLocked(req.SessionKey, profileName, openedTargetID)
	m.markOwnedTabLocked(req.SessionKey, openedTargetID)
	sessionTabs := m.trackedTabsLocked(req.SessionKey, profileName)
	m.mu.Unlock()
	return map[string]any{
		"ok":          true,
		"action":      "open",
		"profile":     profileName,
		"targetId":    tab["targetId"],
		"tab":         tab,
		"sessionTabs": sessionTabs,
		"sessionKey":  normalizeSessionKey(req.SessionKey),
	}, nil
}

func (m *Manager) Focus(ctx context.Context, req TargetRequest) (map[string]any, error) {
	profileName, unavailable := m.resolveProfile(req.Request)
	if unavailable != nil {
		unavailable["action"] = "focus"
		return unavailable, nil
	}
	page, state, targetID, err := m.resolvePage(ctx, profileName, req)
	if err != nil {
		return map[string]any{"ok": false, "action": "focus", "profile": profileName, "error": err.Error()}, nil
	}
	if err := page.BringToFront(); err != nil {
		return map[string]any{"ok": false, "action": "focus", "profile": profileName, "targetId": targetID, "error": err.Error()}, nil
	}
	m.mu.Lock()
	m.setActiveLocked(req.SessionKey, profileName, targetID)
	tab := pageToTab(page)
	sessionTabs := m.trackedTabsLocked(req.SessionKey, profileName)
	_ = state
	m.mu.Unlock()
	return map[string]any{
		"ok":          true,
		"action":      "focus",
		"profile":     profileName,
		"targetId":    targetID,
		"tab":         tab,
		"sessionTabs": sessionTabs,
	}, nil
}

func (m *Manager) Close(ctx context.Context, req TargetRequest) (map[string]any, error) {
	profileName, unavailable := m.resolveProfile(req.Request)
	if unavailable != nil {
		unavailable["action"] = "close"
		return unavailable, nil
	}
	page, state, targetID, err := m.resolvePage(ctx, profileName, req)
	if err != nil {
		return map[string]any{"ok": false, "action": "close", "profile": profileName, "error": err.Error()}, nil
	}
	// Guard: when no explicit targetId was requested, only allow closing AI-owned tabs.
	// This prevents accidentally closing user's pre-existing tabs via fallback resolution.
	explicitTarget := strings.TrimSpace(req.TargetID) != ""
	m.mu.Lock()
	owned := m.isOwnedTabLocked(req.SessionKey, targetID)
	m.mu.Unlock()
	if !explicitTarget && !owned {
		return map[string]any{
			"ok":       false,
			"action":   "close",
			"profile":  profileName,
			"targetId": targetID,
			"error":    fmt.Sprintf("tab %q was not opened by this session; specify targetId explicitly to close it", targetID),
		}, nil
	}
	if err := page.Close(); err != nil {
		return map[string]any{"ok": false, "action": "close", "profile": profileName, "targetId": targetID, "error": err.Error()}, nil
	}
	m.mu.Lock()
	delete(m.snapshots, snapshotKey(profileName, targetID))
	m.unmarkOwnedTabLocked(req.SessionKey, targetID)
	active := m.reconcileActiveAfterCloseLocked(req.SessionKey, profileName, targetID, state.context)
	sessionTabs := m.trackedTabsLocked(req.SessionKey, profileName)
	m.mu.Unlock()
	return map[string]any{
		"ok":          true,
		"action":      "close",
		"profile":     profileName,
		"targetId":    targetID,
		"activeTabId": active,
		"sessionTabs": sessionTabs,
	}, nil
}

func (m *Manager) Navigate(ctx context.Context, req NavigateRequest) (map[string]any, error) {
	profileName, unavailable := m.resolveProfile(req.Request)
	if unavailable != nil {
		unavailable["action"] = "navigate"
		return unavailable, nil
	}
	url := firstNonEmpty(req.TargetURL, req.URL)
	if err := validateNavigationURL(url); err != nil {
		return map[string]any{"ok": false, "action": "navigate", "error": err.Error()}, nil
	}
	page, _, targetID, err := m.resolvePage(ctx, profileName, req.TargetRequest)
	if err != nil {
		return map[string]any{"ok": false, "action": "navigate", "profile": profileName, "error": err.Error()}, nil
	}
	if _, err := page.Goto(url, gotoOptions(req.TimeoutMs)); err != nil {
		return map[string]any{"ok": false, "action": "navigate", "profile": profileName, "targetId": targetID, "error": err.Error()}, nil
	}
	m.mu.Lock()
	m.setActiveLocked(req.SessionKey, profileName, targetID)
	sessionTabs := m.trackedTabsLocked(req.SessionKey, profileName)
	m.mu.Unlock()
	return map[string]any{
		"ok":          true,
		"action":      "navigate",
		"profile":     profileName,
		"targetId":    targetID,
		"tab":         pageToTab(page),
		"sessionTabs": sessionTabs,
	}, nil
}
