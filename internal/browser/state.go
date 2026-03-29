package browser

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strings"
	"time"

	playwright "github.com/playwright-community/playwright-go"
)

func (m *Manager) ensureProfile(ctx context.Context, profileName string, timeoutMs int) (*profileState, error) {
	return m.ensureProfileWithHeadless(ctx, profileName, timeoutMs, nil)
}

// ensureProfileWithHeadless starts or reuses a browser profile. When headlessOverride
// is non-nil and differs from the running profile's headless mode, the profile
// is torn down and relaunched with the requested mode.
func (m *Manager) ensureProfileWithHeadless(ctx context.Context, profileName string, timeoutMs int, headlessOverride *bool) (*profileState, error) {
	m.mu.Lock()
	state := m.profiles[profileName]
	if state != nil && state.browser != nil && state.context != nil && state.browser.IsConnected() {
		// If a headless override is requested and differs from the running profile, restart.
		if headlessOverride != nil && *headlessOverride != state.headless {
			m.mu.Unlock()
			m.teardownProfile(profileName)
		} else {
			m.mu.Unlock()
			return state, nil
		}
	} else {
		m.mu.Unlock()
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pw, err := m.runPlaywright()
	if err != nil {
		return nil, fmt.Errorf("playwright runtime is not available: %w", err)
	}
	timeout := float64(30000)
	if timeoutMs > 0 {
		timeout = float64(timeoutMs)
	}
	headless := m.headless
	if headlessOverride != nil {
		headless = *headlessOverride
	}
	ch := m.resolveChannel()

	var browser playwright.Browser
	var bCtx playwright.BrowserContext

	if m.persistSession {
		// Use LaunchPersistentContext for session persistence.
		userDataDir := m.profileUserDataDir(profileName)
		if err := os.MkdirAll(userDataDir, 0o755); err != nil {
			_ = pw.Stop()
			return nil, fmt.Errorf("failed to create user data dir %q: %w", userDataDir, err)
		}
		persistOpts := playwright.BrowserTypeLaunchPersistentContextOptions{
			Headless: playwright.Bool(headless),
			Timeout:  playwright.Float(timeout),
		}
		if ch != "" {
			persistOpts.Channel = playwright.String(ch)
		}
		bCtx, err = pw.Chromium.LaunchPersistentContext(userDataDir, persistOpts)
		if err != nil {
			_ = pw.Stop()
			return nil, fmt.Errorf("failed to launch persistent browser: %w", err)
		}
		browser = bCtx.Browser()
	} else {
		// Standard non-persistent launch.
		launchOpts := playwright.BrowserTypeLaunchOptions{
			Headless: playwright.Bool(headless),
			Timeout:  playwright.Float(timeout),
		}
		if ch != "" {
			launchOpts.Channel = playwright.String(ch)
		}
		browser, err = pw.Chromium.Launch(launchOpts)
		if err != nil {
			_ = pw.Stop()
			return nil, fmt.Errorf("failed to launch browser: %w", err)
		}
		bCtx, err = browser.NewContext()
		if err != nil {
			_ = browser.Close()
			_ = pw.Stop()
			return nil, err
		}
	}

	state = &profileState{
		name:      profileName,
		pw:        pw,
		browser:   browser,
		context:   bCtx,
		startedAt: time.Now().UTC(),
		headless:  headless,
	}
	ensureObservedContextState(bCtx)
	m.mu.Lock()
	m.profiles[profileName] = state
	m.mu.Unlock()
	return state, nil
}

// profileUserDataDir returns the user-data directory for the given profile.
func (m *Manager) profileUserDataDir(profileName string) string {
	base := m.userDataDir
	if base == "" {
		base = filepath.Join(m.artifactDir, "userdata")
	}
	return filepath.Join(base, sanitizeFileSegment(profileName))
}

// teardownProfile tears down a running profile, closing browser and playwright.
func (m *Manager) teardownProfile(profileName string) {
	m.mu.Lock()
	state := m.profiles[profileName]
	delete(m.profiles, profileName)
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
}

func (m *Manager) runPlaywright() (*playwright.Playwright, error) {
	runOptions := m.runOptions()
	pw, err := playwright.Run(runOptions)
	if err == nil {
		return pw, nil
	}
	if !m.autoInstall || !strings.Contains(strings.ToLower(err.Error()), "install the driver") {
		return nil, err
	}
	if installErr := playwright.Install(runOptions); installErr != nil {
		return nil, fmt.Errorf("%w (install failed: %v)", err, installErr)
	}
	return playwright.Run(runOptions)
}

func (m *Manager) resolveProfile(req Request) (string, map[string]any) {
	if strings.EqualFold(strings.TrimSpace(req.Target), "node") || strings.TrimSpace(req.Node) != "" {
		return "", map[string]any{
			"ok":      false,
			"status":  "unavailable",
			"target":  strings.TrimSpace(req.Target),
			"profile": strings.TrimSpace(req.Profile),
			"error":   "target=node is not implemented in this runtime",
		}
	}
	profileName := strings.TrimSpace(req.Profile)
	if profileName == "" {
		profileName = m.defaultProfile
	}
	return profileName, nil
}

func (m *Manager) resolvePage(ctx context.Context, profileName string, req TargetRequest) (playwright.Page, *profileState, string, error) {
	state, err := m.ensureProfile(ctx, profileName, req.TimeoutMs)
	if err != nil {
		return nil, nil, "", err
	}
	targetID := strings.TrimSpace(req.TargetID)
	if targetID == "" {
		m.mu.Lock()
		targetID = m.activeTargetLocked(req.SessionKey, profileName)
		if targetID == "" {
			targetID = m.lastTrackedTargetLocked(req.SessionKey, profileName, state.context)
		}
		m.mu.Unlock()
	}
	var page playwright.Page
	pages := state.context.Pages()
	if targetID != "" {
		for _, candidate := range pages {
			if pageTargetID(candidate) == targetID {
				page = candidate
				break
			}
		}
	}
	if page == nil && len(pages) > 0 {
		// Prefer falling back to an AI-owned tab over a random user tab.
		m.mu.Lock()
		for i := len(pages) - 1; i >= 0; i-- {
			tid := pageTargetID(pages[i])
			if m.isOwnedTabLocked(req.SessionKey, tid) {
				page = pages[i]
				targetID = tid
				break
			}
		}
		m.mu.Unlock()
		// If no owned tab found, use the last page as before.
		if page == nil {
			page = pages[len(pages)-1]
			targetID = pageTargetID(page)
		}
	}
	if page == nil {
		return nil, state, "", errors.New("no browser tab selected")
	}
	ensureObservedPageState(page)
	m.ensureDialogHook(profileName, targetID, page)
	m.ensureFileChooserHook(profileName, targetID, page)
	return page, state, targetID, nil
}

func (m *Manager) tabsLocked(state *profileState) []map[string]any {
	if state == nil || state.context == nil {
		return nil
	}
	pages := state.context.Pages()
	tabs := make([]map[string]any, 0, len(pages))
	for _, page := range pages {
		tabs = append(tabs, pageToTab(page))
	}
	return tabs
}

func (m *Manager) statusResultLocked(profileName string, state *profileState) map[string]any {
	running := state != nil && state.browser != nil && state.context != nil && state.browser.IsConnected()
	tabCount := 0
	headless := m.headless
	if running {
		tabCount = len(state.context.Pages())
		headless = state.headless
	}
	result := map[string]any{
		"ok":                     true,
		"enabled":                true,
		"profile":                profileName,
		"running":                running,
		"cdpReady":               false,
		"cdpHttp":                false,
		"pid":                    0,
		"cdpPort":                0,
		"cdpUrl":                 "",
		"chosenBrowser":          "chromium",
		"detectedBrowser":        "chromium",
		"detectedExecutablePath": "",
		"detectError":            "",
		"userDataDir":            m.profileUserDataDir(profileName),
		"persistSession":         m.persistSession,
		"color":                  "blue",
		"headless":               headless,
		"noSandbox":              false,
		"executablePath":         "",
		"attachOnly":             false,
		"tabCount":               tabCount,
	}
	if state != nil && state.attachErr != "" {
		result["detectError"] = state.attachErr
	}
	return result
}

func (m *Manager) runtimeHealthLocked() map[string]any {
	resolvedDriverDir := strings.TrimSpace(m.driverDir)
	if resolvedDriverDir == "" {
		resolvedDriverDir = detectBundledDriverDir()
	}
	if resolvedDriverDir == "" {
		resolvedDriverDir = os.Getenv("PLAYWRIGHT_DRIVER_PATH")
	}
	result := map[string]any{
		"driverDirectory":  resolvedDriverDir,
		"autoInstall":      m.autoInstall,
		"useSystemBrowser": m.useSystemBrowser,
		"channel":          m.resolveChannel(),
	}
	if checkErr := m.checkPlaywrightRuntime(); checkErr != nil {
		result["ready"] = false
		result["error"] = checkErr.Error()
		result["installHint"] = "Run playwright.Install() or set auto-install in the browser manager."
		return result
	}
	result["ready"] = true
	return result
}

func (m *Manager) applyRuntimeHealthLocked(result map[string]any) {
	result["runtime"] = m.runtimeHealthLocked()
	if ready, _ := result["runtime"].(map[string]any)["ready"].(bool); !ready {
		if detectError, _ := result["runtime"].(map[string]any)["error"].(string); strings.TrimSpace(detectError) != "" {
			result["detectError"] = detectError
		}
	}
}

func (m *Manager) profileSummaryLocked(profileName string, state *profileState, isDefault bool) map[string]any {
	running := state != nil && state.browser != nil && state.context != nil && state.browser.IsConnected()
	tabCount := 0
	if running {
		tabCount = len(state.context.Pages())
	}
	return map[string]any{
		"name":              profileName,
		"cdpPort":           0,
		"cdpUrl":            "",
		"color":             "blue",
		"running":           running,
		"tabCount":          tabCount,
		"isDefault":         isDefault,
		"isRemote":          false,
		"missingFromConfig": false,
		"reconcileReason":   "",
	}
}

func (m *Manager) activeTargetLocked(sessionKey, profileName string) string {
	state := m.sessions[normalizeSessionKey(sessionKey)]
	if state == nil {
		return ""
	}
	return strings.TrimSpace(state.ActiveByProfile[profileName])
}

func (m *Manager) trackedTabsLocked(sessionKey, profileName string) []string {
	state := m.sessions[normalizeSessionKey(sessionKey)]
	if state == nil || state.TabsByProfile == nil {
		return nil
	}
	tabs := state.TabsByProfile[profileName]
	out := make([]string, 0, len(tabs))
	for _, item := range tabs {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func (m *Manager) setActiveLocked(sessionKey, profileName, targetID string) {
	key := normalizeSessionKey(sessionKey)
	state := m.sessions[key]
	if state == nil {
		state = &sessionState{ActiveByProfile: map[string]string{}, TabsByProfile: map[string][]string{}, OwnedTabs: map[string]struct{}{}}
		m.sessions[key] = state
	}
	state.ActiveByProfile[profileName] = strings.TrimSpace(targetID)
	m.trackTabLocked(state, profileName, targetID)
}

func (m *Manager) syncTrackedTabsLocked(state *sessionState, profileName string, ctx playwright.BrowserContext) {
	if state == nil {
		return
	}
	existing := ctx.Pages()
	if len(existing) == 0 {
		delete(state.TabsByProfile, profileName)
		delete(state.ActiveByProfile, profileName)
		return
	}
	live := make(map[string]struct{}, len(existing))
	ordered := make([]string, 0, len(existing))
	for _, page := range existing {
		targetID := pageTargetID(page)
		live[targetID] = struct{}{}
		ordered = append(ordered, targetID)
	}
	prior := state.TabsByProfile[profileName]
	next := make([]string, 0, len(ordered))
	seen := map[string]struct{}{}
	for _, targetID := range prior {
		targetID = strings.TrimSpace(targetID)
		if _, ok := live[targetID]; ok {
			next = append(next, targetID)
			seen[targetID] = struct{}{}
		}
	}
	for _, targetID := range ordered {
		if _, ok := seen[targetID]; !ok {
			next = append(next, targetID)
		}
	}
	state.TabsByProfile[profileName] = next
	active := strings.TrimSpace(state.ActiveByProfile[profileName])
	if _, ok := live[active]; !ok {
		state.ActiveByProfile[profileName] = next[len(next)-1]
	}
}

func (m *Manager) reconcileActiveAfterCloseLocked(sessionKey, profileName, closedTargetID string, ctx playwright.BrowserContext) string {
	key := normalizeSessionKey(sessionKey)
	state := m.sessions[key]
	if state == nil {
		return ""
	}
	pages := ctx.Pages()
	if len(pages) == 0 {
		delete(state.ActiveByProfile, profileName)
		delete(state.TabsByProfile, profileName)
		return ""
	}
	if closedTargetID = strings.TrimSpace(closedTargetID); closedTargetID != "" {
		m.untrackTabLocked(state, profileName, closedTargetID)
	}
	m.syncTrackedTabsLocked(state, profileName, ctx)
	next := pageTargetID(pages[len(pages)-1])
	state.ActiveByProfile[profileName] = next
	m.trackTabLocked(state, profileName, next)
	return next
}

func (m *Manager) trackTabLocked(state *sessionState, profileName, targetID string) {
	targetID = strings.TrimSpace(targetID)
	if state == nil || targetID == "" {
		return
	}
	if state.TabsByProfile == nil {
		state.TabsByProfile = map[string][]string{}
	}
	tabs := state.TabsByProfile[profileName]
	next := make([]string, 0, len(tabs)+1)
	for _, item := range tabs {
		if strings.TrimSpace(item) != targetID {
			next = append(next, item)
		}
	}
	next = append(next, targetID)
	state.TabsByProfile[profileName] = next
}

func (m *Manager) untrackTabLocked(state *sessionState, profileName, targetID string) {
	targetID = strings.TrimSpace(targetID)
	if state == nil || targetID == "" || state.TabsByProfile == nil {
		return
	}
	tabs := state.TabsByProfile[profileName]
	next := make([]string, 0, len(tabs))
	for _, item := range tabs {
		if strings.TrimSpace(item) != targetID {
			next = append(next, item)
		}
	}
	if len(next) == 0 {
		delete(state.TabsByProfile, profileName)
		return
	}
	state.TabsByProfile[profileName] = next
}

// markOwnedTabLocked records a tab as AI-created (owned by this session).
func (m *Manager) markOwnedTabLocked(sessionKey, targetID string) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return
	}
	key := normalizeSessionKey(sessionKey)
	state := m.sessions[key]
	if state == nil {
		state = &sessionState{
			ActiveByProfile: map[string]string{},
			TabsByProfile:   map[string][]string{},
			OwnedTabs:       map[string]struct{}{},
		}
		m.sessions[key] = state
	}
	if state.OwnedTabs == nil {
		state.OwnedTabs = map[string]struct{}{}
	}
	state.OwnedTabs[targetID] = struct{}{}
}

// isOwnedTabLocked checks if a tab was created by the AI in this session.
func (m *Manager) isOwnedTabLocked(sessionKey, targetID string) bool {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return false
	}
	state := m.sessions[normalizeSessionKey(sessionKey)]
	if state == nil || state.OwnedTabs == nil {
		return false
	}
	_, ok := state.OwnedTabs[targetID]
	return ok
}

