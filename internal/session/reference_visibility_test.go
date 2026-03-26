package session

import (
	"testing"

	"github.com/kocort/kocort/internal/core"
)

func TestResolveVisibleSessionReferenceRestrictsSandboxedNonSpawnedKey(t *testing.T) {
	store := newTestStore(t)
	if err := store.Upsert("agent:main:main", core.SessionEntry{
		SessionID: "sess-parent",
	}); err != nil {
		t.Fatalf("upsert parent: %v", err)
	}
	if err := store.Upsert("agent:main:subagent:child", core.SessionEntry{
		SessionID: "sess-child",
		SpawnedBy: "agent:main:main",
	}); err != nil {
		t.Fatalf("upsert child: %v", err)
	}
	if err := store.Upsert("agent:main:subagent:other", core.SessionEntry{
		SessionID: "sess-other",
		SpawnedBy: "agent:main:other-root",
	}); err != nil {
		t.Fatalf("upsert other: %v", err)
	}
	resolved := ResolveVisibleSessionReference(store, ResolveVisibleSessionReferenceOptions{
		Resolve: ResolveReferenceOptions{
			Reference:        "agent:main:subagent:other",
			RequesterAgentID: "main",
		},
		RequesterSessionKey:      "agent:main:main",
		SandboxEnabled:           true,
		SandboxSessionVisibility: "spawned",
	})
	if resolved.Status != "forbidden" {
		t.Fatalf("expected forbidden, got %+v", resolved)
	}
}

func TestResolveVisibleSessionReferenceAllowsSessionIDInSandbox(t *testing.T) {
	store := newTestStore(t)
	if err := store.Upsert("agent:main:subagent:other", core.SessionEntry{
		SessionID: "sess-other",
		SpawnedBy: "agent:main:other-root",
	}); err != nil {
		t.Fatalf("upsert other: %v", err)
	}
	resolved := ResolveVisibleSessionReference(store, ResolveVisibleSessionReferenceOptions{
		Resolve: ResolveReferenceOptions{
			Reference:        "sess-other",
			RequesterAgentID: "main",
		},
		RequesterSessionKey:      "agent:main:main",
		SandboxEnabled:           true,
		SandboxSessionVisibility: "spawned",
	})
	if !resolved.Found || resolved.Status != "" {
		t.Fatalf("expected session id resolution to pass, got %+v", resolved)
	}
	if resolved.Key != "agent:main:subagent:other" {
		t.Fatalf("expected canonical key, got %q", resolved.Key)
	}
}

func TestShouldRestrictToSpawnedVisibilitySkipsSubagentRequester(t *testing.T) {
	if ShouldRestrictToSpawnedVisibility(ResolveVisibleSessionReferenceOptions{
		RequesterSessionKey:      "agent:main:subagent:child",
		SandboxEnabled:           true,
		SandboxSessionVisibility: "spawned",
	}) {
		t.Fatal("subagent requester should not be re-restricted to spawned tree")
	}
}
