// pipeline_execute.go — Stage 6: Skill dispatch or model-call loop.
//
// Corresponds to the original Run() lines ~385–628.
// First checks for a skill command dispatch (short-circuit). Otherwise
// enters the model-call loop with provider fallback, transient-HTTP
// retry, and context-overflow compaction.
package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/backend"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/infra"
	memorypkg "github.com/kocort/kocort/internal/memory"
	"github.com/kocort/kocort/internal/rtypes"
	sessionpkg "github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/skill"

	"github.com/kocort/kocort/utils"
)

// execute runs the final stage of the pipeline: either a skill command
// dispatch or the model-call loop.
func (p *AgentPipeline) execute(ctx context.Context, state *PipelineState) (core.AgentRunResult, error) {
	// Try skill command dispatch first (short-circuit).
	if result, handled := p.trySkillDispatch(ctx, state); handled {
		return result, nil
	}
	p.rewriteExplicitSkillInvocation(state)

	if err := p.runPreemptiveMemoryFlushIfNeeded(ctx, state); err != nil {
		return core.AgentRunResult{}, err
	}

	// Model call loop with retry.
	return p.modelCallLoop(ctx, state)
}

// trySkillDispatch checks whether the user message matches a skill command
// and dispatches the corresponding tool if so.
func (p *AgentPipeline) trySkillDispatch(ctx context.Context, state *PipelineState) (core.AgentRunResult, bool) {
	r := p.runtime
	req := state.Request
	runCtx := state.AgentRunCtx
	dispatcher := state.Dispatcher

	skillInvocation := skill.ResolveSkillCommandInvocation(state.Skills, req.Message)
	if skillInvocation == nil {
		return core.AgentRunResult{}, false
	}

	dispatch := skillInvocation.Command.Dispatch
	if dispatch == nil || dispatch.Kind != "tool" {
		return core.AgentRunResult{}, false
	}

	event.EmitDebugEvent(r.EventHub, state.Session.SessionKey, req.RunID, "tool", map[string]any{
		"type":        "skill_dispatch",
		"skillName":   skillInvocation.Command.SkillName,
		"commandName": skillInvocation.Command.Name,
		"toolName":    dispatch.ToolName,
		"args":        skillInvocation.Args,
	})

	result, err := r.ExecuteTool(state.runCtxBase, runCtx, dispatch.ToolName, map[string]any{
		"command":     skillInvocation.Args,
		"commandName": skillInvocation.Command.Name,
		"skillName":   skillInvocation.Command.SkillName,
	})
	if err != nil {
		event.EmitDebugEvent(r.EventHub, state.Session.SessionKey, req.RunID, "lifecycle", map[string]any{
			"type":  "run_failed",
			"error": err.Error(),
		})
		dispatcher.SendFinalReply(core.ReplyPayload{Text: "❌ " + strings.TrimSpace(err.Error()), IsError: true})
		dispatcher.MarkComplete()
		_ = dispatcher.WaitForIdle(state.runCtxBase) // best-effort; failure is non-critical
		return core.AgentRunResult{
			RunID:    req.RunID,
			Payloads: []core.ReplyPayload{{Text: "❌ " + strings.TrimSpace(err.Error()), IsError: true}},
		}, true
	}

	payload := core.ReplyPayload{Text: strings.TrimSpace(utils.NonEmpty(result.Text, "✅ Done."))}
	dispatcher.SendFinalReply(payload)
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(state.runCtxBase) // best-effort; failure is non-critical

	if err := delivery.PersistRunArtifacts(r.Sessions, state.Session, req, state.Skills, result.Text); err != nil {
		return core.AgentRunResult{}, true // error already logged internally
	}

	event.EmitDebugEvent(r.EventHub, state.Session.SessionKey, req.RunID, "lifecycle", map[string]any{
		"type":         "run_completed",
		"payloadCount": 1,
	})
	event.RecordRuntimeEvent(state.runCtxBase, r.Audit, r.Logger, runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID, "run_completed", "info", "runtime run completed", map[string]any{
		"payloadCount": 1,
	})

	return core.AgentRunResult{
		RunID:      req.RunID,
		Payloads:   []core.ReplyPayload{payload},
		StartedAt:  time.Now().UTC(),
		FinishedAt: time.Now().UTC(),
	}, true
}

