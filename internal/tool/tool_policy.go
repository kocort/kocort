// Runtime-bound tool policy functions (using AgentRunContext, SandboxContext).
// Pure policy logic is canonical in kocort/internal/tool.
package tool

import (
	"strings"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"
)

func IsToolAllowedByIdentity(identity core.AgentIdentity, runCtx AgentRunContext, meta core.ToolRegistrationMeta, toolName string) bool {
	name := NormalizeToolPolicyName(toolName)
	if name == "" {
		return false
	}
	denySet := map[string]struct{}{}
	mergedDeny := identity.ToolDenylist
	if runCtx.Request.Lane == core.LaneSubagent || session.IsSubagentSessionKey(runCtx.Session.SessionKey) {
		baseDeny := ResolveSubagentDenyList(runCtx.Request.SpawnDepth, runCtx.Request.MaxSpawnDepth)
		explicitAllow := map[string]struct{}{}
		for _, item := range ExpandToolGroups(identity.ToolAllowlist) {
			explicitAllow[item] = struct{}{}
		}
		var filteredBase []string
		for _, item := range baseDeny {
			if _, ok := explicitAllow[NormalizeToolPolicyName(item)]; ok {
				continue
			}
			filteredBase = append(filteredBase, item)
		}
		mergedDeny = MergeToolLists(filteredBase, identity.ToolDenylist)
	}
	for _, item := range ExpandToolGroups(mergedDeny) {
		denySet[item] = struct{}{}
	}
	if _, denied := denySet[name]; denied {
		return false
	}

	allow := ExpandToolGroups(identity.ToolAllowlist)
	if profileAllow, ok := ResolveToolProfilePolicy(identity.ToolProfile); ok {
		allow = append(append([]string{}, allow...), ExpandToolGroups(profileAllow)...)
	}
	if meta.OptionalPlugin {
		pluginID := NormalizeToolPolicyName(meta.PluginID)
		allowSet := map[string]struct{}{}
		for _, item := range allow {
			allowSet[NormalizeToolPolicyName(item)] = struct{}{}
		}
		if len(allowSet) == 0 {
			return false
		}
		if _, ok := allowSet[name]; !ok {
			if pluginID != "" {
				if _, ok := allowSet[pluginID]; !ok {
					if _, ok := allowSet["group:plugins"]; !ok {
						return false
					}
				}
			} else if _, ok := allowSet["group:plugins"]; !ok {
				return false
			}
		}
	}
	if len(allow) == 0 {
		return true
	}
	for _, item := range allow {
		if item == name {
			return true
		}
		if item == "exec" && name == "apply_patch" {
			return true
		}
	}
	return false
}

func IsToolExecutionAllowedByIdentity(identity core.AgentIdentity, runCtx AgentRunContext, meta core.ToolRegistrationMeta) bool {
	if meta.OwnerOnly && strings.TrimSpace(runCtx.Request.To) == "" {
		return false
	}
	if len(meta.AllowedProviders) > 0 {
		currentProvider := NormalizeToolPolicyName(runCtx.ModelSelection.Provider)
		allowed := false
		for _, entry := range meta.AllowedProviders {
			normalized := NormalizeToolPolicyName(entry)
			if normalized == "*" || normalized == currentProvider {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	if len(meta.AllowedChannels) > 0 {
		currentChannel := NormalizeToolPolicyName(runCtx.Request.Channel)
		allowed := false
		for _, entry := range meta.AllowedChannels {
			normalized := NormalizeToolPolicyName(entry)
			if normalized == "*" || normalized == currentChannel {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	if meta.Elevated {
		if !identity.ElevatedEnabled {
			return false
		}
		if len(identity.ElevatedAllowFrom) > 0 {
			channel := strings.ToLower(strings.TrimSpace(runCtx.Request.Channel))
			allowList := identity.ElevatedAllowFrom[channel]
			if len(allowList) == 0 {
				allowList = identity.ElevatedAllowFrom["*"]
			}
			if len(allowList) == 0 {
				return false
			}
			currentTo := strings.TrimSpace(runCtx.Request.To)
			for _, entry := range allowList {
				trimmed := strings.TrimSpace(entry)
				if trimmed == "*" || trimmed == currentTo {
					goto elevatedAllowed
				}
			}
			return false
		}
	}
elevatedAllowed:
	if identity.SandboxMode == "" || strings.EqualFold(identity.SandboxMode, "off") {
		return true
	}
	if strings.EqualFold(identity.SandboxMode, "non-main") && identity.ID == session.DefaultAgentID {
		return true
	}
	return true
}

func IsToolAllowedInSandbox(identity core.AgentIdentity, runCtx AgentRunContext, meta core.ToolRegistrationMeta, toolName string, sandbox *SandboxContext) bool {
	if sandbox == nil || !sandbox.Enabled {
		return true
	}
	if strings.EqualFold(sandbox.WorkspaceAccess, "none") && meta.Elevated {
		return false
	}
	visibility := strings.TrimSpace(strings.ToLower(identity.SandboxSessionVisibility))
	if visibility == "" {
		visibility = "self"
	}
	normalizedToolName := NormalizeToolPolicyName(toolName)
	if !IsSessionScopedToolName(normalizedToolName) {
		return true
	}
	if visibility == "all" {
		return true
	}
	switch visibility {
	case "spawned", "tree":
		if runCtx.Request.Lane == core.LaneSubagent || session.IsSubagentSessionKey(runCtx.Session.SessionKey) {
			return true
		}
	case "self":
		return normalizedToolName == "session_status"
	}
	return false
}
