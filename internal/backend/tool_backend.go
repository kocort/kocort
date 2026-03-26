package backend

import (
	"context"
	"fmt"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/rtypes"
)

// ToolLoopBackend executes a planning-based tool loop using a ToolPlanner.
type ToolLoopBackend struct {
	Runtime  rtypes.RuntimeServices
	Planner  rtypes.ToolPlanner
	MaxSteps int
}

func (b *ToolLoopBackend) Run(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
	runCtx.Runtime = ensureRuntime(runCtx)
	if b.Runtime == nil {
		return core.AgentRunResult{}, fmt.Errorf("tool loop backend requires runtime")
	}
	if b.Planner == nil {
		return core.AgentRunResult{}, fmt.Errorf("tool loop backend requires planner")
	}
	maxSteps := b.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 8
	}

	state := core.ToolPlannerState{
		UserMessage: runCtx.Request.Message,
	}

	for step := 0; step < maxSteps; step++ {
		plan, err := b.Planner.Next(ctx, runCtx, state)
		if err != nil {
			return core.AgentRunResult{}, err
		}
		if plan.ToolCall == nil {
			for _, payload := range plan.Final {
				runCtx.ReplyDispatcher.SendFinalReply(payload)
			}
			return core.AgentRunResult{
				Payloads: plan.Final,
			}, nil
		}

		result, err := b.Runtime.ExecuteTool(ctx, runCtx, plan.ToolCall.Name, plan.ToolCall.Args)
		if err != nil {
			return core.AgentRunResult{}, err
		}
		if result.Text != "" {
			runCtx.ReplyDispatcher.SendToolResult(core.ReplyPayload{Text: result.Text})
		}
		state.ToolCalls = append(state.ToolCalls, core.ToolCallRecord{
			Name:   plan.ToolCall.Name,
			Args:   plan.ToolCall.Args,
			Result: result,
		})
	}

	return core.AgentRunResult{}, fmt.Errorf("tool loop exceeded max steps (%d)", maxSteps)
}
