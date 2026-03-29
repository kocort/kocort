package runtime

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/acp"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/task"
	"github.com/kocort/kocort/internal/tool"
)

func TestSpawnACPSessionStartsNestedACPChildRun(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}

	rt := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:                  "main",
				PersonaPrompt:       "You are Kocort.",
				WorkspaceDir:        filepath.Join(baseDir, "workspace-main"),
				DefaultProvider:     "openai",
				DefaultModel:        "gpt-4.1",
				SubagentAllowAgents: []string{"worker"},
			},
			"worker": {
				ID:              "worker",
				Name:            "Worker ACP",
				PersonaPrompt:   "You are an ACP worker.",
				WorkspaceDir:    filepath.Join(baseDir, "workspace-worker"),
				DefaultProvider: "acp-live",
				DefaultModel:    "gpt-5.4",
				RuntimeType:     "acp",
				RuntimeBackend:  "acp-live",
				RuntimeCwd:      filepath.Join(baseDir, "workspace-worker", "acp"),
			},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(),
		Backend: fakeBackend{
			onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "acp child ok"}}}, nil
			},
		},
	}
	result, err := rt.SpawnACPSession(context.Background(), acp.SessionSpawnRequest{
		RequesterSessionKey: session.BuildMainSessionKey("main"),
		RequesterAgentID:    "main",
		TargetAgentID:       "worker",
		Task:                "inspect logs",
		RunTimeoutSeconds:   30,
	})
	if err != nil {
		t.Fatalf("spawn acp session: %v", err)
	}
	if result.Status != "accepted" || result.Backend != "acp-live" {
		t.Fatalf("unexpected spawn result: %+v", result)
	}
	entry := store.Entry(result.ChildSessionKey)
	if entry == nil || entry.ACP == nil || entry.ACP.Backend != "acp-live" {
		t.Fatalf("expected persisted acp session entry, got %+v", entry)
	}
	record := rt.Subagents.Get(result.RunID)
	if record == nil || record.ChildKind != "acp" || record.ChildSessionKey != result.ChildSessionKey {
		t.Fatalf("expected ACP child in unified registry, got %+v", record)
	}
	if result.Lane != core.LaneNested || result.AgentID != "worker" || result.Mode != "run" {
		t.Fatalf("unexpected accepted metadata: %+v", result)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		record = rt.Subagents.Get(result.RunID)
		if record != nil && record.Outcome != nil && !record.CompletionMessageSentAt.IsZero() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	record = rt.Subagents.Get(result.RunID)
	if record == nil || record.Outcome == nil || record.CompletionMessageSentAt.IsZero() {
		t.Fatalf("expected ACP registry completion lifecycle to settle, got %+v", record)
	}
}

func TestSpawnACPSessionRejectsSandboxRequireForUnsandboxedTarget(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}

	rt := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:                  "main",
				WorkspaceDir:        filepath.Join(baseDir, "workspace-main"),
				SubagentAllowAgents: []string{"worker"},
			},
			"worker": {
				ID:             "worker",
				WorkspaceDir:   filepath.Join(baseDir, "workspace-worker"),
				RuntimeType:    "acp",
				RuntimeBackend: "acp-live",
			},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(),
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			return core.AgentRunResult{}, nil
		}},
	}

	_, err = rt.SpawnACPSession(context.Background(), acp.SessionSpawnRequest{
		RequesterSessionKey: session.BuildMainSessionKey("main"),
		RequesterAgentID:    "main",
		TargetAgentID:       "worker",
		Task:                "inspect logs",
		SandboxMode:         "require",
	})
	if err == nil || !strings.Contains(err.Error(), `sandbox="require" requires`) {
		t.Fatalf("expected sandbox=require rejection, got %v", err)
	}
}

func TestSpawnACPSessionStreamToParentAnnouncesCompletion(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}

	parentSessionKey := session.BuildMainSessionKey("main")
	parentDone := make(chan string, 1)

	rt := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:                  "main",
				PersonaPrompt:       "You are Kocort.",
				WorkspaceDir:        filepath.Join(baseDir, "workspace-main"),
				DefaultProvider:     "openai",
				DefaultModel:        "gpt-4.1",
				SubagentAllowAgents: []string{"worker"},
			},
			"worker": {
				ID:              "worker",
				Name:            "Worker ACP",
				PersonaPrompt:   "You are an ACP worker.",
				WorkspaceDir:    filepath.Join(baseDir, "workspace-worker"),
				DefaultProvider: "acp-live",
				DefaultModel:    "gpt-5.4",
				RuntimeType:     "acp",
				RuntimeBackend:  "acp-live",
				RuntimeCwd:      filepath.Join(baseDir, "workspace-worker", "acp"),
			},
		}),
		Memory:     infra.NullMemoryProvider{},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(),
		Backend: fakeBackend{
			onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
				if runCtx.Session.SessionKey == parentSessionKey && strings.Contains(runCtx.Request.Message, "[Subagent completion]") {
					parentDone <- runCtx.Request.Message
					return core.AgentRunResult{}, nil
				}
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "acp child ok"}}}, nil
			},
		},
	}
	rt.Queue.SetSleep(func(context.Context, time.Duration) error { return nil })
	if err := store.Upsert(parentSessionKey, core.SessionEntry{SessionID: "sess-parent"}); err != nil {
		t.Fatalf("upsert parent session: %v", err)
	}

	_, err = rt.SpawnACPSession(context.Background(), acp.SessionSpawnRequest{
		RequesterSessionKey: parentSessionKey,
		RequesterAgentID:    "main",
		TargetAgentID:       "worker",
		Task:                "inspect logs",
		RunTimeoutSeconds:   30,
		StreamTo:            "parent",
		RouteChannel:        "discord",
		RouteTo:             "chan-1",
		RouteAccountID:      "acct-1",
		RouteThreadID:       "thread-9",
	})
	if err != nil {
		t.Fatalf("spawn acp session with parent relay: %v", err)
	}
	mem, _ := rt.Deliverer.(*delivery.MemoryDeliverer)
	select {
	case msg := <-parentDone:
		if !strings.Contains(msg, "acp child ok") {
			t.Fatalf("expected ACP child result in parent announce, got %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ACP parent relay completion announce")
	}
	if mem == nil || len(mem.Records) < 2 {
		t.Fatalf("expected direct parent stream deliveries, got %+v", mem)
	}
	if got := mem.Records[0].Payload.Text; !strings.Contains(got, "[ACP stream started]") {
		t.Fatalf("expected started notice, got %q", got)
	}
	foundChildOutput := false
	for _, record := range mem.Records {
		if strings.Contains(record.Payload.Text, "acp child ok") {
			foundChildOutput = true
			break
		}
	}
	if !foundChildOutput {
		t.Fatalf("expected direct child output in parent stream, got %+v", mem.Records)
	}
	time.Sleep(50 * time.Millisecond)
}
