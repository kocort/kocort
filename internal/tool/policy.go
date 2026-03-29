package tool

import "strings"

// DefaultSubagentMaxSpawnDepth is the default maximum depth for subagent spawning.
const DefaultSubagentMaxSpawnDepth = 5

// SubagentToolDenyAlways lists tools always denied to subagents.
var SubagentToolDenyAlways = []string{
	"gateway",
	"agents_list",
	"whatsapp_login",
	"session_status",
	"cron",
	"message",
	"memory_search",
	"memory_get",
	"sessions_send",
}

// SubagentToolDenyLeaf lists additional tools denied to leaf subagents (at max depth).
var SubagentToolDenyLeaf = []string{
	"sessions_list",
	"sessions_history",
	"sessions_spawn",
}

// ToolNameAliases maps alternative tool names to their canonical forms.
var ToolNameAliases = map[string]string{
	"bash":        "exec",
	"apply-patch": "apply_patch",
	"send_file":   "message",
}

// CoreToolGroups maps group identifiers to their constituent tool names.
var CoreToolGroups = map[string][]string{
	"group:sessions": {"session_status", "sessions_history", "sessions_list", "sessions_send", "sessions_spawn", "subagents"},
	"group:agents":   {"sessions_spawn", "subagents"},
	"group:kocort":   {"agents_list", "apply_patch", "browser", "cron", "edit", "exec", "find", "gateway", "grep", "image", "ls", "memory_get", "memory_search", "message", "process", "read", "session_status", "sessions_history", "sessions_list", "sessions_send", "sessions_spawn", "subagents", "web_fetch", "web_search", "write"},
	"group:plugins":  {},
}

// CoreToolProfiles maps profile names to their allowed tool lists.
var CoreToolProfiles = map[string][]string{
	"minimal":   {"session_status"},
	"coding":    {"agents_list", "apply_patch", "browser", "cron", "edit", "exec", "find", "gateway", "grep", "image", "ls", "memory_get", "memory_search", "message", "process", "read", "session_status", "sessions_history", "sessions_list", "sessions_send", "sessions_spawn", "subagents", "web_fetch", "web_search", "write"},
	"messaging": {"cron", "message", "process", "session_status", "sessions_history", "sessions_list", "sessions_send"},
	"full":      {},
}

// NormalizeToolPolicyName normalises a tool name for policy matching.
func NormalizeToolPolicyName(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if alias, ok := ToolNameAliases[normalized]; ok {
		return alias
	}
	return normalized
}

// NormalizeToolList normalises a list of tool names.
func NormalizeToolList(list []string) []string {
	if len(list) == 0 {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		normalized := NormalizeToolPolicyName(item)
		if normalized != "" {
			out = append(out, normalized)
		}
	}
	return out
}

// ExpandToolGroups expands group references in a tool list.
func ExpandToolGroups(list []string) []string {
	normalized := NormalizeToolList(list)
	if len(normalized) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var expanded []string
	for _, entry := range normalized {
		if group, ok := CoreToolGroups[entry]; ok {
			for _, t := range group {
				t = NormalizeToolPolicyName(t)
				if _, exists := seen[t]; exists || t == "" {
					continue
				}
				seen[t] = struct{}{}
				expanded = append(expanded, t)
			}
			continue
		}
		if _, exists := seen[entry]; exists {
			continue
		}
		seen[entry] = struct{}{}
		expanded = append(expanded, entry)
	}
	return expanded
}

// ResolveToolProfilePolicy returns the allowed tool list for a profile.
func ResolveToolProfilePolicy(profile string) (allow []string, ok bool) {
	normalized := strings.ToLower(strings.TrimSpace(profile))
	allow, ok = CoreToolProfiles[normalized]
	if !ok {
		return nil, false
	}
	if len(allow) == 0 {
		return nil, true
	}
	return append([]string{}, allow...), true
}

// ResolveSubagentDenyList builds the deny list for a subagent at a given depth.
func ResolveSubagentDenyList(depth int, maxSpawnDepth int) []string {
	effectiveMax := maxSpawnDepth
	if effectiveMax <= 0 {
		effectiveMax = DefaultSubagentMaxSpawnDepth
	}
	if depth >= max(1, effectiveMax) {
		return append(append([]string{}, SubagentToolDenyAlways...), SubagentToolDenyLeaf...)
	}
	return append([]string{}, SubagentToolDenyAlways...)
}

// MergeToolLists concatenates two tool lists.
func MergeToolLists(base []string, extra []string) []string {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	merged := append([]string{}, base...)
	merged = append(merged, extra...)
	return merged
}

// IsSessionScopedToolName returns true for session-scoped tool names.
func IsSessionScopedToolName(name string) bool {
	switch NormalizeToolPolicyName(name) {
	case "session_status", "sessions_history", "sessions_list", "sessions_send", "sessions_spawn", "subagents":
		return true
	default:
		return false
	}
}

// ExistingToolNames returns a set of normalized tool names from the given tools.
func ExistingToolNames(tools []Tool) map[string]struct{} {
	if len(tools) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(tools))
	for _, t := range tools {
		if t == nil {
			continue
		}
		out[NormalizeToolPolicyName(t.Name())] = struct{}{}
	}
	return out
}
