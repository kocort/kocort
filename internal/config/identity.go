package config

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
)

// resolveDefaultAgentDir mirrors infra.ResolveDefaultAgentDir without importing
// internal/infra (which would create an import cycle).
func resolveDefaultAgentDir(stateDir, agentID string) string {
	base := strings.TrimSpace(stateDir)
	if base == "" {
		base = ResolveDefaultStateDir()
	}
	return filepath.Join(base, "agents", normalizeAgentID(agentID), "agent")
}

// resolveDefaultWorkspaceDir mirrors infra.ResolveDefaultAgentWorkspaceDirForState
// without importing internal/infra (which would create an import cycle).
func resolveDefaultWorkspaceDir(stateDir, agentID string) string {
	base := strings.TrimSpace(stateDir)
	if base == "" {
		base = ResolveDefaultStateDir()
	}
	normalizedID := normalizeAgentID(agentID)
	if strings.TrimSpace(base) == "" {
		if normalizedID == defaultAgentID {
			return filepath.Join(".kocort", "workspace")
		}
		return filepath.Join(".kocort", "workspace-"+normalizedID)
	}
	if normalizedID == defaultAgentID {
		return filepath.Join(base, "workspace")
	}
	return filepath.Join(base, "workspace-"+normalizedID)
}

// BuildConfiguredAgentIdentity builds an AgentIdentity from config, merging
// agent defaults with per-agent overrides and request-level parameters.
func BuildConfiguredAgentIdentity(
	cfg AppConfig,
	stateDir string,
	agentID string,
	requestedProvider string,
	requestedModel string,
	requestedWorkspace string,
) (core.AgentIdentity, error) {
	normalizedAgentID := normalizeAgentID(agentID)
	if normalizedAgentID == "" {
		normalizedAgentID = defaultAgentID
	}
	defaults := AgentDefaultsConfig{}
	if cfg.Agents.Defaults != nil {
		defaults = *cfg.Agents.Defaults
	}
	entry, found := ResolveConfiguredAgent(cfg, normalizedAgentID)
	provider := normalizeProviderID(requestedProvider)
	model := strings.TrimSpace(requestedModel)
	toolCfg := defaults.Tools
	toolCfg.Elevated = MergeElevatedConfig(cfg.Tools.Elevated, toolCfg.Elevated)
	toolCfg.Sandbox = MergeSandboxConfig(cfg.Tools.Sandbox, toolCfg.Sandbox)
	memoryCfg := defaults.MemorySearch
	subagentCfg := defaults.Subagents
	runtimeCfg := defaults.Runtime
	identityCfg := defaults.Identity
	heartbeatCfg := defaults.Heartbeat
	compactionCfg := defaults.Compaction
	contextPruningCfg := defaults.ContextPruning
	workspace := strings.TrimSpace(defaults.Workspace)
	agentDir := strings.TrimSpace(defaults.AgentDir)
	sandboxEnabled := defaults.SandboxEnabled
	sandboxDirs := append([]string{}, defaults.SandboxDirs...)
	name := normalizedAgentID
	thinkingDefault := strings.TrimSpace(defaults.ThinkingDefault)
	userTimezone := strings.TrimSpace(defaults.UserTimezone)
	timeoutSeconds := defaults.TimeoutSeconds
	skillFilter := append([]string{}, defaults.Skills...)
	modelCfg := defaults.Model
	if found {
		toolCfg = MergeToolPolicyConfig(toolCfg, entry.Tools)
		memoryCfg = MergeMemorySearchConfig(memoryCfg, entry.MemorySearch)
		subagentCfg = MergeSubagentConfig(subagentCfg, entry.Subagents)
		runtimeCfg = MergeRuntimeConfig(runtimeCfg, entry.Runtime)
		identityCfg = MergeIdentityConfig(identityCfg, entry.Identity)
		heartbeatCfg = MergeHeartbeatConfig(heartbeatCfg, entry.Heartbeat)
		compactionCfg = MergeCompactionConfig(compactionCfg, entry.Compaction)
		contextPruningCfg = MergeContextPruningConfig(contextPruningCfg, entry.ContextPruning)
		if trimmed := strings.TrimSpace(entry.Workspace); trimmed != "" {
			workspace = trimmed
		}
		if trimmed := strings.TrimSpace(entry.AgentDir); trimmed != "" {
			agentDir = trimmed
		}
		if len(entry.SandboxDirs) > 0 {
			sandboxDirs = append([]string{}, entry.SandboxDirs...)
		}
		if entry.SandboxEnabled != nil {
			sandboxEnabled = entry.SandboxEnabled
		}
		if trimmed := strings.TrimSpace(entry.Name); trimmed != "" {
			name = trimmed
		}
		if trimmed := strings.TrimSpace(entry.ThinkingDefault); trimmed != "" {
			thinkingDefault = trimmed
		}
		if trimmed := strings.TrimSpace(entry.UserTimezone); trimmed != "" {
			userTimezone = trimmed
		}
		if entry.TimeoutSeconds > 0 {
			timeoutSeconds = entry.TimeoutSeconds
		}
		if len(entry.Skills) > 0 {
			skillFilter = append([]string{}, entry.Skills...)
		}
		if strings.TrimSpace(entry.Model.Primary) != "" || len(entry.Model.Fallbacks) > 0 {
			modelCfg = entry.Model
		}
	}
	// requestedWorkspace is kept for backward compatibility but is now a no-op
	// for the agent directory. It may be used as a sandbox dir override.
	if trimmed := strings.TrimSpace(requestedWorkspace); trimmed != "" {
		if len(sandboxDirs) == 0 {
			sandboxDirs = []string{trimmed}
		}
	}
	if workspace == "" {
		workspace = resolveDefaultWorkspaceDir(stateDir, normalizedAgentID)
	}
	if strings.TrimSpace(agentDir) == "" {
		agentDir = resolveDefaultAgentDir(stateDir, normalizedAgentID)
	}
	if provider == "" || model == "" {
		if ref, ok := parseModelRef(strings.TrimSpace(modelCfg.Primary), provider); ok {
			if provider == "" {
				provider = ref.Provider
			}
			if model == "" {
				model = ref.Model
			}
		}
	}

	// Resolve the model config; if provider/model are missing or stale
	// (e.g. models.json was deleted), fall back to empty provider/model so
	// the runtime can start and the user can configure one via the UI.
	var modelCfgResolved ProviderModelConfig
	if provider != "" && model != "" {
		if resolved, resolveErr := ResolveConfiguredModel(cfg, provider, model); resolveErr == nil {
			modelCfgResolved = resolved
		} else {
			// referenced provider/model no longer exists — clear both
			provider = ""
			model = ""
		}
	}
	toolAllowlist := append([]string{}, toolCfg.Allow...)
	toolAllowlist = append(toolAllowlist, toolCfg.AlsoAllow...)
	toolProfile := strings.TrimSpace(toolCfg.Profile)
	if toolProfile == "" && len(toolCfg.Allow) == 0 {
		toolProfile = "coding"
	}
	loopDetection := MergeToolLoopDetectionConfig(cfg.Tools.LoopDetection, toolCfg.LoopDetection)
	identity := core.AgentIdentity{
		ID:                              normalizedAgentID,
		Name:                            name,
		WorkspaceDir:                    workspace,
		AgentDir:                        agentDir,
		SandboxEnabled:                  sandboxEnabled != nil && *sandboxEnabled,
		SandboxDirs:                     append([]string{}, sandboxDirs...),
		DefaultProvider:                 provider,
		DefaultModel:                    model,
		ModelFallbacks:                  NormalizeModelFallbacks(modelCfg.Fallbacks),
		ThinkingDefault:                 NonEmpty(thinkingDefault, map[bool]string{true: "low", false: "off"}[modelCfgResolved.Reasoning]),
		UserTimezone:                    userTimezone,
		TimeoutSeconds:                  timeoutSeconds,
		ToolProfile:                     toolProfile,
		ToolAllowlist:                   toolAllowlist,
		ToolDenylist:                    append([]string{}, toolCfg.Deny...),
		ToolLoopDetection:               loopDetection,
		SkillFilter:                     append([]string{}, skillFilter...),
		MemoryEnabled:                   memoryCfg.Enabled == nil || *memoryCfg.Enabled,
		MemoryProvider:                  strings.TrimSpace(NonEmpty(memoryCfg.Provider, cfg.Memory.Backend)),
		MemoryFallback:                  strings.TrimSpace(memoryCfg.Fallback),
		MemoryModel:                     strings.TrimSpace(memoryCfg.Model),
		MemorySources:                   append([]string{}, memoryCfg.Sources...),
		MemoryExtraPaths:                append([]string{}, memoryCfg.ExtraPaths...),
		MemoryStorePath:                 strings.TrimSpace(memoryCfg.Store.Path),
		MemoryVectorEnabled:             memoryCfg.Store.Vector.Enabled != nil && *memoryCfg.Store.Vector.Enabled,
		MemoryVectorExtensionPath:       strings.TrimSpace(memoryCfg.Store.Vector.ExtensionPath),
		MemoryCacheEnabled:              memoryCfg.Cache.Enabled != nil && *memoryCfg.Cache.Enabled,
		MemoryCacheMaxEntries:           memoryCfg.Cache.MaxEntries,
		MemorySyncOnSearch:              memoryCfg.Sync.OnSearch != nil && *memoryCfg.Sync.OnSearch,
		MemorySyncOnSessionStart:        memoryCfg.Sync.OnSessionStart != nil && *memoryCfg.Sync.OnSessionStart,
		MemorySyncWatch:                 memoryCfg.Sync.Watch != nil && *memoryCfg.Sync.Watch,
		MemorySyncWatchDebounceMs:       memoryCfg.Sync.WatchDebounceMs,
		MemoryChunkTokens:               memoryCfg.Chunking.Tokens,
		MemoryChunkOverlap:              memoryCfg.Chunking.Overlap,
		MemoryQueryMaxResults:           memoryCfg.Query.MaxResults,
		MemoryQueryMinScore:             memoryCfg.Query.MinScore,
		MemoryHybridEnabled:             memoryCfg.Query.Hybrid.Enabled != nil && *memoryCfg.Query.Hybrid.Enabled,
		MemoryHybridVectorWeight:        memoryCfg.Query.Hybrid.VectorWeight,
		MemoryHybridTextWeight:          memoryCfg.Query.Hybrid.TextWeight,
		MemoryHybridCandidateFactor:     memoryCfg.Query.Hybrid.CandidateMultiplier,
		MemoryCitationsMode:             strings.TrimSpace(cfg.Memory.Citations),
		SubagentAllowAgents:             append([]string{}, subagentCfg.AllowAgents...),
		SubagentModelPrimary:            strings.TrimSpace(subagentCfg.Model.Primary),
		SubagentModelFallbacks:          NormalizeModelFallbacks(subagentCfg.Model.Fallbacks),
		SubagentThinking:                strings.TrimSpace(subagentCfg.Thinking),
		SubagentMaxSpawnDepth:           subagentCfg.MaxSpawnDepth,
		SubagentMaxChildren:             subagentCfg.MaxChildrenPerAgent,
		SubagentArchiveAfterMinutes:     DerefInt(subagentCfg.ArchiveAfterMinutes),
		SubagentTimeoutSeconds:          subagentCfg.TimeoutSeconds,
		SubagentAttachmentsEnabled:      subagentCfg.AttachmentsEnabled == nil || *subagentCfg.AttachmentsEnabled,
		SubagentAttachmentMaxFiles:      subagentCfg.AttachmentMaxFiles,
		SubagentAttachmentMaxFileBytes:  subagentCfg.AttachmentMaxFileBytes,
		SubagentAttachmentMaxTotalBytes: subagentCfg.AttachmentMaxTotalBytes,
		SubagentRetainAttachmentsOnKeep: subagentCfg.RetainAttachmentsOnKeep != nil && *subagentCfg.RetainAttachmentsOnKeep,
	}
	if heartbeatCfg != nil {
		identity.HeartbeatEvery = strings.TrimSpace(heartbeatCfg.Every)
		identity.HeartbeatSession = strings.TrimSpace(heartbeatCfg.Session)
		identity.HeartbeatPrompt = strings.TrimSpace(heartbeatCfg.Prompt)
		identity.HeartbeatTarget = strings.TrimSpace(heartbeatCfg.Target)
		identity.HeartbeatDirectPolicy = strings.TrimSpace(heartbeatCfg.DirectPolicy)
		identity.HeartbeatTo = strings.TrimSpace(heartbeatCfg.To)
		identity.HeartbeatAccountID = strings.TrimSpace(heartbeatCfg.AccountID)
		identity.HeartbeatModel = strings.TrimSpace(heartbeatCfg.Model)
		identity.HeartbeatAckMaxChars = heartbeatCfg.AckMaxChars
		identity.HeartbeatSuppressToolErr = heartbeatCfg.SuppressToolErr != nil && *heartbeatCfg.SuppressToolErr
		identity.HeartbeatLightContext = heartbeatCfg.LightContext != nil && *heartbeatCfg.LightContext
		identity.HeartbeatIsolatedSession = heartbeatCfg.IsolatedSession != nil && *heartbeatCfg.IsolatedSession
		identity.HeartbeatIncludeReasoning = heartbeatCfg.IncludeReasoning != nil && *heartbeatCfg.IncludeReasoning
		if heartbeatCfg.ActiveHours != nil {
			identity.HeartbeatActiveHoursStart = strings.TrimSpace(heartbeatCfg.ActiveHours.Start)
			identity.HeartbeatActiveHoursEnd = strings.TrimSpace(heartbeatCfg.ActiveHours.End)
			identity.HeartbeatActiveHoursTimezone = strings.TrimSpace(heartbeatCfg.ActiveHours.Timezone)
		}
	}
	if compactionCfg != nil {
		identity.CompactionReserveTokensFloor = compactionCfg.ReserveTokensFloor
		if compactionCfg.MemoryFlush != nil {
			identity.MemoryFlushEnabled = compactionCfg.MemoryFlush.Enabled == nil || *compactionCfg.MemoryFlush.Enabled
			identity.MemoryFlushSoftThresholdTokens = compactionCfg.MemoryFlush.SoftThresholdTokens
			identity.MemoryFlushSystemPrompt = strings.TrimSpace(compactionCfg.MemoryFlush.SystemPrompt)
			identity.MemoryFlushPrompt = strings.TrimSpace(compactionCfg.MemoryFlush.Prompt)
		}
	}
	if contextPruningCfg != nil {
		identity.ContextPruningMode = strings.TrimSpace(contextPruningCfg.Mode)
		if ttl, parseErr := ParseDurationWithDefaultMinutes(contextPruningCfg.TTL); parseErr == nil {
			identity.ContextPruningTTL = ttl
		}
		identity.ContextPruningKeepLastAssistants = contextPruningCfg.KeepLastAssistants
		identity.ContextPruningSoftTrimRatio = contextPruningCfg.SoftTrimRatio
		identity.ContextPruningHardClearRatio = contextPruningCfg.HardClearRatio
		identity.ContextPruningMinPrunableToolChars = contextPruningCfg.MinPrunableToolChars
		identity.ContextPruningSoftTrimMaxChars = contextPruningCfg.SoftTrim.MaxChars
		identity.ContextPruningSoftTrimHeadChars = contextPruningCfg.SoftTrim.HeadChars
		identity.ContextPruningSoftTrimTailChars = contextPruningCfg.SoftTrim.TailChars
		identity.ContextPruningAllowTools = append([]string{}, contextPruningCfg.Tools.Allow...)
		identity.ContextPruningDenyTools = append([]string{}, contextPruningCfg.Tools.Deny...)
		if contextPruningCfg.HardClear != nil {
			identity.ContextPruningHardClearEnabled = contextPruningCfg.HardClear.Enabled == nil || *contextPruningCfg.HardClear.Enabled
			identity.ContextPruningHardClearPlaceholder = strings.TrimSpace(contextPruningCfg.HardClear.Placeholder)
		}
		if strings.EqualFold(identity.ContextPruningMode, "cache-ttl") {
			if identity.ContextPruningTTL <= 0 {
				identity.ContextPruningTTL = 5 * time.Minute
			}
			if identity.ContextPruningKeepLastAssistants <= 0 {
				identity.ContextPruningKeepLastAssistants = 3
			}
			if identity.ContextPruningSoftTrimRatio <= 0 {
				identity.ContextPruningSoftTrimRatio = 0.3
			}
			if identity.ContextPruningHardClearRatio <= 0 {
				identity.ContextPruningHardClearRatio = 0.5
			}
			if identity.ContextPruningMinPrunableToolChars <= 0 {
				identity.ContextPruningMinPrunableToolChars = 50000
			}
			if identity.ContextPruningSoftTrimMaxChars <= 0 {
				identity.ContextPruningSoftTrimMaxChars = 4000
			}
			if identity.ContextPruningSoftTrimHeadChars <= 0 {
				identity.ContextPruningSoftTrimHeadChars = 1500
			}
			if identity.ContextPruningSoftTrimTailChars <= 0 {
				identity.ContextPruningSoftTrimTailChars = 1500
			}
			if strings.TrimSpace(identity.ContextPruningHardClearPlaceholder) == "" {
				identity.ContextPruningHardClearPlaceholder = "[Old tool result content cleared]"
			}
			if contextPruningCfg.HardClear == nil || contextPruningCfg.HardClear.Enabled == nil {
				identity.ContextPruningHardClearEnabled = true
			}
		}
	}
	if toolCfg.Elevated != nil {
		identity.ElevatedEnabled = toolCfg.Elevated.Enabled == nil || *toolCfg.Elevated.Enabled
		identity.ElevatedAllowFrom = CloneAllowFromMap(toolCfg.Elevated.AllowFrom)
	}
	if toolCfg.Sandbox != nil {
		identity.SandboxMode = strings.TrimSpace(toolCfg.Sandbox.Mode)
		identity.SandboxWorkspaceAccess = strings.TrimSpace(toolCfg.Sandbox.WorkspaceAccess)
		identity.SandboxSessionVisibility = strings.TrimSpace(toolCfg.Sandbox.SessionToolsVisibility)
		identity.SandboxScope = strings.TrimSpace(toolCfg.Sandbox.Scope)
		identity.SandboxWorkspaceRoot = strings.TrimSpace(toolCfg.Sandbox.WorkspaceRoot)
	}
	if identityCfg != nil {
		identity.Name = NonEmpty(strings.TrimSpace(identityCfg.Name), identity.Name)
		identity.PersonaPrompt = strings.TrimSpace(identityCfg.PersonaPrompt)
		identity.Emoji = strings.TrimSpace(identityCfg.Emoji)
		identity.Theme = strings.TrimSpace(identityCfg.Theme)
		identity.Avatar = strings.TrimSpace(identityCfg.Avatar)
		identity.OwnerLine = strings.TrimSpace(identityCfg.OwnerLine)
		if len(identityCfg.ModelAliases) > 0 {
			identity.ModelAliases = identityCfg.ModelAliases
		}
	}
	// Global model aliases merged (identity-level takes precedence)
	if len(cfg.Models.ModelAliases) > 0 && identity.ModelAliases == nil {
		identity.ModelAliases = cfg.Models.ModelAliases
	} else if len(cfg.Models.ModelAliases) > 0 {
		// identity aliases override global, but fill in missing keys
		for k, v := range cfg.Models.ModelAliases {
			if _, ok := identity.ModelAliases[k]; !ok {
				identity.ModelAliases[k] = v
			}
		}
	}
	if runtimeCfg != nil {
		identity.RuntimeType = strings.TrimSpace(runtimeCfg.Type)
		if runtimeCfg.ACP != nil {
			identity.RuntimeAgent = strings.TrimSpace(runtimeCfg.ACP.Agent)
			identity.RuntimeBackend = normalizeProviderID(runtimeCfg.ACP.Backend)
			identity.RuntimeMode = strings.TrimSpace(runtimeCfg.ACP.Mode)
			identity.RuntimeCwd = strings.TrimSpace(runtimeCfg.ACP.Cwd)
		}
	}
	if strings.EqualFold(identity.RuntimeType, "acp") && identity.RuntimeBackend == "" {
		identity.RuntimeBackend = normalizeProviderID(cfg.ACP.Backend)
	}
	return identity, nil
}

// BuildConfiguredIdentityMap builds a map of all agent identities from config.
func BuildConfiguredIdentityMap(cfg AppConfig, stateDir string) (map[string]core.AgentIdentity, error) {
	agentID := ResolveDefaultConfiguredAgentID(cfg)
	if agentID == "" {
		agentID = defaultAgentID
	}
	defaultIdentity, err := BuildConfiguredAgentIdentity(cfg, stateDir, agentID, "", "", "")
	if err != nil {
		return nil, err
	}
	identityMap := map[string]core.AgentIdentity{}
	if len(cfg.Agents.List) == 0 {
		identityMap[defaultIdentity.ID] = defaultIdentity
		return identityMap, nil
	}
	for _, entry := range cfg.Agents.List {
		nextIdentity, buildErr := BuildConfiguredAgentIdentity(cfg, stateDir, entry.ID, "", "", "")
		if buildErr != nil {
			return nil, buildErr
		}
		identityMap[nextIdentity.ID] = nextIdentity
	}
	if _, ok := identityMap[defaultIdentity.ID]; !ok {
		identityMap[defaultIdentity.ID] = defaultIdentity
	}
	return identityMap, nil
}
