package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	openai "github.com/sashabaranov/go-openai"

	"github.com/kocort/kocort/internal/acp"
	"github.com/kocort/kocort/internal/backend"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/task"
	"github.com/kocort/kocort/internal/tool"

	"github.com/kocort/kocort/utils"
)

func TestSanitizeTranscriptForOpenAIKeepsCompactionSchemaFields(t *testing.T) {
	sanitized := backend.SanitizeTranscriptForOpenAI([]core.TranscriptMessage{{
		ID:               "entry_compact",
		Type:             "compaction",
		Role:             "system",
		Summary:          "compressed history",
		FirstKeptEntryID: "entry_keep_1",
		TokensBefore:     1234,
		Instructions:     "preserve tasks",
		Timestamp:        time.Now().UTC(),
	}})
	if len(sanitized) != 1 {
		t.Fatalf("expected one compaction entry, got %+v", sanitized)
	}
	if sanitized[0].ID != "entry_compact" || sanitized[0].Summary != "compressed history" {
		t.Fatalf("expected id/summary to survive sanitize, got %+v", sanitized[0])
	}
	if sanitized[0].FirstKeptEntryID != "entry_keep_1" || sanitized[0].TokensBefore != 1234 {
		t.Fatalf("expected firstKeptEntryId/tokensBefore to survive sanitize, got %+v", sanitized[0])
	}
}

func TestCommandBackendTextMode(t *testing.T) {
	backend := &backend.CommandBackend{
		Config: core.CommandBackendConfig{
			Command:    "sh",
			Args:       []string{"-c", "cat"},
			InputMode:  core.CommandBackendInputStdin,
			OutputMode: core.CommandBackendOutputText,
		},
	}
	deliverer := &delivery.MemoryDeliverer{}
	dispatcher := delivery.NewReplyDispatcher(deliverer, core.DeliveryTarget{SessionKey: "agent:main:main"})
	runCtx := rtypes.AgentRunContext{
		Request:         core.AgentRunRequest{Message: "hello from stdin"},
		WorkspaceDir:    t.TempDir(),
		SystemPrompt:    "system",
		ReplyDispatcher: dispatcher,
	}
	result, err := backend.Run(context.Background(), runCtx)
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background())
	if err != nil {
		t.Fatalf("command backend: %v", err)
	}
	if len(result.Payloads) != 1 || strings.TrimSpace(result.Payloads[0].Text) != "hello from stdin" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCommandBackendJSONLModeStreamsBlocks(t *testing.T) {
	backend := &backend.CommandBackend{
		Config: core.CommandBackendConfig{
			Command:    "sh",
			Args:       []string{"-c", `echo '{"thread_id":"sess-cmd","text":"part 1","final":false}'; echo '{"usage":{"tokens":7},"text":"done","final":true}'`},
			InputMode:  core.CommandBackendInputArg,
			OutputMode: core.CommandBackendOutputJSONL,
		},
	}
	deliverer := &delivery.MemoryDeliverer{}
	dispatcher := delivery.NewReplyDispatcher(deliverer, core.DeliveryTarget{SessionKey: "agent:main:main"})
	runCtx := rtypes.AgentRunContext{
		Request:         core.AgentRunRequest{Message: "ignored"},
		WorkspaceDir:    t.TempDir(),
		SystemPrompt:    "system",
		ReplyDispatcher: dispatcher,
	}
	result, err := backend.Run(context.Background(), runCtx)
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background())
	if err != nil {
		t.Fatalf("command backend: %v", err)
	}
	if len(result.Payloads) != 2 {
		t.Fatalf("expected two payloads, got %+v", result)
	}
	if len(deliverer.Records) != 2 {
		t.Fatalf("expected dispatched block + final, got %+v", deliverer.Records)
	}
	if result.Usage["sessionId"] != "sess-cmd" || result.Usage["tokens"] != float64(7) {
		t.Fatalf("unexpected usage metadata: %+v", result.Usage)
	}
}

func TestCommandBackendNoOutputWatchdog(t *testing.T) {
	be := &backend.CommandBackend{
		Config: core.CommandBackendConfig{
			Command:         "sh",
			Args:            []string{"-c", "sleep 1"},
			OutputMode:      core.CommandBackendOutputText,
			NoOutputTimeout: 40 * time.Millisecond,
		},
	}
	dispatcher := delivery.NewReplyDispatcher(&delivery.MemoryDeliverer{}, core.DeliveryTarget{SessionKey: "agent:main:main"})
	_, err := be.Run(context.Background(), rtypes.AgentRunContext{
		Request:         core.AgentRunRequest{Message: "ignored"},
		WorkspaceDir:    t.TempDir(),
		SystemPrompt:    "system",
		ReplyDispatcher: dispatcher,
	})
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background())
	if err == nil {
		t.Fatal("expected watchdog error")
	}
	if backend.ErrorReason(err) != backend.BackendFailureTransientHTTP {
		t.Fatalf("expected transient_http watchdog error, got %v", err)
	}
}

func TestBackendRegistryResolvesBackendFamilies(t *testing.T) {
	registry := backend.NewBackendRegistry(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"embed": {API: "openai-completions"},
				"cli": {
					API: "cli",
					Command: &core.CommandBackendConfig{
						Command: "sh",
						Args:    []string{"-c", "cat"},
					},
				},
				"acp": {
					API: "acp",
					Command: &core.CommandBackendConfig{
						Command: "sh",
						Args:    []string{"-c", "cat"},
					},
				},
			},
		},
	}, nil, nil)
	if backend, kind, err := registry.Resolve("embed"); err != nil || kind != "embedded" {
		t.Fatalf("expected embedded backend, got backend=%T kind=%s err=%v", backend, kind, err)
	}
	if backend, kind, err := registry.Resolve("cli"); err != nil || kind != "cli" {
		t.Fatalf("expected cli backend, got backend=%T kind=%s err=%v", backend, kind, err)
	}
	if backend, kind, err := registry.Resolve("acp"); err != nil || kind != "acp" {
		t.Fatalf("expected acp backend, got backend=%T kind=%s err=%v", backend, kind, err)
	}
}

func TestCLIBackendRetriesAfterSessionExpired(t *testing.T) {
	be := &backend.CLIBackend{
		Provider: "claude-cli",
		Command: core.CommandBackendConfig{
			Command:    "sh",
			Args:       []string{"-c", `echo '{"session_id":"fresh-2","text":"CLI-OK"}'`},
			ResumeArgs: []string{"-c", `echo 'session expired' >&2; exit 1`},
			OutputMode: core.CommandBackendOutputJSON,
		},
	}
	deliverer := &delivery.MemoryDeliverer{}
	dispatcher := delivery.NewReplyDispatcher(deliverer, core.DeliveryTarget{SessionKey: "agent:main:main"})
	result, err := be.Run(context.Background(), rtypes.AgentRunContext{
		Request: core.AgentRunRequest{Message: "hello", Timeout: 5 * time.Second},
		Session: core.SessionResolution{
			SessionID:  "sess-1",
			SessionKey: "agent:main:main",
			Entry: &core.SessionEntry{
				SessionID:          "sess-1",
				ClaudeCLISessionID: "stale-session",
			},
		},
		ModelSelection:  core.ModelSelection{Provider: "claude-cli", Model: "model"},
		ReplyDispatcher: dispatcher,
	})
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background())
	if err != nil {
		t.Fatalf("cli backend: %v", err)
	}
	if len(result.Payloads) != 1 || result.Payloads[0].Text != "CLI-OK" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Meta["sessionRetry"] != true {
		t.Fatalf("expected session retry metadata, got %+v", result.Meta)
	}
	if result.Usage["sessionId"] != "fresh-2" {
		t.Fatalf("expected fresh session id, got %+v", result.Usage)
	}
}

func TestOpenAICompatBackendCallsChatCompletions(t *testing.T) {
	var captured struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	serverURL, cleanup := newLoopbackHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
			t.Fatalf("unexpected auth header: %q", auth)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected flusher")
		}
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello \"}}]}\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"from provider\"}}],\"usage\":{\"prompt_tokens\":11}}\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer cleanup()

	backend := backend.NewOpenAICompatBackend(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"nvidia": {
					BaseURL: serverURL + "/v1",
					APIKey:  "test-key",
					API:     "openai-completions",
					Models: []config.ProviderModelConfig{{
						ID:        "nvidia/glm-4-9b",
						MaxTokens: 8192,
					}},
				},
			},
		},
	}, nil, nil)
	backend.BlockReplyCoalescing = nil
	deliverer := &delivery.MemoryDeliverer{}
	dispatcher := delivery.NewReplyDispatcher(deliverer, core.DeliveryTarget{SessionKey: "agent:main:main"})
	runCtx := rtypes.AgentRunContext{
		Request: core.AgentRunRequest{
			Message: "hi there",
		},
		Session: core.SessionResolution{
			SessionID:  "sess-1",
			SessionKey: "agent:main:main",
		},
		ModelSelection: core.ModelSelection{
			Provider: "nvidia",
			Model:    "nvidia/glm-4-9b",
		},
		SystemPrompt:    "You are kocort.",
		ReplyDispatcher: dispatcher,
		Transcript: []core.TranscriptMessage{
			{Role: "user", Text: "earlier"},
		},
	}
	result, err := backend.Run(context.Background(), runCtx)
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background())
	if err != nil {
		t.Fatalf("backend run: %v", err)
	}
	if captured.Model != "nvidia/glm-4-9b" {
		t.Fatalf("unexpected model: %+v", captured)
	}
	if !captured.Stream {
		t.Fatalf("expected stream=true, got %+v", captured)
	}
	if len(captured.Messages) != 3 || captured.Messages[0].Role != "system" || captured.Messages[2].Content != "hi there" {
		t.Fatalf("unexpected messages: %+v", captured.Messages)
	}
	if len(result.Payloads) != 1 || result.Payloads[0].Text != "hello from provider" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(deliverer.Records) < 1 {
		t.Fatalf("expected delivery records, got %+v", deliverer.Records)
	}
	if deliverer.Records[len(deliverer.Records)-1].Payload.Text != "hello from provider" {
		t.Fatalf("unexpected delivery records: %+v", deliverer.Records)
	}
}

func TestOpenAICompatBackendStreamsBlockRepliesAndFinalFromSSE(t *testing.T) {
	var captured struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	serverURL, cleanup := newLoopbackHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected flusher")
		}
		_, _ = io.WriteString(w, "data: {\"id\":\"resp_stream_1\",\"choices\":[{\"delta\":{\"content\":\"HELLO\"}}]}\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"-STREAM\"}}]}\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":2,\"total_tokens\":4}}\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer cleanup()

	be := backend.NewOpenAICompatBackend(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"nvidia": {
					BaseURL: serverURL + "/v1",
					APIKey:  "test-key",
					API:     "openai-completions",
					Models: []config.ProviderModelConfig{{
						ID:        "z-ai/glm4.7",
						MaxTokens: 8192,
					}},
				},
			},
		},
	}, nil, nil)
	be.BlockReplyCoalescing = nil
	deliverer := &delivery.MemoryDeliverer{}
	dispatcher := delivery.NewReplyDispatcher(deliverer, core.DeliveryTarget{SessionKey: "agent:main:main"})
	result, err := be.Run(context.Background(), rtypes.AgentRunContext{
		Request: core.AgentRunRequest{
			RunID:   "run-stream-only",
			Message: "hello",
			Timeout: 10 * time.Second,
		},
		Session:         core.SessionResolution{SessionID: "sess-stream-only", SessionKey: "agent:main:main"},
		ModelSelection:  core.ModelSelection{Provider: "nvidia", Model: "z-ai/glm4.7"},
		SystemPrompt:    "You are kocort.",
		WorkspaceDir:    t.TempDir(),
		ReplyDispatcher: dispatcher,
	})
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background())
	if err != nil {
		t.Fatalf("backend run: %v", err)
	}
	if !captured.Stream {
		t.Fatalf("expected stream=true request, got %+v", captured)
	}
	if got := strings.TrimSpace(result.Payloads[0].Text); got != "HELLO-STREAM" {
		t.Fatalf("unexpected final payload: %+v", result.Payloads)
	}
	if result.Usage["previousResponseId"] != "resp_stream_1" {
		t.Fatalf("expected previous response id, got %+v", result.Usage)
	}
	if len(deliverer.Records) < 3 {
		t.Fatalf("expected block + block + final deliveries, got %+v", deliverer.Records)
	}
	if deliverer.Records[0].Kind != core.ReplyKindBlock || deliverer.Records[0].Payload.Text != "HELLO" {
		t.Fatalf("expected first streamed block, got %+v", deliverer.Records)
	}
	if deliverer.Records[1].Kind != core.ReplyKindBlock || deliverer.Records[1].Payload.Text != "-STREAM" {
		t.Fatalf("expected second streamed block, got %+v", deliverer.Records)
	}
	last := deliverer.Records[len(deliverer.Records)-1]
	if last.Kind != core.ReplyKindFinal || strings.TrimSpace(last.Payload.Text) != "HELLO-STREAM" {
		t.Fatalf("expected final streamed delivery, got %+v", last)
	}
	textDeltaCount := 0
	for _, event := range result.Events {
		if event.Stream == "assistant" && event.Data["type"] == "text_delta" {
			textDeltaCount++
		}
	}
	if textDeltaCount != 2 {
		t.Fatalf("expected 2 text_delta events, got %d events=%+v", textDeltaCount, result.Events)
	}
}

