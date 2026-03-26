// Canonical implementation — migrated from runtime/skills_runtime.go.
package skill

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/infra"

	"github.com/fsnotify/fsnotify"
)

var defaultSkillConfigTruthyValues = map[string]bool{
	"browser.enabled":         true,
	"browser.evaluateenabled": true,
}

type skillRequirementsEval struct {
	MissingBins   []string
	MissingEnv    []string
	MissingConfig []string
	ConfigChecks  []core.SkillConfigCheck
	Eligible      bool
}

func evaluateSkillRequirements(entry core.SkillEntry, cfg *config.AppConfig, eligibility *core.SkillEligibilityContext) skillRequirementsEval {
	metadata := entry.Metadata
	if metadata == nil || metadata.Requires == nil {
		return skillRequirementsEval{Eligible: true}
	}
	if metadata.Always {
		checks := buildSkillConfigChecks(metadata.Requires.Config, cfg)
		return skillRequirementsEval{Eligible: true, ConfigChecks: checks}
	}
	missingBins := resolveMissingSkillBins(metadata.Requires.Bins, eligibility)
	missingEnv := resolveMissingSkillEnv(entry, cfg, metadata.Requires.Env)
	configChecks := buildSkillConfigChecks(metadata.Requires.Config, cfg)
	missingConfig := make([]string, 0, len(configChecks))
	for _, check := range configChecks {
		if !check.Satisfied {
			missingConfig = append(missingConfig, check.Path)
		}
	}
	missingAnyBins := resolveMissingSkillAnyBins(metadata.Requires.AnyBins, eligibility)
	if len(missingAnyBins) > 0 {
		missingBins = append(missingBins, missingAnyBins...)
	}
	eligibleNow := len(missingBins) == 0 && len(missingEnv) == 0 && len(missingConfig) == 0
	if len(metadata.OS) > 0 && !skillOSAllowed(metadata.OS, eligibility) {
		eligibleNow = false
	}
	return skillRequirementsEval{
		MissingBins:   missingBins,
		MissingEnv:    missingEnv,
		MissingConfig: missingConfig,
		ConfigChecks:  configChecks,
		Eligible:      eligibleNow,
	}
}

func resolveMissingSkillBins(required []string, eligibility *core.SkillEligibilityContext) []string {
	var missing []string
	for _, bin := range required {
		trimmed := strings.TrimSpace(bin)
		if trimmed == "" {
			continue
		}
		if hasSkillBinary(trimmed, eligibility) {
			continue
		}
		missing = append(missing, trimmed)
	}
	return missing
}

