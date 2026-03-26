package tool

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/task"
)

const (
	// maxSteerMessageChars caps the steer/send message length.
	maxSteerMessageChars = 4000
	// steerRateLimitDuration is the per-(caller,child) steer rate limit.
	steerRateLimitDuration = 2 * time.Second
)

// steerRateLimiter tracks per-pair steer timestamps for rate limiting.
var steerRateLimiter = struct {
	mu    sync.Mutex
	times map[string]time.Time
}{times: map[string]time.Time{}}

type SubagentsTool struct{}

func NewSubagentsTool() *SubagentsTool {
	return &SubagentsTool{}
}

func (t *SubagentsTool) Name() string {
	return "subagents"
}

func (t *SubagentsTool) Description() string {
	return "List, focus, unfocus, steer, or kill sub-agent runs for this requester session."
}

func (t *SubagentsTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{"type": "string"},
				"target": map[string]any{"type": "string"},
				"recentMinutes": map[string]any{
					"type":        "number",
					"description": "Optional recent activity cutoff for list/target resolution.",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "Required for send/steer.",
				},
			},
			"additionalProperties": false,
		},
	}
}

func (t *SubagentsTool) Execute(ctx context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	action, _ := ReadStringParam(args, "action", false) // zero value fallback is intentional
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		action = "list"
	}
	recentMinutes, err := ReadOptionalIntParam(args, "recentMinutes")
	if err != nil {
		return core.ToolResult{}, err
	}
	switch action {
	case "agents":
		return t.agents(ctx, toolCtx)
	case "list":
		return t.list(toolCtx, recentMinutes)
	case "focus":
		target, err := ReadStringParam(args, "target", true)
		if err != nil {
			return core.ToolResult{}, err
		}
		return t.focus(toolCtx, target, recentMinutes)
	case "unfocus":
		target, _ := ReadStringParam(args, "target", false)
		return t.unfocus(toolCtx, target, recentMinutes)
	case "info":
		target, err := ReadStringParam(args, "target", true)
		if err != nil {
			return core.ToolResult{}, err
		}
		return t.info(toolCtx, target, recentMinutes)
	case "log":
		target, err := ReadStringParam(args, "target", true)
		if err != nil {
			return core.ToolResult{}, err
		}
		return t.log(toolCtx, target, recentMinutes)
	case "send":
		target, err := ReadStringParam(args, "target", true)
		if err != nil {
			return core.ToolResult{}, err
		}
		message, err := ReadStringParam(args, "message", true)
		if err != nil {
			return core.ToolResult{}, err
		}
		return t.send(ctx, toolCtx, target, message, false, recentMinutes)
	case "steer":
		target, err := ReadStringParam(args, "target", true)
		if err != nil {
			return core.ToolResult{}, err
		}
		message, err := ReadStringParam(args, "message", true)
		if err != nil {
			return core.ToolResult{}, err
		}
		return t.send(ctx, toolCtx, target, message, true, recentMinutes)
	case "kill":
		target, err := ReadStringParam(args, "target", true)
		if err != nil {
			return core.ToolResult{}, err
		}
		return t.kill(ctx, toolCtx, target, recentMinutes)
	default:
		return JSONResult(map[string]any{
			"status": "error",
			"error":  fmt.Sprintf("unsupported subagents action: %s", action),
		})
	}
}

func (t *SubagentsTool) agents(ctx context.Context, toolCtx ToolContext) (core.ToolResult, error) {
	allowed := append([]string{}, toolCtx.Run.Identity.SubagentAllowAgents...)
	items := make([]map[string]any, 0, len(allowed))
	for _, agentID := range allowed {
		agentID = strings.TrimSpace(agentID)
		if agentID == "" {
			continue
		}
		item := map[string]any{"id": agentID}
		if runtime := toolCtx.Runtime; runtime != nil && runtime.GetIdentities() != nil {
			if identity, err := runtime.GetIdentities().Resolve(ctx, agentID); err == nil {
				if trimmed := strings.TrimSpace(identity.Name); trimmed != "" {
					item["name"] = trimmed
				}
				if trimmed := strings.TrimSpace(identity.DefaultProvider); trimmed != "" {
					item["provider"] = trimmed
				}
				if trimmed := strings.TrimSpace(identity.DefaultModel); trimmed != "" {
					item["model"] = trimmed
				}
				if trimmed := strings.TrimSpace(identity.ToolProfile); trimmed != "" {
					item["toolProfile"] = trimmed
				}
				if trimmed := strings.TrimSpace(identity.WorkspaceDir); trimmed != "" {
					item["workspaceDir"] = trimmed
				}
			}
		}
		items = append(items, item)
	}
	return JSONResult(map[string]any{
		"status": "ok",
		"agents": items,
	})
}

