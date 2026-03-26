package config

import (
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
)

// NonEmpty returns primary if non-empty, otherwise fallback.
func NonEmpty(primary, fallback string) string {
	if primary != "" {
		return primary
	}
	return fallback
}

// NormalizeModelFallbacks trims and filters empty entries from a model fallback list.
func NormalizeModelFallbacks(list []string) []string {
	if len(list) == 0 {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// DerefInt safely dereferences an *int, returning 0 if nil.
func DerefInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

// ParseDurationWithDefaultMinutes parses a duration string, defaulting bare numbers to minutes.
func ParseDurationWithDefaultMinutes(raw string) (time.Duration, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	if strings.ContainsAny(trimmed, "hms") {
		return time.ParseDuration(trimmed)
	}
	return time.ParseDuration(trimmed + "m")
}

// CloneAllowFromMap deep-copies a map[string][]string used for elevated allow-from rules.
func CloneAllowFromMap(input map[string][]string) map[string][]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string][]string, len(input))
	for key, values := range input {
		out[strings.TrimSpace(key)] = append([]string{}, values...)
	}
	return out
}

// MergeToolPolicyConfig merges override tool policy into base.
func MergeToolPolicyConfig(base AgentToolPolicyConfig, override AgentToolPolicyConfig) AgentToolPolicyConfig {
	merged := base
	if len(override.Allow) > 0 {
		merged.Allow = append([]string{}, override.Allow...)
	}
	if len(override.AlsoAllow) > 0 {
		merged.AlsoAllow = append([]string{}, override.AlsoAllow...)
	}
	if len(override.Deny) > 0 {
		merged.Deny = append([]string{}, override.Deny...)
	}
	if strings.TrimSpace(override.Profile) != "" {
		merged.Profile = strings.TrimSpace(override.Profile)
	}
	if override.Elevated != nil {
		merged.Elevated = MergeElevatedConfig(merged.Elevated, override.Elevated)
	}
	if override.Sandbox != nil {
		merged.Sandbox = MergeSandboxConfig(merged.Sandbox, override.Sandbox)
	}
	if override.LoopDetection != nil {
		loopDetection := MergeToolLoopDetectionConfig(merged.LoopDetection, override.LoopDetection)
		merged.LoopDetection = &loopDetection
	}
	return merged
}

// MergeToolLoopDetectionConfig merges override loop detection config into base.
func MergeToolLoopDetectionConfig(base *core.ToolLoopDetectionConfig, override *core.ToolLoopDetectionConfig) core.ToolLoopDetectionConfig {
	var merged core.ToolLoopDetectionConfig
	if base != nil {
		merged = *base
		merged.Detectors = base.Detectors
	}
	if override == nil {
		return merged
	}
	if override.Enabled != nil {
		value := *override.Enabled
		merged.Enabled = &value
	}
	if override.HistorySize > 0 {
		merged.HistorySize = override.HistorySize
	}
	if override.WarningThreshold > 0 {
		merged.WarningThreshold = override.WarningThreshold
	}
	if override.CriticalThreshold > 0 {
		merged.CriticalThreshold = override.CriticalThreshold
	}
	if override.GlobalCircuitBreakerThreshold > 0 {
		merged.GlobalCircuitBreakerThreshold = override.GlobalCircuitBreakerThreshold
	}
	if override.Detectors.GenericRepeat != nil {
		value := *override.Detectors.GenericRepeat
		merged.Detectors.GenericRepeat = &value
	}
	if override.Detectors.KnownPollNoProgress != nil {
		value := *override.Detectors.KnownPollNoProgress
		merged.Detectors.KnownPollNoProgress = &value
	}
	if override.Detectors.PingPong != nil {
		value := *override.Detectors.PingPong
		merged.Detectors.PingPong = &value
	}
	return merged
}

// MergeElevatedConfig merges override elevated config into base.
func MergeElevatedConfig(base *ToolElevatedConfig, override *ToolElevatedConfig) *ToolElevatedConfig {
	if base == nil && override == nil {
		return nil
	}
	var merged ToolElevatedConfig
	if base != nil {
		if base.Enabled != nil {
			value := *base.Enabled
			merged.Enabled = &value
		}
		if len(base.AllowFrom) > 0 {
			merged.AllowFrom = CloneAllowFromMap(base.AllowFrom)
		}
	}
	if override == nil {
		return &merged
	}
	if override.Enabled != nil {
		value := *override.Enabled
		merged.Enabled = &value
	}
	if len(override.AllowFrom) > 0 {
		merged.AllowFrom = CloneAllowFromMap(override.AllowFrom)
	}
	return &merged
}

