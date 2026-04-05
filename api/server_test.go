package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kocort/kocort/api/service"
	"github.com/kocort/kocort/api/types"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/runtime"
	"github.com/kocort/kocort/utils"
)

type blockingBackend struct {
	run func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error)
}

func (b blockingBackend) Run(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
	return b.run(ctx, runCtx)
}

type apiMockAuditRecorder struct{}

func (m *apiMockAuditRecorder) Record(_ context.Context, _ core.AuditEvent) error {
	return nil
}

func (m *apiMockAuditRecorder) List(_ context.Context, _ core.AuditQuery) ([]core.AuditEvent, error) {
	return nil, nil
}

type mockLogger struct{}

func (m *mockLogger) LogAuditEvent(_ core.AuditEvent) {}

func (m *mockLogger) Reload(_ config.LoggingConfig, _ string) error {
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newMockDynamicHTTPClient(statusCode int) *infra.DynamicHTTPClient {
	return infra.NewDynamicHTTPClientFromClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: statusCode,
			Status:     strconv.Itoa(statusCode) + " " + http.StatusText(statusCode),
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Request:    req,
		}, nil
	})})
}

func TestServerDashboardEndpoint(t *testing.T) {
	srv := NewServer(testRuntime(t), config.GatewayConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/system/dashboard", nil)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("dashboard status=%d body=%s", res.Code, res.Body.String())
	}
	var payload core.DashboardSnapshot
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}
	if payload.Runtime.ConfiguredAgent != "main" {
		t.Fatalf("unexpected dashboard payload: %+v", payload)
	}
}

func TestServerAuditListSupportsCategoryTypeLevelAndTextFilters(t *testing.T) {
	srv := NewServer(testRuntime(t), config.GatewayConfig{})
	if err := srv.Runtime.Audit.Record(context.Background(), core.AuditEvent{
		Category:   core.AuditCategoryModel,
		Type:       "request_started",
		Level:      "info",
		AgentID:    "main",
		SessionKey: "agent:main:main",
		RunID:      "run-1",
		Message:    "model request started",
		Data:       map[string]any{"model": "demo"},
	}); err != nil {
		t.Fatalf("record model audit event: %v", err)
	}
	if err := srv.Runtime.Audit.Record(context.Background(), core.AuditEvent{
		Category:   core.AuditCategoryTool,
		Type:       "tool_execute_failed",
		Level:      "error",
		AgentID:    "main",
		SessionKey: "agent:main:main",
		RunID:      "run-2",
		ToolName:   "exec",
		Message:    "tool failed",
		Data:       map[string]any{"command": "echo bad"},
	}); err != nil {
		t.Fatalf("record tool audit event: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/system/audit/list", bytes.NewBufferString(`{"category":"tool","type":"tool_execute_failed","level":"error","text":"echo bad","limit":10}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("audit list status=%d body=%s", res.Code, res.Body.String())
	}
	var payload core.AuditListResponse
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode audit list: %v", err)
	}
	if len(payload.Events) != 1 {
		t.Fatalf("expected one filtered event, got %+v", payload.Events)
	}
	if payload.Events[0].Category != core.AuditCategoryTool || payload.Events[0].Type != "tool_execute_failed" {
		t.Fatalf("unexpected filtered event: %+v", payload.Events[0])
	}
}

func TestServerTasksCreateAndCancel(t *testing.T) {
	srv := NewServer(testRuntime(t), config.GatewayConfig{})
	body := bytes.NewBufferString(`{"title":"follow up","message":"ping later","intervalSeconds":60}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspace/tasks", body)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("create task status=%d body=%s", res.Code, res.Body.String())
	}
	var created core.TaskRecord
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created task: %v", err)
	}
	cancelReq := httptest.NewRequest(http.MethodPost, "/api/workspace/tasks/cancel", bytes.NewBufferString(`{"id":"`+created.ID+`"}`))
	cancelReq.Header.Set("Content-Type", "application/json")
	cancelRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(cancelRes, cancelReq)
	if cancelRes.Code != http.StatusOK {
		t.Fatalf("cancel task status=%d body=%s", cancelRes.Code, cancelRes.Body.String())
	}
	var canceled core.TaskRecord
	if err := json.Unmarshal(cancelRes.Body.Bytes(), &canceled); err != nil {
		t.Fatalf("decode canceled task: %v", err)
	}
	if canceled.Status != core.TaskStatusCanceled {
		t.Fatalf("expected canceled task, got %+v", canceled)
	}
}

func TestServerWorkspaceMediaUsesResolvedDefaultWorkspace(t *testing.T) {
	stateDir := t.TempDir()
	mediaDir := filepath.Join(stateDir, "browser")
	mediaPath := filepath.Join(mediaDir, "sample.png")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatalf("mkdir media dir: %v", err)
	}
	if err := os.WriteFile(mediaPath, []byte("png"), 0o644); err != nil {
		t.Fatalf("write media file: %v", err)
	}

	rt, err := runtime.NewRuntimeFromConfig(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {
					BaseURL: "https://example.com/v1",
					API:     "openai-completions",
					APIKey:  "test-key",
					Models:  []config.ProviderModelConfig{{ID: "gpt-4.1"}},
				},
			},
		},
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{{
				ID:      "main",
				Default: true,
				Model:   config.AgentModelConfig{Primary: "openai/gpt-4.1"},
			}},
		},
		Channels: config.ChannelsConfig{Entries: map[string]config.ChannelConfig{}},
	}, config.RuntimeConfigParams{
		StateDir:  stateDir,
		AgentID:   "main",
		Deliverer: &delivery.MemoryDeliverer{},
	})
	if err != nil {
		t.Fatalf("NewRuntimeFromConfig: %v", err)
	}
	rt.HTTPClient = newMockDynamicHTTPClient(http.StatusOK)

	identity, err := service.ResolveDefaultIdentityPublic(context.Background(), rt)
	if err != nil {
		t.Fatalf("resolve default identity: %v", err)
	}
	if identity.WorkspaceDir != filepath.Join(stateDir, "workspace") {
		t.Fatalf("expected resolved default workspace, got %q", identity.WorkspaceDir)
	}

	srv := NewServer(rt, config.GatewayConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/workspace/media?path="+url.QueryEscape(utils.FileURI(mediaPath)), nil)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("media status=%d body=%s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("expected image/png content type, got %q", got)
	}
	if got := res.Body.String(); got != "png" {
		t.Fatalf("expected media body, got %q", got)
	}
}

