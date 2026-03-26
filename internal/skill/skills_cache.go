package skill

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
)

type cachedSkillsSnapshot struct {
	snapshot *core.SkillSnapshot
}

type cachedSkillsStatus struct {
	report core.SkillStatusReport
}

var (
	skillsCacheMu       sync.Mutex
	skillsSnapshotCache = map[string]cachedSkillsSnapshot{}
	skillsStatusCache   = map[string]cachedSkillsStatus{}
)

func getCachedSkillsSnapshot(workspaceDir string, version int, skillFilter []string, cfg *config.AppConfig) *core.SkillSnapshot {
	key := buildSkillsCacheKey("snapshot", workspaceDir, version, skillFilter, cfg, nil)
	skillsCacheMu.Lock()
	defer skillsCacheMu.Unlock()
	entry, ok := skillsSnapshotCache[key]
	if !ok {
		return nil
	}
	return entry.snapshot
}

func putCachedSkillsSnapshot(workspaceDir string, version int, skillFilter []string, cfg *config.AppConfig, snapshot *core.SkillSnapshot) {
	if snapshot == nil {
		return
	}
	key := buildSkillsCacheKey("snapshot", workspaceDir, version, skillFilter, cfg, nil)
	skillsCacheMu.Lock()
	defer skillsCacheMu.Unlock()
	skillsSnapshotCache[key] = cachedSkillsSnapshot{snapshot: snapshot}
}

func getCachedSkillsStatus(workspaceDir string, version int, opts *WorkspaceSkillBuildOptions, eligibility *core.SkillEligibilityContext) (core.SkillStatusReport, bool) {
	if opts != nil && len(opts.GetEntries()) > 0 {
		return core.SkillStatusReport{}, false
	}
	if eligibilityHasDynamicPredicates(eligibility) {
		return core.SkillStatusReport{}, false
	}
	key := buildSkillsCacheKey("status", workspaceDir, version, optsSkillFilter(opts), OptsConfig(opts), opts)
	skillsCacheMu.Lock()
	defer skillsCacheMu.Unlock()
	entry, ok := skillsStatusCache[key]
	if !ok {
		return core.SkillStatusReport{}, false
	}
	return entry.report, true
}

func putCachedSkillsStatus(workspaceDir string, version int, opts *WorkspaceSkillBuildOptions, eligibility *core.SkillEligibilityContext, report core.SkillStatusReport) {
	if opts != nil && len(opts.GetEntries()) > 0 {
		return
	}
	if eligibilityHasDynamicPredicates(eligibility) {
		return
	}
	key := buildSkillsCacheKey("status", workspaceDir, version, optsSkillFilter(opts), OptsConfig(opts), opts)
	skillsCacheMu.Lock()
	defer skillsCacheMu.Unlock()
	skillsStatusCache[key] = cachedSkillsStatus{report: report}
}

func invalidateSkillsCaches(workspaceDir string) {
	workspaceDir = normalizeSkillsWorkspaceKey(workspaceDir)
	skillsCacheMu.Lock()
	defer skillsCacheMu.Unlock()
	if workspaceDir == "" {
		skillsSnapshotCache = map[string]cachedSkillsSnapshot{}
		skillsStatusCache = map[string]cachedSkillsStatus{}
		return
	}
	for key := range skillsSnapshotCache {
		if strings.Contains(key, "|workspace="+workspaceDir+"|") {
			delete(skillsSnapshotCache, key)
		}
	}
	for key := range skillsStatusCache {
		if strings.Contains(key, "|workspace="+workspaceDir+"|") {
			delete(skillsStatusCache, key)
		}
	}
}

func buildSkillsCacheKey(kind, workspaceDir string, version int, skillFilter []string, cfg *config.AppConfig, opts *WorkspaceSkillBuildOptions) string {
	parts := []string{
		kind,
		"workspace=" + normalizeSkillsWorkspaceKey(workspaceDir),
		"version=" + strings.TrimSpace(asString(version)),
		"filter=" + strings.Join(normalizeSkillFilterForComparison(skillFilter), ","),
		"config=" + hashSkillsConfig(cfg),
	}
	if opts != nil {
		parts = append(parts,
			"includeDisabled="+strings.TrimSpace(asString(opts.IncludeDisabled)),
			"managed="+normalizeSkillsWorkspaceKey(opts.ManagedSkillsDir),
			"bundled="+normalizeSkillsWorkspaceKey(opts.BundledSkillsDir),
		)
	}
	return strings.Join(parts, "|")
}

func normalizeSkillsWorkspaceKey(workspaceDir string) string {
	workspaceDir = strings.TrimSpace(workspaceDir)
	if workspaceDir == "" {
		return ""
	}
	return filepath.Clean(workspaceDir)
}

func hashSkillsConfig(cfg *config.AppConfig) string {
	if cfg == nil {
		return ""
	}
	payload, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	return string(payload)
}

func eligibilityHasDynamicPredicates(eligibility *core.SkillEligibilityContext) bool {
	if eligibility == nil || eligibility.Remote == nil {
		return false
	}
	return eligibility.Remote.HasBin != nil || eligibility.Remote.HasAnyBin != nil
}