// unmarkOwnedTabLocked removes a tab from the AI-owned set.
func (m *Manager) unmarkOwnedTabLocked(sessionKey, targetID string) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return
	}
	state := m.sessions[normalizeSessionKey(sessionKey)]
	if state == nil || state.OwnedTabs == nil {
		return
	}
	delete(state.OwnedTabs, targetID)
}

func (m *Manager) lastTrackedTargetLocked(sessionKey, profileName string, ctx playwright.BrowserContext) string {
	state := m.sessions[normalizeSessionKey(sessionKey)]
	if state == nil || state.TabsByProfile == nil {
		return ""
	}
	tabs := state.TabsByProfile[profileName]
	if len(tabs) == 0 {
		return ""
	}
	existing := map[string]struct{}{}
	for _, page := range ctx.Pages() {
		existing[pageTargetID(page)] = struct{}{}
	}
	for i := len(tabs) - 1; i >= 0; i-- {
		targetID := strings.TrimSpace(tabs[i])
		if _, ok := existing[targetID]; ok {
			return targetID
		}
	}
	return ""
}

func (m *Manager) runOptions() *playwright.RunOptions {
	options := &playwright.RunOptions{
		SkipInstallBrowsers: m.skipInstallBrowsers,
		Browsers:            []string{"chromium"},
	}
	driverDir := strings.TrimSpace(m.driverDir)
	if driverDir == "" {
		driverDir = detectBundledDriverDir()
	}
	if driverDir != "" {
		options.DriverDirectory = driverDir
	}
	return options
}

