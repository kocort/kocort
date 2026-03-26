package tool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
)

type ProcessTool struct{}

func NewProcessTool() *ProcessTool {
	return &ProcessTool{}
}

func (t *ProcessTool) Name() string {
	return "process"
}

func (t *ProcessTool) Description() string {
	return "Manage background exec sessions."
}

func (t *ProcessTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"list", "poll", "log", "kill"},
					"description": "Process action to perform.",
				},
				"sessionId": map[string]any{
					"type":        "string",
					"description": "Process session id for poll/log/kill.",
				},
				"timeout": map[string]any{
					"type":        "number",
					"description": "For poll: wait up to this many milliseconds before returning.",
				},
			},
			"required": []string{"action"},
		},
	}
}

func (t *ProcessTool) Execute(_ context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	if toolCtx.Runtime == nil || toolCtx.Runtime.GetProcesses() == nil {
		return core.ToolResult{}, fmt.Errorf("process registry is not configured")
	}
	action, err := ReadStringParam(args, "action", true)
	if err != nil {
		return core.ToolResult{}, err
	}
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "list":
		return JSONResult(map[string]any{
			"sessions": toolCtx.Runtime.GetProcesses().List(),
		})
	case "poll":
		sessionID, err := ReadStringParam(args, "sessionId", true)
		if err != nil {
			return core.ToolResult{}, err
		}
		waitMs, err := ReadOptionalPositiveDurationParam(args, "timeout", time.Millisecond)
		if err != nil {
			return core.ToolResult{}, err
		}
		record, ok := toolCtx.Runtime.GetProcesses().Poll(sessionID, waitMs)
		if !ok {
			return core.ToolResult{}, fmt.Errorf("no process session found for %q", sessionID)
		}
		return JSONResult(record)
	case "log":
		sessionID, err := ReadStringParam(args, "sessionId", true)
		if err != nil {
			return core.ToolResult{}, err
		}
		record, ok := toolCtx.Runtime.GetProcesses().Get(sessionID)
		if !ok {
			return core.ToolResult{}, fmt.Errorf("no process session found for %q", sessionID)
		}
		return JSONResult(map[string]any{
			"sessionId": record.ID,
			"status":    record.Status,
			"output":    record.Output,
			"tail":      record.Tail,
		})
	case "kill":
		sessionID, err := ReadStringParam(args, "sessionId", true)
		if err != nil {
			return core.ToolResult{}, err
		}
		record, ok, err := toolCtx.Runtime.GetProcesses().Kill(sessionID)
		if err != nil {
			return core.ToolResult{}, err
		}
		if !ok {
			return core.ToolResult{}, fmt.Errorf("no process session found for %q", sessionID)
		}
		return JSONResult(record)
	default:
		return core.ToolResult{}, fmt.Errorf("unsupported process action %q", action)
	}
}
