package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/heartbeat"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
	sessionpkg "github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/task"
)

func TestRunHeartbeatTurnRespectsGlobalHeartbeatDisable(t *testing.T) {
	store := storeForTests(t)
	heartbeat.SetHeartbeatsEnabled(false)
	defer heartbeat.SetHeartbeatsEnabled(true)

	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:             "main",
				WorkspaceDir:   t.TempDir(),
				DefaultModel:   "test-model",
				UserTimezone:   "Asia/Shanghai",
				HeartbeatEvery: "30m",
			},
		}),
		Memory:       infra.NullMemoryProvider{},
		Deliverer:    &delivery.MemoryDeliverer{},
		SystemEvents: infra.NewSystemEventQueue(),
		Subagents:    task.NewSubagentRegistry(),
		Queue:        task.NewFollowupQueue(),
		ActiveRuns:   task.NewActiveRunRegistry(),
	}

	result, err := runtime.RunHeartbeatTurn(context.Background(), heartbeat.HeartbeatWakeRequest{AgentID: "main", Reason: "interval"})
	if err != nil {
		t.Fatalf("RunHeartbeatTurn: %v", err)
	}
	if result.Status != "skipped" || result.Reason != "disabled" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestRunHeartbeatTurnSkipsCommentOnlyHeartbeatFile(t *testing.T) {
	store := storeForTests(t)
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "HEARTBEAT.md"), []byte("# Heartbeat\n- [ ]\n"), 0o644); err != nil {
		t.Fatalf("write HEARTBEAT.md: %v", err)
	}
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				WorkspaceDir:    workspace,
				DefaultProvider: "test",
				DefaultModel:    "test-model",
				UserTimezone:    "Asia/Shanghai",
				HeartbeatEvery:  "30m",
			},
		}),
		Memory:       infra.NullMemoryProvider{},
		Deliverer:    &delivery.MemoryDeliverer{},
		SystemEvents: infra.NewSystemEventQueue(),
		Subagents:    task.NewSubagentRegistry(),
		Queue:        task.NewFollowupQueue(),
		ActiveRuns:   task.NewActiveRunRegistry(),
	}

	result, err := runtime.RunHeartbeatTurn(context.Background(), heartbeat.HeartbeatWakeRequest{
		Reason:  "interval",
		AgentID: "main",
	})
	if err != nil {
		t.Fatalf("RunHeartbeatTurn: %v", err)
	}
	if result.Status != "skipped" || result.Reason != "no-events" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestRunHeartbeatTurnUsesHeartbeatModelOverride(t *testing.T) {
	store := storeForTests(t)
	sessionKey := sessionpkg.BuildDirectSessionKey("main", "webchat", "hb-user")
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:                   "main",
				WorkspaceDir:         t.TempDir(),
				DefaultProvider:      "test",
				DefaultModel:         "test-model",
				UserTimezone:         "Asia/Shanghai",
				HeartbeatEvery:       "30m",
				HeartbeatTarget:      "last",
				HeartbeatModel:       "gpt-5.4",
				HeartbeatAckMaxChars: 300,
			},
		}),
		Memory:       infra.NullMemoryProvider{},
		Deliverer:    &delivery.MemoryDeliverer{},
		SystemEvents: infra.NewSystemEventQueue(),
		Subagents:    task.NewSubagentRegistry(),
		Queue:        task.NewFollowupQueue(),
		ActiveRuns:   task.NewActiveRunRegistry(),
	}
	runtime.SystemEvents.Enqueue(sessionKey, "Reminder: ping", "cron:test")
	runtime.Backend = fakeBackend{
		onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			if got := strings.TrimSpace(runCtx.Request.SessionModelOverride); got != "gpt-5.4" {
				t.Fatalf("expected heartbeat model override gpt-5.4, got %q", got)
			}
			runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "Reminder delivered"})
			return core.AgentRunResult{
				Payloads: []core.ReplyPayload{{Text: "Reminder delivered"}},
			}, nil
		},
	}
	if _, err := runtime.Sessions.Resolve(context.Background(), "main", sessionKey, "webchat", "hb-user", ""); err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	if _, err := runtime.RunHeartbeatTurn(context.Background(), heartbeat.HeartbeatWakeRequest{
		Reason:     "cron:test",
		AgentID:    "main",
		SessionKey: sessionKey,
	}); err != nil {
		t.Fatalf("RunHeartbeatTurn: %v", err)
	}
}

