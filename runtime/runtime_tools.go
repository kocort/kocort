package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/cerebellum"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/event"
	hookspkg "github.com/kocort/kocort/internal/hooks"
	pluginpkg "github.com/kocort/kocort/internal/plugin"
	"github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/internal/sandbox"
	sessionpkg "github.com/kocort/kocort/internal/session"
	toolfn "github.com/kocort/kocort/internal/tool"

	"github.com/kocort/kocort/utils"
)

func (r *Runtime) ExecuteTool(ctx context.Context, runCtx rtypes.AgentRunContext, name string, args map[string]any) (core.ToolResult, error) {
	if r.Tools == nil {
		return core.ToolResult{}, core.ErrToolRegistryNotConfigured
	}
	tool := r.Tools.Get(name)
	if tool == nil {
		return core.ToolResult{}, fmt.Errorf("%w: %q", core.ErrToolNotFound, name)
	}
	toolCallID, sanitizedArgs := toolfn.ExtractReservedToolRuntimeArgs(args)
	meta := r.Tools.Meta(tool.Name())
	if !toolfn.IsToolAllowedByIdentity(runCtx.Identity, runCtx, meta, tool.Name()) {
		event.RecordAudit(ctx, r.Audit, r.Logger, core.AuditEvent{
			Category:   core.AuditCategorySandbox,
			Type:       "tool_denied",
			Level:      "warn",
			AgentID:    runCtx.Identity.ID,
			SessionKey: runCtx.Session.SessionKey,
			RunID:      runCtx.Request.RunID,
			ToolName:   tool.Name(),
			Message:    "tool blocked by identity policy",
		})
		return core.ToolResult{}, fmt.Errorf("tool %q is not allowed for agent %q", tool.Name(), runCtx.Identity.ID)
	}
	if !toolfn.IsToolExecutionAllowedByIdentity(runCtx.Identity, runCtx, meta) {
		event.RecordAudit(ctx, r.Audit, r.Logger, core.AuditEvent{
			Category:   core.AuditCategorySandbox,
			Type:       "tool_denied",
			Level:      "warn",
			AgentID:    runCtx.Identity.ID,
			SessionKey: runCtx.Session.SessionKey,
			RunID:      runCtx.Request.RunID,
			ToolName:   tool.Name(),
			Message:    "tool blocked by elevated or sandbox identity policy",
		})
		return core.ToolResult{}, fmt.Errorf("tool %q is blocked by elevated/sandbox policy", tool.Name())
	}
	if meta.PluginID != "" && !pluginpkg.PluginToolExecutableByConfig(r.Config.Plugins, meta.PluginID) {
		return core.ToolResult{}, fmt.Errorf("tool %q is blocked because plugin %q is disabled", tool.Name(), meta.PluginID)
	}
	sandboxCtx, err := sandbox.ResolveSandboxContext(ctx, r, runCtx)
	if err != nil {
		return core.ToolResult{}, err
	}
	if !toolfn.IsToolAllowedInSandbox(runCtx.Identity, runCtx, meta, tool.Name(), sandboxCtx) {
		event.RecordAudit(ctx, r.Audit, r.Logger, core.AuditEvent{
			Category:   core.AuditCategorySandbox,
			Type:       "tool_denied",
			Level:      "warn",
			AgentID:    runCtx.Identity.ID,
			SessionKey: runCtx.Session.SessionKey,
			RunID:      runCtx.Request.RunID,
			ToolName:   tool.Name(),
			Message:    "tool blocked by sandbox context",
		})
		return core.ToolResult{}, fmt.Errorf("tool %q is blocked by sandbox context", tool.Name())
	}
	if meta.Elevated && r.Approvals != nil {
		decision, approveErr := r.Approvals.ApproveToolExecution(ctx, toolfn.BuildToolApprovalRequest(runCtx, meta, tool.Name(), sandboxCtx))
		if approveErr != nil {
			return core.ToolResult{}, approveErr
		}
		if !decision.Allowed {
			reason := strings.TrimSpace(decision.Reason)
			if reason == "" {
				reason = "approval denied"
			}
			return core.ToolResult{}, fmt.Errorf("tool %q requires approval: %s", tool.Name(), reason)
		}
	}
	loopState := (*toolfn.ToolLoopSessionState)(nil)
	if r.ToolLoops != nil {
		loopState = r.ToolLoops.Get(runCtx.Session.SessionKey, runCtx.Session.SessionID)
	}
	loopResult := toolfn.DetectToolCallLoop(loopState, tool.Name(), sanitizedArgs, runCtx.Identity.ToolLoopDetection)
	if loopResult.Stuck {
		event.RecordAudit(ctx, r.Audit, r.Logger, core.AuditEvent{
			Category:   core.AuditCategoryTool,
			Type:       "tool_loop_detected",
			Level:      loopResult.Level,
			AgentID:    runCtx.Identity.ID,
			SessionKey: runCtx.Session.SessionKey,
			RunID:      runCtx.Request.RunID,
			ToolName:   tool.Name(),
			Message:    loopResult.Message,
			Data: map[string]any{
				"detector":       string(loopResult.Detector),
				"count":          loopResult.Count,
				"pairedToolName": loopResult.PairedToolName,
			},
		})
		event.EmitDebugEvent(r.EventHub, runCtx.Session.SessionKey, runCtx.Request.RunID, "tool", map[string]any{
			"type":           "tool_loop_detected",
			"toolName":       tool.Name(),
			"level":          loopResult.Level,
			"detector":       string(loopResult.Detector),
			"count":          loopResult.Count,
			"message":        loopResult.Message,
			"pairedToolName": loopResult.PairedToolName,
		})
		if loopResult.Level == "critical" {
			failure := &core.ToolExecutionFailure{
				ToolName:    tool.Name(),
				Message:     loopResult.Message,
				HistoryText: "ERROR: " + loopResult.Message,
				Recoverable: false,
			}
			if runCtx.RunState != nil {
				runCtx.RunState.LastToolError = failure
			}
			toolfn.RecordToolCallOutcome(loopState, tool.Name(), sanitizedArgs, toolCallID, core.ToolResult{}, failure, runCtx.Identity.ToolLoopDetection)
			return core.ToolResult{}, failure
		}
		if toolfn.ShouldEmitToolLoopWarning(loopState, loopResult.WarningKey, loopResult.Count) {
			event.RecordAudit(ctx, r.Audit, r.Logger, core.AuditEvent{
				Category:   core.AuditCategoryTool,
				Type:       "tool_loop_warning",
				Level:      "warn",
				AgentID:    runCtx.Identity.ID,
				SessionKey: runCtx.Session.SessionKey,
				RunID:      runCtx.Request.RunID,
				ToolName:   tool.Name(),
				Message:    loopResult.Message,
				Data: map[string]any{
					"detector":       string(loopResult.Detector),
					"count":          loopResult.Count,
					"pairedToolName": loopResult.PairedToolName,
				},
			})
		}
	}
	toolfn.RecordToolCall(loopState, tool.Name(), sanitizedArgs, toolCallID, runCtx.Identity.ToolLoopDetection)

	// ── Cerebellum safety review (agent-integrated) ──────────────────────
	// After all static rule checks, perform semantic safety review via the
	// local cerebellum before executing the tool.
	// In the tool execution pipeline, we are always in usage mode (not config
	// mode), because tool calls only occur during normal agent operation.
	const isConfigMode = false
	if !r.Config.BrainLocalEnabled() && r.Cerebellum != nil && cerebellum.ShouldReviewToolCall(tool.Name(), sanitizedArgs, meta.Elevated, isConfigMode) {
		// Emit: cerebellum review started
		event.EmitDebugEvent(r.EventHub, runCtx.Session.SessionKey, runCtx.Request.RunID, "tool", map[string]any{
			"type":       "cerebellum_review_started",
			"toolName":   tool.Name(),
			"toolCallId": toolCallID,
			"args":       utils.CloneAnyMap(sanitizedArgs),
		})

		reviewResult, _ := r.Cerebellum.ReviewToolCall(cerebellum.ToolCallReviewRequest{
			UserMessage: runCtx.Request.Message,
			ToolName:    tool.Name(),
			ToolParams:  sanitizedArgs,
			SessionKey:  runCtx.Session.SessionKey,
			AgentID:     runCtx.Identity.ID,
		})
		switch reviewResult.Verdict {
		case "reject":
			event.RecordAudit(ctx, r.Audit, r.Logger, core.AuditEvent{
				Category:   core.AuditCategoryCerebellum,
				Type:       "tool_review_rejected",
				Level:      "warn",
				AgentID:    runCtx.Identity.ID,
				SessionKey: runCtx.Session.SessionKey,
				RunID:      runCtx.Request.RunID,
				ToolName:   tool.Name(),
				Message:    "cerebellum safety review rejected tool call: " + reviewResult.Reason,
				Data: map[string]any{
					"verdict": reviewResult.Verdict,
					"reason":  reviewResult.Reason,
					"risk":    reviewResult.Risk,
				},
			})
			// Emit: cerebellum review completed (rejected)
			event.EmitDebugEvent(r.EventHub, runCtx.Session.SessionKey, runCtx.Request.RunID, "tool", map[string]any{
				"type":       "cerebellum_review_completed",
				"toolName":   tool.Name(),
				"toolCallId": toolCallID,
				"verdict":    reviewResult.Verdict,
				"reason":     reviewResult.Reason,
				"risk":       reviewResult.Risk,
				"args":       utils.CloneAnyMap(sanitizedArgs),
			})
			return core.ToolResult{}, &core.ToolExecutionFailure{
				ToolName:    tool.Name(),
				Message:     fmt.Sprintf("safety review rejected: %s", reviewResult.Reason),
				HistoryText: "ERROR: safety review rejected: " + reviewResult.Reason,
				Recoverable: false,
			}
		case "flag":
			event.RecordAudit(ctx, r.Audit, r.Logger, core.AuditEvent{
				Category:   core.AuditCategoryCerebellum,
				Type:       "tool_review_flagged",
				Level:      "warn",
				AgentID:    runCtx.Identity.ID,
				SessionKey: runCtx.Session.SessionKey,
				RunID:      runCtx.Request.RunID,
				ToolName:   tool.Name(),
				Message:    "cerebellum flagged tool call: " + reviewResult.Reason,
				Data: map[string]any{
					"verdict": reviewResult.Verdict,
					"reason":  reviewResult.Reason,
					"risk":    reviewResult.Risk,
				},
			})
			// Emit: cerebellum review completed (flagged)
			event.EmitDebugEvent(r.EventHub, runCtx.Session.SessionKey, runCtx.Request.RunID, "tool", map[string]any{
				"type":       "cerebellum_review_completed",
				"toolName":   tool.Name(),
				"toolCallId": toolCallID,
				"verdict":    reviewResult.Verdict,
				"reason":     reviewResult.Reason,
				"risk":       reviewResult.Risk,
				"args":       utils.CloneAnyMap(sanitizedArgs),
			})
		default: // "approve"
			event.RecordAudit(ctx, r.Audit, r.Logger, core.AuditEvent{
				Category:   core.AuditCategoryCerebellum,
				Type:       "tool_review_approved",
				Level:      "info",
				AgentID:    runCtx.Identity.ID,
				SessionKey: runCtx.Session.SessionKey,
				RunID:      runCtx.Request.RunID,
				ToolName:   tool.Name(),
				Message:    "cerebellum approved tool call",
			})
			// Emit: cerebellum review completed (approved)
			event.EmitDebugEvent(r.EventHub, runCtx.Session.SessionKey, runCtx.Request.RunID, "tool", map[string]any{
				"type":       "cerebellum_review_completed",
				"toolName":   tool.Name(),
				"toolCallId": toolCallID,
				"verdict":    reviewResult.Verdict,
				"reason":     reviewResult.Reason,
				"risk":       reviewResult.Risk,
				"args":       utils.CloneAnyMap(sanitizedArgs),
			})
		}
	} else {
		// Emit: cerebellum review skipped (conditions not met)
		skipReason := "cerebellum not applicable"
		if r.Config.BrainLocalEnabled() {
			skipReason = "brain local mode enabled"
		} else if r.Cerebellum == nil {
			skipReason = "cerebellum not configured"
		} else {
			skipReason = "low-risk tool or config mode"
		}
		event.EmitDebugEvent(r.EventHub, runCtx.Session.SessionKey, runCtx.Request.RunID, "tool", map[string]any{
			"type":       "cerebellum_review_skipped",
			"toolName":   tool.Name(),
			"toolCallId": toolCallID,
			"reason":     skipReason,
			"args":       utils.CloneAnyMap(sanitizedArgs),
		})
	}

	restorePluginEnv := pluginpkg.ApplyPluginEnvOverrides(r.Config, meta.PluginID)
	defer restorePluginEnv()
	execCtx := ctx
	cancel := func() {}
	if meta.DefaultTimeoutMs > 0 {
		execCtx, cancel = context.WithTimeout(ctx, time.Duration(meta.DefaultTimeoutMs)*time.Millisecond)
	}
	defer cancel()
	event.EmitDebugEvent(r.EventHub, runCtx.Session.SessionKey, runCtx.Request.RunID, "tool", map[string]any{
		"type":     "tool_execute_started",
		"toolName": tool.Name(),
		"args":     utils.CloneAnyMap(sanitizedArgs),
	})
	event.RecordAudit(ctx, r.Audit, r.Logger, core.AuditEvent{
		Category:   core.AuditCategoryTool,
		Type:       "tool_execute_started",
		Level:      "info",
		AgentID:    runCtx.Identity.ID,
		SessionKey: runCtx.Session.SessionKey,
		RunID:      runCtx.Request.RunID,
		ToolName:   tool.Name(),
		Message:    "tool execution started",
		Data:       map[string]any{"args": utils.CloneAnyMap(sanitizedArgs)},
	})
	if strings.TrimSpace(toolCallID) == "" {
		toolCallID = fmt.Sprintf(
			"call_%s_%d",
			strings.ReplaceAll(strings.ToLower(strings.TrimSpace(tool.Name())), " ", "_"),
			time.Now().UTC().UnixNano(),
		)
	}
	appendToolTranscript(r.Sessions, runCtx, tool.Name(), sanitizedArgs, core.TranscriptMessage{
		Type:       "tool_call",
		Role:       "assistant",
		ToolCallID: toolCallID,
		ToolName:   tool.Name(),
		Args:       utils.CloneAnyMap(sanitizedArgs),
		Timestamp:  time.Now().UTC(),
	})
	result, err := tool.Execute(execCtx, rtypes.ToolContext{
		Runtime: r,
		Run:     runCtx,
		Sandbox: sandboxCtx,
	}, sanitizedArgs)
	if err != nil {
		failureMessage := strings.TrimSpace(err.Error())
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			failureMessage = fmt.Sprintf("tool %q timed out", tool.Name())
		case errors.Is(execCtx.Err(), context.DeadlineExceeded):
			failureMessage = fmt.Sprintf("tool %q timed out", tool.Name())
		case errors.Is(err, context.Canceled):
			failureMessage = fmt.Sprintf("tool %q was canceled", tool.Name())
		case errors.Is(execCtx.Err(), context.Canceled):
			failureMessage = fmt.Sprintf("tool %q was canceled", tool.Name())
		}
		historyText := "ERROR"
		if failureMessage != "" {
			historyText = "ERROR: " + failureMessage
		}
		failure := &core.ToolExecutionFailure{
			ToolName:    tool.Name(),
			Message:     failureMessage,
			VisibleText: "",
			HistoryText: historyText,
			Recoverable: toolfn.IsRecoverableToolFailureMessage(failureMessage),
		}
		if runCtx.RunState != nil {
			runCtx.RunState.LastToolError = failure
		}
		event.RecordAudit(ctx, r.Audit, r.Logger, core.AuditEvent{
			Category:   core.AuditCategoryTool,
			Type:       "tool_execute_failed",
			Level:      "error",
			AgentID:    runCtx.Identity.ID,
			SessionKey: runCtx.Session.SessionKey,
			RunID:      runCtx.Request.RunID,
			ToolName:   tool.Name(),
			Message:    strings.TrimSpace(err.Error()),
		})
		event.EmitDebugEvent(r.EventHub, runCtx.Session.SessionKey, runCtx.Request.RunID, "tool", map[string]any{
			"type":     "tool_execute_failed",
			"toolName": tool.Name(),
			"error":    err.Error(),
		})
		appendToolTranscript(r.Sessions, runCtx, tool.Name(), nil, core.TranscriptMessage{
			Type:       "tool_result",
			Role:       "tool",
			ToolCallID: toolCallID,
			ToolName:   tool.Name(),
			Text:       historyText,
			Timestamp:  time.Now().UTC(),
		})
		toolfn.RecordToolCallOutcome(loopState, tool.Name(), sanitizedArgs, toolCallID, core.ToolResult{}, failure, runCtx.Identity.ToolLoopDetection)

		// ── Fire tool:post_execute hook (failure) ────────────────────
		r.fireToolPostExecuteHook(ctx, runCtx, tool.Name(), nil, err)

		return core.ToolResult{}, failure
	}
	text := toolfn.ResolveToolResultHistoryContent(result)
	visibleText := toolfn.ResolveToolResultText(result)
	action := ""
	if rawAction, ok := sanitizedArgs["action"].(string); ok {
		action = strings.TrimSpace(rawAction)
	}
	if runCtx.RunState != nil &&
		strings.EqualFold(strings.TrimSpace(tool.Name()), "cron") &&
		strings.EqualFold(action, "add") {
		runCtx.RunState.SuccessfulCronAdds++
	}
	event.RecordAudit(ctx, r.Audit, r.Logger, core.AuditEvent{
		Category:   core.AuditCategoryTool,
		Type:       "tool_execute_completed",
		Level:      "info",
		AgentID:    runCtx.Identity.ID,
		SessionKey: runCtx.Session.SessionKey,
		RunID:      runCtx.Request.RunID,
		ToolName:   tool.Name(),
		Message:    "tool execution completed",
		Data:       map[string]any{"text": visibleText},
	})
	appendToolTranscript(r.Sessions, runCtx, tool.Name(), nil, core.TranscriptMessage{
		Type:       "tool_result",
		Role:       "tool",
		ToolCallID: toolCallID,
		ToolName:   tool.Name(),
		Text:       text,
		Timestamp:  time.Now().UTC(),
	})
	event.EmitDebugEvent(r.EventHub, runCtx.Session.SessionKey, runCtx.Request.RunID, "tool", map[string]any{
		"type":     "tool_execute_completed",
		"toolName": tool.Name(),
		"text":     visibleText,
	})
	toolfn.RecordToolCallOutcome(loopState, tool.Name(), sanitizedArgs, toolCallID, result, nil, runCtx.Identity.ToolLoopDetection)

	// ── Fire tool:post_execute hook (success) ────────────────────
	r.fireToolPostExecuteHook(ctx, runCtx, tool.Name(), &result, nil)

	return result, nil
}

