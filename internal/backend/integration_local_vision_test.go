//go:build integration

package backend

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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/localmodel"
	"github.com/kocort/kocort/internal/localmodel/catalog"
	"github.com/kocort/kocort/internal/localmodel/manager"
	"github.com/kocort/kocort/internal/rtypes"
)

// testModelsDir returns the path to the local models directory, or skips.
func testModelsDir(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("KOCORT_TEST_MODELS_DIR"); p != "" {
		return p
	}
	// Fallback: dev workspace models.
	dir := filepath.Join("..", "..", "cmd", "kocort", ".kocort", "models")
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("resolve models dir: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("models dir not found at %s", abs)
	}
	return abs
}

// generateTestPNG creates a simple 64x64 colored-quadrant PNG and returns
// the raw PNG bytes.
func generateTestPNG() []byte {
	const size = 64
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	half := size / 2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			switch {
			case x < half && y < half:
				img.Set(x, y, color.RGBA{R: 255, A: 255})
			case x >= half && y < half:
				img.Set(x, y, color.RGBA{G: 255, A: 255})
			case x < half && y >= half:
				img.Set(x, y, color.RGBA{B: 255, A: 255})
			default:
				img.Set(x, y, color.RGBA{R: 255, G: 255, A: 255})
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// newIntegrationDispatcher creates a real ReplyDispatcher for integration tests.
func newIntegrationDispatcher() *delivery.ReplyDispatcher {
	return delivery.NewReplyDispatcher(&delivery.MemoryDeliverer{}, core.DeliveryTarget{
		SessionKey: "test:test:test",
		Channel:    "test",
	})
}

// ---------------------------------------------------------------------------
// Test: trace image data through each conversion stage (no model needed)
// ---------------------------------------------------------------------------

func TestIntegration_ImagePipelineTrace(t *testing.T) {
	imgBytes := generateTestPNG()
	imgB64 := base64.StdEncoding.EncodeToString(imgBytes)

	runCtx := rtypes.AgentRunContext{
		SystemPrompt: "You are a helpful assistant with vision capabilities.",
		Request: core.AgentRunRequest{
			Message: "Describe the image.",
			Attachments: []core.Attachment{{
				Type:     "image",
				Name:     "test.png",
				MIMEType: "image/png",
				Content:  imgBytes,
			}},
		},
	}

	// ── Stage 1: BuildOpenAICompatMessages ──
	openaiMsgs := BuildOpenAICompatMessages(runCtx)
	t.Logf("Stage 1: BuildOpenAICompatMessages → %d messages", len(openaiMsgs))
	for i, m := range openaiMsgs {
		t.Logf("  [%d] role=%s content=%T multiContent=%d", i, m.Role, m.Content, len(m.MultiContent))
		for j, p := range m.MultiContent {
			t.Logf("      part[%d] type=%s textLen=%d hasImageURL=%v", j, p.Type, len(p.Text), p.ImageURL != nil)
			if p.ImageURL != nil {
				urlPreview := p.ImageURL.URL
				if len(urlPreview) > 60 {
					urlPreview = urlPreview[:60] + "..."
				}
				t.Logf("      imageURL=%s", urlPreview)
			}
		}
	}

	// Verify image is in OpenAI messages.
	var openaiImageMsg *openAIChatMessage
	for i := range openaiMsgs {
		if openaiMsgs[i].Role == "user" && len(openaiMsgs[i].MultiContent) > 0 {
			openaiImageMsg = &openaiMsgs[i]
			break
		}
	}
	if openaiImageMsg == nil {
		t.Fatal("Stage 1 FAIL: no multimodal user message found")
	}

	// ── Stage 2: SanitizeOpenAICompatMessages ──
	sanitized := SanitizeOpenAICompatMessages(openaiMsgs)
	t.Logf("Stage 2: SanitizeOpenAICompatMessages → %d messages", len(sanitized))
	var sanitizedImageMsg *openAIChatMessage
	for i := range sanitized {
		t.Logf("  [%d] role=%s content=%T multiContent=%d", i, sanitized[i].Role, sanitized[i].Content, len(sanitized[i].MultiContent))
		if sanitized[i].Role == "user" && len(sanitized[i].MultiContent) > 0 {
			sanitizedImageMsg = &sanitized[i]
		}
	}
	if sanitizedImageMsg == nil {
		t.Fatal("Stage 2 FAIL: multimodal message stripped by sanitization")
	}

	// ── Stage 3: convertOpenAIMessagesToLlama ──
	llamaMsgs := convertOpenAIMessagesToLlama(sanitized)
	t.Logf("Stage 3: convertOpenAIMessagesToLlama → %d messages", len(llamaMsgs))
	var llamaImageMsg *localmodel.ChatMessage
	for i := range llamaMsgs {
		contentType := fmt.Sprintf("%T", llamaMsgs[i].Content)
		t.Logf("  [%d] role=%s content_type=%s", i, llamaMsgs[i].Role, contentType)

		if llamaMsgs[i].Role == "user" {
			if parts, ok := llamaMsgs[i].Content.([]any); ok && len(parts) > 0 {
				llamaImageMsg = &llamaMsgs[i]
				for j, p := range parts {
					pm, _ := p.(map[string]any)
					t.Logf("      part[%d] %v", j, pm["type"])
				}
			}
		}
	}
	if llamaImageMsg == nil {
		t.Fatal("Stage 3 FAIL: image lost in convertOpenAIMessagesToLlama")
	}

	// ── Stage 4: sanitizeLlamaMessages ──
	finalMsgs := sanitizeLlamaMessages(llamaMsgs)
	t.Logf("Stage 4: sanitizeLlamaMessages → %d messages", len(finalMsgs))
	var finalImageMsg *localmodel.ChatMessage
	for i := range finalMsgs {
		contentType := fmt.Sprintf("%T", finalMsgs[i].Content)
		t.Logf("  [%d] role=%s content_type=%s", i, finalMsgs[i].Role, contentType)

		if finalMsgs[i].Role == "user" {
			if parts, ok := finalMsgs[i].Content.([]any); ok && len(parts) > 0 {
				finalImageMsg = &finalMsgs[i]
				for j, p := range parts {
					pm, _ := p.(map[string]any)
					t.Logf("      part[%d] %v", j, pm["type"])
				}
			}
		}
	}
	if finalImageMsg == nil {
		t.Fatal("Stage 4 FAIL: image lost in sanitizeLlamaMessages")
	}

	// ── Stage 5: Verify the final image data URL is valid ──
	parts, _ := finalImageMsg.Content.([]any)
	for _, p := range parts {
		pm, ok := p.(map[string]any)
		if !ok || pm["type"] != "image_url" {
			continue
		}
		imgURLMap, ok := pm["image_url"].(map[string]any)
		if !ok {
			t.Fatal("Stage 5 FAIL: image_url part is not map[string]any")
		}
		urlStr, ok := imgURLMap["url"].(string)
		if !ok || urlStr == "" {
			t.Fatal("Stage 5 FAIL: image url is empty")
		}
		expectedPrefix := "data:image/png;base64,"
		if !strings.HasPrefix(urlStr, expectedPrefix) {
			t.Fatalf("Stage 5 FAIL: unexpected url prefix: %s...", urlStr[:min(len(urlStr), 40)])
		}
		// Verify base64 decodes to valid data.
		b64Data := urlStr[len(expectedPrefix):]
		decoded, err := base64.StdEncoding.DecodeString(b64Data)
		if err != nil {
			t.Fatalf("Stage 5 FAIL: base64 decode error: %v", err)
		}
		if len(decoded) == 0 {
			t.Fatal("Stage 5 FAIL: decoded image data is empty")
		}
		t.Logf("Stage 5: image data URL valid, decoded %d bytes (original %d)", len(decoded), len(imgBytes))

		// Verify it's the same image data.
		if !bytes.Equal(decoded, imgBytes) {
			t.Errorf("Stage 5: decoded image differs from original (%d vs %d bytes)", len(decoded), len(imgBytes))
		}
		break
	}

	// ── Stage 6: Verify JSON round-trip (as sent to engine) ──
	jsonBytes, err := json.Marshal(finalMsgs)
	if err != nil {
		t.Fatalf("Stage 6 FAIL: marshal error: %v", err)
	}
	t.Logf("Stage 6: JSON marshal OK (%d bytes)", len(jsonBytes))

	// Verify the JSON contains image_url.
	if !strings.Contains(string(jsonBytes), imgB64[:20]) {
		t.Fatal("Stage 6 FAIL: JSON does not contain image base64 data")
	}

	t.Log("All pipeline stages passed — image data preserved end-to-end")
}

// ---------------------------------------------------------------------------
// Test: real model inference with image
// ---------------------------------------------------------------------------

func TestIntegration_LocalModelVisionInference(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	modelsDir := testModelsDir(t)

	// Check model files exist.
	modelFile := filepath.Join(modelsDir, "gemma-4-E2B-it-Q4_K_M.gguf")
	mmprojFile := filepath.Join(modelsDir, "mmproj-gemma-4-E2B-it-BF16.gguf")
	if _, err := os.Stat(modelFile); err != nil {
		t.Skipf("model not found: %s", modelFile)
	}
	if _, err := os.Stat(mmprojFile); err != nil {
		t.Skipf("mmproj not found: %s", mmprojFile)
	}

	presets := catalog.BuiltinCatalogPresets()

	mgr := manager.NewManager(manager.Config{
		ModelsDir:      modelsDir,
		ModelID:        "gemma-4-e2b-it-q4_k_m",
		Threads:        4,
		ContextSize:    4096,
		GpuLayers:      999,
		EnableThinking: false,
	}, presets)
	defer mgr.Close()

	t.Log("Starting model...")
	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	status := mgr.WaitReady()
	t.Logf("Model status: %s", status)
	if status != "running" {
		t.Fatalf("model not running: %s (error: %s)", status, mgr.LastError())
	}
	if mgr.IsStub() {
		t.Skip("binary built without llamacpp — cannot run real inference")
	}

	// ── Test 1: Direct Manager.CreateChatCompletionStream with image ──
	t.Log("=== Test 1: Direct stream with image ===")
	imgBytes := generateTestPNG()
	imgB64 := base64.StdEncoding.EncodeToString(imgBytes)
	imgDataURI := "data:image/png;base64," + imgB64

	maxTokens := 128
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	ch, err := mgr.CreateChatCompletionStream(ctx, localmodel.ChatCompletionRequest{
		Model: "gemma-4-e2b-it-q4_k_m",
		Messages: []localmodel.ChatMessage{
			{
				Role: "user",
				Content: []any{
					map[string]any{
						"type": "text",
						"text": "Describe what you see in this image briefly.",
					},
					map[string]any{
						"type":      "image_url",
						"image_url": map[string]any{"url": imgDataURI},
					},
				},
			},
		},
		MaxTokens: &maxTokens,
		Stream:    true,
	})
	if err != nil {
		t.Fatalf("CreateChatCompletionStream: %v", err)
	}

	var fullText strings.Builder
	for chunk := range ch {
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				fullText.WriteString(choice.Delta.Content)
			}
		}
	}
	response := strings.TrimSpace(fullText.String())
	t.Logf("Direct response (%d chars): %s", len(response), response)
	if response == "" {
		t.Fatal("empty response from direct stream with image")
	}

	// ── Test 2: Full LocalModelBackend.Run with simulated runCtx ──
	t.Log("=== Test 2: Full backend.Run with image attachment ===")

	backend := NewLocalModelBackend(mgr)

	// Trace the messages being built (same as what backend.Run does internally).
	traceRunCtx := rtypes.AgentRunContext{
		SystemPrompt: "You are a helpful assistant with vision capabilities. Describe images concisely.",
		Request: core.AgentRunRequest{
			Message: "What is in this image?",
			RunID:   "test-vision-run",
			Attachments: []core.Attachment{{
				Type:     "image",
				Name:     "test.png",
				MIMEType: "image/png",
				Content:  imgBytes,
			}},
		},
	}

	openaiMsgs := BuildOpenAICompatMessages(traceRunCtx)
	t.Logf("BuildOpenAICompatMessages: %d messages", len(openaiMsgs))
	for i, m := range openaiMsgs {
		t.Logf("  [%d] role=%s multiContent=%d content_type=%T", i, m.Role, len(m.MultiContent), m.Content)
	}

	llamaMsgs := convertOpenAIMessagesToLlama(SanitizeOpenAICompatMessages(openaiMsgs))
	t.Logf("After conversion: %d messages", len(llamaMsgs))
	for i, m := range llamaMsgs {
		t.Logf("  [%d] role=%s content_type=%T", i, m.Role, m.Content)
		if parts, ok := m.Content.([]any); ok {
			for j, p := range parts {
				pm, _ := p.(map[string]any)
				t.Logf("      part[%d] type=%v", j, pm["type"])
			}
		}
	}

	sanitizedLlama := sanitizeLlamaMessages(llamaMsgs)
	t.Logf("After sanitizeLlamaMessages: %d messages", len(sanitizedLlama))
	for i, m := range sanitizedLlama {
		t.Logf("  [%d] role=%s content_type=%T", i, m.Role, m.Content)
		if parts, ok := m.Content.([]any); ok {
			for j, p := range parts {
				pm, _ := p.(map[string]any)
				t.Logf("      part[%d] type=%v", j, pm["type"])
			}
		}
	}

	// Check that image is still present after all conversion stages.
	hasImage := false
	for _, m := range sanitizedLlama {
		if parts, ok := m.Content.([]any); ok {
			for _, p := range parts {
				pm, _ := p.(map[string]any)
				if pm["type"] == "image_url" {
					hasImage = true
				}
			}
		}
	}
	if !hasImage {
		t.Fatal("PIPELINE BUG: image_url part not found in final messages after all conversion stages")
	}
	t.Log("Image present in final messages — pipeline conversion OK")

	// Now do the actual inference through the full backend.Run path.
	runCtx := rtypes.AgentRunContext{
		Runtime:      &NopRuntimeServices{},
		SystemPrompt: "You are a helpful assistant with vision capabilities. Describe images concisely.",
		Request: core.AgentRunRequest{
			Message: "What is in this image?",
			RunID:   "test-vision-run",
			Timeout: 3 * time.Minute,
			Attachments: []core.Attachment{{
				Type:     "image",
				Name:     "test.png",
				MIMEType: "image/png",
				Content:  imgBytes,
			}},
		},
		Identity:        core.AgentIdentity{ID: "test-agent"},
		Session:         core.SessionResolution{SessionKey: "test:test:test"},
		ReplyDispatcher: newIntegrationDispatcher(),
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel2()

	result, err := backend.Run(ctx2, runCtx)
	if err != nil {
		t.Fatalf("backend.Run: %v", err)
	}

	// Collect all response text from payloads.
	var responseText strings.Builder
	for _, p := range result.Payloads {
		responseText.WriteString(p.Text)
	}
	respStr := strings.TrimSpace(responseText.String())

	t.Logf("Backend response (%d chars): %s", len(respStr), respStr)
	if respStr == "" {
		t.Fatal("empty response from backend.Run with image")
	}

	// Check the model actually saw the image (shouldn't say "no image provided").
	lower := strings.ToLower(respStr)
	if strings.Contains(lower, "no image") || strings.Contains(lower, "upload") || strings.Contains(lower, "haven't provided") || strings.Contains(lower, "not provided") {
		t.Errorf("Model claims no image was provided — image pipeline broken. Response: %s", respStr)
	}
}