func TestRunHeartbeatTurnUsesIsolatedHeartbeatSessionKey(t *testing.T) {
	store := storeForTests(t)
	sessionKey := sessionpkg.BuildDirectSessionKey("main", "webchat", "hb-user")
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:                       "main",
				WorkspaceDir:             t.TempDir(),
				DefaultProvider:          "test",
				DefaultModel:             "test-model",
				UserTimezone:             "Asia/Shanghai",
				HeartbeatEvery:           "30m",
				HeartbeatTarget:          "last",
				HeartbeatIsolatedSession: true,
			},
		}),
		Memory:       infra.NullMemoryProvider{},
		Deliverer:    &delivery.MemoryDeliverer{},
		SystemEvents: infra.NewSystemEventQueue(),
		Subagents:    task.NewSubagentRegistry(),
		Queue:        task.NewFollowupQueue(),
		ActiveRuns:   task.NewActiveRunRegistry(),
	}
	runtime.SystemEvents.Enqueue(sessionKey, "Reminder: ping", "cron:test")
	runtime.Backend = fakeBackend{
		onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			if got := strings.TrimSpace(runCtx.Request.SessionKey); got != sessionKey+":heartbeat" {
				t.Fatalf("expected isolated heartbeat session key, got %q", got)
			}
			runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "Reminder delivered"})
			return core.AgentRunResult{
				Payloads: []core.ReplyPayload{{Text: "Reminder delivered"}},
			}, nil
		},
	}
	if _, err := runtime.Sessions.Resolve(context.Background(), "main", sessionKey, "webchat", "hb-user", ""); err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	if _, err := runtime.RunHeartbeatTurn(context.Background(), heartbeat.HeartbeatWakeRequest{
		Reason:     "cron:test",
		AgentID:    "main",
		SessionKey: sessionKey,
	}); err != nil {
		t.Fatalf("RunHeartbeatTurn: %v", err)
	}
	if entry := runtime.Sessions.Entry(sessionKey + ":heartbeat"); entry == nil || strings.TrimSpace(entry.SessionID) == "" {
		t.Fatalf("expected isolated heartbeat session entry, got %+v", entry)
	}
}

func TestRunHeartbeatTurnDoesNotDrainEventsWhenSkippedBusy(t *testing.T) {
	store := storeForTests(t)
	sessionKey := sessionpkg.BuildDirectSessionKey("main", "webchat", "hb-user")
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:             "main",
				WorkspaceDir:   t.TempDir(),
				DefaultModel:   "test-model",
				UserTimezone:   "Asia/Shanghai",
				HeartbeatEvery: "30m",
			},
		}),
		Memory:       infra.NullMemoryProvider{},
		Deliverer:    &delivery.MemoryDeliverer{},
		SystemEvents: infra.NewSystemEventQueue(),
		Subagents:    task.NewSubagentRegistry(),
		Queue:        task.NewFollowupQueue(),
		ActiveRuns:   task.NewActiveRunRegistry(),
	}
	runtime.SystemEvents.Enqueue(sessionKey, "Reminder: retain", "cron:test")
	done := runtime.ActiveRuns.Start("other-session")
	defer done()

	result, err := runtime.RunHeartbeatTurn(context.Background(), heartbeat.HeartbeatWakeRequest{
		Reason:     "cron:test",
		AgentID:    "main",
		SessionKey: sessionKey,
	})
	if err != nil {
		t.Fatalf("RunHeartbeatTurn: %v", err)
	}
	if result.Status != "skipped" || result.Reason != "requests-in-flight" {
		t.Fatalf("unexpected result: %+v", result)
	}
	events := runtime.SystemEvents.Peek(sessionKey)
	if len(events) != 1 || strings.TrimSpace(events[0].Text) != "Reminder: retain" {
		t.Fatalf("expected event retained after skip, got %+v", events)
	}
}

