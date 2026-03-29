// runtime_compaction.go — session compaction and reset command handlers.
//
// Per RUNTIME_SLIM_PLAN P2-1:
//   - The core compaction algorithm lives in internal/session/compaction.go.
//   - This file contains:
//   - compactionRunnerAdapter — implements session.CompactionRunner and
//     bridges session/ to the runtime's backend registry and infra helpers.
//   - handleSessionResetCommand / handleSessionCompactionCommand — thin
//     orchestrators that stay in runtime because they call r.Run().
//   - Pure command-matching helpers.
//   - Prompt-building helpers that use infra (can't live in session/).
package runtime

import (
	"context"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/backend"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/event"
	hookspkg "github.com/kocort/kocort/internal/hooks"
	"github.com/kocort/kocort/internal/infra"
	memorypkg "github.com/kocort/kocort/internal/memory"
	"github.com/kocort/kocort/internal/rtypes"
	sessionpkg "github.com/kocort/kocort/internal/session"
)

// ---------------------------------------------------------------------------
// compactionRunnerAdapter — implements session.CompactionRunner
// ---------------------------------------------------------------------------

// compactionRunnerAdapter bridges session.CompactionRunner to the Runtime's
// backend registry, dispatcher machinery, and event emitter.  It holds a
// pointer to the Runtime so it can call resolveBackend and emitDebugEvent
// without those methods becoming part of the public Runtime surface.
type compactionRunnerAdapter struct {
	rt *Runtime
}

// RunCompactionTurn resolves the backend and runs a single LLM compaction turn.
// It returns the extracted summary text, or the fallback summary when the LLM
// returns an empty response.
func (a *compactionRunnerAdapter) RunCompactionTurn(ctx context.Context, params sessionpkg.CompactionTurnParams) (string, error) {
	deliverer := &delivery.MemoryDeliverer{}
	dispatcher := delivery.NewReplyDispatcher(deliverer, core.DeliveryTarget{SessionKey: params.Session.SessionKey})
	defer func() {
		dispatcher.MarkComplete()
		_ = dispatcher.WaitForIdle(context.Background()) // best-effort; failure is non-critical
	}()

	runCtx := rtypes.AgentRunContext{
		Runtime: a.rt,
		Request: core.AgentRunRequest{
			RunID:    params.RunID,
			AgentID:  params.AgentID,
			Message:  params.Prompt,
			Timeout:  params.Timeout,
			Deliver:  false,
			Channel:  params.Channel,
			To:       params.To,
			ThreadID: params.ThreadID,
		},
		Session:         params.Session,
		Identity:        params.Identity,
		ModelSelection:  params.Selection,
		Transcript:      nil,
		Memory:          nil,
		Skills:          nil,
		AvailableTools:  nil,
		SystemPrompt:    params.SystemPrompt,
		WorkspaceDir:    params.WorkspaceDir,
		ReplyDispatcher: dispatcher,
		RunState:        &core.AgentRunState{},
	}

	resolvedBackend, _, err := backend.ResolveBackendForRun(a.rt.Backends, a.rt.Backend, runCtx.Identity, runCtx.ModelSelection)
	if err != nil {
		return "", err
	}

	result, err := resolvedBackend.Run(ctx, runCtx)
	if err != nil {
		return "", err
	}

	summary := extractCompactionSummary(result, dispatcher)
	if strings.TrimSpace(summary) == "" {
		// Fall back to infra-formatted summary using the summarizable history
		// (params.Summarizable was set by the caller before invoking CompactTranscript).
		return fallbackCompactionSummary(params.Summarizable), nil
	}
	return summary, nil
}

