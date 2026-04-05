package config

import (
	"path/filepath"
	"strings"
)

// resolveRelPath resolves a path relative to baseDir if it is not absolute.
// Returns the original value if it is empty or already absolute.
func resolveRelPath(baseDir, raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return trimmed
	}
	if filepath.IsAbs(trimmed) {
		return trimmed
	}
	return filepath.Join(baseDir, trimmed)
}

// resolveRelPaths resolves each path in the slice relative to baseDir.
func resolveRelPaths(baseDir string, paths []string) []string {
	if len(paths) == 0 {
		return paths
	}
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = resolveRelPath(baseDir, p)
	}
	return out
}

// ResolveConfigPaths resolves all relative directory / file paths inside cfg
// so that they become absolute, using configDir as the base directory.
// This ensures that no matter where the process CWD is, config-referenced
// paths always point to the correct location relative to the config root.
//
// Convention:
//   - Most directory fields are stored as relative paths in JSON config and
//     resolved against configDir here.
//   - SandboxDirs is the sole exception: it must be configured with absolute
//     paths and is NOT resolved here.
func ResolveConfigPaths(cfg *AppConfig, configDir string) {
	if strings.TrimSpace(configDir) == "" {
		return
	}
	base := configDir

	// ── StateDir ─────────────────────────────────────────────────────
	cfg.StateDir = resolveRelPath(base, cfg.StateDir)

	// ── Cerebellum ───────────────────────────────────────────────────
	cfg.Cerebellum.ModelsDir = resolveRelPath(base, cfg.Cerebellum.ModelsDir)

	// ── BrainLocal ───────────────────────────────────────────────────
	cfg.BrainLocal.ModelsDir = resolveRelPath(base, cfg.BrainLocal.ModelsDir)

	// ── Logging ──────────────────────────────────────────────────────
	cfg.Logging.File = resolveRelPath(base, cfg.Logging.File)

	// ── Data sources ─────────────────────────────────────────────────
	if cfg.Data.Entries != nil {
		for k, ds := range cfg.Data.Entries {
			ds.Path = resolveRelPath(base, ds.Path)
			cfg.Data.Entries[k] = ds
		}
	}

	// ── Plugins load paths ───────────────────────────────────────────
	cfg.Plugins.Load.Paths = resolveRelPaths(base, cfg.Plugins.Load.Paths)

	// ── Skills extra dirs ────────────────────────────────────────────
	cfg.Skills.Load.ExtraDirs = resolveRelPaths(base, cfg.Skills.Load.ExtraDirs)

	// ── Agent defaults ───────────────────────────────────────────────
	if cfg.Agents.Defaults != nil {
		cfg.Agents.Defaults.Workspace = resolveRelPath(base, cfg.Agents.Defaults.Workspace)
		cfg.Agents.Defaults.AgentDir = resolveRelPath(base, cfg.Agents.Defaults.AgentDir)
		// NOTE: SandboxDirs are intentionally NOT resolved here.
		// They must be configured as absolute paths in the JSON config.
		resolveMemorySearchPaths(base, &cfg.Agents.Defaults.MemorySearch)
	}

	// ── Per-agent overrides ──────────────────────────────────────────
	for i := range cfg.Agents.List {
		a := &cfg.Agents.List[i]
		a.Workspace = resolveRelPath(base, a.Workspace)
		a.AgentDir = resolveRelPath(base, a.AgentDir)
		// NOTE: SandboxDirs are intentionally NOT resolved here.
		// They must be configured as absolute paths in the JSON config.
		resolveMemorySearchPaths(base, &a.MemorySearch)
	}

	// ── Tool sandbox workspace root ──────────────────────────────────
	if cfg.Tools.Sandbox != nil {
		cfg.Tools.Sandbox.WorkspaceRoot = resolveRelPath(base, cfg.Tools.Sandbox.WorkspaceRoot)
	}

	// ── Browser user data dir ──────────────────────────────────────
	cfg.Tools.BrowserUserDataDir = resolveRelPath(base, cfg.Tools.BrowserUserDataDir)

	// ── Memory QMD paths ─────────────────────────────────────────────
	if cfg.Memory.QMD != nil {
		for i := range cfg.Memory.QMD.Paths {
			cfg.Memory.QMD.Paths[i].Path = resolveRelPath(base, cfg.Memory.QMD.Paths[i].Path)
		}
	}
}

// resolveMemorySearchPaths resolves paths within an AgentMemorySearchConfig.
func resolveMemorySearchPaths(base string, ms *AgentMemorySearchConfig) {
	ms.ExtraPaths = resolveRelPaths(base, ms.ExtraPaths)
	ms.Store.Path = resolveRelPath(base, ms.Store.Path)
	ms.Store.Vector.ExtensionPath = resolveRelPath(base, ms.Store.Vector.ExtensionPath)
}

// ---------------------------------------------------------------------------
// Unresolve: convert absolute paths back to relative for persistence
// ---------------------------------------------------------------------------

