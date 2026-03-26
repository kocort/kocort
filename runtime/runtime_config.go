package runtime

import (
	"strings"

	"github.com/kocort/kocort/internal/config"
)

func ResolveConfiguredProviderWithEnvironment(cfg config.AppConfig, environment EnvironmentResolver, provider string) (config.ProviderConfig, string, error) {
	entry, key, err := config.ResolveConfiguredProvider(cfg, provider)
	if err != nil {
		return config.ProviderConfig{}, "", err
	}
	if environment == nil {
		return entry, key, nil
	}
	if resolved, resolveErr := environment.ResolveString(entry.BaseURL); resolveErr == nil && strings.TrimSpace(resolved) != "" {
		entry.BaseURL = strings.TrimSpace(resolved)
	}
	if resolved, resolveErr := environment.ResolveString(entry.APIKey); resolveErr == nil && strings.TrimSpace(resolved) != "" {
		entry.APIKey = strings.TrimSpace(resolved)
	}
	if entry.Command != nil {
		if resolvedEnv, resolveErr := environment.ResolveMap(entry.Command.Env); resolveErr == nil && len(resolvedEnv) > 0 {
			entry.Command.Env = resolvedEnv
		}
		if resolved, resolveErr := environment.ResolveString(entry.Command.WorkingDir); resolveErr == nil && strings.TrimSpace(resolved) != "" {
			entry.Command.WorkingDir = strings.TrimSpace(resolved)
		}
	}
	return entry, key, nil
}

// NewRuntimeFromConfig constructs a fully wired Runtime from config.
// This is a convenience wrapper around RuntimeBuilder.
func NewRuntimeFromConfig(cfg config.AppConfig, params config.RuntimeConfigParams) (*Runtime, error) {
	return NewRuntimeBuilder(cfg, params).Build()
}
