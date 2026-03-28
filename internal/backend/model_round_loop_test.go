package backend

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/rtypes"
)

type loopTestRuntime struct {
	NopRuntimeServices
	execute func(ctx context.Context, runCtx rtypes.AgentRunContext, name string, args map[string]any) (core.ToolResult, error)
}

func (r *loopTestRuntime) ExecuteTool(ctx context.Context, runCtx rtypes.AgentRunContext, name string, args map[string]any) (core.ToolResult, error) {
	if r.execute != nil {
		return r.execute(ctx, runCtx, name, args)
	}
	return core.ToolResult{}, nil
}

type loopTestState struct {
	round int
}

func TestRunStandardModelToolLoopBlocksRepeatedIdenticalRounds(t *testing.T) {
	dispatcher := delivery.NewReplyDispatcher(&delivery.MemoryDeliverer{}, core.DeliveryTarget{SessionKey: "agent:main:main"})
	runCtx := rtypes.AgentRunContext{
		Runtime: &loopTestRuntime{
			execute: func(ctx context.Context, runCtx rtypes.AgentRunContext, name string, args map[string]any) (core.ToolResult, error) {
				return core.ToolResult{Text: "same-result"}, nil
			},
		},
		Request:         core.AgentRunRequest{RunID: "run-identical"},
		Session:         core.SessionResolution{SessionID: "sess-identical", SessionKey: "agent:main:main"},
		Identity:        core.AgentIdentity{ID: "main"},
		ReplyDispatcher: dispatcher,
	}

	result, err := runStandardModelToolLoop(context.Background(), func() {}, runCtx, StandardModelToolLoopConfig[loopTestState]{
		NoProgressWarningThreshold:  2,
		NoProgressCriticalThreshold: 4,
		ExecuteRound: func(ctx context.Context, cancel context.CancelFunc, state *loopTestState, runCtx rtypes.AgentRunContext, events *agentEventBuilder) (StandardModelRoundResult, error) {
			state.round++
			return StandardModelRoundResult{StopReason: "tool_calls"}, nil
		},
		NormalizeToolCalls: func(state *loopTestState, round StandardModelRoundResult) ([]StandardModelToolCall, error) {
			return []StandardModelToolCall{{
				ID:        fmt.Sprintf("call_%d", state.round),
				Name:      "demo",
				Arguments: `{"value":"same"}`,
			}}, nil
		},
		IsToolCallStopReason: func(reason string) bool {
			return reason == "tool_calls"
		},
		NoProgressLoopError: func(detector string, repeatedRounds int) error {
			return fmt.Errorf("%s:%d", detector, repeatedRounds)
		},
	})
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background())
	if err == nil {
		t.Fatal("expected no-progress loop error")
	}
	if !strings.Contains(err.Error(), "identical_round:4") {
		t.Fatalf("expected identical_round breaker, got %v", err)
	}
	if len(result.Payloads) != 0 {
		t.Fatalf("expected no final payloads, got %+v", result.Payloads)
	}
}

func TestRunStandardModelToolLoopBlocksPingPongRounds(t *testing.T) {
	dispatcher := delivery.NewReplyDispatcher(&delivery.MemoryDeliverer{}, core.DeliveryTarget{SessionKey: "agent:main:main"})
	runCtx := rtypes.AgentRunContext{
		Runtime: &loopTestRuntime{
			execute: func(ctx context.Context, runCtx rtypes.AgentRunContext, name string, args map[string]any) (core.ToolResult, error) {
				value, _ := args["value"].(string)
				return core.ToolResult{Text: "same-" + strings.TrimSpace(value)}, nil
			},
		},
		Request:         core.AgentRunRequest{RunID: "run-pingpong"},
		Session:         core.SessionResolution{SessionID: "sess-pingpong", SessionKey: "agent:main:main"},
		Identity:        core.AgentIdentity{ID: "main"},
		ReplyDispatcher: dispatcher,
	}

	result, err := runStandardModelToolLoop(context.Background(), func() {}, runCtx, StandardModelToolLoopConfig[loopTestState]{
		NoProgressWarningThreshold:  2,
		NoProgressCriticalThreshold: 4,
		ExecuteRound: func(ctx context.Context, cancel context.CancelFunc, state *loopTestState, runCtx rtypes.AgentRunContext, events *agentEventBuilder) (StandardModelRoundResult, error) {
			state.round++
			return StandardModelRoundResult{StopReason: "tool_calls"}, nil
		},
		NormalizeToolCalls: func(state *loopTestState, round StandardModelRoundResult) ([]StandardModelToolCall, error) {
			value := "A"
			if state.round%2 == 0 {
				value = "B"
			}
			return []StandardModelToolCall{{
				ID:        fmt.Sprintf("call_%d", state.round),
				Name:      "demo",
				Arguments: fmt.Sprintf(`{"value":%q}`, value),
			}}, nil
		},
		IsToolCallStopReason: func(reason string) bool {
			return reason == "tool_calls"
		},
		NoProgressLoopError: func(detector string, repeatedRounds int) error {
			return fmt.Errorf("%s:%d", detector, repeatedRounds)
		},
	})
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background())
	if err == nil {
		t.Fatal("expected ping-pong loop error")
	}
	if !strings.Contains(err.Error(), "ping_pong_round:4") {
		t.Fatalf("expected ping_pong_round breaker, got %v", err)
	}
	if len(result.Payloads) != 0 {
		t.Fatalf("expected no final payloads, got %+v", result.Payloads)
	}
}