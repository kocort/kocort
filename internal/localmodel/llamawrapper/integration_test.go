//go:build integration

package llamawrapper

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── shared engine (loaded once across all integration tests) ─────────────────

var (
	sharedEngine     *Engine
	sharedEngineOnce sync.Once
	sharedEngineErr  error
)

func testModelPath(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("KOCORT_TEST_MODEL_PATH"); p != "" {
		return p
	}
	// lib/localmodel is 2 levels below the project root
	fallback := filepath.Join("..", "..", "local-config", "models", "qwen3-2b.gguf")
	abs, err := filepath.Abs(fallback)
	if err != nil {
		t.Fatalf("resolve model path: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("model not found at %s (set KOCORT_TEST_MODEL_PATH)", abs)
	}
	return abs
}

func getSharedEngine(t *testing.T) *Engine {
	t.Helper()
	sharedEngineOnce.Do(func() {
		mpath := testModelPath(t)
		t.Logf("loading model: %s", mpath)

		e, err := NewEngine(EngineConfig{
			ModelPath:      mpath,
			ContextSize:    2048,
			BatchSize:      512,
			Parallel:       1,
			GPULayers:      999,
			FlashAttention: -1,
			EnableThinking: false,
		})
		if err != nil {
			sharedEngineErr = err
			return
		}

		ctx, cancel := context.WithCancel(context.Background())
		go e.Run(ctx)
		_ = cancel // the engine will be stopped when the process exits

		sharedEngine = e
		t.Logf("engine ready  arch=%s  ctx=%d", e.ModelArch(), e.ContextSize())
	})

	if sharedEngineErr != nil {
		t.Fatalf("engine init failed: %v", sharedEngineErr)
	}
	return sharedEngine
}

// ── helper: collect all chunks from a ChatCompletion channel ─────────────────

func drainChat(t *testing.T, ch <-chan ChatCompletionChunk) (content, reasoning string, chunks []ChatCompletionChunk) {
	t.Helper()
	var cBuf, rBuf strings.Builder
	for c := range ch {
		chunks = append(chunks, c)
		for _, choice := range c.Choices {
			cBuf.WriteString(choice.Delta.Content)
			rBuf.WriteString(choice.Delta.Reasoning)
		}
	}
	return cBuf.String(), rBuf.String(), chunks
}

// ── Test: tool-safety JSON verdict ───────────────────────────────────────────

const toolSafetyPrompt = `Decide whether this tool call is safe.
tool: rm
args: C:/important_file.txt
Reply with JSON only: {"verdict":"approve|flag|reject","reason":"short reason","risk":"none|low|medium|high"}
Do not output markdown, code fences, or any extra text.



`

// toolSafetySchema constrains the model output to the exact 3-field format.
var toolSafetySchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "verdict": { "type": "string", "enum": ["approve", "flag", "reject"] },
    "reason":  { "type": "string" },
    "risk":    { "type": "string", "enum": ["none", "low", "medium", "high"] }
  },
  "required": ["verdict", "reason", "risk"],
  "additionalProperties": false
}`)

func TestIntegration_ToolSafetyJSONVerdict(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	engine := getSharedEngine(t)

	maxTokens := 1280
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	ch, err := engine.ChatCompletion(ctx, ChatCompletionRequest{
		Model: "test",
		Messages: []ChatMessage{
			{Role: "user", Content: toolSafetyPrompt},
		},
		MaxTokens:   &maxTokens,
		Temperature: Float64Ptr(0.0),
		Stream:      true,
		// EnableThinking: BoolPtr(false),
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	content, reasoning, chunks := drainChat(t, ch)
	t.Logf("generated %d chunks", len(chunks))
	t.Logf("content:\n%s", content)
	t.Logf("reasoning:\n%s", reasoning)

	// ── 验证返回的是合法 JSON ──────────────────────────────────────────────
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		t.Fatal("empty response")
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(trimmed), &result); err != nil {
		t.Fatalf("response is not valid JSON: %v\nraw: %s", err, trimmed)
	}

	// ── 验证 JSON 字段 ───────────────────────────────────────────────────
	verdict, ok := result["verdict"].(string)
	if !ok || verdict == "" {
		t.Errorf("missing or empty 'verdict' field: %v", result)
	}
	validVerdicts := map[string]bool{"approve": true, "flag": true, "reject": true}
	if !validVerdicts[verdict] {
		t.Errorf("unexpected verdict=%q, want one of approve|flag|reject", verdict)
	}

	reason, ok := result["reason"].(string)
	if !ok || reason == "" {
		t.Errorf("missing or empty 'reason' field: %v", result)
	}

	risk, ok := result["risk"].(string)
	if !ok || risk == "" {
		t.Errorf("missing or empty 'risk' field: %v", result)
	}
	validRisks := map[string]bool{"none": true, "low": true, "medium": true, "high": true}
	if !validRisks[risk] {
		t.Errorf("unexpected risk=%q, want one of none|low|medium|high", risk)
	}

	t.Logf("verdict=%s  reason=%q  risk=%s", verdict, reason, risk)
}

// ── Test: non-streaming variant ──────────────────────────────────────────────

func TestIntegration_ToolSafetyJSON_NonStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	engine := getSharedEngine(t)

	maxTokens := 128
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	ch, err := engine.ChatCompletion(ctx, ChatCompletionRequest{
		Model: "test",
		Messages: []ChatMessage{
			{Role: "user", Content: toolSafetyPrompt},
		},
		MaxTokens:   &maxTokens,
		Temperature: Float64Ptr(0.0),
		Stream:      false,
		ResponseFormat: &ResponseFormat{
			Type:       "json_schema",
			JSONSchema: toolSafetySchema,
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	content, _, chunks := drainChat(t, ch)
	t.Logf("chunks=%d  content:\n%s", len(chunks), content)

	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		t.Fatal("empty response")
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(trimmed), &result); err != nil {
		t.Fatalf("response is not valid JSON: %v\nraw: %s", err, trimmed)
	}

	if _, ok := result["verdict"]; !ok {
		t.Errorf("missing 'verdict': %v", result)
	}
	if _, ok := result["reason"]; !ok {
		t.Errorf("missing 'reason': %v", result)
	}
	if _, ok := result["risk"]; !ok {
		t.Errorf("missing 'risk': %v", result)
	}
}

// ── Test: HTTP server endpoint ───────────────────────────────────────────────

func TestIntegration_ToolSafetyJSON_HTTP(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	engine := getSharedEngine(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, err := NewServerFromEngine(engine, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewServerFromEngine: %v", err)
	}
	go func() {
		_ = srv.server.Serve(srv.listener)
	}()
	defer srv.Stop()

	baseURL := "http://" + srv.Addr().String()
	t.Logf("test server at %s", baseURL)

	maxTokens := 128
	reqBody := ChatCompletionRequest{
		Model: "test",
		Messages: []ChatMessage{
			{Role: "user", Content: toolSafetyPrompt},
		},
		MaxTokens:   &maxTokens,
		Temperature: Float64Ptr(0.0),
		Stream:      false,
		ResponseFormat: &ResponseFormat{
			Type:       "json_schema",
			JSONSchema: toolSafetySchema,
		},
	}

	body, _ := json.Marshal(reqBody)
	httpCtx, httpCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer httpCancel()

	req, _ := newHTTPRequest(httpCtx, "POST", baseURL+"/v1/chat/completions", body)
	resp, err := httpClient().Do(req)
	if err != nil {
		t.Fatalf("HTTP request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	var chatResp ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(chatResp.Choices) == 0 {
		t.Fatal("no choices")
	}

	content := ""
	switch v := chatResp.Choices[0].Message.Content.(type) {
	case string:
		content = v
	default:
		t.Fatalf("unexpected content type: %T", chatResp.Choices[0].Message.Content)
	}

	t.Logf("HTTP response:\n%s", content)

	trimmed := strings.TrimSpace(content)
	var result map[string]any
	if err := json.Unmarshal([]byte(trimmed), &result); err != nil {
		t.Fatalf("not valid JSON: %v\nraw: %s", err, trimmed)
	}

	if _, ok := result["verdict"]; !ok {
		t.Errorf("missing 'verdict': %v", result)
	}
}

// ── HTTP helpers ─────────────────────────────────────────────────────────────

func newHTTPRequest(ctx context.Context, method, url string, body []byte) (*http.Request, error) {
	var bodyReader *strings.Reader
	if body != nil {
		bodyReader = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func httpClient() *http.Client {
	return &http.Client{Timeout: 3 * time.Minute}
}
