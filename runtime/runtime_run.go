package runtime

import (
	"context"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/task"
)

// This file contains the main Run() method for the Runtime.
//
// The Run() method is a thin orchestrator that delegates to the
// AgentPipeline stages defined in the pipeline_*.go files:
//
//   - pipeline.go:           AgentPipeline + PipelineState definitions
//   - pipeline_validate.go:  Stage 1 — readiness checks, input defaults
//   - pipeline_resolve.go:   Stage 2 — identity, session, commands
//   - pipeline_queue.go:     Stage 3 — active-run gating, queue/drop
//   - pipeline_context.go:   Stage 4 — workspace, transcript, skills, memory, model
//   - pipeline_tools.go:     Stage 5 — tool filtering, plugin injection, RunContext
//   - pipeline_execute.go:   Stage 6 — skill dispatch or model-call loop
//
// Other runtime methods remain in their respective files:
//   - runtime_core.go:             Runtime struct and configuration methods
//   - runtime_helpers.go:          Helper functions
//   - runtime_events.go:           Event emission functions
//   - runtime_tasks.go:            Task management functions
//   - runtime_heartbeat.go:        Heartbeat and scheduled event functions
//   - runtime_inbound.go:          Inbound message processing functions
//   - runtime_subagent.go:         Subagent lifecycle functions
//   - runtime_compaction.go:       Session compaction functions
//   - runtime_transcript.go:       Transcript management functions
//   - runtime_services.go:         Public API methods

// Run executes an agent run request, processing the message through the full
// agent pipeline including session management, tool execution, model inference,
// and response delivery.
//
// The method delegates to AgentPipeline stages. Each stage operates on a
// shared PipelineState, making the individual stages independently testable.
func (r *Runtime) Run(ctx context.Context, req core.AgentRunRequest) (core.AgentRunResult, error) {
	event.SyncDelivererHooks(r.Deliverer, r.Hooks, r.Audit)

	pipeline := newPipeline(r)
	state := &PipelineState{Request: req}

	// Stage 1: Validate inputs and populate defaults.
	if err := pipeline.validate(state); err != nil {
		return core.AgentRunResult{}, err
	}

	// Stage 2: Resolve identity, session; handle reset/compaction commands.
	shortCircuit, err := pipeline.resolve(ctx, state)
	if err != nil {
		return core.AgentRunResult{}, err
	}
	if shortCircuit != nil {
		return shortCircuit.Result, shortCircuit.Err
	}

	// Stage 3: Queue gating — may drop or enqueue the run.
	gateResult, err := pipeline.gateQueue(ctx, state)
	if err != nil {
		return core.AgentRunResult{}, err
	}
	if !gateResult.Proceed {
		return gateResult.Result, nil
	}

	// Active-run lifecycle: cancel context and drain queue on exit.
	defer state.cancelRun()
	defer func() {
		state.finishActiveRun()
		r.Queue.ScheduleDrain(context.Background(), state.Session.SessionKey, func(run task.FollowupRun) error {
			_, err := r.Run(context.Background(), run.Request)
			return err
		})
	}()

	// Stage 4: Load context — workspace, transcript, skills, memory, model.
	if err := pipeline.loadContext(state.runCtxBase, state); err != nil {
		return core.AgentRunResult{}, err
	}

	// Stage 5: Filter tools, resolve plugins, build RunContext.
	if err := pipeline.buildRunContext(state.runCtxBase, state); err != nil {
		return core.AgentRunResult{}, err
	}
	defer state.RestoreSkillEnv()

	// Stage 6: Execute — skill dispatch or model-call loop.
	return pipeline.execute(state.runCtxBase, state)
}
