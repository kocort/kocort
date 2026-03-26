package backend

import (
	"testing"
)

func TestResolveTranscriptPolicy_Anthropic(t *testing.T) {
	policy := ResolveTranscriptPolicy("anthropic", "anthropic-messages", "claude-sonnet-4-20250514")
	if !policy.SanitizeToolCallIDs {
		t.Error("expected SanitizeToolCallIDs=true for anthropic")
	}
	if policy.ToolCallIDMode != "strict" {
		t.Errorf("expected ToolCallIDMode=strict, got %q", policy.ToolCallIDMode)
	}
	if !policy.ValidateAnthropicTurns {
		t.Error("expected ValidateAnthropicTurns=true")
	}
	if !policy.RepairToolUseResultPairing {
		t.Error("expected RepairToolUseResultPairing=true")
	}
	if policy.ValidateGeminiTurns {
		t.Error("expected ValidateGeminiTurns=false for anthropic")
	}
}

func TestResolveTranscriptPolicy_Google(t *testing.T) {
	policy := ResolveTranscriptPolicy("google", "google-genai", "gemini-2.5-flash")
	if !policy.ValidateGeminiTurns {
		t.Error("expected ValidateGeminiTurns=true for google")
	}
	if !policy.ApplyGoogleTurnOrdering {
		t.Error("expected ApplyGoogleTurnOrdering=true for google")
	}
	if policy.ValidateAnthropicTurns {
		t.Error("expected ValidateAnthropicTurns=false for google")
	}
}

func TestResolveTranscriptPolicy_Mistral(t *testing.T) {
	policy := ResolveTranscriptPolicy("mistral", "", "mistral-large-latest")
	if !policy.SanitizeToolCallIDs {
		t.Error("expected SanitizeToolCallIDs=true for mistral")
	}
	if policy.ToolCallIDMode != "strict9" {
		t.Errorf("expected ToolCallIDMode=strict9, got %q", policy.ToolCallIDMode)
	}
}

func TestResolveTranscriptPolicy_OpenAI(t *testing.T) {
	policy := ResolveTranscriptPolicy("openai", "openai-completions", "gpt-4o")
	if !policy.SanitizeToolCallIDs {
		t.Error("expected SanitizeToolCallIDs=true for openai")
	}
	if policy.ToolCallIDMode != "strict" {
		t.Errorf("expected ToolCallIDMode=strict, got %q", policy.ToolCallIDMode)
	}
	if policy.ValidateAnthropicTurns {
		t.Error("expected ValidateAnthropicTurns=false for openai")
	}
}

func TestResolveTranscriptPolicy_Ollama(t *testing.T) {
	policy := ResolveTranscriptPolicy("ollama", "ollama", "llama3.1")
	if policy.SanitizeToolCallIDs {
		t.Error("expected SanitizeToolCallIDs=false for ollama")
	}
	if !policy.RepairToolUseResultPairing {
		t.Error("expected RepairToolUseResultPairing=true (universal)")
	}
}

func TestResolveTranscriptPolicy_Kimi(t *testing.T) {
	policy := ResolveTranscriptPolicy("kimi", "", "moonshot-v1-8k")
	if !policy.RepairMalformedToolCallArgs {
		t.Error("expected RepairMalformedToolCallArgs=true for kimi")
	}
}

func TestResolveTranscriptPolicy_Xai(t *testing.T) {
	policy := ResolveTranscriptPolicy("xai", "", "grok-2")
	if !policy.DecodeHTMLEntityToolCallArgs {
		t.Error("expected DecodeHTMLEntityToolCallArgs=true for xai")
	}
}

func TestResolveTranscriptPolicy_Default(t *testing.T) {
	policy := ResolveTranscriptPolicy("unknown-provider", "", "some-model")
	if !policy.RepairToolUseResultPairing {
		t.Error("expected RepairToolUseResultPairing=true (universal default)")
	}
	if !policy.TrimToolCallNames {
		t.Error("expected TrimToolCallNames=true (universal default)")
	}
	if policy.SanitizeToolCallIDs {
		t.Error("expected SanitizeToolCallIDs=false for unknown provider")
	}
}

func TestShouldDropThinkingBlocks(t *testing.T) {
	tests := []struct {
		provider string
		modelID  string
		want     bool
	}{
		{"openai", "deepseek-r1", true},
		{"anthropic", "claude-sonnet-4-20250514", false},
		{"anthropic", "claude-3.5-thinking", true},
		{"ollama", "qwq-32b", true},
		{"ollama", "qwen3-4b", true},
		{"openai", "gpt-4o", false},
	}
	for _, tt := range tests {
		got := shouldDropThinkingBlocks(tt.provider, tt.modelID)
		if got != tt.want {
			t.Errorf("shouldDropThinkingBlocks(%q, %q) = %v, want %v", tt.provider, tt.modelID, got, tt.want)
		}
	}
}
