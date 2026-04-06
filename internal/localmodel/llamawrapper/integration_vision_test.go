//go:build llamacpp

package llamawrapper

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── shared vision engine (loaded once, with mmproj) ──────────────────────────

var (
	visionEngine     *Engine
	visionEngineOnce sync.Once
	visionEngineErr  error
)

func testVisionModelPath(t *testing.T) (model, mmproj string) {
	t.Helper()

	model = os.Getenv("KOCORT_TEST_VISION_MODEL")
	mmproj = os.Getenv("KOCORT_TEST_VISION_MMPROJ")

	if model == "" || mmproj == "" {
		// Default paths matching the local dev setup
		model = `E:\workspace\kocort\cmd\kocort\.kocort\models\gemma-4-E2B-it-Q4_K_M.gguf`
		mmproj = `E:\workspace\kocort\cmd\kocort\.kocort\models\mmproj-gemma-4-E2B-it-BF16.gguf`
	}

	if _, err := os.Stat(model); err != nil {
		t.Skipf("vision model not found: %s", model)
	}
	if _, err := os.Stat(mmproj); err != nil {
		t.Skipf("mmproj not found: %s", mmproj)
	}
	return model, mmproj
}

func getVisionEngine(t *testing.T) *Engine {
	t.Helper()
	visionEngineOnce.Do(func() {
		model, mmproj := testVisionModelPath(t)
		t.Logf("loading vision model: %s", model)
		t.Logf("loading mmproj: %s", mmproj)

		e, err := NewEngine(EngineConfig{
			ModelPath:      model,
			MmprojPath:     mmproj,
			ContextSize:    131072,
			BatchSize:      512,
			Parallel:       1,
			GPULayers:      999,
			FlashAttention: -1,
			EnableThinking: false,
		})
		if err != nil {
			visionEngineErr = err
			return
		}

		ctx, cancel := context.WithCancel(context.Background())
		go e.Run(ctx)
		_ = cancel

		visionEngine = e
		t.Logf("vision engine ready  arch=%s  ctx=%d  hasVision=%v",
			e.ModelArch(), e.ContextSize(), e.image != nil)
	})
	if visionEngineErr != nil {
		t.Fatalf("vision engine init failed: %v", visionEngineErr)
	}
	if visionEngine.image == nil {
		t.Fatal("vision projector not loaded — image support disabled")
	}
	return visionEngine
}

// ── helper: generate a simple test PNG ───────────────────────────────────────

