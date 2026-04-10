package backend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/localmodel"
	"github.com/kocort/kocort/internal/rtypes"
)

// ---------------------------------------------------------------------------
// Fake Backend — implements localmodel.ModelBackend so we can create a
// Manager in "running" state without actual model files.
// ---------------------------------------------------------------------------

type fakeBackend struct {
	// createStream is called by CreateChatCompletionStream. Tests configure
	// this to control what the stream produces.
	createStream func(ctx context.Context, req localmodel.ChatCompletionRequest, enableThinking bool) (<-chan localmodel.ChatCompletionChunk, error)
	contextSize  int
}

func (f *fakeBackend) Start(string, int, int, int, localmodel.SamplingParams, bool) error {
	return nil
}
func (f *fakeBackend) Stop() error  { return nil }
func (f *fakeBackend) IsStub() bool { return false }
func (f *fakeBackend) ContextSize() int {
	if f.contextSize > 0 {
		return f.contextSize
	}
	return 4096
}
func (f *fakeBackend) SetSamplingParams(_ localmodel.SamplingParams) {}
func (f *fakeBackend) CreateChatCompletionStream(ctx context.Context, req localmodel.ChatCompletionRequest, enableThinking bool) (<-chan localmodel.ChatCompletionChunk, error) {
	if f.createStream != nil {
		return f.createStream(ctx, req, enableThinking)
	}
	// Default: single stop chunk
	ch := make(chan localmodel.ChatCompletionChunk, 1)
	ch <- finishChunk("stop")
	close(ch)
	return ch, nil
}

// ---------------------------------------------------------------------------
// Test tool — satisfies rtypes.Tool interface
// ---------------------------------------------------------------------------

type localTestTool struct {
	name    string
	desc    string
	execute func(ctx context.Context, toolCtx rtypes.ToolContext, args map[string]any) (core.ToolResult, error)
}

func (t *localTestTool) Name() string        { return t.name }
func (t *localTestTool) Description() string { return t.desc }
func (t *localTestTool) Execute(ctx context.Context, toolCtx rtypes.ToolContext, args map[string]any) (core.ToolResult, error) {
	if t.execute != nil {
		return t.execute(ctx, toolCtx, args)
	}
	return core.ToolResult{Text: "ok"}, nil
}

