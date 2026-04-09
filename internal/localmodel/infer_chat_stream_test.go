//go:build integration

package localmodel

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// modelPath resolves the GGUF model file path used by integration tests.
// It first checks the KOCORT_TEST_MODEL_PATH environment variable; if unset
// it falls back to the repository-local model directory.
func modelPath(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("KOCORT_TEST_MODEL_PATH"); p != "" {
		return p
	}
	// From internal/localmodel/ → ../../local-config/models/
	p := filepath.Join("..", "..", "local-config", "models", "Qwen3.5-0.8B-Q4_K_M.gguf")
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatalf("failed to resolve model path: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("model file not found at %s — skipping integration test (set KOCORT_TEST_MODEL_PATH to override)", abs)
	}
	return abs
}

// modelsDir returns the directory containing the model file.
func modelsDir(t *testing.T) string {
	t.Helper()
	return filepath.Dir(modelPath(t))
}

// newTestManagerWithThinking creates a Manager backed by the real CGO
// inferencer with thinking mode explicitly enabled or disabled.
func newTestManagerWithThinking(t *testing.T, enableThinking bool) *Manager {
	t.Helper()
	inf := NewCGOInferencer()
	mgr := NewManager(Config{
		ModelID:        "Qwen3.5-0.8B-Q4_K_M",
		ModelsDir:      modelsDir(t),
		Threads:        4,
		ContextSize:    4096,
		GpuLayers:      -1, // -1 = offload all layers to GPU
		EnableThinking: enableThinking,
	}, inf, nil)

	if err := mgr.Start(); err != nil {
		t.Fatalf("Manager.Start failed: %v", err)
	}
	t.Cleanup(func() {
		_ = mgr.Stop()
	})
	return mgr
}

// newTestManager creates a Manager backed by the real CGO inferencer and
// the Qwen3.5-0.8B model. It calls t.Cleanup to stop the model automatically.
// Thinking mode is disabled by default; use newTestManagerWithThinking(t, true)
// to enable it.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	inf := NewCGOInferencer()
	mgr := NewManager(Config{
		ModelID:     "Qwen3.5-0.8B-Q4_K_M",
		ModelsDir:   modelsDir(t),
		Threads:     4,
		ContextSize: 4096,
		GpuLayers:   -1, // -1 = offload all layers to GPU
	}, inf, nil)

	if err := mgr.Start(); err != nil {
		t.Fatalf("Manager.Start failed: %v", err)
	}
	t.Cleanup(func() {
		_ = mgr.Stop()
	})
	return mgr
}

// recvAll reads all chunks from a ChatCompletionStream until EOF.
// It returns the concatenated text, the final finish reason, and any error.
func recvAll(t *testing.T, stream ChatCompletionStream, timeout time.Duration) (fullText string, finishReason string, err error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var text strings.Builder
	for {
		select {
		case <-ctx.Done():
			return text.String(), finishReason, fmt.Errorf("timed out waiting for stream")
		default:
		}
		chunk, recvErr := stream.Recv()
		if recvErr != nil {
			if recvErr == io.EOF {
				return text.String(), finishReason, nil
			}
			return text.String(), finishReason, recvErr
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				text.WriteString(choice.Delta.Content)
			}
			if reason := strings.TrimSpace(string(choice.FinishReason)); reason != "" {
				finishReason = reason
			}
		}
	}
}

// ---------------------------------------------------------------------------
// CreateChatCompletionStream tests
// ---------------------------------------------------------------------------

