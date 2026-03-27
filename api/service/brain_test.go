package service

import (
	"testing"

	"github.com/kocort/kocort/internal/config"
)

func TestBrainProviderProbeRequestOpenAICompatUsesNormalizedBaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		baseURL string
		wantURL string
	}{
		{
			name:    "versioned base URL",
			baseURL: "https://portal.qwen.ai/v1",
			wantURL: "https://portal.qwen.ai/v1",
		},
		{
			name:    "chat completions endpoint",
			baseURL: "https://example.com/v1/chat/completions",
			wantURL: "https://example.com/v1",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotURL, _, err := brainProviderProbeRequest(config.ProviderConfig{
				BaseURL: tt.baseURL,
				API:     "openai-completions",
			})
			if err != nil {
				t.Fatalf("brainProviderProbeRequest: %v", err)
			}
			if gotURL != tt.wantURL {
				t.Fatalf("probe URL = %q, want %q", gotURL, tt.wantURL)
			}
		})
	}
}

func TestBrainProviderProbeRequestAnthropicCompatUsesNormalizedBaseURL(t *testing.T) {
	t.Parallel()

	gotURL, _, err := brainProviderProbeRequest(config.ProviderConfig{
		BaseURL: "https://api.anthropic.com/v1/messages",
		API:     "anthropic-messages",
	})
	if err != nil {
		t.Fatalf("brainProviderProbeRequest: %v", err)
	}
	if gotURL != "https://api.anthropic.com" {
		t.Fatalf("probe URL = %q, want %q", gotURL, "https://api.anthropic.com")
	}
}