func TestServerChatCancelCancelsActiveRun(t *testing.T) {
	rt := testRuntime(t)
	rt.Backends = nil
	rt.Backend = blockingBackend{run: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		select {
		case <-ctx.Done():
			return core.AgentRunResult{}, ctx.Err()
		case <-time.After(5 * time.Second):
			t.Fatal("expected chat cancel to abort active run")
			return core.AgentRunResult{}, nil
		}
	}}
	srv := NewServer(rt, config.GatewayConfig{})
	sessionKey := "agent:main:webchat:direct:webchat-user"

	done := make(chan struct{}, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/api/workspace/chat/send", bytes.NewBufferString(`{"sessionKey":"`+sessionKey+`","message":"hello","channel":"webchat","to":"webchat-user","timeoutMs":5000}`))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		srv.Handler().ServeHTTP(res, req)
		done <- struct{}{}
	}()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if rt.ActiveRuns.IsActive(sessionKey) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !rt.ActiveRuns.IsActive(sessionKey) {
		t.Fatal("expected active run before cancel API")
	}

	cancelReq := httptest.NewRequest(http.MethodPost, "/api/workspace/chat/cancel", bytes.NewBufferString(`{"sessionKey":"`+sessionKey+`"}`))
	cancelReq.Header.Set("Content-Type", "application/json")
	cancelRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(cancelRes, cancelReq)
	if cancelRes.Code != http.StatusOK {
		t.Fatalf("chat cancel status=%d body=%s", cancelRes.Code, cancelRes.Body.String())
	}
	var payload core.ChatCancelResponse
	if err := json.Unmarshal(cancelRes.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode cancel response: %v", err)
	}
	if !payload.Aborted {
		t.Fatalf("expected aborted cancel response, got %+v", payload)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for canceled chat.send to return")
	}
}

func TestServerChatSendForwardsAttachments(t *testing.T) {
	rt := testRuntime(t)
	rt.Backends = nil
	rt.Backend = blockingBackend{run: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		if !strings.Contains(runCtx.Request.Message, "hello") {
			t.Fatalf("unexpected message: %q", runCtx.Request.Message)
		}
		if len(runCtx.Request.Attachments) != 2 {
			t.Fatalf("expected 2 attachments, got %+v", runCtx.Request.Attachments)
		}
		if runCtx.Request.Attachments[0].Name != "photo.png" || runCtx.Request.Attachments[0].MIMEType != "image/png" || string(runCtx.Request.Attachments[0].Content) != "PNGDATA" {
			t.Fatalf("unexpected first attachment: %+v", runCtx.Request.Attachments[0])
		}
		if runCtx.Request.Attachments[1].Name != "notes.txt" || runCtx.Request.Attachments[1].MIMEType != "text/plain" || string(runCtx.Request.Attachments[1].Content) != "hello file" {
			t.Fatalf("unexpected second attachment: %+v", runCtx.Request.Attachments[1])
		}
		runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "ok"})
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "ok"}}}, nil
	}}
	srv := NewServer(rt, config.GatewayConfig{})

	imageB64 := base64.StdEncoding.EncodeToString([]byte("PNGDATA"))
	textB64 := base64.StdEncoding.EncodeToString([]byte("hello file"))
	body := bytes.NewBufferString(`{"sessionKey":"agent:main:webchat:direct:webchat-user","message":"hello","channel":"webchat","to":"webchat-user","attachments":[{"type":"image","mimeType":"image/png","fileName":"photo.png","content":"data:image/png;base64,` + imageB64 + `"},{"type":"file","mimeType":"text/plain","fileName":"notes.txt","content":"` + textB64 + `"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspace/chat/send", body)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("chat.send status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestServerChatSendReturnsNoDefaultModelCodeWhenUnset(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	rt, err := runtime.NewRuntimeFromConfig(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {
					BaseURL: "https://example.com/v1",
					API:     "openai-completions",
					APIKey:  "test-key",
					Models:  []config.ProviderModelConfig{{ID: "gpt-4.1"}},
				},
			},
		},
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{{
				ID:        "main",
				Default:   true,
				Workspace: workspace,
			}},
		},
		Channels: config.ChannelsConfig{Entries: map[string]config.ChannelConfig{}},
	}, config.RuntimeConfigParams{
		StateDir:  t.TempDir(),
		AgentID:   "main",
		Deliverer: &delivery.MemoryDeliverer{},
	})
	if err != nil {
		t.Fatalf("NewRuntimeFromConfig: %v", err)
	}
	srv := NewServer(rt, config.GatewayConfig{})

	req := httptest.NewRequest(http.MethodPost, "/api/workspace/chat/send", bytes.NewBufferString(`{"sessionKey":"agent:main:webchat:direct:webchat-user","message":"hello","channel":"webchat","to":"webchat-user"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("chat.send status=%d body=%s", res.Code, res.Body.String())
	}
	var payload struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if payload.Error != "NO_DEFAULT_MODEL" {
		t.Fatalf("unexpected error payload: %+v", payload)
	}
	if payload.Message != core.ErrNoDefaultModelConfigured.Error() {
		t.Fatalf("unexpected error message: %+v", payload)
	}
}

