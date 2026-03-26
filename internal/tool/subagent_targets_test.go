package tool

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/task"
)

func TestResolveSubagentTargetPrefersExactMatch(t *testing.T) {
	target, err := resolveSubagentTarget([]subagentTarget{
		{RunID: "run-123", ChildSessionKey: "agent:main:subagent:alpha", Label: "alpha"},
		{RunID: "run-456", ChildSessionKey: "agent:main:subagent:beta", Label: "beta"},
	}, "run-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target == nil || target.RunID != "run-123" {
		t.Fatalf("expected exact target, got %+v", target)
	}
}

func TestResolveSubagentTargetRejectsAmbiguousPrefix(t *testing.T) {
	target, err := resolveSubagentTarget([]subagentTarget{
		{RunID: "run-alpha", ChildSessionKey: "agent:main:subagent:alpha", Label: "alpha"},
		{RunID: "run-beta", ChildSessionKey: "agent:main:subagent:beta", Label: "alphabet"},
	}, "alp")
	if err == nil {
		t.Fatal("expected ambiguous prefix error")
	}
	if target != nil {
		t.Fatalf("expected nil target on ambiguity, got %+v", target)
	}
}

func TestSubagentsToolSchemaIncludesRecentMinutes(t *testing.T) {
	schema := NewSubagentsTool().OpenAIFunctionTool()
	props, _ := schema.Parameters["properties"].(map[string]any)
	if _, ok := props["recentMinutes"]; !ok {
		t.Fatal("expected recentMinutes in schema")
	}
}

func TestSubagentsToolAgentsListsAllowedTargets(t *testing.T) {
	result, err := NewSubagentsTool().Execute(context.Background(), ToolContext{
		Runtime: stubRuntimeServices{
			identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
				"worker": {ID: "worker", Name: "Worker", DefaultProvider: "openai", DefaultModel: "gpt-4.1-mini", ToolProfile: "coding", WorkspaceDir: "/tmp/worker"},
			}),
		},
		Run: AgentRunContext{
			Identity: core.AgentIdentity{
				ID:                  "main",
				SubagentAllowAgents: []string{"worker"},
			},
		},
	}, map[string]any{"action": "agents"})
	if err != nil {
		t.Fatalf("execute subagents agents: %v", err)
	}
	var payload struct {
		Status string                   `json:"status"`
		Agents []map[string]interface{} `json:"agents"`
	}
	if unmarshalErr := json.Unmarshal(result.JSON, &payload); unmarshalErr != nil {
		t.Fatalf("unmarshal result: %v", unmarshalErr)
	}
	if payload.Status != "ok" || len(payload.Agents) != 1 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if payload.Agents[0]["id"] != "worker" || payload.Agents[0]["name"] != "Worker" {
		t.Fatalf("unexpected agent entry: %+v", payload.Agents[0])
	}
}

func TestSubagentsToolFocusRebindsCurrentThread(t *testing.T) {
	store, err := session.NewSessionStore(t.TempDir())
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	registry := task.NewSubagentRegistry()
	now := time.Now().UTC()
	registry.Register(task.SubagentRunRecord{
		RunID:               "run-old",
		ChildKind:           "subagent",
		ChildSessionKey:     "agent:worker:subagent:old",
		RequesterSessionKey: "agent:main:main",
		Label:               "old",
		CreatedAt:           now,
		StartedAt:           now,
	})
	registry.Register(task.SubagentRunRecord{
		RunID:               "run-new",
		ChildKind:           "subagent",
		ChildSessionKey:     "agent:worker:subagent:new",
		RequesterSessionKey: "agent:main:main",
		Label:               "new",
		CreatedAt:           now.Add(time.Second),
		StartedAt:           now.Add(time.Second),
	})
	svc := session.NewThreadBindingService(store)
	if err := svc.BindThreadSession(session.BindThreadSessionInput{
		TargetSessionKey:    "agent:worker:subagent:old",
		RequesterSessionKey: "agent:main:main",
		TargetKind:          "subagent",
		Placement:           session.ThreadBindingPlacementCurrent,
		Channel:             "discord",
		To:                  "room-1",
		ThreadID:            "thread-1",
		ConversationID:      "thread-1",
		Label:               "old",
	}); err != nil {
		t.Fatalf("initial bind: %v", err)
	}

	result, err := NewSubagentsTool().Execute(context.Background(), ToolContext{
		Runtime: stubRuntimeServices{
			sessions:  store,
			subagents: registry,
		},
		Run: AgentRunContext{
			Session: core.SessionResolution{SessionKey: "agent:main:main"},
			Request: core.AgentRunRequest{
				Channel:   "discord",
				To:        "room-1",
				ThreadID:  "thread-1",
				AccountID: "",
			},
		},
	}, map[string]any{
		"action": "focus",
		"target": "new",
	})
	if err != nil {
		t.Fatalf("focus target: %v", err)
	}
	var payload map[string]any
	if unmarshalErr := json.Unmarshal(result.JSON, &payload); unmarshalErr != nil {
		t.Fatalf("unmarshal focus result: %v", unmarshalErr)
	}
	if payload["status"] != "ok" {
		t.Fatalf("unexpected focus payload: %+v", payload)
	}
	key, ok := svc.ResolveThreadSession(session.BoundSessionLookupOptions{
		Channel:  "discord",
		To:       "room-1",
		ThreadID: "thread-1",
	})
	if !ok || key != "agent:worker:subagent:new" {
		t.Fatalf("expected rebound target, got %q %v", key, ok)
	}
	if oldBindings := svc.ListBindingsForTargetSession("agent:worker:subagent:old"); len(oldBindings) != 0 {
		t.Fatalf("expected old bindings cleared after focus rebind, got %+v", oldBindings)
	}
}