// RunMemoryFlushTurn resolves the backend and runs a memory flush LLM turn.
// System prompt and user prompt are pre-built by the caller (pipeline_execute.go)
// to avoid importing infra from the session package.
func (a *compactionRunnerAdapter) RunMemoryFlushTurn(ctx context.Context, params sessionpkg.MemoryFlushTurnParams) error {
	r := a.rt

	flushReq := core.AgentRunRequest{
		RunID:             params.RunID,
		AgentID:           params.AgentID,
		Message:           params.Prompt,
		Timeout:           params.Timeout,
		Deliver:           false,
		IsMaintenance:     true,
		Channel:           params.Channel,
		To:                params.To,
		AccountID:         params.AccountID,
		ThreadID:          params.ThreadID,
		ExtraSystemPrompt: params.SystemPrompt,
	}

	dispatcher := delivery.NewReplyDispatcher(&delivery.MemoryDeliverer{}, core.DeliveryTarget{
		SessionKey:           params.Session.SessionKey,
		SkipTranscriptMirror: true,
	})
	defer func() {
		dispatcher.MarkComplete()
		_ = dispatcher.WaitForIdle(context.Background()) // best-effort; failure is non-critical
	}()

	runCtx := rtypes.AgentRunContext{
		Runtime:         r,
		Request:         flushReq,
		Session:         params.Session,
		Identity:        params.Identity,
		ModelSelection:  params.Selection,
		Transcript:      params.History,
		Memory:          params.MemoryHits,
		Skills:          params.Skills,
		AvailableTools:  nil, // memory flush uses the live tool registry via Runtime
		SystemPrompt:    params.SystemPrompt,
		WorkspaceDir:    params.WorkspaceDir,
		ReplyDispatcher: dispatcher,
		RunState:        &core.AgentRunState{},
	}

	resolvedBackend, _, err := backend.ResolveBackendForRun(r.Backends, r.Backend, runCtx.Identity, runCtx.ModelSelection)
	if err != nil {
		return err
	}
	_, err = resolvedBackend.Run(ctx, runCtx)
	return err
}

// EmitDebugEvent satisfies session.CompactionRunner by forwarding to
// the Runtime's EventBus.
func (a *compactionRunnerAdapter) EmitDebugEvent(sessionKey, runID, stream string, data map[string]any) {
	event.EmitDebugEvent(a.rt.EventHub, sessionKey, runID, stream, data)
}

// ---------------------------------------------------------------------------
// Command handlers (orchestrators — stay in runtime because they call r.Run)
// ---------------------------------------------------------------------------

// handleSessionResetCommand processes a session reset command.
func (r *Runtime) handleSessionResetCommand(
	ctx context.Context,
	req core.AgentRunRequest,
	command sessionpkg.SessionResetCommandMatch,
	session core.SessionResolution,
	identity core.AgentIdentity,
) (bool, core.AgentRunResult, error) {
	effectiveSession := sessionpkg.ResolveEffectiveACPResetSession(r.Sessions, session, sessionpkg.BoundSessionLookupOptions{
		Channel:   req.Channel,
		To:        req.To,
		AccountID: req.AccountID,
		ThreadID:  req.ThreadID,
	})
	execution, err := sessionpkg.ExecuteSessionReset(r.newACPResetLifecycleStore(), effectiveSession, command)
	if err != nil {
		return true, core.AgentRunResult{}, err
	}
	if archiveErr := memorypkg.ArchiveSessionToMemory(identity.WorkspaceDir, effectiveSession, execution.History, command.Reason, time.Now().UTC()); archiveErr != nil {
		event.EmitDebugEvent(r.EventHub, req.SessionKey, req.RunID, "memory_flush", map[string]any{
			"type":  "session_memory_archive_failed",
			"error": archiveErr.Error(),
		})
	}
	event.EmitDebugEvent(r.EventHub, req.SessionKey, req.RunID, "lifecycle", map[string]any{
		"type":          "session_reset",
		"reason":        command.Reason,
		"nextSessionId": execution.NextSessionID,
	})
	// Fire command:reset or command:new hook.
	if r.InternalHooks != nil {
		action := command.Reason // "reset" or "new"
		r.InternalHooks.Trigger(ctx, hookspkg.NewEvent(hookspkg.EventCommand, action, effectiveSession.SessionKey, map[string]any{
			"trigger":           command.Trigger,
			"reason":            command.Reason,
			"previousSessionId": effectiveSession.SessionID,
			"nextSessionId":     execution.NextSessionID,
		}))
	}
	req.SessionKey = effectiveSession.SessionKey
	req.SessionID = execution.NextSessionID
	if !execution.HasFollowup() {
		return true, execution.ImmediateResult(req.RunID), nil
	}
	req.Message = execution.FollowupMessage()
	result, err := r.Run(ctx, req)
	return true, result, err
}

