package tool

import (
	"context"
	"strconv"
	"strings"

	"github.com/kocort/kocort/internal/core"
	memorypkg "github.com/kocort/kocort/internal/memory"
)

const maxMemoryGetLines = 200

type MemoryGetTool struct{}

func NewMemoryGetTool() *MemoryGetTool {
	return &MemoryGetTool{}
}

func (t *MemoryGetTool) Name() string {
	return "memory_get"
}

func (t *MemoryGetTool) Description() string {
	return "Read a specific memory file or line range."
}

func (t *MemoryGetTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path inside MEMORY.md/memory/* or an explicitly configured memory extra path.",
				},
				"from": map[string]any{
					"type":        "number",
					"description": "1-based starting line.",
				},
				"lines": map[string]any{
					"type":        "number",
					"description": "Maximum number of lines to read.",
				},
			},
			"required":             []string{"path"},
			"additionalProperties": false,
		},
	}
}

func (t *MemoryGetTool) Execute(ctx context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	_ = ctx // reserved for future use
	pathArg, err := ReadStringParam(args, "path", true)
	if err != nil {
		return core.ToolResult{}, err
	}
	workspaceDir := strings.TrimSpace(toolCtx.Run.Identity.WorkspaceDir)
	if workspaceDir == "" {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  "workspace is not configured",
		})
	}
	cleanPath, absPath, ok := memorypkg.ResolveReadablePath(workspaceDir, toolCtx.Run.Identity, pathArg)
	if !ok {
		return JSONResult(map[string]any{
			"status": "forbidden",
			"error":  "memory_get path must point to MEMORY.md, memory.md, memory/*, or a configured memory extra path",
		})
	}
	content, err := memorypkg.ReadAllowedTextFile(absPath)
	if err != nil {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  err.Error(),
		})
	}
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	lines := []string{}
	if normalized != "" {
		lines = strings.Split(normalized, "\n")
	}
	start := 1
	if raw, ok := args["from"]; ok {
		switch value := raw.(type) {
		case float64:
			if value > 0 {
				start = int(value)
			}
		case string:
			if parsed, parseErr := strconv.Atoi(strings.TrimSpace(value)); parseErr == nil && parsed > 0 {
				start = parsed
			}
		}
	}
	count := len(lines)
	if raw, ok := args["lines"]; ok {
		switch value := raw.(type) {
		case float64:
			if value > 0 {
				count = int(value)
			}
		case string:
			if parsed, parseErr := strconv.Atoi(strings.TrimSpace(value)); parseErr == nil && parsed > 0 {
				count = parsed
			}
		}
	}
	if start > len(lines) {
		start = len(lines)
	}
	if count > maxMemoryGetLines {
		count = maxMemoryGetLines
	}
	end := start - 1 + count
	if end > len(lines) {
		end = len(lines)
	}
	if start < 1 {
		start = 1
	}
	snippet := ""
	if start <= end && start-1 < len(lines) {
		snippet = strings.Join(lines[start-1:end], "\n")
	}
	return JSONResult(map[string]any{
		"path":      cleanPath,
		"from":      start,
		"lines":     max(0, end-start+1),
		"text":      snippet,
		"truncated": end < len(lines),
	})
}