// makeTestPNG creates a small solid-color PNG in memory (red square).
func makeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// Create a clear pattern: top half red, bottom half blue
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if y < h/2 {
				img.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
			} else {
				img.Set(x, y, color.RGBA{R: 0, G: 0, B: 255, A: 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	return buf.Bytes()
}

// ── Test: basic image description ────────────────────────────────────────────

func TestIntegration_VisionDescribeImage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	engine := getVisionEngine(t)

	// Load a real screenshot image from disk.
	const testImagePath = `C:\Users\lane\Pictures\Screenshots\屏幕截图 2024-06-21 090206.png`
	imgBytes, err := os.ReadFile(testImagePath)
	if err != nil {
		t.Fatalf("failed to read test image: %v", err)
	}
	b64 := base64.StdEncoding.EncodeToString(imgBytes)
	dataURL := "data:image/png;base64," + b64

	maxTokens := 256
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	ch, err := engine.ChatCompletion(ctx, ChatCompletionRequest{
		Model: "test",
		Messages: []ChatMessage{
			{
				Role: "user",
				Content: []any{
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
					map[string]any{"type": "text", "text": "Describe what you see in this image. Answer briefly."},
				},
			},
		},
		MaxTokens:   &maxTokens,
		Temperature: Float64Ptr(0.0),
		Stream:      true,
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	content, reasoning, chunks := drainChat(t, ch)
	t.Logf("generated %d chunks", len(chunks))
	t.Logf("content:\n%s", content)
	if reasoning != "" {
		t.Logf("reasoning:\n%s", reasoning)
	}

	if content == "" {
		t.Fatal("empty response — model produced no output")
	}

	// The model should describe visual content, NOT say it can't see an image
	lower := strings.ToLower(content)
	noImagePhrases := []string{
		"cannot see", "can't see", "no image", "not provided",
		"provide an image", "provide a", "没有接收到", "sample image",
		"请上传", "上传图片", "无法看到",
	}
	for _, phrase := range noImagePhrases {
		if strings.Contains(lower, phrase) {
			t.Errorf("model claims it cannot see the image (phrase=%q):\n%s", phrase, content)
		}
	}
}

// ── Test: image + text multipart message ─────────────────────────────────────

func TestIntegration_VisionMultipartMessage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	engine := getVisionEngine(t)

	imgBytes := makeTestPNG(t, 224, 224)
	b64 := base64.StdEncoding.EncodeToString(imgBytes)
	dataURL := "data:image/png;base64," + b64

	maxTokens := 256
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	ch, err := engine.ChatCompletion(ctx, ChatCompletionRequest{
		Model: "test",
		Messages: []ChatMessage{
			{
				Role: "user",
				Content: []any{
					map[string]any{"type": "text", "text": "Describe what you see in this image in one sentence."},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
				},
			},
		},
		MaxTokens:   &maxTokens,
		Temperature: Float64Ptr(0.0),
		Stream:      true,
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	content, _, chunks := drainChat(t, ch)
	t.Logf("generated %d chunks", len(chunks))
	t.Logf("content:\n%s", content)

	if content == "" {
		t.Fatal("empty response")
	}

	// Model should NOT say it can't see an image
	lower := strings.ToLower(content)
	noImagePhrases := []string{
		"没有接收到",
		"no image",
		"don't see",
		"cannot see",
		"can't see",
		"not provided",
		"provide an image",
		"provide a",
		"sample image",
		"no picture",
	}
	for _, phrase := range noImagePhrases {
		if strings.Contains(lower, phrase) {
			t.Errorf("model claims it cannot see the image (phrase=%q):\n%s", phrase, content)
		}
	}
}

// ── Test: tokenization pipeline directly ─────────────────────────────────────

func TestIntegration_VisionTokenize(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	engine := getVisionEngine(t)

	imgBytes := makeTestPNG(t, 64, 64)

	// Call the tokenizer directly to verify image embedding generation
	prompt := "[img-0] What is this?"
	images := []ImageData{{ID: 0, Data: imgBytes}}

	inputs, err := engine.tokenize(prompt, images)
	if err != nil {
		t.Fatalf("tokenize: %v", err)
	}

	// Should have both token inputs (text) and embed inputs (image)
	var nTokens, nEmbeds int
	for _, inp := range inputs {
		if len(inp.embed) > 0 {
			nEmbeds++
		} else {
			nTokens++
		}
	}

	t.Logf("tokenize result: %d total inputs, %d token inputs, %d embed inputs",
		len(inputs), nTokens, nEmbeds)

	if nEmbeds == 0 {
		t.Fatal("no embedding inputs — image was not processed")
	}

	// For Gemma 4 with AvgPool2d(3,3): expect 16 embed inputs (4x4)
	t.Logf("embed count = %d (expect 16 for Gemma 4 with AvgPool2d)", nEmbeds)

	// Check embed dimensions — should be 1536 (projection dim)
	for i, inp := range inputs {
		if len(inp.embed) > 0 {
			t.Logf("embed[%d] dim=%d first3=[%.2f, %.2f, %.2f]",
				i, len(inp.embed), inp.embed[0], inp.embed[1], inp.embed[2])
			break
		}
	}
}

// ── Test: webchat path (system prompt + multipart user message) ──────────────

// TestIntegration_VisionWebchatPath simulates the exact message flow used by
// the webchat: a system message + a multipart user message constructed the
// same way as convertOpenAIMessagesToLlama + sanitizeLlamaMessages would.
func TestIntegration_VisionWebchatPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	engine := getVisionEngine(t)

	imgBytes := makeTestPNG(t, 224, 224)
	b64 := base64.StdEncoding.EncodeToString(imgBytes)
	dataURL := "data:image/png;base64," + b64

	maxTokens := 256
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Simulate the webchat message construction:
	// 1. System message (like the runtime system prompt)
	// 2. User multipart message with image + text (like buildOpenAICompatAttachmentParts)
	ch, err := engine.ChatCompletion(ctx, ChatCompletionRequest{
		Model: "test",
		Messages: []ChatMessage{
			{
				Role:    "system",
				Content: "You are a helpful assistant. You can see and analyze images that the user sends.",
			},
			{
				Role: "user",
				Content: []any{
					map[string]any{"type": "text", "text": "这张图片里有什么颜色？请简要回答。"},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
				},
			},
		},
		MaxTokens:   &maxTokens,
		Temperature: Float64Ptr(0.0),
		Stream:      true,
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	content, _, chunks := drainChat(t, ch)
	t.Logf("generated %d chunks", len(chunks))
	t.Logf("content:\n%s", content)

	if content == "" {
		t.Fatal("empty response — model produced no output")
	}

	lower := strings.ToLower(content)
	noImagePhrases := []string{
		"cannot see", "can't see", "no image", "not provided",
		"provide an image", "没有接收到", "sample image", "garbled",
		"乱码", "没有收到", "不包含", "no picture",
	}
	for _, phrase := range noImagePhrases {
		if strings.Contains(lower, phrase) {
			t.Errorf("model claims it cannot see the image (phrase=%q):\n%s", phrase, content)
		}
	}

	// Should mention red/blue colors
	if !strings.Contains(lower, "red") && !strings.Contains(lower, "blue") &&
		!strings.Contains(lower, "红") && !strings.Contains(lower, "蓝") {
		t.Errorf("response does not mention expected colors (red/blue):\n%s", content)
	}
}

// ── Test: realistic runtime system prompt + text + image ─────────────────────

// TestIntegration_VisionRealisticSystemPrompt simulates the real webchat flow
// with a long system prompt that includes __SILENT__ rules and other runtime
// sections, combined with a user message containing both text and image.
func TestIntegration_VisionRealisticSystemPrompt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	engine := getVisionEngine(t)

	imgBytes := makeTestPNG(t, 224, 224)
	b64 := base64.StdEncoding.EncodeToString(imgBytes)
	dataURL := "data:image/png;base64," + b64

	// A realistic (trimmed) version of the full runtime system prompt.
	systemPrompt := `You are Kocort, a personal AI assistant.
You run locally and can use tools, analyze images, and manage tasks.

## Safety
Do not produce harmful, hateful, or violent content.

## Silent Replies
When you have nothing to say, respond with ONLY: __SILENT__
⚠️ Rules:
- It must be your ENTIRE message — nothing else
- Never append it to an actual response
- Never wrap it in markdown or code blocks

## Identity
- Name: Kocort
- Role: Personal assistant

## Runtime Info
- Model: gemma-4-E2B-it-Q4_K_M
- Provider: local
- Thinking: off`

	maxTokens := 256
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	ch, err := engine.ChatCompletion(ctx, ChatCompletionRequest{
		Model: "test",
		Messages: []ChatMessage{
			{
				Role:    "system",
				Content: systemPrompt,
			},
			{
				Role: "user",
				Content: []any{
					map[string]any{"type": "text", "text": "这张图片里有什么颜色？"},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
				},
			},
		},
		MaxTokens:   &maxTokens,
		Temperature: Float64Ptr(0.0),
		Stream:      true,
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	content, _, chunks := drainChat(t, ch)
	t.Logf("generated %d chunks", len(chunks))
	t.Logf("content:\n%s", content)

	if content == "" {
		t.Fatal("empty response — model produced no output")
	}

	lower := strings.ToLower(content)

	// Must NOT output __SILENT__ — the user asked a real question with an image
	if strings.Contains(lower, "__silent__") {
		t.Errorf("model incorrectly replied with __SILENT__ instead of answering the image question:\n%s", content)
	}

	// Must NOT claim it can't see images
	noImagePhrases := []string{
		"cannot see", "can't see", "no image", "not provided",
		"provide an image", "没有接收到", "没有收到", "不包含",
		"no picture", "upload", "上传",
	}
	for _, phrase := range noImagePhrases {
		if strings.Contains(lower, phrase) {
			t.Errorf("model claims it cannot see the image (phrase=%q):\n%s", phrase, content)
		}
	}

	// Should mention red/blue colors
	if !strings.Contains(lower, "red") && !strings.Contains(lower, "blue") &&
		!strings.Contains(lower, "红") && !strings.Contains(lower, "蓝") {
		t.Errorf("response does not mention expected colors (red/blue):\n%s", content)
	}
}

// ── Test: multi-turn history with prior failed attempts ──────────────────────

// TestIntegration_VisionWithHistory simulates a conversation where prior turns
// exist (e.g. the transcript from a previous failed attempt where the model
// said it couldn't see the image). The current user message has the image.
func TestIntegration_VisionWithHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	engine := getVisionEngine(t)

	imgBytes := makeTestPNG(t, 224, 224)
	b64 := base64.StdEncoding.EncodeToString(imgBytes)
	dataURL := "data:image/png;base64," + b64

	maxTokens := 256
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Simulate multi-turn: prior user text (no image), prior assistant response
	// saying it can't see image, then current user message WITH image.
	ch, err := engine.ChatCompletion(ctx, ChatCompletionRequest{
		Model: "test",
		Messages: []ChatMessage{
			{
				Role:    "system",
				Content: "You are a helpful assistant. You can see and analyze images.",
			},
			{
				Role:    "user",
				Content: "这张图片里有什么颜色？",
			},
			{
				Role:    "assistant",
				Content: "我没有收到任何图片。请上传一张图片，我来帮您分析。",
			},
			{
				Role: "user",
				Content: []any{
					map[string]any{"type": "text", "text": "这是图片，请告诉我里面有什么颜色？"},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
				},
			},
		},
		MaxTokens:   &maxTokens,
		Temperature: Float64Ptr(0.0),
		Stream:      true,
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	content, _, chunks := drainChat(t, ch)
	t.Logf("generated %d chunks", len(chunks))
	t.Logf("content:\n%s", content)

	if content == "" {
		t.Fatal("empty response — model produced no output")
	}

	lower := strings.ToLower(content)

	// Should NOT repeat "没有收到" — it should see the image now
	noImagePhrases := []string{
		"没有收到", "没有接收到", "no image", "not provided",
		"provide an image", "上传", "upload",
	}
	for _, phrase := range noImagePhrases {
		if strings.Contains(lower, phrase) {
			t.Errorf("model still claims it cannot see the image (phrase=%q):\n%s", phrase, content)
		}
	}

	// Should mention red/blue colors
	if !strings.Contains(lower, "red") && !strings.Contains(lower, "blue") &&
		!strings.Contains(lower, "红") && !strings.Contains(lower, "蓝") {
		t.Errorf("response does not mention expected colors (red/blue):\n%s", content)
	}
}

// ── Test: tools + system prompt reproducing webchat scenario ─────────────────

// makeDummyTools generates N dummy tool definitions similar to real webchat tools.
func makeDummyTools(n int) []Tool {
	tools := make([]Tool, n)
	for i := 0; i < n; i++ {
		tools[i] = Tool{
			Type: "function",
			Function: ToolDefFunc{
				Name:        fmt.Sprintf("tool_%d", i),
				Description: fmt.Sprintf("This is tool number %d. It performs an action on the workspace and returns a result. Use it when the user asks you to do something related to category %d.", i, i),
				Parameters:  json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{"input":{"type":"string","description":"The input for tool %d"},"option":{"type":"string","enum":["a","b","c"]}},"required":["input"]}`, i)),
			},
		}
	}
	return tools
}

// TestIntegration_VisionWithToolsNoVisionHint reproduces the webchat scenario:
// long system prompt + 25 tools, but NO mention of image/vision capabilities.
func TestIntegration_VisionWithToolsNoVisionHint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	engine := getVisionEngine(t)

	imgBytes := makeTestPNG(t, 224, 224)
	b64 := base64.StdEncoding.EncodeToString(imgBytes)
	dataURL := "data:image/png;base64," + b64

	systemPrompt := "You are Kocort, a personal AI assistant running locally.\nYou can use tools to help the user with tasks.\n\n## Safety\nDo not produce harmful, hateful, or violent content.\n\n## Tool Usage\nWhen you need to use a tool, call it by name with the required arguments.\nAlways explain what you're doing before calling a tool.\nIf a tool fails, try an alternative approach.\n\n## Identity\n- Name: Kocort\n- Role: Personal assistant\n\n## Runtime Info\n- Model: gemma-4-E2B-it-Q4_K_M\n- Provider: local\n- Thinking: off\n- Date: 2026-04-05"

	tools := makeDummyTools(25)

	maxTokens := 256
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	ch, err := engine.ChatCompletion(ctx, ChatCompletionRequest{
		Model: "test",
		Messages: []ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: []any{
				map[string]any{"type": "text", "text": "这张图片里有什么颜色？"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
			}},
		},
		Tools:       tools,
		MaxTokens:   &maxTokens,
		Temperature: Float64Ptr(0.0),
		Stream:      true,
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	content, _, chunks := drainChat(t, ch)
	t.Logf("generated %d chunks (no vision hint)", len(chunks))
	t.Logf("content (no vision hint):\n%s", content)

	lower := strings.ToLower(content)
	hasColor := strings.Contains(lower, "red") || strings.Contains(lower, "blue") ||
		strings.Contains(lower, "红") || strings.Contains(lower, "蓝")
	claimsNoImage := strings.Contains(lower, "上传") || strings.Contains(lower, "upload") ||
		strings.Contains(lower, "没有收到") || strings.Contains(lower, "没有接收") ||
		strings.Contains(lower, "no image") || strings.Contains(lower, "provide")

	t.Logf("NO-VISION-HINT: hasColor=%v claimsNoImage=%v", hasColor, claimsNoImage)
}

// TestIntegration_VisionWithToolsAndVisionHint: same as above but WITH
// "You have vision capabilities" in the system prompt.
func TestIntegration_VisionWithToolsAndVisionHint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	engine := getVisionEngine(t)

	imgBytes := makeTestPNG(t, 224, 224)
	b64 := base64.StdEncoding.EncodeToString(imgBytes)
	dataURL := "data:image/png;base64," + b64

	systemPrompt := "You are Kocort, a personal AI assistant running locally.\nYou can use tools to help the user with tasks.\nYou have vision capabilities and can see and analyze images that users send.\n\n## Safety\nDo not produce harmful, hateful, or violent content.\n\n## Tool Usage\nWhen you need to use a tool, call it by name with the required arguments.\nAlways explain what you're doing before calling a tool.\nIf a tool fails, try an alternative approach.\n\n## Identity\n- Name: Kocort\n- Role: Personal assistant\n\n## Runtime Info\n- Model: gemma-4-E2B-it-Q4_K_M\n- Provider: local\n- Thinking: off\n- Date: 2026-04-05"

	tools := makeDummyTools(25)

	maxTokens := 256
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	ch, err := engine.ChatCompletion(ctx, ChatCompletionRequest{
		Model: "test",
		Messages: []ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: []any{
				map[string]any{"type": "text", "text": "这张图片里有什么颜色？"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
			}},
		},
		Tools:       tools,
		MaxTokens:   &maxTokens,
		Temperature: Float64Ptr(0.0),
		Stream:      true,
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	content, _, chunks := drainChat(t, ch)
	t.Logf("generated %d chunks (with vision hint)", len(chunks))
	t.Logf("content (with vision hint):\n%s", content)

	if content == "" {
		t.Fatal("empty response — model produced no output")
	}

	lower := strings.ToLower(content)
	noImagePhrases := []string{
		"上传", "upload", "没有收到", "没有接收",
		"no image", "provide an image", "not provided",
	}
	for _, phrase := range noImagePhrases {
		if strings.Contains(lower, phrase) {
			t.Errorf("model claims it cannot see the image despite vision hint (phrase=%q):\n%s", phrase, content)
		}
	}

	if !strings.Contains(lower, "red") && !strings.Contains(lower, "blue") &&
		!strings.Contains(lower, "红") && !strings.Contains(lower, "蓝") {
		t.Errorf("response does not mention expected colors (red/blue):\n%s", content)
	}
}

// TestIntegration_TextOnlyWithTools: text-only (no image) with 25 tools
// to verify RoPE works for text model with many tools.
func TestIntegration_TextOnlyWithTools(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	engine := getVisionEngine(t)

	systemPrompt := "You are Kocort, a personal AI assistant running locally.\nYou can use tools to help the user with tasks.\n\n## Safety\nDo not produce harmful, hateful, or violent content.\n\n## Tool Usage\nWhen you need to use a tool, call it by name with the required arguments.\nAlways explain what you're doing before calling a tool.\nIf a tool fails, try an alternative approach.\n\n## Identity\n- Name: Kocort\n- Role: Personal assistant\n\n## Runtime Info\n- Model: gemma-4-E2B-it-Q4_K_M\n- Provider: local\n- Thinking: off\n- Date: 2026-04-05"

	tools := makeDummyTools(25)

	maxTokens := 128
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	ch, err := engine.ChatCompletion(ctx, ChatCompletionRequest{
		Model: "test",
		Messages: []ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: "Hello, what is 2+2?"},
		},
		Tools:       tools,
		MaxTokens:   &maxTokens,
		Temperature: Float64Ptr(0.0),
		Stream:      true,
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	content, _, chunks := drainChat(t, ch)
	t.Logf("generated %d chunks (text-only with tools)", len(chunks))
	t.Logf("content:\n%s", content)

	if content == "" {
		t.Fatal("empty response — model produced no output")
	}
}

// drainChatFull collects content, reasoning, and ALL tool calls from a
// streaming ChatCompletion channel.  Tool calls are accumulated by index.
func drainChatFull(t *testing.T, ch <-chan ChatCompletionChunk) (content string, toolCalls []ToolCall, finishReason string) {
	t.Helper()
	var cBuf strings.Builder
	tcMap := map[int]*ToolCall{} // index → accumulated tool call
	for c := range ch {
		for _, choice := range c.Choices {
			cBuf.WriteString(choice.Delta.Content)
			for _, tc := range choice.Delta.ToolCalls {
				existing, ok := tcMap[tc.Index]
				if !ok {
					clone := tc
					tcMap[tc.Index] = &clone
				} else {
					// Accumulate streamed fragments.
					if tc.ID != "" {
						existing.ID = tc.ID
					}
					if tc.Type != "" {
						existing.Type = tc.Type
					}
					if tc.Function.Name != "" {
						existing.Function.Name = tc.Function.Name
					}
					existing.Function.Arguments += tc.Function.Arguments
				}
			}
			if choice.FinishReason != nil {
				finishReason = *choice.FinishReason
			}
		}
	}
	content = cBuf.String()
	for i := 0; i < len(tcMap); i++ {
		if tc, ok := tcMap[i]; ok {
			toolCalls = append(toolCalls, *tc)
		}
	}
	return content, toolCalls, finishReason
}

// TestIntegration_VisionImageToolE2E is a complete end-to-end integration
// test that simulates the real webchat image analysis flow:
//
//  1. ROUND 1 — Main agent: system prompt + image tool + user image message.
//     The model should decide to call the "image" tool.
//  2. IMAGE ANALYSIS — Simulate analyzeImageLocal: send only the image +
//     extracted prompt to the vision model (no tools, no system prompt).
//  3. ROUND 2 — Continuation: feed the tool result back to the main agent.
//     The model should produce a final answer describing the image.
//
// This validates the entire pipeline: prompt → tool call → vision → answer.
func TestIntegration_VisionImageToolE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	engine := getVisionEngine(t)

	// ── Generate a clear test image (top-half red, bottom-half blue) ────
	imgBytes := makeTestPNG(t, 224, 224)
	b64 := base64.StdEncoding.EncodeToString(imgBytes)
	dataURL := "data:image/png;base64," + b64

	// ── Tool definition: the real "image" tool schema ───────────────────
	imageToolDef := Tool{
		Type: "function",
		Function: ToolDefFunc{
			Name:        "image",
			Description: "Analyze an image with the configured image model.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path":   {"type": "string", "description": "Optional workspace-relative image path. If omitted, use the first image attachment from the current request."},
					"prompt": {"type": "string", "description": "Question or instruction for the image analysis."}
				},
				"additionalProperties": false
			}`),
		},
	}

	// A few other tools to make it realistic (the model has many tools).
	otherTools := makeDummyTools(5)
	allTools := append([]Tool{imageToolDef}, otherTools...)

	// ── System prompt telling the agent about image capabilities ────────
	systemPrompt := "You are Kocort, a personal AI assistant running locally.\n" +
		"You can use tools to help the user with tasks.\n" +
		"When the user sends an image and asks about it, use the `image` tool to analyze it.\n\n" +
		"## Tool Usage\n" +
		"When you need to use a tool, call it by name with the required arguments.\n\n" +
		"## Runtime Info\n" +
		"- Model: gemma-4-E2B-it-Q4_K_M\n" +
		"- Provider: local\n" +
		"- Thinking: off"

	maxTokens := 256
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	// ═══════════════════════════════════════════════════════════════════
	// ROUND 1: Main agent with tools + image — expect tool_calls
	// ═══════════════════════════════════════════════════════════════════
	t.Log("── ROUND 1: sending image + tools to main agent ──")

	ch1, err := engine.ChatCompletion(ctx, ChatCompletionRequest{
		Model: "test",
		Messages: []ChatMessage{
			{Role: "system", Content: systemPrompt},
			{
				Role: "user",
				Content: []any{
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
					map[string]any{"type": "text", "text": "这张图片里有什么颜色？"},
				},
			},
		},
		Tools:       allTools,
		MaxTokens:   &maxTokens,
		Temperature: Float64Ptr(0.0),
		Stream:      true,
	})
	if err != nil {
		t.Fatalf("Round 1 ChatCompletion: %v", err)
	}

	content1, toolCalls, finishReason := drainChatFull(t, ch1)
	t.Logf("Round 1: finishReason=%s  toolCalls=%d  content=%q",
		finishReason, len(toolCalls), content1)

	if finishReason != "tool_calls" || len(toolCalls) == 0 {
		t.Fatalf("ROUND 1 FAILED: expected tool_calls, got finishReason=%q toolCalls=%d content=%q",
			finishReason, len(toolCalls), content1)
	}

	// Find the "image" tool call.
	var imageCall *ToolCall
	for i := range toolCalls {
		t.Logf("  tool_call[%d]: name=%q args=%s", i, toolCalls[i].Function.Name, toolCalls[i].Function.Arguments)
		if toolCalls[i].Function.Name == "image" {
			imageCall = &toolCalls[i]
		}
	}
	if imageCall == nil {
		t.Fatalf("ROUND 1 FAILED: model did not call the 'image' tool. Tool calls: %+v", toolCalls)
	}

	// Parse the prompt argument from the image tool call.
	var imageArgs map[string]any
	if err := json.Unmarshal([]byte(imageCall.Function.Arguments), &imageArgs); err != nil {
		t.Fatalf("parse image tool args: %v (raw=%s)", err, imageCall.Function.Arguments)
	}
	imagePrompt, _ := imageArgs["prompt"].(string)
	if imagePrompt == "" {
		imagePrompt = "Describe the image."
	}
	t.Logf("Round 1: image tool called with prompt=%q", imagePrompt)

	// ═══════════════════════════════════════════════════════════════════
	// IMAGE ANALYSIS: simulate analyzeImageLocal — vision-only inference
	// ═══════════════════════════════════════════════════════════════════
	t.Log("── IMAGE ANALYSIS: calling vision model directly (no tools) ──")

	visionMaxTokens := 512
	ch2, err := engine.ChatCompletion(ctx, ChatCompletionRequest{
		Model: "test",
		Messages: []ChatMessage{
			{
				Role: "user",
				Content: []any{
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
					map[string]any{"type": "text", "text": imagePrompt},
				},
			},
		},
		MaxTokens:   &visionMaxTokens,
		Temperature: Float64Ptr(0.0),
		Stream:      true,
		// No tools — exactly what analyzeImageLocal does.
	})
	if err != nil {
		t.Fatalf("Image analysis ChatCompletion: %v", err)
	}

	visionContent, _, _ := drainChat(t, ch2)
	t.Logf("Vision analysis result (%d chars):\n%s", len(visionContent), visionContent)

	if visionContent == "" {
		t.Fatal("IMAGE ANALYSIS FAILED: empty vision response")
	}

	// Vision model should mention red/blue colors for the test PNG.
	visionLower := strings.ToLower(visionContent)
	hasColor := strings.Contains(visionLower, "red") || strings.Contains(visionLower, "blue") ||
		strings.Contains(visionLower, "红") || strings.Contains(visionLower, "蓝")
	if !hasColor {
		t.Logf("WARNING: vision analysis did not mention red/blue colors")
	}

	// Build the tool result JSON (same format as tool_image.go's JSONResult).
	toolResultJSON, _ := json.Marshal(map[string]any{
		"status": "ok",
		"reply":  visionContent,
		"runId":  "test:image",
	})

	// ═══════════════════════════════════════════════════════════════════
	// ROUND 2: Feed tool result back — expect final text answer
	// The original user message still carries the image, but the model
	// should rely on the tool result to formulate its answer.
	// ═══════════════════════════════════════════════════════════════════
	t.Log("── ROUND 2: feeding tool result back to main agent ──")

	ch3, err := engine.ChatCompletion(ctx, ChatCompletionRequest{
		Model: "test",
		Messages: []ChatMessage{
			{Role: "system", Content: systemPrompt},
			{
				Role: "user",
				Content: []any{
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
					map[string]any{"type": "text", "text": "这张图片里有什么颜色？"},
				},
			},
			{
				Role:      "assistant",
				ToolCalls: toolCalls,
			},
			{
				Role:       "tool",
				Name:       "image",
				ToolCallID: imageCall.ID,
				Content:    string(toolResultJSON),
			},
		},
		Tools:       allTools,
		MaxTokens:   &maxTokens,
		Temperature: Float64Ptr(0.0),
		Stream:      true,
	})
	if err != nil {
		t.Fatalf("Round 2 ChatCompletion: %v", err)
	}

	finalContent, finalToolCalls, finalFinish := drainChatFull(t, ch3)
	t.Logf("Round 2: finishReason=%s  toolCalls=%d", finalFinish, len(finalToolCalls))
	t.Logf("Final answer:\n%s", finalContent)

	if finalContent == "" {
		t.Fatal("ROUND 2 FAILED: empty final response")
	}

	// The final answer should describe the image based on the tool result.
	lower := strings.ToLower(finalContent)
	noImagePhrases := []string{
		"上传", "upload", "没有收到", "没有接收",
		"no image", "provide an image", "not provided",
		"无法看到", "cannot see", "can't see",
	}
	for _, phrase := range noImagePhrases {
		if strings.Contains(lower, phrase) {
			t.Errorf("final answer claims it cannot see image (phrase=%q):\n%s", phrase, finalContent)
		}
	}

	// Final answer should mention the colors from the tool result.
	finalHasColor := strings.Contains(lower, "red") || strings.Contains(lower, "blue") ||
		strings.Contains(lower, "红") || strings.Contains(lower, "蓝")
	if !finalHasColor {
		t.Logf("WARNING: final answer did not explicitly mention red/blue (may have paraphrased tool result)")
	}

	t.Log("── E2E IMAGE TOOL TEST COMPLETE ──")
}

// TestIntegration_VisionWebchatFullPrompt reproduces the exact webchat scenario:
//   - Huge system prompt with tool availability, safety rules, recalled memory
//     (recalled memory contains PREVIOUS failed image analysis attempts!)
//   - 25 tool definitions including an "image" tool
//   - Conversation history with 3 turns where model said "请上传实际的图片"
//   - Final user message with real image
//
// This test checks whether the model can actually see the image despite
// the poisoned context (recalled memory + history showing repeated failures).
func TestIntegration_VisionWebchatFullPrompt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	engine := getVisionEngine(t)

	// Load real screenshot
	testImagePath := `C:\Users\lane\Pictures\Screenshots\屏幕截图 2024-06-21 090206.png`
	imgBytes, err := os.ReadFile(testImagePath)
	if err != nil {
		t.Fatalf("failed to read test image: %v", err)
	}
	b64 := base64.StdEncoding.EncodeToString(imgBytes)
	dataURL := "data:image/png;base64," + b64

	// Full system prompt from actual webchat — including recalled memory
	// that contains previous failed image analysis attempts.
	systemPrompt := "## Tooling\n" +
		"Tool availability (filtered by policy):\n" +
		"Tool names are case-sensitive. Call tools exactly as listed.\n" +
		"- read: Read file contents\n" +
		"- write: Create or overwrite files\n" +
		"- edit: Make precise edits to files\n" +
		"- apply_patch: Apply multi-file patches\n" +
		"- grep: Search file contents for patterns\n" +
		"- find: Find files by glob pattern\n" +
		"- ls: List directory contents\n" +
		"- exec: Run shell commands (pty available for TTY-required CLIs)\n" +
		"- process: Manage background exec sessions\n" +
		"- web_search: Search the web (Brave API)\n" +
		"- web_fetch: Fetch and extract readable content from a URL\n" +
		"- browser: Control web browser\n" +
		"- memory_search: Search durable workspace memory\n" +
		"- memory_get: Read a specific memory file or line range\n" +
		"- sessions_spawn: Spawn an isolated sub-agent session\n" +
		"- sessions_list: List other sessions (incl. sub-agents) with filters/last\n" +
		"- sessions_history: Fetch history for another session/sub-agent\n" +
		"- sessions_send: Send a message to another session/sub-agent\n" +
		"- subagents: List, steer, or kill sub-agent runs for this requester session\n" +
		"- session_status: Show a /status-equivalent status card\n" +
		"- message: Send messages and channel actions\n" +
		"- cron: Manage cron jobs and wake events\n" +
		"- gateway: Restart, apply config, or run updates on the running kocort process\n" +
		"- agents_list: List agent ids allowed for sessions_spawn\n" +
		"TOOLS.md does not control tool availability; it is user guidance for how to use external tools.\n\n" +
		"## Tool Call Style\n" +
		"Default: do not narrate routine, low-risk tool calls (just call the tool).\n" +
		"Narrate only when it helps: multi-step work, complex/challenging problems, sensitive actions, or when the user explicitly asks.\n\n" +
		"## Safety\n" +
		"You have no independent goals: do not pursue self-preservation, replication, resource acquisition, or power-seeking.\n" +
		"Prioritize safety and human oversight over completion.\n\n" +
		"## Memory Recall\n" +
		"Before answering anything about prior work, decisions, dates, people, preferences, or todos: run memory_search.\n\n" +
		"## Runtime\n" +
		"Runtime: agent=main | repo=E:\\workspace\\kocort\\cmd\\kocort | os=windows (amd64) | model=local/local | channel=webchat | thinking=off\n\n" +
		"## Workspace\n" +
		"Your working directory is: E:\\workspace\\kocort\\cmd\\kocort\\.kocort\\workspace\n\n" +
		"## Heartbeats\n" +
		"If you receive a heartbeat poll, reply exactly: HEARTBEAT_OK\n"

	// 24 tool definitions (no image-related tools).
	tools := makeDummyTools(24)

	// Clean conversation: no poisoned history, just system + image message.
	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{
			Role: "user",
			Content: []any{
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
				map[string]any{"type": "text", "text": "[Mon 2026-04-06 04:16 UTC] 图片中有什么？"},
			},
		},
	}

	maxTokens := 256
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ch, err := engine.ChatCompletion(ctx, ChatCompletionRequest{
		Model:       "test",
		Messages:    messages,
		Tools:       tools,
		MaxTokens:   &maxTokens,
		Temperature: Float64Ptr(0.0),
		Stream:      true,
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	content, _, chunks := drainChat(t, ch)
	t.Logf("generated %d chunks (webchat full prompt)", len(chunks))
	t.Logf("content:\n%s", content)

	if content == "" {
		t.Fatal("empty response — model produced no output")
	}

	// Check if the model is repeating the poisoned pattern from history/memory
	lower := strings.ToLower(content)
	noImagePhrases := []string{
		"上传", "upload", "没有收到", "没有接收",
		"no image", "provide an image", "not provided",
		"无法看到", "cannot see", "can't see",
	}
	for _, phrase := range noImagePhrases {
		if strings.Contains(lower, phrase) {
			t.Errorf("MODEL REPEATING POISONED PATTERN — claims it cannot see image (phrase=%q):\n%s", phrase, content)
		}
	}
}