func resolveMissingSkillAnyBins(required []string, eligibility *core.SkillEligibilityContext) []string {
	var normalized []string
	for _, bin := range required {
		if trimmed := strings.TrimSpace(bin); trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	if eligibility != nil && eligibility.Remote != nil && eligibility.Remote.HasAnyBin != nil && eligibility.Remote.HasAnyBin(normalized) {
		return nil
	}
	for _, bin := range normalized {
		if hasSkillBinary(bin, eligibility) {
			return nil
		}
	}
	return normalized
}

func hasSkillBinary(bin string, eligibility *core.SkillEligibilityContext) bool {
	if eligibility != nil && eligibility.Remote != nil && eligibility.Remote.HasBin != nil && eligibility.Remote.HasBin(bin) {
		return true
	}
	_, err := exec.LookPath(bin)
	return err == nil
}

func resolveMissingSkillEnv(entry core.SkillEntry, cfg *config.AppConfig, required []string) []string {
	var missing []string
	for _, envName := range required {
		trimmed := strings.TrimSpace(envName)
		if trimmed == "" {
			continue
		}
		if strings.TrimSpace(resolveSkillEnvValue(cfg, entry, trimmed)) != "" {
			continue
		}
		missing = append(missing, trimmed)
	}
	return missing
}

func resolveSkillEnvValue(cfg *config.AppConfig, entry core.SkillEntry, key string) string {
	if value := strings.TrimSpace(os.Getenv(strings.TrimSpace(key))); value != "" {
		return value
	}
	envRuntime := infra.NewEnvironmentRuntime(config.EnvironmentConfig{})
	if cfg != nil {
		envRuntime = infra.NewEnvironmentRuntime(cfg.Env)
	}
	skillCfg := resolveSkillConfigForEntry(cfg, entry)
	if skillCfg == nil {
		if value, ok := envRuntime.Resolve(strings.TrimSpace(key)); ok {
			return strings.TrimSpace(value)
		}
		return ""
	}
	if value, err := envRuntime.ResolveString(skillCfg.Env[strings.TrimSpace(key)]); err == nil && strings.TrimSpace(value) != "" {
		return value
	}
	primary := ""
	if entry.Metadata != nil {
		primary = strings.TrimSpace(entry.Metadata.PrimaryEnv)
	}
	if primary != "" && strings.EqualFold(strings.TrimSpace(key), primary) {
		if value, err := envRuntime.ResolveString(skillCfg.APIKey); err == nil {
			return strings.TrimSpace(value)
		}
	}
	if value, ok := envRuntime.Resolve(strings.TrimSpace(key)); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func buildSkillConfigChecks(required []string, cfg *config.AppConfig) []core.SkillConfigCheck {
	checks := make([]core.SkillConfigCheck, 0, len(required))
	for _, rawPath := range required {
		if path := strings.TrimSpace(rawPath); path != "" {
			checks = append(checks, core.SkillConfigCheck{
				Path:      path,
				Satisfied: IsSkillConfigPathTruthy(cfg, path),
			})
		}
	}
	return checks
}

func IsSkillConfigPathTruthy(cfg *config.AppConfig, pathStr string) bool {
	normalizedPath := normalizeSkillConfigPath(pathStr)
	if normalizedPath == "" {
		return false
	}
	if value, ok := defaultSkillConfigTruthyValues[normalizedPath]; ok {
		return value
	}
	if cfg == nil {
		return false
	}
	value, ok := resolveConfigPathValue(reflect.ValueOf(*cfg), strings.Split(normalizedPath, "."))
	if !ok {
		return false
	}
	return isTruthyConfigValue(value)
}

func normalizeSkillConfigPath(pathStr string) string {
	parts := strings.Split(strings.TrimSpace(pathStr), ".")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.ToLower(strings.TrimSpace(part)); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return strings.Join(out, ".")
}

func resolveConfigPathValue(value reflect.Value, parts []string) (reflect.Value, bool) {
	for value.IsValid() {
		if value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return reflect.Value{}, false
			}
			value = value.Elem()
			continue
		}
		break
	}
	if len(parts) == 0 {
		return value, value.IsValid()
	}
	if !value.IsValid() {
		return reflect.Value{}, false
	}
	head := parts[0]
	switch value.Kind() {
	case reflect.Struct:
		typ := value.Type()
		for i := 0; i < value.NumField(); i++ {
			field := typ.Field(i)
			tag := strings.Split(field.Tag.Get("json"), ",")[0]
			name := strings.ToLower(strings.TrimSpace(tag))
			if name == "" || name == "-" {
				name = strings.ToLower(field.Name)
			}
			if name != head {
				continue
			}
			return resolveConfigPathValue(value.Field(i), parts[1:])
		}
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			key := strings.ToLower(strings.TrimSpace(asString(iter.Key().Interface())))
			if key != head {
				continue
			}
			return resolveConfigPathValue(iter.Value(), parts[1:])
		}
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Value{}, false
		}
		return resolveConfigPathValue(value.Elem(), parts)
	}
	return reflect.Value{}, false
}

func isTruthyConfigValue(value reflect.Value) bool {
	for value.IsValid() {
		if value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return false
			}
			value = value.Elem()
			continue
		}
		break
	}
	if !value.IsValid() {
		return false
	}
	switch value.Kind() {
	case reflect.Bool:
		return value.Bool()
	case reflect.String:
		return strings.TrimSpace(value.String()) != ""
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return value.Uint() != 0
	case reflect.Float32, reflect.Float64:
		return value.Float() != 0
	case reflect.Slice, reflect.Array, reflect.Map:
		return value.Len() > 0
	case reflect.Struct:
		return true
	default:
		return !value.IsZero()
	}
}

