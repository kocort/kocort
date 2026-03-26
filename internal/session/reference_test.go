package session

import (
	"testing"

	"github.com/kocort/kocort/internal/core"
)

func TestResolveSessionReferenceResolvesMainAlias(t *testing.T) {
	store := newTestStore(t)
	key, ok := ResolveSessionReference(store, ResolveReferenceOptions{
		Reference:        "main",
		RequesterAgentID: "worker",
	})
	if !ok {
		t.Fatal("expected main alias to resolve")
	}
	if key != BuildMainSessionKey("worker") {
		t.Fatalf("expected worker main key, got %q", key)
	}
}

func TestResolveSessionReferenceResolvesConfiguredMainAlias(t *testing.T) {
	store := newTestStore(t)
	key, ok := ResolveSessionReference(store, ResolveReferenceOptions{
		Reference:        "main",
		RequesterAgentID: "worker",
		MainKey:          "assistant",
	})
	if !ok {
		t.Fatal("expected main alias to resolve")
	}
	if key != BuildMainSessionKeyWithMain("worker", "assistant") {
		t.Fatalf("expected worker assistant key, got %q", key)
	}
}

func TestResolveSessionReferencePrefersExistingStoreReference(t *testing.T) {
	store := newTestStore(t)
	if err := store.Upsert("agent:main:main", core.SessionEntry{
		SessionID: "sess-1",
		Label:     "deploy",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	key, ok := ResolveSessionReference(store, ResolveReferenceOptions{
		Reference:        "sess-1",
		RequesterAgentID: "main",
	})
	if !ok || key != "agent:main:main" {
		t.Fatalf("expected session id to resolve, got %q, %v", key, ok)
	}
}

func TestResolveSessionReferenceDetailedMarksSessionIDResolution(t *testing.T) {
	store := newTestStore(t)
	if err := store.Upsert("agent:main:main", core.SessionEntry{
		SessionID: "sess-1",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	resolution := ResolveSessionReferenceDetailed(store, ResolveReferenceOptions{
		Reference:        "sess-1",
		RequesterAgentID: "main",
	})
	if !resolution.Found {
		t.Fatal("expected session id to resolve")
	}
	if !resolution.ResolvedViaID || resolution.ResolvedVia != SessionReferenceSessionID {
		t.Fatalf("expected session id resolution, got %+v", resolution)
	}
	if resolution.Key != "agent:main:main" {
		t.Fatalf("expected canonical key, got %q", resolution.Key)
	}
}

func TestResolveSessionReferenceFallsBackToScopedLabel(t *testing.T) {
	store := newTestStore(t)
	if err := store.Upsert("agent:main:subagent:child", core.SessionEntry{
		SessionID: "sess-child",
		Label:     "deploy",
		SpawnedBy: "agent:main:main",
	}); err != nil {
		t.Fatalf("upsert child: %v", err)
	}
	key, ok := ResolveSessionReference(store, ResolveReferenceOptions{
		Reference:        "deploy",
		RequesterAgentID: "main",
		SpawnedBy:        "agent:main:main",
	})
	if !ok || key != "agent:main:subagent:child" {
		t.Fatalf("expected scoped label to resolve, got %q, %v", key, ok)
	}
}
