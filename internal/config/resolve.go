package config

import (
	"fmt"
	"strings"

	"github.com/kocort/kocort/internal/core"
)

// SessionFreshnessPolicy describes when a session should be considered stale.
// Mirrors session.SessionFreshnessPolicy to avoid config → session → config cycle.
type SessionFreshnessPolicy struct {
	Mode        string
	AtHour      int
	IdleMinutes int
}

// normalizeProviderID mirrors backend.NormalizeProviderID to avoid
// config → backend → infra → config import cycle.
func normalizeProviderID(provider string) string {
	normalized := strings.TrimSpace(strings.ToLower(provider))
	switch normalized {
	case "z.ai", "z-ai":
		return "zai"
	case "opencode-zen":
		return "opencode"
	case "qwen":
		return "qwen-portal"
	case "kimi-code":
		return "kimi-coding"
	case "bedrock", "aws-bedrock":
		return "amazon-bedrock"
	case "bytedance", "doubao":
		return "volcengine"
	default:
		return normalized
	}
}

// parseModelRef mirrors backend.ParseModelRef to avoid import cycle.
func parseModelRef(raw, defaultProvider string) (core.ModelCandidate, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return core.ModelCandidate{}, false
	}
	slash := strings.Index(trimmed, "/")
	if slash == -1 {
		return core.ModelCandidate{
			Provider: normalizeProviderID(defaultProvider),
			Model:    strings.TrimSpace(trimmed),
		}, true
	}
	provider := strings.TrimSpace(trimmed[:slash])
	model := strings.TrimSpace(trimmed[slash+1:])
	if provider == "" || model == "" {
		return core.ModelCandidate{}, false
	}
	return core.ModelCandidate{
		Provider: normalizeProviderID(provider),
		Model:    model,
	}, true
}

// ResolveConfiguredAgent finds an agent by ID in the config's agent list.
func ResolveConfiguredAgent(cfg AppConfig, agentID string) (AgentConfig, bool) {
	normalized := normalizeAgentID(agentID)
	for _, agent := range cfg.Agents.List {
		if normalizeAgentID(agent.ID) == normalized {
			return agent, true
		}
	}
	return AgentConfig{}, false
}

// ResolveDefaultConfiguredAgentID returns the default agent ID from config.
func ResolveDefaultConfiguredAgentID(cfg AppConfig) string {
	for _, agent := range cfg.Agents.List {
		if agent.Default && normalizeAgentID(agent.ID) != "" {
			return normalizeAgentID(agent.ID)
		}
	}
	if len(cfg.Agents.List) > 0 {
		if normalized := normalizeAgentID(cfg.Agents.List[0].ID); normalized != "" {
			return normalized
		}
	}
	return defaultAgentID
}

// ResolveConfiguredProvider finds a provider by ID in the config.
func ResolveConfiguredProvider(cfg AppConfig, provider string) (ProviderConfig, string, error) {
	provider = normalizeProviderID(provider)
	for key, entry := range cfg.Models.Providers {
		if normalizeProviderID(key) == provider {
			return entry, strings.TrimSpace(key), nil
		}
	}
	return ProviderConfig{}, "", fmt.Errorf("provider %q is not configured", provider)
}

// ResolveConfiguredModel finds a model by ID under a provider.
func ResolveConfiguredModel(cfg AppConfig, provider, model string) (ProviderModelConfig, error) {
	providerCfg, _, err := ResolveConfiguredProvider(cfg, provider)
	if err != nil {
		return ProviderModelConfig{}, err
	}
	model = strings.TrimSpace(model)
	for _, entry := range providerCfg.Models {
		if strings.TrimSpace(entry.ID) == model {
			return entry, nil
		}
	}
	return ProviderModelConfig{}, fmt.Errorf("model %q is not configured for provider %q", model, provider)
}