func skillOSAllowed(required []string, eligibility *core.SkillEligibilityContext) bool {
	if len(required) == 0 {
		return true
	}
	for _, osName := range required {
		if strings.EqualFold(strings.TrimSpace(osName), runtimeGOOS()) {
			return true
		}
	}
	if eligibility != nil && eligibility.Remote != nil {
		for _, remoteOS := range eligibility.Remote.Platforms {
			for _, expected := range required {
				if strings.EqualFold(strings.TrimSpace(remoteOS), strings.TrimSpace(expected)) {
					return true
				}
			}
		}
	}
	return false
}

func runtimeGOOS() string {
	return strings.TrimSpace(strings.ToLower(runtime.GOOS))
}

type activeSkillEnvEntry struct {
	baseline *string
	value    string
	count    int
}

var (
	activeSkillEnvMu      sync.Mutex
	activeSkillEnvEntries = map[string]*activeSkillEnvEntry{}
)

func ApplySkillEnvOverridesFromEntries(cfg *config.AppConfig, entries []core.SkillEntry) func() {
	var updates []string
	for _, entry := range entries {
		skillCfg := resolveSkillConfigForEntry(cfg, entry)
		if skillCfg == nil {
			continue
		}
		applySkillConfigEnvOverrides(cfg, &updates, skillCfg, entry)
	}
	return func() {
		for _, key := range updates {
			releaseActiveSkillEnvKey(key)
		}
	}
}

func applySkillConfigEnvOverrides(cfg *config.AppConfig, updates *[]string, skillCfg *config.SkillConfigLite, entry core.SkillEntry) {
	if skillCfg == nil {
		return
	}
	envRuntime := infra.NewEnvironmentRuntime(config.EnvironmentConfig{})
	if cfg != nil {
		envRuntime = infra.NewEnvironmentRuntime(cfg.Env)
	}
	primaryEnv := ""
	requiredEnv := map[string]struct{}{}
	if entry.Metadata != nil {
		primaryEnv = strings.TrimSpace(entry.Metadata.PrimaryEnv)
		if entry.Metadata.Requires != nil {
			for _, envName := range entry.Metadata.Requires.Env {
				if trimmed := strings.TrimSpace(envName); trimmed != "" {
					requiredEnv[trimmed] = struct{}{}
				}
			}
		}
	}
	for key, value := range skillCfg.Env {
		envKey := strings.TrimSpace(key)
		envValue, _ := envRuntime.ResolveString(value) // zero value fallback is intentional
		envValue = strings.TrimSpace(envValue)
		if envKey == "" || envValue == "" {
			continue
		}
		if _, ok := requiredEnv[envKey]; !ok && !strings.EqualFold(envKey, primaryEnv) {
			continue
		}
		applySkillEnvValue(updates, envKey, envValue)
	}
	if primaryEnv != "" {
		if value, err := envRuntime.ResolveString(skillCfg.APIKey); err == nil && strings.TrimSpace(value) != "" {
			value = strings.TrimSpace(value)
			applySkillEnvValue(updates, primaryEnv, value)
		}
	}
}

func applySkillEnvValue(updates *[]string, key string, value string) {
	if key == "" || value == "" {
		return
	}
	if !acquireActiveSkillEnvKey(key, value) {
		return
	}
	*updates = append(*updates, key)
}

type SkillsChangeEvent struct {
	WorkspaceDir string
	ChangedPath  string
	Reason       string
}

type skillWatchState struct {
	watcher     *fsnotify.Watcher
	workspace   string
	pathsKey    string
	debounce    time.Duration
	timer       *time.Timer
	pendingPath string
}

var (
	skillsWatchMu       sync.Mutex
	skillsWatchers      = map[string]*skillWatchState{}
	skillsVersions      = map[string]int{}
	globalSkillsVersion int
)

