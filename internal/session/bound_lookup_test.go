package session

import (
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
)

func TestResolveBoundSessionKeyFindsPersistentSpawnedChild(t *testing.T) {
	store := &SessionStore{
		entries: map[string]core.SessionEntry{
			"agent:worker:subagent:abc": {
				SessionID:       "sess_1",
				SpawnedBy:       "agent:main:main",
				SpawnMode:       "session",
				UpdatedAt:       time.Now().UTC(),
				DeliveryContext: &core.DeliveryContext{Channel: "slack", To: "room-1", ThreadID: "thread-9"},
			},
		},
	}
	key, ok := store.ResolveBoundSessionKey(BoundSessionLookupOptions{
		Channel:  "slack",
		To:       "room-1",
		ThreadID: "thread-9",
	})
	if !ok || key != "agent:worker:subagent:abc" {
		t.Fatalf("unexpected bound session lookup: %v %q", ok, key)
	}
}

func TestResolveBoundSessionKeyIgnoresOneShotChild(t *testing.T) {
	store := &SessionStore{
		entries: map[string]core.SessionEntry{
			"agent:worker:acp:acp-live:abc": {
				SessionID:       "sess_1",
				SpawnedBy:       "agent:main:main",
				SpawnMode:       "run",
				UpdatedAt:       time.Now().UTC(),
				DeliveryContext: &core.DeliveryContext{Channel: "discord", To: "chan-1", ThreadID: "thread-2"},
			},
		},
	}
	if key, ok := store.ResolveBoundSessionKey(BoundSessionLookupOptions{
		Channel:  "discord",
		To:       "chan-1",
		ThreadID: "thread-2",
	}); ok || key != "" {
		t.Fatalf("expected no match, got %v %q", ok, key)
	}
}
