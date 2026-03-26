package session

import (
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
)

// ResolveFreshnessPolicyFromConfig resolves the effective session freshness
// policy for a request from config-layer settings.
func ResolveFreshnessPolicyFromConfig(cfg config.AppConfig, chatType core.ChatType, channel string) SessionFreshnessPolicy {
	p := config.ResolveSessionResetPolicy(cfg, chatType, channel)
	return SessionFreshnessPolicy{
		Mode:        p.Mode,
		AtHour:      p.AtHour,
		IdleMinutes: p.IdleMinutes,
	}
}

// ResolveFreshnessPolicyForSession resolves the effective session freshness
// policy for the given request/session context, using internal/session helpers
// to derive reset type semantics before bridging to config-layer policy.
func ResolveFreshnessPolicyForSession(cfg config.AppConfig, sessionKey string, chatType core.ChatType, channel string, threadID string) SessionFreshnessPolicy {
	resetType := ResolveSessionResetType(sessionKey, chatType, threadID)
	switch resetType {
	case SessionResetThread:
		chatType = core.ChatTypeThread
	case SessionResetGroup:
		chatType = core.ChatTypeGroup
	default:
		chatType = core.ChatTypeDirect
	}
	return ResolveFreshnessPolicyFromConfig(cfg, chatType, channel)
}
