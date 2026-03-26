package task

import (
	"context"
	"testing"

	"github.com/kocort/kocort/internal/core"
)

func TestRunSubagentAnnounceDispatch_SteerSucceeds(t *testing.T) {
	result := RunSubagentAnnounceDispatch(context.Background(), SubagentAnnounceDispatchParams{
		RunID:                    "run-1",
		RequesterSessionKey:      "agent:main:main",
		ExpectsCompletionMessage: true,
		Announcement: SubagentAnnouncement{
			RunID:          "run-1",
			PrimaryRequest: core.AgentRunRequest{Message: "done"},
		},
		Steer: func(_ context.Context, _ string, _ core.AgentRunRequest) bool { return true },
		Direct: func(_ context.Context, _ core.AgentRunRequest) error {
			t.Fatal("direct should not be called when steer succeeds")
			return nil
		},
	})
	if !result.Delivered || result.Path != DeliveryPathSteered {
		t.Fatalf("expected steered delivery, got path=%q delivered=%v", result.Path, result.Delivered)
	}
}

func TestRunSubagentAnnounceDispatch_NoExpectQueue(t *testing.T) {
	// !expectsCompletionMessage → queue-primary first
	result := RunSubagentAnnounceDispatch(context.Background(), SubagentAnnounceDispatchParams{
		RunID:                    "run-2",
		RequesterSessionKey:      "agent:main:main",
		ExpectsCompletionMessage: false,
		Announcement: SubagentAnnouncement{
			RunID:          "run-2",
			PrimaryRequest: core.AgentRunRequest{Message: "done"},
		},
		Steer: func(_ context.Context, _ string, _ core.AgentRunRequest) bool { return false },
		Queue: func(_ context.Context, _ core.AgentRunRequest) (string, error) { return DeliveryPathQueued, nil },
		Direct: func(_ context.Context, _ core.AgentRunRequest) error {
			t.Fatal("direct should not be called when queue succeeds")
			return nil
		},
	})
	if !result.Delivered || result.Path != DeliveryPathQueued {
		t.Fatalf("expected queued delivery, got path=%q delivered=%v", result.Path, result.Delivered)
	}
	if len(result.Phases) != 1 || result.Phases[0].Phase != DispatchPhaseQueuePrimary {
		t.Fatalf("expected queue-primary phase, got %v", result.Phases)
	}
}

func TestRunSubagentAnnounceDispatch_ExpectDirectThenQueueFallback(t *testing.T) {
	// expectsCompletionMessage → direct-primary first, then queue-fallback
	directCalled := false
	result := RunSubagentAnnounceDispatch(context.Background(), SubagentAnnounceDispatchParams{
		RunID:                    "run-3",
		RequesterSessionKey:      "agent:main:main",
		ExpectsCompletionMessage: true,
		Announcement: SubagentAnnouncement{
			RunID:          "run-3",
			PrimaryRequest: core.AgentRunRequest{Message: "done"},
		},
		Steer: func(_ context.Context, _ string, _ core.AgentRunRequest) bool { return false },
		Direct: func(_ context.Context, _ core.AgentRunRequest) error {
			directCalled = true
			return context.DeadlineExceeded
		},
		Queue: func(_ context.Context, _ core.AgentRunRequest) (string, error) { return DeliveryPathQueued, nil },
	})
	if !directCalled {
		t.Fatal("direct should have been called first")
	}
	if !result.Delivered || result.Path != DeliveryPathQueued {
		t.Fatalf("expected queue fallback, got path=%q delivered=%v", result.Path, result.Delivered)
	}
	if len(result.Phases) != 2 {
		t.Fatalf("expected 2 phases, got %d", len(result.Phases))
	}
	if result.Phases[0].Phase != DispatchPhaseDirectPrimary {
		t.Fatalf("phase[0] should be direct-primary, got %v", result.Phases[0].Phase)
	}
	if result.Phases[1].Phase != DispatchPhaseQueueFallback {
		t.Fatalf("phase[1] should be queue-fallback, got %v", result.Phases[1].Phase)
	}
}
