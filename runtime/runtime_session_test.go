package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/task"
	"github.com/kocort/kocort/internal/tool"
)

func TestSessionStoreResolveUsesProvidedChannelForDirectSessions(t *testing.T) {
	store, err := session.NewSessionStore(t.TempDir())
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	sess, err := store.Resolve(context.Background(), "main", "", "", "peer-1", "telegram")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sess.SessionKey != session.BuildDirectSessionKey("main", "telegram", "peer-1") {
		t.Fatalf("unexpected session key: %s", sess.SessionKey)
	}
}

func TestChatSendReusesPersistentBoundChildSession(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	childSessionKey := "agent:worker:subagent:bound-child"
	if err := store.Upsert(childSessionKey, core.SessionEntry{
		SessionID: "sess_bound",
		SpawnedBy: session.BuildMainSessionKey("main"),
		SpawnMode: "session",
		UpdatedAt: time.Now().UTC(),
		DeliveryContext: &core.DeliveryContext{
			Channel:  "slack",
			To:       "room-1",
			ThreadID: "thread-9",
		},
	}); err != nil {
		t.Fatalf("upsert child session: %v", err)
	}
	var captured core.AgentRunRequest
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main":   {ID: "main", DefaultProvider: "openai", DefaultModel: "gpt-4.1"},
			"worker": {ID: "worker", DefaultProvider: "openai", DefaultModel: "gpt-4.1-mini"},
		}),
		Memory: infra.NullMemoryProvider{},
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			captured = runCtx.Request
			return core.AgentRunResult{RunID: "run-bound", Payloads: []core.ReplyPayload{{Text: "bound ok"}}}, nil
		}},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(),
	}
	resp, err := runtime.ChatSend(context.Background(), core.ChatSendRequest{
		AgentID:  "main",
		Message:  "follow up",
		Channel:  "slack",
		To:       "room-1",
		ThreadID: "thread-9",
		ChatType: core.ChatTypeThread,
	})
	if err != nil {
		t.Fatalf("chat send: %v", err)
	}
	if resp.SessionKey != childSessionKey {
		t.Fatalf("expected bound child session key, got %s", resp.SessionKey)
	}
	if captured.SessionKey != childSessionKey || captured.AgentID != "worker" {
		t.Fatalf("expected run routed to persistent child session, got %+v", captured)
	}
}

func TestSessionsListAndHistoryToolsReadStoreAndTranscript(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	sessionKey := session.BuildDirectSessionKey("main", "telegram", "peer-9")
	if err := store.Upsert(sessionKey, core.SessionEntry{
		SessionID:     "sess_test",
		Label:         "peer-9",
		LastChannel:   "telegram",
		LastTo:        "peer-9",
		ThinkingLevel: "high",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := store.AppendTranscript(sessionKey, "sess_test",
		core.TranscriptMessage{Role: "user", Text: "hello", Timestamp: time.Now().UTC()},
		core.TranscriptMessage{Role: "assistant", Text: "world", Timestamp: time.Now().UTC()},
	); err != nil {
		t.Fatalf("append transcript: %v", err)
	}

	runtime := &Runtime{
		Sessions:   store,
		Identities: infra.NewStaticIdentityResolver(nil),
		Memory:     infra.NullMemoryProvider{},
		Policy:     RuntimePolicy{SessionToolsVisibility: core.SessionVisibilityAgent},
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			return core.AgentRunResult{}, nil
		}},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewSessionsListTool(), tool.NewSessionsHistoryTool()),
	}

	session, err := runtime.Sessions.Resolve(context.Background(), "main", session.BuildMainSessionKey("main"), "", "", "")
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	identity, _ := runtime.Identities.Resolve(context.Background(), "main")
	runCtx := rtypes.AgentRunContext{
		Runtime:      runtime,
		Request:      core.AgentRunRequest{AgentID: "main", SessionKey: session.SessionKey},
		Session:      session,
		Identity:     identity,
		WorkspaceDir: identity.WorkspaceDir,
	}

	listResult, err := runtime.ExecuteTool(context.Background(), runCtx, "sessions_list", map[string]any{"limit": float64(10)})
	if err != nil {
		t.Fatalf("sessions_list: %v", err)
	}
	if !strings.Contains(listResult.Text, sessionKey) {
		t.Fatalf("expected session in list result, got %s", listResult.Text)
	}

	historyResult, err := runtime.ExecuteTool(context.Background(), runCtx, "sessions_history", map[string]any{"sessionKey": sessionKey, "limit": float64(10)})
	if err != nil {
		t.Fatalf("sessions_history: %v", err)
	}
	if !strings.Contains(historyResult.Text, "\"hello\"") || !strings.Contains(historyResult.Text, "\"world\"") {
		t.Fatalf("expected transcript messages in history result, got %s", historyResult.Text)
	}
}

