package catalog

import "testing"

func TestCompanionModelIDFromFilename(t *testing.T) {
	if got := CompanionModelIDFromFilename("gemma-4-e2b-it-mmproj.gguf"); got != "gemma-4-e2b-it" {
		t.Fatalf("expected companion model ID %q, got %q", "gemma-4-e2b-it", got)
	}
	// Mixed-case prefix-style filename from catalog downloads.
	if got := CompanionModelIDFromFilename("mmproj-gemma-4-E2B-it-Q4_K_M.gguf"); got != "gemma-4-e2b-it-q4_k_m" {
		t.Fatalf("expected companion model ID %q, got %q", "gemma-4-e2b-it-q4_k_m", got)
	}
}

func TestInferCapabilitiesGemma4E2B(t *testing.T) {
	caps := InferCapabilities("gemma-4-e2b-it-q4_k_m", "Gemma 4 E2B Instruct", "", true)
	if !caps.Vision || !caps.Audio || !caps.Video || !caps.Tools || !caps.Coding {
		t.Fatalf("unexpected Gemma 4 capabilities: %+v", caps)
	}
	runtimeCaps := IntersectCapabilities(caps, RuntimeSupportedCapabilities())
	if !runtimeCaps.Vision || !runtimeCaps.Tools || !runtimeCaps.Coding || runtimeCaps.Audio || runtimeCaps.Video {
		t.Fatalf("unexpected runtime-filtered Gemma 4 capabilities: %+v", runtimeCaps)
	}
}

func TestInferCapabilitiesQwenCoderNext(t *testing.T) {
	caps := InferCapabilities("qwen3-coder-next-q4_k_m", "Qwen3-Coder-Next", "", false)
	if !caps.Tools || !caps.Coding {
		t.Fatalf("expected Qwen coder capabilities, got %+v", caps)
	}
	if caps.Reasoning {
		t.Fatalf("expected Qwen3-Coder-Next to omit reasoning badge, got %+v", caps)
	}
}