// resolveChannel determines which browser channel to use.
// Priority: explicit channel config > system browser auto-detect > empty (bundled Chromium).
func (m *Manager) resolveChannel() string {
	if m.channel != "" {
		return m.channel
	}
	if !m.useSystemBrowser {
		return ""
	}
	return detectSystemBrowser()
}

// detectBundledDriverDir probes standard locations next to the current
// executable for a pre-bundled Playwright driver directory. This allows
// the app to ship with the driver without requiring an explicit config.
//
// Search order:
//  1. <exe_dir>/playwright-driver          (standalone / Linux / Windows)
//  2. <exe_dir>/../Resources/playwright-driver   (macOS .app bundle)
//  3. <cwd>/playwright-driver               (development)
func detectBundledDriverDir() string {
	probe := func(dir string) string {
		candidate := filepath.Join(dir, "playwright-driver")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		return ""
	}

	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		// 1. Next to executable
		if d := probe(exeDir); d != "" {
			return d
		}
		// 2. macOS .app bundle: Contents/MacOS/../Resources = Contents/Resources
		if goruntime.GOOS == "darwin" {
			if d := probe(filepath.Join(exeDir, "..", "Resources")); d != "" {
				return d
			}
		}
	}
	// 3. Working directory
	if wd, err := os.Getwd(); err == nil {
		if d := probe(wd); d != "" {
			return d
		}
	}
	return ""
}