func TestSessionsListToolIncludesChildSessions(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	parentSessionKey := session.BuildMainSessionKey("main")
	childSessionKey := "agent:main:subagent:child-1"
	if err := store.Upsert(parentSessionKey, core.SessionEntry{SessionID: "sess-parent", UpdatedAt: time.Now().UTC().Add(-time.Minute)}); err != nil {
		t.Fatalf("upsert parent: %v", err)
	}
	if err := store.Upsert(childSessionKey, core.SessionEntry{
		SessionID: "sess-child",
		SpawnedBy: parentSessionKey,
		SpawnMode: "session",
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert child: %v", err)
	}

	rt := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {ID: "main", WorkspaceDir: filepath.Join(baseDir, "workspace"), DefaultProvider: "openai", DefaultModel: "gpt-4.1"},
		}),
		Memory: infra.NullMemoryProvider{},
		Policy: RuntimePolicy{SessionToolsVisibility: core.SessionVisibilityTree},
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			return core.AgentRunResult{}, nil
		}},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewSessionsListTool()),
	}
	resolution, err := rt.Sessions.Resolve(context.Background(), "main", parentSessionKey, "", "", "")
	if err != nil {
		t.Fatalf("resolve parent: %v", err)
	}
	identity, _ := rt.Identities.Resolve(context.Background(), "main")
	runCtx := rtypes.AgentRunContext{
		Runtime:      rt,
		Request:      core.AgentRunRequest{AgentID: "main", SessionKey: parentSessionKey},
		Session:      resolution,
		Identity:     identity,
		WorkspaceDir: identity.WorkspaceDir,
	}

	result, err := rt.ExecuteTool(context.Background(), runCtx, "sessions_list", map[string]any{"limit": float64(10)})
	if err != nil {
		t.Fatalf("sessions_list: %v", err)
	}
	var payload struct {
		Count    int                       `json:"count"`
		Sessions []session.SessionListItem `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(result.Text), &payload); err != nil {
		t.Fatalf("unmarshal sessions_list result: %v; body=%s", err, result.Text)
	}
	var foundParent *session.SessionListItem
	for i := range payload.Sessions {
		if payload.Sessions[i].Key == parentSessionKey {
			foundParent = &payload.Sessions[i]
			break
		}
	}
	if foundParent == nil {
		t.Fatalf("expected parent session in payload, got %+v", payload.Sessions)
	}
	if len(foundParent.ChildSessions) != 1 || foundParent.ChildSessions[0] != childSessionKey {
		t.Fatalf("expected child session relationship in payload, got %+v", foundParent.ChildSessions)
	}
}

func TestSessionsHistoryAndSendRespectTreeVisibilityAndA2A(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	parentSessionKey := session.BuildMainSessionKey("main")
	sameAgentOther := session.BuildDirectSessionKey("main", "webchat", "peer-1")
	crossAgent := session.BuildDirectSessionKey("worker", "webchat", "peer-2")
	if err := store.Upsert(sameAgentOther, core.SessionEntry{SessionID: "sess_same"}); err != nil {
		t.Fatalf("upsert same-agent session: %v", err)
	}
	if err := store.Upsert(crossAgent, core.SessionEntry{SessionID: "sess_cross"}); err != nil {
		t.Fatalf("upsert cross-agent session: %v", err)
	}
	if err := store.AppendTranscript(sameAgentOther, "sess_same", core.TranscriptMessage{Role: "user", Text: "same", Timestamp: time.Now().UTC()}); err != nil {
		t.Fatalf("append same transcript: %v", err)
	}

	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				WorkspaceDir:    filepath.Join(baseDir, "workspace"),
				ToolAllowlist:   []string{"sessions_history", "sessions_send"},
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1",
			},
			"worker": {
				ID:              "worker",
				WorkspaceDir:    filepath.Join(baseDir, "workspace-worker"),
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1-mini",
			},
		}),
		Memory: infra.NullMemoryProvider{},
		Policy: RuntimePolicy{
			SessionToolsVisibility: core.SessionVisibilityTree,
			AgentToAgent:           core.AgentToAgentPolicy{Enabled: false},
		},
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "ok"}}}, nil
		}},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewSessionsHistoryTool(), tool.NewSessionsSendTool()),
	}
	session, err := runtime.Sessions.Resolve(context.Background(), "main", parentSessionKey, "", "", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	identity, _ := runtime.Identities.Resolve(context.Background(), "main")
	runCtx := rtypes.AgentRunContext{
		Runtime: runtime,
		Request: core.AgentRunRequest{AgentID: "main", SessionKey: parentSessionKey},
		Session: session, Identity: identity, WorkspaceDir: identity.WorkspaceDir,
	}
	historyResult, err := runtime.ExecuteTool(context.Background(), runCtx, "sessions_history", map[string]any{"sessionKey": sameAgentOther})
	if err != nil {
		t.Fatalf("sessions_history execute: %v", err)
	}
	if !strings.Contains(historyResult.Text, `"status":"forbidden"`) {
		t.Fatalf("expected tree visibility to block non-tree session history, got %s", historyResult.Text)
	}
	sendResult, err := runtime.ExecuteTool(context.Background(), runCtx, "sessions_send", map[string]any{"sessionKey": crossAgent, "message": "hello"})
	if err != nil {
		t.Fatalf("sessions_send execute: %v", err)
	}
	if !strings.Contains(sendResult.Text, `"status":"forbidden"`) {
		t.Fatalf("expected cross-agent send to be blocked, got %s", sendResult.Text)
	}
}

func TestSessionsSendAllowsCrossAgentWhenVisibilityAllAndA2AEnabled(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	targetSessionKey := session.BuildDirectSessionKey("worker", "webchat", "peer-2")
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				WorkspaceDir:    filepath.Join(baseDir, "workspace"),
				ToolAllowlist:   []string{"sessions_send"},
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1",
			},
			"worker": {
				ID:              "worker",
				WorkspaceDir:    filepath.Join(baseDir, "workspace-worker"),
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1-mini",
			},
		}),
		Memory: infra.NullMemoryProvider{},
		Policy: RuntimePolicy{
			SessionToolsVisibility: core.SessionVisibilityAll,
			AgentToAgent:           core.AgentToAgentPolicy{Enabled: true, Allow: []string{"main", "worker"}},
		},
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "cross-agent ok"}}}, nil
		}},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewSessionsSendTool()),
	}
	parentSession, err := runtime.Sessions.Resolve(context.Background(), "main", session.BuildMainSessionKey("main"), "", "", "")
	if err != nil {
		t.Fatalf("resolve parent: %v", err)
	}
	identity, _ := runtime.Identities.Resolve(context.Background(), "main")
	runCtx := rtypes.AgentRunContext{
		Runtime: runtime,
		Request: core.AgentRunRequest{AgentID: "main", SessionKey: parentSession.SessionKey, Channel: "webchat"},
		Session: parentSession, Identity: identity, WorkspaceDir: identity.WorkspaceDir,
	}
	result, err := runtime.ExecuteTool(context.Background(), runCtx, "sessions_send", map[string]any{
		"sessionKey": targetSessionKey,
		"message":    "hello",
	})
	if err != nil {
		t.Fatalf("sessions_send: %v", err)
	}
	if !strings.Contains(result.Text, `"reply":"cross-agent ok"`) {
		t.Fatalf("expected allowed cross-agent reply, got %s", result.Text)
	}
}

func TestSessionStatusToolReportsAndUpdatesModelOverride(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	sessionKey := session.BuildMainSessionKey("main")
	if err := store.Upsert(sessionKey, core.SessionEntry{SessionID: "sess_status"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {ID: "main", ToolProfile: "coding", DefaultProvider: "openai", DefaultModel: "gpt-4.1"},
		}),
		Memory: infra.NullMemoryProvider{},
		Policy: RuntimePolicy{SessionToolsVisibility: core.SessionVisibilityAgent},
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			return core.AgentRunResult{}, nil
		}},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewSessionStatusTool()),
	}
	session, _ := runtime.Sessions.Resolve(context.Background(), "main", sessionKey, "", "", "")
	identity, _ := runtime.Identities.Resolve(context.Background(), "main")
	runCtx := rtypes.AgentRunContext{Runtime: runtime, Request: core.AgentRunRequest{AgentID: "main", SessionKey: sessionKey}, Session: session, Identity: identity}
	result, err := runtime.ExecuteTool(context.Background(), runCtx, "session_status", map[string]any{"model": "openai/gpt-4.1-mini"})
	if err != nil {
		t.Fatalf("session_status: %v", err)
	}
	if !strings.Contains(result.Text, `"model":"gpt-4.1-mini"`) {
		t.Fatalf("expected updated model in status, got %s", result.Text)
	}
	entry := runtime.Sessions.Entry(sessionKey)
	if entry == nil || entry.ModelOverride != "gpt-4.1-mini" || entry.ProviderOverride != "openai" {
		t.Fatalf("expected persisted override, got %+v", entry)
	}
}

func TestSessionStoreResolveForRequestSupportsThreadAndGroupKeys(t *testing.T) {
	store := storeForTests(t)
	thread, err := store.ResolveForRequest(context.Background(), session.SessionResolveOptions{
		AgentID:  "main",
		Channel:  "discord",
		To:       "room-1",
		ThreadID: "thread-9",
		ChatType: core.ChatTypeThread,
		MainKey:  session.DefaultMainKey,
		DMScope:  "per-channel-peer",
		Now:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("resolve thread: %v", err)
	}
	if thread.SessionKey != "agent:main:discord:direct:room-1:thread:thread-9" {
		t.Fatalf("unexpected thread session key: %q", thread.SessionKey)
	}
	group, err := store.ResolveForRequest(context.Background(), session.SessionResolveOptions{
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
	store := storeForTests(t)
	direct, err := store.ResolveForRequest(context.Background(), session.SessionResolveOptions{
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
	if direct.SessionKey != session.BuildMainSessionKey("main") {
		t.Fatalf("expected main session key, got %q", direct.SessionKey)
	}
}

func TestSessionStoreResolveForRequestSupportsPerChannelPeerDMScope(t *testing.T) {
	store := storeForTests(t)
	direct, err := store.ResolveForRequest(context.Background(), session.SessionResolveOptions{
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
	if direct.SessionKey != session.BuildDirectSessionKey("main", "feishu", "ou_user_1") {
		t.Fatalf("expected peer session key, got %q", direct.SessionKey)
	}
}

func TestSessionStoreResolveForRequestRollsOverOnIdleReset(t *testing.T) {
	store := storeForTests(t)
	key := session.BuildMainSessionKey("main")
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
	resolution, err := store.ResolveForRequest(context.Background(), session.SessionResolveOptions{
		AgentID: "main",
		MainKey: session.DefaultMainKey,
		Now:     time.Now().UTC(),
		ResetPolicy: session.SessionFreshnessPolicy{
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
	freshness := session.EvaluateSessionFreshness(updatedAt, now, session.SessionFreshnessPolicy{
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
	freshness := session.EvaluateSessionFreshness(updatedAt, now, session.SessionFreshnessPolicy{
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
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	store.SetMaintenanceConfig(session.SessionStoreMaintenanceConfig{
		Mode:                  "enforce",
		PruneAfter:            24 * time.Hour,
		MaxEntries:            100,
		RotateBytes:           1024 * 1024,
		ResetArchiveRetention: 30 * 24 * time.Hour,
	})

	sessionKey := session.BuildMainSessionKey("main")
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
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	store.SetMaintenanceConfig(session.SessionStoreMaintenanceConfig{
		Mode:                  "enforce",
		PruneAfter:            365 * 24 * time.Hour,
		MaxEntries:            2,
		RotateBytes:           1024 * 1024,
		ResetArchiveRetention: 30 * 24 * time.Hour,
	})

	for idx := range []int{0, 1, 2} {
		key := session.BuildDirectSessionKey("main", "webchat", fmt.Sprintf("peer-%d", idx))
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
	if store.Entry(session.BuildDirectSessionKey("main", "webchat", "peer-0")) != nil {
		t.Fatalf("expected oldest session pruned")
	}
}

func TestSessionStoreMaintenancePurgesExpiredTranscriptArchives(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	store.SetMaintenanceConfig(session.SessionStoreMaintenanceConfig{
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

	if err := store.Upsert(session.BuildMainSessionKey("main"), core.SessionEntry{SessionID: "sess-main"}); err != nil {
		t.Fatalf("trigger maintenance flush: %v", err)
	}
	if _, err := os.Stat(archived); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected expired archived transcript removed, stat err=%v", err)
	}
}

func TestSessionStoreMaintenanceRotatesOversizedStore(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	store.SetMaintenanceConfig(session.SessionStoreMaintenanceConfig{
		Mode:                  "enforce",
		PruneAfter:            365 * 24 * time.Hour,
		MaxEntries:            100,
		RotateBytes:           64,
		ResetArchiveRetention: 30 * 24 * time.Hour,
	})

	if err := store.Upsert(session.BuildDirectSessionKey("main", "webchat", "first"), core.SessionEntry{
		SessionID: "sess-first",
		Label:     strings.Repeat("a", 128),
	}); err != nil {
		t.Fatalf("upsert first session: %v", err)
	}
	if err := store.Upsert(session.BuildDirectSessionKey("main", "webchat", "second"), core.SessionEntry{
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

func storeForTests(t *testing.T) *session.SessionStore {
	t.Helper()
	store, err := session.NewSessionStore(t.TempDir())
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	return store
}

func TestSessionsSendToolRunsTargetSessionWithNestedLane(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}

	parentSessionKey := session.BuildMainSessionKey("main")
	targetSessionKey := session.BuildDirectSessionKey("worker", "webchat", "peer-2")

	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				WorkspaceDir:    filepath.Join(baseDir, "workspace"),
				ToolAllowlist:   []string{"sessions_send"},
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1",
			},
			"worker": {
				ID:              "worker",
				WorkspaceDir:    filepath.Join(baseDir, "workspace-worker"),
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1-mini",
			},
		}),
		Memory: infra.NullMemoryProvider{},
		Policy: RuntimePolicy{
			SessionToolsVisibility: core.SessionVisibilityAll,
			AgentToAgent:           core.AgentToAgentPolicy{Enabled: true, Allow: []string{"main", "worker"}},
		},
		Backend: fakeBackend{
			onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
				if runCtx.Session.SessionKey != targetSessionKey {
					return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "unexpected session"}}}, nil
				}
				if runCtx.Request.Lane != core.LaneNested {
					t.Fatalf("expected nested lane, got %s", runCtx.Request.Lane)
				}
				if runCtx.Request.Deliver {
					t.Fatal("expected sessions_send target run to disable delivery")
				}
				if !strings.Contains(runCtx.SystemPrompt, "Agent-to-agent message context:") {
					t.Fatalf("expected a2a context in system prompt, got %q", runCtx.SystemPrompt)
				}
				if !strings.Contains(runCtx.SystemPrompt, "Agent 1 (requester) channel: webchat.") {
					t.Fatalf("expected requester channel in system prompt, got %q", runCtx.SystemPrompt)
				}
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "worker reply"}}}, nil
			},
		},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewSessionsSendTool()),
	}

	session, err := runtime.Sessions.Resolve(context.Background(), "main", parentSessionKey, "", "", "")
	if err != nil {
		t.Fatalf("resolve parent session: %v", err)
	}
	identity, err := runtime.Identities.Resolve(context.Background(), "main")
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	runCtx := rtypes.AgentRunContext{
		Runtime: runtime,
		Request: core.AgentRunRequest{
			AgentID: "main",
			Channel: "webchat",
		},
		Session:      session,
		Identity:     identity,
		WorkspaceDir: identity.WorkspaceDir,
	}

	result, err := runtime.ExecuteTool(context.Background(), runCtx, "sessions_send", map[string]any{
		"sessionKey": targetSessionKey,
		"message":    "check status",
	})
	if err != nil {
		t.Fatalf("sessions_send: %v", err)
	}
	if !strings.Contains(result.Text, `"status":"ok"`) {
		t.Fatalf("expected ok result, got %s", result.Text)
	}
	if !strings.Contains(result.Text, `"reply":"worker reply"`) {
		t.Fatalf("expected target reply in tool result, got %s", result.Text)
	}

	history, err := store.LoadTranscript(targetSessionKey)
	if err != nil {
		t.Fatalf("load target transcript: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected target transcript user+assistant entries, got %d", len(history))
	}
	if !strings.Contains(history[0].Text, "check status") || history[1].Text != "worker reply" {
		t.Fatalf("unexpected target transcript: %+v", history)
	}
}