// handleSessionCompactionCommand processes a manual /compact command.
func (r *Runtime) handleSessionCompactionCommand(
	ctx context.Context,
	req core.AgentRunRequest,
	command sessionpkg.SessionCompactCommandMatch,
	session core.SessionResolution,
	identity core.AgentIdentity,
) (bool, core.AgentRunResult, error) {
	history, err := r.Sessions.LoadTranscript(session.SessionKey)
	if err != nil {
		return true, core.AgentRunResult{}, err
	}
	history = backend.SanitizeTranscriptForOpenAI(history)
	plan := sessionpkg.PrepareCompactionPlan(history, sessionpkg.DefaultCompactionKeepRecentMessages, command.Instructions)
	if plan.IsEmpty() {
		return true, plan.EmptyResult(req.RunID), nil
	}

	selection, err := backend.ResolveModelSelection(ctx, identity, req, session)
	if err != nil {
		return true, core.AgentRunResult{}, err
	}

	prompt := buildCompactionPrompt(plan.Summarizable, command.Instructions)

	adapter := &compactionRunnerAdapter{rt: r}
	compaction, err := sessionpkg.CompactTranscript(
		r.Sessions, adapter, ctx, req, session, identity, selection,
		plan.Summarizable, plan.Kept, command.Instructions, prompt,
	)
	if err != nil {
		return true, core.AgentRunResult{}, err
	}

	event.EmitDebugEvent(r.EventHub, session.SessionKey, req.RunID, "compaction", map[string]any{
		"type":             "manual_compaction_complete",
		"compactionCount":  compaction.CompactionCount,
		"keptMessageCount": compaction.KeptCount,
	})
	// Fire command:compact hook.
	if r.InternalHooks != nil {
		r.InternalHooks.Trigger(ctx, hookspkg.NewEvent(hookspkg.EventCommand, "compact", session.SessionKey, map[string]any{
			"instructions":    command.Instructions,
			"compactionCount": compaction.CompactionCount,
		}))
	}
	return true, plan.SuccessResult(req.RunID, compaction), nil
}

// ---------------------------------------------------------------------------
// Prompt / summary helpers
// (These use infra, which is why they stay in runtime rather than session.)
// ---------------------------------------------------------------------------

// buildCompactionPrompt builds the LLM user prompt for a compaction turn.
func buildCompactionPrompt(history []core.TranscriptMessage, instructions string) string {
	lines := []string{
		"Compact the older session history below into a durable summary for future turns.",
		"Keep commitments, decisions, constraints, task state, and important tool outcomes.",
	}
	if strings.TrimSpace(instructions) != "" {
		lines = append(lines, "Additional instructions:", strings.TrimSpace(instructions))
	}
	lines = append(lines, "", infra.BuildTranscriptPromptSection(history))
	return strings.Join(lines, "\n")
}

// extractCompactionSummary extracts the summary text from a compaction run result.
func extractCompactionSummary(result core.AgentRunResult, dispatcher *delivery.ReplyDispatcher) string {
	for i := len(result.Payloads) - 1; i >= 0; i-- {
		if text := strings.TrimSpace(result.Payloads[i].Text); text != "" {
			return text
		}
	}
	dispatcherPayloads := delivery.VisibleAssistantPayloads(dispatcher)
	for i := len(dispatcherPayloads) - 1; i >= 0; i-- {
		if text := strings.TrimSpace(dispatcherPayloads[i].Text); text != "" {
			return text
		}
	}
	return ""
}

// fallbackCompactionSummary generates a fallback summary using infra formatting.
func fallbackCompactionSummary(history []core.TranscriptMessage) string {
	lines := []string{"Summary of earlier conversation:"}
	for _, msg := range history {
		line := infra.FormatTranscriptPromptLine(msg)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// Memory-flush prompt helpers
// (Used in pipeline_execute.go when building MemoryFlushTurnParams.)
// ---------------------------------------------------------------------------

// preCompactionMemoryFlushSystemPrompt returns the system prompt for a memory flush.
func preCompactionMemoryFlushSystemPrompt(identity core.AgentIdentity) string {
	if text := strings.TrimSpace(identity.MemoryFlushSystemPrompt); text != "" {
		return text
	}
	return "Session nearing compaction. Store durable memories now."
}

// preCompactionMemoryFlushPrompt returns the user prompt for a memory flush.
func preCompactionMemoryFlushPrompt(identity core.AgentIdentity) string {
	if text := strings.TrimSpace(identity.MemoryFlushPrompt); text != "" {
		return text
	}
	return "Write any lasting notes to memory/YYYY-MM-DD.md or MEMORY.md using available tools. Reply with NO_REPLY if nothing should be stored."
}
