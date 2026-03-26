package tool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/kocort/kocort/internal/acp"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/infra"
	memorypkg "github.com/kocort/kocort/internal/memory"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/task"
)

type stubMemoryProvider struct {
	recall func(ctx context.Context, identity core.AgentIdentity, session core.SessionResolution, message string) ([]core.MemoryHit, error)
}

func (s stubMemoryProvider) Recall(ctx context.Context, identity core.AgentIdentity, session core.SessionResolution, message string) ([]core.MemoryHit, error) {
	return s.recall(ctx, identity, session, message)
}

type stubStatusMemoryProvider struct {
	stubMemoryProvider
	status memorypkg.SearchStatus
}

func (s stubStatusMemoryProvider) SearchStatus(core.AgentIdentity) memorypkg.SearchStatus {
	return s.status
}

type stubRuntimeServices struct {
	memory     core.MemoryProvider
	identities core.IdentityResolver
	sessions   *session.SessionStore
	subagents  *task.SubagentRegistry
}

func (s stubRuntimeServices) Run(context.Context, core.AgentRunRequest) (core.AgentRunResult, error) {
	return core.AgentRunResult{}, nil
}
func (s stubRuntimeServices) SpawnSubagent(context.Context, task.SubagentSpawnRequest) (task.SubagentSpawnResult, error) {
	return task.SubagentSpawnResult{}, nil
}
func (s stubRuntimeServices) SpawnACPSession(context.Context, acp.SessionSpawnRequest) (acp.SessionSpawnResult, error) {
	return acp.SessionSpawnResult{}, nil
}
func (s stubRuntimeServices) PushInbound(context.Context, core.ChannelInboundMessage) (core.ChatSendResponse, error) {
	return core.ChatSendResponse{}, nil
}
func (s stubRuntimeServices) ExecuteTool(context.Context, AgentRunContext, string, map[string]any) (core.ToolResult, error) {
	return core.ToolResult{}, nil
}
func (s stubRuntimeServices) ScheduleTask(context.Context, task.TaskScheduleRequest) (core.TaskRecord, error) {
	return core.TaskRecord{}, nil
}
func (s stubRuntimeServices) ListTasks(context.Context) []core.TaskRecord { return nil }
func (s stubRuntimeServices) GetTask(context.Context, string) *core.TaskRecord {
	return nil
}
func (s stubRuntimeServices) CancelTask(context.Context, string) (*core.TaskRecord, error) {
	return nil, nil
}
func (s stubRuntimeServices) GetAudit() event.AuditRecorder { return nil }
func (s stubRuntimeServices) GetEventBus() event.EventBus   { return nil }
func (s stubRuntimeServices) CheckSessionAccess(session.SessionAccessAction, string, string) session.SessionAccessResult {
	return session.SessionAccessResult{}
}
func (s stubRuntimeServices) ResolveModelSelection(context.Context, core.AgentIdentity, core.AgentRunRequest, core.SessionResolution) (core.ModelSelection, error) {
	return core.ModelSelection{}, nil
}
func (s stubRuntimeServices) GetSessions() *session.SessionStore     { return s.sessions }
func (s stubRuntimeServices) GetIdentities() core.IdentityResolver   { return s.identities }
func (s stubRuntimeServices) GetProcesses() *ProcessRegistry         { return nil }
func (s stubRuntimeServices) GetMemory() core.MemoryProvider         { return s.memory }
func (s stubRuntimeServices) GetSubagents() *task.SubagentRegistry   { return s.subagents }
func (s stubRuntimeServices) GetActiveRuns() *task.ActiveRunRegistry { return nil }
func (s stubRuntimeServices) GetQueue() *task.FollowupQueue          { return nil }
func (s stubRuntimeServices) GetTasks() *task.TaskScheduler          { return nil }
func (s stubRuntimeServices) GetEnvironment() *infra.EnvironmentRuntime {
	return nil
}
func (s stubRuntimeServices) ResolveChannelConfig(string) config.ChannelConfig {
	return config.ChannelConfig{}
}
func (s stubRuntimeServices) GetSendPolicy() *session.SendPolicyConfig { return nil }

