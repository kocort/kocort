package runtime

import (
	"context"
	"encoding/json"
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
	if err := store.Upsert(targetSessionKey, core.SessionEntry{SessionID: "sess-target", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("upsert target: %v", err)
	}
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

func TestSessionsSendToolRunsTargetSessionWithNestedLane(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}

	parentSessionKey := session.BuildMainSessionKey("main")
	targetSessionKey := session.BuildDirectSessionKey("worker", "webchat", "peer-2")
	if err := store.Upsert(targetSessionKey, core.SessionEntry{SessionID: "sess-nested-target", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("upsert target: %v", err)
	}

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