func TestRuntimeRecordsModelEventsForStreamingOpenAICompatRun(t *testing.T) {
	serverURL, cleanup := newLoopbackHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected flusher")
		}
		_, _ = io.WriteString(w, "data: {\"id\":\"resp_audit_stream\",\"choices\":[{\"delta\":{\"content\":\"MODEL\"}}]}\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"-EVENT\"}}]}\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":2,\"total_tokens\":4}}\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer cleanup()

	stateDir := t.TempDir()
	rt, err := NewRuntimeFromConfig(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"nvidia": {
					BaseURL: serverURL + "/v1",
					APIKey:  "test-key",
					API:     "openai-completions",
					Models:  []config.ProviderModelConfig{{ID: "z-ai/glm4.7", MaxTokens: 8192}},
				},
			},
		},
		Tasks: config.TasksConfig{Enabled: utils.BoolPtr(false)},
	}, config.RuntimeConfigParams{
		StateDir:  stateDir,
		AgentID:   "main",
		Provider:  "nvidia",
		Model:     "z-ai/glm4.7",
		Deliverer: &delivery.MemoryDeliverer{},
	})
	if err != nil {
		t.Fatalf("NewRuntimeFromConfig: %v", err)
	}

	_, err = rt.Run(context.Background(), core.AgentRunRequest{
		RunID:      "run-model-events",
		AgentID:    "main",
		SessionKey: session.BuildMainSessionKey("main"),
		Message:    "hello",
		Timeout:    10 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	events, err := rt.Audit.List(context.Background(), core.AuditQuery{Category: core.AuditCategoryModel})
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("expected model audit events")
	}
	hasType := func(want string) bool {
		for _, event := range events {
			if event.Type == want {
				return true
			}
		}
		return false
	}
	if !hasType("request_started") || !hasType("stream_opened") || !hasType("response_completed") {
		t.Fatalf("missing expected model events, got %+v", events)
	}
}

func TestOpenAICompatBackendExecutesNativeToolCalls(t *testing.T) {
	var requestBodies []map[string]any
	serverURL, cleanup := newLoopbackHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requestBodies = append(requestBodies, body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)

		switch len(requestBodies) {
		case 1:
			_, _ = io.WriteString(w, "data: {\"id\":\"resp_tool_round_1\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"exec\",\"arguments\":\"{\\\"command\\\":\\\"echo TOOL-OK\\\"}\"}}]}}]}\n\n")
			flusher.Flush()
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":3}}\n\n")
			flusher.Flush()
		default:
			_, _ = io.WriteString(w, "data: {\"id\":\"resp_tool_round_2\",\"choices\":[{\"delta\":{\"content\":\"FINAL-OK\"}}]}\n\n")
			flusher.Flush()
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"completion_tokens\":2}}\n\n")
			flusher.Flush()
		}
	}))
	defer cleanup()

	backend := backend.NewOpenAICompatBackend(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"nvidia": {
					BaseURL: serverURL + "/v1",
					APIKey:  "test-key",
					API:     "openai-completions",
					Models: []config.ProviderModelConfig{{
						ID:        "z-ai/glm4.7",
						MaxTokens: 8192,
					}},
				},
			},
		},
	}, nil, nil)
	deliverer := &delivery.MemoryDeliverer{}
	dispatcher := delivery.NewReplyDispatcher(deliverer, core.DeliveryTarget{SessionKey: "agent:main:main"})
	runCtx := rtypes.AgentRunContext{
		Runtime: &Runtime{
			Tools: tool.NewToolRegistry(tool.NewExecTool()),
		},
		Request: core.AgentRunRequest{Message: "Use the exec tool."},
		Session: core.SessionResolution{
			SessionID:  "sess-tool",
			SessionKey: "agent:main:main",
		},
		Identity: core.AgentIdentity{
			ID:            "main",
			ToolAllowlist: []string{"exec"},
		},
		ModelSelection:  core.ModelSelection{Provider: "nvidia", Model: "z-ai/glm4.7"},
		AvailableTools:  []tool.Tool{tool.NewExecTool()},
		SystemPrompt:    "You are kocort.",
		WorkspaceDir:    t.TempDir(),
		ReplyDispatcher: dispatcher,
	}
	result, err := backend.Run(context.Background(), runCtx)
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background())
	if err != nil {
		t.Fatalf("backend run: %v", err)
	}
	if len(requestBodies) != 2 {
		t.Fatalf("expected two provider requests, got %d", len(requestBodies))
	}
	if tools, ok := requestBodies[0]["tools"].([]any); !ok || len(tools) != 1 {
		t.Fatalf("expected first request to include exec tool schema, got %+v", requestBodies[0]["tools"])
	}
	secondMessages, ok := requestBodies[1]["messages"].([]any)
	if !ok || len(secondMessages) < 3 {
		t.Fatalf("expected second request to include tool result messages, got %+v", requestBodies[1]["messages"])
	}
	foundToolMessage := false
	for _, messageValue := range secondMessages {
		message, ok := messageValue.(map[string]any)
		if !ok {
			continue
		}
		if message["role"] == "tool" && message["content"] == "TOOL-OK" {
			foundToolMessage = true
			break
		}
	}
	if !foundToolMessage {
		t.Fatalf("expected second request to include tool output, got %+v", secondMessages)
	}
	if len(result.Payloads) != 1 || result.Payloads[0].Text != "FINAL-OK" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(deliverer.Records) < 2 {
		t.Fatalf("expected tool and final deliveries, got %+v", deliverer.Records)
	}
	if deliverer.Records[0].Kind != core.ReplyKindTool || deliverer.Records[0].Payload.Text != "TOOL-OK" {
		t.Fatalf("unexpected tool delivery: %+v", deliverer.Records)
	}
	last := deliverer.Records[len(deliverer.Records)-1]
	if last.Kind != core.ReplyKindFinal || last.Payload.Text != "FINAL-OK" {
		t.Fatalf("unexpected final delivery: %+v", deliverer.Records)
	}
}

func TestSanitizeOpenAICompatMessagesDropsOrphanToolMessages(t *testing.T) {
	sanitized := backend.SanitizeOpenAICompatMessages([]openai.ChatCompletionMessage{
		{Role: "user", Content: "hello"},
		{Role: "tool", ToolCallID: "missing", Name: "exec", Content: "orphan"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []openai.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: struct {
					Name      string `json:"name,omitempty"`
					Arguments string `json:"arguments,omitempty"`
				}{
					Name:      "exec",
					Arguments: `{"command":"echo ok"}`,
				},
			}},
		},
		{Role: "tool", ToolCallID: "call_1", Name: "exec", Content: "ok"},
	})
	if len(sanitized) != 3 {
		t.Fatalf("expected user + assistant + tool, got %+v", sanitized)
	}
	if sanitized[1].Role != "assistant" || len(sanitized[1].ToolCalls) != 1 {
		t.Fatalf("unexpected assistant message: %+v", sanitized[1])
	}
	if sanitized[2].Role != "tool" || sanitized[2].ToolCallID != "call_1" {
		t.Fatalf("unexpected tool message: %+v", sanitized[2])
	}
}

func TestSanitizeTranscriptForOpenAIMergesAdjacentSameRoleMessages(t *testing.T) {
	sanitized := backend.SanitizeTranscriptForOpenAI([]core.TranscriptMessage{
		{Role: "user", Text: "one"},
		{Role: "user", Text: "two"},
		{Role: "assistant", Text: "three"},
		{Role: "assistant", Text: "four"},
	})
	if len(sanitized) != 2 {
		t.Fatalf("expected merged transcript, got %+v", sanitized)
	}
	if sanitized[0].Text != "one\n\ntwo" || sanitized[1].Text != "three\n\nfour" {
		t.Fatalf("unexpected merged transcript: %+v", sanitized)
	}
}

func TestSanitizeTranscriptForOpenAIPreservesToolChain(t *testing.T) {
	sanitized := backend.SanitizeTranscriptForOpenAI([]core.TranscriptMessage{
		{Role: "user", Text: "search docs"},
		{Type: "assistant_partial", Role: "assistant", Text: "thinking"},
		{Type: "tool_call", Role: "assistant", ToolCallID: "call_1", ToolName: "memory_search", Args: map[string]any{"query": "docs"}},
		{Type: "tool_result", Role: "tool", ToolCallID: "call_1", ToolName: "memory_search", Text: "found docs"},
		{Type: "assistant_final", Role: "assistant", Text: "done", Final: true},
	})
	if len(sanitized) != 4 {
		t.Fatalf("expected user + tool call + tool result + final, got %+v", sanitized)
	}
	if sanitized[1].Type != "tool_call" || sanitized[1].ToolName != "memory_search" {
		t.Fatalf("unexpected tool call transcript: %+v", sanitized[1])
	}
	if sanitized[2].Type != "tool_result" || sanitized[2].ToolCallID != "call_1" {
		t.Fatalf("unexpected tool result transcript: %+v", sanitized[2])
	}
	if sanitized[3].Type != "assistant_final" || sanitized[3].Text != "done" {
		t.Fatalf("unexpected final transcript: %+v", sanitized[3])
	}
}

func TestBuildOpenAICompatMessagesIncludesTranscriptToolChain(t *testing.T) {
	messages := backend.BuildOpenAICompatMessages(rtypes.AgentRunContext{
		SystemPrompt: "system",
		Request:      core.AgentRunRequest{Message: "continue"},
		Transcript: []core.TranscriptMessage{
			{Role: "user", Text: "search docs"},
			{Type: "tool_call", Role: "assistant", ToolCallID: "call_1", ToolName: "memory_search", Args: map[string]any{"query": "docs"}},
			{Type: "tool_result", Role: "tool", ToolCallID: "call_1", ToolName: "memory_search", Text: "found docs"},
			{Type: "assistant_final", Role: "assistant", Text: "done", Final: true},
		},
	})
	if len(messages) != 6 {
		t.Fatalf("unexpected message count: %+v", messages)
	}
	if messages[2].Role != "assistant" || len(messages[2].ToolCalls) != 1 || messages[2].ToolCalls[0].ID != "call_1" {
		t.Fatalf("expected transcript tool call in assistant message, got %+v", messages[2])
	}
	if messages[3].Role != "tool" || messages[3].ToolCallID != "call_1" {
		t.Fatalf("expected transcript tool result, got %+v", messages[3])
	}
}

func TestBuildOpenAICompatMessagesPreservesEmptyToolResult(t *testing.T) {
	messages := backend.BuildOpenAICompatMessages(rtypes.AgentRunContext{
		Transcript: []core.TranscriptMessage{
			{Role: "user", Text: "do it"},
			{Type: "tool_call", Role: "assistant", ToolCallID: "call_1", ToolName: "exec", Args: map[string]any{"command": "true"}},
			{Type: "tool_result", Role: "tool", ToolCallID: "call_1", ToolName: "exec", Text: ""},
		},
	})
	if len(messages) != 3 {
		t.Fatalf("unexpected message count: %+v", messages)
	}
	if messages[2].Role != "tool" || messages[2].ToolCallID != "call_1" {
		t.Fatalf("expected tool result message to be preserved, got %+v", messages[2])
	}
}

func TestBuildOpenAICompatMessagesIncludesImageAttachments(t *testing.T) {
	messages := backend.BuildOpenAICompatMessages(rtypes.AgentRunContext{
		Request: core.AgentRunRequest{
			Message: "describe this image",
			Attachments: []core.Attachment{{
				Type:     "image",
				Name:     "pixel.png",
				MIMEType: "image/png",
				Content:  []byte("PNGDATA"),
			}},
		},
	})
	if len(messages) != 1 {
		t.Fatalf("unexpected message count: %+v", messages)
	}
	if messages[0].Role != "user" || len(messages[0].MultiContent) != 2 {
		t.Fatalf("expected multimodal user message, got %+v", messages[0])
	}
	if messages[0].MultiContent[0].Type != openai.ChatMessagePartTypeText || messages[0].MultiContent[0].Text != "describe this image" {
		t.Fatalf("unexpected text part: %+v", messages[0].MultiContent[0])
	}
	wantURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("PNGDATA"))
	if messages[0].MultiContent[1].Type != openai.ChatMessagePartTypeImageURL || messages[0].MultiContent[1].ImageURL == nil || messages[0].MultiContent[1].ImageURL.URL != wantURL {
		t.Fatalf("unexpected image part: %+v", messages[0].MultiContent[1])
	}
}