// MergeSandboxConfig merges override sandbox config into base.
func MergeSandboxConfig(base *ToolSandboxConfig, override *ToolSandboxConfig) *ToolSandboxConfig {
	if base == nil && override == nil {
		return nil
	}
	var merged ToolSandboxConfig
	if base != nil {
		merged = *base
	}
	if override == nil {
		return &merged
	}
	if trimmed := strings.TrimSpace(override.Mode); trimmed != "" {
		merged.Mode = trimmed
	}
	if trimmed := strings.TrimSpace(override.WorkspaceAccess); trimmed != "" {
		merged.WorkspaceAccess = trimmed
	}
	if trimmed := strings.TrimSpace(override.SessionToolsVisibility); trimmed != "" {
		merged.SessionToolsVisibility = trimmed
	}
	if trimmed := strings.TrimSpace(override.Scope); trimmed != "" {
		merged.Scope = trimmed
	}
	if trimmed := strings.TrimSpace(override.WorkspaceRoot); trimmed != "" {
		merged.WorkspaceRoot = trimmed
	}
	return &merged
}

// MergeMemorySearchConfig merges override memory search config into base.
func MergeMemorySearchConfig(base AgentMemorySearchConfig, override AgentMemorySearchConfig) AgentMemorySearchConfig {
	merged := base
	if override.Enabled != nil {
		value := *override.Enabled
		merged.Enabled = &value
	}
	if trimmed := strings.TrimSpace(override.Provider); trimmed != "" {
		merged.Provider = trimmed
	}
	if trimmed := strings.TrimSpace(override.Fallback); trimmed != "" {
		merged.Fallback = trimmed
	}
	if trimmed := strings.TrimSpace(override.Model); trimmed != "" {
		merged.Model = trimmed
	}
	if len(override.Sources) > 0 {
		merged.Sources = append([]string{}, override.Sources...)
	}
	if len(override.ExtraPaths) > 0 {
		merged.ExtraPaths = append([]string{}, override.ExtraPaths...)
	}
	if override.Query.MaxResults > 0 {
		merged.Query.MaxResults = override.Query.MaxResults
	}
	if override.Query.MinScore > 0 {
		merged.Query.MinScore = override.Query.MinScore
	}
	if override.Query.Hybrid.Enabled != nil {
		value := *override.Query.Hybrid.Enabled
		merged.Query.Hybrid.Enabled = &value
	}
	if override.Query.Hybrid.VectorWeight > 0 {
		merged.Query.Hybrid.VectorWeight = override.Query.Hybrid.VectorWeight
	}
	if override.Query.Hybrid.TextWeight > 0 {
		merged.Query.Hybrid.TextWeight = override.Query.Hybrid.TextWeight
	}
	if override.Query.Hybrid.CandidateMultiplier > 0 {
		merged.Query.Hybrid.CandidateMultiplier = override.Query.Hybrid.CandidateMultiplier
	}
	if trimmed := strings.TrimSpace(override.Store.Path); trimmed != "" {
		merged.Store.Path = trimmed
	}
	if override.Store.Vector.Enabled != nil {
		value := *override.Store.Vector.Enabled
		merged.Store.Vector.Enabled = &value
	}
	if trimmed := strings.TrimSpace(override.Store.Vector.ExtensionPath); trimmed != "" {
		merged.Store.Vector.ExtensionPath = trimmed
	}
	if override.Cache.Enabled != nil {
		value := *override.Cache.Enabled
		merged.Cache.Enabled = &value
	}
	if override.Cache.MaxEntries > 0 {
		merged.Cache.MaxEntries = override.Cache.MaxEntries
	}
	if override.Sync.OnSearch != nil {
		value := *override.Sync.OnSearch
		merged.Sync.OnSearch = &value
	}
	if override.Sync.OnSessionStart != nil {
		value := *override.Sync.OnSessionStart
		merged.Sync.OnSessionStart = &value
	}
	if override.Sync.Watch != nil {
		value := *override.Sync.Watch
		merged.Sync.Watch = &value
	}
	if override.Sync.WatchDebounceMs > 0 {
		merged.Sync.WatchDebounceMs = override.Sync.WatchDebounceMs
	}
	if override.Chunking.Tokens > 0 {
		merged.Chunking.Tokens = override.Chunking.Tokens
	}
	if override.Chunking.Overlap > 0 {
		merged.Chunking.Overlap = override.Chunking.Overlap
	}
	return merged
}

