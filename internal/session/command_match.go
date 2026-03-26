package session

import "github.com/kocort/kocort/internal/core"

// SessionCommandKind identifies the high-level session command family.
type SessionCommandKind string

const (
	SessionCommandReset   SessionCommandKind = "reset"
	SessionCommandCompact SessionCommandKind = "compact"
)

// SessionCommandMatch is the unified domain representation of a recognized
// session command. Callers can switch on Kind and consume the typed payload.
type SessionCommandMatch struct {
	Kind    SessionCommandKind
	Reset   *SessionResetCommandMatch
	Compact *SessionCompactCommandMatch
}

// ParseSessionCommandForChatType recognizes the supported session commands for
// the given message/chat type. Reset/new commands take precedence over compact
// commands to preserve the existing runtime handling order.
func ParseSessionCommandForChatType(triggers []string, message string, chatType core.ChatType) (SessionCommandMatch, bool) {
	if reset, ok := ParseSessionResetCommandForChatType(triggers, message, chatType); ok {
		return SessionCommandMatch{
			Kind:  SessionCommandReset,
			Reset: &reset,
		}, true
	}
	if compact, ok := ParseSessionCompactCommandForChatType(message, chatType); ok {
		return SessionCommandMatch{
			Kind:    SessionCommandCompact,
			Compact: &compact,
		}, true
	}
	return SessionCommandMatch{}, false
}