// TestImageAttachmentSurvivesSanitizeHistoryPipeline verifies that when an
// image attachment message goes through BuildOpenAICompatMessages →
// SanitizeHistoryPipeline (with the nvidia/openai-completions policy), the
// MultiContent parts (text + image_url) are fully preserved.
func TestImageAttachmentSurvivesSanitizeHistoryPipeline(t *testing.T) {
	// Build messages exactly as the real backend does
	messages := backend.BuildOpenAICompatMessages(rtypes.AgentRunContext{
		SystemPrompt: "You are a helpful assistant.",
		Request: core.AgentRunRequest{
			Message: "describe this image",
			Attachments: []core.Attachment{{
				Type:     "image",
				Name:     "photo.jpg",
				MIMEType: "image/jpeg",
				Content:  []byte("JPEGDATA"),
			}},
		},
		Transcript: []core.TranscriptMessage{
			{Role: "user", Text: "hello"},
			{Role: "assistant", Text: "Hi! How can I help?", Type: "assistant_final"},
		},
	})

	// Apply SanitizeHistoryPipeline with the nvidia/openai-completions policy
	policy := backend.ResolveTranscriptPolicy("nvidia", "openai-completions", "qwen3.5-plus")
	allowedNames := map[string]bool{"exec": true}
	sanitized := backend.SanitizeHistoryPipeline(messages, policy, allowedNames)

	// Find the user message with MultiContent
	var found *openai.ChatCompletionMessage
	for i := range sanitized {
		if sanitized[i].Role == "user" && len(sanitized[i].MultiContent) > 0 {
			found = &sanitized[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no multimodal user message found after SanitizeHistoryPipeline; messages: %+v", sanitized)
	}
	if len(found.MultiContent) != 2 {
		t.Fatalf("expected 2 MultiContent parts (text + image), got %d: %+v", len(found.MultiContent), found.MultiContent)
	}
	if found.MultiContent[0].Type != openai.ChatMessagePartTypeText || found.MultiContent[0].Text != "describe this image" {
		t.Fatalf("unexpected text part: %+v", found.MultiContent[0])
	}
	wantURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("JPEGDATA"))
	if found.MultiContent[1].Type != openai.ChatMessagePartTypeImageURL || found.MultiContent[1].ImageURL == nil || found.MultiContent[1].ImageURL.URL != wantURL {
		t.Fatalf("unexpected image part: %+v", found.MultiContent[1])
	}

	// Verify the message can be marshaled to JSON without error (ErrContentFieldsMisused check)
	for _, msg := range sanitized {
		if _, err := json.Marshal(msg); err != nil {
			t.Fatalf("message failed JSON marshal: %v (message: %+v)", err, msg)
		}
	}
}

// TestBuildOpenAICompatMessagesDedupesUserWithImage verifies that when the
// transcript already contains the current user message (as happens in the
// real pipeline: Stage 2 AppendIncomingUserTranscript → Stage 4 LoadTranscript),
// BuildOpenAICompatMessages replaces the text-only duplicate with the
// multimodal version (text + image), avoiding consecutive user messages.
func TestBuildOpenAICompatMessagesDedupesUserWithImage(t *testing.T) {
	messages := backend.BuildOpenAICompatMessages(rtypes.AgentRunContext{
		SystemPrompt: "You are helpful.",
		Request: core.AgentRunRequest{
			Message: "describe this image",
			Attachments: []core.Attachment{{
				Type: "image", Name: "photo.png", MIMEType: "image/png", Content: []byte("PNG"),
			}},
		},
		Transcript: []core.TranscriptMessage{
			{Role: "user", Text: "hello"},
			{Role: "assistant", Text: "Hi!", Type: "assistant_final"},
			// This entry exists because AppendIncomingUserTranscript was called in Stage 2
			{Role: "user", Text: "describe this image"},
		},
	})

	// After the fix, there should be 4 messages total:
	// system, user("hello"), assistant("Hi!"), user(MultiContent with text+image)
	// The duplicate text-only "describe this image" should have been REPLACED.
	if len(messages) != 4 {
		for i, msg := range messages {
			t.Logf("  messages[%d]: role=%s multiContent=%d content=%q", i, msg.Role, len(msg.MultiContent), fmt.Sprintf("%v", msg.Content))
		}
		t.Fatalf("expected 4 messages after dedup, got %d", len(messages))
	}

	// The last message should be the multimodal user message
	last := messages[len(messages)-1]
	if last.Role != "user" || len(last.MultiContent) != 2 {
		t.Fatalf("last message should be multimodal user; got role=%s multiContent=%d", last.Role, len(last.MultiContent))
	}
	if last.MultiContent[0].Type != openai.ChatMessagePartTypeText || last.MultiContent[0].Text != "describe this image" {
		t.Fatalf("unexpected text part: %+v", last.MultiContent[0])
	}
	if last.MultiContent[1].Type != openai.ChatMessagePartTypeImageURL {
		t.Fatalf("unexpected image part type: %v", last.MultiContent[1].Type)
	}

	// The second-to-last message should be assistant, not user (no consecutive user messages)
	if messages[len(messages)-2].Role != "assistant" {
		t.Fatalf("expected assistant before last user message, got %s", messages[len(messages)-2].Role)
	}
}

func TestSanitizeOpenAICompatMessagesPreservesEmptyToolResultForPendingCall(t *testing.T) {
	sanitized := backend.SanitizeOpenAICompatMessages([]openai.ChatCompletionMessage{
		{Role: "user", Content: "do it"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []openai.ToolCall{{
				ID:   "call_1",
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      "exec",
					Arguments: "{}",
				},
			}},
		},
		{Role: "tool", ToolCallID: "call_1", Name: "exec", Content: ""},
	})
	if len(sanitized) != 3 {
		t.Fatalf("expected tool result to survive sanitize, got %+v", sanitized)
	}
	if sanitized[2].Role != "tool" || sanitized[2].ToolCallID != "call_1" {
		t.Fatalf("unexpected sanitized tool message: %+v", sanitized[2])
	}
}

func TestSanitizeOpenAICompatMessagesStripsDanglingToolCallButKeepsAssistantText(t *testing.T) {
	sanitized := backend.SanitizeOpenAICompatMessages([]openai.ChatCompletionMessage{
		{Role: "user", Content: "take a screenshot"},
		{
			Role:    "assistant",
			Content: "我来打开浏览器并截图",
			ToolCalls: []openai.ToolCall{{
				ID:   "call_browser_1",
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      "agent-browser",
					Arguments: `{"action":"screenshot"}`,
				},
			}},
		},
		{Role: "user", Content: "next turn"},
	})
	if len(sanitized) != 3 {
		t.Fatalf("expected dangling tool call to be stripped, got %+v", sanitized)
	}
	if sanitized[1].Role != "assistant" || len(sanitized[1].ToolCalls) != 0 || strings.TrimSpace(fmt.Sprint(sanitized[1].Content)) != "我来打开浏览器并截图" {
		t.Fatalf("expected assistant text without tool calls, got %+v", sanitized[1])
	}
}

func TestBuildOpenAICompatMessagesStripsDanglingTranscriptToolCall(t *testing.T) {
	messages := backend.BuildOpenAICompatMessages(rtypes.AgentRunContext{
		Transcript: []core.TranscriptMessage{
			{Role: "user", Text: "take a screenshot"},
			{Type: "tool_call", Role: "assistant", ToolCallID: "call_browser_1", ToolName: "agent-browser", Text: "我来打开浏览器并截图", Args: map[string]any{"action": "screenshot"}},
		},
		Request: core.AgentRunRequest{Message: "next turn"},
	})
	if len(messages) != 3 {
		t.Fatalf("expected dangling transcript tool call to become plain assistant text, got %+v", messages)
	}
	if messages[1].Role != "assistant" || len(messages[1].ToolCalls) != 0 || strings.TrimSpace(fmt.Sprint(messages[1].Content)) != "我来打开浏览器并截图" {
		t.Fatalf("expected assistant text without tool calls, got %+v", messages[1])
	}
	if messages[2].Role != "user" || strings.TrimSpace(fmt.Sprint(messages[2].Content)) != "next turn" {
		t.Fatalf("unexpected final message sequence: %+v", messages)
	}
}

func TestOpenAICompatBackendUsesJSONToolResultForContinuation(t *testing.T) {
	round := 0
	serverURL, cleanup := newLoopbackHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		round++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		messages, _ := body["messages"].([]any)
		w.Header().Set("content-type", "text/event-stream")
		switch round {
		case 1:
			_, _ = io.WriteString(w, "data: {\"id\":\"msg_json_tool_1\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_json\",\"type\":\"function\",\"function\":{\"name\":\"json_tool\",\"arguments\":\"{\\\"ok\\\":true}\"}}]},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		case 2:
			if len(messages) < 3 {
				t.Fatalf("expected tool continuation messages, got %#v", messages)
			}
			last, _ := messages[len(messages)-1].(map[string]any)
			if strings.TrimSpace(fmt.Sprint(last["role"])) != "tool" {
				t.Fatalf("expected last message to be tool, got %#v", last)
			}
			content := strings.TrimSpace(fmt.Sprint(last["content"]))
			if content != "{\"status\":\"ok\"}" {
				t.Fatalf("expected JSON fallback tool content, got %q", content)
			}
			_, _ = io.WriteString(w, "data: {\"id\":\"msg_json_tool_2\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		default:
			t.Fatalf("unexpected round %d", round)
		}
	}))
	defer cleanup()

	backend := backend.NewOpenAICompatBackend(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {
					BaseURL: serverURL,
					APIKey:  "test-key",
					API:     "openai-completions",
					Models:  []config.ProviderModelConfig{{ID: "gpt-test", MaxTokens: 64}},
				},
			},
		},
	}, infra.NewEnvironmentRuntime(config.EnvironmentConfig{}), nil)
	store := storeForTests(t)
	rt := &Runtime{
		Sessions:   store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{"main": {ID: "main", DefaultProvider: "openai", DefaultModel: "gpt-test"}}),
		Tools: tool.NewToolRegistry(&stubTool{
			name: "json_tool",
			execute: func(ctx context.Context, toolCtx rtypes.ToolContext, args map[string]any) (core.ToolResult, error) {
				return core.ToolResult{JSON: json.RawMessage(`{"status":"ok"}`)}, nil
			},
		}),
	}
	runCtx := rtypes.AgentRunContext{
		Runtime:  rt,
		Request:  core.AgentRunRequest{RunID: "run_json_tool", Message: "do it"},
		Session:  core.SessionResolution{SessionKey: "agent:main:main", SessionID: "sess_json_tool"},
		Identity: core.AgentIdentity{ID: "main"},
		ModelSelection: core.ModelSelection{
			Provider: "openai",
			Model:    "gpt-test",
		},
		SystemPrompt:    "system",
		ReplyDispatcher: delivery.NewReplyDispatcher(&delivery.MemoryDeliverer{}, core.DeliveryTarget{SessionKey: "agent:main:main"}),
		AvailableTools:  []tool.Tool{rt.Tools.Get("json_tool")},
	}
	defer runCtx.ReplyDispatcher.MarkComplete()
	result, err := backend.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("backend run: %v", err)
	}
	if got := task.ExtractFinalText(result); got != "done" {
		t.Fatalf("unexpected final text: %q", got)
	}
}

func TestCommandBackendJSONLTracksStopReasonAndToolEvents(t *testing.T) {
	be := &backend.CommandBackend{
		Config: core.CommandBackendConfig{
			Command:    "sh",
			Args:       []string{"-c", `echo '{"type":"tool_call","text":"tool output"}'; echo '{"type":"final","text":"done","stopReason":"completed"}'`},
			OutputMode: core.CommandBackendOutputJSONL,
		},
	}
	deliverer := &delivery.MemoryDeliverer{}
	dispatcher := delivery.NewReplyDispatcher(deliverer, core.DeliveryTarget{SessionKey: "agent:main:main"})
	result, err := be.Run(context.Background(), rtypes.AgentRunContext{
		Request:         core.AgentRunRequest{Message: "hi"},
		Session:         core.SessionResolution{SessionKey: "agent:main:main", SessionID: "sess-1"},
		ReplyDispatcher: dispatcher,
	})
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background())
	if err != nil {
		t.Fatalf("command backend: %v", err)
	}
	if result.StopReason != "completed" {
		t.Fatalf("expected stopReason completed, got %+v", result)
	}
	if len(deliverer.Records) != 2 || deliverer.Records[0].Kind != core.ReplyKindTool || deliverer.Records[1].Kind != core.ReplyKindFinal {
		t.Fatalf("unexpected delivery records: %+v", deliverer.Records)
	}
}

func TestCommandBackendJSONModeDispatchesFinalReply(t *testing.T) {
	be := &backend.CommandBackend{
		Config: core.CommandBackendConfig{
			Command:    "sh",
			Args:       []string{"-c", `echo '{"text":"JSON-OK","session_id":"sess-json"}'`},
			OutputMode: core.CommandBackendOutputJSON,
		},
	}
	deliverer := &delivery.MemoryDeliverer{}
	dispatcher := delivery.NewReplyDispatcher(deliverer, core.DeliveryTarget{SessionKey: "agent:main:main"})
	result, err := be.Run(context.Background(), rtypes.AgentRunContext{
		Request:         core.AgentRunRequest{Message: "hi"},
		Session:         core.SessionResolution{SessionKey: "agent:main:main", SessionID: "sess-1"},
		ReplyDispatcher: dispatcher,
	})
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background())
	if err != nil {
		t.Fatalf("command backend: %v", err)
	}
	if len(result.Payloads) != 1 || result.Payloads[0].Text != "JSON-OK" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(deliverer.Records) != 1 || deliverer.Records[0].Kind != core.ReplyKindFinal || deliverer.Records[0].Payload.Text != "JSON-OK" {
		t.Fatalf("unexpected delivery records: %+v", deliverer.Records)
	}
}

