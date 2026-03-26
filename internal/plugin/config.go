// Package plugin — configuration and policy helpers for runtime plugins.
package plugin

import (
	"os"
	"strings"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/tool"
)

// PluginBlockedByConfig returns true if the plugin is explicitly denied
// or disabled in the configuration.
func PluginBlockedByConfig(cfg config.PluginsConfig, pluginID string) bool {
	for _, entry := range cfg.Deny {
		if tool.NormalizeToolPolicyName(entry) == pluginID {
			return true
		}
	}
	if cfg.Entries != nil {
		if entry, ok := cfg.Entries[pluginID]; ok && entry.Enabled != nil && !*entry.Enabled {
			return true
		}
	}
	return false
}

// PluginEnabledByConfig returns true if the plugin passes allow/deny checks.
func PluginEnabledByConfig(cfg config.PluginsConfig, pluginID string) bool {
	if cfg.Entries != nil {
		if entry, ok := cfg.Entries[pluginID]; ok && entry.Enabled != nil {
			return *entry.Enabled
		}
	}
	if len(cfg.Allow) > 0 {
		for _, entry := range cfg.Allow {
			if tool.NormalizeToolPolicyName(entry) == pluginID {
				return true
			}
		}
		return false
	}
	return true
}

// PluginToolExecutableByConfig returns true if a tool from the given plugin
// is allowed to execute under the current configuration.
func PluginToolExecutableByConfig(cfg config.PluginsConfig, pluginID string) bool {
	pluginID = tool.NormalizeToolPolicyName(pluginID)
	if pluginID == "" {
		return true
	}
	if PluginBlockedByConfig(cfg, pluginID) {
		return false
	}
	return PluginEnabledByConfig(cfg, pluginID)
}

// ApplyPluginEnvOverrides sets environment variables specified in the plugin
// configuration and returns a restore function that undoes the changes.
func ApplyPluginEnvOverrides(appCfg config.AppConfig, pluginID string) func() {
	cfg := appCfg.Plugins
	if cfg.Entries == nil {
		return func() {}
	}
	entry, ok := cfg.Entries[strings.TrimSpace(pluginID)]
	if !ok {
		return func() {}
	}
	restoreFns := make([]func(), 0, len(entry.Env)+1)
	envRuntime := infra.NewEnvironmentRuntime(appCfg.Env)
	if trimmed, _ := envRuntime.ResolveString(entry.APIKey); strings.TrimSpace(trimmed) != "" { // zero value fallback is intentional
		restoreFns = append(restoreFns, ApplyEnvOverride("KOCORT_PLUGIN_API_KEY", trimmed))
	}
	for key, value := range entry.Env {
		resolved, _ := envRuntime.ResolveString(value) // zero value fallback is intentional
		restoreFns = append(restoreFns, ApplyEnvOverride(strings.TrimSpace(key), resolved))
	}
	return func() {
		for i := len(restoreFns) - 1; i >= 0; i-- {
			restoreFns[i]()
		}
	}
}

// ApplyEnvOverride sets a single environment variable and returns a function
// that restores the previous value. If the variable already has a non-empty
// value it is left unchanged.
func ApplyEnvOverride(key string, value string) func() {
	key = strings.TrimSpace(key)
	if key == "" {
		return func() {}
	}
	previous, existed := os.LookupEnv(key)
	if !existed || strings.TrimSpace(previous) == "" {
		_ = os.Setenv(key, value) // best-effort; env set failure is non-critical
		return func() {
			_ = os.Unsetenv(key) // best-effort; env cleanup failure is non-critical
		}
	}
	return func() {}
}