func (t *SubagentsTool) focus(toolCtx ToolContext, target string, recentMinutes int) (core.ToolResult, error) {
	entry, err := resolveSubagentTarget(listSubagentTargets(toolCtx, recentMinutes), target)
	if err != nil {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  err.Error(),
		})
	}
	if entry == nil {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  "unknown subagent target",
		})
	}
	req := toolCtx.Run.Request
	if strings.TrimSpace(req.Channel) == "" || strings.TrimSpace(req.ThreadID) == "" {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  "subagents focus requires an active thread-bound requester context",
		})
	}
	svc := session.NewThreadBindingService(toolCtx.Runtime.GetSessions())
	if !svc.CapabilitiesFor(req.Channel, req.AccountID).SupportsBinding {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  "thread bindings are unavailable for this conversation",
		})
	}
	targetKind := "subagent"
	if entry.Kind == "acp" {
		targetKind = string(session.ThreadBindingTargetKindSession)
	}
	bindInput := session.BindThreadSessionInput{
		TargetSessionKey:    entry.ChildSessionKey,
		RequesterSessionKey: toolCtx.Run.Session.SessionKey,
		TargetKind:          targetKind,
		Placement:           session.ThreadBindingPlacementCurrent,
		Channel:             req.Channel,
		To:                  req.To,
		AccountID:           req.AccountID,
		ThreadID:            req.ThreadID,
		ConversationID:      req.ThreadID,
		Label:               entry.Label,
	}
	currentTarget, found := svc.ResolveThreadSession(session.BoundSessionLookupOptions{
		Channel:   req.Channel,
		To:        req.To,
		AccountID: req.AccountID,
		ThreadID:  req.ThreadID,
	})
	var bindErr error
	if found && strings.TrimSpace(currentTarget) != "" && currentTarget != entry.ChildSessionKey {
		bindErr = svc.RebindThreadSession(bindInput, "focus")
	} else {
		bindErr = svc.BindThreadSession(bindInput)
	}
	if bindErr != nil {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  bindErr.Error(),
		})
	}
	return JSONResult(map[string]any{
		"status":    "ok",
		"action":    "focus",
		"target":    entry.ChildSessionKey,
		"kind":      entry.Kind,
		"threadId":  req.ThreadID,
		"channel":   req.Channel,
		"accountId": req.AccountID,
	})
}

func (t *SubagentsTool) unfocus(toolCtx ToolContext, target string, recentMinutes int) (core.ToolResult, error) {
	req := toolCtx.Run.Request
	if strings.TrimSpace(req.Channel) == "" || strings.TrimSpace(req.ThreadID) == "" {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  "subagents unfocus requires an active thread-bound requester context",
		})
	}
	svc := session.NewThreadBindingService(toolCtx.Runtime.GetSessions())
	var targetKey string
	if strings.TrimSpace(target) != "" {
		entry, err := resolveSubagentTarget(listSubagentTargets(toolCtx, recentMinutes), target)
		if err != nil {
			return JSONResult(map[string]any{
				"status": "error",
				"error":  err.Error(),
			})
		}
		if entry == nil {
			return JSONResult(map[string]any{
				"status": "error",
				"error":  "unknown subagent target",
			})
		}
		targetKey = entry.ChildSessionKey
	} else if boundKey, ok := svc.ResolveThreadSession(session.BoundSessionLookupOptions{
		Channel:   req.Channel,
		To:        req.To,
		AccountID: req.AccountID,
		ThreadID:  req.ThreadID,
	}); ok {
		targetKey = boundKey
	}
	if strings.TrimSpace(targetKey) == "" {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  "no focused subagent is bound to this conversation",
		})
	}
	removed := svc.UnbindTargetSession(targetKey, "unfocused")
	return JSONResult(map[string]any{
		"status":    "ok",
		"action":    "unfocus",
		"target":    targetKey,
		"removed":   removed,
		"threadId":  req.ThreadID,
		"channel":   req.Channel,
		"accountId": req.AccountID,
	})
}