func ensureSkillsWatcher(workspaceDir string, cfg *config.AppConfig) {
	workspaceDir = strings.TrimSpace(workspaceDir)
	if workspaceDir == "" || cfg == nil {
		return
	}
	enabled := true
	if cfg.Skills.Load.Watch != nil {
		enabled = *cfg.Skills.Load.Watch
	}
	debounce := 250 * time.Millisecond
	if cfg.Skills.Load.WatchDebounceMs > 0 {
		debounce = time.Duration(cfg.Skills.Load.WatchDebounceMs) * time.Millisecond
	}
	roots := resolveSkillWatchRoots(workspaceDir, cfg)
	pathsKey := strings.Join(roots, "|")

	skillsWatchMu.Lock()
	defer skillsWatchMu.Unlock()
	existing := skillsWatchers[workspaceDir]
	if !enabled {
		if existing != nil {
			stopSkillWatcher(existing)
			delete(skillsWatchers, workspaceDir)
		}
		return
	}
	if existing != nil && existing.pathsKey == pathsKey && existing.debounce == debounce {
		return
	}
	if existing != nil {
		stopSkillWatcher(existing)
		delete(skillsWatchers, workspaceDir)
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	state := &skillWatchState{
		watcher:   watcher,
		workspace: workspaceDir,
		pathsKey:  pathsKey,
		debounce:  debounce,
	}
	skillsWatchers[workspaceDir] = state
	go runSkillWatcher(state)
	for _, root := range roots {
		addSkillWatchRoot(watcher, root)
	}
}

func stopSkillWatcher(state *skillWatchState) {
	if state.timer != nil {
		state.timer.Stop()
	}
	_ = state.watcher.Close() // best-effort cleanup
}

func resolveSkillWatchRoots(workspaceDir string, cfg *config.AppConfig) []string {
	sources := resolveSkillSources(workspaceDir, &WorkspaceSkillBuildOptions{Config: cfg})
	seen := map[string]struct{}{}
	var roots []string
	for _, source := range sources {
		root := strings.TrimSpace(source.dir)
		if root == "" {
			continue
		}
		root = filepath.Clean(root)
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		roots = append(roots, root)
	}
	return roots
}

func addSkillWatchRoot(watcher *fsnotify.Watcher, root string) {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return
	}
	_ = watcher.Add(root) // best-effort; watch failure is non-critical
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		_ = watcher.Add(filepath.Join(root, entry.Name())) // best-effort; watch failure is non-critical
	}
}

