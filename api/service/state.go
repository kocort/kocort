package service

// State building service functions.

import (
	"context"
	"sort"
	"strings"

	"github.com/kocort/kocort/api/presets"
	"github.com/kocort/kocort/api/types"
	"github.com/kocort/kocort/internal/channel/adapter"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/localmodel/catalog"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/skill"
	"github.com/kocort/kocort/runtime"
)

// BuildBrainState builds the brain state response.
func BuildBrainState(ctx context.Context, rt *runtime.Runtime) types.BrainState {
	brainMode := strings.TrimSpace(rt.Config.BrainMode)
	if brainMode == "" {
		brainMode = "cloud"
	}
	return types.BrainState{
		DefaultAgent:      config.ResolveDefaultConfiguredAgentID(rt.Config),
		Agents:            rt.Config.Agents,
		Models:            rt.Config.Models,
		Providers:         SummarizeProviders(ctx, rt),
		SystemPrompt:      ResolveDefaultSystemPrompt(rt.Config),
		ModelRecords:      BuildBrainModelRecords(ctx, rt),
		ModelPresets:      presets.AsTypes(),
		BrainMode:         brainMode,
		BrainLocal:        BuildBrainLocalState(rt),
		Cerebellum:        BuildCerebellumState(rt),
		LocalModelCatalog: buildUnifiedCatalog(),
	}
}

// buildUnifiedCatalog returns the full model catalog with role tags and resolved capabilities.
func buildUnifiedCatalog() []types.CerebellumModelPreset {
	entries := catalog.BuiltinCatalog
	out := make([]types.CerebellumModelPreset, len(entries))
	for i, e := range entries {
		caps := e.Preset.CapabilitiesResolved()
		out[i] = types.CerebellumModelPreset{
			ID:          e.ID,
			ModelID:     e.Preset.ModelID(),
			Name:        e.Name,
			Description: cloneLocalizedText(e.Description),
			Size:        e.Size,
			DownloadURL: e.DownloadURL,
			Filename:    e.Filename,
			Role:        e.Role,
			Capabilities: types.ModelCapabilities{
				Vision:    caps.Vision,
				Audio:     caps.Audio,
				Video:     caps.Video,
				Tools:     caps.Tools,
				Reasoning: caps.Reasoning,
				Coding:    caps.Coding,
			},
		}
		if e.Defaults != nil {
			out[i].Defaults = &types.ModelPresetDefaults{
				Threads:     e.Defaults.Threads,
				ContextSize: e.Defaults.ContextSize,
				GpuLayers:   e.Defaults.GpuLayers,
			}
			if e.Defaults.Sampling != nil {
				out[i].Defaults.Sampling = &types.SamplingParams{
					Temp:           e.Defaults.Sampling.Temp,
					TopP:           e.Defaults.Sampling.TopP,
					TopK:           e.Defaults.Sampling.TopK,
					MinP:           e.Defaults.Sampling.MinP,
					TypicalP:       e.Defaults.Sampling.TypicalP,
					RepeatLastN:    e.Defaults.Sampling.RepeatLastN,
					PenaltyRepeat:  e.Defaults.Sampling.PenaltyRepeat,
					PenaltyFreq:    e.Defaults.Sampling.PenaltyFreq,
					PenaltyPresent: e.Defaults.Sampling.PenaltyPresent,
				}
			}
		}
	}
	return out
}

// BuildCapabilitiesState builds the capabilities state response.
func BuildCapabilitiesState(ctx context.Context, rt *runtime.Runtime) types.CapabilitiesState {
	state := types.CapabilitiesState{
		Config:            rt.Config.Skills,
		Tools:             summarizeTools(rt),
		Plugins:           summarizePlugins(rt.Config),
		HeartbeatsEnabled: rt.HeartbeatsEnabled(),
	}
	if identity, err := resolveDefaultIdentity(ctx, rt); err == nil && strings.TrimSpace(identity.WorkspaceDir) != "" {
		report, reportErr := skill.BuildWorkspaceSkillStatus(identity.WorkspaceDir, &skill.WorkspaceSkillBuildOptions{
			Config: &rt.Config,
		}, &core.SkillEligibilityContext{Remote: skill.GetRemoteSkillEligibility()})
		if reportErr == nil {
			for i := range report.Skills {
				skillKey := strings.TrimSpace(report.Skills[i].SkillKey)
				if skillKey == "" {
					skillKey = strings.TrimSpace(report.Skills[i].Name)
				}
				report.Skills[i].Disabled = !SkillEnabledForAllAgents(rt.Config, skillKey)
				if report.Skills[i].Disabled {
					report.Skills[i].Eligible = false
				}
			}
			state.Skills = report
		}
	}
	return state
}

