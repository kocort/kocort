package tool

import (
	"context"
	"strings"

	"github.com/kocort/kocort/internal/core"

	"github.com/kocort/kocort/internal/session"
)

type SessionStatusTool struct{}

func NewSessionStatusTool() *SessionStatusTool {
	return &SessionStatusTool{}
}

func (t *SessionStatusTool) Name() string {
	return "session_status"
}

func (t *SessionStatusTool) Description() string {
	return "Show a /status-equivalent status card (usage + time + Reasoning/Verbose/Elevated); optional per-session model override."
}

func (t *SessionStatusTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"sessionKey": map[string]any{"type": "string"},
				"model":      map[string]any{"type": "string"},
			},
			"additionalProperties": false,
		},
	}
}

func (t *SessionStatusTool) Execute(ctx context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	_ = ctx                                                     // reserved for future use
	sessionRef, _ := ReadStringParam(args, "sessionKey", false) // zero value fallback is intentional
	modelRaw, _ := ReadStringParam(args, "model", false)        // zero value fallback is intentional

	sessionKey := strings.TrimSpace(sessionRef)
	if sessionKey == "" {
		sessionKey = toolCtx.Run.Session.SessionKey
	}
	target, denied, err := resolveAccessibleSessionTarget(toolCtx, session.SessionAccessHistory, sessionKey, toolCtx.Run.Request.AgentID)
	if err != nil {
		return core.ToolResult{}, err
	}
	if denied != nil {
		return *denied, nil
	}
	sessionKey = target.Key

	entry := toolCtx.Runtime.GetSessions().Entry(sessionKey)
	if strings.TrimSpace(modelRaw) != "" {
		if entry == nil {
			return JSONResult(map[string]any{
				"status": "error",
				"error":  "unknown session",
			})
		}
		next := *entry
		modelRaw = strings.TrimSpace(modelRaw)
		if strings.EqualFold(modelRaw, "default") {
			next.ProviderOverride = ""
			next.ModelOverride = ""
		} else {
			if provider, model, ok := splitModelRef(modelRaw); ok {
				next.ProviderOverride = provider
				next.ModelOverride = model
			} else {
				next.ModelOverride = modelRaw
			}
		}
		if err := toolCtx.Runtime.GetSessions().Upsert(sessionKey, next); err != nil {
			return core.ToolResult{}, err
		}
		entry = &next
	}

	selection, _ := toolCtx.Runtime.ResolveModelSelection(context.Background(), toolCtx.Run.Identity, core.AgentRunRequest{}, core.SessionResolution{ // zero value fallback is intentional
		SessionKey: sessionKey,
		Entry:      entry,
	})
	queueDepth := toolCtx.Runtime.GetQueue().Depth(sessionKey)
	activeRuns := toolCtx.Runtime.GetActiveRuns().Count(sessionKey)
	status := map[string]any{
		"sessionKey": sessionKey,
		"queueDepth": queueDepth,
		"activeRuns": activeRuns,
		"model":      selection.Model,
		"provider":   selection.Provider,
	}
	if entry != nil {
		status["sessionId"] = entry.SessionID
		status["thinkingLevel"] = entry.ThinkingLevel
		status["verboseLevel"] = entry.VerboseLevel
		status["lastChannel"] = entry.LastChannel
		status["lastTo"] = entry.LastTo
	}
	return JSONResult(status)
}

func splitModelRef(value string) (string, string, bool) {
	raw := strings.TrimSpace(value)
	slash := strings.Index(raw, "/")
	if slash <= 0 || slash >= len(raw)-1 {
		return "", "", false
	}
	return raw[:slash], raw[slash+1:], true
}
