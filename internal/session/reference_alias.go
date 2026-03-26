package session

import "strings"

// MainSessionAlias describes the configured main-session naming that tools use
// to map user-facing aliases to canonical internal keys.
//
// current project only carries main-key semantics for now, so Alias defaults to
// the canonical mainKey until scope support is introduced.
type MainSessionAlias struct {
	MainKey string
	Alias   string
}

// ResolveMainSessionAlias normalizes the configured main-session alias state.
func ResolveMainSessionAlias(mainKey string) MainSessionAlias {
	normalized := strings.TrimSpace(strings.ToLower(mainKey))
	if normalized == "" {
		normalized = DefaultMainKey
	}
	return MainSessionAlias{
		MainKey: normalized,
		Alias:   normalized,
	}
}

// ResolveDisplaySessionKey maps an internal key back to the user-facing key.
func ResolveDisplaySessionKey(key string, alias MainSessionAlias) string {
	resolved := strings.TrimSpace(key)
	if resolved == "" {
		return ""
	}
	if resolved == "main" || resolved == alias.Alias || resolved == alias.MainKey {
		return "main"
	}
	if resolved == BuildMainSessionKeyWithMain(ResolveAgentIDFromSessionKey(resolved), alias.MainKey) {
		return "main"
	}
	return resolved
}

// ResolveInternalSessionKey maps a user-facing key to the canonical internal
// key used for lookups.
func ResolveInternalSessionKey(key string, alias MainSessionAlias) string {
	resolved := strings.TrimSpace(strings.ToLower(key))
	if resolved == "" {
		return ""
	}
	if resolved == "main" {
		return alias.Alias
	}
	return strings.TrimSpace(key)
}