// BuildSandboxState builds the sandbox state response.
func BuildSandboxState(rt *runtime.Runtime) types.SandboxState {
	return types.SandboxState{
		DefaultAgent: config.ResolveDefaultConfiguredAgentID(rt.Config),
		Agents:       summarizeWorkingDirectoryAgents(rt.Config),
	}
}

// BuildChannelsState builds the channels state response.
func BuildChannelsState(rt *runtime.Runtime) types.ChannelsState {
	state := types.ChannelsState{Config: rt.Config.Channels}
	if rt.Channels != nil {
		state.Integrations = rt.Channels.Snapshot()
	}
	state.Schemas = adapter.AllDriverSchemas()
	return state
}

// BuildEnvironmentState builds the environment state response.
func BuildEnvironmentState(rt *runtime.Runtime) types.EnvironmentState {
	return types.EnvironmentState{
		Environment: rt.Config.Env,
		Resolved:    snapshotEnvironment(rt, false),
		Masked:      snapshotEnvironment(rt, true),
	}
}

// Helper functions

func ResolveDefaultSystemPrompt(cfg config.AppConfig) string {
	agentID := config.ResolveDefaultConfiguredAgentID(cfg)
	if agent := resolveAgentConfig(cfg, agentID); agent != nil && agent.Identity != nil && strings.TrimSpace(agent.Identity.PersonaPrompt) != "" {
		return strings.TrimSpace(agent.Identity.PersonaPrompt)
	}
	if cfg.Agents.Defaults != nil && cfg.Agents.Defaults.Identity != nil {
		return strings.TrimSpace(cfg.Agents.Defaults.Identity.PersonaPrompt)
	}
	return ""
}

func snapshotEnvironment(rt *runtime.Runtime, masked bool) map[string]string {
	if rt == nil || rt.Environment == nil {
		return nil
	}
	return rt.Environment.Snapshot(masked)
}