// fireToolPostExecuteHook dispatches a tool:post_execute internal hook event.
// This allows skill hooks (e.g. self-improving-agent error-detector) to
// observe tool execution outcomes.
func (r *Runtime) fireToolPostExecuteHook(ctx context.Context, runCtx rtypes.AgentRunContext, toolName string, result *core.ToolResult, toolErr error) {
	if r.InternalHooks == nil || !r.InternalHooks.HasHandlers(hookspkg.EventTool, "post_execute") {
		return
	}
	hookCtx := map[string]any{
		"toolName":   toolName,
		"sessionKey": runCtx.Session.SessionKey,
		"agentId":    runCtx.Identity.ID,
	}
	if toolErr != nil {
		hookCtx["error"] = toolErr.Error()
		hookCtx["success"] = false
	} else {
		hookCtx["success"] = true
	}
	if result != nil {
		hookCtx["resultText"] = toolfn.ResolveToolResultText(*result)
	}
	evt := hookspkg.NewEvent(hookspkg.EventTool, "post_execute", runCtx.Session.SessionKey, hookCtx)
	r.InternalHooks.Trigger(ctx, evt)
}

func appendToolTranscript(sessions *sessionpkg.SessionStore, runCtx rtypes.AgentRunContext, toolName string, args map[string]any, msg core.TranscriptMessage) {
	if sessions == nil || runCtx.Request.IsMaintenance || strings.TrimSpace(runCtx.Session.SessionKey) == "" || strings.TrimSpace(runCtx.Session.SessionID) == "" {
		return
	}
	if strings.TrimSpace(msg.ToolCallID) == "" && strings.TrimSpace(toolName) != "" {
		msg.ToolCallID = fmt.Sprintf(
			"call_%s_%d",
			strings.ReplaceAll(strings.ToLower(strings.TrimSpace(toolName)), " ", "_"),
			time.Now().UTC().UnixNano(),
		)
	}
	if msg.Args == nil && args != nil {
		msg.Args = utils.CloneAnyMap(args)
	}
	_ = sessions.AppendTranscript(runCtx.Session.SessionKey, runCtx.Session.SessionID, msg) // best-effort; failure is non-critical
}
