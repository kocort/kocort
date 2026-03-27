package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/task"

	"github.com/kocort/kocort/utils"
)

func TestRuntimeAuditLogRecordsCoreEvents(t *testing.T) {
	stateDir := t.TempDir()
	cfg := config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"test": {
					BaseURL: "https://example.invalid/v1",
					APIKey:  "test-key",
					API:     "openai-completions",
					Models:  []config.ProviderModelConfig{{ID: "demo", Name: "Demo"}},
				},
			},
		},
		Gateway: config.GatewayConfig{
			Enabled: true,
			Webchat: &config.GatewayWebchatConfig{Enabled: utils.BoolPtr(true)},
		},
		Channels: config.ChannelsConfig{
			Entries: map[string]config.ChannelConfig{
				"mock": {
					Enabled: utils.BoolPtr(true),
					Agent:   "main",
					Config:  map[string]any{"driver": "mock"},
				},
			},
		},
		Tasks: config.TasksConfig{Enabled: utils.BoolPtr(false), MaxConcurrent: 2},
	}
	rt, err := NewRuntimeFromConfig(cfg, config.RuntimeConfigParams{
		StateDir:  stateDir,
		AgentID:   "main",
		Provider:  "test",
		Model:     "demo",
		Deliverer: &delivery.MemoryDeliverer{},
	})
	if err != nil {
		t.Fatalf("NewRuntimeFromConfig: %v", err)
	}

	sessionKey := session.BuildDirectSessionKey("main", "mock", "audit-user")
	if err := rt.Sessions.Upsert(sessionKey, core.SessionEntry{
		SessionID:   "sess-audit",
		UpdatedAt:   time.Now().UTC(),
		LastChannel: "mock",
		LastTo:      "audit-user",
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	rt.Tools.Register(&stubTool{
		name: "audit_demo",
		execute: func(ctx context.Context, toolCtx rtypes.ToolContext, args map[string]any) (core.ToolResult, error) {
			return core.ToolResult{Text: "tool-ok"}, nil
		},
	})
	identity, err := rt.Identities.Resolve(context.Background(), "main")
	if err != nil {
		t.Fatalf("Resolve identity: %v", err)
	}
	runCtx := rtypes.AgentRunContext{
		Runtime:      rt,
		Request:      core.AgentRunRequest{RunID: "run-audit", SessionKey: sessionKey, AgentID: "main", Channel: "mock", To: "audit-user"},
		Session:      core.SessionResolution{SessionKey: sessionKey, SessionID: "sess-audit"},
		Identity:     identity,
		WorkspaceDir: identity.WorkspaceDir,
	}
	runCtx.Identity.ToolAllowlist = []string{"audit_demo"}
	if _, err := rt.ExecuteTool(context.Background(), runCtx, "audit_demo", map[string]any{"value": "x"}); err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}

	rt.ReloadEnvironment()

	taskRec, err := rt.ScheduleTask(context.Background(), task.TaskScheduleRequest{
		AgentID: "main",
		Title:   "Audit Task",
		Message: "Hello",
	})
	if err != nil {
		t.Fatalf("ScheduleTask: %v", err)
	}
	if err := rt.Tasks.MarkQueued(taskRec.ID); err != nil {
		t.Fatalf("MarkQueued: %v", err)
	}
	if err := rt.Tasks.MarkRunStarted(taskRec.ID, "task-run", sessionKey); err != nil {
		t.Fatalf("MarkRunStarted: %v", err)
	}
	if err := rt.Tasks.MarkRunFinished(taskRec.ID, core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "done"}}}, nil, time.Time{}); err != nil {
		t.Fatalf("MarkRunFinished: %v", err)
	}

	if err := rt.Deliverer.Deliver(context.Background(), core.ReplyKindFinal, core.ReplyPayload{Text: "delivered"}, core.DeliveryTarget{
		SessionKey: sessionKey,
		Channel:    "webchat",
		To:         "audit-user",
	}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	events, err := rt.Audit.List(context.Background(), core.AuditQuery{})
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("expected audit events")
	}
	assertAuditType := func(category core.AuditCategory, typ string) {
		t.Helper()
		for _, event := range events {
			if event.Category == category && event.Type == typ {
				return
			}
		}
		t.Fatalf("expected audit event %s/%s, got %+v", category, typ, events)
	}
	assertAuditType(core.AuditCategoryTool, "tool_execute_started")
	assertAuditType(core.AuditCategoryTool, "tool_execute_completed")
	assertAuditType(core.AuditCategoryEnvironment, "environment_reloaded")
	assertAuditType(core.AuditCategoryTask, "scheduled")
	assertAuditType(core.AuditCategoryTask, "running")
	assertAuditType(core.AuditCategoryTask, "completed")
	assertAuditType(core.AuditCategoryDelivery, "sent")
}

func TestRuntimeAuditLogRecordsSandboxDeny(t *testing.T) {
	stateDir := t.TempDir()
	cfg := config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"test": {
					BaseURL: "https://example.invalid/v1",
					APIKey:  "test-key",
					API:     "openai-completions",
					Models:  []config.ProviderModelConfig{{ID: "demo", Name: "Demo"}},
				},
			},
		},
		Tasks: config.TasksConfig{Enabled: utils.BoolPtr(false)},
	}
	rt, err := NewRuntimeFromConfig(cfg, config.RuntimeConfigParams{
		StateDir:  stateDir,
		AgentID:   "main",
		Provider:  "test",
		Model:     "demo",
		Deliverer: &delivery.MemoryDeliverer{},
	})
	if err != nil {
		t.Fatalf("NewRuntimeFromConfig: %v", err)
	}
	sessionKey := session.BuildMainSessionKey("main")
	if err := rt.Sessions.Upsert(sessionKey, core.SessionEntry{SessionID: "sess-sandbox", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	runCtx := rtypes.AgentRunContext{
		Runtime: rt,
		Request: core.AgentRunRequest{RunID: "run-sandbox", SessionKey: sessionKey, AgentID: "main"},
		Session: core.SessionResolution{SessionKey: sessionKey, SessionID: "sess-sandbox"},
		Identity: core.AgentIdentity{
			ID:                       "main",
			DefaultProvider:          "test",
			DefaultModel:             "demo",
			SandboxMode:              "strict",
			SandboxSessionVisibility: "self",
			WorkspaceDir:             t.TempDir(),
		},
		WorkspaceDir: t.TempDir(),
	}
	if _, err := rt.ExecuteTool(context.Background(), runCtx, "sessions_history", map[string]any{"sessionKey": sessionKey}); err == nil {
		t.Fatalf("expected sandbox denial")
	}
	events, err := rt.Audit.List(context.Background(), core.AuditQuery{Category: core.AuditCategorySandbox})
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(events) == 0 || events[len(events)-1].Type != "tool_denied" {
		t.Fatalf("expected sandbox tool_denied event, got %+v", events)
	}
}

func TestAuditListMissingFileIsEmpty(t *testing.T) {
	log, err := infra.NewAuditLog(t.TempDir())
	if err != nil {
		t.Fatalf("NewAuditLog: %v", err)
	}
	events, err := log.List(context.Background(), core.AuditQuery{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected empty events, got %+v", events)
	}
}
