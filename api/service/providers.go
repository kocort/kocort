package service

// Provider summarization and tool policy helpers.

import (
	"context"
	"sort"
	"strings"

	"github.com/kocort/kocort/internal/backend"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/runtime"
)

// SummarizeProviders builds provider health summaries.
func SummarizeProviders(ctx context.Context, rt *runtime.Runtime) []core.ProviderHealthSummary {
	if rt == nil || len(rt.Config.Models.Providers) == 0 {
		return nil
	}
	keys := make([]string, 0, len(rt.Config.Models.Providers))
	for key := range rt.Config.Models.Providers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]core.ProviderHealthSummary, 0, len(keys))
	for _, key := range keys {
		summary := core.ProviderHealthSummary{Provider: key, Configured: true}
		provider, _, err := runtime.ResolveConfiguredProviderWithEnvironment(rt.Config, rt.Environment, key)
		if err != nil {
			summary.LastError = err.Error()
			out = append(out, summary)
			continue
		}
		summary.BackendKind = backend.BackendKindForProvider(rt.Config, key)
		summary.ModelCount = len(provider.Models)
		summary.Ready = strings.TrimSpace(provider.BaseURL) != ""
		if provider.Command != nil && strings.TrimSpace(provider.Command.Command) != "" {
			summary.Ready = true
		}
		switch strings.TrimSpace(provider.API) {
		case "", "openai-completions", "anthropic-messages":
			summary.Ready = summary.Ready && strings.TrimSpace(provider.APIKey) != ""
		}
		if !summary.Ready && summary.LastError == "" {
			summary.LastError = "provider is not fully configured"
		}
		out = append(out, summary)
	}
	return out
}

// Tool policy helpers

var apiCoreToolGroups = map[string][]string{
	"group:sessions": {"session_status", "sessions_history", "sessions_list", "sessions_send", "sessions_spawn", "subagents"},
	"group:agents":   {"sessions_spawn", "subagents"},
	"group:kocort":   {"cron", "exec", "memory_get", "memory_search", "session_status", "sessions_history", "sessions_list", "sessions_send", "sessions_spawn", "subagents"},
	"group:plugins":  {},
}

var apiCoreToolProfiles = map[string][]string{
	"minimal":   {"session_status"},
	"coding":    {"agents_list", "apply_patch", "browser", "cron", "edit", "exec", "find", "gateway", "grep", "image", "ls", "memory_get", "memory_search", "message", "process", "read", "session_status", "sessions_history", "sessions_list", "sessions_send", "sessions_spawn", "subagents", "web_fetch", "web_search", "write"},
	"messaging": {"cron", "message", "process", "session_status", "sessions_history", "sessions_list", "sessions_send"},
	"full":      {},
}

func effectiveAgentToolPolicy(cfg config.AppConfig, agentID string) config.AgentToolPolicyConfig {
	var policy config.AgentToolPolicyConfig
	if cfg.Agents.Defaults != nil {
		policy = cfg.Agents.Defaults.Tools
	}
	if agent := resolveAgentConfig(cfg, agentID); agent != nil {
		if len(agent.Tools.Allow) > 0 {
			policy.Allow = append([]string{}, agent.Tools.Allow...)
		}
		if len(agent.Tools.AlsoAllow) > 0 {
			policy.AlsoAllow = append([]string{}, agent.Tools.AlsoAllow...)
		}
		if len(agent.Tools.Deny) > 0 {
			policy.Deny = append([]string{}, agent.Tools.Deny...)
		}
		if strings.TrimSpace(agent.Tools.Profile) != "" {
			policy.Profile = agent.Tools.Profile
		}
		if agent.Tools.Elevated != nil {
			policy.Elevated = agent.Tools.Elevated
		}
		if agent.Tools.Sandbox != nil {
			policy.Sandbox = agent.Tools.Sandbox
		}
	}
	// Default to "coding" profile when no explicit Allow list is set.
	// AlsoAllow/Deny (used by UI tool toggles) are additive/subtractive
	// and should work alongside the profile, not replace it.
	if strings.TrimSpace(policy.Profile) == "" && len(policy.Allow) == 0 {
		policy.Profile = "coding"
	}
	return policy
}

func toolDeniedByPolicy(policy config.AgentToolPolicyConfig, toolName string) bool {
	for _, item := range expandAPIToolEntries(policy.Deny) {
		if item == toolName {
			return true
		}
	}
	return false
}

func toolAllowedByPolicy(policy config.AgentToolPolicyConfig, toolName string) bool {
	if toolDeniedByPolicy(policy, toolName) {
		return false
	}
	allow := expandAPIToolEntries(policy.Allow)
	allow = append(allow, expandAPIToolEntries(policy.AlsoAllow)...)
	if profileAllow, ok := resolveAPIToolProfilePolicy(policy.Profile); ok {
		allow = append(allow, expandAPIToolEntries(profileAllow)...)
	}
	if len(allow) == 0 {
		return true
	}
	for _, item := range allow {
		if item == toolName {
			return true
		}
		if item == "exec" && toolName == "apply_patch" {
			return true
		}
	}
	return false
}

func expandAPIToolEntries(list []string) []string {
	if len(list) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, raw := range list {
		entry := normalizeAPIToolName(raw)
		if entry == "" {
			continue
		}
		if group, ok := apiCoreToolGroups[entry]; ok {
			for _, tool := range group {
				tool = normalizeAPIToolName(tool)
				if tool == "" {
					continue
				}
				if _, exists := seen[tool]; exists {
					continue
				}
				seen[tool] = struct{}{}
				out = append(out, tool)
			}
			continue
		}
		if _, exists := seen[entry]; exists {
			continue
		}
		seen[entry] = struct{}{}
		out = append(out, entry)
	}
	return out
}

func resolveAPIToolProfilePolicy(profile string) ([]string, bool) {
	allow, ok := apiCoreToolProfiles[strings.ToLower(strings.TrimSpace(profile))]
	if !ok {
		return nil, false
	}
	return append([]string{}, allow...), true
}

func normalizeAPIToolName(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	switch normalized {
	case "bash":
		return "exec"
	case "apply-patch":
		return "apply_patch"
	default:
		return normalized
	}
}
