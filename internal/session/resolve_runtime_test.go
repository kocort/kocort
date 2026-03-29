package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
)

func TestSessionStoreResolveUsesProvidedChannelForDirectSessions(t *testing.T) {
	store, err := NewSessionStore(t.TempDir())
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	sess, err := store.Resolve(context.Background(), "main", "", "", "peer-1", "telegram")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sess.SessionKey != BuildDirectSessionKey("main", "telegram", "peer-1") {
		t.Fatalf("unexpected session key: %s", sess.SessionKey)
	}
}

func TestSessionStoreResolveForRequestSupportsThreadAndGroupKeys(t *testing.T) {
	store := storeForRuntimeTests(t)
	thread, err := store.ResolveForRequest(context.Background(), SessionResolveOptions{
		AgentID:  "main",
		Channel:  "discord",
		To:       "room-1",
		ThreadID: "thread-9",
		ChatType: core.ChatTypeThread,
		MainKey:  DefaultMainKey,
		DMScope:  "per-channel-peer",
		Now:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("resolve thread: %v", err)
	}
	if thread.SessionKey != "agent:main:discord:direct:room-1:thread:thread-9" {
		t.Fatalf("unexpected thread session key: %q", thread.SessionKey)
	}
	group, err := store.ResolveForRequest(context.Background(), SessionResolveOptions{
		AgentID:  "main",
		Channel:  "slack",
		To:       "channel-1",
		ChatType: core.ChatTypeGroup,
		MainKey:  "desk",
		Now:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("resolve group: %v", err)
	}
	if group.SessionKey != "agent:main:slack:group:channel-1" {
		t.Fatalf("unexpected group session key: %q", group.SessionKey)
	}
}

func TestSessionStoreResolveForRequestMapsDirectChatsToMainByDefault(t *testing.T) {
	store := storeForRuntimeTests(t)
	direct, err := store.ResolveForRequest(context.Background(), SessionResolveOptions{
		AgentID:  "main",
		Channel:  "feishu",
		To:       "ou_user_1",
		ChatType: core.ChatTypeDirect,
		MainKey:  "main",
		DMScope:  "main",
		Now:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("resolve direct: %v", err)
	}
	if direct.SessionKey != BuildMainSessionKey("main") {
		t.Fatalf("expected main session key, got %q", direct.SessionKey)
	}
}

func TestSessionStoreResolveForRequestSupportsPerChannelPeerDMScope(t *testing.T) {
	store := storeForRuntimeTests(t)
	direct, err := store.ResolveForRequest(context.Background(), SessionResolveOptions{
		AgentID:  "main",
		Channel:  "feishu",
		To:       "ou_user_1",
		ChatType: core.ChatTypeDirect,
		MainKey:  "main",
		DMScope:  "per-channel-peer",
		Now:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("resolve direct: %v", err)
	}
	if direct.SessionKey != BuildDirectSessionKey("main", "feishu", "ou_user_1") {
		t.Fatalf("expected peer session key, got %q", direct.SessionKey)
	}
}

func TestSessionStoreResolveForRequestRollsOverOnIdleReset(t *testing.T) {
	store := storeForRuntimeTests(t)
	key := BuildMainSessionKey("main")
	if err := store.Upsert(key, core.SessionEntry{
		SessionID:          "sess_old",
		SessionFile:        filepath.Join(store.BaseDir(), "transcripts", "sess_old.jsonl"),
		UpdatedAt:          time.Now().UTC().Add(-2 * time.Hour),
		LastActivityReason: "turn",
	}); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(store.BaseDir(), "transcripts"), 0o755); err != nil {
		t.Fatalf("mkdir transcripts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.BaseDir(), "transcripts", "sess_old.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	resolution, err := store.ResolveForRequest(context.Background(), SessionResolveOptions{
		AgentID: "main",
		MainKey: DefaultMainKey,
		Now:     time.Now().UTC(),
		ResetPolicy: SessionFreshnessPolicy{
			Mode:        "idle",
			IdleMinutes: 30,
		},
	})
	if err != nil {
		t.Fatalf("resolve rolled session: %v", err)
	}
	if resolution.SessionID == "sess_old" || resolution.Fresh {
		t.Fatalf("expected fresh rollover session, got %+v", resolution)
	}
	entry := store.Entry(key)
	if entry == nil || entry.ResetReason != "idle" || entry.SessionID == "sess_old" {
		t.Fatalf("expected rolled store entry, got %+v", entry)
	}
	matches, err := filepath.Glob(filepath.Join(store.BaseDir(), "transcripts", "sess_old.jsonl.idle.*"))
	if err != nil {
		t.Fatalf("glob archive: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected archived transcript, got %v", matches)
	}
}

func TestEvaluateSessionFreshnessUsesEarlierIdleExpiryWhenDailyAlsoConfigured(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)
	updatedAt := time.Date(2026, 3, 12, 10, 0, 0, 0, loc)
	now := time.Date(2026, 3, 12, 10, 45, 0, 0, loc)
	freshness := EvaluateSessionFreshness(updatedAt, now, SessionFreshnessPolicy{
		Mode:        "daily",
		AtHour:      23,
		IdleMinutes: 30,
	})
	if freshness.Fresh || freshness.Reason != "idle" {
		t.Fatalf("expected idle expiry to win, got %+v", freshness)
	}
}

func TestEvaluateSessionFreshnessUsesEarlierDailyExpiryWhenIdleAlsoConfigured(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)
	updatedAt := time.Date(2026, 3, 11, 3, 0, 0, 0, loc)
	now := time.Date(2026, 3, 11, 5, 0, 0, 0, loc)
	freshness := EvaluateSessionFreshness(updatedAt, now, SessionFreshnessPolicy{
		Mode:        "daily",
		AtHour:      4,
		IdleMinutes: 180,
	})
	if freshness.Fresh || freshness.Reason != "daily" {
		t.Fatalf("expected daily expiry to win, got %+v", freshness)
	}
}

func TestSessionStoreMaintenancePrunesStaleEntries(t *testing.T) {
	baseDir := t.TempDir()
	store, err := NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	store.SetMaintenanceConfig(SessionStoreMaintenanceConfig{
		Mode:                  "enforce",
		PruneAfter:            24 * time.Hour,
		MaxEntries:            100,
		RotateBytes:           1024 * 1024,
		ResetArchiveRetention: 30 * 24 * time.Hour,
	})

	sessionKey := BuildMainSessionKey("main")
	if err := store.Upsert(sessionKey, core.SessionEntry{SessionID: "sess-old"}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if err := store.AppendTranscript(sessionKey, "sess-old", core.TranscriptMessage{Role: "user", Text: "old"}); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	oldEntry := store.Entry(sessionKey)
	oldEntry.UpdatedAt = time.Now().UTC().Add(-48 * time.Hour)
	if err := store.Upsert(sessionKey, *oldEntry); err != nil {
		t.Fatalf("mark stale session: %v", err)
	}

	if got := store.Entry(sessionKey); got != nil {
		t.Fatalf("expected stale session pruned, got %+v", got)
	}
	matches, err := filepath.Glob(filepath.Join(baseDir, "transcripts", "*.deleted.*"))
	if err != nil {
		t.Fatalf("glob deleted transcript: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("expected deleted transcript archive, got %v", matches)
	}
}

func TestSessionStoreMaintenanceCapsMaxEntries(t *testing.T) {
	baseDir := t.TempDir()
	store, err := NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	store.SetMaintenanceConfig(SessionStoreMaintenanceConfig{
		Mode:                  "enforce",
		PruneAfter:            365 * 24 * time.Hour,
		MaxEntries:            2,
		RotateBytes:           1024 * 1024,
		ResetArchiveRetention: 30 * 24 * time.Hour,
	})

	for idx := range []int{0, 1, 2} {
		key := BuildDirectSessionKey("main", "webchat", fmt.Sprintf("peer-%d", idx))
		if err := store.Upsert(key, core.SessionEntry{
			SessionID:   fmt.Sprintf("sess-%d", idx),
			UpdatedAt:   time.Now().UTC().Add(time.Duration(idx) * time.Minute),
			SessionFile: "",
		}); err != nil {
			t.Fatalf("upsert session %d: %v", idx, err)
		}
	}

	items := store.ListSessions()
	if len(items) != 2 {
		t.Fatalf("expected maxEntries=2 to keep 2 sessions, got %d", len(items))
	}
	if store.Entry(BuildDirectSessionKey("main", "webchat", "peer-0")) != nil {
		t.Fatalf("expected oldest session pruned")
	}
}

func TestSessionStoreMaintenancePurgesExpiredTranscriptArchives(t *testing.T) {
	baseDir := t.TempDir()
	store, err := NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	store.SetMaintenanceConfig(SessionStoreMaintenanceConfig{
		Mode:                  "enforce",
		PruneAfter:            365 * 24 * time.Hour,
		MaxEntries:            100,
		RotateBytes:           1024 * 1024,
		ResetArchiveRetention: 24 * time.Hour,
	})

	transcriptsDir := filepath.Join(baseDir, "transcripts")
	if err := os.MkdirAll(transcriptsDir, 0o755); err != nil {
		t.Fatalf("mkdir transcripts: %v", err)
	}
	archived := filepath.Join(transcriptsDir, "sess-old.jsonl.reset.2026-03-01T00-00-00.000Z")
	if err := os.WriteFile(archived, []byte("old"), 0o600); err != nil {
		t.Fatalf("write archived transcript: %v", err)
	}
	oldTime := time.Now().UTC().Add(-48 * time.Hour)
	if err := os.Chtimes(archived, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes archived transcript: %v", err)
	}

	if err := store.Upsert(BuildMainSessionKey("main"), core.SessionEntry{SessionID: "sess-main"}); err != nil {
		t.Fatalf("trigger maintenance flush: %v", err)
	}
	if _, err := os.Stat(archived); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected expired archived transcript removed, stat err=%v", err)
	}
}

func TestSessionStoreMaintenanceRotatesOversizedStore(t *testing.T) {
	baseDir := t.TempDir()
	store, err := NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	store.SetMaintenanceConfig(SessionStoreMaintenanceConfig{
		Mode:                  "enforce",
		PruneAfter:            365 * 24 * time.Hour,
		MaxEntries:            100,
		RotateBytes:           64,
		ResetArchiveRetention: 30 * 24 * time.Hour,
	})

	if err := store.Upsert(BuildDirectSessionKey("main", "webchat", "first"), core.SessionEntry{
		SessionID: "sess-first",
		Label:     strings.Repeat("a", 128),
	}); err != nil {
		t.Fatalf("upsert first session: %v", err)
	}
	if err := store.Upsert(BuildDirectSessionKey("main", "webchat", "second"), core.SessionEntry{
		SessionID: "sess-second",
		Label:     strings.Repeat("b", 128),
	}); err != nil {
		t.Fatalf("upsert second session: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(baseDir, "sessions.json.*"))
	if err != nil {
		t.Fatalf("glob rotated store: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("expected rotated sessions store, got %v", matches)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "sessions.json")); err != nil {
		t.Fatalf("expected fresh sessions.json after rotation: %v", err)
	}
}

func storeForRuntimeTests(t *testing.T) *SessionStore {
	t.Helper()
	store, err := NewSessionStore(t.TempDir())
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	return store
}


