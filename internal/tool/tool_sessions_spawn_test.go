package tool

import (
	"context"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/acp"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/task"
)

type spawnToolRuntimeStub struct {
	spawnACP func(ctx context.Context, req acp.SessionSpawnRequest) (acp.SessionSpawnResult, error)
	spawnSub func(ctx context.Context, req task.SubagentSpawnRequest) (task.SubagentSpawnResult, error)
}

func (s spawnToolRuntimeStub) Run(context.Context, core.AgentRunRequest) (core.AgentRunResult, error) {
	return core.AgentRunResult{}, nil
}
func (s spawnToolRuntimeStub) SpawnSubagent(ctx context.Context, req task.SubagentSpawnRequest) (task.SubagentSpawnResult, error) {
	if s.spawnSub != nil {
		return s.spawnSub(ctx, req)
	}
	return task.SubagentSpawnResult{}, nil
}
func (s spawnToolRuntimeStub) SpawnACPSession(ctx context.Context, req acp.SessionSpawnRequest) (acp.SessionSpawnResult, error) {
	return s.spawnACP(ctx, req)
}
func (s spawnToolRuntimeStub) PushInbound(context.Context, core.ChannelInboundMessage) (core.ChatSendResponse, error) {
	return core.ChatSendResponse{}, nil
}
func (s spawnToolRuntimeStub) ExecuteTool(context.Context, AgentRunContext, string, map[string]any) (core.ToolResult, error) {
	return core.ToolResult{}, nil
}
func (s spawnToolRuntimeStub) ScheduleTask(context.Context, task.TaskScheduleRequest) (core.TaskRecord, error) {
	return core.TaskRecord{}, nil
}
func (s spawnToolRuntimeStub) ListTasks(context.Context) []core.TaskRecord { return nil }
func (s spawnToolRuntimeStub) GetTask(context.Context, string) *core.TaskRecord {
	return nil
}
func (s spawnToolRuntimeStub) CancelTask(context.Context, string) (*core.TaskRecord, error) {
	return nil, nil
}
func (s spawnToolRuntimeStub) GetAudit() event.AuditRecorder { return nil }
func (s spawnToolRuntimeStub) GetEventBus() event.EventBus   { return nil }
func (s spawnToolRuntimeStub) CheckSessionAccess(session.SessionAccessAction, string, string) session.SessionAccessResult {
	return session.SessionAccessResult{Allowed: true}
}
func (s spawnToolRuntimeStub) ResolveModelSelection(context.Context, core.AgentIdentity, core.AgentRunRequest, core.SessionResolution) (core.ModelSelection, error) {
	return core.ModelSelection{}, nil
}
func (s spawnToolRuntimeStub) GetSessions() *session.SessionStore        { return nil }
func (s spawnToolRuntimeStub) GetIdentities() core.IdentityResolver      { return nil }
func (s spawnToolRuntimeStub) GetProcesses() *ProcessRegistry            { return nil }
func (s spawnToolRuntimeStub) GetMemory() core.MemoryProvider            { return nil }
func (s spawnToolRuntimeStub) GetSubagents() *task.SubagentRegistry      { return nil }
func (s spawnToolRuntimeStub) GetActiveRuns() *task.ActiveRunRegistry    { return nil }
func (s spawnToolRuntimeStub) GetQueue() *task.FollowupQueue             { return nil }
func (s spawnToolRuntimeStub) GetTasks() *task.TaskScheduler             { return nil }
func (s spawnToolRuntimeStub) GetEnvironment() *infra.EnvironmentRuntime { return nil }
func (s spawnToolRuntimeStub) ResolveChannelConfig(string) config.ChannelConfig {
	return config.ChannelConfig{}
}
func (s spawnToolRuntimeStub) GetSendPolicy() *session.SendPolicyConfig { return nil }

func TestSessionsSpawnToolRoutesACPRuntime(t *testing.T) {
	tool := NewSessionsSpawnTool()
	called := false
	result, err := tool.Execute(context.Background(), ToolContext{
		Runtime: spawnToolRuntimeStub{
			spawnACP: func(ctx context.Context, req acp.SessionSpawnRequest) (acp.SessionSpawnResult, error) {
				called = true
				if req.TargetAgentID != "worker" || req.SpawnMode != "run" {
					t.Fatalf("unexpected acp request: %+v", req)
				}
				return acp.SessionSpawnResult{
					Status:          "accepted",
					ChildSessionKey: "agent:worker:acp:acp-live:test",
					Backend:         "acp-live",
				}, nil
			},
		},
		Run: AgentRunContext{
			Session: core.SessionResolution{SessionKey: "agent:main:main"},
			Identity: core.AgentIdentity{
				ID:                         "main",
				SubagentTimeoutSeconds:     90,
				SubagentAttachmentsEnabled: true,
			},
		},
	}, map[string]any{
		"task":    "inspect",
		"agentId": "worker",
		"runtime": "acp",
	})
	if err != nil {
		t.Fatalf("execute sessions_spawn: %v", err)
	}
	if !called {
		t.Fatal("expected ACP runtime path to be called")
	}
	if !strings.Contains(result.Text, `"Backend":"acp-live"`) {
		t.Fatalf("unexpected tool result: %s", result.Text)
	}
}

