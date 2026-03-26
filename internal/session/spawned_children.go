package session

import (
	"sort"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
)

// SpawnedChildSession is a user-facing projection of a persistent spawned child
// session stored in the session store.
type SpawnedChildSession struct {
	SessionKey          string
	SessionID           string
	RequesterSessionKey string
	AgentID             string
	Label               string
	Kind                string
	Mode                string
	ThreadID            string
	BoundBy             string
	BindingPlacement    string
	BindingIntroText    string
	UpdatedAt           time.Time
	ACPState            string
}

// ListPersistentSpawnedChildren returns persistent spawned child sessions for
// the given requester. This is intentionally session-store-based so ACP
// persistent children can participate in the same management view as
// subagents, even when they are not represented in the subagent registry.
func (s *SessionStore) ListPersistentSpawnedChildren(requesterSessionKey string) []SpawnedChildSession {
	s.mu.Lock()
	defer s.mu.Unlock()

	requester := strings.TrimSpace(requesterSessionKey)
	if requester == "" {
		return nil
	}

	items := make([]SpawnedChildSession, 0)
	for key, entry := range s.entries {
		if strings.TrimSpace(entry.SpawnedBy) != requester || strings.TrimSpace(entry.SpawnMode) != "session" {
			continue
		}
		kind := persistentSpawnedChildKind(key, entry)
		if kind == "" {
			continue
		}
		item := SpawnedChildSession{
			SessionKey:          key,
			SessionID:           strings.TrimSpace(entry.SessionID),
			RequesterSessionKey: requester,
			AgentID:             ResolveAgentIDFromSessionKey(key),
			Label:               strings.TrimSpace(entry.Label),
			Kind:                kind,
			Mode:                strings.TrimSpace(entry.SpawnMode),
			ThreadID:            strings.TrimSpace(entry.LastThreadID),
			UpdatedAt:           entry.UpdatedAt,
		}
		if bindings := s.listSessionBindingsForTargetSessionLocked(key, time.Now().UTC()); len(bindings) > 0 {
			item.BoundBy = strings.TrimSpace(bindings[0].BoundBy)
			item.BindingPlacement = strings.TrimSpace(bindings[0].Placement)
			item.BindingIntroText = strings.TrimSpace(bindings[0].IntroText)
		}
		if entry.DeliveryContext != nil && strings.TrimSpace(entry.DeliveryContext.ThreadID) != "" {
			item.ThreadID = strings.TrimSpace(entry.DeliveryContext.ThreadID)
		}
		if entry.ACP != nil {
			item.ACPState = strings.TrimSpace(entry.ACP.State)
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return items
}

func (s *SessionStore) listSessionBindingsForTargetSessionLocked(targetSessionKey string, now time.Time) []SessionBindingRecord {
	target := strings.TrimSpace(targetSessionKey)
	if target == "" {
		return nil
	}
	out := make([]SessionBindingRecord, 0)
	for _, record := range s.bindings {
		if strings.TrimSpace(record.TargetSessionKey) != target {
			continue
		}
		if isSessionBindingExpired(record, now) {
			continue
		}
		out = append(out, record)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].LastActivityAt.After(out[j].LastActivityAt)
	})
	return out
}

func persistentSpawnedChildKind(sessionKey string, entry core.SessionEntry) string {
	switch {
	case IsSubagentSessionKey(sessionKey):
		return "subagent"
	case IsAcpSessionKey(sessionKey):
		return "acp"
	default:
		return ""
	}
}