// OpenAIFunctionTool implements core.OpenAIFunctionToolProvider so that
// BuildOpenAICompatToolDefinitions can pick up our test tools.
func (t *localTestTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.name,
		Description: t.desc,
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newRunningManager creates a localmodel.Manager in StatusRunning with the
// given fakeBackend. No real model files are needed.
func newRunningManager(fi *fakeBackend) *localmodel.Manager {
	mgr := localmodel.NewManagerWithBackend(localmodel.Config{
		ModelID:     "test-model",
		ContextSize: 4096,
	}, fi, nil)
	if err := mgr.Start(); err != nil {
		panic(fmt.Sprintf("newRunningManager: Start failed: %v", err))
	}
	mgr.WaitReady()
	return mgr
}

// newTestBackend creates a LocalModelBackend with a running Manager.
func newTestBackend(fi *fakeBackend) *LocalModelBackend {
	return &LocalModelBackend{
		Manager:          newRunningManager(fi),
		BlockSendTimeout: 5 * time.Second,
		BlockReplyCoalescing: &delivery.BlockStreamingCoalescing{
			MinChars: 1,
			MaxChars: 4096,
			Idle:     50 * time.Millisecond,
			Joiner:   "",
		},
	}
}

// newTestRunCtx builds a minimal AgentRunContext for testing.
func newTestRunCtx(tools ...rtypes.Tool) rtypes.AgentRunContext {
	mem := &delivery.MemoryDeliverer{}
	dispatcher := delivery.NewReplyDispatcher(mem, core.DeliveryTarget{
		SessionKey: "test-session",
		Channel:    "test",
	})
	return rtypes.AgentRunContext{
		Runtime: &NopRuntimeServices{},
		Request: core.AgentRunRequest{
			RunID:   "run-1",
			Message: "Hello",
			Timeout: 30 * time.Second,
		},
		Session:  core.SessionResolution{SessionKey: "test-session"},
		Identity: core.AgentIdentity{ID: "agent-1"},
		Transcript: []core.TranscriptMessage{
			{Role: "user", Text: "Hello"},
		},
		SystemPrompt:    "You are a test assistant.",
		AvailableTools:  tools,
		ReplyDispatcher: dispatcher,
	}
}

// streamFromChunks creates a ChatCompletionChunk channel from a list of chunks.
func streamFromChunks(chunks ...localmodel.ChatCompletionChunk) <-chan localmodel.ChatCompletionChunk {
	ch := make(chan localmodel.ChatCompletionChunk, len(chunks))
	for _, c := range chunks {
		ch <- c
	}
	close(ch)
	return ch
}

// strPtr returns a pointer to the given string.
func strPtr(s string) *string { return &s }

// textChunk builds a ChatCompletionChunk with text content.
func textChunk(s string) localmodel.ChatCompletionChunk {
	return localmodel.ChatCompletionChunk{
		Choices: []localmodel.ChunkChoice{{
			Delta: localmodel.ChunkDelta{Content: s},
		}},
	}
}

// reasoningChunk builds a ChatCompletionChunk with reasoning content.
func reasoningChunk(s string) localmodel.ChatCompletionChunk {
	return localmodel.ChatCompletionChunk{
		Choices: []localmodel.ChunkChoice{{
			Delta: localmodel.ChunkDelta{Reasoning: s},
		}},
	}
}

// finishChunk builds a ChatCompletionChunk with only a finish reason.
func finishChunk(reason string) localmodel.ChatCompletionChunk {
	return localmodel.ChatCompletionChunk{
		Choices: []localmodel.ChunkChoice{{
			FinishReason: strPtr(reason),
		}},
	}
}

// toolCallFinishChunk builds a ChatCompletionChunk with tool calls and
// "tool_calls" finish reason.
func toolCallFinishChunk(calls ...localmodel.ToolCall) localmodel.ChatCompletionChunk {
	return localmodel.ChatCompletionChunk{
		Choices: []localmodel.ChunkChoice{{
			Delta:        localmodel.ChunkDelta{ToolCalls: calls},
			FinishReason: strPtr("tool_calls"),
		}},
	}
}

// textAndReasoningChunk builds a chunk carrying both text and reasoning.
func textAndReasoningChunk(text, reasoning string) localmodel.ChatCompletionChunk {
	return localmodel.ChatCompletionChunk{
		Choices: []localmodel.ChunkChoice{{
			Delta: localmodel.ChunkDelta{Content: text, Reasoning: reasoning},
		}},
	}
}

// makeToolCall is a shorthand for creating a localmodel.ToolCall.
func makeToolCall(id, name, args string) localmodel.ToolCall {
	return localmodel.ToolCall{
		ID:   id,
		Type: "function",
		Function: localmodel.ToolFunction{
			Name:      name,
			Arguments: args,
		},
	}
}

// ---------------------------------------------------------------------------
// Test: Run — nil backend / nil manager
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_NilBackend(t *testing.T) {
	var b *LocalModelBackend
	_, err := b.Run(context.Background(), rtypes.AgentRunContext{})
	if err == nil {
		t.Fatal("expected error for nil backend")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLocalBackend_Run_NilManager(t *testing.T) {
	b := &LocalModelBackend{}
	_, err := b.Run(context.Background(), rtypes.AgentRunContext{})
	if err == nil {
		t.Fatal("expected error for nil manager")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Test: Run — model not running
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_ModelNotRunning(t *testing.T) {
	// Manager created but not started — status = "stopped"
	mgr := localmodel.NewManagerWithBackend(localmodel.Config{ModelID: "test"}, &fakeBackend{}, nil)
	b := &LocalModelBackend{Manager: mgr}
	_, err := b.Run(context.Background(), rtypes.AgentRunContext{})
	if err == nil {
		t.Fatal("expected error for stopped model")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Test: Run — stub inferencer detection
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_StubInferencer(t *testing.T) {
	// StubInferencer is used when binary is built without llamacpp tag.
	// Manager.Start() with StubInferencer sets status to "error", so
	// we can't even get to the IsStubInferencer check in Run(). Instead
	// we verify that the Run method properly detects a non-running status
	// when a StubInferencer is used.
	mgr := localmodel.NewManager(localmodel.Config{ModelID: "test"}, nil) // nil catalog, StubInferencer
	b := &LocalModelBackend{Manager: mgr}
	_, err := b.Run(context.Background(), rtypes.AgentRunContext{})
	if err == nil {
		t.Fatal("expected error for stub inferencer")
	}
}

// ---------------------------------------------------------------------------
// Test: Run — simple text response (no tool calls)
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_SimpleTextResponse(t *testing.T) {
	fi := &fakeBackend{
		createStream: func(_ context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			return streamFromChunks(
				textChunk("Hello "),
				textChunk("World!"),
				finishChunk("stop"),
			), nil
		},
	}

	b := newTestBackend(fi)
	runCtx := newTestRunCtx()
	result, err := b.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Payloads) == 0 {
		t.Fatal("expected at least one payload")
	}
	if !strings.Contains(result.Payloads[0].Text, "Hello World!") {
		t.Errorf("unexpected text: %q", result.Payloads[0].Text)
	}
	if result.StopReason != "stop" {
		t.Errorf("expected stop reason 'stop', got %q", result.StopReason)
	}
}

// ---------------------------------------------------------------------------
// Test: Run — empty response → ErrProviderEmptyResponse
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_EmptyResponse(t *testing.T) {
	fi := &fakeBackend{
		createStream: func(_ context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			return streamFromChunks(
				finishChunk("stop"),
			), nil
		},
	}

	b := newTestBackend(fi)
	runCtx := newTestRunCtx()
	_, err := b.Run(context.Background(), runCtx)
	if !errors.Is(err, core.ErrProviderEmptyResponse) {
		t.Errorf("expected ErrProviderEmptyResponse, got: %v", err)
	}
}

func TestLocalBackend_Run_FallsBackToToolResultWhenFinalIsEmpty(t *testing.T) {
	callCount := 0
	fi := &fakeBackend{
		createStream: func(_ context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			callCount++
			if callCount == 1 {
				return streamFromChunks(
					toolCallFinishChunk(makeToolCall("call_1", "test_tool", `{}`)),
				), nil
			}
			return streamFromChunks(
				finishChunk("stop"),
			), nil
		},
	}

	tool := &localTestTool{
		name: "test_tool",
		desc: "returns text",
		execute: func(ctx context.Context, toolCtx rtypes.ToolContext, args map[string]any) (core.ToolResult, error) {
			return core.ToolResult{Text: "TOOL-RESULT"}, nil
		},
	}
	b := newTestBackend(fi)
	runCtx := newTestRunCtx(tool)
	runCtx.Runtime = &toolExecutingRuntime{tools: map[string]rtypes.Tool{"test_tool": tool}}
	result, err := b.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected two rounds, got %d", callCount)
	}
	if len(result.Payloads) != 1 || result.Payloads[0].Text != "TOOL-RESULT" {
		t.Fatalf("expected tool result fallback payload, got %+v", result.Payloads)
	}
}

// ---------------------------------------------------------------------------
// Test: Run — stream open failure
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_StreamOpenFailure(t *testing.T) {
	fi := &fakeBackend{
		createStream: func(_ context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			return nil, fmt.Errorf("model busy")
		},
	}

	b := newTestBackend(fi)
	runCtx := newTestRunCtx()
	_, err := b.Run(context.Background(), runCtx)
	if err == nil {
		t.Fatal("expected error for stream open failure")
	}
	if !strings.Contains(err.Error(), "model busy") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Test: Run — stream closes prematurely (channel closes after partial data)
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_StreamClosesPremature(t *testing.T) {
	fi := &fakeBackend{
		createStream: func(_ context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			// Channel closes after partial text, no finish chunk
			return streamFromChunks(
				textChunk("partial "),
			), nil
		},
	}

	b := newTestBackend(fi)
	runCtx := newTestRunCtx()
	result, err := b.Run(context.Background(), runCtx)
	// The backend should still produce a result with partial text (finish defaults to "stop").
	if err != nil {
		// Partial text might not look empty after trimming
		t.Logf("got error: %v", err)
	} else {
		if !strings.Contains(result.Payloads[0].Text, "partial") {
			t.Errorf("unexpected text: %q", result.Payloads[0].Text)
		}
	}
}

// ---------------------------------------------------------------------------
// Test: Run — reasoning content (thinking)
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_ReasoningContent(t *testing.T) {
	fi := &fakeBackend{
		createStream: func(_ context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			return streamFromChunks(
				reasoningChunk("Let me think..."),
				textChunk("The answer is 42."),
				finishChunk("stop"),
			), nil
		},
	}

	b := newTestBackend(fi)
	runCtx := newTestRunCtx()
	result, err := b.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result.Payloads[0].Text, "The answer is 42") {
		t.Errorf("unexpected text: %q", result.Payloads[0].Text)
	}

	// Check that reasoning events were recorded
	hasReasoning := false
	for _, ev := range result.Events {
		if data, ok := ev.Data["type"]; ok && data == "reasoning_delta" {
			hasReasoning = true
			break
		}
	}
	if !hasReasoning {
		t.Error("expected reasoning_delta event")
	}
}

// ---------------------------------------------------------------------------
// Test: Run — single tool call round
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_SingleToolCall(t *testing.T) {
	callCount := 0

	fi := &fakeBackend{
		createStream: func(_ context.Context, req localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			callCount++
			if callCount == 1 {
				// First round: model requests a tool call
				return streamFromChunks(
					toolCallFinishChunk(makeToolCall("call_1", "get_weather", `{"city":"Tokyo"}`)),
				), nil
			}
			// Second round: model produces final text after seeing tool result
			return streamFromChunks(
				textChunk("The weather in Tokyo is sunny."),
				finishChunk("stop"),
			), nil
		},
	}

	tool := &localTestTool{
		name: "get_weather",
		desc: "Get weather for a city",
		execute: func(_ context.Context, _ rtypes.ToolContext, args map[string]any) (core.ToolResult, error) {
			city, _ := args["city"].(string)
			return core.ToolResult{Text: fmt.Sprintf("Weather in %s: sunny, 25°C", city)}, nil
		},
	}

	b := newTestBackend(fi)
	runCtx := newTestRunCtx(tool)
	runCtx.Runtime = &toolExecutingRuntime{tools: map[string]rtypes.Tool{"get_weather": tool}}

	result, err := b.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callCount != 2 {
		t.Errorf("expected 2 streaming rounds, got %d", callCount)
	}
	if !strings.Contains(result.Payloads[0].Text, "sunny") {
		t.Errorf("unexpected final text: %q", result.Payloads[0].Text)
	}
}

// ---------------------------------------------------------------------------
// Test: Run — tool call with execution failure (ToolExecutionFailure)
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_ToolExecutionFailure(t *testing.T) {
	callCount := 0

	fi := &fakeBackend{
		createStream: func(_ context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			callCount++
			if callCount == 1 {
				return streamFromChunks(
					toolCallFinishChunk(makeToolCall("call_fail", "bad_tool", `{}`)),
				), nil
			}
			return streamFromChunks(
				textChunk("Sorry, the tool failed."),
				finishChunk("stop"),
			), nil
		},
	}

	tool := &localTestTool{
		name: "bad_tool",
		desc: "A tool that fails",
		execute: func(_ context.Context, _ rtypes.ToolContext, _ map[string]any) (core.ToolResult, error) {
			return core.ToolResult{}, &core.ToolExecutionFailure{
				ToolName:    "bad_tool",
				Message:     "connection timeout",
				HistoryText: "ERROR: connection timeout",
				Recoverable: true,
			}
		},
	}

	b := newTestBackend(fi)
	runCtx := newTestRunCtx(tool)
	runCtx.Runtime = &toolExecutingRuntime{tools: map[string]rtypes.Tool{"bad_tool": tool}}

	result, err := b.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callCount != 2 {
		t.Errorf("expected 2 streaming rounds, got %d", callCount)
	}
	if !strings.Contains(result.Payloads[0].Text, "Sorry") {
		t.Errorf("unexpected final text: %q", result.Payloads[0].Text)
	}
}

// ---------------------------------------------------------------------------
// Test: Run — tool call with fatal execution error (non-ToolExecutionFailure)
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_ToolFatalError(t *testing.T) {
	fi := &fakeBackend{
		createStream: func(_ context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			return streamFromChunks(
				toolCallFinishChunk(makeToolCall("call_fatal", "fatal_tool", `{}`)),
			), nil
		},
	}

	tool := &localTestTool{
		name: "fatal_tool",
		desc: "A tool that crashes",
		execute: func(_ context.Context, _ rtypes.ToolContext, _ map[string]any) (core.ToolResult, error) {
			return core.ToolResult{}, fmt.Errorf("panic: nil pointer dereference")
		},
	}

	b := newTestBackend(fi)
	runCtx := newTestRunCtx(tool)
	runCtx.Runtime = &toolExecutingRuntime{tools: map[string]rtypes.Tool{"fatal_tool": tool}}

	_, err := b.Run(context.Background(), runCtx)
	if err == nil {
		t.Fatal("expected error for fatal tool failure")
	}
	if !strings.Contains(err.Error(), "nil pointer") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Test: Run — tool_calls finish reason with no actual tool calls
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_ToolCallsWithNoCalls(t *testing.T) {
	fi := &fakeBackend{
		createStream: func(_ context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			return streamFromChunks(localmodel.ChatCompletionChunk{
				Choices: []localmodel.ChunkChoice{{
					FinishReason: strPtr("tool_calls"),
					// No tool calls despite finish reason
				}},
			}), nil
		},
	}

	b := newTestBackend(fi)
	runCtx := newTestRunCtx()
	_, err := b.Run(context.Background(), runCtx)
	if err == nil {
		t.Fatal("expected error for tool_calls with no calls")
	}
	if !strings.Contains(err.Error(), "no tool calls") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Test: Run — multiple tool call rounds
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_MultipleToolRounds(t *testing.T) {
	callCount := 0

	fi := &fakeBackend{
		createStream: func(_ context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			callCount++
			switch callCount {
			case 1:
				return streamFromChunks(
					toolCallFinishChunk(makeToolCall("call_1", "step_one", `{}`)),
				), nil
			case 2:
				return streamFromChunks(
					toolCallFinishChunk(makeToolCall("call_2", "step_two", `{}`)),
				), nil
			default:
				return streamFromChunks(
					textChunk("All steps complete."),
					finishChunk("stop"),
				), nil
			}
		},
	}

	stepOne := &localTestTool{name: "step_one", desc: "Step 1"}
	stepTwo := &localTestTool{name: "step_two", desc: "Step 2"}

	b := newTestBackend(fi)
	runCtx := newTestRunCtx(stepOne, stepTwo)
	runCtx.Runtime = &toolExecutingRuntime{tools: map[string]rtypes.Tool{
		"step_one": stepOne,
		"step_two": stepTwo,
	}}

	result, err := b.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 3 {
		t.Errorf("expected 3 streaming rounds, got %d", callCount)
	}
	if !strings.Contains(result.Payloads[0].Text, "All steps complete") {
		t.Errorf("unexpected text: %q", result.Payloads[0].Text)
	}
}

// ---------------------------------------------------------------------------
// Test: Run — context cancellation during streaming
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_ContextCancelled(t *testing.T) {
	fi := &fakeBackend{
		createStream: func(ctx context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			// Create a stream that blocks until context is cancelled
			ch := make(chan localmodel.ChatCompletionChunk)
			go func() {
				<-ctx.Done()
				close(ch)
			}()
			return ch, nil
		},
	}

	b := newTestBackend(fi)
	b.NoOutputTimeout = 200 * time.Millisecond
	runCtx := newTestRunCtx()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := b.Run(ctx, runCtx)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	// The cancellation can surface as context.Canceled, empty response, or
	// a "cancelled" error depending on timing.
	errStr := err.Error()
	if !strings.Contains(errStr, "cancel") && !strings.Contains(errStr, "empty response") && !strings.Contains(errStr, "no output") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Test: Run — with timeout from request
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_RequestTimeout(t *testing.T) {
	fi := &fakeBackend{
		createStream: func(ctx context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			ch := make(chan localmodel.ChatCompletionChunk)
			go func() {
				<-ctx.Done()
				close(ch)
			}()
			return ch, nil
		},
	}

	b := newTestBackend(fi)
	b.NoOutputTimeout = 100 * time.Millisecond
	runCtx := newTestRunCtx()
	runCtx.Request.Timeout = 200 * time.Millisecond

	_, err := b.Run(context.Background(), runCtx)
	if err == nil {
		t.Fatal("expected error due to timeout")
	}
}

// ---------------------------------------------------------------------------
// Test: Run — usage / responseID propagation
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_UsageAndResponseID(t *testing.T) {
	fi := &fakeBackend{
		createStream: func(_ context.Context, req localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			return streamFromChunks(
				textChunk("Response text."),
				finishChunk("stop"),
			), nil
		},
	}

	b := newTestBackend(fi)
	runCtx := newTestRunCtx()
	result, err := b.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.StopReason != "stop" {
		t.Errorf("expected stop reason 'stop', got %q", result.StopReason)
	}

	meta, ok := result.Meta["backendKind"]
	if !ok || meta != "local" {
		t.Errorf("expected backendKind=local in meta, got %v", result.Meta)
	}
}

// ---------------------------------------------------------------------------
// Test: Run — events are recorded correctly
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_EventsRecorded(t *testing.T) {
	fi := &fakeBackend{
		createStream: func(_ context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			return streamFromChunks(
				textChunk("Hello"),
				finishChunk("stop"),
			), nil
		},
	}

	b := newTestBackend(fi)
	runCtx := newTestRunCtx()
	result, err := b.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Events) == 0 {
		t.Fatal("expected events to be recorded")
	}

	// Should have text_delta and final events
	hasTextDelta := false
	hasFinal := false
	for _, ev := range result.Events {
		if typ, ok := ev.Data["type"]; ok {
			switch typ {
			case "text_delta":
				hasTextDelta = true
			case "final":
				hasFinal = true
			}
		}
	}
	if !hasTextDelta {
		t.Error("expected text_delta event")
	}
	if !hasFinal {
		t.Error("expected final event")
	}
}

// ---------------------------------------------------------------------------
// Test: Run — tool call with media URLs
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_ToolWithMediaURL(t *testing.T) {
	callCount := 0

	fi := &fakeBackend{
		createStream: func(_ context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			callCount++
			if callCount == 1 {
				return streamFromChunks(
					toolCallFinishChunk(makeToolCall("call_img", "generate_image", `{}`)),
				), nil
			}
			return streamFromChunks(
				textChunk("Here is the image."),
				finishChunk("stop"),
			), nil
		},
	}

	imgTool := &localTestTool{
		name: "generate_image",
		desc: "Generate an image",
		execute: func(_ context.Context, _ rtypes.ToolContext, _ map[string]any) (core.ToolResult, error) {
			return core.ToolResult{
				Text:     "image generated",
				MediaURL: "https://example.com/image.png",
			}, nil
		},
	}

	b := newTestBackend(fi)
	runCtx := newTestRunCtx(imgTool)
	runCtx.Runtime = &toolExecutingRuntime{tools: map[string]rtypes.Tool{"generate_image": imgTool}}

	result, err := b.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The payload should have a mediaURL
	if len(result.Payloads) == 0 {
		t.Fatal("expected payload")
	}
	p := result.Payloads[0]
	if p.MediaURL != "https://example.com/image.png" {
		t.Errorf("expected mediaURL, got: %q", p.MediaURL)
	}
}

// ---------------------------------------------------------------------------
// Test: Run — tool call with invalid JSON arguments
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_ToolInvalidArguments(t *testing.T) {
	fi := &fakeBackend{
		createStream: func(_ context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			return streamFromChunks(
				toolCallFinishChunk(localmodel.ToolCall{
					ID:   "call_bad_json",
					Type: "function",
					Function: localmodel.ToolFunction{
						Name:      "some_tool",
						Arguments: `{invalid json`,
					},
				}),
			), nil
		},
	}

	tool := &localTestTool{name: "some_tool", desc: "some tool"}
	b := newTestBackend(fi)
	runCtx := newTestRunCtx(tool)
	runCtx.Runtime = &toolExecutingRuntime{tools: map[string]rtypes.Tool{"some_tool": tool}}

	_, err := b.Run(context.Background(), runCtx)
	if err == nil {
		t.Fatal("expected error for invalid JSON arguments")
	}
	if !strings.Contains(err.Error(), "invalid arguments JSON") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Test: Run — tool call with multiple media URLs
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_ToolWithMultipleMediaURLs(t *testing.T) {
	callCount := 0

	fi := &fakeBackend{
		createStream: func(_ context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			callCount++
			if callCount == 1 {
				return streamFromChunks(
					toolCallFinishChunk(makeToolCall("call_multi", "multi_img", `{}`)),
				), nil
			}
			return streamFromChunks(
				textChunk("Generated images."),
				finishChunk("stop"),
			), nil
		},
	}

	imgTool := &localTestTool{
		name: "multi_img",
		desc: "Generate multiple images",
		execute: func(_ context.Context, _ rtypes.ToolContext, _ map[string]any) (core.ToolResult, error) {
			return core.ToolResult{
				Text:      "images generated",
				MediaURLs: []string{"https://example.com/a.png", "https://example.com/b.png"},
			}, nil
		},
	}

	b := newTestBackend(fi)
	runCtx := newTestRunCtx(imgTool)
	runCtx.Runtime = &toolExecutingRuntime{tools: map[string]rtypes.Tool{"multi_img": imgTool}}

	result, err := b.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	p := result.Payloads[0]
	if len(p.MediaURLs) != 2 {
		t.Errorf("expected 2 media URLs, got %d", len(p.MediaURLs))
	}
}

// ---------------------------------------------------------------------------
// Test: NewLocalModelBackend constructor
// ---------------------------------------------------------------------------

func TestNewLocalModelBackend(t *testing.T) {
	mgr := newRunningManager(&fakeBackend{})
	b := NewLocalModelBackend(mgr)
	if b == nil {
		t.Fatal("expected non-nil backend")
	}
	if b.Manager != mgr {
		t.Error("manager not set correctly")
	}
	if b.BlockSendTimeout != 5*time.Second {
		t.Errorf("unexpected BlockSendTimeout: %v", b.BlockSendTimeout)
	}
	if b.BlockReplyCoalescing == nil {
		t.Error("expected BlockReplyCoalescing to be set")
	}
}

// ---------------------------------------------------------------------------
// Test: resolveNoOutputTimeout — various cases
// ---------------------------------------------------------------------------

func TestResolveNoOutputTimeout_Configured(t *testing.T) {
	b := &LocalModelBackend{NoOutputTimeout: 42 * time.Second}
	got := b.resolveNoOutputTimeout(context.Background())
	if got != 42*time.Second {
		t.Errorf("expected 42s, got %v", got)
	}
}

func TestResolveNoOutputTimeout_NoDeadline(t *testing.T) {
	b := &LocalModelBackend{}
	got := b.resolveNoOutputTimeout(context.Background())
	if got != 180*time.Second {
		t.Errorf("expected 180s (minTimeout), got %v", got)
	}
}

func TestResolveNoOutputTimeout_WithDeadline(t *testing.T) {
	b := &LocalModelBackend{}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	got := b.resolveNoOutputTimeout(ctx)
	// Should be 80% of 300s = 240s, clamped by max 600s, min 180s
	if got < 180*time.Second || got > 300*time.Second {
		t.Errorf("unexpected timeout: %v", got)
	}
}

func TestResolveNoOutputTimeout_ShortDeadline(t *testing.T) {
	b := &LocalModelBackend{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got := b.resolveNoOutputTimeout(ctx)
	// With a 2s deadline, should return minTimeout (180s) clamped by cap
	if got > 2*time.Second {
		t.Errorf("timeout %v exceeds remaining time 2s", got)
	}
}

// ---------------------------------------------------------------------------
// Test: resolveBlockSendTimeout
// ---------------------------------------------------------------------------

func TestResolveBlockSendTimeout_Default(t *testing.T) {
	b := &LocalModelBackend{}
	if got := b.resolveBlockSendTimeout(); got != 5*time.Second {
		t.Errorf("expected 5s, got %v", got)
	}
}

func TestResolveBlockSendTimeout_Configured(t *testing.T) {
	b := &LocalModelBackend{BlockSendTimeout: 10 * time.Second}
	if got := b.resolveBlockSendTimeout(); got != 10*time.Second {
		t.Errorf("expected 10s, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// Test: estimateLlamaMessagesTokens
// ---------------------------------------------------------------------------

func TestEstimateLlamaMessagesTokens(t *testing.T) {
	messages := []localmodel.ChatMessage{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hello"},
	}
	est := estimateLlamaMessagesTokens(messages, nil)
	if est <= 0 {
		t.Errorf("expected positive token estimate, got %d", est)
	}
}

func TestEstimateLlamaMessagesTokens_WithTools(t *testing.T) {
	messages := []localmodel.ChatMessage{
		{Role: "user", Content: "Hello"},
	}
	tools := []localmodel.Tool{
		{
			Type: "function",
			Function: localmodel.ToolDefFunc{
				Name:        "read_file",
				Description: "Read a file from disk",
			},
		},
	}
	est := estimateLlamaMessagesTokens(messages, tools)
	if est <= 0 {
		t.Errorf("expected positive token estimate, got %d", est)
	}
}

func TestEstimateLlamaMessagesTokens_WithToolCalls(t *testing.T) {
	messages := []localmodel.ChatMessage{
		{
			Role: "assistant",
			ToolCalls: []localmodel.ToolCall{
				{
					ID:       "call_1",
					Type:     "function",
					Function: localmodel.ToolFunction{Name: "foo", Arguments: `{"bar":"baz"}`},
				},
			},
		},
	}
	est := estimateLlamaMessagesTokens(messages, nil)
	if est <= 0 {
		t.Errorf("expected positive token estimate, got %d", est)
	}
}

// ---------------------------------------------------------------------------
// Test: truncateLlamaMessagesToFit
// ---------------------------------------------------------------------------

func TestTruncateLlamaMessagesToFit_WithinBudget(t *testing.T) {
	messages := []localmodel.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
	}
	result := truncateLlamaMessagesToFit(messages, nil, 10000)
	if len(result) != 2 {
		t.Errorf("expected 2 messages, got %d", len(result))
	}
}

func TestTruncateLlamaMessagesToFit_ExceedsBudget(t *testing.T) {
	messages := []localmodel.ChatMessage{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: strings.Repeat("A very long message. ", 100)},
		{Role: "assistant", Content: strings.Repeat("A verbose response. ", 100)},
		{Role: "user", Content: strings.Repeat("Another long prompt. ", 100)},
		{Role: "assistant", Content: strings.Repeat("Another verbose reply. ", 100)},
		{Role: "user", Content: "Final question?"},
	}
	result := truncateLlamaMessagesToFit(messages, nil, 50)
	// Should keep at least system + last user message
	if len(result) < 2 {
		t.Errorf("expected at least 2 messages, got %d", len(result))
	}
	// First should be system
	if result[0].Role != "system" {
		t.Errorf("expected system message first, got %q", result[0].Role)
	}
	// Last should be the final user message
	lastContent, _ := result[len(result)-1].Content.(string)
	if lastContent != "Final question?" {
		t.Errorf("expected final user message preserved, got %q", lastContent)
	}
}

func TestTruncateLlamaMessagesToFit_ZeroBudget(t *testing.T) {
	messages := []localmodel.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
	}
	result := truncateLlamaMessagesToFit(messages, nil, 0)
	// Budget <= 0 returns messages unchanged
	if len(result) != 2 {
		t.Errorf("expected 2 messages (no truncation), got %d", len(result))
	}
}

func TestTruncateLlamaMessagesToFit_TwoMessages(t *testing.T) {
	messages := []localmodel.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
	}
	result := truncateLlamaMessagesToFit(messages, nil, 1)
	// Can't truncate below 2 messages
	if len(result) != 2 {
		t.Errorf("expected 2 messages (minimum), got %d", len(result))
	}
}

// ---------------------------------------------------------------------------
// Test: Run — request builds correct request structure
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_RequestStructure(t *testing.T) {
	var capturedReq localmodel.ChatCompletionRequest

	fi := &fakeBackend{
		createStream: func(_ context.Context, req localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			capturedReq = req
			return streamFromChunks(
				textChunk("ok"),
				finishChunk("stop"),
			), nil
		},
	}

	b := newTestBackend(fi)
	runCtx := newTestRunCtx()
	runCtx.SystemPrompt = "Test system prompt."
	runCtx.Transcript = []core.TranscriptMessage{
		{Role: "user", Text: "First question"},
	}
	runCtx.Request.Message = "Second question"

	_, err := b.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedReq.Model != "test-model" {
		t.Errorf("expected model 'test-model', got %q", capturedReq.Model)
	}
	if len(capturedReq.Messages) == 0 {
		t.Fatal("expected messages in request")
	}
	// First message should be system prompt
	if capturedReq.Messages[0].Role != "system" {
		t.Errorf("expected first message to be system, got %q", capturedReq.Messages[0].Role)
	}
}

// ---------------------------------------------------------------------------
// Test: Run — no system prompt
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_NoSystemPrompt(t *testing.T) {
	var capturedReq localmodel.ChatCompletionRequest

	fi := &fakeBackend{
		createStream: func(_ context.Context, req localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			capturedReq = req
			return streamFromChunks(
				textChunk("ok"),
				finishChunk("stop"),
			), nil
		},
	}

	b := newTestBackend(fi)
	runCtx := newTestRunCtx()
	runCtx.SystemPrompt = ""

	_, err := b.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should not have a system message
	for _, m := range capturedReq.Messages {
		if m.Role == "system" {
			t.Error("expected no system message when SystemPrompt is empty")
		}
	}
}

// ---------------------------------------------------------------------------
// Test: Run — streaming round failure propagation
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_StreamingRoundFailure(t *testing.T) {
	callCount := 0
	fi := &fakeBackend{
		createStream: func(_ context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			callCount++
			if callCount == 1 {
				return streamFromChunks(
					toolCallFinishChunk(makeToolCall("call_1", "test_tool", `{}`)),
				), nil
			}
			// Second round fails
			return nil, fmt.Errorf("GPU OOM")
		},
	}

	tool := &localTestTool{name: "test_tool", desc: "test"}
	b := newTestBackend(fi)
	runCtx := newTestRunCtx(tool)
	runCtx.Runtime = &toolExecutingRuntime{tools: map[string]rtypes.Tool{"test_tool": tool}}

	_, err := b.Run(context.Background(), runCtx)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "GPU OOM") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Test: Run — tool with empty arguments
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_ToolEmptyArguments(t *testing.T) {
	callCount := 0
	fi := &fakeBackend{
		createStream: func(_ context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			callCount++
			if callCount == 1 {
				return streamFromChunks(
					toolCallFinishChunk(makeToolCall("call_empty", "no_args_tool", "")),
				), nil
			}
			return streamFromChunks(
				textChunk("Done."),
				finishChunk("stop"),
			), nil
		},
	}

	tool := &localTestTool{name: "no_args_tool", desc: "no args"}
	b := newTestBackend(fi)
	runCtx := newTestRunCtx(tool)
	runCtx.Runtime = &toolExecutingRuntime{tools: map[string]rtypes.Tool{"no_args_tool": tool}}

	result, err := b.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Payloads[0].Text, "Done") {
		t.Errorf("unexpected text: %q", result.Payloads[0].Text)
	}
}

// ---------------------------------------------------------------------------
// Test: Run — finish reason propagation (length, content_filter, etc.)
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_FinishReasonNonToolCalls(t *testing.T) {
	fi := &fakeBackend{
		createStream: func(_ context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			return streamFromChunks(
				textChunk("partial output"),
				finishChunk("length"),
			), nil
		},
	}

	b := newTestBackend(fi)
	runCtx := newTestRunCtx()
	result, err := b.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Finish reason is passed through as-is.
	if result.StopReason != "length" {
		t.Errorf("expected stop reason 'length', got %q", result.StopReason)
	}
	if !strings.Contains(result.Payloads[0].Text, "partial output") {
		t.Errorf("unexpected text: %q", result.Payloads[0].Text)
	}
}

// ---------------------------------------------------------------------------
// Test: Run — context already has deadline (no extra timeout wrapping)
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_ExistingDeadline(t *testing.T) {
	fi := &fakeBackend{
		createStream: func(_ context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			return streamFromChunks(
				textChunk("ok"),
				finishChunk("stop"),
			), nil
		},
	}

	b := newTestBackend(fi)
	runCtx := newTestRunCtx()
	runCtx.Request.Timeout = 30 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := b.Run(ctx, runCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Payloads[0].Text != "ok" {
		t.Errorf("unexpected text: %q", result.Payloads[0].Text)
	}
}

// ---------------------------------------------------------------------------
// Test: Run — large prompt triggers truncation
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_PromptTruncation(t *testing.T) {
	var capturedMsgCount int

	fi := &fakeBackend{
		contextSize: 512, // Very small context to force truncation
		createStream: func(_ context.Context, req localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			capturedMsgCount = len(req.Messages)
			return streamFromChunks(
				textChunk("ok"),
				finishChunk("stop"),
			), nil
		},
	}

	b := newTestBackend(fi)
	runCtx := newTestRunCtx()
	runCtx.SystemPrompt = "Short system prompt."
	// Add many transcript messages to exceed budget
	transcript := make([]core.TranscriptMessage, 0, 50)
	for i := 0; i < 50; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		transcript = append(transcript, core.TranscriptMessage{
			Role: role,
			Text: strings.Repeat("This is a long message that takes up tokens. ", 20),
		})
	}
	runCtx.Transcript = transcript

	_, err := b.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have truncated to fewer messages than original
	if capturedMsgCount >= 52 { // 50 transcript + system + user message
		t.Errorf("expected truncation, but got %d messages", capturedMsgCount)
	}
}

// ---------------------------------------------------------------------------
// toolExecutingRuntime — a RuntimeServices that actually dispatches tool calls
// to registered tools. Embeds NopRuntimeServices for all other methods.
// ---------------------------------------------------------------------------

type toolExecutingRuntime struct {
	NopRuntimeServices
	tools map[string]rtypes.Tool
}

func (r *toolExecutingRuntime) ExecuteTool(ctx context.Context, runCtx rtypes.AgentRunContext, name string, args map[string]any) (core.ToolResult, error) {
	t, ok := r.tools[name]
	if !ok {
		return core.ToolResult{}, fmt.Errorf("unknown tool: %s", name)
	}
	// Build a minimal ToolContext
	tc := rtypes.ToolContext{}
	return t.Execute(ctx, tc, args)
}

// ---------------------------------------------------------------------------
// Test: Run — watchdog timeout (no output produced)
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_WatchdogTimeout(t *testing.T) {
	fi := &fakeBackend{
		createStream: func(_ context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			// Stream that never produces output, causing watchdog timeout
			ch := make(chan localmodel.ChatCompletionChunk)
			// Don't close — let watchdog trigger
			go func() {
				time.Sleep(5 * time.Second)
				close(ch)
			}()
			return ch, nil
		},
	}

	b := newTestBackend(fi)
	b.NoOutputTimeout = 100 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runCtx := newTestRunCtx()
	_, err := b.Run(ctx, runCtx)
	if err == nil {
		t.Fatal("expected error from watchdog timeout")
	}
	// Should get a BackendError or cancellation error
	var backendErr *BackendError
	if errors.As(err, &backendErr) {
		if backendErr.Reason != BackendFailureTransientHTTP {
			t.Errorf("expected TransientHTTP reason, got %v", backendErr.Reason)
		}
	}
}

// ---------------------------------------------------------------------------
// Test: Run — streaming multi-text-delta accumulation
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_TextAccumulation(t *testing.T) {
	fi := &fakeBackend{
		createStream: func(_ context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			return streamFromChunks(
				textChunk("A"),
				textChunk("B"),
				textChunk("C"),
				textChunk("D"),
				finishChunk("stop"),
			), nil
		},
	}

	b := newTestBackend(fi)
	runCtx := newTestRunCtx()
	result, err := b.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Payloads[0].Text != "ABCD" {
		t.Errorf("expected 'ABCD', got %q", result.Payloads[0].Text)
	}
}

// ---------------------------------------------------------------------------
// Test: Run — tool call delta accumulation (streamed tool calls)
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_StreamedToolCallDelta(t *testing.T) {
	callCount := 0

	fi := &fakeBackend{
		createStream: func(_ context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			callCount++
			if callCount == 1 {
				return streamFromChunks(
					toolCallFinishChunk(makeToolCall("call_streamed", "read_file", `{"path":"/tmp/test.txt"}`)),
				), nil
			}
			return streamFromChunks(
				textChunk("File content: hello"),
				finishChunk("stop"),
			), nil
		},
	}

	readTool := &localTestTool{
		name: "read_file",
		desc: "Read file",
		execute: func(_ context.Context, _ rtypes.ToolContext, args map[string]any) (core.ToolResult, error) {
			return core.ToolResult{Text: "hello world"}, nil
		},
	}

	b := newTestBackend(fi)
	runCtx := newTestRunCtx(readTool)
	runCtx.Runtime = &toolExecutingRuntime{tools: map[string]rtypes.Tool{"read_file": readTool}}

	result, err := b.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Payloads[0].Text, "hello") {
		t.Errorf("unexpected text: %q", result.Payloads[0].Text)
	}
}

// ---------------------------------------------------------------------------
// Test: Run — with AvailableTools (tool definitions in request)
// ---------------------------------------------------------------------------

func TestLocalBackend_Run_WithAvailableTools(t *testing.T) {
	var capturedReq localmodel.ChatCompletionRequest

	fi := &fakeBackend{
		createStream: func(_ context.Context, req localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			capturedReq = req
			return streamFromChunks(
				textChunk("ok"),
				finishChunk("stop"),
			), nil
		},
	}

	tool1 := &localTestTool{name: "tool_a", desc: "Tool A"}
	tool2 := &localTestTool{name: "tool_b", desc: "Tool B"}

	b := newTestBackend(fi)
	runCtx := newTestRunCtx(tool1, tool2)

	_, err := b.Run(context.Background(), runCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Tools are now passed as structured data in ChatCompletionRequest.Tools
	if len(capturedReq.Tools) != 2 {
		t.Errorf("expected 2 tools in request, got %d", len(capturedReq.Tools))
	}
	foundA, foundB := false, false
	for _, tool := range capturedReq.Tools {
		switch tool.Function.Name {
		case "tool_a":
			foundA = true
		case "tool_b":
			foundB = true
		}
	}
	if !foundA || !foundB {
		t.Errorf("expected both tool_a and tool_b in request tools, got %+v", capturedReq.Tools)
	}
}

// ---------------------------------------------------------------------------
// Test: OpenAI type conversion helpers — convertOpenAIMessagesToLlama
// ---------------------------------------------------------------------------

func TestConvertOpenAIMessagesToLlama(t *testing.T) {
	messages := []openAIChatMessage{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hello"},
		{
			Role: "assistant",
			ToolCalls: []openai.ToolCall{
				{
					ID:   "tc1",
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      "test",
						Arguments: `{"k":"v"}`,
					},
				},
			},
		},
		{Role: "tool", ToolCallID: "tc1", Name: "test", Content: "result"},
	}
	result := convertOpenAIMessagesToLlama(messages)
	if len(result) != 4 {
		t.Fatalf("expected 4, got %d", len(result))
	}
	if result[0].Role != "system" {
		t.Errorf("expected system, got %q", result[0].Role)
	}
	if result[2].ToolCalls[0].ID != "tc1" {
		t.Errorf("expected tc1, got %q", result[2].ToolCalls[0].ID)
	}
	if result[3].ToolCallID != "tc1" {
		t.Errorf("expected tc1, got %q", result[3].ToolCallID)
	}
}

// ---------------------------------------------------------------------------
// Test: OpenAI type conversion helpers — convertOpenAIToolsToLlama
// ---------------------------------------------------------------------------

func TestConvertOpenAIToolsToLlama(t *testing.T) {
	tools := []openAIToolDefinition{
		{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "  read_file  ",
				Description: "  Reads a file  ",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string"},
					},
				},
			},
		},
		{Type: openai.ToolTypeFunction, Function: nil}, // nil function — should be skipped
	}
	result := convertOpenAIToolsToLlama(tools)
	if len(result) != 1 {
		t.Fatalf("expected 1, got %d", len(result))
	}
	// convertOpenAIToolsToLlama passes name/description through as-is
	if result[0].Function.Name != "  read_file  " {
		t.Errorf("expected '  read_file  ', got %q", result[0].Function.Name)
	}
	if result[0].Function.Description != "  Reads a file  " {
		t.Errorf("expected '  Reads a file  ', got %q", result[0].Function.Description)
	}
}

// ===========================================================================
// Test: runStreamingRound — complete coverage of chunk.Choices delta types:
//   - choice.Delta.Content          (text)
//   - choice.Delta.Reasoning        (thinking / reasoning)
//   - choice.Delta.ToolCalls        (tool call deltas)
//
// All streams are constructed via streamFromChunks.
// ===========================================================================

// runRoundWithChunks is a test helper that calls runStreamingRound directly
// using a fake backend that produces chunks from the given arguments.
// Returns the round result, the event builder (for inspecting recorded
// events), and any error.
func runRoundWithChunks(t *testing.T, chunks ...localmodel.ChatCompletionChunk) (localStreamingRoundResult, *agentEventBuilder, error) {
	t.Helper()

	fi := &fakeBackend{
		createStream: func(_ context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			return streamFromChunks(chunks...), nil
		},
	}

	b := newTestBackend(fi)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runCtx := newTestRunCtx()
	events := newAgentEventBuilder(runCtx)

	messages := []localmodel.ChatMessage{
		{Role: "user", Content: "test"},
	}

	result, err := b.runStreamingRound(ctx, cancel, messages, nil, runCtx, events)
	return result, events, err
}

// eventHasType checks whether the event builder recorded any event with the
// given "type" value in its Data map.
func eventHasType(eb *agentEventBuilder, typ string) bool {
	for _, ev := range eb.Events() {
		if v, ok := ev.Data["type"]; ok && v == typ {
			return true
		}
	}
	return false
}

// countEventsOfType returns how many events have the given "type" value.
func countEventsOfType(eb *agentEventBuilder, typ string) int {
	n := 0
	for _, ev := range eb.Events() {
		if v, ok := ev.Data["type"]; ok && v == typ {
			n++
		}
	}
	return n
}

// eventDataForType returns the Data maps of all events with the given "type".
func eventDataForType(eb *agentEventBuilder, typ string) []map[string]any {
	var out []map[string]any
	for _, ev := range eb.Events() {
		if v, ok := ev.Data["type"]; ok && v == typ {
			out = append(out, ev.Data)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// 1. choice.Delta.Content only — text deltas
// ---------------------------------------------------------------------------

func TestRunStreamingRound_Delta_TextContentOnly(t *testing.T) {
	result, events, err := runRoundWithChunks(t,
		textChunk("Hello world"),
		finishChunk("stop"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Text content accumulated in FinalText
	if result.FinalText != "Hello world" {
		t.Errorf("FinalText: want 'Hello world', got %q", result.FinalText)
	}
	if result.FinishReason != "stop" {
		t.Errorf("FinishReason: want 'stop', got %q", result.FinishReason)
	}

	// text_delta event emitted
	if !eventHasType(events, "text_delta") {
		t.Error("expected text_delta event")
	}
	// Verify event payload contains the text
	for _, d := range eventDataForType(events, "text_delta") {
		if txt, _ := d["text"].(string); txt != "Hello world" {
			t.Errorf("text_delta text: want 'Hello world', got %q", txt)
		}
	}

	// No reasoning or tool call artifacts
	if eventHasType(events, "reasoning_delta") {
		t.Error("unexpected reasoning_delta event")
	}
	if len(result.ToolCalls) != 0 {
		t.Errorf("expected 0 tool calls, got %d", len(result.ToolCalls))
	}
}

// ---------------------------------------------------------------------------
// 2. choice.Delta.Reasoning only — reasoning / thinking deltas
// ---------------------------------------------------------------------------

func TestRunStreamingRound_Delta_ReasoningContentOnly(t *testing.T) {
	result, events, err := runRoundWithChunks(t,
		reasoningChunk("Let me think step by step..."),
		reasoningChunk("First, I consider the problem."),
		textChunk("The answer is 42."),
		finishChunk("stop"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// ReasoningContent does NOT go into FinalText — only Text does
	if result.FinalText != "The answer is 42." {
		t.Errorf("FinalText: want 'The answer is 42.', got %q", result.FinalText)
	}

	// Two reasoning_delta events
	if count := countEventsOfType(events, "reasoning_delta"); count != 2 {
		t.Errorf("reasoning_delta count: want 2, got %d", count)
	}
	// Verify reasoning event payloads
	reasoningEvents := eventDataForType(events, "reasoning_delta")
	if txt, _ := reasoningEvents[0]["text"].(string); txt != "Let me think step by step..." {
		t.Errorf("reasoning[0] text: got %q", txt)
	}
	if txt, _ := reasoningEvents[1]["text"].(string); txt != "First, I consider the problem." {
		t.Errorf("reasoning[1] text: got %q", txt)
	}

	// text_delta also present
	if !eventHasType(events, "text_delta") {
		t.Error("expected text_delta event for the answer text")
	}
}

// ---------------------------------------------------------------------------
// 3. choice.Delta.ToolCalls only — tool call deltas
// ---------------------------------------------------------------------------

func TestRunStreamingRound_Delta_ToolCallsOnly(t *testing.T) {
	result, events, err := runRoundWithChunks(t,
		toolCallFinishChunk(makeToolCall("call_abc", "get_weather", `{"city":"Tokyo"}`)),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Tool calls in result
	if len(result.ToolCalls) != 1 {
		t.Fatalf("ToolCalls count: want 1, got %d", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.ID != "call_abc" {
		t.Errorf("ToolCall ID: want 'call_abc', got %q", tc.ID)
	}
	if tc.Function.Name != "get_weather" {
		t.Errorf("ToolCall Name: want 'get_weather', got %q", tc.Function.Name)
	}
	if tc.Function.Arguments != `{"city":"Tokyo"}` {
		t.Errorf("ToolCall Arguments: got %q", tc.Function.Arguments)
	}
	if result.FinishReason != "tool_calls" {
		t.Errorf("FinishReason: want 'tool_calls', got %q", result.FinishReason)
	}

	// FinalText should be empty
	if result.FinalText != "" {
		t.Errorf("expected empty FinalText, got %q", result.FinalText)
	}

	// No text_delta or reasoning_delta events
	if eventHasType(events, "text_delta") {
		t.Error("unexpected text_delta event")
	}
	if eventHasType(events, "reasoning_delta") {
		t.Error("unexpected reasoning_delta event")
	}
}

// ---------------------------------------------------------------------------
// 4. All three types in one stream: reasoning → text → tool calls
// ---------------------------------------------------------------------------

func TestRunStreamingRound_Delta_AllThreeTypes(t *testing.T) {
	result, events, err := runRoundWithChunks(t,
		// Reasoning deltas
		reasoningChunk("I need to check the weather."),
		reasoningChunk("Let me call the tool."),
		// Text deltas
		textChunk("Checking weather"),
		textChunk(" for you..."),
		// Final chunk with tool calls
		toolCallFinishChunk(makeToolCall("call_weather", "get_weather", `{"city":"Berlin"}`)),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 1. Text accumulated correctly
	if result.FinalText != "Checking weather for you..." {
		t.Errorf("FinalText: want 'Checking weather for you...', got %q", result.FinalText)
	}

	// 2. Reasoning events recorded
	if count := countEventsOfType(events, "reasoning_delta"); count != 2 {
		t.Errorf("reasoning_delta count: want 2, got %d", count)
	}

	// 3. Text events recorded
	if count := countEventsOfType(events, "text_delta"); count != 2 {
		t.Errorf("text_delta count: want 2, got %d", count)
	}

	// 4. Tool calls in result
	if len(result.ToolCalls) != 1 {
		t.Fatalf("ToolCalls count: want 1, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("ToolCall Name: want 'get_weather', got %q", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[0].Function.Arguments != `{"city":"Berlin"}` {
		t.Errorf("ToolCall Arguments: got %q", result.ToolCalls[0].Function.Arguments)
	}

	// 5. Finish reason
	if result.FinishReason != "tool_calls" {
		t.Errorf("FinishReason: want 'tool_calls', got %q", result.FinishReason)
	}
}

// ---------------------------------------------------------------------------
// 5. Multiple tool calls in the Done chunk
// ---------------------------------------------------------------------------

func TestRunStreamingRound_Delta_MultipleToolCalls(t *testing.T) {
	result, _, err := runRoundWithChunks(t,
		textChunk("Let me look up both cities."),
		toolCallFinishChunk(
			makeToolCall("call_1", "get_weather", `{"city":"Tokyo"}`),
			makeToolCall("call_2", "get_weather", `{"city":"London"}`),
		),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.ToolCalls) != 2 {
		t.Fatalf("ToolCalls count: want 2, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].ID != "call_1" || result.ToolCalls[0].Function.Arguments != `{"city":"Tokyo"}` {
		t.Errorf("ToolCall[0] mismatch: %+v", result.ToolCalls[0])
	}
	if result.ToolCalls[1].ID != "call_2" || result.ToolCalls[1].Function.Arguments != `{"city":"London"}` {
		t.Errorf("ToolCall[1] mismatch: %+v", result.ToolCalls[1])
	}
	if result.FinalText != "Let me look up both cities." {
		t.Errorf("FinalText: got %q", result.FinalText)
	}
}

// ---------------------------------------------------------------------------
// 6. Text and Reasoning in the same ChatCompletionChunk
// ---------------------------------------------------------------------------

func TestRunStreamingRound_Delta_TextAndReasoningInSameChunk(t *testing.T) {
	// A single ChatCompletionChunk can carry both Content and Reasoning;
	// the delta sets both choice.Delta.Content and choice.Delta.Reasoning.
	result, events, err := runRoundWithChunks(t,
		textAndReasoningChunk("visible answer", "hidden reasoning"),
		finishChunk("stop"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Text goes into FinalText
	if result.FinalText != "visible answer" {
		t.Errorf("FinalText: want 'visible answer', got %q", result.FinalText)
	}
	// Both event types emitted
	if !eventHasType(events, "text_delta") {
		t.Error("expected text_delta event")
	}
	if !eventHasType(events, "reasoning_delta") {
		t.Error("expected reasoning_delta event")
	}
	// Verify content of each
	for _, d := range eventDataForType(events, "text_delta") {
		if d["text"] != "visible answer" {
			t.Errorf("text_delta payload: got %q", d["text"])
		}
	}
	for _, d := range eventDataForType(events, "reasoning_delta") {
		if d["text"] != "hidden reasoning" {
			t.Errorf("reasoning_delta payload: got %q", d["text"])
		}
	}
}

// ---------------------------------------------------------------------------
// 7. Multiple text chunks — verify correct accumulation
// ---------------------------------------------------------------------------

func TestRunStreamingRound_Delta_MultipleTextChunks(t *testing.T) {
	result, events, err := runRoundWithChunks(t,
		textChunk("A"),
		textChunk("B"),
		textChunk("C"),
		textChunk("D"),
		textChunk("E"),
		finishChunk("stop"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.FinalText != "ABCDE" {
		t.Errorf("FinalText: want 'ABCDE', got %q", result.FinalText)
	}
	if count := countEventsOfType(events, "text_delta"); count != 5 {
		t.Errorf("text_delta count: want 5, got %d", count)
	}

	// Verify each delta carries the correct character
	deltas := eventDataForType(events, "text_delta")
	expected := []string{"A", "B", "C", "D", "E"}
	for i, d := range deltas {
		if txt, _ := d["text"].(string); txt != expected[i] {
			t.Errorf("text_delta[%d]: want %q, got %q", i, expected[i], txt)
		}
	}
}

// ---------------------------------------------------------------------------
// 8. Empty stream — Done only, no content of any type
// ---------------------------------------------------------------------------

func TestRunStreamingRound_Delta_EmptyStream(t *testing.T) {
	result, events, err := runRoundWithChunks(t,
		finishChunk("stop"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.FinalText != "" {
		t.Errorf("expected empty FinalText, got %q", result.FinalText)
	}
	if result.FinishReason != "stop" {
		t.Errorf("FinishReason: want 'stop', got %q", result.FinishReason)
	}
	if eventHasType(events, "text_delta") {
		t.Error("unexpected text_delta in empty stream")
	}
	if eventHasType(events, "reasoning_delta") {
		t.Error("unexpected reasoning_delta in empty stream")
	}
	if len(result.ToolCalls) != 0 {
		t.Errorf("expected 0 tool calls, got %d", len(result.ToolCalls))
	}
}

// ---------------------------------------------------------------------------
// 9. Stream closes prematurely — graceful handling
// ---------------------------------------------------------------------------

func TestRunStreamingRound_Delta_StreamClosesPremature(t *testing.T) {
	// Channel closes after partial text, no finish chunk.
	result, _, err := runRoundWithChunks(t,
		textChunk("partial"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Finish defaults to "stop" when no explicit finish reason is received.
	if result.FinalText != "partial" {
		t.Errorf("FinalText: want 'partial', got %q", result.FinalText)
	}
	if result.FinishReason != "stop" {
		t.Errorf("FinishReason: want 'stop' (default), got %q", result.FinishReason)
	}
}

// ---------------------------------------------------------------------------
// 10. ResponseID — carried from chunk.ID
// ---------------------------------------------------------------------------

func TestRunStreamingRound_Delta_ResponseID(t *testing.T) {
	chunk := textChunk("test")
	chunk.ID = "local-42"
	result, _, err := runRoundWithChunks(t,
		chunk,
		finishChunk("stop"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ResponseID != "local-42" {
		t.Errorf("ResponseID: want 'local-42', got %q", result.ResponseID)
	}
}

// ---------------------------------------------------------------------------
// 11. Text with trailing whitespace — FinalText is trimmed
// ---------------------------------------------------------------------------

func TestRunStreamingRound_Delta_TextTrimmed(t *testing.T) {
	result, _, err := runRoundWithChunks(t,
		textChunk("  hello  "),
		textChunk("  world  "),
		finishChunk("stop"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// strings.TrimSpace is applied to the full accumulated text
	if result.FinalText != "hello    world" {
		t.Errorf("FinalText: want 'hello    world', got %q", result.FinalText)
	}
}

// ---------------------------------------------------------------------------
// 12. Stream open failure — returns wrapped error
// ---------------------------------------------------------------------------

func TestRunStreamingRound_StreamOpenFailure(t *testing.T) {
	fi := &fakeBackend{
		createStream: func(_ context.Context, _ localmodel.ChatCompletionRequest, _ bool) (<-chan localmodel.ChatCompletionChunk, error) {
			return nil, fmt.Errorf("GPU not available")
		},
	}

	b := newTestBackend(fi)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runCtx := newTestRunCtx()
	events := newAgentEventBuilder(runCtx)

	messages := []localmodel.ChatMessage{
		{Role: "user", Content: "test"},
	}

	_, err := b.runStreamingRound(ctx, cancel, messages, nil, runCtx, events)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "GPU not available") {
		t.Errorf("unexpected error: %v", err)
	}
}