func (p *AgentPipeline) rewriteExplicitSkillInvocation(state *PipelineState) {
	invocation := skill.ResolveSkillCommandInvocation(state.Skills, state.Request.Message)
	if invocation == nil {
		return
	}
	if invocation.Command.Dispatch != nil && invocation.Command.Dispatch.Kind == "tool" {
		return
	}
	parts := []string{
		fmt.Sprintf("Use the %q skill for this request.", invocation.Command.SkillName),
	}
	if trimmedArgs := strings.TrimSpace(invocation.Args); trimmedArgs != "" {
		parts = append(parts, "User input:\n"+trimmedArgs)
	}
	rewritten := strings.Join(parts, "\n\n")
	state.Request.Message = rewritten
	state.AgentRunCtx.Request.Message = rewritten
	state.AgentRunCtx.SystemPrompt = infra.BuildSystemPrompt(buildPromptParams(state, state.Request, state.Transcript))
}

// modelCallLoop runs the model with provider fallback and retry logic
// for transient HTTP errors and context overflow.
func (p *AgentPipeline) modelCallLoop(ctx context.Context, state *PipelineState) (core.AgentRunResult, error) {
	r := p.runtime
	req := state.Request
	sess := state.Session
	runCtx := state.AgentRunCtx
	dispatcher := state.Dispatcher
	target := state.Target
	startedAt := state.StartedAt
	selection := state.Selection

	didRetryTransientHTTP := false
	didRetryCompaction := false

	for {
		// Fast-exit: if the run context has already been cancelled (e.g. via
		// ChatCancel), bail out immediately instead of starting a new model call.
		if ctx.Err() != nil {
			slog.Info("[AgentPipeline] call loop cancel")
			return core.AgentRunResult{}, ctx.Err()
		}

		fallbackResult, err := backend.RunWithModelFallback(state.runCtxBase, selection, func(
			fctx context.Context,
			provider, model string,
			thinkLevel string,
			isFallbackRetry bool,
		) (core.AgentRunResult, error) {
			attemptCtx := runCtx
			attemptCtx.ModelSelection.Provider = provider
			attemptCtx.ModelSelection.Model = model
			attemptCtx.ModelSelection.ThinkLevel = thinkLevel
			if isFallbackRetry {
				attemptCtx.Request.Message = "Continue where you left off. The previous model attempt failed or timed out."
			}
			resolvedBackend, _, resolveErr := resolveBackend(r.Backends, r.Backend, attemptCtx)
			if resolveErr != nil {
				return core.AgentRunResult{}, resolveErr
			}
			return resolvedBackend.Run(fctx, attemptCtx)
		})

		result := fallbackResult.Result
		result.SuccessfulCronAdds = runCtx.RunState.SuccessfulCronAdds
		var reminderTasks delivery.TaskLister
		if r.Tasks != nil {
			reminderTasks = r.Tasks
		}
		result = delivery.ApplyReminderGuard(reminderTasks, runCtx.Session.SessionKey, result.SuccessfulCronAdds, result)
		dispatcher.MarkComplete()
		if waitErr := dispatcher.WaitForIdle(state.runCtxBase); waitErr != nil && err == nil {
			err = waitErr
		}

		// ---- Error handling ----
		if err != nil {
			reason := backend.ErrorReason(err)
			slog.Warn("[pipeline] model call failed",
				"reason", reason,
				"error", err,
				"session", sess.SessionKey,
				"runID", req.RunID)
			event.EmitDebugEvent(r.EventHub, sess.SessionKey, req.RunID, "lifecycle", map[string]any{
				"type":   "run_failed",
				"reason": reason,
				"error":  err.Error(),
			})
			event.RecordRuntimeEvent(state.runCtxBase, r.Audit, r.Logger, runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID, "run_failed", "error", "runtime run failed", map[string]any{
				"reason": reason,
				"error":  err.Error(),
			})

			// If the dispatcher has already streamed visible payloads, return
			// those as a partial success.
			if visiblePayloads := delivery.VisibleAssistantPayloads(dispatcher); len(visiblePayloads) > 0 {
				result = p.buildPartialSuccessResult(state, fallbackResult, result, visiblePayloads, reason, startedAt)
				if r.Tasks != nil && !result.Queued {
					_ = r.Tasks.MarkRunFinished(req.TaskID, result, nil, time.Time{}) // best-effort; failure is non-critical
				}
				return result, nil
			}

			// ---- Retry: transient HTTP ----
			if reason == backend.BackendFailureTransientHTTP && !didRetryTransientHTTP {
				didRetryTransientHTTP = true
				dispatcher = delivery.NewReplyDispatcher(r.Deliverer, target)
				runCtx.ReplyDispatcher = dispatcher
				state.Dispatcher = dispatcher
				state.AgentRunCtx = runCtx
				continue
			}

			// ---- Retry: context overflow via compaction ----
			if reason == backend.BackendFailureContextOverflow && !didRetryCompaction {
				if retried := p.tryCompactionRetry(state, &runCtx, &dispatcher, target); retried {
					didRetryCompaction = true
					state.Dispatcher = dispatcher
					state.AgentRunCtx = runCtx
					continue
				}
			}

			// ---- Unrecoverable: attempt session reset for specific errors ----
			if resetResult, ok := p.trySessionReset(state, reason); ok {
				return resetResult, nil
			}

			// Subagent lifecycle on failure.
			if req.Lane == core.LaneSubagent && strings.TrimSpace(req.SpawnedBy) != "" {
				r.handleSubagentLifecycleCompletion(context.Background(), req, core.AgentRunResult{}, err)
			}
			if r.Tasks != nil {
				_ = r.Tasks.MarkRunFinished(req.TaskID, core.AgentRunResult{}, err, time.Time{}) // best-effort; failure is non-critical
			}
			return core.AgentRunResult{}, err
		}

		// ---- Success path ----
		// NOTE: result.Events were already emitted in real-time by the
		// agentEventBuilder during streaming. Do NOT re-emit them here
		// to avoid duplicate SSE events reaching subscribers.
		result.RunID = req.RunID
		result.StartedAt = startedAt
		result.FinishedAt = time.Now().UTC()

		entry := p.buildSessionEntry(state, fallbackResult, result)
		backend.ApplySessionState(&entry, fallbackResult.Provider, result)
		if err := r.Sessions.Upsert(sess.SessionKey, entry); err != nil {
			return core.AgentRunResult{}, err
		}

		transcript := delivery.TranscriptMessagesForPersistence(req.Message, startedAt, dispatcher, result.Payloads, req.IsHeartbeat, req.RunID)
		if err := r.Sessions.AppendTranscript(sess.SessionKey, sess.SessionID, transcript...); err != nil {
			return core.AgentRunResult{}, err
		}

		// Subagent lifecycle on success.
		if req.Lane == core.LaneSubagent && strings.TrimSpace(req.SpawnedBy) != "" {
			r.handleSubagentLifecycleCompletion(context.Background(), req, result, nil)
		} else {
			if r.Subagents != nil {
				r.Subagents.ReleaseDeferredAnnouncementsForRequester(sess.SessionKey)
			}
			_ = r.flushSubagentAnnouncements(context.Background(), sess.SessionKey) // best-effort; failure is non-critical
			if r.Subagents != nil {
				r.Subagents.SweepOrphans(r.Sessions, r.ActiveRuns)
			}
		}

		event.EmitDebugEvent(r.EventHub, sess.SessionKey, req.RunID, "lifecycle", map[string]any{
			"type":         "run_completed",
			"stopReason":   result.StopReason,
			"payloadCount": len(result.Payloads),
		})
		slog.Info("[AgentPipeline] model call success",
			"session", sess.SessionKey,
			"runID", req.RunID,
			"stopReason", result.StopReason,
			"payloadCount", len(result.Payloads),
		)

		if r.Tasks != nil && !result.Queued {
			_ = r.Tasks.MarkRunFinished(req.TaskID, result, nil, time.Time{}) // best-effort; failure is non-critical
		}
		return result, nil
	}
}

