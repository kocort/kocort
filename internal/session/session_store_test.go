package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestStore(t *testing.T) *SessionStore {
	t.Helper()
	dir := t.TempDir()
	store, err := NewSessionStore(dir)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}
	return store
}

// ---------------------------------------------------------------------------
// NewSessionStore
// ---------------------------------------------------------------------------

func TestNewSessionStore(t *testing.T) {
	store := newTestStore(t)
	if store == nil {
		t.Fatal("expected non-nil store")
	}
	if store.BaseDir() == "" {
		t.Error("expected non-empty BaseDir")
	}
}

func TestNewSessionStoreLoadsExistingState(t *testing.T) {
	dir := t.TempDir()
	// Create initial store and add an entry.
	store1, err := NewSessionStore(dir)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	err = store1.Upsert("agent:main:main", core.SessionEntry{SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Create a new store from same dir — should see the entry.
	store2, err := NewSessionStore(dir)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	entry := store2.Entry("agent:main:main")
	if entry == nil {
		t.Fatal("expected to find persisted entry")
	}
	if entry.SessionID != "sess_1" {
		t.Errorf("got sessionID=%q, want sess_1", entry.SessionID)
	}
}

// ---------------------------------------------------------------------------
// Resolve / ResolveForRequest
// ---------------------------------------------------------------------------

func TestResolveForRequestCreatesNewSession(t *testing.T) {
	store := newTestStore(t)
	res, err := store.ResolveForRequest(context.Background(), SessionResolveOptions{
		AgentID:    "main",
		SessionKey: "agent:main:main",
		Now:        time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !res.IsNew {
		t.Error("expected IsNew=true for first resolve")
	}
	if res.SessionKey != "agent:main:main" {
		t.Errorf("expected sessionKey=agent:main:main, got %q", res.SessionKey)
	}
	if res.SessionID == "" {
		t.Error("expected non-empty sessionID")
	}
}

func TestResolveForRequestReturnsExisting(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()
	err := store.Upsert("agent:main:main", core.SessionEntry{
		SessionID: "sess_existing",
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	res, err := store.ResolveForRequest(context.Background(), SessionResolveOptions{
		AgentID:    "main",
		SessionKey: "agent:main:main",
		Now:        now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.IsNew {
		t.Error("expected IsNew=false for existing session")
	}
	if res.SessionID != "sess_existing" {
		t.Errorf("expected sessionID=sess_existing, got %q", res.SessionID)
	}
}

func TestResolveForRequestResetsStaleSession(t *testing.T) {
	store := newTestStore(t)
	past := time.Now().UTC().Add(-24 * time.Hour)
	err := store.Upsert("agent:main:main", core.SessionEntry{
		SessionID: "sess_old",
		UpdatedAt: past,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	res, err := store.ResolveForRequest(context.Background(), SessionResolveOptions{
		AgentID:    "main",
		SessionKey: "agent:main:main",
		Now:        time.Now().UTC(),
		ResetPolicy: SessionFreshnessPolicy{
			Mode:        "idle",
			IdleMinutes: 30,
		},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !res.IsNew {
		t.Error("expected IsNew=true after stale reset")
	}
	if res.SessionID == "sess_old" {
		t.Error("expected new session ID after reset")
	}
}

func TestResolveForRequestForksNewThreadSessionFromParent(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()
	parentKey := "agent:main:discord:direct:room-1"
	if err := store.Upsert(parentKey, core.SessionEntry{
		SessionID:        "sess_parent",
		ThinkingLevel:    "high",
		ProviderOverride: "openai",
		ModelOverride:    "gpt-4.1",
		UpdatedAt:        now,
	}); err != nil {
		t.Fatalf("upsert parent: %v", err)
	}
	if err := store.AppendTranscript(parentKey, "sess_parent", core.TranscriptMessage{
		Role:      "user",
		Text:      "parent context",
		Timestamp: now,
	}); err != nil {
		t.Fatalf("append parent transcript: %v", err)
	}

	res, err := store.ResolveForRequest(context.Background(), SessionResolveOptions{
		AgentID:  "main",
		Channel:  "discord",
		To:       "room-1",
		ThreadID: "thread-9",
		ChatType: core.ChatTypeThread,
		MainKey:  DefaultMainKey,
		DMScope:  "per-channel-peer",
		Now:      now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("resolve thread fork: %v", err)
	}
	if !res.IsNew || res.Entry == nil || res.Entry.ThinkingLevel != "high" || res.Entry.ModelOverride != "gpt-4.1" {
		t.Fatalf("expected forked thread entry, got %+v", res)
	}
	if !res.Entry.ForkedFromParent {
		t.Fatalf("expected fork marker on forked thread entry, got %+v", res.Entry)
	}
	history, err := store.LoadTranscript(res.SessionKey)
	if err != nil {
		t.Fatalf("load forked transcript: %v", err)
	}
	if len(history) != 1 || history[0].Text != "parent context" {
		t.Fatalf("expected forked transcript, got %+v", history)
	}
}

func TestResolveForRequestSkipsParentForkWhenParentTooLarge(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()
	parentKey := "agent:main:discord:direct:room-1"
	if err := store.Upsert(parentKey, core.SessionEntry{
		SessionID:     "sess_parent",
		ContextTokens: DefaultParentForkMaxTokens + 1,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("upsert parent: %v", err)
	}
	res, err := store.ResolveForRequest(context.Background(), SessionResolveOptions{
		AgentID:             "main",
		Channel:             "discord",
		To:                  "room-1",
		ThreadID:            "thread-9",
		ChatType:            core.ChatTypeThread,
		MainKey:             DefaultMainKey,
		DMScope:             "per-channel-peer",
		ParentForkMaxTokens: DefaultParentForkMaxTokens,
		Now:                 now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("resolve thread without fork: %v", err)
	}
	if res.Entry == nil || !res.Entry.ForkedFromParent {
		t.Fatalf("expected fork-skip marker entry, got %+v", res.Entry)
	}
	history, err := store.LoadTranscript(res.SessionKey)
	if err != nil {
		t.Fatalf("load skipped-fork transcript: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("expected no forked transcript when parent too large, got %+v", history)
	}
}

func TestResolveForRequestContextCanceled(t *testing.T) {
	store := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := store.ResolveForRequest(ctx, SessionResolveOptions{
		AgentID:    "main",
		SessionKey: "agent:main:main",
	})
	if err == nil {
		t.Error("expected error for canceled context")
	}
}

// ---------------------------------------------------------------------------
// Upsert
// ---------------------------------------------------------------------------

func TestUpsertCreatesAndUpdates(t *testing.T) {
	store := newTestStore(t)
	// Create
	err := store.Upsert("key1", core.SessionEntry{SessionID: "s1", Label: "first"})
	if err != nil {
		t.Fatalf("upsert create: %v", err)
	}
	entry := store.Entry("key1")
	if entry == nil || entry.SessionID != "s1" || entry.Label != "first" {
		t.Fatalf("unexpected entry after create: %+v", entry)
	}
	// Update
	err = store.Upsert("key1", core.SessionEntry{Label: "second"})
	if err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	entry = store.Entry("key1")
	if entry.Label != "second" {
		t.Errorf("expected label=second, got %q", entry.Label)
	}
	if entry.SessionID != "s1" {
		t.Errorf("merge should preserve sessionID, got %q", entry.SessionID)
	}
}

// ---------------------------------------------------------------------------
// Mutate
// ---------------------------------------------------------------------------

func TestMutate(t *testing.T) {
	store := newTestStore(t)
	_ = store.Upsert("key1", core.SessionEntry{SessionID: "s1"})

	err := store.Mutate("key1", func(e *core.SessionEntry) error {
		e.ThinkingLevel = "high"
		return nil
	})
	if err != nil {
		t.Fatalf("mutate: %v", err)
	}
	entry := store.Entry("key1")
	if entry.ThinkingLevel != "high" {
		t.Errorf("expected thinkingLevel=high, got %q", entry.ThinkingLevel)
	}
}

// ---------------------------------------------------------------------------
// AppendTranscript / LoadTranscript
// ---------------------------------------------------------------------------

func TestAppendAndLoadTranscript(t *testing.T) {
	store := newTestStore(t)
	_ = store.Upsert("key1", core.SessionEntry{SessionID: "s1"})

	msgs := []core.TranscriptMessage{
		{Role: "user", Text: "hello"},
		{Role: "assistant", Text: "hi there"},
	}
	err := store.AppendTranscript("key1", "s1", msgs...)
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	loaded, err := store.LoadTranscript("key1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded))
	}
	if loaded[0].Role != "user" || loaded[0].Text != "hello" {
		t.Errorf("unexpected first message: %+v", loaded[0])
	}
	if loaded[1].Role != "assistant" || loaded[1].Text != "hi there" {
		t.Errorf("unexpected second message: %+v", loaded[1])
	}
}

func TestLoadTranscriptMissingSession(t *testing.T) {
	store := newTestStore(t)
	msgs, err := store.LoadTranscript("nonexistent")
	if err != nil {
		t.Fatalf("expected nil error for missing session, got %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected empty transcript, got %d messages", len(msgs))
	}
}

// ---------------------------------------------------------------------------
// RewriteTranscript
// ---------------------------------------------------------------------------

func TestRewriteTranscript(t *testing.T) {
	store := newTestStore(t)
	_ = store.Upsert("key1", core.SessionEntry{SessionID: "s1"})
	_ = store.AppendTranscript("key1", "s1",
		core.TranscriptMessage{Role: "user", Text: "first"},
		core.TranscriptMessage{Role: "assistant", Text: "response"},
	)

	// Rewrite with a single compacted message.
	err := store.RewriteTranscript("key1", "s1", []core.TranscriptMessage{
		{Type: "compaction", Summary: "compacted history"},
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	loaded, err := store.LoadTranscript("key1")
	if err != nil {
		t.Fatalf("load after rewrite: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 message after rewrite, got %d", len(loaded))
	}
	if loaded[0].Type != "compaction" {
		t.Errorf("expected compaction message, got type=%q", loaded[0].Type)
	}
}

// ---------------------------------------------------------------------------
// ListSessions
// ---------------------------------------------------------------------------

func TestListSessions(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()
	_ = store.Upsert("key1", core.SessionEntry{SessionID: "s1", UpdatedAt: now.Add(-time.Hour)})
	_ = store.Upsert("key2", core.SessionEntry{SessionID: "s2", UpdatedAt: now})

	list := store.ListSessions()
	if len(list) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(list))
	}
	// Should be sorted by UpdatedAt descending.
	if list[0].Key != "key2" {
		t.Errorf("expected most recent first, got %q", list[0].Key)
	}
}

func TestListSessionsIncludesChildRelationships(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()
	_ = store.Upsert("parent", core.SessionEntry{SessionID: "sp", UpdatedAt: now.Add(-2 * time.Minute)})
	_ = store.Upsert("child-a", core.SessionEntry{SessionID: "sca", SpawnedBy: "parent", UpdatedAt: now.Add(-time.Minute)})
	_ = store.Upsert("child-b", core.SessionEntry{SessionID: "scb", SpawnedBy: "parent", UpdatedAt: now})

	list := store.ListSessions()
	byKey := map[string]SessionListItem{}
	for _, item := range list {
		byKey[item.Key] = item
	}
	parent := byKey["parent"]
	if len(parent.ChildSessions) != 2 || parent.ChildSessions[0] != "child-b" || parent.ChildSessions[1] != "child-a" {
		t.Fatalf("expected parent child sessions ordered by recency, got %+v", parent.ChildSessions)
	}
	if byKey["child-a"].ParentSessionKey != "parent" || byKey["child-b"].ParentSessionKey != "parent" {
		t.Fatalf("expected child parent session key to be populated, got %+v %+v", byKey["child-a"], byKey["child-b"])
	}
}

// ---------------------------------------------------------------------------
// ResolveSessionKeyReference
// ---------------------------------------------------------------------------

func TestResolveSessionKeyReference(t *testing.T) {
	store := newTestStore(t)
	_ = store.Upsert("agent:main:main", core.SessionEntry{
		SessionID: "sess_abc",
		Label:     "My Session",
	})

	// Match by key
	key, ok := store.ResolveSessionKeyReference("agent:main:main")
	if !ok || key != "agent:main:main" {
		t.Errorf("expected match by key, got %q, %v", key, ok)
	}

	// Match by session ID
	key, ok = store.ResolveSessionKeyReference("sess_abc")
	if !ok || key != "agent:main:main" {
		t.Errorf("expected match by sessionID, got %q, %v", key, ok)
	}

	// Match by label
	key, ok = store.ResolveSessionKeyReference("My Session")
	if !ok || key != "agent:main:main" {
		t.Errorf("expected match by label, got %q, %v", key, ok)
	}

	// No match
	_, ok = store.ResolveSessionKeyReference("nonexistent")
	if ok {
		t.Error("expected no match for nonexistent reference")
	}
}

// ---------------------------------------------------------------------------
// ResolveSessionLabel
// ---------------------------------------------------------------------------

func TestResolveSessionLabel(t *testing.T) {
	store := newTestStore(t)
	_ = store.Upsert("agent:main:main", core.SessionEntry{
		SessionID: "s1",
		Label:     "deploy-session",
		SpawnedBy: "parent-key",
	})

	key, ok := store.ResolveSessionLabel("main", "deploy-session", "")
	if !ok || key != "agent:main:main" {
		t.Errorf("expected match, got %q, %v", key, ok)
	}

	key, ok = store.ResolveSessionLabel("main", "deploy-session", "parent-key")
	if !ok || key != "agent:main:main" {
		t.Errorf("expected match with spawnedBy, got %q, %v", key, ok)
	}

	_, ok = store.ResolveSessionLabel("main", "deploy-session", "other-parent")
	if ok {
		t.Error("expected no match with wrong spawnedBy")
	}
}

// ---------------------------------------------------------------------------
// IsSpawnedSessionVisible
// ---------------------------------------------------------------------------

func TestIsSpawnedSessionVisible(t *testing.T) {
	store := newTestStore(t)
	_ = store.Upsert("parent", core.SessionEntry{SessionID: "sp"})
	_ = store.Upsert("child", core.SessionEntry{SessionID: "sc", SpawnedBy: "parent"})
	_ = store.Upsert("grandchild", core.SessionEntry{SessionID: "sg", SpawnedBy: "child"})

	if !store.IsSpawnedSessionVisible("parent", "child") {
		t.Error("parent should see child")
	}
	if !store.IsSpawnedSessionVisible("parent", "grandchild") {
		t.Error("parent should see grandchild")
	}
	if store.IsSpawnedSessionVisible("child", "parent") {
		t.Error("child should not see parent")
	}
	if !store.IsSpawnedSessionVisible("child", "child") {
		t.Error("same session should always be visible")
	}
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

func TestDelete(t *testing.T) {
	store := newTestStore(t)
	_ = store.Upsert("key1", core.SessionEntry{SessionID: "s1"})
	_ = store.AppendTranscript("key1", "s1", core.TranscriptMessage{Role: "user", Text: "hi"})

	err := store.Delete("key1")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if entry := store.Entry("key1"); entry != nil {
		t.Error("expected nil entry after delete")
	}
}

// ---------------------------------------------------------------------------
// Reset
// ---------------------------------------------------------------------------

func TestReset(t *testing.T) {
	store := newTestStore(t)
	_ = store.Upsert("key1", core.SessionEntry{SessionID: "s1"})
	_ = store.AppendTranscript("key1", "s1", core.TranscriptMessage{Role: "user", Text: "hi"})

	newID, err := store.Reset("key1", "daily")
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if newID == "s1" {
		t.Error("expected new session ID after reset")
	}
	entry := store.Entry("key1")
	if entry == nil {
		t.Fatal("expected entry to still exist after reset")
	}
	if entry.SessionID != newID {
		t.Errorf("expected sessionID=%q, got %q", newID, entry.SessionID)
	}
	if entry.ResetReason != "daily" {
		t.Errorf("expected resetReason=daily, got %q", entry.ResetReason)
	}
}

// ---------------------------------------------------------------------------
// AllEntries
// ---------------------------------------------------------------------------

func TestAllEntries(t *testing.T) {
	store := newTestStore(t)
	_ = store.Upsert("k1", core.SessionEntry{SessionID: "s1"})
	_ = store.Upsert("k2", core.SessionEntry{SessionID: "s2"})

	entries := store.AllEntries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if _, ok := entries["k1"]; !ok {
		t.Error("expected k1 in snapshot")
	}
}

// ---------------------------------------------------------------------------
// EvaluateSessionFreshness
// ---------------------------------------------------------------------------

func TestEvaluateSessionFreshness(t *testing.T) {
	now := time.Now().UTC()

	t.Run("fresh_within_idle", func(t *testing.T) {
		result := EvaluateSessionFreshness(now.Add(-5*time.Minute), now, SessionFreshnessPolicy{
			Mode:        "idle",
			IdleMinutes: 30,
		})
		if !result.Fresh {
			t.Error("expected fresh within idle window")
		}
	})

	t.Run("stale_past_idle", func(t *testing.T) {
		result := EvaluateSessionFreshness(now.Add(-60*time.Minute), now, SessionFreshnessPolicy{
			Mode:        "idle",
			IdleMinutes: 30,
		})
		if result.Fresh {
			t.Error("expected stale past idle window")
		}
		if result.Reason != "idle" {
			t.Errorf("expected reason=idle, got %q", result.Reason)
		}
	})

	t.Run("zero_updated_at", func(t *testing.T) {
		result := EvaluateSessionFreshness(time.Time{}, now, SessionFreshnessPolicy{})
		if result.Fresh {
			t.Error("expected not fresh for zero updatedAt")
		}
		if result.Reason != "missing" {
			t.Errorf("expected reason=missing, got %q", result.Reason)
		}
	})

	t.Run("no_policy_always_fresh", func(t *testing.T) {
		result := EvaluateSessionFreshness(now.Add(-1000*time.Hour), now, SessionFreshnessPolicy{})
		if !result.Fresh {
			t.Error("expected fresh when no policy set")
		}
	})
}

// ---------------------------------------------------------------------------
// MergeSessionEntry
// ---------------------------------------------------------------------------

func TestMergeSessionEntry(t *testing.T) {
	existing := core.SessionEntry{
		SessionID:     "s1",
		Label:         "old-label",
		ThinkingLevel: "high",
		LastChannel:   "slack",
	}
	next := core.SessionEntry{
		Label:       "new-label",
		LastChannel: "",
	}
	merged := MergeSessionEntry(existing, next)
	if merged.SessionID != "s1" {
		t.Errorf("expected sessionID preserved, got %q", merged.SessionID)
	}
	if merged.Label != "new-label" {
		t.Errorf("expected new label, got %q", merged.Label)
	}
	if merged.ThinkingLevel != "high" {
		t.Errorf("expected thinkingLevel preserved, got %q", merged.ThinkingLevel)
	}
	if merged.LastChannel != "slack" {
		t.Errorf("expected lastChannel preserved when next is empty, got %q", merged.LastChannel)
	}
}

// ---------------------------------------------------------------------------
// NormalizeTranscriptMessageForWrite
// ---------------------------------------------------------------------------

func TestNormalizeTranscriptMessageForWrite(t *testing.T) {
	t.Run("fills_id_and_timestamp", func(t *testing.T) {
		msg := NormalizeTranscriptMessageForWrite(core.TranscriptMessage{
			Role: "user",
			Text: "hello",
		})
		if msg.ID == "" {
			t.Error("expected non-empty ID")
		}
		if msg.Timestamp.IsZero() {
			t.Error("expected non-zero timestamp")
		}
	})

	t.Run("compaction_fills_summary_from_text", func(t *testing.T) {
		msg := NormalizeTranscriptMessageForWrite(core.TranscriptMessage{
			Type: "compaction",
			Text: "summary content",
		})
		if msg.Summary != "summary content" {
			t.Errorf("expected summary to be filled from text, got %q", msg.Summary)
		}
	})

	t.Run("fills_text_from_summary", func(t *testing.T) {
		msg := NormalizeTranscriptMessageForWrite(core.TranscriptMessage{
			Summary: "some summary",
		})
		if msg.Text != "some summary" {
			t.Errorf("expected text filled from summary, got %q", msg.Text)
		}
	})
}

// ---------------------------------------------------------------------------
// IsTranscriptArchiveName
// ---------------------------------------------------------------------------

func TestIsTranscriptArchiveName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"sess_abc.jsonl.reset.2025-01-01", true},
		{"sess_abc.jsonl.deleted.2025-01-01", true},
		{"sess_abc.jsonl", false},
		{"sess_abc.jsonl.tmp", false},
		{"plain.json", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTranscriptArchiveName(tt.name); got != tt.want {
				t.Errorf("IsTranscriptArchiveName(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ArchiveTranscriptFile
// ---------------------------------------------------------------------------

func TestArchiveTranscriptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	archived, err := ArchiveTranscriptFile(path, "reset")
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if archived == "" {
		t.Error("expected non-empty archive path")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("original file should be renamed")
	}
	if _, err := os.Stat(archived); err != nil {
		t.Errorf("archived file should exist: %v", err)
	}
}

func TestArchiveTranscriptFileNonexistent(t *testing.T) {
	archived, err := ArchiveTranscriptFile("/nonexistent/path.jsonl", "reset")
	if err != nil {
		t.Fatalf("expected no error for nonexistent file, got %v", err)
	}
	if archived != "" {
		t.Errorf("expected empty archive path, got %q", archived)
	}
}

func TestArchiveTranscriptFileEmpty(t *testing.T) {
	archived, err := ArchiveTranscriptFile("", "reset")
	if err != nil {
		t.Fatalf("expected no error for empty path, got %v", err)
	}
	if archived != "" {
		t.Errorf("expected empty archive path, got %q", archived)
	}
}

// ---------------------------------------------------------------------------
// SessionStore Maintenance
// ---------------------------------------------------------------------------

func TestSessionStoreMaintenancePrune(t *testing.T) {
	store := newTestStore(t)
	store.SetMaintenanceConfig(SessionStoreMaintenanceConfig{
		Mode:       "enforce",
		PruneAfter: 24 * time.Hour,
		MaxEntries: 100,
	})
	past := time.Now().UTC().Add(-48 * time.Hour)
	_ = store.Upsert("old-key", core.SessionEntry{SessionID: "s1", UpdatedAt: past})
	_ = store.Upsert("recent-key", core.SessionEntry{SessionID: "s2", UpdatedAt: time.Now().UTC()})

	// Trigger maintenance by flushing (e.g., via another upsert).
	_ = store.Upsert("recent-key", core.SessionEntry{SessionID: "s2"})

	if entry := store.Entry("old-key"); entry != nil {
		t.Error("expected old entry to be pruned")
	}
	if entry := store.Entry("recent-key"); entry == nil {
		t.Error("expected recent entry to survive")
	}
}

func TestSessionStoreMaintenanceMaxEntries(t *testing.T) {
	store := newTestStore(t)
	store.SetMaintenanceConfig(SessionStoreMaintenanceConfig{
		Mode:       "enforce",
		PruneAfter: 999 * 24 * time.Hour,
		MaxEntries: 2,
	})
	now := time.Now().UTC()
	_ = store.Upsert("k1", core.SessionEntry{SessionID: "s1", UpdatedAt: now.Add(-3 * time.Hour)})
	_ = store.Upsert("k2", core.SessionEntry{SessionID: "s2", UpdatedAt: now.Add(-2 * time.Hour)})
	_ = store.Upsert("k3", core.SessionEntry{SessionID: "s3", UpdatedAt: now.Add(-1 * time.Hour)})

	entries := store.AllEntries()
	if len(entries) > 2 {
		t.Errorf("expected at most 2 entries after cap enforcement, got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// NextDailyResetAfter
// ---------------------------------------------------------------------------

func TestNextDailyResetAfter(t *testing.T) {
	loc := time.UTC
	updatedAt := time.Date(2025, 3, 15, 10, 0, 0, 0, loc)

	// atHour=0 → next boundary is 2025-03-16 00:00
	boundary := NextDailyResetAfter(updatedAt, loc, 0)
	expected := time.Date(2025, 3, 16, 0, 0, 0, 0, loc)
	if !boundary.Equal(expected) {
		t.Errorf("got %v, want %v", boundary, expected)
	}

	// atHour=10 → next boundary is still 2025-03-16 10:00 (since updatedAt is at 10:00)
	boundary = NextDailyResetAfter(updatedAt, loc, 10)
	expected = time.Date(2025, 3, 16, 10, 0, 0, 0, loc)
	if !boundary.Equal(expected) {
		t.Errorf("got %v, want %v", boundary, expected)
	}

	// atHour=11 → next boundary is 2025-03-15 11:00 (same day, later hour)
	boundary = NextDailyResetAfter(updatedAt, loc, 11)
	expected = time.Date(2025, 3, 15, 11, 0, 0, 0, loc)
	if !boundary.Equal(expected) {
		t.Errorf("got %v, want %v", boundary, expected)
	}
}
