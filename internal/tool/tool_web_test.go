package tool

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/acp"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/task"
	webpkg "github.com/kocort/kocort/internal/web"
)

type webRuntimeStub struct {
	env *infra.EnvironmentRuntime
}

func (s *webRuntimeStub) Run(context.Context, core.AgentRunRequest) (core.AgentRunResult, error) {
	return core.AgentRunResult{}, nil
}
func (s *webRuntimeStub) SpawnSubagent(context.Context, task.SubagentSpawnRequest) (task.SubagentSpawnResult, error) {
	return task.SubagentSpawnResult{}, nil
}
func (s *webRuntimeStub) SpawnACPSession(context.Context, acp.SessionSpawnRequest) (acp.SessionSpawnResult, error) {
	return acp.SessionSpawnResult{}, nil
}
func (s *webRuntimeStub) PushInbound(context.Context, core.ChannelInboundMessage) (core.ChatSendResponse, error) {
	return core.ChatSendResponse{}, nil
}
func (s *webRuntimeStub) ExecuteTool(context.Context, AgentRunContext, string, map[string]any) (core.ToolResult, error) {
	return core.ToolResult{}, nil
}
func (s *webRuntimeStub) ScheduleTask(context.Context, task.TaskScheduleRequest) (core.TaskRecord, error) {
	return core.TaskRecord{}, nil
}
func (s *webRuntimeStub) ListTasks(context.Context) []core.TaskRecord { return nil }
func (s *webRuntimeStub) GetTask(context.Context, string) *core.TaskRecord {
	return nil
}
func (s *webRuntimeStub) CancelTask(context.Context, string) (*core.TaskRecord, error) {
	return nil, nil
}
func (s *webRuntimeStub) GetAudit() event.AuditRecorder { return nil }
func (s *webRuntimeStub) GetEventBus() event.EventBus   { return nil }
func (s *webRuntimeStub) CheckSessionAccess(session.SessionAccessAction, string, string) session.SessionAccessResult {
	return session.SessionAccessResult{Allowed: true}
}
func (s *webRuntimeStub) ResolveModelSelection(context.Context, core.AgentIdentity, core.AgentRunRequest, core.SessionResolution) (core.ModelSelection, error) {
	return core.ModelSelection{}, nil
}
func (s *webRuntimeStub) GetSessions() *session.SessionStore        { return nil }
func (s *webRuntimeStub) GetIdentities() core.IdentityResolver      { return nil }
func (s *webRuntimeStub) GetProcesses() *ProcessRegistry            { return nil }
func (s *webRuntimeStub) GetMemory() core.MemoryProvider            { return nil }
func (s *webRuntimeStub) GetSubagents() *task.SubagentRegistry      { return nil }
func (s *webRuntimeStub) GetActiveRuns() *task.ActiveRunRegistry    { return nil }
func (s *webRuntimeStub) GetQueue() *task.FollowupQueue             { return nil }
func (s *webRuntimeStub) GetTasks() *task.TaskScheduler             { return nil }
func (s *webRuntimeStub) GetEnvironment() *infra.EnvironmentRuntime { return s.env }
func (s *webRuntimeStub) ResolveChannelConfig(string) config.ChannelConfig {
	return config.ChannelConfig{}
}
func (s *webRuntimeStub) GetSendPolicy() *session.SendPolicyConfig { return nil }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestWebFetchTool(t *testing.T) {
	tool := NewWebFetchTool(nil)
	tool.client = webpkg.NewClientWithHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"text/html"}},
				Body:       io.NopCloser(strings.NewReader(`<html><head><title>Atlas</title></head><body><h1>Hello</h1><p>BLUE-SPARROW-17</p></body></html>`)),
				Request:    req,
			}, nil
		}),
	})
	result, err := tool.Execute(context.Background(), ToolContext{}, map[string]any{"url": "https://example.com/atlas"})
	if err != nil {
		t.Fatalf("web_fetch: %v", err)
	}
	if !strings.Contains(result.Text, `"title":"Atlas"`) || !strings.Contains(result.Text, `BLUE-SPARROW-17`) {
		t.Fatalf("unexpected web_fetch result: %s", result.Text)
	}
}

func TestWebSearchTool(t *testing.T) {
	env := infra.NewEnvironmentRuntime(config.EnvironmentConfig{
		Entries: map[string]config.EnvironmentEntryConfig{
			"KOCORT_WEB_SEARCH_ENDPOINT": {Value: "https://search.test.local"},
			"BRAVE_SEARCH_API_KEY":       {Value: "test-key"},
		},
	})
	tool := NewWebSearchTool(nil)
	tool.client = webpkg.NewClientWithHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if got := req.Header.Get("X-Subscription-Token"); got != "test-key" {
				t.Fatalf("unexpected api key: %q", got)
			}
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"web":{"results":[{"title":"Atlas Launch","url":"https://example.com/atlas","description":"Launch docs"}]}}`)),
				Request:    req,
			}, nil
		}),
	})
	result, err := tool.Execute(context.Background(), ToolContext{
		Runtime: &webRuntimeStub{env: env},
	}, map[string]any{"query": "atlas", "count": float64(1)})
	if err != nil {
		t.Fatalf("web_search: %v", err)
	}
	if !strings.Contains(result.Text, `"status":"ok"`) || !strings.Contains(result.Text, `Atlas Launch`) {
		t.Fatalf("unexpected web_search result: %s", result.Text)
	}
}