// MergeSubagentConfig merges override subagent config into base.
func MergeSubagentConfig(base AgentSubagentConfig, override AgentSubagentConfig) AgentSubagentConfig {
	merged := base
	if len(override.AllowAgents) > 0 {
		merged.AllowAgents = append([]string{}, override.AllowAgents...)
	}
	if strings.TrimSpace(override.Model.Primary) != "" || len(override.Model.Fallbacks) > 0 {
		merged.Model = AgentModelConfig{
			Primary:   strings.TrimSpace(override.Model.Primary),
			Fallbacks: append([]string{}, override.Model.Fallbacks...),
		}
	}
	if strings.TrimSpace(override.Thinking) != "" {
		merged.Thinking = strings.TrimSpace(override.Thinking)
	}
	if override.MaxSpawnDepth > 0 {
		merged.MaxSpawnDepth = override.MaxSpawnDepth
	}
	if override.MaxChildrenPerAgent > 0 {
		merged.MaxChildrenPerAgent = override.MaxChildrenPerAgent
	}
	if override.ArchiveAfterMinutes != nil {
		value := *override.ArchiveAfterMinutes
		merged.ArchiveAfterMinutes = &value
	}
	if override.TimeoutSeconds > 0 {
		merged.TimeoutSeconds = override.TimeoutSeconds
	}
	if override.AttachmentsEnabled != nil {
		value := *override.AttachmentsEnabled
		merged.AttachmentsEnabled = &value
	}
	if override.AttachmentMaxFiles > 0 {
		merged.AttachmentMaxFiles = override.AttachmentMaxFiles
	}
	if override.AttachmentMaxFileBytes > 0 {
		merged.AttachmentMaxFileBytes = override.AttachmentMaxFileBytes
	}
	if override.AttachmentMaxTotalBytes > 0 {
		merged.AttachmentMaxTotalBytes = override.AttachmentMaxTotalBytes
	}
	if override.RetainAttachmentsOnKeep != nil {
		value := *override.RetainAttachmentsOnKeep
		merged.RetainAttachmentsOnKeep = &value
	}
	return merged
}

// MergeRuntimeConfig merges override runtime config into base.
func MergeRuntimeConfig(base *AgentRuntimeConfig, override *AgentRuntimeConfig) *AgentRuntimeConfig {
	if base == nil && override == nil {
		return nil
	}
	var merged AgentRuntimeConfig
	if base != nil {
		merged = *base
		if base.ACP != nil {
			acpCopy := *base.ACP
			merged.ACP = &acpCopy
		}
	}
	if override == nil {
		return &merged
	}
	if trimmed := strings.TrimSpace(override.Type); trimmed != "" {
		merged.Type = trimmed
	}
	if override.ACP != nil {
		if merged.ACP == nil {
			merged.ACP = &AgentRuntimeACPConfig{}
		}
		if trimmed := strings.TrimSpace(override.ACP.Agent); trimmed != "" {
			merged.ACP.Agent = trimmed
		}
		if trimmed := strings.TrimSpace(override.ACP.Backend); trimmed != "" {
			merged.ACP.Backend = trimmed
		}
		if trimmed := strings.TrimSpace(override.ACP.Mode); trimmed != "" {
			merged.ACP.Mode = trimmed
		}
		if trimmed := strings.TrimSpace(override.ACP.Cwd); trimmed != "" {
			merged.ACP.Cwd = trimmed
		}
	}
	return &merged
}

// MergeHeartbeatConfig merges override heartbeat config into base.
func MergeHeartbeatConfig(base *AgentHeartbeatConfig, override *AgentHeartbeatConfig) *AgentHeartbeatConfig {
	if base == nil && override == nil {
		return nil
	}
	var merged AgentHeartbeatConfig
	if base != nil {
		merged = *base
	}
	if override != nil {
		if trimmed := strings.TrimSpace(override.Every); trimmed != "" {
			merged.Every = trimmed
		}
		if trimmed := strings.TrimSpace(override.Prompt); trimmed != "" {
			merged.Prompt = trimmed
		}
		if trimmed := strings.TrimSpace(override.Target); trimmed != "" {
			merged.Target = trimmed
		}
		if trimmed := strings.TrimSpace(override.Model); trimmed != "" {
			merged.Model = trimmed
		}
		if override.AckMaxChars > 0 {
			merged.AckMaxChars = override.AckMaxChars
		}
	}
	return &merged
}

// MergeCompactionConfig merges override compaction config into base.
func MergeCompactionConfig(base *AgentCompactionConfig, override *AgentCompactionConfig) *AgentCompactionConfig {
	if base == nil && override == nil {
		return nil
	}
	var merged AgentCompactionConfig
	if base != nil {
		merged = *base
		if base.MemoryFlush != nil {
			flushCopy := *base.MemoryFlush
			merged.MemoryFlush = &flushCopy
		}
	}
	if override == nil {
		return &merged
	}
	if override.ReserveTokensFloor > 0 {
		merged.ReserveTokensFloor = override.ReserveTokensFloor
	}
	if override.MemoryFlush != nil {
		if merged.MemoryFlush == nil {
			merged.MemoryFlush = &AgentCompactionMemoryFlushConfig{}
		}
		if override.MemoryFlush.Enabled != nil {
			value := *override.MemoryFlush.Enabled
			merged.MemoryFlush.Enabled = &value
		}
		if override.MemoryFlush.SoftThresholdTokens > 0 {
			merged.MemoryFlush.SoftThresholdTokens = override.MemoryFlush.SoftThresholdTokens
		}
		if trimmed := strings.TrimSpace(override.MemoryFlush.SystemPrompt); trimmed != "" {
			merged.MemoryFlush.SystemPrompt = trimmed
		}
		if trimmed := strings.TrimSpace(override.MemoryFlush.Prompt); trimmed != "" {
			merged.MemoryFlush.Prompt = trimmed
		}
	}
	return &merged
}