// ResolveDefaultConfiguredModel returns the first model for a provider.
func ResolveDefaultConfiguredModel(cfg AppConfig, provider string) (ProviderModelConfig, error) {
	providerCfg, _, err := ResolveConfiguredProvider(cfg, provider)
	if err != nil {
		return ProviderModelConfig{}, err
	}
	if len(providerCfg.Models) == 0 {
		return ProviderModelConfig{}, fmt.Errorf("provider %q has no configured models", provider)
	}
	return providerCfg.Models[0], nil
}

// ResolveSessionMainKey returns the configured main session key.
func ResolveSessionMainKey(cfg AppConfig) string {
	mainKey := strings.TrimSpace(cfg.Session.MainKey)
	if mainKey == "" {
		return defaultMainKey
	}
	return strings.ToLower(mainKey)
}

// ResolveSessionDMScope returns the configured DM scope.
func ResolveSessionDMScope(cfg AppConfig) string {
	scope := strings.TrimSpace(strings.ToLower(cfg.Session.DMScope))
	switch scope {
	case "", "main":
		return "main"
	case "per-peer", "per-channel-peer":
		return scope
	default:
		return "main"
	}
}

// ResolveSessionMainKeyForAPI is the API-facing wrapper for ResolveSessionMainKey.
func ResolveSessionMainKeyForAPI(cfg AppConfig) string {
	return ResolveSessionMainKey(cfg)
}

// ResolveSessionResetTriggers returns the configured session reset trigger phrases.
func ResolveSessionResetTriggers(cfg AppConfig) []string {
	if len(cfg.Session.ResetTriggers) == 0 {
		return []string{"/new", "/reset"}
	}
	out := make([]string, 0, len(cfg.Session.ResetTriggers))
	for _, item := range cfg.Session.ResetTriggers {
		if trimmed := strings.TrimSpace(strings.ToLower(item)); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return []string{"/new", "/reset"}
	}
	return out
}

// ResolveSessionResetPolicy resolves the effective session reset policy
// for a given chat type and channel.
func ResolveSessionResetPolicy(cfg AppConfig, chatType core.ChatType, channel string) SessionFreshnessPolicy {
	result := SessionFreshnessPolicy{}
	if cfg.Session.Reset != nil {
		result = SessionFreshnessPolicy{
			Mode:        strings.TrimSpace(cfg.Session.Reset.Mode),
			AtHour:      cfg.Session.Reset.AtHour,
			IdleMinutes: cfg.Session.Reset.IdleMinutes,
		}
	}
	if result.IdleMinutes <= 0 && cfg.Session.IdleMinutes > 0 {
		result.IdleMinutes = cfg.Session.IdleMinutes
	}
	if byType := cfg.Session.ResetByType; byType != nil {
		var override *SessionResetConfig
		switch chatType {
		case core.ChatTypeGroup, core.ChatTypeTopic:
			override = byType.Group
		case core.ChatTypeThread:
			override = byType.Thread
		default:
			if byType.Direct != nil {
				override = byType.Direct
			} else {
				override = byType.DM
			}
		}
		if override != nil {
			if trimmed := strings.TrimSpace(override.Mode); trimmed != "" {
				result.Mode = trimmed
			}
			if override.AtHour != 0 || strings.EqualFold(strings.TrimSpace(override.Mode), "daily") {
				result.AtHour = override.AtHour
			}
			if override.IdleMinutes > 0 {
				result.IdleMinutes = override.IdleMinutes
			}
		}
	}
	if channelOverride, ok := cfg.Session.ResetByChannel[strings.TrimSpace(strings.ToLower(channel))]; ok {
		if trimmed := strings.TrimSpace(channelOverride.Mode); trimmed != "" {
			result.Mode = trimmed
		}
		if channelOverride.AtHour != 0 || strings.EqualFold(strings.TrimSpace(channelOverride.Mode), "daily") {
			result.AtHour = channelOverride.AtHour
		}
		if channelOverride.IdleMinutes > 0 {
			result.IdleMinutes = channelOverride.IdleMinutes
		}
	}
	return result
}

func ResolveSessionParentForkMaxTokens(cfg AppConfig) int {
	if cfg.Session.ParentForkMaxTokens > 0 {
		return cfg.Session.ParentForkMaxTokens
	}
	return 100_000
}