func (p *AgentPipeline) runPreemptiveMemoryFlushIfNeeded(ctx context.Context, state *PipelineState) error {
	r := p.runtime
	req := state.Request
	sess := state.Session

	contextWindow := memorypkg.ResolveConfiguredContextWindowTokens(r.Config, state.Selection)
	if contextWindow <= 0 {
		return nil
	}

	threshold := contextWindow - max(0, state.Identity.CompactionReserveTokensFloor) - max(0, state.Identity.MemoryFlushSoftThresholdTokens)
	if threshold <= 0 {
		return nil
	}
	if !sessionpkg.ShouldRunPreCompactionMemoryFlush(state.Identity, sess) {
		return nil
	}

	estimatedContextTokens := memorypkg.EstimatePreRunContextTokens(
		state.Session.Entry,
		state.Transcript,
		state.AgentRunCtx.SystemPrompt,
		state.Request.Message,
	)
	if estimatedContextTokens <= 0 || estimatedContextTokens < threshold {
		return nil
	}

	event.EmitDebugEvent(r.EventHub, sess.SessionKey, req.RunID, "memory_flush", map[string]any{
		"type":               "memory_flush_threshold_triggered",
		"estimatedTokens":    estimatedContextTokens,
		"contextWindow":      contextWindow,
		"reserveTokensFloor": state.Identity.CompactionReserveTokensFloor,
		"softThreshold":      state.Identity.MemoryFlushSoftThresholdTokens,
		"threshold":          threshold,
	})

	return p.runPreCompactionMemoryFlush(ctx, state, state.Transcript)
}