// MergeContextPruningConfig merges override context pruning config into base.
func MergeContextPruningConfig(base *AgentContextPruningConfig, override *AgentContextPruningConfig) *AgentContextPruningConfig {
	if base == nil && override == nil {
		return nil
	}
	var merged AgentContextPruningConfig
	if base != nil {
		merged = *base
		if base.HardClear != nil {
			hardClearCopy := *base.HardClear
			merged.HardClear = &hardClearCopy
		}
	}
	if override == nil {
		return &merged
	}
	if trimmed := strings.TrimSpace(override.Mode); trimmed != "" {
		merged.Mode = trimmed
	}
	if trimmed := strings.TrimSpace(override.TTL); trimmed != "" {
		merged.TTL = trimmed
	}
	if override.KeepLastAssistants > 0 {
		merged.KeepLastAssistants = override.KeepLastAssistants
	}
	if override.SoftTrimRatio > 0 {
		merged.SoftTrimRatio = override.SoftTrimRatio
	}
	if override.HardClearRatio > 0 {
		merged.HardClearRatio = override.HardClearRatio
	}
	if override.MinPrunableToolChars > 0 {
		merged.MinPrunableToolChars = override.MinPrunableToolChars
	}
	if override.SoftTrim.MaxChars > 0 {
		merged.SoftTrim.MaxChars = override.SoftTrim.MaxChars
	}
	if override.SoftTrim.HeadChars > 0 {
		merged.SoftTrim.HeadChars = override.SoftTrim.HeadChars
	}
	if override.SoftTrim.TailChars > 0 {
		merged.SoftTrim.TailChars = override.SoftTrim.TailChars
	}
	if override.HardClear != nil {
		if merged.HardClear == nil {
			merged.HardClear = &AgentContextPruningHardClearConfig{}
		}
		if override.HardClear.Enabled != nil {
			value := *override.HardClear.Enabled
			merged.HardClear.Enabled = &value
		}
		if trimmed := strings.TrimSpace(override.HardClear.Placeholder); trimmed != "" {
			merged.HardClear.Placeholder = trimmed
		}
	}
	if len(override.Tools.Allow) > 0 {
		merged.Tools.Allow = append([]string{}, override.Tools.Allow...)
	}
	if len(override.Tools.Deny) > 0 {
		merged.Tools.Deny = append([]string{}, override.Tools.Deny...)
	}
	return &merged
}

// MergeIdentityConfig merges override identity config into base.
func MergeIdentityConfig(base *AgentIdentityConfig, override *AgentIdentityConfig) *AgentIdentityConfig {
	if base == nil && override == nil {
		return nil
	}
	var merged AgentIdentityConfig
	if base != nil {
		merged = *base
	}
	if override != nil {
		if trimmed := strings.TrimSpace(override.Name); trimmed != "" {
			merged.Name = trimmed
		}
		if trimmed := strings.TrimSpace(override.PersonaPrompt); trimmed != "" {
			merged.PersonaPrompt = trimmed
		}
		if trimmed := strings.TrimSpace(override.Emoji); trimmed != "" {
			merged.Emoji = trimmed
		}
		if trimmed := strings.TrimSpace(override.Theme); trimmed != "" {
			merged.Theme = trimmed
		}
		if trimmed := strings.TrimSpace(override.Avatar); trimmed != "" {
			merged.Avatar = trimmed
		}
	}
	return &merged
}

// -----------------------------------------------------------------------
// Session helpers — inlined to avoid config → session → config cycle.
// -----------------------------------------------------------------------

const (
	defaultAgentID = "main"
	defaultMainKey = "main"
)

// normalizeAgentID mirrors session.NormalizeAgentID to avoid
// config → session → config import cycle.
func normalizeAgentID(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return defaultAgentID
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-':
			b.WriteRune(r)
		default:
			if b.Len() == 0 || b.String()[b.Len()-1] == '-' {
				continue
			}
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return defaultAgentID
	}
	if len(out) > 64 {
		return out[:64]
	}
	return out
}