func TestSessionsSpawnToolPassesACPStreamToParent(t *testing.T) {
	tool := NewSessionsSpawnTool()
	called := false
	_, err := tool.Execute(context.Background(), ToolContext{
		Runtime: spawnToolRuntimeStub{
			spawnACP: func(ctx context.Context, req acp.SessionSpawnRequest) (acp.SessionSpawnResult, error) {
				called = true
				if req.StreamTo != "parent" {
					t.Fatalf("expected streamTo=parent, got %+v", req)
				}
				return acp.SessionSpawnResult{Status: "accepted"}, nil
			},
		},
		Run: AgentRunContext{
			Session: core.SessionResolution{SessionKey: "agent:main:main"},
			Identity: core.AgentIdentity{
				ID:                         "main",
				SubagentTimeoutSeconds:     90,
				SubagentAttachmentsEnabled: true,
			},
		},
	}, map[string]any{
		"task":     "inspect",
		"runtime":  "acp",
		"streamTo": "parent",
	})
	if err != nil {
		t.Fatalf("execute sessions_spawn with streamTo parent: %v", err)
	}
	if !called {
		t.Fatal("expected ACP runtime path to be called")
	}
}

func TestSessionsSpawnToolParsesSubagentAttachments(t *testing.T) {
	tool := NewSessionsSpawnTool()
	called := false
	result, err := tool.Execute(context.Background(), ToolContext{
		Runtime: spawnToolRuntimeStub{
			spawnSub: func(ctx context.Context, req task.SubagentSpawnRequest) (task.SubagentSpawnResult, error) {
				called = true
				if len(req.Attachments) != 1 || req.Attachments[0].Name != "a.txt" || req.AttachMountPath != "docs" {
					t.Fatalf("unexpected attachment request: %+v", req)
				}
				return task.SubagentSpawnResult{Status: "accepted"}, nil
			},
		},
		Run: AgentRunContext{
			Session: core.SessionResolution{SessionKey: "agent:main:main"},
			Identity: core.AgentIdentity{
				ID:                         "main",
				SubagentTimeoutSeconds:     90,
				SubagentAttachmentsEnabled: true,
			},
		},
	}, map[string]any{
		"task": "inspect",
		"attachments": []any{
			map[string]any{"name": "a.txt", "content": "hello", "encoding": "utf8"},
		},
		"attachAs": map[string]any{"mountPath": "docs"},
	})
	if err != nil {
		t.Fatalf("execute sessions_spawn with attachments: %v", err)
	}
	if !called || !strings.Contains(result.Text, `"Status":"accepted"`) {
		t.Fatalf("unexpected result: %s", result.Text)
	}
}

func TestSessionsSpawnToolRejectsAttachmentsForACPRuntime(t *testing.T) {
	tool := NewSessionsSpawnTool()
	result, err := tool.Execute(context.Background(), ToolContext{
		Runtime: spawnToolRuntimeStub{},
		Run: AgentRunContext{
			Session:  core.SessionResolution{SessionKey: "agent:main:main"},
			Identity: core.AgentIdentity{ID: "main", SubagentTimeoutSeconds: 90},
		},
	}, map[string]any{
		"task":    "inspect",
		"runtime": "acp",
		"attachments": []any{
			map[string]any{"name": "a.txt", "content": "hello", "encoding": "utf8"},
		},
	})
	if err != nil {
		t.Fatalf("execute sessions_spawn with acp attachments: %v", err)
	}
	if !strings.Contains(result.Text, "attachments are currently unsupported for runtime=acp") {
		t.Fatalf("unexpected tool result: %s", result.Text)
	}
}

func TestSessionsSpawnToolRejectsAttachmentsWhenDisabledByPolicy(t *testing.T) {
	tool := NewSessionsSpawnTool()
	result, err := tool.Execute(context.Background(), ToolContext{
		Runtime: spawnToolRuntimeStub{},
		Run: AgentRunContext{
			Session: core.SessionResolution{SessionKey: "agent:main:main"},
			Identity: core.AgentIdentity{
				ID:                         "main",
				SubagentTimeoutSeconds:     90,
				SubagentAttachmentsEnabled: false,
			},
		},
	}, map[string]any{
		"task": "inspect",
		"attachments": []any{
			map[string]any{"name": "a.txt", "content": "hello", "encoding": "utf8"},
		},
	})
	if err != nil {
		t.Fatalf("execute sessions_spawn with disabled attachments: %v", err)
	}
	if !strings.Contains(result.Text, "attachments are disabled for this agent's subagent policy") {
		t.Fatalf("unexpected tool result: %s", result.Text)
	}
}

func TestSessionsSpawnToolParsesExplicitCompletionOptOut(t *testing.T) {
	tool := NewSessionsSpawnTool()
	called := false
	_, err := tool.Execute(context.Background(), ToolContext{
		Runtime: spawnToolRuntimeStub{
			spawnSub: func(ctx context.Context, req task.SubagentSpawnRequest) (task.SubagentSpawnResult, error) {
				called = true
				if req.ExpectsCompletionMessage || !req.ExpectsCompletionMessageSet {
					t.Fatalf("expected explicit completion opt-out, got %+v", req)
				}
				return task.SubagentSpawnResult{Status: "accepted"}, nil
			},
		},
		Run: AgentRunContext{
			Session:  core.SessionResolution{SessionKey: "agent:main:main"},
			Identity: core.AgentIdentity{ID: "main", SubagentTimeoutSeconds: 90},
		},
	}, map[string]any{
		"task":                     "inspect",
		"expectsCompletionMessage": false,
	})
	if err != nil {
		t.Fatalf("execute sessions_spawn with expectsCompletionMessage=false: %v", err)
	}
	if !called {
		t.Fatal("expected subagent runtime path to be called")
	}
}