func TestRunHeartbeatTurnAllowsMissingHeartbeatFile(t *testing.T) {
	store := storeForTests(t)
	sessionKey := sessionpkg.BuildDirectSessionKey("main", "webchat", "hb-user")
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				WorkspaceDir:    t.TempDir(),
				DefaultProvider: "test",
				DefaultModel:    "test-model",
				UserTimezone:    "Asia/Shanghai",
				HeartbeatEvery:  "30m",
			},
		}),
		Memory:       infra.NullMemoryProvider{},
		Deliverer:    &delivery.MemoryDeliverer{},
		SystemEvents: infra.NewSystemEventQueue(),
		Subagents:    task.NewSubagentRegistry(),
		Queue:        task.NewFollowupQueue(),
		ActiveRuns:   task.NewActiveRunRegistry(),
		Backend: fakeBackend{
			onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
				runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: heartbeat.HeartbeatToken})
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: heartbeat.HeartbeatToken}}}, nil
			},
		},
	}
	if _, err := runtime.Sessions.Resolve(context.Background(), "main", sessionKey, "webchat", "hb-user", ""); err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	result, err := runtime.RunHeartbeatTurn(context.Background(), heartbeat.HeartbeatWakeRequest{
		Reason:     "interval",
		AgentID:    "main",
		SessionKey: sessionKey,
	})
	if err != nil {
		t.Fatalf("RunHeartbeatTurn: %v", err)
	}
	if result.Status != "ran" {
		t.Fatalf("expected heartbeat to run without HEARTBEAT.md, got %+v", result)
	}
}

func TestRunHeartbeatTurnKeepsReasoningWhenMainReplyIsHeartbeatOK(t *testing.T) {
	store := storeForTests(t)
	sessionKey := sessionpkg.BuildDirectSessionKey("main", "webchat", "hb-user")
	deliverer := &delivery.MemoryDeliverer{}
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:                        "main",
				WorkspaceDir:              t.TempDir(),
				DefaultProvider:           "test",
				DefaultModel:              "test-model",
				UserTimezone:              "Asia/Shanghai",
				HeartbeatEvery:            "30m",
				HeartbeatTarget:           "last",
				HeartbeatIncludeReasoning: true,
			},
		}),
		Memory:       infra.NullMemoryProvider{},
		Deliverer:    deliverer,
		SystemEvents: infra.NewSystemEventQueue(),
		Subagents:    task.NewSubagentRegistry(),
		Queue:        task.NewFollowupQueue(),
		ActiveRuns:   task.NewActiveRunRegistry(),
		Backend: fakeBackend{
			onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
				runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "need follow-up", IsReasoning: true})
				runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: heartbeat.HeartbeatToken})
				return core.AgentRunResult{
					Payloads: []core.ReplyPayload{
						{Text: "need follow-up", IsReasoning: true},
						{Text: heartbeat.HeartbeatToken},
					},
				}, nil
			},
		},
	}
	resolved, err := runtime.Sessions.Resolve(context.Background(), "main", sessionKey, "webchat", "hb-user", "")
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	if err := runtime.Sessions.Upsert(sessionKey, core.SessionEntry{
		SessionID:   resolved.SessionID,
		LastChannel: "webchat",
		LastTo:      "hb-user",
	}); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if _, err := runtime.RunHeartbeatTurn(context.Background(), heartbeat.HeartbeatWakeRequest{
		Reason:     "wake",
		AgentID:    "main",
		SessionKey: sessionKey,
	}); err != nil {
		t.Fatalf("RunHeartbeatTurn: %v", err)
	}
	records := append([]core.DeliveryRecord{}, deliverer.Records...)
	if len(records) != 1 || !strings.Contains(records[0].Payload.Text, "Reasoning:") {
		t.Fatalf("expected reasoning payload to be delivered, got %+v", records)
	}
}
