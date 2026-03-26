package session

import (
	"strings"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
)

func TestSessionBindingsUpsertResolveAndTouch(t *testing.T) {
	store := &SessionStore{
		entries:   map[string]core.SessionEntry{},
		bindings:  map[string]SessionBindingRecord{},
		storePath: "",
	}
	if err := store.UpsertSessionBinding(SessionBindingUpsert{
		TargetSessionKey:    "agent:worker:subagent:one",
		RequesterSessionKey: "agent:main:main",
		Channel:             "discord",
		AccountID:           "acct-1",
		To:                  "room-1",
		ThreadID:            "thread-1",
		BoundAt:             time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("upsert binding: %v", err)
	}
	record, ok := store.ResolveSessionBinding(SessionBindingLookup{
		Channel:   "discord",
		AccountID: "acct-1",
		To:        "room-1",
		ThreadID:  "thread-1",
	})
	if !ok || record.TargetSessionKey != "agent:worker:subagent:one" {
		t.Fatalf("unexpected binding lookup result: %+v %v", record, ok)
	}
	if !store.TouchSessionBindingActivity(SessionBindingLookup{
		Channel:   "discord",
		AccountID: "acct-1",
		To:        "room-1",
		ThreadID:  "thread-1",
	}, time.Unix(200, 0).UTC()) {
		t.Fatal("expected binding touch to succeed")
	}
	record, _ = store.ResolveSessionBinding(SessionBindingLookup{
		Channel:   "discord",
		AccountID: "acct-1",
		To:        "room-1",
		ThreadID:  "thread-1",
	})
	if record.LastActivityAt.Unix() != 200 {
		t.Fatalf("expected updated last activity, got %+v", record)
	}
}

func TestSessionBindingsPersistAcrossStoreReload(t *testing.T) {
	baseDir := t.TempDir()
	store, err := NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	if err := store.UpsertSessionBinding(SessionBindingUpsert{
		TargetSessionKey: "agent:worker:acp:test",
		Channel:          "discord",
		AccountID:        "acct-1",
		ThreadID:         "thread-1",
	}); err != nil {
		t.Fatalf("upsert binding: %v", err)
	}

	reloaded, err := NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("reload session store: %v", err)
	}
	record, ok := reloaded.ResolveSessionBinding(SessionBindingLookup{
		Channel:   "discord",
		AccountID: "acct-1",
		ThreadID:  "thread-1",
	})
	if !ok || record.TargetSessionKey != "agent:worker:acp:test" {
		t.Fatalf("expected persisted binding after reload, got %+v %v", record, ok)
	}
}

func TestSessionBindingsExpireOnIdleAndMaxAge(t *testing.T) {
	store := &SessionStore{
		entries: map[string]core.SessionEntry{
			"agent:worker:subagent:one": {SessionID: "sess_1"},
		},
		bindings:  map[string]SessionBindingRecord{},
		storePath: "",
	}
	if err := store.UpsertSessionBinding(SessionBindingUpsert{
		TargetSessionKey: "agent:worker:subagent:one",
		Channel:          "discord",
		AccountID:        "acct-1",
		ThreadID:         "thread-1",
		BoundAt:          time.Unix(100, 0).UTC(),
		LastActivityAt:   time.Unix(100, 0).UTC(),
		IdleTimeoutMs:    int64((2 * time.Second) / time.Millisecond),
	}); err != nil {
		t.Fatalf("upsert idle binding: %v", err)
	}
	store.mu.Lock()
	record, ok, changed := store.resolveSessionBindingLocked(SessionBindingLookup{
		Channel:   "discord",
		AccountID: "acct-1",
		ThreadID:  "thread-1",
	}, time.Unix(103, 0).UTC())
	store.mu.Unlock()
	if ok || changed == false || record.TargetSessionKey != "" {
		t.Fatalf("expected idle-expired binding to be removed, got %+v ok=%v changed=%v", record, ok, changed)
	}

	if err := store.UpsertSessionBinding(SessionBindingUpsert{
		TargetSessionKey: "agent:worker:subagent:one",
		Channel:          "discord",
		AccountID:        "acct-1",
		ThreadID:         "thread-1",
		BoundAt:          time.Unix(200, 0).UTC(),
		MaxAgeMs:         int64((2 * time.Second) / time.Millisecond),
	}); err != nil {
		t.Fatalf("upsert max-age binding: %v", err)
	}
	store.mu.Lock()
	_, ok, changed = store.resolveSessionBindingLocked(SessionBindingLookup{
		Channel:   "discord",
		AccountID: "acct-1",
		ThreadID:  "thread-1",
	}, time.Unix(203, 0).UTC())
	store.mu.Unlock()
	if ok || !changed {
		t.Fatalf("expected max-age-expired binding to be removed, ok=%v changed=%v", ok, changed)
	}
}

func TestSessionBindingsExposeMetadataAndUnbindByTarget(t *testing.T) {
	store := &SessionStore{
		entries:   map[string]core.SessionEntry{},
		bindings:  map[string]SessionBindingRecord{},
		storePath: "",
	}
	if err := store.UpsertSessionBinding(SessionBindingUpsert{
		TargetSessionKey:    "agent:worker:acp:test",
		RequesterSessionKey: "agent:main:main",
		TargetKind:          "session",
		Status:              "active",
		BoundBy:             "system",
		Placement:           "child",
		Label:               "codex-acp",
		AgentID:             "worker",
		Channel:             "discord",
		AccountID:           "acct-1",
		ThreadID:            "thread-1",
		IdleTimeoutMs:       int64((24 * time.Hour) / time.Millisecond),
		MaxAgeMs:            int64((48 * time.Hour) / time.Millisecond),
	}); err != nil {
		t.Fatalf("upsert binding with metadata: %v", err)
	}
	items := store.ListSessionBindingsForTargetSession("agent:worker:acp:test")
	if len(items) != 1 {
		t.Fatalf("expected one binding, got %d", len(items))
	}
	if items[0].TargetKind != "session" || items[0].Placement != "child" || items[0].BoundBy != "system" {
		t.Fatalf("unexpected metadata: %+v", items[0])
	}
	if items[0].IntroText == "" || !strings.Contains(items[0].IntroText, "idle auto-unfocus") {
		t.Fatalf("expected intro text with lifecycle details, got %+v", items[0])
	}
	if removed := store.UnbindSessionBindingsByTargetSession("agent:worker:acp:test", "max-age-expired"); removed != 1 {
		t.Fatalf("expected one binding removed, got %d", removed)
	}
	if remaining := store.ListSessionBindingsForTargetSession("agent:worker:acp:test"); len(remaining) != 0 {
		t.Fatalf("expected no remaining bindings, got %+v", remaining)
	}
}
