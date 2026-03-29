package backend

import (
	"context"
	"testing"

	"github.com/kocort/kocort/internal/core"
)

// ---------------------------------------------------------------------------
// NormalizeProviderID
// ---------------------------------------------------------------------------

func TestNormalizeProviderID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"openai", "openai"},
		{"OpenAI", "openai"},
		{"  openai  ", "openai"},
		{"z.ai", "zai"},
		{"z-ai", "zai"},
		{"opencode-zen", "opencode"},
		{"qwen", "qwen-portal"},
		{"kimi-code", "kimi-coding"},
		{"bedrock", "amazon-bedrock"},
		{"aws-bedrock", "amazon-bedrock"},
		{"bytedance", "volcengine"},
		{"doubao", "volcengine"},
		{"anthropic", "anthropic"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := NormalizeProviderID(tt.input); got != tt.want {
				t.Errorf("NormalizeProviderID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// NormalizeModelRef
// ---------------------------------------------------------------------------

func TestNormalizeModelRef(t *testing.T) {
	ref := NormalizeModelRef("OpenAI", " gpt-4.1 ")
	if ref.Provider != "openai" {
		t.Errorf("got provider=%q", ref.Provider)
	}
	if ref.Model != "gpt-4.1" {
		t.Errorf("got model=%q", ref.Model)
	}
}

// ---------------------------------------------------------------------------
// ModelKey
// ---------------------------------------------------------------------------

func TestModelKey(t *testing.T) {
	key := ModelKey("OpenAI", "gpt-4.1")
	if key != "openai/gpt-4.1" {
		t.Errorf("got %q", key)
	}
}

// ---------------------------------------------------------------------------
// ParseModelRef
// ---------------------------------------------------------------------------

func TestParseModelRef(t *testing.T) {
	tests := []struct {
		name            string
		raw             string
		defaultProvider string
		wantProvider    string
		wantModel       string
		wantOK          bool
	}{
		{"with_provider", "anthropic/claude-3", "openai", "anthropic", "claude-3", true},
		{"no_provider", "gpt-4.1", "openai", "openai", "gpt-4.1", true},
		{"empty", "", "openai", "", "", false},
		{"empty_provider", "/gpt-4.1", "openai", "", "", false},
		{"empty_model", "openai/", "openai", "", "", false},
		{"alias", "z.ai/model-x", "openai", "zai", "model-x", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, ok := ParseModelRef(tt.raw, tt.defaultProvider)
			if ok != tt.wantOK {
				t.Fatalf("ok=%v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if ref.Provider != tt.wantProvider {
				t.Errorf("provider=%q, want %q", ref.Provider, tt.wantProvider)
			}
			if ref.Model != tt.wantModel {
				t.Errorf("model=%q, want %q", ref.Model, tt.wantModel)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// NormalizeThinkLevel
// ---------------------------------------------------------------------------

func TestNormalizeThinkLevel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"adaptive", "adaptive"},
		{"auto", "adaptive"},
		{"ADAPTIVE", "adaptive"},
		{"x high", "xhigh"},
		{"xhigh", "xhigh"},
		{"extra_high", "xhigh"},
		{"off", "off"},
		{"on", "low"},
		{"enable", "low"},
		{"enabled", "low"},
		{"min", "minimal"},
		{"minimal", "minimal"},
		{"low", "low"},
		{"thinkhard", "low"},
		{"think-hard", "low"},
		{"think_hard", "low"},
		{"mid", "medium"},
		{"med", "medium"},
		{"medium", "medium"},
		{"thinkharder", "medium"},
		{"think-harder", "medium"},
		{"harder", "medium"},
		{"high", "high"},
		{"ultra", "high"},
		{"ultrathink", "high"},
		{"thinkhardest", "high"},
		{"highest", "high"},
		{"max", "high"},
		{"think", "minimal"},
		{"nonsense", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := NormalizeThinkLevel(tt.input); got != tt.want {
				t.Errorf("NormalizeThinkLevel(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// SupportsXHighThinking
// ---------------------------------------------------------------------------

func TestSupportsXHighThinking(t *testing.T) {
	if !SupportsXHighThinking("openai", "gpt-5.4") {
		t.Error("expected xhigh support for openai/gpt-5.4")
	}
	if !SupportsXHighThinking("openai", "gpt-5.4-mini") {
		t.Error("expected xhigh support for openai/gpt-5.4-mini")
	}
	if !SupportsXHighThinking("openai", "gpt-5.4-nano") {
		t.Error("expected xhigh support for openai/gpt-5.4-nano")
	}
	if SupportsXHighThinking("openai", "gpt-4.1") {
		t.Error("expected no xhigh support for openai/gpt-4.1")
	}
	if !SupportsXHighThinking("openai-codex", "gpt-5.3-codex") {
		t.Error("expected xhigh support for openai-codex/gpt-5.3-codex")
	}
}

// ---------------------------------------------------------------------------
// ResolveThinkingDefault
// ---------------------------------------------------------------------------

func TestResolveThinkingDefault(t *testing.T) {
	t.Run("from_identity", func(t *testing.T) {
		identity := core.AgentIdentity{ThinkingDefault: "high"}
		if got := ResolveThinkingDefault(identity, "openai", "gpt-4.1"); got != "high" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("claude_model", func(t *testing.T) {
		identity := core.AgentIdentity{}
		if got := ResolveThinkingDefault(identity, "anthropic", "claude-3.5-sonnet"); got != "adaptive" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("default_off", func(t *testing.T) {
		identity := core.AgentIdentity{}
		if got := ResolveThinkingDefault(identity, "openai", "gpt-4.1"); got != "off" {
			t.Errorf("got %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// BuildAllowedModelSet
// ---------------------------------------------------------------------------

func TestBuildAllowedModelSetNoAllowlist(t *testing.T) {
	identity := core.AgentIdentity{}
	allowAny, keys := BuildAllowedModelSet(identity, "openai", "gpt-4.1")
	if !allowAny {
		t.Error("expected allowAny=true with empty allowlist")
	}
	if _, ok := keys["openai/gpt-4.1"]; !ok {
		t.Error("expected default model in keys")
	}
}

func TestBuildAllowedModelSetWithAllowlist(t *testing.T) {
	identity := core.AgentIdentity{
		ModelAllowlist: []string{"anthropic/claude-3.5-sonnet", "gpt-4.1"},
	}
	allowAny, keys := BuildAllowedModelSet(identity, "openai", "gpt-4.1")
	if allowAny {
		t.Error("expected allowAny=false with allowlist")
	}
	if _, ok := keys["anthropic/claude-3.5-sonnet"]; !ok {
		t.Error("expected anthropic model in keys")
	}
	if _, ok := keys["openai/gpt-4.1"]; !ok {
		t.Error("expected default model always in keys")
	}
}

// ---------------------------------------------------------------------------
// ResolveModelSelection
// ---------------------------------------------------------------------------

func TestResolveModelSelectionDefaults(t *testing.T) {
	identity := core.AgentIdentity{
		DefaultProvider: "openai",
		DefaultModel:    "gpt-4.1",
	}
	sel, err := ResolveModelSelection(context.Background(), identity, core.AgentRunRequest{}, core.SessionResolution{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sel.Provider != "openai" || sel.Model != "gpt-4.1" {
		t.Errorf("got %s/%s", sel.Provider, sel.Model)
	}
	if sel.ThinkLevel != "off" {
		t.Errorf("got thinkLevel=%q", sel.ThinkLevel)
	}
}

func TestResolveModelSelectionEmptyDefaults(t *testing.T) {
	sel, err := ResolveModelSelection(context.Background(), core.AgentIdentity{}, core.AgentRunRequest{}, core.SessionResolution{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sel.Provider != "" || sel.Model != "" {
		t.Errorf("expected empty selection without configured default, got %s/%s", sel.Provider, sel.Model)
	}
}

func TestResolveModelSelectionWithOverride(t *testing.T) {
	identity := core.AgentIdentity{
		DefaultProvider: "openai",
		DefaultModel:    "gpt-4.1",
	}
	req := core.AgentRunRequest{
		SessionModelOverride:    "claude-3.5-sonnet",
		SessionProviderOverride: "anthropic",
	}
	sel, err := ResolveModelSelection(context.Background(), identity, req, core.SessionResolution{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sel.Provider != "anthropic" || sel.Model != "claude-3.5-sonnet" {
		t.Errorf("got %s/%s", sel.Provider, sel.Model)
	}
	if !sel.StoredOverride {
		t.Error("expected storedOverride=true")
	}
}

func TestResolveModelSelectionBlockedOverride(t *testing.T) {
	identity := core.AgentIdentity{
		DefaultProvider: "openai",
		DefaultModel:    "gpt-4.1",
		ModelAllowlist:  []string{"openai/gpt-4.1"},
	}
	req := core.AgentRunRequest{
		SessionModelOverride:    "claude-3.5-sonnet",
		SessionProviderOverride: "anthropic",
	}
	sel, err := ResolveModelSelection(context.Background(), identity, req, core.SessionResolution{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// Should still be default since override is not in allowlist
	if sel.Provider != "openai" || sel.Model != "gpt-4.1" {
		t.Errorf("got %s/%s, expected default model (override blocked)", sel.Provider, sel.Model)
	}
}

func TestResolveModelSelectionXHighUnsupported(t *testing.T) {
	identity := core.AgentIdentity{
		DefaultProvider: "openai",
		DefaultModel:    "gpt-4.1",
	}
	req := core.AgentRunRequest{Thinking: "xhigh"}
	_, err := ResolveModelSelection(context.Background(), identity, req, core.SessionResolution{})
	if err == nil {
		t.Error("expected error for xhigh on unsupported model")
	}
}

func TestResolveModelSelectionWithFallbacks(t *testing.T) {
	identity := core.AgentIdentity{
		DefaultProvider: "openai",
		DefaultModel:    "gpt-4.1",
		ModelFallbacks:  []string{"anthropic/claude-3.5-sonnet"},
	}
	sel, err := ResolveModelSelection(context.Background(), identity, core.AgentRunRequest{}, core.SessionResolution{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(sel.Fallbacks) != 2 {
		t.Errorf("expected 2 fallbacks (primary + 1), got %d", len(sel.Fallbacks))
	}
}

func TestResolveModelSelectionClaudeThinking(t *testing.T) {
	identity := core.AgentIdentity{
		DefaultProvider: "anthropic",
		DefaultModel:    "claude-3.5-sonnet",
	}
	sel, err := ResolveModelSelection(context.Background(), identity, core.AgentRunRequest{}, core.SessionResolution{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sel.ThinkLevel != "adaptive" {
		t.Errorf("expected adaptive for Claude, got %q", sel.ThinkLevel)
	}
}