func summarizeTools(rt *runtime.Runtime) []types.CapabilityTool {
	if rt == nil || rt.Tools == nil {
		return nil
	}
	names := rt.Tools.List()
	out := make([]types.CapabilityTool, 0, len(names))
	for _, tool := range names {
		if tool == nil {
			continue
		}
		meta := rt.Tools.Meta(tool.Name())
		out = append(out, types.CapabilityTool{
			Name:        tool.Name(),
			Description: tool.Description(),
			PluginID:    meta.PluginID,
			Optional:    meta.OptionalPlugin,
			Elevated:    meta.Elevated,
			OwnerOnly:   meta.OwnerOnly,
			Allowed:     ToolEnabledForAllAgents(rt.Config, tool.Name()),
			Meta:        meta,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func summarizePlugins(cfg config.AppConfig) []types.CapabilityPlugin {
	if len(cfg.Plugins.Entries) == 0 {
		return nil
	}
	keys := make([]string, 0, len(cfg.Plugins.Entries))
	for key := range cfg.Plugins.Entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]types.CapabilityPlugin, 0, len(keys))
	for _, key := range keys {
		entry := cfg.Plugins.Entries[key]
		enabled := entry.Enabled == nil || *entry.Enabled
		out = append(out, types.CapabilityPlugin{ID: key, Enabled: enabled})
	}
	return out
}

func summarizeWorkingDirectoryAgents(cfg config.AppConfig) []types.AgentWorkdirSnapshot {
	keys := make([]string, 0)
	if cfg.Agents.Defaults != nil {
		keys = append(keys, config.ResolveDefaultConfiguredAgentID(cfg))
	}
	for _, agent := range cfg.Agents.List {
		if normalized := session.NormalizeAgentID(agent.ID); normalized != "" {
			keys = append(keys, normalized)
		}
	}
	seen := map[string]struct{}{}
	out := make([]types.AgentWorkdirSnapshot, 0, len(keys))
	for _, key := range keys {
		key = session.NormalizeAgentID(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		sandboxEnabled, sandboxDirs := resolveAgentSandboxConfig(cfg, key)
		out = append(out, types.AgentWorkdirSnapshot{
			AgentID:        key,
			WorkspaceDir:   resolveAgentWorkspace(cfg, key),
			AgentDir:       resolveAgentDir(cfg, key),
			SandboxEnabled: sandboxEnabled,
			SandboxDirs:    sandboxDirs,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	return out
}

func resolveAgentWorkspace(cfg config.AppConfig, agentID string) string {
	agent := resolveAgentConfig(cfg, agentID)
	if agent != nil && strings.TrimSpace(agent.Workspace) != "" {
		return strings.TrimSpace(agent.Workspace)
	}
	if cfg.Agents.Defaults != nil {
		return strings.TrimSpace(cfg.Agents.Defaults.Workspace)
	}
	return ""
}

func resolveAgentDir(cfg config.AppConfig, agentID string) string {
	agent := resolveAgentConfig(cfg, agentID)
	if agent != nil && strings.TrimSpace(agent.AgentDir) != "" {
		return strings.TrimSpace(agent.AgentDir)
	}
	if cfg.Agents.Defaults != nil {
		return strings.TrimSpace(cfg.Agents.Defaults.AgentDir)
	}
	return ""
}

func resolveAgentSandboxConfig(cfg config.AppConfig, agentID string) (*bool, []string) {
	agent := resolveAgentConfig(cfg, agentID)
	if agent != nil {
		if agent.SandboxEnabled != nil || len(agent.SandboxDirs) > 0 {
			return agent.SandboxEnabled, agent.SandboxDirs
		}
	}
	if cfg.Agents.Defaults != nil {
		return cfg.Agents.Defaults.SandboxEnabled, cfg.Agents.Defaults.SandboxDirs
	}
	return nil, nil
}

func resolveDefaultIdentity(ctx context.Context, rt *runtime.Runtime) (core.AgentIdentity, error) {
	agentID := config.ResolveDefaultConfiguredAgentID(rt.Config)
	return rt.Identities.Resolve(ctx, agentID)
}

// ResolveDefaultIdentityPublic resolves the default agent identity. Exported for use by handlers.
func ResolveDefaultIdentityPublic(ctx context.Context, rt *runtime.Runtime) (core.AgentIdentity, error) {
	return resolveDefaultIdentity(ctx, rt)
}

func SkillEnabledForAllAgents(cfg config.AppConfig, skillKey string) bool {
	skillKey = strings.TrimSpace(skillKey)
	if skillKey == "" {
		return false
	}
	if entry, ok := cfg.Skills.Entries[skillKey]; ok && entry.Enabled != nil && !*entry.Enabled {
		return false
	}
	agentIDs := effectiveAgentIDs(cfg)
	if len(agentIDs) == 0 {
		return false
	}
	for _, agentID := range agentIDs {
		if _, ok := effectiveAgentSkillSet(cfg, agentID)[skillKey]; !ok {
			return false
		}
	}
	return true
}

func effectiveAgentIDs(cfg config.AppConfig) []string {
	seen := map[string]struct{}{}
	var ids []string
	defaultID := config.ResolveDefaultConfiguredAgentID(cfg)
	if defaultID != "" {
		seen[defaultID] = struct{}{}
		ids = append(ids, defaultID)
	}
	for _, agent := range cfg.Agents.List {
		id := session.NormalizeAgentID(agent.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func effectiveAgentSkillSet(cfg config.AppConfig, agentID string) map[string]struct{} {
	out := map[string]struct{}{}
	if cfg.Agents.Defaults != nil {
		for _, skill := range cfg.Agents.Defaults.Skills {
			if normalized := strings.TrimSpace(skill); normalized != "" {
				out[normalized] = struct{}{}
			}
		}
	}
	if agent := resolveAgentConfig(cfg, agentID); agent != nil {
		for _, skill := range agent.Skills {
			if normalized := strings.TrimSpace(skill); normalized != "" {
				out[normalized] = struct{}{}
			}
		}
	}
	return out
}

// ApplySkillToggles merges skill entries into the config and syncs agents' skills
// lists. When a skill is enabled it is added to every agent; when disabled it is
// removed.
func ApplySkillToggles(cfg *config.AppConfig, incoming *config.SkillsConfig) {
	if incoming == nil || len(incoming.Entries) == 0 {
		return
	}
	if cfg.Skills.Entries == nil {
		cfg.Skills.Entries = make(map[string]config.SkillConfigLite)
	}
	for key, entry := range incoming.Entries {
		cfg.Skills.Entries[key] = entry

		enabled := entry.Enabled == nil || *entry.Enabled
		if enabled {
			if cfg.Agents.Defaults != nil {
				cfg.Agents.Defaults.Skills = addSkillUnique(cfg.Agents.Defaults.Skills, key)
			}
			for i := range cfg.Agents.List {
				cfg.Agents.List[i].Skills = addSkillUnique(cfg.Agents.List[i].Skills, key)
			}
		} else {
			if cfg.Agents.Defaults != nil {
				cfg.Agents.Defaults.Skills = removeSkill(cfg.Agents.Defaults.Skills, key)
			}
			for i := range cfg.Agents.List {
				cfg.Agents.List[i].Skills = removeSkill(cfg.Agents.List[i].Skills, key)
			}
		}
	}
}

func addSkillUnique(list []string, skill string) []string {
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(skill)) {
			return list
		}
	}
	return append(list, skill)
}

func removeSkill(list []string, skill string) []string {
	var out []string
	for _, item := range list {
		if !strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(skill)) {
			out = append(out, item)
		}
	}
	return out
}

// ApplyToolToggles updates the config to enable/disable tools across all agents.
// When a tool is enabled, it is removed from Deny and added to AlsoAllow
// (so that tools outside the active profile become reachable).
// When a tool is disabled, it is added to Deny and removed from AlsoAllow.
func ApplyToolToggles(cfg *config.AppConfig, toggles map[string]bool) {
	for toolName, enabled := range toggles {
		normalized := normalizeAPIToolName(toolName)
		if normalized == "" {
			continue
		}
		if enabled {
			// Remove from Deny and add to AlsoAllow everywhere.
			if cfg.Agents.Defaults != nil {
				cfg.Agents.Defaults.Tools.Deny = removeTool(cfg.Agents.Defaults.Tools.Deny, normalized)
				cfg.Agents.Defaults.Tools.AlsoAllow = addToolUnique(cfg.Agents.Defaults.Tools.AlsoAllow, normalized)
			}
			for i := range cfg.Agents.List {
				cfg.Agents.List[i].Tools.Deny = removeTool(cfg.Agents.List[i].Tools.Deny, normalized)
				cfg.Agents.List[i].Tools.AlsoAllow = addToolUnique(cfg.Agents.List[i].Tools.AlsoAllow, normalized)
			}
		} else {
			// Add to Deny and remove from AlsoAllow everywhere.
			if cfg.Agents.Defaults != nil {
				cfg.Agents.Defaults.Tools.Deny = addToolUnique(cfg.Agents.Defaults.Tools.Deny, normalized)
				cfg.Agents.Defaults.Tools.AlsoAllow = removeTool(cfg.Agents.Defaults.Tools.AlsoAllow, normalized)
			}
			for i := range cfg.Agents.List {
				cfg.Agents.List[i].Tools.Deny = addToolUnique(cfg.Agents.List[i].Tools.Deny, normalized)
				cfg.Agents.List[i].Tools.AlsoAllow = removeTool(cfg.Agents.List[i].Tools.AlsoAllow, normalized)
			}
		}
	}
}

// removeTool removes all occurrences of toolName from the list.
func removeTool(list []string, toolName string) []string {
	var out []string
	for _, item := range list {
		if normalizeAPIToolName(item) != toolName {
			out = append(out, item)
		}
	}
	return out
}

// addToolUnique adds toolName to the list if not already present.
func addToolUnique(list []string, toolName string) []string {
	for _, item := range list {
		if normalizeAPIToolName(item) == toolName {
			return list
		}
	}
	return append(list, toolName)
}

func ToolEnabledForAllAgents(cfg config.AppConfig, toolName string) bool {
	normalized := normalizeAPIToolName(toolName)
	if normalized == "" {
		return false
	}
	agentIDs := effectiveAgentIDs(cfg)
	if len(agentIDs) == 0 {
		return false
	}
	for _, agentID := range agentIDs {
		policy := effectiveAgentToolPolicy(cfg, agentID)
		if toolDeniedByPolicy(policy, normalized) {
			return false
		}
		if !toolAllowedByPolicy(policy, normalized) {
			return false
		}
	}
	return true
}