// detectSystemBrowser probes for system-installed browsers and returns
// the first available Playwright channel. Fallback order: chrome → msedge.
// Safari/WebKit cannot be used via Channel — Playwright uses its own patched WebKit.
func detectSystemBrowser() string {
	candidates := []struct {
		channel string
		paths   []string // platform-specific executable paths
	}{
		{"chrome", chromeExecutablePaths()},
		{"msedge", edgeExecutablePaths()},
	}
	for _, c := range candidates {
		for _, p := range c.paths {
			if _, err := exec.LookPath(p); err == nil {
				return c.channel
			}
			if _, err := os.Stat(p); err == nil {
				return c.channel
			}
		}
	}
	return "" // no system browser found, will use bundled Chromium
}

func chromeExecutablePaths() []string {
	switch strings.ToLower(sysOS()) {
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"google-chrome",
		}
	case "linux":
		return []string{
			"google-chrome",
			"google-chrome-stable",
			"chromium-browser",
			"chromium",
		}
	case "windows":
		return []string{
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			"chrome",
		}
	default:
		return []string{"google-chrome", "chromium"}
	}
}

func edgeExecutablePaths() []string {
	switch strings.ToLower(sysOS()) {
	case "darwin":
		return []string{
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"microsoft-edge",
		}
	case "linux":
		return []string{
			"microsoft-edge",
			"microsoft-edge-stable",
		}
	case "windows":
		return []string{
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
			"msedge",
		}
	default:
		return []string{"microsoft-edge"}
	}
}

