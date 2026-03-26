package session

import (
	"strings"
	"time"
)

// BoundSessionLookupOptions describes the route metadata used to discover a
// persistent child session bound to a conversation thread.
type BoundSessionLookupOptions struct {
	Channel   string
	To        string
	AccountID string
	ThreadID  string
}

// ResolveBoundSessionKey returns a persistent child session key whose stored
// route metadata matches the current conversation route.
func (s *SessionStore) ResolveBoundSessionKey(opts BoundSessionLookupOptions) (string, bool) {
	if s == nil {
		return "", false
	}
	channel := strings.TrimSpace(opts.Channel)
	to := strings.TrimSpace(opts.To)
	accountID := strings.TrimSpace(opts.AccountID)
	threadID := strings.TrimSpace(opts.ThreadID)
	if channel == "" || threadID == "" {
		return "", false
	}
	if binding, ok := s.ResolveSessionBinding(SessionBindingLookup{
		Channel:   channel,
		To:        to,
		AccountID: accountID,
		ThreadID:  threadID,
	}); ok && strings.TrimSpace(binding.TargetSessionKey) != "" {
		return binding.TargetSessionKey, true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	bestKey := ""
	bestUpdated := time.Time{}
	for key, entry := range s.entries {
		if strings.TrimSpace(entry.SpawnedBy) == "" || strings.TrimSpace(entry.SpawnMode) != "session" {
			continue
		}
		if !IsSubagentSessionKey(key) && !IsAcpSessionKey(key) {
			continue
		}
		delivery := entry.DeliveryContext
		if delivery == nil {
			continue
		}
		if strings.TrimSpace(delivery.Channel) != channel || strings.TrimSpace(delivery.ThreadID) != threadID {
			continue
		}
		if to != "" && strings.TrimSpace(delivery.To) != "" && strings.TrimSpace(delivery.To) != to {
			continue
		}
		if bestKey == "" || entry.UpdatedAt.After(bestUpdated) {
			bestKey = key
			bestUpdated = entry.UpdatedAt
		}
	}
	if bestKey == "" {
		return "", false
	}
	return bestKey, true
}