func TestMemorySearchToolReturnsUnavailablePayloadOnRecallError(t *testing.T) {
	tool := NewMemorySearchTool()
	result, err := tool.Execute(context.Background(), ToolContext{
		Runtime: stubRuntimeServices{
			memory: stubMemoryProvider{
				recall: func(context.Context, core.AgentIdentity, core.SessionResolution, string) ([]core.MemoryHit, error) {
					return nil, errors.New("qmd missing")
				},
			},
		},
		Run: AgentRunContext{
			Identity: core.AgentIdentity{WorkspaceDir: t.TempDir()},
		},
	}, map[string]any{"query": "atlas"})
	if err != nil {
		t.Fatalf("execute memory_search: %v", err)
	}
	var payload struct {
		Status   string `json:"status"`
		Disabled bool   `json:"disabled"`
		Error    string `json:"error"`
		Count    int    `json:"count"`
		Backend  string `json:"backend"`
	}
	if unmarshalErr := json.Unmarshal(result.JSON, &payload); unmarshalErr != nil {
		t.Fatalf("unmarshal result: %v", unmarshalErr)
	}
	if payload.Status != "unavailable" || !payload.Disabled || payload.Count != 0 || payload.Error == "" || payload.Backend != "" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestMemorySearchToolAppliesMaxResultsAndMinScore(t *testing.T) {
	tool := NewMemorySearchTool()
	result, err := tool.Execute(context.Background(), ToolContext{
		Runtime: stubRuntimeServices{
			memory: stubMemoryProvider{
				recall: func(context.Context, core.AgentIdentity, core.SessionResolution, string) ([]core.MemoryHit, error) {
					return []core.MemoryHit{
						{Source: "MEMORY.md", Path: "MEMORY.md", Snippet: "top", Score: 0.9},
						{Source: "MEMORY.md", Path: "MEMORY.md", Snippet: "mid", Score: 0.5},
						{Source: "MEMORY.md", Path: "MEMORY.md", Snippet: "low", Score: 0.1},
					}, nil
				},
			},
		},
		Run: AgentRunContext{
			Identity: core.AgentIdentity{WorkspaceDir: t.TempDir()},
		},
	}, map[string]any{
		"query":      "atlas",
		"maxResults": float64(2),
		"minScore":   float64(0.4),
	})
	if err != nil {
		t.Fatalf("execute memory_search: %v", err)
	}
	var payload struct {
		Count   int `json:"count"`
		Results []struct {
			Snippet string  `json:"snippet"`
			Score   float64 `json:"score"`
		} `json:"results"`
	}
	if unmarshalErr := json.Unmarshal(result.JSON, &payload); unmarshalErr != nil {
		t.Fatalf("unmarshal result: %v", unmarshalErr)
	}
	if payload.Count != 2 || len(payload.Results) != 2 {
		t.Fatalf("unexpected result count: %+v", payload)
	}
	if payload.Results[0].Snippet != "top" || payload.Results[1].Snippet != "mid" {
		t.Fatalf("unexpected clipped results: %+v", payload.Results)
	}
}

func TestMemorySearchToolIncludesSearchStatusMetadata(t *testing.T) {
	tool := NewMemorySearchTool()
	result, err := tool.Execute(context.Background(), ToolContext{
		Runtime: stubRuntimeServices{
			memory: stubStatusMemoryProvider{
				stubMemoryProvider: stubMemoryProvider{
					recall: func(context.Context, core.AgentIdentity, core.SessionResolution, string) ([]core.MemoryHit, error) {
						return []core.MemoryHit{{Source: "MEMORY.md", Path: "MEMORY.md", Snippet: "atlas", Score: 0.9}}, nil
					},
				},
				status: memorypkg.SearchStatus{
					Backend:  "qmd",
					Provider: "qmd",
					Fallback: "builtin",
					Mode:     "search",
				},
			},
		},
		Run: AgentRunContext{
			Identity: core.AgentIdentity{WorkspaceDir: t.TempDir()},
		},
	}, map[string]any{"query": "atlas"})
	if err != nil {
		t.Fatalf("execute memory_search: %v", err)
	}
	var payload struct {
		Backend  string `json:"backend"`
		Provider string `json:"provider"`
		Fallback string `json:"fallback"`
		Mode     string `json:"mode"`
	}
	if unmarshalErr := json.Unmarshal(result.JSON, &payload); unmarshalErr != nil {
		t.Fatalf("unmarshal result: %v", unmarshalErr)
	}
	if payload.Backend != "qmd" || payload.Provider != "qmd" || payload.Fallback != "builtin" || payload.Mode != "search" {
		t.Fatalf("unexpected status payload: %+v", payload)
	}
}
