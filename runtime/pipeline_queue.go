// pipeline_queue.go — Stage 3: Queue gating and active-run lifecycle.
//
// Corresponds to the original Run() lines ~108–195.
// Evaluates queue/active-run policy (drop, enqueue, proceed), sets up
// the run timeout context, and registers the active run.
package runtime

import (
	"context"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/task"
)

// gateQueueResult describes the outcome of queue gating.
type gateQueueResult struct {
	// Proceed is true when the run should continue to subsequent stages.
	Proceed bool

	// Result is set when the run was dropped or enqueued (short-circuit).
	Result core.AgentRunResult
}

// gateQueue evaluates the active-run and queue policy, potentially
// short-circuiting the pipeline with a drop or enqueue result.
// When the run should proceed, it sets up the timeout context and
// registers the active run on the PipelineState.
func (p *AgentPipeline) gateQueue(ctx context.Context, state *PipelineState) (gateQueueResult, error) {
	r := p.runtime
	req := state.Request
	sess := state.Session
	identity := state.Identity

	// ---- Build queue settings ----
	queueSettings := task.QueueSettings{
		Mode:       req.QueueMode,
		Debounce:   req.QueueDebounce,
		Cap:        req.QueueCap,
		DropPolicy: req.QueueDropPolicy,
	}
	queueDedupe := req.QueueDedupeMode
	if queueDedupe == "" {
		queueDedupe = core.QueueDedupeMessageID
	}

	// ---- Evaluate active-run action ----
	activeRunQueueAction := task.ResolveActiveRunQueueAction(
		r.ActiveRuns.IsActive(sess.SessionKey),
		req.IsHeartbeat,
		req.ShouldFollowup,
		queueSettings.Mode,
	)

	// Drop: discard the run silently.
	if activeRunQueueAction == core.ActiveRunDrop {
		event.EmitDebugEvent(r.EventHub, sess.SessionKey, req.RunID, "lifecycle", map[string]any{
			"type": "run_dropped",
		})
		return gateQueueResult{Result: core.AgentRunResult{RunID: req.RunID}}, nil
	}

	// Enqueue: push to followup queue.
	if activeRunQueueAction == core.ActiveRunEnqueueFollowup {
		event.RecordRuntimeEvent(ctx, r.Audit, r.Logger,
			identity.ID, sess.SessionKey, req.RunID,
			"run_queued", "info", "runtime queued followup run", map[string]any{
				"queueDepth": r.Queue.Depth(sess.SessionKey),
				"mode":       queueSettings.Mode,
			})
		enqueued := r.Queue.Enqueue(task.FollowupRun{
			QueueKey:             sess.SessionKey,
			Request:              req,
			Prompt:               req.Message,
			EnqueuedAt:           time.Now().UTC(),
			OriginatingChannel:   req.Channel,
			OriginatingTo:        req.To,
			OriginatingAccountID: req.AccountID,
			OriginatingThreadID:  req.ThreadID,
		}, queueSettings, queueDedupe)
		event.EmitDebugEvent(r.EventHub, sess.SessionKey, req.RunID, "lifecycle", map[string]any{
			"type":       "run_queued",
			"queueDepth": r.Queue.Depth(sess.SessionKey),
			"mode":       queueSettings.Mode,
			"enqueued":   enqueued,
		})
		if r.Tasks != nil {
			_ = r.Tasks.MarkQueued(req.TaskID) // best-effort; failure is non-critical
		}
		return gateQueueResult{
			Result: core.AgentRunResult{
				RunID:      req.RunID,
				Queued:     enqueued,
				QueueDepth: r.Queue.Depth(sess.SessionKey),
			},
		}, nil
	}

	// ---- Proceed: set up timeout context and register active run ----
	state.StartedAt = time.Now().UTC()

	runCtxBase := ctx
	cancelRun := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && req.Timeout > 0 {
		runCtxBase, cancelRun = context.WithTimeout(ctx, req.Timeout)
	} else {
		runCtxBase, cancelRun = context.WithCancel(ctx)
	}

	finishActiveRun := r.ActiveRuns.StartRun(sess.SessionKey, req.RunID, cancelRun)

	event.EmitDebugEvent(r.EventHub, sess.SessionKey, req.RunID, "lifecycle", map[string]any{
		"type":  "run_started",
		"agent": req.AgentID,
		"lane":  req.Lane,
	})
	event.RecordRuntimeEvent(runCtxBase, r.Audit, r.Logger,
		identity.ID, sess.SessionKey, req.RunID,
		"run_started", "info", "runtime run started", map[string]any{
			"agent": req.AgentID,
			"lane":  req.Lane,
		})

	// Store lifecycle handles on the state so the caller can wire defers.
	state.runCtxBase = runCtxBase
	state.cancelRun = cancelRun
	state.finishActiveRun = finishActiveRun

	return gateQueueResult{Proceed: true}, nil
}
