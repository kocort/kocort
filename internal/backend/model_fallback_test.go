package backend

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/core"
)

func TestRunWithModelFallbackFirstSuccess(t *testing.T) {
	sel := core.ModelSelection{
		Provider:  "openai",
		Model:     "gpt-4.1",
		Fallbacks: []core.ModelCandidate{{Provider: "openai", Model: "gpt-4.1"}},
	}
	result, err := RunWithModelFallback(context.Background(), sel, func(_ context.Context, provider, model, thinkLevel string, isFallback bool) (core.AgentRunResult, error) {
		if isFallback {
			t.Error("should not be a fallback attempt")
		}
		return core.AgentRunResult{
			Payloads: []core.ReplyPayload{{Text: "hello from " + provider}},
		}, nil
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if result.Provider != "openai" {
		t.Errorf("got provider=%q", result.Provider)
	}
	if len(result.Attempts) != 0 {
		t.Errorf("expected 0 failed attempts, got %d", len(result.Attempts))
	}
}

func TestRunWithModelFallbackSecondSuccess(t *testing.T) {
	sel := core.ModelSelection{
		Provider: "openai",
		Model:    "gpt-4.1",
		Fallbacks: []core.ModelCandidate{
			{Provider: "openai", Model: "gpt-4.1"},
			{Provider: "anthropic", Model: "claude-3"},
		},
	}
	callNum := 0
	result, err := RunWithModelFallback(context.Background(), sel, func(_ context.Context, provider, model, thinkLevel string, isFallback bool) (core.AgentRunResult, error) {
		callNum++
		if callNum == 1 {
			return core.AgentRunResult{}, fmt.Errorf("first model failed")
		}
		if !isFallback {
			t.Error("second call should be flagged as fallback")
		}
		return core.AgentRunResult{
			Payloads: []core.ReplyPayload{{Text: "hello from " + provider}},
		}, nil
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if result.Provider != "anthropic" {
		t.Errorf("got provider=%q", result.Provider)
	}
	if len(result.Attempts) != 1 {
		t.Errorf("expected 1 failed attempt, got %d", len(result.Attempts))
	}
}

func TestRunWithModelFallbackAllFail(t *testing.T) {
	sel := core.ModelSelection{
		Provider: "openai",
		Model:    "gpt-4.1",
		Fallbacks: []core.ModelCandidate{
			{Provider: "openai", Model: "gpt-4.1"},
			{Provider: "anthropic", Model: "claude-3"},
		},
	}
	_, err := RunWithModelFallback(context.Background(), sel, func(_ context.Context, _, _, _ string, _ bool) (core.AgentRunResult, error) {
		return core.AgentRunResult{}, fmt.Errorf("model unavailable")
	})
	if err == nil {
		t.Fatal("expected error when all models fail")
	}
	if !strings.Contains(err.Error(), "all models failed (2)") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunWithModelFallbackSingleFail(t *testing.T) {
	sel := core.ModelSelection{
		Provider:  "openai",
		Model:     "gpt-4.1",
		Fallbacks: []core.ModelCandidate{{Provider: "openai", Model: "gpt-4.1"}},
	}
	_, err := RunWithModelFallback(context.Background(), sel, func(_ context.Context, _, _, _ string, _ bool) (core.AgentRunResult, error) {
		return core.AgentRunResult{}, fmt.Errorf("single failure")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	// Single failure returns the original error, not the aggregated one.
	if err.Error() != "single failure" {
		t.Errorf("got %q", err.Error())
	}
}

func TestRunWithModelFallbackNoFallbacks(t *testing.T) {
	sel := core.ModelSelection{
		Provider: "openai",
		Model:    "gpt-4.1",
		// No Fallbacks — should default to primary
	}
	result, err := RunWithModelFallback(context.Background(), sel, func(_ context.Context, provider, model, _ string, _ bool) (core.AgentRunResult, error) {
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "ok"}}}, nil
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if result.Provider != "openai" {
		t.Errorf("got provider=%q", result.Provider)
	}
}
