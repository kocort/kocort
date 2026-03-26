package tool

import (
	"context"
	"strings"

	"github.com/kocort/kocort/internal/core"
)

// ---------------------------------------------------------------------------
// sessions_yield — End the current turn
//
// Use after spawning subagents to receive their results as the next message.
// Triggers an end_turn stop reason so the parent session becomes idle and
// can receive completion announcements.
//

// ---------------------------------------------------------------------------

// SessionsYieldTool implements the sessions_yield tool.
type SessionsYieldTool struct{}

func NewSessionsYieldTool() *SessionsYieldTool {
	return &SessionsYieldTool{}
}

func (t *SessionsYieldTool) Name() string {
	return "sessions_yield"
}

func (t *SessionsYieldTool) Description() string {
	return "End your current turn. Use after spawning subagents to receive their results as the next message."
}

func (t *SessionsYieldTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{
					"type":        "string",
					"description": "Optional message to include with the yield.",
				},
			},
			"additionalProperties": false,
		},
	}
}

func (t *SessionsYieldTool) Execute(_ context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	message, _ := ReadStringParam(args, "message", false)
	message = strings.TrimSpace(message)
	if message == "" {
		message = "Turn yielded."
	}

	sessionKey := toolCtx.Run.Session.SessionKey
	if sessionKey == "" {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  "No session context",
		})
	}

	// Signal the runtime that this turn should end. The yield callback is
	// stored in the RunState so the pipeline can detect the yield and stop
	// the model call loop.
	if toolCtx.Run.RunState != nil {
		toolCtx.Run.RunState.Yielded = true
		toolCtx.Run.RunState.YieldMessage = message
	}

	return JSONResult(map[string]any{
		"status":  "yielded",
		"message": message,
	})
}
