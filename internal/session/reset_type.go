package session

import (
	"strings"

	"github.com/kocort/kocort/internal/core"
)

// SessionResetType classifies a session for reset-policy selection.
type SessionResetType string

const (
	SessionResetDirect SessionResetType = "direct"
	SessionResetGroup  SessionResetType = "group"
	SessionResetThread SessionResetType = "thread"
)

var (
	threadSessionMarkers = []string{":thread:", ":topic:"}
	groupSessionMarkers  = []string{":group:", ":channel:"}
)

// IsThreadSessionKey reports whether a session key is thread-like.
func IsThreadSessionKey(sessionKey string) bool {
	normalized := strings.ToLower(strings.TrimSpace(sessionKey))
	if normalized == "" {
		return false
	}
	for _, marker := range threadSessionMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

// ResolveThreadFlag derives whether the current request/session should be
// treated as thread-bound for policy purposes.
func ResolveThreadFlag(sessionKey string, chatType core.ChatType, threadID string) bool {
	if strings.TrimSpace(threadID) != "" {
		return true
	}
	if chatType == core.ChatTypeThread || chatType == core.ChatTypeTopic {
		return true
	}
	return IsThreadSessionKey(sessionKey)
}

// ResolveSessionResetType derives the effective reset-policy type for the
// current request/session.
func ResolveSessionResetType(sessionKey string, chatType core.ChatType, threadID string) SessionResetType {
	if ResolveThreadFlag(sessionKey, chatType, threadID) {
		return SessionResetThread
	}
	switch chatType {
	case core.ChatTypeGroup:
		return SessionResetGroup
	}
	normalized := strings.ToLower(strings.TrimSpace(sessionKey))
	for _, marker := range groupSessionMarkers {
		if strings.Contains(normalized, marker) {
			return SessionResetGroup
		}
	}
	return SessionResetDirect
}