func TestServerTasksUpdateAndDelete(t *testing.T) {
	srv := NewServer(testRuntime(t), config.GatewayConfig{})
	createReq := httptest.NewRequest(http.MethodPost, "/api/workspace/tasks", bytes.NewBufferString(`{"title":"follow up","message":"ping later","intervalSeconds":60}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusOK {
		t.Fatalf("create task status=%d body=%s", createRes.Code, createRes.Body.String())
	}
	var created core.TaskRecord
	if err := json.Unmarshal(createRes.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created task: %v", err)
	}

	updateBody := bytes.NewBufferString(`{"id":"` + created.ID + `","title":"updated","message":"updated message","intervalSeconds":120}`)
	updateReq := httptest.NewRequest(http.MethodPost, "/api/workspace/tasks/update", updateBody)
	updateReq.Header.Set("Content-Type", "application/json")
	updateRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("update task status=%d body=%s", updateRes.Code, updateRes.Body.String())
	}
	var updated core.TaskRecord
	if err := json.Unmarshal(updateRes.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated task: %v", err)
	}
	if updated.Title != "updated" || updated.Message != "updated message" || updated.IntervalSeconds != 120 {
		t.Fatalf("unexpected updated task: %+v", updated)
	}

	deleteReq := httptest.NewRequest(http.MethodPost, "/api/workspace/tasks/delete", bytes.NewBufferString(`{"id":"`+created.ID+`"}`))
	deleteReq.Header.Set("Content-Type", "application/json")
	deleteRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("delete task status=%d body=%s", deleteRes.Code, deleteRes.Body.String())
	}
}

func TestServerTasksCreatePersistsRecurringAndFailureAlertFields(t *testing.T) {
	srv := NewServer(testRuntime(t), config.GatewayConfig{})
	body := bytes.NewBufferString(`{
		"title":"repeat reminder",
		"message":"stretch",
		"scheduleKind":"every",
		"scheduleEveryMs":300000,
		"payloadKind":"systemEvent",
		"sessionTarget":"main",
		"wakeMode":"next-heartbeat",
		"deliveryMode":"announce",
		"failureAlertAfter":2,
		"failureAlertCooldownMs":60000,
		"failureAlertMode":"announce"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspace/tasks", body)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("create task status=%d body=%s", res.Code, res.Body.String())
	}
	var created core.TaskRecord
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created task: %v", err)
	}
	if created.ScheduleKind != core.TaskScheduleEvery || created.ScheduleEveryMs != 300000 {
		t.Fatalf("expected recurring schedule fields, got %+v", created)
	}
	if created.PayloadKind != core.TaskPayloadKindSystemEvent || created.SessionTarget != core.TaskSessionTargetMain {
		t.Fatalf("expected reminder routing fields, got %+v", created)
	}
	if created.WakeMode != core.TaskWakeNextHeartbeat {
		t.Fatalf("expected next-heartbeat wake mode, got %+v", created)
	}
	if created.FailureAlertAfter != 2 || created.FailureAlertCooldownMs != 60000 || created.FailureAlertMode != "announce" {
		t.Fatalf("expected failure alert fields, got %+v", created)
	}
}

func TestServerEnvironmentSavePersistsAndReloads(t *testing.T) {
	rt, configDir := testRuntimeWithConfigStore(t)
	srv := NewServer(rt, config.GatewayConfig{})
	reqBody := `{"environment":{"strict":true,"entries":{"API_KEY":{"value":"abc123","masked":true}}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/system/environment/save", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("environment save status=%d body=%s", res.Code, res.Body.String())
	}
	if got, ok := rt.Environment.Resolve("API_KEY"); !ok || got != "abc123" {
		t.Fatalf("expected runtime environment reload, got=%q ok=%v", got, ok)
	}
	raw, err := os.ReadFile(filepath.Join(configDir, "kocort.json"))
	if err != nil {
		t.Fatalf("read persisted config: %v", err)
	}
	if !strings.Contains(string(raw), `"API_KEY"`) {
		t.Fatalf("expected persisted environment config, got %s", string(raw))
	}
}

func TestServerDataSavePersistsSystemPromptAndContextFiles(t *testing.T) {
	rt, configDir := testRuntimeWithConfigStore(t)
	srv := NewServer(rt, config.GatewayConfig{})
	reqBody := `{"systemPrompt":"You are a focused assistant.","files":[{"name":"AGENTS.md","content":"# Agent Notes\nFollow the checklist."},{"name":"MEMORY.md","content":"Remember the launch code."}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/engine/data/save", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("data save status=%d body=%s", res.Code, res.Body.String())
	}
	var payload types.DataState
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode data response: %v", err)
	}
	if payload.SystemPrompt != "You are a focused assistant." {
		t.Fatalf("unexpected system prompt: %+v", payload)
	}
	if !strings.Contains(payload.Workspace, "workspace") {
		t.Fatalf("expected workspace path, got %+v", payload)
	}
	if !containsContextFile(payload.Files, "AGENTS.md", "Follow the checklist.") {
		t.Fatalf("expected AGENTS.md in payload, got %+v", payload.Files)
	}
	for _, name := range []string{"SOUL.md", "TOOLS.md", "USER.md", "HEARTBEAT.md", "BOOTSTRAP.md"} {
		if !hasManagedContextFile(payload.Files, name) {
			t.Fatalf("expected managed context file %s in payload, got %+v", name, payload.Files)
		}
	}
	for _, name := range []string{"README.md", "CONTEXT.md", "SYSTEM.md"} {
		if hasManagedContextFile(payload.Files, name) {
			t.Fatalf("did not expect legacy managed context file %s in payload, got %+v", name, payload.Files)
		}
	}
	raw, err := os.ReadFile(filepath.Join(configDir, "kocort.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(raw), "You are a focused assistant.") {
		t.Fatalf("expected persisted prompt, got %s", string(raw))
	}
}

func TestServerSandboxSavePersistsWorkingDirectory(t *testing.T) {
	rt, configDir := testRuntimeWithConfigStore(t)
	srv := NewServer(rt, config.GatewayConfig{})
	reqBody := `{"agents":[{"agentId":"main","sandboxEnabled":true,"sandboxDirs":["/tmp/agent-main-workdir"]},{"agentId":"worker","sandboxEnabled":true,"sandboxDirs":["/tmp/agent-worker-workdir"]}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/engine/sandbox/save", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("sandbox save status=%d body=%s", res.Code, res.Body.String())
	}
	var payload types.SandboxState
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode sandbox response: %v", err)
	}
	if dirs := findAgentSandboxDirs(payload.Agents, "main"); len(dirs) == 0 || dirs[0] != "/tmp/agent-main-workdir" {
		t.Fatalf("expected main sandbox dirs update, got %v", dirs)
	}
	if dirs := findAgentSandboxDirs(payload.Agents, "worker"); len(dirs) == 0 || dirs[0] != "/tmp/agent-worker-workdir" {
		t.Fatalf("expected worker sandbox dirs update, got %v", dirs)
	}
	raw, err := os.ReadFile(filepath.Join(configDir, "kocort.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, "/tmp/agent-main-workdir") || !strings.Contains(body, "/tmp/agent-worker-workdir") {
		t.Fatalf("expected persisted sandbox directories, got %s", body)
	}
}

func TestServerBrainSavePersistsModelsAndPrompt(t *testing.T) {
	rt, configDir := testRuntimeWithConfigStore(t)
	srv := NewServer(rt, config.GatewayConfig{})
	reqBody := `{"models":{"providers":{"openai":{"baseUrl":"https://example.com/v2","api":"openai-completions","apiKey":"key","models":[{"id":"gpt-4.1"}]}}},"systemPrompt":"You are API Brain."}`
	req := httptest.NewRequest(http.MethodPost, "/api/engine/brain/save", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("brain save status=%d body=%s", res.Code, res.Body.String())
	}
	if got := service.ResolveDefaultSystemPrompt(rt.Config); got != "You are API Brain." {
		t.Fatalf("expected updated system prompt, got %q", got)
	}
	raw, err := os.ReadFile(filepath.Join(configDir, "models.json"))
	if err != nil {
		t.Fatalf("read models config: %v", err)
	}
	if !strings.Contains(string(raw), `"openai"`) {
		t.Fatalf("expected persisted models overlay, got %s", string(raw))
	}
}

func TestServerCapabilitiesSaveSyncsSkillAcrossAllAgents(t *testing.T) {
	configDir := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "workspace")
	skillDir := filepath.Join(workspace, "skills", "deploy")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillBody := "---\n---\n# Deploy\nDeploy skill.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillBody), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}
	disabled := false
	rt, err := runtime.NewRuntimeFromConfig(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {BaseURL: "https://example.com/v1", API: "openai-completions", Models: []config.ProviderModelConfig{{ID: "gpt-4.1"}}},
			},
		},
		Skills: config.SkillsConfig{
			Entries: map[string]config.SkillConfigLite{
				"deploy": {Enabled: &disabled},
			},
		},
		Agents: config.AgentsConfig{
			Defaults: &config.AgentDefaultsConfig{Skills: []string{"deploy"}},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Workspace: workspace, Model: config.AgentModelConfig{Primary: "openai/gpt-4.1"}, Skills: []string{"deploy"}},
				{ID: "worker", Workspace: workspace, Model: config.AgentModelConfig{Primary: "openai/gpt-4.1"}, Skills: []string{"deploy"}},
			},
		},
		Channels: config.ChannelsConfig{Entries: map[string]config.ChannelConfig{}},
	}, config.RuntimeConfigParams{
		StateDir:  t.TempDir(),
		AgentID:   "main",
		Deliverer: &delivery.MemoryDeliverer{},
		ConfigLoad: config.ConfigLoadOptions{
			ConfigDir: configDir,
		},
	})
	if err != nil {
		t.Fatalf("NewRuntimeFromConfig: %v", err)
	}
	srv := NewServer(rt, config.GatewayConfig{})

	getReq := httptest.NewRequest(http.MethodGet, "/api/engine/capabilities", nil)
	getRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("capabilities status=%d body=%s", getRes.Code, getRes.Body.String())
	}
	var initial types.CapabilitiesState
	if err := json.Unmarshal(getRes.Body.Bytes(), &initial); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	foundDeployDisabled := false
	for _, skill := range initial.Skills.Skills {
		if skill.SkillKey == "deploy" || skill.Name == "deploy" {
			foundDeployDisabled = skill.Disabled
			break
		}
	}
	if !foundDeployDisabled {
		t.Fatalf("expected disabled deploy skill in capabilities state, got %+v", initial.Skills.Skills)
	}

	enableReq := httptest.NewRequest(http.MethodPost, "/api/engine/capabilities/save", bytes.NewBufferString(`{"skills":{"entries":{"deploy":{"enabled":true}}}}`))
	enableReq.Header.Set("Content-Type", "application/json")
	enableRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(enableRes, enableReq)
	if enableRes.Code != http.StatusOK {
		t.Fatalf("enable capabilities status=%d body=%s", enableRes.Code, enableRes.Body.String())
	}
	if !service.SkillEnabledForAllAgents(rt.Config, "deploy") {
		t.Fatalf("expected deploy enabled for all agents")
	}

	disableReq := httptest.NewRequest(http.MethodPost, "/api/engine/capabilities/save", bytes.NewBufferString(`{"skills":{"entries":{"deploy":{"enabled":false}}}}`))
	disableReq.Header.Set("Content-Type", "application/json")
	disableRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(disableRes, disableReq)
	if disableRes.Code != http.StatusOK {
		t.Fatalf("disable capabilities status=%d body=%s", disableRes.Code, disableRes.Body.String())
	}
	if service.SkillEnabledForAllAgents(rt.Config, "deploy") {
		t.Fatalf("expected deploy disabled for all agents")
	}
	if containsSkill(rt.Config.Agents.Defaults.Skills, "deploy") {
		t.Fatalf("expected deploy removed from defaults skills: %+v", rt.Config.Agents.Defaults.Skills)
	}
	for _, agent := range rt.Config.Agents.List {
		if containsSkill(agent.Skills, "deploy") {
			t.Fatalf("expected deploy removed from agent %s skills: %+v", agent.ID, agent.Skills)
		}
	}
}

func TestServerCapabilitiesSaveSyncsToolAcrossAllAgents(t *testing.T) {
	configDir := t.TempDir()
	rt, err := runtime.NewRuntimeFromConfig(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {BaseURL: "https://example.com/v1", API: "openai-completions", Models: []config.ProviderModelConfig{{ID: "gpt-4.1"}}},
			},
		},
		Agents: config.AgentsConfig{
			Defaults: &config.AgentDefaultsConfig{
				Tools: config.AgentToolPolicyConfig{
					Profile: "coding",
				},
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Model: config.AgentModelConfig{Primary: "openai/gpt-4.1"}, Tools: config.AgentToolPolicyConfig{Profile: "coding"}},
				{ID: "worker", Model: config.AgentModelConfig{Primary: "openai/gpt-4.1"}, Tools: config.AgentToolPolicyConfig{Profile: "coding"}},
			},
		},
		Channels: config.ChannelsConfig{Entries: map[string]config.ChannelConfig{}},
	}, config.RuntimeConfigParams{
		StateDir:  t.TempDir(),
		AgentID:   "main",
		Deliverer: &delivery.MemoryDeliverer{},
		ConfigLoad: config.ConfigLoadOptions{
			ConfigDir: configDir,
		},
	})
	if err != nil {
		t.Fatalf("NewRuntimeFromConfig: %v", err)
	}
	srv := NewServer(rt, config.GatewayConfig{})

	getReq := httptest.NewRequest(http.MethodGet, "/api/engine/capabilities", nil)
	getRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("capabilities status=%d body=%s", getRes.Code, getRes.Body.String())
	}
	var initial types.CapabilitiesState
	if err := json.Unmarshal(getRes.Body.Bytes(), &initial); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	foundExecAllowed := false
	for _, tool := range initial.Tools {
		if tool.Name == "exec" {
			foundExecAllowed = tool.Allowed
			break
		}
	}
	if !foundExecAllowed {
		t.Fatalf("expected exec to be allowed initially, got %+v", initial.Tools)
	}

	disableReq := httptest.NewRequest(http.MethodPost, "/api/engine/capabilities/save", bytes.NewBufferString(`{"toolToggles":{"exec":false}}`))
	disableReq.Header.Set("Content-Type", "application/json")
	disableRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(disableRes, disableReq)
	if disableRes.Code != http.StatusOK {
		t.Fatalf("disable capabilities status=%d body=%s", disableRes.Code, disableRes.Body.String())
	}
	if service.ToolEnabledForAllAgents(rt.Config, "exec") {
		t.Fatalf("expected exec blocked for all agents after disable")
	}

	enableReq := httptest.NewRequest(http.MethodPost, "/api/engine/capabilities/save", bytes.NewBufferString(`{"toolToggles":{"exec":true}}`))
	enableReq.Header.Set("Content-Type", "application/json")
	enableRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(enableRes, enableReq)
	if enableRes.Code != http.StatusOK {
		t.Fatalf("enable capabilities status=%d body=%s", enableRes.Code, enableRes.Body.String())
	}
	if !service.ToolEnabledForAllAgents(rt.Config, "exec") {
		t.Fatalf("expected exec allowed for all agents after enable")
	}
}

func TestServerSkillInstallRunsConfiguredInstaller(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	skillDir := filepath.Join(workspace, "skills", "echo-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillBody := `---
name: echo-skill
description: Echo installer
install-kind: go
install-id: install-go
install-module: example.com/echo@latest
---
# Echo
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillBody), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}
	rt, err := runtime.NewRuntimeBuilder(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {BaseURL: "https://example.com/v1", API: "openai-completions", Models: []config.ProviderModelConfig{{ID: "gpt-4.1"}}},
			},
		},
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{{
				ID:        "main",
				Default:   true,
				Workspace: workspace,
				Model:     config.AgentModelConfig{Primary: "openai/gpt-4.1"},
			}},
		},
		Channels: config.ChannelsConfig{Entries: map[string]config.ChannelConfig{}},
	}, config.RuntimeConfigParams{
		StateDir:  t.TempDir(),
		AgentID:   "main",
		Deliverer: &delivery.MemoryDeliverer{},
	}).
		WithAudit(&apiMockAuditRecorder{}).
		WithLogger(&mockLogger{}).
		Build()
	if err != nil {
		t.Fatalf("Build runtime: %v", err)
	}

	origPath := os.Getenv("PATH")
	tmpBin := t.TempDir()
	fakeGo := filepath.Join(tmpBin, fakeGoBinaryName())
	fakeGoBody := "@echo off\r\necho GO-INSTALL-OK\r\n"
	mode := os.FileMode(0o755)
	if stdruntime.GOOS != "windows" {
		fakeGo = filepath.Join(tmpBin, "go")
		fakeGoBody = "#!/bin/sh\necho GO-INSTALL-OK\n"
	}
	if err := os.WriteFile(fakeGo, []byte(fakeGoBody), mode); err != nil {
		t.Fatalf("write fake go: %v", err)
	}
	if err := os.Setenv("PATH", tmpBin+string(os.PathListSeparator)+origPath); err != nil {
		t.Fatalf("set PATH: %v", err)
	}
	defer os.Setenv("PATH", origPath)

	srv := NewServer(rt, config.GatewayConfig{})
	req := httptest.NewRequest(http.MethodPost, "/api/engine/capabilities/skill/install", bytes.NewBufferString(`{"skillName":"echo-skill","installId":"install-go","timeoutMs":10000}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("skill install status=%d body=%s", res.Code, res.Body.String())
	}
	var payload core.SkillInstallResult
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode install response: %v", err)
	}
	if !payload.OK || !strings.Contains(payload.Stdout, "GO-INSTALL-OK") {
		t.Fatalf("unexpected install result: %+v", payload)
	}
}

func findAgentWorkdir(items []types.AgentWorkdirSnapshot, agentID string) string {
	for _, item := range items {
		if item.AgentID == agentID {
			return item.WorkspaceDir
		}
	}
	return ""
}

func findAgentSandboxDirs(items []types.AgentWorkdirSnapshot, agentID string) []string {
	for _, item := range items {
		if item.AgentID == agentID {
			return item.SandboxDirs
		}
	}
	return nil
}

func fakeGoBinaryName() string {
	if stdruntime.GOOS == "windows" {
		return "go.bat"
	}
	return "go"
}

func containsSkill(skills []string, target string) bool {
	for _, skill := range skills {
		if strings.EqualFold(strings.TrimSpace(skill), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

func TestServerBrainReturnsFlattenedModelRecordsAndPresets(t *testing.T) {
	srv := NewServer(testRuntime(t), config.GatewayConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/engine/brain", nil)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("brain status=%d body=%s", res.Code, res.Body.String())
	}
	var payload types.BrainState
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode brain: %v", err)
	}
	if len(payload.ModelRecords) != 1 {
		t.Fatalf("expected flattened model records, got %+v", payload.ModelRecords)
	}
	if len(payload.ModelPresets) == 0 {
		t.Fatalf("expected embedded model presets")
	}
	if !payload.ModelRecords[0].IsDefault {
		t.Fatalf("expected initial model to be default, got %+v", payload.ModelRecords[0])
	}
}

func TestServerBrainUsesConfigBasedProviderStatus(t *testing.T) {
	rt := testRuntime(t)
	srv := NewServer(rt, config.GatewayConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/engine/brain", nil)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("brain status=%d body=%s", res.Code, res.Body.String())
	}
	var payload types.BrainState
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode brain: %v", err)
	}
	if len(payload.ModelRecords) != 1 {
		t.Fatalf("expected one model record, got %+v", payload.ModelRecords)
	}
}

func TestServerBrainModelCrudAndAssignments(t *testing.T) {
	rt, _ := testRuntimeWithConfigStore(t)
	srv := NewServer(rt, config.GatewayConfig{})

	createReq := httptest.NewRequest(http.MethodPost, "/api/engine/brain/models/upsert", bytes.NewBufferString(`{"presetId":"nvidia","modelId":"nvidia/qwen3.5-plus","apiKey":"k1"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusOK {
		t.Fatalf("create model status=%d body=%s", createRes.Code, createRes.Body.String())
	}
	var created types.BrainState
	if err := json.Unmarshal(createRes.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if !containsModelRecord(created.ModelRecords, "nvidia", "nvidia/qwen3.5-plus") {
		t.Fatalf("expected created model record, got %+v", created.ModelRecords)
	}

	updateReq := httptest.NewRequest(http.MethodPost, "/api/engine/brain/models/upsert", bytes.NewBufferString(`{"existingProviderId":"nvidia","existingModelId":"nvidia/qwen3.5-plus","providerId":"nvidia","modelId":"nvidia/qwen3.5-plus","displayName":"Qwen Plus","baseUrl":"https://integrate.api.nvidia.com/v1","api":"openai-completions","apiKey":"k2","reasoning":true,"contextWindow":65536,"maxTokens":4096}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("update model status=%d body=%s", updateRes.Code, updateRes.Body.String())
	}

	defaultReq := httptest.NewRequest(http.MethodPost, "/api/engine/brain/models/default", bytes.NewBufferString(`{"providerId":"nvidia","modelId":"nvidia/qwen3.5-plus"}`))
	defaultReq.Header.Set("Content-Type", "application/json")
	defaultRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(defaultRes, defaultReq)
	if defaultRes.Code != http.StatusOK {
		t.Fatalf("set default status=%d body=%s", defaultRes.Code, defaultRes.Body.String())
	}
	var defaulted types.BrainState
	if err := json.Unmarshal(defaultRes.Body.Bytes(), &defaulted); err != nil {
		t.Fatalf("decode default response: %v", err)
	}
	if !findModelRecord(defaulted.ModelRecords, "nvidia", "nvidia/qwen3.5-plus").IsDefault {
		t.Fatalf("expected nvidia model default, got %+v", defaulted.ModelRecords)
	}

	fallbackReq := httptest.NewRequest(http.MethodPost, "/api/engine/brain/models/fallback", bytes.NewBufferString(`{"providerId":"openai","modelId":"gpt-4.1","enabled":true}`))
	fallbackReq.Header.Set("Content-Type", "application/json")
	fallbackRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(fallbackRes, fallbackReq)
	if fallbackRes.Code != http.StatusOK {
		t.Fatalf("set fallback status=%d body=%s", fallbackRes.Code, fallbackRes.Body.String())
	}
	var fallbackState types.BrainState
	if err := json.Unmarshal(fallbackRes.Body.Bytes(), &fallbackState); err != nil {
		t.Fatalf("decode fallback response: %v", err)
	}
	if !findModelRecord(fallbackState.ModelRecords, "openai", "gpt-4.1").IsFallback {
		t.Fatalf("expected fallback model, got %+v", fallbackState.ModelRecords)
	}

	deleteReq := httptest.NewRequest(http.MethodPost, "/api/engine/brain/models/delete", bytes.NewBufferString(`{"providerId":"nvidia","modelId":"nvidia/qwen3.5-plus"}`))
	deleteReq.Header.Set("Content-Type", "application/json")
	deleteRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("delete model status=%d body=%s", deleteRes.Code, deleteRes.Body.String())
	}
	var deleted types.BrainState
	if err := json.Unmarshal(deleteRes.Body.Bytes(), &deleted); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if containsModelRecord(deleted.ModelRecords, "nvidia", "nvidia/qwen3.5-plus") {
		t.Fatalf("expected deleted model to be removed, got %+v", deleted.ModelRecords)
	}
}

func TestServerBrainModelUpsertAutoAssignsDefaultWhenMissing(t *testing.T) {
	rt, _ := testRuntimeWithConfigStore(t)
	if err := service.ModifyAndPersist(rt, func(cfg *config.AppConfig) (service.ConfigSections, error) {
		service.SetDefaultSystemPrompt(cfg, "test")
		service.SetBrainModelDefault(cfg, "openai", "gpt-4.1")
		if err := service.DeleteBrainModelRecord(cfg, "openai", "gpt-4.1"); err != nil {
			return service.ConfigSections{}, err
		}
		return service.ConfigSections{Main: true, Models: true}, nil
	}); err != nil {
		t.Fatalf("clear default model: %v", err)
	}

	srv := NewServer(rt, config.GatewayConfig{})
	createReq := httptest.NewRequest(http.MethodPost, "/api/engine/brain/models/upsert", bytes.NewBufferString(`{"presetId":"nvidia","modelId":"nvidia/qwen3.5-plus","apiKey":"k1"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusOK {
		t.Fatalf("create model status=%d body=%s", createRes.Code, createRes.Body.String())
	}
	var created types.BrainState
	if err := json.Unmarshal(createRes.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if !findModelRecord(created.ModelRecords, "nvidia", "nvidia/qwen3.5-plus").IsDefault {
		t.Fatalf("expected first configured model to become default, got %+v", created.ModelRecords)
	}
}

func containsModelRecord(records []types.BrainModelRecord, providerID string, modelID string) bool {
	return findModelRecord(records, providerID, modelID).Key != ""
}

func findModelRecord(records []types.BrainModelRecord, providerID string, modelID string) types.BrainModelRecord {
	for _, record := range records {
		if record.ProviderID == providerID && record.ModelID == modelID {
			return record
		}
	}
	return types.BrainModelRecord{}
}

func testRuntime(t *testing.T) *runtime.Runtime {
	t.Helper()
	workspace := filepath.Join(t.TempDir(), "workspace")
	rt, err := runtime.NewRuntimeFromConfig(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {
					BaseURL: "https://example.com/v1",
					API:     "openai-completions",
					APIKey:  "test-key",
					Models:  []config.ProviderModelConfig{{ID: "gpt-4.1"}},
				},
			},
		},
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{{
				ID:        "main",
				Default:   true,
				Workspace: workspace,
				Model:     config.AgentModelConfig{Primary: "openai/gpt-4.1"},
			}},
		},
		Channels: config.ChannelsConfig{Entries: map[string]config.ChannelConfig{}},
	}, config.RuntimeConfigParams{
		StateDir:  t.TempDir(),
		AgentID:   "main",
		Deliverer: &delivery.MemoryDeliverer{},
	})
	if err != nil {
		t.Fatalf("NewRuntimeFromConfig: %v", err)
	}
	rt.HTTPClient = newMockDynamicHTTPClient(http.StatusOK)
	return rt
}

func testRuntimeWithConfigStore(t *testing.T) (*runtime.Runtime, string) {
	t.Helper()
	configDir := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "workspace")
	rt, err := runtime.NewRuntimeFromConfig(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {
					BaseURL: "https://example.com/v1",
					API:     "openai-completions",
					APIKey:  "test-key",
					Models:  []config.ProviderModelConfig{{ID: "gpt-4.1"}},
				},
			},
		},
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{{
				ID:        "main",
				Default:   true,
				Workspace: workspace,
				Model:     config.AgentModelConfig{Primary: "openai/gpt-4.1"},
			}},
		},
		Channels: config.ChannelsConfig{Entries: map[string]config.ChannelConfig{}},
	}, config.RuntimeConfigParams{
		StateDir:  t.TempDir(),
		AgentID:   "main",
		Deliverer: &delivery.MemoryDeliverer{},
		ConfigLoad: config.ConfigLoadOptions{
			ConfigDir: configDir,
		},
	})
	if err != nil {
		t.Fatalf("NewRuntimeFromConfig: %v", err)
	}
	rt.HTTPClient = newMockDynamicHTTPClient(http.StatusOK)
	return rt, configDir
}

func containsContextFile(files []types.ContextFileState, name string, snippet string) bool {
	for _, file := range files {
		if file.Name == name && strings.Contains(file.Content, snippet) {
			return true
		}
	}
	return false
}

func hasManagedContextFile(files []types.ContextFileState, name string) bool {
	for _, file := range files {
		if file.Name == name {
			return true
		}
	}
	return false
}