func TestCommandBackendSystemPromptModeAppendAndReplace(t *testing.T) {
	tests := []struct {
		name             string
		mode             string
		want             string
		systemPromptArg  string
		expectedContains string
	}{
		{name: "append", mode: "append", want: "SYSTEM\n\nUSER"},
		{name: "replace", mode: "replace", want: "SYSTEM"},
		{name: "separate arg bypasses merge", mode: "append", systemPromptArg: "--system", expectedContains: "USER"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command := "cat"
			args := []string{}
			if tt.systemPromptArg != "" {
				command = "sh"
				args = []string{"-c", "cat"}
			}
			be := &backend.CommandBackend{
				Config: core.CommandBackendConfig{
					Command:          command,
					Args:             args,
					OutputMode:       core.CommandBackendOutputText,
					InputMode:        core.CommandBackendInputStdin,
					SystemPromptMode: tt.mode,
					SystemPromptArg:  tt.systemPromptArg,
				},
			}
			dispatcher := delivery.NewReplyDispatcher(&delivery.MemoryDeliverer{}, core.DeliveryTarget{SessionKey: "agent:main:main"})
			result, err := be.Run(context.Background(), rtypes.AgentRunContext{
				Request:         core.AgentRunRequest{Message: "USER"},
				Session:         core.SessionResolution{SessionKey: "agent:main:main", SessionID: "sess-1"},
				SystemPrompt:    "SYSTEM",
				ReplyDispatcher: dispatcher,
			})
			dispatcher.MarkComplete()
			_ = dispatcher.WaitForIdle(context.Background())
			if err != nil {
				t.Fatalf("command backend: %v", err)
			}
			if len(result.Payloads) != 1 {
				t.Fatalf("expected one payload, got %+v", result)
			}
			got := strings.TrimSpace(result.Payloads[0].Text)
			if tt.want != "" && got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
			if tt.expectedContains != "" && !strings.Contains(got, tt.expectedContains) {
				t.Fatalf("expected output to contain %q, got %q", tt.expectedContains, got)
			}
		})
	}
}

func TestCLIBackendSessionExpiredClearsStoredSessionIDAndReportsMeta(t *testing.T) {
	entry := &core.SessionEntry{
		SessionID:     "sess-cli",
		CLISessionIDs: map[string]string{"demo-cli": "stale-session"},
	}
	be := &backend.CLIBackend{
		Provider: "demo-cli",
		Command: core.CommandBackendConfig{
			Command: "sh",
			Args:    []string{"-c", `echo 'CLI-OK'`},
			ResumeArgs: []string{
				"-c",
				`echo 'session expired' >&2; exit 1`,
			},
			OutputMode:         core.CommandBackendOutputText,
			SessionExpiredText: []string{"session expired"},
		},
	}
	dispatcher := delivery.NewReplyDispatcher(&delivery.MemoryDeliverer{}, core.DeliveryTarget{SessionKey: "agent:main:main"})
	result, err := be.Run(context.Background(), rtypes.AgentRunContext{
		Request:         core.AgentRunRequest{Message: "hello", Timeout: 10 * time.Second},
		Session:         core.SessionResolution{SessionKey: "agent:main:main", SessionID: "sess-cli", Entry: entry},
		ReplyDispatcher: dispatcher,
	})
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background())
	if err != nil {
		t.Fatalf("cli backend: %v", err)
	}
	if len(result.Payloads) != 1 || strings.TrimSpace(result.Payloads[0].Text) != "CLI-OK" {
		t.Fatalf("unexpected payloads: %+v", result.Payloads)
	}
	if result.Meta["sessionRetry"] != true {
		t.Fatalf("expected session retry meta, got %+v", result.Meta)
	}
	if result.Meta["watchdogMs"] == nil {
		t.Fatalf("expected watchdog metadata, got %+v", result.Meta)
	}
	if stored := backend.GetCLISessionID(entry, "demo-cli"); stored != "" {
		t.Fatalf("expected stale session id to be cleared, got %q", stored)
	}
}

func TestAcpSessionManagerInitializeRunAndStatus(t *testing.T) {
	store, err := session.NewSessionStore(t.TempDir())
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	var setConfigCalls []string
	manager := acp.NewAcpSessionManager()
	runtime := fakeAcpRuntime{
		ensureSession: func(_ context.Context, input core.AcpEnsureSessionInput) (core.AcpRuntimeHandle, error) {
			return core.AcpRuntimeHandle{
				SessionKey:         input.SessionKey,
				Backend:            "acp-cli",
				RuntimeSessionName: "runtime-1",
				Cwd:                input.Cwd,
				BackendSessionID:   "backend-1",
				AgentSessionID:     "agent-1",
			}, nil
		},
		runTurn: func(_ context.Context, input core.AcpRunTurnInput) error {
			if input.OnEvent != nil {
				if err := input.OnEvent(core.AcpRuntimeEvent{Type: "text_delta", Text: "hello"}); err != nil {
					return err
				}
				if err := input.OnEvent(core.AcpRuntimeEvent{Type: "done", StopReason: "stop"}); err != nil {
					return err
				}
			}
			return nil
		},
		getCaps: func(_ context.Context, _ *core.AcpRuntimeHandle) (core.AcpRuntimeCapabilities, error) {
			return core.AcpRuntimeCapabilities{
				Controls: []core.AcpRuntimeControl{core.AcpControlSetMode, core.AcpControlSetConfigOption, core.AcpControlStatus},
			}, nil
		},
		getStatus: func(_ context.Context, handle core.AcpRuntimeHandle) (core.AcpRuntimeStatus, error) {
			return core.AcpRuntimeStatus{
				Summary:          "alive",
				BackendSessionID: handle.BackendSessionID,
				AgentSessionID:   handle.AgentSessionID,
			}, nil
		},
		setMode: func(_ context.Context, input core.AcpSetModeInput) error {
			if input.Mode != "plan" {
				t.Fatalf("unexpected mode: %+v", input)
			}
			return nil
		},
		setConfig: func(_ context.Context, input core.AcpSetConfigOptionInput) error {
			setConfigCalls = append(setConfigCalls, input.Key+"="+input.Value)
			return nil
		},
	}
	handle, meta, err := manager.InitializeSession(context.Background(), store, runtime, "agent:main:acp:test", "main", core.AcpSessionModePersistent, "/tmp", "acp-cli")
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if handle.BackendSessionID != "backend-1" || meta.Backend != "acp-cli" {
		t.Fatalf("unexpected ACP initialization: handle=%+v meta=%+v", handle, meta)
	}
	entry := store.Entry("agent:main:acp:test")
	entry.ACP.RuntimeOptions = &core.AcpSessionRuntimeOptions{
		RuntimeMode:       "plan",
		Model:             "m1",
		PermissionProfile: "strict",
		TimeoutSeconds:    90,
	}
	if err := store.Upsert("agent:main:acp:test", *entry); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	result, err := manager.RunTurn(context.Background(), store, runtime, "agent:main:acp:test", "hello", "run-1", core.AcpPromptModePrompt, nil)
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}
	if len(result.Payloads) != 1 || result.Payloads[0].Text != "hello" || result.StopReason != "stop" {
		t.Fatalf("unexpected run result: %+v", result)
	}
	if len(setConfigCalls) != 3 {
		t.Fatalf("expected runtime options to be applied, got %+v", setConfigCalls)
	}
	status, err := manager.GetSessionStatus(context.Background(), store, runtime, "agent:main:acp:test")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Backend != "acp-cli" || status.RuntimeStatus == nil || status.RuntimeStatus.Summary != "alive" {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestAcpSessionManagerHonorsConfigOptionKeysAndPersistsSnapshot(t *testing.T) {
	store, err := session.NewSessionStore(t.TempDir())
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	var setConfigCalls []string
	manager := acp.NewAcpSessionManager()
	runtime := fakeAcpRuntime{
		ensureSession: func(_ context.Context, input core.AcpEnsureSessionInput) (core.AcpRuntimeHandle, error) {
			return core.AcpRuntimeHandle{
				SessionKey:         input.SessionKey,
				Backend:            "acp-cli",
				RuntimeSessionName: "runtime-2",
				Cwd:                input.Cwd,
				BackendSessionID:   "backend-2",
				AgentSessionID:     "agent-2",
			}, nil
		},
		runTurn: func(_ context.Context, input core.AcpRunTurnInput) error {
			if input.OnEvent != nil {
				_ = input.OnEvent(core.AcpRuntimeEvent{Type: "done", StopReason: "completed"})
			}
			return nil
		},
		getCaps: func(_ context.Context, _ *core.AcpRuntimeHandle) (core.AcpRuntimeCapabilities, error) {
			return core.AcpRuntimeCapabilities{
				Controls:         []core.AcpRuntimeControl{core.AcpControlSetConfigOption, core.AcpControlStatus},
				ConfigOptionKeys: []string{"model"},
			}, nil
		},
		getStatus: func(_ context.Context, handle core.AcpRuntimeHandle) (core.AcpRuntimeStatus, error) {
			return core.AcpRuntimeStatus{
				Summary:          "healthy",
				BackendSessionID: handle.BackendSessionID,
				AgentSessionID:   handle.AgentSessionID,
				Details:          map[string]any{"provider": "demo"},
			}, nil
		},
		setConfig: func(_ context.Context, input core.AcpSetConfigOptionInput) error {
			setConfigCalls = append(setConfigCalls, input.Key+"="+input.Value)
			return nil
		},
	}
	_, _, err = manager.InitializeSession(context.Background(), store, runtime, "agent:main:acp:snap", "main", core.AcpSessionModePersistent, "/tmp", "acp-cli")
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	entry := store.Entry("agent:main:acp:snap")
	entry.ACP.RuntimeOptions = &core.AcpSessionRuntimeOptions{
		Model:             "m2",
		PermissionProfile: "strict",
		TimeoutSeconds:    45,
		BackendExtras:     map[string]string{"unsupported_extra": "x"},
	}
	if err := store.Upsert("agent:main:acp:snap", *entry); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := manager.RunTurn(context.Background(), store, runtime, "agent:main:acp:snap", "hello", "run-2", core.AcpPromptModePrompt, nil); err != nil {
		t.Fatalf("run turn: %v", err)
	}
	if len(setConfigCalls) != 1 || setConfigCalls[0] != "model=m2" {
		t.Fatalf("expected only supported config option to be applied, got %+v", setConfigCalls)
	}
	updated := store.Entry("agent:main:acp:snap")
	if updated == nil || updated.ACP == nil {
		t.Fatalf("expected ACP metadata to persist")
	}
	if len(updated.ACP.UnsupportedOptions) != 3 {
		t.Fatalf("expected unsupported options to persist, got %+v", updated.ACP.UnsupportedOptions)
	}
	if updated.ACP.RuntimeStatus == nil || updated.ACP.RuntimeStatus.Summary != "healthy" {
		t.Fatalf("expected runtime status to persist, got %+v", updated.ACP.RuntimeStatus)
	}
	if updated.ACP.Capabilities == nil || len(updated.ACP.Capabilities.ConfigOptionKeys) != 1 {
		t.Fatalf("expected capabilities to persist, got %+v", updated.ACP.Capabilities)
	}
	if updated.ACP.Observability == nil || updated.ACP.Observability["sessionKey"] != "agent:main:acp:snap" {
		t.Fatalf("expected observability snapshot, got %+v", updated.ACP.Observability)
	}
	snapshot := manager.SnapshotSessions(store)
	if len(snapshot) != 1 || snapshot[0].SessionKey != "agent:main:acp:snap" {
		t.Fatalf("unexpected ACP snapshot: %+v", snapshot)
	}
}

func TestBuildOpenAICompatToolDefinitionsIncludesCoreSessionAndMemoryTools(t *testing.T) {
	definitions := backend.BuildOpenAICompatToolDefinitions([]tool.Tool{
		tool.NewExecTool(),
		tool.NewMemorySearchTool(),
		tool.NewMemoryGetTool(),
		tool.NewSessionsListTool(),
		tool.NewSessionsHistoryTool(),
		tool.NewSessionsSendTool(),
		tool.NewSessionsSpawnTool(),
		tool.NewSubagentsTool(),
		tool.NewSessionStatusTool(),
	})
	if len(definitions) != 9 {
		t.Fatalf("expected 9 tool definitions, got %d", len(definitions))
	}
	names := map[string]struct{}{}
	for _, definition := range definitions {
		names[definition.Function.Name] = struct{}{}
	}
	for _, expected := range []string{
		"exec",
		"memory_search",
		"memory_get",
		"sessions_list",
		"sessions_history",
		"sessions_send",
		"sessions_spawn",
		"subagents",
		"session_status",
	} {
		if _, ok := names[expected]; !ok {
			t.Fatalf("missing tool schema for %s", expected)
		}
	}
}

func TestOpenAICompatBackendIgnoresToolCallsUnlessFinishReasonRequestsTools(t *testing.T) {
	var requestBodies []map[string]any
	serverURL, cleanup := newLoopbackHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requestBodies = append(requestBodies, body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"id\":\"resp_final_only\",\"choices\":[{\"delta\":{\"content\":\"FINAL-ONLY\",\"tool_calls\":[{\"index\":0,\"id\":\"call_ignored\",\"type\":\"function\",\"function\":{\"name\":\"exec\",\"arguments\":\"{\\\"command\\\":\\\"echo SHOULD-NOT-RUN\\\"}\"}}]}}]}\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()
	}))
	defer cleanup()

	backend := backend.NewOpenAICompatBackend(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"nvidia": {
					BaseURL: serverURL + "/v1",
					APIKey:  "test-key",
					API:     "openai-completions",
					Models: []config.ProviderModelConfig{{
						ID:        "z-ai/glm4.7",
						MaxTokens: 8192,
					}},
				},
			},
		},
	}, nil, nil)
	deliverer := &delivery.MemoryDeliverer{}
	dispatcher := delivery.NewReplyDispatcher(deliverer, core.DeliveryTarget{SessionKey: "agent:main:main"})
	runCtx := rtypes.AgentRunContext{
		Runtime: &Runtime{
			Tools: tool.NewToolRegistry(tool.NewExecTool()),
		},
		Request: core.AgentRunRequest{Message: "Do not call tools."},
		Session: core.SessionResolution{
			SessionID:  "sess-tool",
			SessionKey: "agent:main:main",
		},
		Identity: core.AgentIdentity{
			ID:            "main",
			ToolAllowlist: []string{"exec"},
		},
		ModelSelection:  core.ModelSelection{Provider: "nvidia", Model: "z-ai/glm4.7"},
		AvailableTools:  []tool.Tool{tool.NewExecTool()},
		SystemPrompt:    "You are kocort.",
		WorkspaceDir:    t.TempDir(),
		ReplyDispatcher: dispatcher,
	}
	result, err := backend.Run(context.Background(), runCtx)
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background())
	if err != nil {
		t.Fatalf("backend run: %v", err)
	}
	if len(requestBodies) != 1 {
		t.Fatalf("expected single provider request, got %d", len(requestBodies))
	}
	if len(result.Payloads) != 1 || result.Payloads[0].Text != "FINAL-ONLY" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(deliverer.Records) == 0 {
		t.Fatalf("expected final delivery, got %+v", deliverer.Records)
	}
	last := deliverer.Records[len(deliverer.Records)-1]
	if last.Kind != core.ReplyKindFinal || last.Payload.Text != "FINAL-ONLY" {
		t.Fatalf("unexpected deliveries: %+v", deliverer.Records)
	}
}