// unresolveRelPath converts an absolute path back to a relative path based on
// baseDir. If the path is empty, not absolute, or not under baseDir, it is
// returned unchanged. SandboxDirs are never unresolved since they must always
// be stored as absolute paths.
func unresolveRelPath(baseDir, raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || baseDir == "" {
		return trimmed
	}
	if !filepath.IsAbs(trimmed) {
		return trimmed
	}
	rel, err := filepath.Rel(baseDir, trimmed)
	if err != nil {
		return trimmed
	}
	// If the relative path escapes the base (starts with ".."), keep absolute.
	if strings.HasPrefix(rel, "..") {
		return trimmed
	}
	return rel
}

// unresolveRelPaths converts each absolute path in the slice back to relative.
func unresolveRelPaths(baseDir string, paths []string) []string {
	if len(paths) == 0 {
		return paths
	}
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = unresolveRelPath(baseDir, p)
	}
	return out
}

// UnresolveConfigPaths is the inverse of ResolveConfigPaths: it converts
// absolute paths back to relative paths (based on configDir) for persistence.
// This ensures the JSON config files remain portable and human-readable.
//
// SandboxDirs are NOT converted — they must remain absolute.
func UnresolveConfigPaths(cfg *AppConfig, configDir string) {
	if strings.TrimSpace(configDir) == "" {
		return
	}
	base := configDir

	// ── StateDir ─────────────────────────────────────────────────────
	cfg.StateDir = unresolveRelPath(base, cfg.StateDir)

	// ── Cerebellum ───────────────────────────────────────────────────
	cfg.Cerebellum.ModelsDir = unresolveRelPath(base, cfg.Cerebellum.ModelsDir)

	// ── BrainLocal ───────────────────────────────────────────────────
	cfg.BrainLocal.ModelsDir = unresolveRelPath(base, cfg.BrainLocal.ModelsDir)

	// ── Logging ──────────────────────────────────────────────────────
	cfg.Logging.File = unresolveRelPath(base, cfg.Logging.File)

	// ── Data sources ─────────────────────────────────────────────────
	if cfg.Data.Entries != nil {
		for k, ds := range cfg.Data.Entries {
			ds.Path = unresolveRelPath(base, ds.Path)
			cfg.Data.Entries[k] = ds
		}
	}

	// ── Plugins load paths ───────────────────────────────────────────
	cfg.Plugins.Load.Paths = unresolveRelPaths(base, cfg.Plugins.Load.Paths)

	// ── Skills extra dirs ────────────────────────────────────────────
	cfg.Skills.Load.ExtraDirs = unresolveRelPaths(base, cfg.Skills.Load.ExtraDirs)

	// ── Agent defaults ───────────────────────────────────────────────
	if cfg.Agents.Defaults != nil {
		cfg.Agents.Defaults.Workspace = unresolveRelPath(base, cfg.Agents.Defaults.Workspace)
		cfg.Agents.Defaults.AgentDir = unresolveRelPath(base, cfg.Agents.Defaults.AgentDir)
		// NOTE: SandboxDirs are NOT unresolved — they must stay absolute.
		unresolveMemorySearchPaths(base, &cfg.Agents.Defaults.MemorySearch)
	}

	// ── Per-agent overrides ──────────────────────────────────────────
	for i := range cfg.Agents.List {
		a := &cfg.Agents.List[i]
		a.Workspace = unresolveRelPath(base, a.Workspace)
		a.AgentDir = unresolveRelPath(base, a.AgentDir)
		// NOTE: SandboxDirs are NOT unresolved — they must stay absolute.
		unresolveMemorySearchPaths(base, &a.MemorySearch)
	}

	// ── Tool sandbox workspace root ──────────────────────────────────
	if cfg.Tools.Sandbox != nil {
		cfg.Tools.Sandbox.WorkspaceRoot = unresolveRelPath(base, cfg.Tools.Sandbox.WorkspaceRoot)
	}

	// ── Browser user data dir ──────────────────────────────────────
	cfg.Tools.BrowserUserDataDir = unresolveRelPath(base, cfg.Tools.BrowserUserDataDir)

	// ── Memory QMD paths ─────────────────────────────────────────────
	if cfg.Memory.QMD != nil {
		for i := range cfg.Memory.QMD.Paths {
			cfg.Memory.QMD.Paths[i].Path = unresolveRelPath(base, cfg.Memory.QMD.Paths[i].Path)
		}
	}
}

// unresolveMemorySearchPaths converts absolute paths back to relative within
// an AgentMemorySearchConfig.
func unresolveMemorySearchPaths(base string, ms *AgentMemorySearchConfig) {
	ms.ExtraPaths = unresolveRelPaths(base, ms.ExtraPaths)
	ms.Store.Path = unresolveRelPath(base, ms.Store.Path)
	ms.Store.Vector.ExtensionPath = unresolveRelPath(base, ms.Store.Vector.ExtensionPath)
}