// TestCreateChatCompletionStream_BasicReply verifies that CreateChatCompletionStream
// returns at least one text chunk and a finish reason for a long user message.
func TestCreateChatCompletionStream_BasicReply(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	mgr := newTestManager(t)

	longPrompt := `I'm building a distributed task scheduling system and need your help analyzing the architecture. ` +
		`The system consists of multiple worker nodes that communicate through a message broker (RabbitMQ). ` +
		`Each worker node runs a local scheduler that manages task execution, retry logic, and resource allocation. ` +
		`Tasks are defined as DAGs (Directed Acyclic Graphs) where each node represents a unit of work and edges ` +
		`represent dependencies between tasks. The system needs to handle the following requirements: ` +
		`1) Task prioritization - higher priority tasks should preempt lower priority ones when resources are constrained. ` +
		`2) Fault tolerance - if a worker node crashes, its in-progress tasks should be reassigned to other healthy nodes. ` +
		`3) Resource awareness - tasks declare their CPU and memory requirements, and the scheduler should only assign ` +
		`tasks to nodes with sufficient available resources. 4) Dead letter handling - tasks that fail after exhausting ` +
		`all retries should be moved to a dead letter queue for manual inspection. 5) Rate limiting - certain task types ` +
		`have external API dependencies and must respect rate limits. The current implementation uses a simple round-robin ` +
		`assignment strategy, but this doesn't account for resource constraints or task priorities. I'm considering ` +
		`switching to a weighted scoring algorithm where each candidate worker receives a score based on: available CPU ` +
		`(weight 0.4), available memory (weight 0.3), current task queue depth (weight 0.2), and network latency to the ` +
		`message broker (weight 0.1). The worker with the highest score gets the task assigned. Additionally, I need to ` +
		`implement a circuit breaker pattern for the external API calls to prevent cascading failures when downstream ` +
		`services are unavailable. The circuit breaker should track failure rates over a sliding window of 60 seconds, ` +
		`trip when the failure rate exceeds 50%, and attempt recovery after a 30-second cooldown period. For the DAG ` +
		`execution engine, I'm using a topological sort to determine execution order, but I need to handle dynamic DAGs ` +
		`where new nodes can be added at runtime based on the output of previously completed nodes. This requires ` +
		`re-evaluating the execution plan after each node completes. Please provide a concise summary of the key ` +
		`architectural decisions and potential pitfalls in this design, in about 3-5 sentences.`

	request := buildTestRequest("Qwen3.5-0.8B-Q4_K_M", []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "You are a senior software architect. Provide concise, actionable advice."},
		{Role: openai.ChatMessageRoleUser, Content: longPrompt},
	}, nil, 512)

	stream, err := mgr.CreateChatCompletionStream(context.Background(), request)
	if err != nil {
		t.Fatalf("CreateChatCompletionStream failed: %v", err)
	}
	defer stream.Close()

	fullText, finishReason, err := recvAll(t, stream, 180*time.Second)
	if err != nil {
		t.Fatalf("recvAll failed: %v", err)
	}

	t.Logf("streamed text: %s", fullText)

	if fullText == "" {
		t.Error("expected at least some text in the stream, got nothing")
	}
	if finishReason == "" {
		t.Error("expected a finish reason")
	}
}

// TestCreateChatCompletionStream_WithSystemPrompt verifies streaming works
// when a system prompt is included.
func TestCreateChatCompletionStream_WithSystemPrompt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	mgr := newTestManager(t)

	request := buildTestRequest("Qwen3.5-0.8B-Q4_K_M", []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "You are a helpful assistant. Keep answers very short."},
		{Role: openai.ChatMessageRoleUser, Content: "What is 1+1?"},
	}, nil, 64)

	stream, err := mgr.CreateChatCompletionStream(context.Background(), request)
	if err != nil {
		t.Fatalf("CreateChatCompletionStream failed: %v", err)
	}
	defer stream.Close()

	fullText, _, err := recvAll(t, stream, 60*time.Second)
	if err != nil {
		t.Fatalf("recvAll failed: %v", err)
	}

	t.Logf("response: %s", fullText)

	if fullText == "" {
		t.Error("expected non-empty response for '1+1' question")
	}
}

