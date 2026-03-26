package session

import (
	"strings"

	"github.com/kocort/kocort/internal/core"
)

// ResolveSessionKeyFromOptions derives a session key from resolve options.
func ResolveSessionKeyFromOptions(opts SessionResolveOptions) string {
	if strings.TrimSpace(opts.SessionKey) != "" {
		return strings.TrimSpace(opts.SessionKey)
	}
	agentID := NormalizeAgentID(opts.AgentID)
	channel := strings.TrimSpace(strings.ToLower(opts.Channel))
	if channel == "" {
		channel = "webchat"
	}
	mainKey := strings.TrimSpace(opts.MainKey)
	if mainKey == "" {
		mainKey = DefaultMainKey
	}
	dmScope := strings.TrimSpace(strings.ToLower(opts.DMScope))
	if dmScope == "" {
		dmScope = "per-peer"
	}
	switch opts.ChatType {
	case core.ChatTypeThread:
		base := ResolveBaseSessionKey(agentID, channel, opts.To, core.ChatTypeDirect, mainKey, dmScope)
		return BuildThreadSessionKey(base, opts.ThreadID)
	case core.ChatTypeGroup, core.ChatTypeTopic:
		base := ResolveBaseSessionKey(agentID, channel, opts.To, opts.ChatType, mainKey, dmScope)
		if strings.TrimSpace(opts.ThreadID) != "" {
			return BuildThreadSessionKey(base, opts.ThreadID)
		}
		return base
	default:
		base := ResolveBaseSessionKey(agentID, channel, opts.To, core.ChatTypeDirect, mainKey, dmScope)
		if strings.TrimSpace(opts.ThreadID) != "" {
			return BuildThreadSessionKey(base, opts.ThreadID)
		}
		return base
	}
}

// ResolveBaseSessionKey determines the base session key for a given chat type.
func ResolveBaseSessionKey(agentID, channel, to string, chatType core.ChatType, mainKey string, dmScope string) string {
	to = strings.TrimSpace(to)
	switch chatType {
	case core.ChatTypeGroup, core.ChatTypeTopic:
		if to != "" {
			return BuildGroupSessionKey(agentID, channel, chatType, to)
		}
	case core.ChatTypeDirect:
		if to != "" && strings.TrimSpace(strings.ToLower(dmScope)) != "main" {
			return BuildDirectSessionKey(agentID, channel, to)
		}
	}
	return BuildMainSessionKeyWithMain(agentID, mainKey)
}