func sysOS() string {
	return goruntime.GOOS
}

func (m *Manager) checkPlaywrightRuntime() error {
	driver, err := playwright.NewDriver(m.runOptions())
	if err != nil {
		return err
	}
	cmd := driver.Command("--version")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return fmt.Errorf("playwright driver is missing: %w", err)
		}
		return err
	}
	return nil
}

func normalizeSessionKey(sessionKey string) string {
	if strings.TrimSpace(sessionKey) == "" {
		return "__default__"
	}
	return strings.TrimSpace(sessionKey)
}

func sanitizeFileSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "page"
	}
	value = strings.ReplaceAll(value, "/", "-")
	value = strings.ReplaceAll(value, ":", "-")
	return value
}

func snapshotKey(profileName, targetID string) string {
	return strings.TrimSpace(profileName) + "::" + strings.TrimSpace(targetID)
}

func pageToTab(page playwright.Page) map[string]any {
	title, _ := page.Title()
	return map[string]any{
		"targetId": pageTargetID(page),
		"title":    title,
		"url":      page.URL(),
		"wsUrl":    "",
		"type":     "page",
	}
}

func pageTargetID(page playwright.Page) string {
	if page == nil {
		return ""
	}
	value := reflect.ValueOf(page)
	if value.Kind() == reflect.Interface {
		value = value.Elem()
	}
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	field := value.FieldByName("channelOwner")
	if field.IsValid() {
		guid := field.FieldByName("guid")
		if guid.IsValid() && guid.Kind() == reflect.String {
			return guid.String()
		}
	}
	return page.URL()
}

func gotoOptions(timeoutMs int) playwright.PageGotoOptions {
	options := playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateLoad,
	}
	if timeoutMs > 0 {
		options.Timeout = playwright.Float(float64(timeoutMs))
	}
	return options
}

func timeoutFloat(timeoutMs int) *float64 {
	if timeoutMs <= 0 {
		return nil
	}
	return playwright.Float(float64(timeoutMs))
}

func imagePathString(path string) *string {
	return playwright.String(path)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