func (p *AgentPipeline) runPreCompactionMemoryFlush(_ context.Context, state *PipelineState, history []core.TranscriptMessage) error {
	r := p.runtime
	req := state.Request
	sess := state.Session

	adapter := &compactionRunnerAdapter{rt: r}
	flushSystemPrompt := infra.BuildSystemPrompt(buildPromptParams(state, req, history))
	flushParams := sessionpkg.MemoryFlushTurnParams{
		RunID:             req.RunID + ":memory-flush",
		AgentID:           req.AgentID,
		Channel:           req.Channel,
		To:                req.To,
		AccountID:         req.AccountID,
		ThreadID:          req.ThreadID,
		Timeout:           req.Timeout,
		Session:           sess,
		Identity:          state.Identity,
		Selection:         state.Selection,
		History:           history,
		MemoryHits:        state.MemoryHits,
		Skills:            state.Skills,
		InternalEvents:    state.InternalEvents,
		BootstrapWarnings: state.BootstrapWarnings,
		SystemPrompt:      flushSystemPrompt,
		Prompt:            preCompactionMemoryFlushPrompt(state.Identity),
		WorkspaceDir:      state.Identity.WorkspaceDir,
	}
	return sessionpkg.RunPreCompactionMemoryFlush(r.Sessions, adapter, state.runCtxBase, flushParams)
}

// buildPartialSuccessResult constructs a result from already-streamed
// payloads when the model call fails mid-stream.
func (p *AgentPipeline) buildPartialSuccessResult(
	state *PipelineState,
	fallbackResult backend.ModelFallbackResult,
	result core.AgentRunResult,
	visiblePayloads []core.ReplyPayload,
	reason backend.BackendFailureReason,
	startedAt time.Time,
) core.AgentRunResult {
	r := p.runtime
	req := state.Request
	sess := state.Session

	result.RunID = req.RunID
	result.StartedAt = startedAt
	result.FinishedAt = time.Now().UTC()
	result.Payloads = visiblePayloads
	result.StopReason = utils.NonEmpty(result.StopReason, string(reason))

	entry := p.buildSessionEntry(state, fallbackResult, result)
	backend.ApplySessionState(&entry, fallbackResult.Provider, result)
	if upsertErr := r.Sessions.Upsert(sess.SessionKey, entry); upsertErr == nil {
		transcript := delivery.TranscriptMessagesForPersistence(req.Message, startedAt, state.Dispatcher, result.Payloads, req.IsHeartbeat, req.RunID)
		_ = r.Sessions.AppendTranscript(sess.SessionKey, sess.SessionID, transcript...) // best-effort; failure is non-critical
	}

	return result
}

