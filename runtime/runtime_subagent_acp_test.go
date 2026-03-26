package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/infra"
	rtypes "github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/internal/task"
	"github.com/kocort/kocort/internal/tool"
)

func TestSubagentsToolListsAndSendsPersistentACPSession(t *testing.T) {
	store := storeForTests(t)
	if err := store.Upsert("agent:worker:acp:claude:test", core.SessionEntry{
		SessionID: "sess-acp",
		Label:     "acp-worker",
		SpawnedBy: "agent:main:main",
		SpawnMode: "session",
		UpdatedAt: time.Now().UTC(),
		ACP:       &core.AcpSessionMeta{State: "idle", Mode: core.AcpSessionModePersistent},
		DeliveryContext: &core.DeliveryContext{
			Channel:  "discord",
			To:       "room-1",
			ThreadID: "thread-1",
		},
	}); err != nil {
		t.Fatalf("upsert acp child: %v", err)
	}
	if err := store.AppendTranscript("agent:worker:acp:claude:test", "sess-acp", core.TranscriptMessage{
		Role:      "assistant",
		Text:      "existing",
		Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("append transcript: %v", err)
	}

	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				PersonaPrompt:   "You are Kocort.",
				WorkspaceDir:    t.TempDir(),
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1",
				ToolAllowlist:   []string{"subagents"},
			},
			"worker": {
				ID:          "worker",
				RuntimeType: "acp",
			},
		}),
		Memory:     infra.NullMemoryProvider{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewSubagentsTool()),
		Deliverer:  &delivery.MemoryDeliverer{},
		Backend: fakeBackend{
			onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
				if runCtx.Request.SessionKey != "agent:worker:acp:claude:test" {
					t.Fatalf("unexpected session key: %s", runCtx.Request.SessionKey)
				}
				if runCtx.Request.Lane != core.LaneDefault {
					t.Fatalf("expected persistent ACP follow-up to use default lane, got %v", runCtx.Request.Lane)
				}
				runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "acp:" + runCtx.Request.Message})
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "acp:" + runCtx.Request.Message}}}, nil
			},
		},
	}

	runCtx := rtypes.AgentRunContext{
		Runtime: runtime,
		Request: core.AgentRunRequest{AgentID: "main", SessionKey: "agent:main:main", MaxSpawnDepth: 5},
		Session: core.SessionResolution{SessionID: "sess-main", SessionKey: "agent:main:main"},
		Identity: core.AgentIdentity{
			ID:            "main",
			ToolAllowlist: []string{"subagents"},
		},
	}

	listResult, err := runtime.ExecuteTool(context.Background(), runCtx, "subagents", map[string]any{"action": "list"})
	if err != nil {
		t.Fatalf("subagents list: %v", err)
	}
	if !strings.Contains(listResult.Text, `"kind":"acp"`) || !strings.Contains(listResult.Text, "acp-worker") {
		t.Fatalf("expected ACP persistent child in list, got %s", listResult.Text)
	}

	infoResult, err := runtime.ExecuteTool(context.Background(), runCtx, "subagents", map[string]any{
		"action": "info",
		"target": "acp-worker",
	})
	if err != nil {
		t.Fatalf("subagents info: %v", err)
	}
	if !strings.Contains(infoResult.Text, `"kind":"acp"`) || !strings.Contains(infoResult.Text, `"state":"idle"`) {
		t.Fatalf("expected ACP info payload, got %s", infoResult.Text)
	}

	logResult, err := runtime.ExecuteTool(context.Background(), runCtx, "subagents", map[string]any{
		"action": "log",
		"target": "acp-worker",
	})
	if err != nil {
		t.Fatalf("subagents log: %v", err)
	}
	if !strings.Contains(logResult.Text, "existing") {
		t.Fatalf("expected ACP transcript in log result, got %s", logResult.Text)
	}

	sendResult, err := runtime.ExecuteTool(context.Background(), runCtx, "subagents", map[string]any{
		"action":  "send",
		"target":  "acp-worker",
		"message": "continue",
	})
	if err != nil {
		t.Fatalf("subagents send: %v", err)
	}
	if !strings.Contains(sendResult.Text, `"kind":"acp"`) || !strings.Contains(sendResult.Text, "acp:") || !strings.Contains(sendResult.Text, "continue") {
		t.Fatalf("expected ACP child reply in send result, got %s", sendResult.Text)
	}
}