func TestOpenAICompatBackendRejectsToolCallWithoutID(t *testing.T) {
	serverURL, cleanup := newLoopbackHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"type\":\"function\",\"function\":{\"name\":\"exec\",\"arguments\":\"{\\\"command\\\":\\\"echo TOOL-OK\\\"}\"}}]}}]}\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
		flusher.Flush()
	}))
	defer cleanup()

	backend := backend.NewOpenAICompatBackend(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"nvidia": {
					BaseURL: serverURL + "/v1",
					APIKey:  "test-key",
					API:     "openai-completions",
					Models:  []config.ProviderModelConfig{{ID: "z-ai/glm4.7", MaxTokens: 8192}},
				},
			},
		},
	}, nil, nil)
	dispatcher := delivery.NewReplyDispatcher(&delivery.MemoryDeliverer{}, core.DeliveryTarget{SessionKey: "agent:main:main"})
	_, err := backend.Run(context.Background(), rtypes.AgentRunContext{
		Runtime: &Runtime{Tools: tool.NewToolRegistry(tool.NewExecTool())},
		Request: core.AgentRunRequest{Message: "Use the tool."},
		Session: core.SessionResolution{SessionID: "sess-tool", SessionKey: "agent:main:main"},
		Identity: core.AgentIdentity{
			ID:            "main",
			ToolAllowlist: []string{"exec"},
		},
		ModelSelection:  core.ModelSelection{Provider: "nvidia", Model: "z-ai/glm4.7"},
		AvailableTools:  []tool.Tool{tool.NewExecTool()},
		SystemPrompt:    "You are Kocort.",
		WorkspaceDir:    t.TempDir(),
		ReplyDispatcher: dispatcher,
	})
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background())
	if err == nil || !strings.Contains(err.Error(), "empty id") {
		t.Fatalf("expected empty id error, got %v", err)
	}
}

func TestOpenAICompatBackendRejectsInvalidToolArgumentsJSON(t *testing.T) {
	serverURL, cleanup := newLoopbackHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_bad_args\",\"type\":\"function\",\"function\":{\"name\":\"exec\",\"arguments\":\"{\\\"command\\\":\"}}]}}]}\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
		flusher.Flush()
	}))
	defer cleanup()

	backend := backend.NewOpenAICompatBackend(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"nvidia": {
					BaseURL: serverURL + "/v1",
					APIKey:  "test-key",
					API:     "openai-completions",
					Models:  []config.ProviderModelConfig{{ID: "z-ai/glm4.7", MaxTokens: 8192}},
				},
			},
		},
	}, nil, nil)
	dispatcher := delivery.NewReplyDispatcher(&delivery.MemoryDeliverer{}, core.DeliveryTarget{SessionKey: "agent:main:main"})
	_, err := backend.Run(context.Background(), rtypes.AgentRunContext{
		Runtime: &Runtime{Tools: tool.NewToolRegistry(tool.NewExecTool())},
		Request: core.AgentRunRequest{Message: "Use the tool."},
		Session: core.SessionResolution{SessionID: "sess-tool", SessionKey: "agent:main:main"},
		Identity: core.AgentIdentity{
			ID:            "main",
			ToolAllowlist: []string{"exec"},
		},
		ModelSelection:  core.ModelSelection{Provider: "nvidia", Model: "z-ai/glm4.7"},
		AvailableTools:  []tool.Tool{tool.NewExecTool()},
		SystemPrompt:    "You are Kocort.",
		WorkspaceDir:    t.TempDir(),
		ReplyDispatcher: dispatcher,
	})
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background())
	if err == nil || !strings.Contains(err.Error(), "invalid arguments JSON") {
		t.Fatalf("expected invalid arguments JSON error, got %v", err)
	}
}

func TestOpenAICompatBackendStreamingIncludesEventsAndPreviousResponseID(t *testing.T) {
	serverURL, cleanup := newLoopbackHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"id\":\"resp_1\",\"choices\":[{\"delta\":{\"content\":\"HELLO\"}}]}\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"-WORLD\"},\"finish_reason\":\"stop\"}],\"usage\":{\"output_tokens\":2}}\n\n")
		flusher.Flush()
	}))
	defer cleanup()
	be := backend.NewOpenAICompatBackend(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"nvidia": {
					BaseURL: serverURL + "/v1",
					APIKey:  "test-key",
					API:     "openai-completions",
					Models:  []config.ProviderModelConfig{{ID: "z-ai/glm4.7", MaxTokens: 8192}},
				},
			},
		},
	}, nil, nil)
	dispatcher := delivery.NewReplyDispatcher(&delivery.MemoryDeliverer{}, core.DeliveryTarget{SessionKey: "agent:main:main"})
	result, err := be.Run(context.Background(), rtypes.AgentRunContext{
		Request:         core.AgentRunRequest{RunID: "run-stream", Message: "hello"},
		Session:         core.SessionResolution{SessionID: "sess-stream", SessionKey: "agent:main:main"},
		ModelSelection:  core.ModelSelection{Provider: "nvidia", Model: "z-ai/glm4.7"},
		SystemPrompt:    "You are Kocort.",
		WorkspaceDir:    t.TempDir(),
		ReplyDispatcher: dispatcher,
	})
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background())
	if err != nil {
		t.Fatalf("backend run: %v", err)
	}
	if got := strings.TrimSpace(result.Payloads[0].Text); got != "HELLO-WORLD" {
		t.Fatalf("unexpected final payload: %+v", result.Payloads)
	}
	if result.Usage["previousResponseId"] != "resp_1" {
		t.Fatalf("expected previousResponseId, got %+v", result.Usage)
	}
	if len(result.Events) < 3 {
		t.Fatalf("expected streamed events, got %+v", result.Events)
	}
	if result.Events[0].Stream != "assistant" || result.Events[len(result.Events)-1].Data["type"] != "usage" {
		t.Fatalf("unexpected event sequence: %+v", result.Events)
	}
}

func TestUsageToMapHandlesMissingCompletionDetails(t *testing.T) {
	usage := backend.UsageToMap(openai.Usage{
		PromptTokens:     1,
		CompletionTokens: 2,
		TotalTokens:      3,
	})
	if usage["prompt_tokens"] != 1 || usage["completion_tokens"] != 2 || usage["total_tokens"] != 3 {
		t.Fatalf("unexpected usage map: %+v", usage)
	}
	if _, ok := usage["reasoning_tokens"]; ok {
		t.Fatalf("did not expect reasoning_tokens when completion details are absent: %+v", usage)
	}
}

func TestOpenAICompatBackendToolLoopIncludesToolEventsAndResponseID(t *testing.T) {
	callCount := 0
	serverURL, cleanup := newLoopbackHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		if callCount == 1 {
			_, _ = io.WriteString(w, "data: {\"id\":\"resp_tool_1\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_exec\",\"type\":\"function\",\"function\":{\"name\":\"exec\",\"arguments\":\"{\\\"command\\\":\\\"printf TOOL-OK\\\"}\"}}]}}]}\n\n")
			flusher.Flush()
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
			flusher.Flush()
			return
		}
		_, _ = io.WriteString(w, "data: {\"id\":\"resp_tool_2\",\"choices\":[{\"delta\":{\"content\":\"FINAL-OK\"}}]}\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()
	}))
	defer cleanup()
	be := backend.NewOpenAICompatBackend(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"nvidia": {
					BaseURL: serverURL + "/v1",
					APIKey:  "test-key",
					API:     "openai-completions",
					Models:  []config.ProviderModelConfig{{ID: "z-ai/glm4.7", MaxTokens: 8192}},
				},
			},
		},
	}, nil, nil)
	dispatcher := delivery.NewReplyDispatcher(&delivery.MemoryDeliverer{}, core.DeliveryTarget{SessionKey: "agent:main:main"})
	result, err := be.Run(context.Background(), rtypes.AgentRunContext{
		Runtime:         &Runtime{Tools: tool.NewToolRegistry(tool.NewExecTool())},
		Request:         core.AgentRunRequest{RunID: "run-tool", Message: "Use tool"},
		Session:         core.SessionResolution{SessionID: "sess-tool", SessionKey: "agent:main:main"},
		Identity:        core.AgentIdentity{ID: "main", ToolAllowlist: []string{"exec"}},
		ModelSelection:  core.ModelSelection{Provider: "nvidia", Model: "z-ai/glm4.7"},
		AvailableTools:  []tool.Tool{tool.NewExecTool()},
		SystemPrompt:    "You are Kocort.",
		WorkspaceDir:    t.TempDir(),
		ReplyDispatcher: dispatcher,
	})
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background())
	if err != nil {
		t.Fatalf("backend run: %v", err)
	}
	if result.Usage["previousResponseId"] != "resp_tool_2" {
		t.Fatalf("expected final response id, got %+v", result.Usage)
	}
	if result.Meta["toolRounds"] != 1 {
		t.Fatalf("expected toolRounds=1, got %+v", result.Meta)
	}
	foundCall := false
	foundResult := false
	for _, event := range result.Events {
		if event.Data["type"] == "tool_call" {
			foundCall = true
		}
		if event.Data["type"] == "tool_result" {
			foundResult = true
		}
	}
	if !foundCall || !foundResult {
		t.Fatalf("expected tool events, got %+v", result.Events)
	}
}

