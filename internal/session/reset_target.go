package session

import (
	"strings"

	"github.com/kocort/kocort/internal/core"
)

func ResolveEffectiveACPResetSession(store *SessionStore, sess core.SessionResolution, route BoundSessionLookupOptions) core.SessionResolution {
	if store == nil {
		return sess
	}
	if sess.Entry != nil && sess.Entry.ACP != nil {
		return sess
	}
	if strings.TrimSpace(route.Channel) == "" || strings.TrimSpace(route.ThreadID) == "" {
		return sess
	}
	bindingSvc := NewThreadBindingService(store)
	boundKey, ok := bindingSvc.ResolveThreadSession(route)
	if !ok || strings.TrimSpace(boundKey) == "" || boundKey == sess.SessionKey || !IsAcpSessionKey(boundKey) {
		return sess
	}
	entry := store.Entry(boundKey)
	if entry == nil || entry.ACP == nil {
		return sess
	}
	return core.SessionResolution{
		SessionID:  entry.SessionID,
		SessionKey: boundKey,
		Entry:      entry,
		IsNew:      false,
		Fresh:      true,
	}
}