// TestCreateChatCompletionStream_WithToolDefinitions verifies that the model
// can receive tool definitions and potentially generate tool calls.
func TestCreateChatCompletionStream_WithToolDefinitions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	mgr := newTestManager(t)

	request := buildTestRequest("Qwen3.5-0.8B-Q4_K_M", []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "You are a helpful assistant. Use tools when appropriate."},
		{Role: openai.ChatMessageRoleUser, Content: "What is the weather in Tokyo?"},
	}, []openai.Tool{
		{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "get_weather",
				Description: "Get the current weather for a given city.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"city": map[string]any{
							"type":        "string",
							"description": "The city name",
						},
					},
					"required": []any{"city"},
				},
			},
		},
	}, 256)

	stream, err := mgr.CreateChatCompletionStream(context.Background(), request)
	if err != nil {
		t.Fatalf("CreateChatCompletionStream failed: %v", err)
	}
	defer stream.Close()

	var (
		textParts    []string
		finishReason string
		toolCalls    []openai.ToolCall
	)

	timeout := time.After(15 * time.Second)
	for {
		select {
		case <-timeout:
			t.Fatal("timed out waiting for stream")
		default:
		}
		chunk, recvErr := stream.Recv()
		if recvErr != nil {
			if recvErr == io.EOF {
				break
			}
			t.Fatalf("stream error: %v", recvErr)
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				textParts = append(textParts, choice.Delta.Content)
			}
			for _, tc := range choice.Delta.ToolCalls {
				toolCalls = append(toolCalls, tc)
			}
			if reason := strings.TrimSpace(string(choice.FinishReason)); reason != "" {
				finishReason = reason
			}
		}
	}

	fullText := strings.Join(textParts, "")
	t.Logf("streamed text: %s", fullText)
	t.Logf("finish_reason: %s, tool_calls: %d", finishReason, len(toolCalls))

	// The model may or may not produce tool calls depending on its size and
	// prompt interpretation. We just verify the stream completed cleanly.
	if finishReason == "" {
		t.Error("expected a finish reason")
	}
}

// TestCreateChatCompletionStream_NotRunning verifies that calling
// CreateChatCompletionStream on a stopped manager returns an error immediately.
func TestCreateChatCompletionStream_NotRunning(t *testing.T) {
	inf := NewCGOInferencer()
	mgr := NewManager(Config{
		ModelID:   "Qwen3.5-0.8B-Q4_K_M",
		ModelsDir: modelsDir(t),
	}, inf, nil)
	// Intentionally do NOT call mgr.Start()

	request := buildTestRequest("Qwen3.5-0.8B-Q4_K_M", []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hello"},
	}, nil, 64)

	_, err := mgr.CreateChatCompletionStream(context.Background(), request)
	if err == nil {
		t.Fatal("expected error for stopped manager, got nil")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("expected 'not running' error, got: %v", err)
	}
}

// TestCreateChatCompletionStream_MultiTurn verifies streaming works for a
// multi-turn conversation (multiple user/assistant exchanges).
func TestCreateChatCompletionStream_MultiTurn(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	mgr := newTestManager(t)

	request := buildTestRequest("Qwen3.5-0.8B-Q4_K_M", []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "You are a helpful assistant. Keep answers very short."},
		{Role: openai.ChatMessageRoleUser, Content: "What is the capital of France?"},
		{Role: openai.ChatMessageRoleAssistant, Content: "The capital of France is Paris."},
		{Role: openai.ChatMessageRoleUser, Content: "And what about Japan?"},
	}, nil, 64)

	stream, err := mgr.CreateChatCompletionStream(context.Background(), request)
	if err != nil {
		t.Fatalf("CreateChatCompletionStream failed: %v", err)
	}
	defer stream.Close()

	fullText, _, err := recvAll(t, stream, 60*time.Second)
	if err != nil {
		t.Fatalf("recvAll failed: %v", err)
	}

	t.Logf("multi-turn response: %s", fullText)

	if fullText == "" {
		t.Error("expected non-empty response for multi-turn conversation")
	}
}
