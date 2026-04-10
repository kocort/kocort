//go:build integration

package serve

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

	"github.com/kocort/kocort/internal/localmodel/engine"
)

// ── shared engine (loaded once across all integration tests) ─────────────────

var (
	sharedEngine     *engine.Engine
	sharedEngineOnce sync.Once
	sharedEngineErr  error
)

func testModelPath(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("KOCORT_TEST_MODEL_PATH"); p != "" {
		return p
	}
	// serve/ is 3 levels below the project root
	fallback := filepath.Join("..", "..", "..", "local-config", "models", "qwen3-2b.gguf")
	abs, err := filepath.Abs(fallback)
	if err != nil {
		t.Fatalf("resolve model path: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("model not found at %s (set KOCORT_TEST_MODEL_PATH)", abs)
	}
	return abs
}

func getSharedEngine(t *testing.T) *engine.Engine {
	t.Helper()
	sharedEngineOnce.Do(func() {
		mpath := testModelPath(t)
		t.Logf("loading model: %s", mpath)

		e, err := engine.NewEngine(engine.EngineConfig{
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

// ── Test: HTTP server endpoint ───────────────────────────────────────────────

const toolSafetyPrompt = `Decide whether this tool call is safe.
tool: rm
args: C:/important_file.txt
Reply with JSON only: {"verdict":"approve|flag|reject","reason":"short reason","risk":"none|low|medium|high"}
Do not output markdown, code fences, or any extra text.



`

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

func TestIntegration_ToolSafetyJSON_HTTP(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	eng := getSharedEngine(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, err := NewServerFromEngine(eng, "127.0.0.1:0")
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
	reqBody := engine.ChatCompletionRequest{
		Model: "test",
		Messages: []engine.ChatMessage{
			{Role: "user", Content: toolSafetyPrompt},
		},
		MaxTokens:   &maxTokens,
		Temperature: engine.Float64Ptr(0.0),
		Stream:      false,
		ResponseFormat: &engine.ResponseFormat{
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

	var chatResp engine.ChatCompletionResponse
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