func TestOpenAICompatBackendSupportsMoreThanFourToolRounds(t *testing.T) {
	callCount := 0
	serverURL, cleanup := newLoopbackHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		if callCount <= 5 {
			_, _ = io.WriteString(w, fmt.Sprintf("data: {\"id\":\"resp_tool_%d\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_exec_%d\",\"type\":\"function\",\"function\":{\"name\":\"exec\",\"arguments\":\"{\\\"command\\\":\\\"printf TOOL-%d\\\"}\"}}]}}]}\n\n", callCount, callCount, callCount))
			flusher.Flush()
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
			flusher.Flush()
			return
		}
		_, _ = io.WriteString(w, "data: {\"id\":\"resp_tool_final\",\"choices\":[{\"delta\":{\"content\":\"FINAL-AFTER-FIVE-TOOLS\"}}]}\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()
	}))
	defer cleanup()

	be := backend.NewOpenAICompatBackend(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"nvidia": {
					BaseURL: serverURL + "/v1",
					APIKey:  "test-key",
					API:     "openai-completions",
					Models:  []config.ProviderModelConfig{{ID: "z-ai/glm4.7", MaxTokens: 8192}},
				},
			},
		},
	}, nil, nil)
	dispatcher := delivery.NewReplyDispatcher(&delivery.MemoryDeliverer{}, core.DeliveryTarget{SessionKey: "agent:main:main"})
	result, err := be.Run(context.Background(), rtypes.AgentRunContext{
		Runtime:         &Runtime{Tools: tool.NewToolRegistry(tool.NewExecTool())},
		Request:         core.AgentRunRequest{RunID: "run-tool-5", Message: "Use tool repeatedly"},
		Session:         core.SessionResolution{SessionID: "sess-tool-5", SessionKey: "agent:main:main"},
		Identity:        core.AgentIdentity{ID: "main", ToolAllowlist: []string{"exec"}},
		ModelSelection:  core.ModelSelection{Provider: "nvidia", Model: "z-ai/glm4.7"},
		AvailableTools:  []tool.Tool{tool.NewExecTool()},
		SystemPrompt:    "You are Kocort.",
		WorkspaceDir:    t.TempDir(),
		ReplyDispatcher: dispatcher,
	})
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background())
	if err != nil {
		t.Fatalf("backend run: %v", err)
	}
	if callCount != 6 {
		t.Fatalf("expected six provider rounds, got %d", callCount)
	}
	if len(result.Payloads) != 1 || result.Payloads[0].Text != "FINAL-AFTER-FIVE-TOOLS" {
		t.Fatalf("expected final payload after five tool rounds, got %+v", result.Payloads)
	}
	if result.Meta["toolRounds"] != 5 {
		t.Fatalf("expected toolRounds=5, got %+v", result.Meta)
	}
}

func TestOpenAICompatBackendSupportsFiveToolRoundsWithoutPrematureStop(t *testing.T) {
	callCount := 0
	serverURL, cleanup := newLoopbackHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		if callCount <= 5 {
			_, _ = io.WriteString(w, fmt.Sprintf("data: {\"id\":\"resp_loop_%d\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_exec_loop_%d\",\"type\":\"function\",\"function\":{\"name\":\"exec\",\"arguments\":\"{\\\"command\\\":\\\"printf LOOP-%d\\\"}\"}}]}}]}\n\n", callCount, callCount, callCount))
			flusher.Flush()
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
			flusher.Flush()
			return
		}
		_, _ = io.WriteString(w, "data: {\"id\":\"resp_loop_final\",\"choices\":[{\"delta\":{\"content\":\"FINAL-LOOP-OK\"}}]}\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()
	}))
	defer cleanup()

	be := backend.NewOpenAICompatBackend(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"nvidia": {
					BaseURL: serverURL + "/v1",
					APIKey:  "test-key",
					API:     "openai-completions",
					Models:  []config.ProviderModelConfig{{ID: "z-ai/glm4.7", MaxTokens: 8192}},
				},
			},
		},
	}, nil, nil)
	dispatcher := delivery.NewReplyDispatcher(&delivery.MemoryDeliverer{}, core.DeliveryTarget{SessionKey: "agent:main:main"})
	result, err := be.Run(context.Background(), rtypes.AgentRunContext{
		Runtime:         &Runtime{Tools: tool.NewToolRegistry(tool.NewExecTool())},
		Request:         core.AgentRunRequest{RunID: "run-loop-5", Message: "Use the exec tool several times"},
		Session:         core.SessionResolution{SessionID: "sess-loop-5", SessionKey: "agent:main:main"},
		Identity:        core.AgentIdentity{ID: "main", ToolAllowlist: []string{"exec"}},
		ModelSelection:  core.ModelSelection{Provider: "nvidia", Model: "z-ai/glm4.7"},
		AvailableTools:  []tool.Tool{tool.NewExecTool()},
		SystemPrompt:    "You are Kocort.",
		WorkspaceDir:    t.TempDir(),
		ReplyDispatcher: dispatcher,
	})
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background())
	if err != nil {
		t.Fatalf("backend run: %v", err)
	}
	if callCount != 6 {
		t.Fatalf("expected six requests, got %d", callCount)
	}
	if len(result.Payloads) != 1 || result.Payloads[0].Text != "FINAL-LOOP-OK" {
		t.Fatalf("expected final payload, got %+v", result.Payloads)
	}
}

func TestOpenAICompatBackendTimesOutWhenStreamStalls(t *testing.T) {
	serverURL, cleanup := newLoopbackHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected flusher")
		}
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer cleanup()

	be := backend.NewOpenAICompatBackend(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"nvidia": {
					BaseURL: serverURL + "/v1",
					APIKey:  "test-key",
					API:     "openai-completions",
					Models: []config.ProviderModelConfig{{
						ID:        "nvidia/glm-4-9b",
						MaxTokens: 8192,
					}},
				},
			},
		},
	}, nil, nil)
	be.NoOutputTimeout = 40 * time.Millisecond

	dispatcher := delivery.NewReplyDispatcher(&delivery.MemoryDeliverer{}, core.DeliveryTarget{SessionKey: "agent:main:main"})
	runCtx := rtypes.AgentRunContext{
		Request: core.AgentRunRequest{Message: "hi there"},
		Session: core.SessionResolution{
			SessionID:  "sess-1",
			SessionKey: "agent:main:main",
		},
		ModelSelection:  core.ModelSelection{Provider: "nvidia", Model: "nvidia/glm-4-9b"},
		SystemPrompt:    "You are Kocort.",
		ReplyDispatcher: dispatcher,
	}
	_, err := be.Run(context.Background(), runCtx)
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background())
	if err == nil {
		t.Fatal("expected watchdog timeout")
	}
	if backend.ErrorReason(err) != backend.BackendFailureTransientHTTP {
		t.Fatalf("expected transient_http timeout, got %v", err)
	}
}

func TestAnthropicCompatBackendStreamsBlockRepliesAndFinalFromSSE(t *testing.T) {
	serverURL, cleanup := newLoopbackHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if stream, _ := body["stream"].(bool); !stream {
			t.Fatalf("expected stream=true, got %v", body["stream"])
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_123\",\"content\":[],\"model\":\"claude-test\",\"role\":\"assistant\",\"stop_reason\":\"\",\"stop_sequence\":\"\",\"type\":\"message\",\"usage\":{\"input_tokens\":2,\"output_tokens\":0,\"cache_creation_input_tokens\":0,\"cache_read_input_tokens\":0}}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_start\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_delta\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"HELLO\"}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_delta\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"-STREAM\"}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_stop\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		_, _ = io.WriteString(w, "event: message_delta\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":\"\"},\"usage\":{\"output_tokens\":4}}\n\n")
		_, _ = io.WriteString(w, "event: message_stop\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer cleanup()

	be := backend.NewAnthropicCompatBackend(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"anthropic": {
					BaseURL: serverURL,
					APIKey:  "test-key",
					API:     "anthropic-messages",
					Models: []config.ProviderModelConfig{{
						ID:        "claude-test",
						MaxTokens: 128,
					}},
				},
			},
		},
	}, nil, nil)
	be.BlockReplyCoalescing = nil
	deliverer := &delivery.MemoryDeliverer{}
	dispatcher := delivery.NewReplyDispatcher(deliverer, core.DeliveryTarget{SessionKey: "agent:main:main"})
	runCtx := rtypes.AgentRunContext{
		Runtime: &Runtime{},
		Request: core.AgentRunRequest{
			RunID:    "run_anthropic_stream",
			Message:  "hi",
			Timeout:  5 * time.Second,
			AgentID:  "main",
			Channel:  "webchat",
			To:       "user",
			ChatType: core.ChatTypeDirect,
		},
		Session: core.SessionResolution{
			SessionID:  "sess_anthropic_stream",
			SessionKey: "agent:main:main",
		},
		Identity: core.AgentIdentity{ID: "main"},
		ModelSelection: core.ModelSelection{
			Provider: "anthropic",
			Model:    "claude-test",
		},
		SystemPrompt:    "You are helpful.",
		ReplyDispatcher: dispatcher,
	}

	result, err := be.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("backend run failed: %v", err)
	}
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background())

	if len(deliverer.Records) != 3 {
		t.Fatalf("expected block + block + final deliveries, got %+v", deliverer.Records)
	}
	if deliverer.Records[0].Kind != core.ReplyKindBlock || deliverer.Records[0].Payload.Text != "HELLO" {
		t.Fatalf("unexpected first delivery: %+v", deliverer.Records[0])
	}
	if deliverer.Records[1].Kind != core.ReplyKindBlock || deliverer.Records[1].Payload.Text != "-STREAM" {
		t.Fatalf("unexpected second delivery: %+v", deliverer.Records[1])
	}
	if last := deliverer.Records[2]; last.Kind != core.ReplyKindFinal || strings.TrimSpace(last.Payload.Text) != "HELLO-STREAM" {
		t.Fatalf("unexpected final delivery: %+v", last)
	}
	if got := strings.TrimSpace(result.Payloads[0].Text); got != "HELLO-STREAM" {
		t.Fatalf("unexpected final payload: %q", got)
	}
	if got := result.Usage["previousResponseId"]; got != "msg_123" {
		t.Fatalf("expected previousResponseId=msg_123, got %#v", got)
	}
}

func TestAnthropicCompatBackendExecutesNativeToolUse(t *testing.T) {
	round := 0
	serverURL, cleanup := newLoopbackHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		round++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		messages, _ := body["messages"].([]any)
		w.Header().Set("content-type", "text/event-stream")
		switch round {
		case 1:
			if len(messages) == 0 {
				t.Fatal("expected first round messages")
			}
			_, _ = io.WriteString(w, "event: message_start\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_tool_1\",\"content\":[],\"model\":\"claude-test\",\"role\":\"assistant\",\"stop_reason\":\"\",\"stop_sequence\":\"\",\"type\":\"message\",\"usage\":{\"input_tokens\":2,\"output_tokens\":0,\"cache_creation_input_tokens\":0,\"cache_read_input_tokens\":0}}}\n\n")
			_, _ = io.WriteString(w, "event: content_block_start\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_123\",\"name\":\"test_tool\",\"input\":{}}}\n\n")
			_, _ = io.WriteString(w, "event: content_block_delta\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"value\\\":\\\"abc\\\"}\"}}\n\n")
			_, _ = io.WriteString(w, "event: content_block_stop\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
			_, _ = io.WriteString(w, "event: message_delta\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\",\"stop_sequence\":\"\"},\"usage\":{\"output_tokens\":2}}\n\n")
			_, _ = io.WriteString(w, "event: message_stop\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
		case 2:
			if len(messages) < 3 {
				t.Fatalf("expected assistant tool_use + user tool_result in round two, got %#v", messages)
			}
			_, _ = io.WriteString(w, "event: message_start\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_tool_2\",\"content\":[],\"model\":\"claude-test\",\"role\":\"assistant\",\"stop_reason\":\"\",\"stop_sequence\":\"\",\"type\":\"message\",\"usage\":{\"input_tokens\":4,\"output_tokens\":0,\"cache_creation_input_tokens\":0,\"cache_read_input_tokens\":0}}}\n\n")
			_, _ = io.WriteString(w, "event: content_block_start\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
			_, _ = io.WriteString(w, "event: content_block_delta\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"FINAL-ANTHROPIC\"}}\n\n")
			_, _ = io.WriteString(w, "event: content_block_stop\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
			_, _ = io.WriteString(w, "event: message_delta\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":\"\"},\"usage\":{\"output_tokens\":3}}\n\n")
			_, _ = io.WriteString(w, "event: message_stop\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
		default:
			t.Fatalf("unexpected round %d", round)
		}
	}))
	defer cleanup()

	be := backend.NewAnthropicCompatBackend(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"anthropic": {
					BaseURL: serverURL,
					APIKey:  "test-key",
					API:     "anthropic-messages",
					Models: []config.ProviderModelConfig{{
						ID:        "claude-test",
						MaxTokens: 128,
					}},
				},
			},
		},
	}, nil, nil)
	runtime := &Runtime{
		Tools: tool.NewToolRegistry(),
	}
	runtime.Tools.Register(&stubTool{
		name: "test_tool",
		execute: func(ctx context.Context, toolCtx rtypes.ToolContext, args map[string]any) (core.ToolResult, error) {
			if got := fmt.Sprint(args["value"]); got != "abc" {
				t.Fatalf("unexpected tool args: %#v", args)
			}
			return core.ToolResult{Text: "TOOL-RESULT"}, nil
		},
	})
	deliverer := &delivery.MemoryDeliverer{}
	dispatcher := delivery.NewReplyDispatcher(deliverer, core.DeliveryTarget{SessionKey: "agent:main:main"})
	runCtx := rtypes.AgentRunContext{
		Runtime: runtime,
		Request: core.AgentRunRequest{
			RunID:   "run_anthropic_tool",
			Message: "use a tool",
			Timeout: 5 * time.Second,
		},
		Session: core.SessionResolution{
			SessionID:  "sess_anthropic_tool",
			SessionKey: "agent:main:main",
		},
		Identity: core.AgentIdentity{
			ID:            "main",
			ToolProfile:   "coding",
			ToolAllowlist: []string{"test_tool"},
			MemoryEnabled: false,
		},
		ModelSelection: core.ModelSelection{
			Provider: "anthropic",
			Model:    "claude-test",
		},
		AvailableTools:  []tool.Tool{runtime.Tools.Get("test_tool")},
		SystemPrompt:    "You are helpful.",
		ReplyDispatcher: dispatcher,
	}

	result, err := be.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("backend run failed: %v", err)
	}
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background())

	if len(deliverer.Records) < 2 {
		t.Fatalf("expected tool and final deliveries, got %+v", deliverer.Records)
	}
	if deliverer.Records[0].Kind != core.ReplyKindTool || strings.TrimSpace(deliverer.Records[0].Payload.Text) != "TOOL-RESULT" {
		t.Fatalf("unexpected tool delivery: %+v", deliverer.Records[0])
	}
	last := deliverer.Records[len(deliverer.Records)-1]
	if last.Kind != core.ReplyKindFinal || strings.TrimSpace(last.Payload.Text) != "FINAL-ANTHROPIC" {
		t.Fatalf("unexpected final delivery: %+v", last)
	}
	if got := strings.TrimSpace(result.Payloads[0].Text); got != "FINAL-ANTHROPIC" {
		t.Fatalf("unexpected final payload: %q", got)
	}
	foundToolCall := false
	for _, event := range result.Events {
		if event.Stream == "tool" && event.Data["type"] == "tool_call" {
			foundToolCall = true
			break
		}
	}
	if !foundToolCall {
		t.Fatalf("expected tool_call event, got %+v", result.Events)
	}
}

