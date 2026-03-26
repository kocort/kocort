package session

import (
	"sort"
	"strings"
	"time"
)

// ClassifySessionKind maps a canonical session key to the user-facing kind
// buckets used by sessions_list.
func ClassifySessionKind(sessionKey string, mainKey string) string {
	trimmed := strings.TrimSpace(sessionKey)
	if trimmed == "" {
		return "other"
	}
	lowered := strings.ToLower(trimmed)
	agentID := ResolveAgentIDFromSessionKey(trimmed)
	if trimmed == BuildMainSessionKeyWithMain(agentID, mainKey) {
		return "main"
	}
	switch {
	case strings.HasPrefix(lowered, "cron:"):
		return "cron"
	case strings.HasPrefix(lowered, "hook:"):
		return "hook"
	case strings.HasPrefix(lowered, "node-"), strings.HasPrefix(lowered, "node:"):
		return "node"
	case IsSubagentSessionKey(trimmed):
		return "subagent"
	case IsAcpSessionKey(trimmed):
		return "acp"
	case IsThreadSessionKey(trimmed):
		return "thread"
	case strings.Contains(lowered, ":group:"), strings.Contains(lowered, ":channel:"):
		return "group"
	default:
		return "other"
	}
}

// DeriveSessionChannel returns the most useful display channel for a listed

// listings.
func DeriveSessionChannel(sessionKey string, kind string, channel string, lastChannel string) string {
	if kind == "cron" || kind == "hook" || kind == "node" || kind == "subagent" || kind == "acp" {
		return "internal"
	}
	if trimmed := strings.TrimSpace(channel); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(lastChannel); trimmed != "" {
		return trimmed
	}
	parts := strings.Split(strings.TrimSpace(sessionKey), ":")
	if len(parts) >= 4 && (parts[2] == "group" || parts[2] == "channel") {
		return parts[1]
	}
	return "unknown"
}

// SessionListFilterOptions controls session list filtering/projection behavior.
type SessionListFilterOptions struct {
	Now           time.Time
	ActiveMinutes int
	Limit         int
	AllowedKinds  map[string]struct{}
	MainKey       string
	Allow         func(item SessionListItem) bool
}

// FilterSessionListItems applies access, activity, kind, sort, and limit
// semantics to a session list in one place so tool code does not need to
// duplicate list behavior.
func FilterSessionListItems(items []SessionListItem, opts SessionListFilterOptions) []SessionListItem {
	filtered := make([]SessionListItem, 0, len(items))
	cutoff := opts.Now.UTC().Add(-time.Duration(opts.ActiveMinutes) * time.Minute)
	for _, item := range items {
		if opts.Allow != nil && !opts.Allow(item) {
			continue
		}
		if opts.ActiveMinutes > 0 && item.UpdatedAt.Before(cutoff) {
			continue
		}
		if len(opts.AllowedKinds) > 0 {
			kind := ClassifySessionKind(item.Key, opts.MainKey)
			if _, ok := opts.AllowedKinds[kind]; !ok {
				continue
			}
		}
		filtered = append(filtered, item)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt)
	})
	if opts.Limit > 0 && len(filtered) > opts.Limit {
		filtered = filtered[:opts.Limit]
	}
	return filtered
}

// DecorateSessionListItems enriches raw session list items with user-facing
// display fields while keeping the canonical key intact.
func DecorateSessionListItems(items []SessionListItem, mainKey string) []SessionListItem {
	alias := ResolveMainSessionAlias(mainKey)
	out := make([]SessionListItem, 0, len(items))
	for _, item := range items {
		next := item
		next.Kind = ClassifySessionKind(item.Key, mainKey)
		next.Channel = DeriveSessionChannel(item.Key, next.Kind, "", item.LastChannel)
		next.DisplayKey = ResolveDisplaySessionKey(item.Key, alias)
		out = append(out, next)
	}
	return out
}