// buildSessionEntry constructs a SessionEntry from the current pipeline state.
func (p *AgentPipeline) buildSessionEntry(
	state *PipelineState,
	fallbackResult backend.ModelFallbackResult,
	result core.AgentRunResult,
) core.SessionEntry {
	req := state.Request
	sess := state.Session

	return core.SessionEntry{
		SessionID:          sess.SessionID,
		ThinkingLevel:      utils.NonEmpty(req.Thinking, sess.PersistedThink),
		VerboseLevel:       utils.NonEmpty(req.Verbose, sess.PersistedVerbose),
		ProviderOverride:   utils.NonEmpty(req.SessionProviderOverride, fallbackResult.Provider),
		ModelOverride:      utils.NonEmpty(req.SessionModelOverride, fallbackResult.Model),
		Usage:              result.Usage,
		ContextTokens:      memorypkg.DeriveContextTokensFromUsage(result.Usage),
		ActiveProvider:     fallbackResult.Provider,
		ActiveModel:        fallbackResult.Model,
		LastModelCallAt:    result.FinishedAt,
		LastActivityReason: "turn",
		SpawnedBy:          req.SpawnedBy,
		SpawnDepth:         req.SpawnDepth,
		LastChannel:        req.Channel,
		LastTo:             req.To,
		LastAccountID:      req.AccountID,
		LastThreadID:       req.ThreadID,
		DeliveryContext:    &core.DeliveryContext{Channel: req.Channel, To: req.To, AccountID: req.AccountID, ThreadID: req.ThreadID},
		SkillsSnapshot:     state.Skills,
	}
}

// tryCompactionRetry attempts to compact the session and retry the model
// call when a context-overflow error occurs.
func (p *AgentPipeline) tryCompactionRetry(
	state *PipelineState,
	runCtx *rtypes.AgentRunContext,
	dispatcher **delivery.ReplyDispatcher,
	target core.DeliveryTarget,
) bool {
	r := p.runtime
	req := state.Request
	sess := state.Session

	currentHistory, loadErr := r.Sessions.LoadTranscript(sess.SessionKey)
	if loadErr != nil {
		return false
	}
	currentHistory = backend.SanitizeTranscriptForOpenAI(currentHistory)
	if flushErr := p.runPreCompactionMemoryFlush(state.runCtxBase, state, currentHistory); flushErr != nil {
		event.EmitDebugEvent(r.EventHub, sess.SessionKey, req.RunID, "memory_flush", map[string]any{
			"type":  "memory_flush_failed",
			"error": flushErr.Error(),
		})
	}

	// Compact the session via session.CompactTranscript.
	adapter := &compactionRunnerAdapter{rt: r}
	plan := sessionpkg.PrepareCompactionPlan(currentHistory, sessionpkg.DefaultCompactionKeepRecentMessages, sessionpkg.AutoCompactionInstructions)
	compactPrompt := buildCompactionPrompt(plan.Summarizable, sessionpkg.AutoCompactionInstructions)
	selection, selErr := backend.ResolveModelSelection(state.runCtxBase, state.Identity, req, sess)
	if selErr != nil {
		return false
	}
	if _, compactErr := sessionpkg.CompactTranscript(
		r.Sessions, adapter, state.runCtxBase, req, sess, state.Identity, selection,
		plan.Summarizable, plan.Kept, sessionpkg.AutoCompactionInstructions, compactPrompt,
	); compactErr != nil {
		// Compaction failed — fall through to session reset.
		return false
	}

	// Reload the compacted history and rebuild the system prompt.
	reloadedHistory, reloadErr := r.Sessions.LoadTranscript(sess.SessionKey)
	if reloadErr != nil {
		return false
	}
	reloadedHistory = backend.SanitizeTranscriptForOpenAI(reloadedHistory)
	runCtx.Transcript = reloadedHistory
	runCtx.SystemPrompt = infra.BuildSystemPrompt(buildPromptParams(state, req, reloadedHistory))
	*dispatcher = delivery.NewReplyDispatcher(r.Deliverer, target)
	runCtx.ReplyDispatcher = *dispatcher

	event.EmitDebugEvent(r.EventHub, sess.SessionKey, req.RunID, "compaction", map[string]any{
		"type":   "auto_compaction_complete",
		"reason": "overflow",
	})
	return true
}

// trySessionReset attempts a session reset for specific backend failure
// reasons (context overflow after failed compaction, role ordering, corrupt).
func (p *AgentPipeline) trySessionReset(
	state *PipelineState,
	reason backend.BackendFailureReason,
) (core.AgentRunResult, bool) {
	r := p.runtime
	req := state.Request
	sess := state.Session
	plan, ok := sessionpkg.ResolveRecoveryResetPlan(string(reason))
	if !ok {
		return core.AgentRunResult{}, false
	}
	result, handled, err := sessionpkg.ExecuteRecoveryReset(r.Sessions, sess.SessionKey, req.RunID, plan)
	if err == nil && handled {
		return result, true
	}
	return core.AgentRunResult{}, false
}