func (t *SubagentsTool) list(toolCtx ToolContext, recentMinutes int) (core.ToolResult, error) {
	targets := listSubagentTargets(toolCtx, recentMinutes)
	type row struct {
		Kind                   string `json:"kind,omitempty"`
		RunID                  string `json:"runId"`
		ChildSessionKey        string `json:"childSessionKey"`
		Requester              string `json:"requester,omitempty"`
		Label                  string `json:"label,omitempty"`
		Task                   string `json:"task"`
		Model                  string `json:"model,omitempty"`
		RuntimeBackend         string `json:"runtimeBackend,omitempty"`
		RuntimeState           string `json:"runtimeState,omitempty"`
		RuntimeMode            string `json:"runtimeMode,omitempty"`
		RuntimeStatusSummary   string `json:"runtimeStatusSummary,omitempty"`
		SteeredFromRunID       string `json:"steeredFromRunId,omitempty"`
		ReplacementRunID       string `json:"replacementRunId,omitempty"`
		Mode                   string `json:"mode,omitempty"`
		ThreadID               string `json:"threadId,omitempty"`
		Status                 string `json:"status"`
		EndedReason            string `json:"endedReason,omitempty"`
		SuppressAnnounceReason string `json:"suppressAnnounceReason,omitempty"`
		AnnounceDeliveryPath   string `json:"announceDeliveryPath,omitempty"`
		CreatedAt              string `json:"createdAt"`
	}
	items := make([]row, 0, len(targets))
	for _, run := range targets {
		items = append(items, row{
			Kind:                   run.Kind,
			RunID:                  run.RunID,
			ChildSessionKey:        run.ChildSessionKey,
			Requester:              run.RequesterDisplayKey,
			Label:                  run.Label,
			Task:                   run.Task,
			Model:                  run.Model,
			RuntimeBackend:         run.RuntimeBackend,
			RuntimeState:           run.RuntimeState,
			RuntimeMode:            run.RuntimeMode,
			RuntimeStatusSummary:   run.RuntimeStatusSummary,
			SteeredFromRunID:       run.SteeredFromRunID,
			ReplacementRunID:       run.ReplacementRunID,
			Mode:                   run.Mode,
			ThreadID:               run.ThreadID,
			Status:                 resolveSubagentTargetState(run),
			EndedReason:            run.EndedReason,
			SuppressAnnounceReason: run.SuppressAnnounceReason,
			AnnounceDeliveryPath:   run.AnnounceDeliveryPath,
			CreatedAt:              run.CreatedAt.Format(time.RFC3339),
		})
	}
	return JSONResult(map[string]any{
		"count": itemsCount(items),
		"runs":  items,
	})
}

func (t *SubagentsTool) kill(ctx context.Context, toolCtx ToolContext, target string, recentMinutes int) (core.ToolResult, error) {
	entry, err := resolveSubagentTarget(listSubagentTargets(toolCtx, recentMinutes), target)
	if err != nil {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  err.Error(),
		})
	}
	if entry == nil {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  "unknown subagent target",
		})
	}
	killed := false
	if entry.RunID != "" {
		killed = toolCtx.Runtime.GetActiveRuns().CancelRun(entry.ChildSessionKey, entry.RunID)
	} else {
		killed = len(toolCtx.Runtime.GetActiveRuns().CancelSession(entry.ChildSessionKey)) > 0
	}
	cleared := toolCtx.Runtime.GetQueue().Clear(entry.ChildSessionKey)
	if !killed && cleared == 0 {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  "subagent is not active",
		})
	}
	if entry.RegistryRecord != nil {
		toolCtx.Runtime.GetSubagents().SuppressCompletionAnnouncement(entry.RunID, "killed")
		completed, _ := toolCtx.Runtime.GetSubagents().Complete(entry.RunID, core.AgentRunResult{}, context.Canceled) // zero value fallback is intentional
		if completed != nil {
			toolCtx.Runtime.GetSubagents().MarkCompletionMessageSent(entry.RunID)
		}
	}
	// Cascade-kill any descendant subagent runs spawned by this target.
	if registry := toolCtx.Runtime.GetSubagents(); registry != nil && toolCtx.Runtime.GetActiveRuns() != nil {
		task.CascadeKillChildren(registry, toolCtx.Runtime.GetActiveRuns(), entry.ChildSessionKey, nil)
	}
	_ = ctx // reserved for future use
	return JSONResult(map[string]any{
		"status":          "ok",
		"killed":          killed,
		"clearedFollowup": cleared,
		"runId":           entry.RunID,
		"sessionKey":      entry.ChildSessionKey,
	})
}

