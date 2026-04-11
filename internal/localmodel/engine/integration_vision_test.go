//go:build integration

package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── shared vision engine (loaded once across all vision integration tests) ───

var (
	visionEngine     *Engine
	visionEngineOnce sync.Once
	visionEngineErr  error
)

func testVisionModelPath(t *testing.T) (modelPath, mmprojPath string) {
	t.Helper()

	if p := os.Getenv("KOCORT_TEST_VISION_MODEL_PATH"); p != "" {
		mp := os.Getenv("KOCORT_TEST_VISION_MMPROJ_PATH")
		if mp == "" {
			t.Fatal("KOCORT_TEST_VISION_MMPROJ_PATH must be set when KOCORT_TEST_VISION_MODEL_PATH is set")
		}
		return p, mp
	}

	// Fallback: use the gemma-4 model from the dev workspace.
	modelsDir := filepath.Join("..", "..", "..", "cmd", "kocort", ".kocort", "models")
	abs, err := filepath.Abs(modelsDir)
	if err != nil {
		t.Fatalf("resolve models dir: %v", err)
	}

	model := filepath.Join(abs, "gemma-4-E2B-it-Q4_K_M.gguf")
	mmproj := filepath.Join(abs, "mmproj-gemma-4-E2B-it-BF16.gguf")

	if _, err := os.Stat(model); err != nil {
		t.Skipf("vision model not found at %s (set KOCORT_TEST_VISION_MODEL_PATH)", model)
	}
	if _, err := os.Stat(mmproj); err != nil {
		t.Skipf("mmproj not found at %s (set KOCORT_TEST_VISION_MMPROJ_PATH)", mmproj)
	}
	return model, mmproj
}

func getVisionEngine(t *testing.T) *Engine {
	t.Helper()
	visionEngineOnce.Do(func() {
		modelPath, mmprojPath := testVisionModelPath(t)
		t.Logf("loading vision model: %s", modelPath)
		t.Logf("loading mmproj: %s", mmprojPath)

		e, err := NewEngine(EngineConfig{
			ModelPath:      modelPath,
			MmprojPath:     mmprojPath,
			ContextSize:    4096,
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

		if !e.HasVision() {
			visionEngineErr = nil
			visionEngine = e
			// Engine loaded but vision unavailable — will skip in test.
			return
		}

		ctx, cancel := context.WithCancel(context.Background())
		go e.Run(ctx)
		_ = cancel

		visionEngine = e
		t.Logf("vision engine ready  arch=%s  ctx=%d  vision=%v",
			e.ModelArch(), e.ContextSize(), e.HasVision())
	})

	if visionEngineErr != nil {
		t.Fatalf("vision engine init failed: %v", visionEngineErr)
	}
	if !visionEngine.HasVision() {
		t.Skip("vision not available (mtmd library not loaded)")
	}
	return visionEngine
}

// generateTestImageBase64 creates a simple 64x64 PNG with distinct colored
// quadrants (red, green, blue, yellow) and returns it as a base64 data URI.
func generateTestImageBase64() string {
	const size = 64
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	half := size / 2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			switch {
			case x < half && y < half:
				img.Set(x, y, color.RGBA{R: 255, A: 255}) // red
			case x >= half && y < half:
				img.Set(x, y, color.RGBA{G: 255, A: 255}) // green
			case x < half && y >= half:
				img.Set(x, y, color.RGBA{B: 255, A: 255}) // blue
			default:
				img.Set(x, y, color.RGBA{R: 255, G: 255, A: 255}) // yellow
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// ── Test: multimodal image description (streaming) ───────────────────────────

func TestIntegration_VisionImageDescription(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	eng := getVisionEngine(t)
	imgDataURI := generateTestImageBase64()

	maxTokens := 256
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	ch, err := eng.ChatCompletion(ctx, ChatCompletionRequest{
		Model: "test",
		Messages: []ChatMessage{
			{
				Role: "user",
				Content: []any{
					map[string]any{
						"type": "text",
						"text": "Describe what you see in this image. Be concise.",
					},
					map[string]any{
						"type": "image_url",
						"image_url": map[string]any{
							"url": imgDataURI,
						},
					},
				},
			},
		},
		MaxTokens:   &maxTokens,
		Temperature: Float64Ptr(0.3),
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

	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		t.Fatal("empty response from vision model")
	}

	// The image is a simple 4-color grid; the model should say *something*
	// about colors or quadrants. We just verify the response is non-trivial.
	if len(trimmed) < 10 {
		t.Errorf("response too short (%d chars): %q", len(trimmed), trimmed)
	}

	t.Logf("vision response length: %d chars", len(trimmed))
}

// ── Test: multimodal with text-only message (no image, vision engine) ────────

func TestIntegration_VisionEngineTextOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	eng := getVisionEngine(t)

	maxTokens := 128
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	ch, err := eng.ChatCompletion(ctx, ChatCompletionRequest{
		Model: "test",
		Messages: []ChatMessage{
			{Role: "user", Content: "What is 2 + 3? Reply with just the number."},
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

	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		t.Fatal("empty response")
	}

	if !strings.Contains(trimmed, "5") {
		t.Errorf("expected response to contain '5', got: %q", trimmed)
	}
}
