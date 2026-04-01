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
	return "Manage background exec sessions: list, poll, log, write, submit, paste, kill, clear, remove."
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
					"enum":        []string{"list", "poll", "log", "write", "submit", "paste", "kill", "clear", "remove"},
					"description": "Process action to perform.",
				},
				"sessionId": map[string]any{
					"type":        "string",
					"description": "Process session id for actions other than list.",
				},
				"data": map[string]any{
					"type":        "string",
					"description": "For write: raw data to send to stdin.",
				},
				"text": map[string]any{
					"type":        "string",
					"description": "For paste: text to send to stdin.",
				},
				"eof": map[string]any{
					"type":        "boolean",
					"description": "For write: close stdin after writing.",
				},
				"offset": map[string]any{
					"type":        "number",
					"description": "For log: line offset.",
				},
				"limit": map[string]any{
					"type":        "number",
					"description": "For log: number of lines to return.",
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
		offset, err := ReadOptionalIntParam(args, "offset")
		if err != nil {
			return core.ToolResult{}, err
		}
		limit, err := ReadOptionalIntParam(args, "limit")
		if err != nil {
			return core.ToolResult{}, err
		}
		output, paging := sliceProcessLog(record.Output, offset, limit)
		return JSONResult(map[string]any{
			"sessionId": record.ID,
			"status":    record.Status,
			"output":    output,
			"tail":      record.Tail,
			"paging":    paging,
		})
	case "write":
		sessionID, err := ReadStringParam(args, "sessionId", true)
		if err != nil {
			return core.ToolResult{}, err
		}
		data, err := ReadStringParam(args, "data", false)
		if err != nil {
			return core.ToolResult{}, err
		}
		eof, err := ReadBoolParam(args, "eof")
		if err != nil {
			return core.ToolResult{}, err
		}
		record, ok, err := toolCtx.Runtime.GetProcesses().Write(sessionID, data, eof)
		if err != nil {
			return core.ToolResult{}, err
		}
		if !ok {
			return core.ToolResult{}, fmt.Errorf("no process session found for %q", sessionID)
		}
		return JSONResult(record)
	case "submit":
		sessionID, err := ReadStringParam(args, "sessionId", true)
		if err != nil {
			return core.ToolResult{}, err
		}
		record, ok, err := toolCtx.Runtime.GetProcesses().Submit(sessionID)
		if err != nil {
			return core.ToolResult{}, err
		}
		if !ok {
			return core.ToolResult{}, fmt.Errorf("no process session found for %q", sessionID)
		}
		return JSONResult(record)
	case "paste":
		sessionID, err := ReadStringParam(args, "sessionId", true)
		if err != nil {
			return core.ToolResult{}, err
		}
		text, err := ReadStringParam(args, "text", false)
		if err != nil {
			return core.ToolResult{}, err
		}
		record, ok, err := toolCtx.Runtime.GetProcesses().Paste(sessionID, text)
		if err != nil {
			return core.ToolResult{}, err
		}
		if !ok {
			return core.ToolResult{}, fmt.Errorf("no process session found for %q", sessionID)
		}
		return JSONResult(record)
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
	case "clear":
		sessionID, err := ReadStringParam(args, "sessionId", true)
		if err != nil {
			return core.ToolResult{}, err
		}
		if !toolCtx.Runtime.GetProcesses().Clear(sessionID) {
			return core.ToolResult{}, fmt.Errorf("no finished process session found for %q", sessionID)
		}
		return JSONResult(map[string]any{
			"sessionId": sessionID,
			"cleared":   true,
		})
	case "remove":
		sessionID, err := ReadStringParam(args, "sessionId", true)
		if err != nil {
			return core.ToolResult{}, err
		}
		record, ok, err := toolCtx.Runtime.GetProcesses().Remove(sessionID)
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

const defaultProcessLogTailLines = 200

func sliceProcessLog(value string, offset int, limit int) (string, map[string]any) {
	lines := strings.Split(value, "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	total := len(lines)
	usingDefaultTail := offset <= 0 && limit <= 0
	if usingDefaultTail {
		if total > defaultProcessLogTailLines {
			offset = total - defaultProcessLogTailLines
			limit = defaultProcessLogTailLines
		} else {
			offset = 0
			limit = total
		}
	} else {
		if offset < 0 {
			offset = 0
		}
		if offset > total {
			offset = total
		}
		if limit <= 0 || offset+limit > total {
			limit = total - offset
		}
	}
	end := offset + limit
	if end > total {
		end = total
	}
	sliced := strings.Join(lines[offset:end], "\n")
	paging := map[string]any{
		"offset":     offset,
		"limit":      limit,
		"totalLines": total,
	}
	if usingDefaultTail && total > defaultProcessLogTailLines {
		paging["note"] = fmt.Sprintf("showing last %d of %d lines; pass offset/limit to page", defaultProcessLogTailLines, total)
	}
	return sliced, paging
}
