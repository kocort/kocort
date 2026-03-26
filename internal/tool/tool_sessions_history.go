package tool

import (
	"context"
	"strings"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"
)

type SessionsHistoryTool struct{}

func NewSessionsHistoryTool() *SessionsHistoryTool {
	return &SessionsHistoryTool{}
}

func (t *SessionsHistoryTool) Name() string {
	return "sessions_history"
}

func (t *SessionsHistoryTool) Description() string {
	return "Fetch history for another session/sub-agent."
}

func (t *SessionsHistoryTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"sessionKey": map[string]any{"type": "string"},
				"limit":      map[string]any{"type": "number"},
			},
			"required":             []string{"sessionKey"},
			"additionalProperties": false,
		},
	}
}

func (t *SessionsHistoryTool) Execute(ctx context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	_ = ctx // reserved for future use
	sessionRef, err := ReadStringParam(args, "sessionKey", true)
	if err != nil {
		return core.ToolResult{}, err
	}
	target, denied, err := resolveAccessibleSessionTarget(toolCtx, session.SessionAccessHistory, sessionRef, toolCtx.Run.Request.AgentID)
	if err != nil {
		return core.ToolResult{}, err
	}
	if denied != nil {
		return *denied, nil
	}
	sessionKey := target.Key
	limit := 0
	if raw, ok := args["limit"]; ok {
		if value, ok := raw.(float64); ok && value > 0 {
			limit = int(value)
		}
	}
	history, err := toolCtx.Runtime.GetSessions().LoadTranscript(sessionKey)
	if err != nil {
		return core.ToolResult{}, err
	}
	if limit > 0 && len(history) > limit {
		history = history[len(history)-limit:]
	}
	truncated := false
	for i := range history {
		if len(history[i].Text) > 4000 {
			history[i].Text = strings.TrimSpace(history[i].Text[:4000]) + "\n…(truncated)…"
			truncated = true
		}
	}
	return JSONResult(map[string]any{
		"sessionKey": sessionKey,
		"messages":   history,
		"truncated":  truncated,
	})
}