func runSkillWatcher(state *skillWatchState) {
	for {
		select {
		case event, ok := <-state.watcher.Events:
			if !ok {
				return
			}
			handleSkillWatchEvent(state, event)
		case _, ok := <-state.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

func handleSkillWatchEvent(state *skillWatchState, event fsnotify.Event) {
	path := filepath.Clean(event.Name)
	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			_ = state.watcher.Add(path) // best-effort; watch failure is non-critical
		}
	}
	if !shouldBumpSkillsForPath(path) {
		return
	}
	if state.timer != nil {
		state.timer.Stop()
	}
	state.pendingPath = path
	state.timer = time.AfterFunc(state.debounce, func() {
		BumpSkillsSnapshotVersion(state.workspace, state.pendingPath, "watch")
	})
}

func shouldBumpSkillsForPath(path string) bool {
	base := strings.TrimSpace(filepath.Base(path))
	if strings.EqualFold(base, DefaultSkillFilename) {
		return true
	}
	return false
}

func BumpSkillsSnapshotVersion(workspaceDir, changedPath, reason string) int {
	_ = changedPath // unused; reserved for future use
	_ = reason      // unused; reserved for future use
	invalidateSkillsCaches(workspaceDir)
	skillsWatchMu.Lock()
	defer skillsWatchMu.Unlock()
	globalSkillsVersion = nextSkillVersion(globalSkillsVersion)
	if workspaceDir == "" {
		return globalSkillsVersion
	}
	current := skillsVersions[workspaceDir]
	next := nextSkillVersion(current)
	skillsVersions[workspaceDir] = next
	if next > globalSkillsVersion {
		globalSkillsVersion = next
	}
	return next
}

func getSkillsSnapshotVersion(workspaceDir string) int {
	skillsWatchMu.Lock()
	defer skillsWatchMu.Unlock()
	local := skillsVersions[strings.TrimSpace(workspaceDir)]
	if local > globalSkillsVersion {
		return local
	}
	return globalSkillsVersion
}

func nextSkillVersion(current int) int {
	now := int(time.Now().UnixMilli())
	if now <= current {
		return current + 1
	}
	return now
}

func ResetSkillsWatchersForTests() {
	skillsWatchMu.Lock()
	defer skillsWatchMu.Unlock()
	for workspaceDir, state := range skillsWatchers {
		stopSkillWatcher(state)
		delete(skillsWatchers, workspaceDir)
	}
	skillsVersions = map[string]int{}
	globalSkillsVersion = 0
	invalidateSkillsCaches("")

	activeSkillEnvMu.Lock()
	defer activeSkillEnvMu.Unlock()
	for key, entry := range activeSkillEnvEntries {
		if entry == nil || entry.baseline == nil {
			_ = os.Unsetenv(key) // best-effort; env cleanup failure is non-critical
			continue
		}
		_ = os.Setenv(key, *entry.baseline) // best-effort; env restore failure is non-critical
	}
	activeSkillEnvEntries = map[string]*activeSkillEnvEntry{}
}

func acquireActiveSkillEnvKey(key string, value string) bool {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return false
	}
	activeSkillEnvMu.Lock()
	defer activeSkillEnvMu.Unlock()
	if active := activeSkillEnvEntries[key]; active != nil {
		active.count += 1
		if _, ok := os.LookupEnv(key); !ok {
			_ = os.Setenv(key, active.value) // best-effort; env set failure is non-critical
		}
		return true
	}
	if existing, ok := os.LookupEnv(key); ok && strings.TrimSpace(existing) != "" {
		return false
	}
	var baseline *string
	if existing, ok := os.LookupEnv(key); ok {
		copy := existing
		baseline = &copy
	}
	activeSkillEnvEntries[key] = &activeSkillEnvEntry{
		baseline: baseline,
		value:    value,
		count:    1,
	}
	_ = os.Setenv(key, value) // best-effort; env set failure is non-critical
	return true
}

func releaseActiveSkillEnvKey(key string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	activeSkillEnvMu.Lock()
	defer activeSkillEnvMu.Unlock()
	active := activeSkillEnvEntries[key]
	if active == nil {
		return
	}
	active.count -= 1
	if active.count > 0 {
		if _, ok := os.LookupEnv(key); !ok {
			_ = os.Setenv(key, active.value) // best-effort; env set failure is non-critical
		}
		return
	}
	delete(activeSkillEnvEntries, key)
	if active.baseline == nil {
		_ = os.Unsetenv(key) // best-effort; env cleanup failure is non-critical
		return
	}
	_ = os.Setenv(key, *active.baseline) // best-effort; env restore failure is non-critical
}

func resolveSkillConfigForEntry(cfg *config.AppConfig, entry core.SkillEntry) *config.SkillConfigLite {
	if cfg == nil || len(cfg.Skills.Entries) == 0 {
		return nil
	}
	candidates := []string{}
	if entry.Metadata != nil {
		if key := strings.TrimSpace(entry.Metadata.SkillKey); key != "" {
			candidates = append(candidates, key)
		}
	}
	candidates = append(candidates, entry.Name)
	for _, candidate := range candidates {
		if skillCfg := resolveSkillConfigByName(cfg, candidate); skillCfg != nil {
			return skillCfg
		}
	}
	return nil
}

func resolveSkillConfigByName(cfg *config.AppConfig, skillName string) *config.SkillConfigLite {
	if cfg == nil || len(cfg.Skills.Entries) == 0 {
		return nil
	}
	if entry, ok := cfg.Skills.Entries[skillName]; ok {
		copy := entry
		return &copy
	}
	lowered := strings.ToLower(strings.TrimSpace(skillName))
	if entry, ok := cfg.Skills.Entries[lowered]; ok {
		copy := entry
		return &copy
	}
	for key, entry := range cfg.Skills.Entries {
		if strings.EqualFold(strings.TrimSpace(key), skillName) {
			copy := entry
			return &copy
		}
	}
	return nil
}