func (t *SubagentsTool) info(toolCtx ToolContext, target string, recentMinutes int) (core.ToolResult, error) {
	entry, err := resolveSubagentTarget(listSubagentTargets(toolCtx, recentMinutes), target)
	if err != nil {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  err.Error(),
		})
	}
	if entry == nil {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  "unknown subagent target",
		})
	}
	return JSONResult(map[string]any{
		"status":                 "ok",
		"kind":                   entry.Kind,
		"runId":                  entry.RunID,
		"childSessionKey":        entry.ChildSessionKey,
		"requester":              entry.RequesterDisplayKey,
		"label":                  entry.Label,
		"task":                   entry.Task,
		"model":                  entry.Model,
		"runtimeBackend":         entry.RuntimeBackend,
		"runtimeState":           entry.RuntimeState,
		"runtimeMode":            entry.RuntimeMode,
		"runtimeStatusSummary":   entry.RuntimeStatusSummary,
		"steeredFromRunId":       entry.SteeredFromRunID,
		"replacementRunId":       entry.ReplacementRunID,
		"mode":                   entry.Mode,
		"threadId":               entry.ThreadID,
		"endedReason":            entry.EndedReason,
		"suppressAnnounceReason": entry.SuppressAnnounceReason,
		"announceDeliveryPath":   entry.AnnounceDeliveryPath,
		"spawnDepth":             entry.SpawnDepth,
		"createdAt":              entry.CreatedAt.Format(time.RFC3339),
		"state":                  resolveSubagentTargetState(*entry),
		"result":                 entry.Result,
	})
}

func (t *SubagentsTool) log(toolCtx ToolContext, target string, recentMinutes int) (core.ToolResult, error) {
	entry, err := resolveSubagentTarget(listSubagentTargets(toolCtx, recentMinutes), target)
	if err != nil {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  err.Error(),
		})
	}
	if entry == nil {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  "unknown subagent target",
		})
	}
	history, err := toolCtx.Runtime.GetSessions().LoadTranscript(entry.ChildSessionKey)
	if err != nil {
		return core.ToolResult{}, err
	}
	type row struct {
		Role      string `json:"role"`
		Text      string `json:"text"`
		Timestamp string `json:"timestamp"`
	}
	rows := make([]row, 0, len(history))
	for _, message := range history {
		rows = append(rows, row{
			Role:      message.Role,
			Text:      message.Text,
			Timestamp: message.Timestamp.Format(time.RFC3339),
		})
	}
	return JSONResult(map[string]any{
		"status":          "ok",
		"kind":            entry.Kind,
		"runId":           entry.RunID,
		"childSessionKey": entry.ChildSessionKey,
		"messages":        rows,
	})
}