func TestAnthropicCompatBackendContinuesAfterToolFailure(t *testing.T) {
	round := 0
	serverURL, cleanup := newLoopbackHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		round++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		messages, _ := body["messages"].([]any)
		w.Header().Set("content-type", "text/event-stream")
		switch round {
		case 1:
			_, _ = io.WriteString(w, "event: message_start\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_tool_fail_1\",\"content\":[],\"model\":\"claude-test\",\"role\":\"assistant\",\"stop_reason\":\"\",\"stop_sequence\":\"\",\"type\":\"message\",\"usage\":{\"input_tokens\":2,\"output_tokens\":0,\"cache_creation_input_tokens\":0,\"cache_read_input_tokens\":0}}}\n\n")
			_, _ = io.WriteString(w, "event: content_block_start\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_fail_123\",\"name\":\"test_tool\",\"input\":{}}}\n\n")
			_, _ = io.WriteString(w, "event: content_block_delta\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\n")
			_, _ = io.WriteString(w, "event: content_block_stop\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
			_, _ = io.WriteString(w, "event: message_delta\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\",\"stop_sequence\":\"\"},\"usage\":{\"output_tokens\":2}}\n\n")
			_, _ = io.WriteString(w, "event: message_stop\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
		case 2:
			if len(messages) < 3 {
				t.Fatalf("expected assistant tool_use + user tool_result in round two, got %#v", messages)
			}
			_, _ = io.WriteString(w, "event: message_start\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_tool_fail_2\",\"content\":[],\"model\":\"claude-test\",\"role\":\"assistant\",\"stop_reason\":\"\",\"stop_sequence\":\"\",\"type\":\"message\",\"usage\":{\"input_tokens\":4,\"output_tokens\":0,\"cache_creation_input_tokens\":0,\"cache_read_input_tokens\":0}}}\n\n")
			_, _ = io.WriteString(w, "event: content_block_start\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
			_, _ = io.WriteString(w, "event: content_block_delta\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"FINAL-AFTER-FAIL\"}}\n\n")
			_, _ = io.WriteString(w, "event: content_block_stop\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
			_, _ = io.WriteString(w, "event: message_delta\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":\"\"},\"usage\":{\"output_tokens\":3}}\n\n")
			_, _ = io.WriteString(w, "event: message_stop\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
		default:
			t.Fatalf("unexpected round %d", round)
		}
	}))
	defer cleanup()

	be := backend.NewAnthropicCompatBackend(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"anthropic": {
					BaseURL: serverURL,
					APIKey:  "test-key",
					API:     "anthropic-messages",
					Models: []config.ProviderModelConfig{{
						ID:        "claude-test",
						MaxTokens: 128,
					}},
				},
			},
		},
	}, nil, nil)
	runtime := &Runtime{
		Tools: tool.NewToolRegistry(),
	}
	runtime.Tools.Register(&stubTool{
		name: "test_tool",
		execute: func(ctx context.Context, toolCtx rtypes.ToolContext, args map[string]any) (core.ToolResult, error) {
			return core.ToolResult{}, context.DeadlineExceeded
		},
	})
	deliverer := &delivery.MemoryDeliverer{}
	dispatcher := delivery.NewReplyDispatcher(deliverer, core.DeliveryTarget{SessionKey: "agent:main:main"})
	runCtx := rtypes.AgentRunContext{
		Runtime: runtime,
		Request: core.AgentRunRequest{
			RunID:   "run_anthropic_tool_fail",
			Message: "use a tool",
			Timeout: 5 * time.Second,
		},
		Session: core.SessionResolution{
			SessionID:  "sess_anthropic_tool_fail",
			SessionKey: "agent:main:main",
		},
		Identity: core.AgentIdentity{
			ID:            "main",
			ToolProfile:   "coding",
			ToolAllowlist: []string{"test_tool"},
			MemoryEnabled: false,
		},
		ModelSelection: core.ModelSelection{
			Provider: "anthropic",
			Model:    "claude-test",
		},
		AvailableTools:  []tool.Tool{runtime.Tools.Get("test_tool")},
		SystemPrompt:    "You are helpful.",
		ReplyDispatcher: dispatcher,
	}

	result, err := be.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("backend run failed: %v", err)
	}
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background())

	last := deliverer.Records[len(deliverer.Records)-1]
	if last.Kind != core.ReplyKindFinal || strings.TrimSpace(last.Payload.Text) != "FINAL-AFTER-FAIL" {
		t.Fatalf("unexpected final delivery: %+v", last)
	}
	if got := strings.TrimSpace(result.Payloads[0].Text); got != "FINAL-AFTER-FAIL" {
		t.Fatalf("unexpected final payload: %q", got)
	}
}

func TestSanitizeAnthropicMessagesStripsDanglingToolUseAndMergesUsers(t *testing.T) {
	messages := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock("one")),
		anthropic.NewUserMessage(anthropic.NewTextBlock("two")),
		anthropic.NewAssistantMessage(
			anthropic.NewTextBlock("working"),
			anthropic.NewToolUseBlock("toolu_1", map[string]any{"x": 1}, "demo"),
		),
		anthropic.NewUserMessage(anthropic.NewTextBlock("no tool result here")),
	}
	sanitized := backend.SanitizeAnthropicMessages(messages)
	if len(sanitized) != 3 {
		t.Fatalf("expected merged user + repaired assistant + trailing user, got %#v", sanitized)
	}
	if len(sanitized[0].Content) != 2 {
		t.Fatalf("expected merged user content, got %#v", sanitized[0].Content)
	}
	if len(sanitized[1].Content) != 1 || sanitized[1].Content[0].OfText == nil || strings.TrimSpace(sanitized[1].Content[0].OfText.Text) != "working" {
		t.Fatalf("expected dangling tool_use to be stripped, got %#v", sanitized[1].Content)
	}
}

func TestBuildAnthropicMessagesPrunesOldToolResultsAfterTTL(t *testing.T) {
	runCtx := rtypes.AgentRunContext{
		Session: core.SessionResolution{
			SessionID:  "sess-prune",
			SessionKey: "agent:main:main",
			Entry: &core.SessionEntry{
				SessionID:       "sess-prune",
				LastModelCallAt: time.Now().Add(-10 * time.Minute),
			},
		},
		Identity: core.AgentIdentity{
			ContextPruningMode:                 "cache-ttl",
			ContextPruningTTL:                  5 * time.Minute,
			ContextPruningKeepLastAssistants:   1,
			ContextPruningSoftTrimRatio:        1,
			ContextPruningMinPrunableToolChars: 20,
			ContextPruningSoftTrimMaxChars:     20,
			ContextPruningSoftTrimHeadChars:    5,
			ContextPruningSoftTrimTailChars:    5,
		},
		Transcript: []core.TranscriptMessage{
			{Role: "user", Text: "u1"},
			{Type: "assistant_final", Role: "assistant", Text: "a1", Final: true},
			{Type: "tool_result", Role: "tool", ToolCallID: "call_1", ToolName: "exec", Text: strings.Repeat("A", 64)},
			{Type: "assistant_final", Role: "assistant", Text: "latest", Final: true},
		},
		Request: core.AgentRunRequest{Message: "continue"},
	}
	messages := backend.BuildAnthropicMessages(runCtx.Transcript, runCtx.Request, runCtx.Identity, runCtx.Session)
	if len(messages) != 5 {
		t.Fatalf("unexpected anthropic message count: %#v", messages)
	}
	toolResultBlock := messages[2].Content[0].OfToolResult
	if toolResultBlock == nil {
		t.Fatalf("expected tool result block, got %#v", messages[2].Content)
	}
	got := extractAnthropicToolResultText(toolResultBlock)
	if !strings.Contains(got, "[tool result trimmed from 64 chars]") {
		t.Fatalf("expected pruned tool result content, got %q", got)
	}
}

func TestBuildAnthropicMessagesSkipsPruningWithoutEnoughAssistantMessages(t *testing.T) {
	longText := strings.Repeat("B", 64)
	runCtx := rtypes.AgentRunContext{
		Session: core.SessionResolution{
			SessionID:  "sess-prune-skip",
			SessionKey: "agent:main:main",
			Entry: &core.SessionEntry{
				SessionID:       "sess-prune-skip",
				LastModelCallAt: time.Now().Add(-10 * time.Minute),
			},
		},
		Identity: core.AgentIdentity{
			ContextPruningMode:                 "cache-ttl",
			ContextPruningTTL:                  5 * time.Minute,
			ContextPruningKeepLastAssistants:   3,
			ContextPruningSoftTrimRatio:        1,
			ContextPruningMinPrunableToolChars: 20,
			ContextPruningSoftTrimMaxChars:     20,
			ContextPruningSoftTrimHeadChars:    5,
			ContextPruningSoftTrimTailChars:    5,
		},
		Transcript: []core.TranscriptMessage{
			{Role: "user", Text: "u1"},
			{Type: "assistant_final", Role: "assistant", Text: "a1", Final: true},
			{Type: "tool_result", Role: "tool", ToolCallID: "call_1", ToolName: "exec", Text: longText},
		},
		Request: core.AgentRunRequest{Message: "continue"},
	}
	messages := backend.BuildAnthropicMessages(runCtx.Transcript, runCtx.Request, runCtx.Identity, runCtx.Session)
	toolResultBlock := messages[2].Content[0].OfToolResult
	if toolResultBlock == nil || extractAnthropicToolResultText(toolResultBlock) != longText {
		t.Fatalf("expected unpruned tool result, got %#v", messages[2].Content)
	}
}

func TestBuildAnthropicMessagesHardClearsOlderToolResults(t *testing.T) {
	runCtx := rtypes.AgentRunContext{
		Session: core.SessionResolution{
			SessionID:  "sess-prune-clear",
			SessionKey: "agent:main:main",
			Entry: &core.SessionEntry{
				SessionID:       "sess-prune-clear",
				LastModelCallAt: time.Now().Add(-2 * time.Hour),
			},
		},
		Identity: core.AgentIdentity{
			ContextPruningMode:                 "cache-ttl",
			ContextPruningTTL:                  5 * time.Minute,
			ContextPruningKeepLastAssistants:   1,
			ContextPruningSoftTrimRatio:        0,
			ContextPruningHardClearRatio:       1,
			ContextPruningMinPrunableToolChars: 20,
			ContextPruningHardClearEnabled:     true,
			ContextPruningHardClearPlaceholder: "[Old tool result content cleared]",
		},
		Transcript: []core.TranscriptMessage{
			{Role: "user", Text: "u1"},
			{Type: "assistant_final", Role: "assistant", Text: "a1", Final: true},
			{Type: "tool_result", Role: "tool", ToolCallID: "call_1", ToolName: "exec", Text: strings.Repeat("C", 64)},
			{Type: "assistant_final", Role: "assistant", Text: "latest", Final: true},
		},
		Request: core.AgentRunRequest{Message: "continue"},
	}
	messages := backend.BuildAnthropicMessages(runCtx.Transcript, runCtx.Request, runCtx.Identity, runCtx.Session)
	toolResultBlock := messages[2].Content[0].OfToolResult
	if toolResultBlock == nil {
		t.Fatalf("expected tool result block, got %#v", messages[2].Content)
	}
	got := extractAnthropicToolResultText(toolResultBlock)
	if !strings.Contains(got, "[Old tool result content cleared]") {
		t.Fatalf("expected hard-clear placeholder, got %q", got)
	}
}

