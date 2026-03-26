package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadAppConfig loads an AppConfig from a single JSON file.
func LoadAppConfig(path string) (AppConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return AppConfig{}, err
	}
	var cfg AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return AppConfig{}, err
	}
	return cfg, nil
}

// resolveUserConfigDir returns the platform-specific user config directory
// for kocort using the Go standard library.
func resolveUserConfigDir() string {
	if dir, err := os.UserConfigDir(); err == nil && strings.TrimSpace(dir) != "" {
		return filepath.Join(dir, "kocort")
	}
	return filepath.Join(".kocort")
}

// ResolveDefaultConfigDir resolves the default config directory for CLI mode.
// Priority:
//  1. KOCORT_CONFIG_DIR env var (explicit override)
//  2. PWD/.kocort if it exists
//  3. ~/.kocort if it exists
//  4. PWD/.kocort (default when neither exists; created on first write)
func ResolveDefaultConfigDir() string {
	if override := strings.TrimSpace(os.Getenv("KOCORT_CONFIG_DIR")); override != "" {
		return override
	}
	cwd, cwdErr := os.Getwd()
	// Check PWD/.kocort
	if cwdErr == nil {
		candidate := filepath.Join(cwd, ".kocort")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	// Check ~/.kocort
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		candidate := filepath.Join(home, ".kocort")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	// Neither exists → default to PWD/.kocort
	if cwdErr == nil {
		return filepath.Join(cwd, ".kocort")
	}
	return resolveUserConfigDir()
}

// ResolveDesktopConfigDir resolves the config directory for desktop app mode.
// Desktop apps always use ~/.kocort as the config directory so that config
// files are in a predictable, user-visible location across all platforms.
// Priority: KOCORT_CONFIG_DIR env → ~/.kocort.
func ResolveDesktopConfigDir() string {
	if override := strings.TrimSpace(os.Getenv("KOCORT_CONFIG_DIR")); override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		// Absolute fallback: use platform-specific dir if home is unavailable
		return resolveUserConfigDir()
	}
	return filepath.Join(home, ".kocort")
}

// ResolveDefaultStateDir resolves the default state directory.
// The state directory stores sessions, transcripts, audit logs, etc.
// Priority: KOCORT_STATE_DIR env → stateDir field in config → configDir (same as config).
func ResolveDefaultStateDir() string {
	if override := strings.TrimSpace(os.Getenv("KOCORT_STATE_DIR")); override != "" {
		return override
	}
	// Default: same as config dir (will be overridden if config has stateDir)
	return ResolveDefaultConfigDir()
}

// ResolveStateDirFromConfig returns the effective state directory based on
// config content. If the config specifies a stateDir, use it; otherwise
// fall back to configDir.
func ResolveStateDirFromConfig(cfg AppConfig, configDir string) string {
	if override := strings.TrimSpace(os.Getenv("KOCORT_STATE_DIR")); override != "" {
		return override
	}
	if trimmed := strings.TrimSpace(cfg.StateDir); trimmed != "" {
		if filepath.IsAbs(trimmed) {
			return trimmed
		}
		if configDir != "" {
			return filepath.Join(configDir, trimmed)
		}
		return trimmed
	}
	if configDir != "" {
		return configDir
	}
	return ResolveDefaultConfigDir()
}

// ResolveEffectiveConfigLoadOptions fills in defaults for empty config load options.
func ResolveEffectiveConfigLoadOptions(opts ConfigLoadOptions) ConfigLoadOptions {
	resolved := opts
	configDir := strings.TrimSpace(opts.ConfigDir)
	if configDir == "" {
		configDir = ResolveDefaultConfigDir()
	}
	if strings.TrimSpace(resolved.ConfigPath) == "" {
		resolved.ConfigPath = filepath.Join(configDir, "kocort.json")
	}
	if strings.TrimSpace(resolved.ModelsConfigPath) == "" {
		resolved.ModelsConfigPath = filepath.Join(configDir, "models.json")
	}
	if strings.TrimSpace(resolved.ChannelsConfigPath) == "" {
		resolved.ChannelsConfigPath = filepath.Join(configDir, "channels.json")
	}
	resolved.ConfigDir = configDir
	return resolved
}

// LoadOptionalConfigMap loads a JSON config file; returns nil,false,nil if not required and missing.
func LoadOptionalConfigMap(path string, required bool) (map[string]any, bool, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, false, nil
	}
	data, err := os.ReadFile(trimmed)
	if err != nil {
		if !required && os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	parsed, err := ParseJSONConfigMap(data)
	if err != nil {
		return nil, false, fmt.Errorf("parse config %q: %w", trimmed, err)
	}
	return parsed, true, nil
}

// ParseJSONConfigMap parses JSON data into a map.
func ParseJSONConfigMap(data []byte) (map[string]any, error) {
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	if parsed == nil {
		parsed = map[string]any{}
	}
	return parsed, nil
}

// DeepMergeJSONMaps deep-merges overlay into base.
func DeepMergeJSONMaps(base map[string]any, overlay map[string]any) map[string]any {
	if base == nil {
		base = map[string]any{}
	}
	for key, value := range overlay {
		nextMap, nextIsMap := value.(map[string]any)
		currentMap, currentIsMap := base[key].(map[string]any)
		if nextIsMap && currentIsMap {
			base[key] = DeepMergeJSONMaps(currentMap, nextMap)
			continue
		}
		base[key] = value
	}
	return base
}

// IsZeroJSON returns true if the marshaled value is an empty/zero JSON value.
func IsZeroJSON(value any) bool {
	raw, err := json.Marshal(value)
	if err != nil {
		return false
	}
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "null" || trimmed == "{}" || trimmed == "[]" || trimmed == `""` || trimmed == "false" || trimmed == "0"
}