func (t *SubagentsTool) send(ctx context.Context, toolCtx ToolContext, target string, message string, steer bool, recentMinutes int) (core.ToolResult, error) {
	message = strings.TrimSpace(message)
	if len(message) > maxSteerMessageChars {
		message = message[:maxSteerMessageChars]
	}
	entry, err := resolveSubagentTarget(listSubagentTargets(toolCtx, recentMinutes), target)
	if err != nil {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  err.Error(),
		})
	}
	if entry == nil {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  "unknown subagent target",
		})
	}
	// Rate limit steer operations per (caller, child) pair.
	if steer {
		rlKey := toolCtx.Run.Session.SessionKey + ":" + entry.ChildSessionKey
		steerRateLimiter.mu.Lock()
		last := steerRateLimiter.times[rlKey]
		now := time.Now()
		if !last.IsZero() && now.Sub(last) < steerRateLimitDuration {
			remaining := steerRateLimitDuration - now.Sub(last)
			steerRateLimiter.mu.Unlock()
			return JSONResult(map[string]any{
				"status": "error",
				"error":  fmt.Sprintf("steer rate limited, try again in %v", remaining),
			})
		}
		steerRateLimiter.times[rlKey] = now
		steerRateLimiter.mu.Unlock()
	}
	restartOriginalRunID := ""
	restartReplacementRunID := ""
	if steer {
		if entry.RegistryRecord != nil && toolCtx.Runtime.GetSubagents() != nil {
			replacement, ok, restartErr := toolCtx.Runtime.GetSubagents().RegisterSteerRestartReplacement(entry.RunID)
			if restartErr != nil {
				return core.ToolResult{}, restartErr
			}
			if ok && replacement != nil {
				restartOriginalRunID = entry.RunID
				restartReplacementRunID = replacement.RunID
			} else {
				toolCtx.Runtime.GetSubagents().SuppressCompletionAnnouncement(entry.RunID, "steer-restart")
			}
		}
		if entry.RunID != "" {
			_ = toolCtx.Runtime.GetActiveRuns().CancelRun(entry.ChildSessionKey, entry.RunID) // best-effort; failure is non-critical
		} else {
			_ = toolCtx.Runtime.GetActiveRuns().CancelSession(entry.ChildSessionKey) // best-effort; failure is non-critical
		}
		_ = toolCtx.Runtime.GetQueue().Clear(entry.ChildSessionKey) // best-effort; failure is non-critical
	}
	lane := core.LaneSubagent
	if entry.Kind == "acp" && entry.Mode == "session" {
		lane = core.LaneDefault
	}
	result, err := toolCtx.Runtime.Run(ctx, core.AgentRunRequest{
		RunID:             restartReplacementRunID,
		Message:           message,
		SessionKey:        entry.ChildSessionKey,
		SessionID:         resolveStoredSessionID(toolCtx.Runtime, entry.ChildSessionKey),
		AgentID:           session.ResolveAgentIDFromSessionKey(entry.ChildSessionKey),
		Lane:              lane,
		SpawnedBy:         entry.Requester,
		SpawnDepth:        entry.SpawnDepth,
		MaxSpawnDepth:     toolCtx.Run.Request.MaxSpawnDepth,
		Deliver:           false,
		ShouldFollowup:    true,
		QueueMode:         core.QueueModeSteer,
		ExtraSystemPrompt: BuildAgentToAgentMessageContext(toolCtx.Run.Session.SessionKey, toolCtx.Run.Request.Channel, entry.ChildSessionKey),
	})
	if err != nil {
		if restartOriginalRunID != "" && restartReplacementRunID != "" && toolCtx.Runtime.GetSubagents() != nil {
			toolCtx.Runtime.GetSubagents().RestoreAfterFailedSteerRestart(restartOriginalRunID, restartReplacementRunID)
		}
		return core.ToolResult{}, err
	}
	replyText := latestResultText(result)
	status := "ok"
	if result.Queued {
		status = "queued"
	}
	return JSONResult(map[string]any{
		"status":          status,
		"kind":            entry.Kind,
		"runId":           result.RunID,
		"childSessionKey": entry.ChildSessionKey,
		"reply":           replyText,
		"queued":          result.Queued,
		"queueDepth":      result.QueueDepth,
		"steered":         steer,
	})
}

func resolveRequesterForSubagents(runCtx AgentRunContext) string {
	if runCtx.Request.Lane == core.LaneSubagent && strings.TrimSpace(runCtx.Request.SpawnedBy) != "" {
		return runCtx.Request.SessionKey
	}
	return runCtx.Session.SessionKey
}

func parseSubagentIndex(value string) (int, bool) {
	n := 0
	for _, r := range strings.TrimSpace(value) {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, n > 0
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}

func latestResultText(result core.AgentRunResult) string {
	for i := len(result.Payloads) - 1; i >= 0; i-- {
		text := strings.TrimSpace(result.Payloads[i].Text)
		if text != "" {
			return text
		}
	}
	return ""
}

func resolveStoredSessionID(runtime RuntimeServices, sessionKey string) string {
	if runtime == nil || runtime.GetSessions() == nil {
		return ""
	}
	entry := runtime.GetSessions().Entry(sessionKey)
	if entry == nil {
		return ""
	}
	return strings.TrimSpace(entry.SessionID)
}

func itemsCount[T any](items []T) int {
	return len(items)
}