func TestBuildAnthropicMessagesIncludesImageAttachments(t *testing.T) {
	messages := backend.BuildAnthropicMessages(nil, core.AgentRunRequest{
		Message: "describe this image",
		Attachments: []core.Attachment{{
			Type:     "image",
			Name:     "pixel.png",
			MIMEType: "image/png",
			Content:  []byte("PNGDATA"),
		}},
	}, core.AgentIdentity{}, core.SessionResolution{})
	if len(messages) != 1 {
		t.Fatalf("unexpected anthropic message count: %#v", messages)
	}
	if len(messages[0].Content) != 2 {
		t.Fatalf("expected text + image blocks, got %#v", messages[0].Content)
	}
	if messages[0].Content[0].OfText == nil || strings.TrimSpace(messages[0].Content[0].OfText.Text) != "describe this image" {
		t.Fatalf("unexpected first block: %#v", messages[0].Content[0])
	}
	imageBlock := messages[0].Content[1].OfImage
	if imageBlock == nil || imageBlock.Source.OfBase64 == nil {
		t.Fatalf("expected image block, got %#v", messages[0].Content[1])
	}
	if string(imageBlock.Source.OfBase64.MediaType) != "image/png" || imageBlock.Source.OfBase64.Data != base64.StdEncoding.EncodeToString([]byte("PNGDATA")) {
		t.Fatalf("unexpected image block payload: %#v", imageBlock.Source.OfBase64)
	}
}

func extractAnthropicToolResultText(block *anthropic.ToolResultBlockParam) string {
	if block == nil {
		return ""
	}
	parts := make([]string, 0, len(block.Content))
	for _, item := range block.Content {
		if item.OfText != nil {
			parts = append(parts, strings.TrimSpace(item.OfText.Text))
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func TestResolveBackendUsesAgentRuntimeACPBackend(t *testing.T) {
	cfg := config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {
					BaseURL: "https://example.com/v1",
					API:     "openai-completions",
					Models:  []config.ProviderModelConfig{{ID: "gpt-4.1"}},
				},
				"acp-live": {
					API: "acp",
					Command: &core.CommandBackendConfig{
						Command: "/bin/echo",
					},
				},
			},
		},
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{{
				ID: "worker",
				Runtime: &config.AgentRuntimeConfig{
					Type: "acp",
					ACP:  &config.AgentRuntimeACPConfig{Backend: "acp-live"},
				},
				Model: config.AgentModelConfig{Primary: "openai/gpt-4.1"},
			}},
		},
	}
	rt, err := NewRuntimeFromConfig(cfg, config.RuntimeConfigParams{StateDir: t.TempDir(), AgentID: "worker"})
	if err != nil {
		t.Fatalf("new runtime from config: %v", err)
	}
	identity, err := rt.Identities.Resolve(context.Background(), "worker")
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	be, kind, err := resolveBackend(rt.Backends, rt.Backend, rtypes.AgentRunContext{
		Identity:       identity,
		ModelSelection: core.ModelSelection{Provider: "openai", Model: "gpt-4.1"},
	})
	if err != nil {
		t.Fatalf("resolve backend: %v", err)
	}
	if kind != "acp" {
		t.Fatalf("expected acp backend kind, got %q", kind)
	}
	if _, ok := be.(*backend.ACPBackend); !ok {
		t.Fatalf("expected ACPBackend, got %T", be)
	}
}

func TestBackendRegistryUsesConfiguredAcpTTL(t *testing.T) {
	cfg := config.AppConfig{
		ACP: config.AcpConfigLite{
			Runtime: config.AcpRuntimeConfigLite{TTLMinutes: 7},
		},
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"acp-live": {
					API: "acp",
					Command: &core.CommandBackendConfig{
						Command: "/bin/echo",
					},
				},
			},
		},
	}
	registry := backend.NewBackendRegistry(cfg, nil, nil)
	be, _, err := registry.Resolve("acp-live")
	if err != nil {
		t.Fatalf("resolve acp backend: %v", err)
	}
	acpBackend, ok := be.(*backend.ACPBackend)
	if !ok || acpBackend.Mgr == nil {
		t.Fatalf("expected ACP backend with manager, got %T", be)
	}
	if acpBackend.Mgr.IdleTTL() != 7*time.Minute {
		t.Fatalf("expected configured ACP ttl, got %s", acpBackend.Mgr.IdleTTL())
	}
}

func TestACPBackendRespectsGlobalEnabledFlag(t *testing.T) {
	backend := &backend.ACPBackend{
		Config: config.AppConfig{
			ACP: config.AcpConfigLite{Enabled: false},
		},
		Provider: "acp-live",
		Command:  core.CommandBackendConfig{Command: "/bin/echo"},
	}
	_, err := backend.Run(context.Background(), rtypes.AgentRunContext{})
	if err == nil || !strings.Contains(err.Error(), "acp.enabled=false") {
		t.Fatalf("expected ACP disabled error, got %v", err)
	}
}

func TestResolveModelSelectionHonorsAllowlistAndStoredOverride(t *testing.T) {
	identity := core.AgentIdentity{
		ID:              "main",
		DefaultProvider: "openai",
		DefaultModel:    "gpt-4.1",
		ModelAllowlist:  []string{"openai/gpt-4.1", "openai/gpt-4.1-mini"},
	}
	session := core.SessionResolution{
		SessionID:  "sess_1",
		SessionKey: "agent:main:main",
		Entry: &core.SessionEntry{
			SessionID:        "sess_1",
			ProviderOverride: "openai",
			ModelOverride:    "gpt-4.1-mini",
		},
	}
	selection, err := backend.ResolveModelSelection(context.Background(), identity, core.AgentRunRequest{}, session)
	if err != nil {
		t.Fatalf("resolve model selection: %v", err)
	}
	if selection.Model != "gpt-4.1-mini" {
		t.Fatalf("expected stored override to win, got %+v", selection)
	}
}

func TestResolveModelSelectionDropsDisallowedStoredOverride(t *testing.T) {
	identity := core.AgentIdentity{
		ID:              "main",
		DefaultProvider: "openai",
		DefaultModel:    "gpt-4.1",
		ModelAllowlist:  []string{"openai/gpt-4.1"},
	}
	session := core.SessionResolution{
		SessionID:  "sess_1",
		SessionKey: "agent:main:main",
		Entry: &core.SessionEntry{
			SessionID:        "sess_1",
			ProviderOverride: "openai",
			ModelOverride:    "gpt-4.1-mini",
		},
	}
	selection, err := backend.ResolveModelSelection(context.Background(), identity, core.AgentRunRequest{}, session)
	if err != nil {
		t.Fatalf("resolve model selection: %v", err)
	}
	if selection.Model != "gpt-4.1" {
		t.Fatalf("expected default model after disallowed override, got %+v", selection)
	}
}

func TestRunDropsInvalidStoredModelOverrideAndFallsBackToConfiguredDefault(t *testing.T) {
	baseDir := t.TempDir()
	rt, err := NewRuntimeFromConfig(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"nvidia": {
					BaseURL: "https://example.com/v1",
					APIKey:  "test-key",
					API:     "openai-completions",
					Models: []config.ProviderModelConfig{{
						ID:        "qwen3.5-plus",
						MaxTokens: 8192,
					}},
				},
			},
		},
		Agents: config.AgentsConfig{
			Defaults: &config.AgentDefaultsConfig{
				Model: config.AgentModelConfig{Primary: "nvidia/qwen3.5-plus"},
			},
			List: []config.AgentConfig{{
				ID:      "main",
				Default: true,
				Model:   config.AgentModelConfig{Primary: "nvidia/qwen3.5-plus"},
			}},
		},
	}, config.RuntimeConfigParams{
		StateDir:  baseDir,
		AgentID:   "main",
		Deliverer: &delivery.MemoryDeliverer{},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	rt.Backends = nil
	sessionKey := session.BuildDirectSessionKey("main", "webchat", "webchat-user")
	if err := rt.Sessions.Upsert(sessionKey, core.SessionEntry{
		SessionID:        "sess_old",
		ProviderOverride: "nvidia",
		ModelOverride:    "z-ai/glm4.7",
	}); err != nil {
		t.Fatalf("upsert stale session override: %v", err)
	}
	var gotProvider, gotModel string
	rt.Backend = backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		gotProvider = runCtx.ModelSelection.Provider
		gotModel = runCtx.ModelSelection.Model
		return core.AgentRunResult{
			Payloads: []core.ReplyPayload{{Text: "OK"}},
		}, nil
	})
	result, err := rt.Run(context.Background(), core.AgentRunRequest{
		AgentID:    "main",
		SessionKey: sessionKey,
		Channel:    "webchat",
		To:         "webchat-user",
		Message:    "hello",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotProvider != "nvidia" || gotModel != "qwen3.5-plus" {
		t.Fatalf("expected fallback to configured default model, got %s/%s", gotProvider, gotModel)
	}
	if len(result.Payloads) != 1 || result.Payloads[0].Text != "OK" {
		t.Fatalf("unexpected run result: %+v", result)
	}
}

func TestRunUsesUpdatedConfiguredDefaultWithoutRestart(t *testing.T) {
	baseDir := t.TempDir()
	cfg := config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {
					BaseURL: "https://example.com/v1",
					API:     "openai-completions",
					Models: []config.ProviderModelConfig{
						{ID: "gpt-4.1"},
						{ID: "gpt-4.1-mini"},
					},
				},
			},
		},
		Agents: config.AgentsConfig{
			Defaults: &config.AgentDefaultsConfig{
				Model: config.AgentModelConfig{Primary: "openai/gpt-4.1"},
			},
			List: []config.AgentConfig{{
				ID:      "main",
				Default: true,
				Model:   config.AgentModelConfig{Primary: "openai/gpt-4.1"},
			}},
		},
	}
	rt, err := NewRuntimeFromConfig(cfg, config.RuntimeConfigParams{
		StateDir:  baseDir,
		AgentID:   "main",
		Deliverer: &delivery.MemoryDeliverer{},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	sessionKey := session.BuildDirectSessionKey("main", "webchat", "webchat-user")
	seen := make([]string, 0, 2)
	stubBackend := backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		seen = append(seen, runCtx.ModelSelection.Provider+"/"+runCtx.ModelSelection.Model)
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "OK"}}}, nil
	})
	rt.Backends = nil
	rt.Backend = stubBackend

	run := func(message string) {
		if _, err := rt.Run(context.Background(), core.AgentRunRequest{
			AgentID:    "main",
			SessionKey: sessionKey,
			Channel:    "webchat",
			To:         "webchat-user",
			Message:    message,
		}); err != nil {
			t.Fatalf("run %q: %v", message, err)
		}
	}

	run("hello one")

	cfg.Agents.Defaults.Model.Primary = "openai/gpt-4.1-mini"
	cfg.Agents.List[0].Model.Primary = "openai/gpt-4.1-mini"
	if err := rt.ApplyConfig(cfg); err != nil {
		t.Fatalf("apply config: %v", err)
	}
	rt.Backends = nil
	rt.Backend = stubBackend

	run("hello two")

	if len(seen) != 2 {
		t.Fatalf("expected two runs, got %+v", seen)
	}
	if seen[0] != "openai/gpt-4.1" {
		t.Fatalf("expected first run to use old default, got %+v", seen)
	}
	if seen[1] != "openai/gpt-4.1-mini" {
		t.Fatalf("expected second run to use updated default without restart, got %+v", seen)
	}
	entry := rt.Sessions.Entry(sessionKey)
	if entry == nil {
		t.Fatal("expected session entry")
	}
	if entry.ProviderOverride != "" || entry.ModelOverride != "" {
		t.Fatalf("expected configured default not to be persisted as session override, got %+v", entry)
	}
}

func TestRunWithModelFallbackRetriesNextCandidate(t *testing.T) {
	selection := core.ModelSelection{
		Provider:   "openai",
		Model:      "gpt-4.1",
		ThinkLevel: "off",
		Fallbacks: []core.ModelCandidate{
			{Provider: "openai", Model: "gpt-4.1"},
			{Provider: "openai", Model: "gpt-4.1-mini"},
		},
	}
	// runCtx := rtypes.AgentRunContext{}
	calls := 0
	result, err := backend.RunWithModelFallback(context.Background(), selection, func(ctx context.Context, provider, model, thinkLevel string, isFallbackRetry bool) (core.AgentRunResult, error) {
		calls++
		if calls == 1 {
			return core.AgentRunResult{}, fmt.Errorf("boom")
		}
		if !isFallbackRetry {
			t.Fatal("expected second attempt to be marked fallback retry")
		}
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "done"}}}, nil
	})
	if err != nil {
		t.Fatalf("run with model fallback: %v", err)
	}
	if result.Model != "gpt-4.1-mini" {
		t.Fatalf("expected fallback model, got %+v", result)
	}
}
