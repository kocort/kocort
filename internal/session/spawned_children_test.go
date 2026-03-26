package session

import (
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
)

func TestListPersistentSpawnedChildrenIncludesACPAndSubagentSessions(t *testing.T) {
	store := &SessionStore{
		entries: map[string]core.SessionEntry{
			"agent:worker:subagent:one": {
				SessionID:    "sess-sub",
				SpawnedBy:    "agent:main:main",
				SpawnMode:    "session",
				Label:        "worker-sub",
				UpdatedAt:    time.Unix(200, 0).UTC(),
				LastThreadID: "thread-sub",
			},
			"agent:worker:acp:claude:two": {
				SessionID: "sess-acp",
				SpawnedBy: "agent:main:main",
				SpawnMode: "session",
				Label:     "worker-acp",
				UpdatedAt: time.Unix(300, 0).UTC(),
				DeliveryContext: &core.DeliveryContext{
					ThreadID: "thread-acp",
				},
				ACP: &core.AcpSessionMeta{State: "idle"},
			},
			"agent:worker:acp:claude:oneshot": {
				SessionID: "sess-skip",
				SpawnedBy: "agent:main:main",
				SpawnMode: "run",
				UpdatedAt: time.Unix(400, 0).UTC(),
			},
			"agent:worker:main": {
				SessionID: "sess-main",
				SpawnedBy: "agent:main:main",
				SpawnMode: "session",
				UpdatedAt: time.Unix(500, 0).UTC(),
			},
		},
	}

	items := store.ListPersistentSpawnedChildren("agent:main:main")
	if len(items) != 2 {
		t.Fatalf("expected 2 persistent child sessions, got %d", len(items))
	}
	if items[0].Kind != "acp" || items[0].SessionKey != "agent:worker:acp:claude:two" || items[0].ThreadID != "thread-acp" || items[0].ACPState != "idle" {
		t.Fatalf("unexpected first item: %+v", items[0])
	}
	if items[1].Kind != "subagent" || items[1].SessionKey != "agent:worker:subagent:one" || items[1].ThreadID != "thread-sub" {
		t.Fatalf("unexpected second item: %+v", items[1])
	}
}
