package tool

import (
	"context"
	"os"
	"strings"

	"github.com/kocort/kocort/internal/core"
)

const defaultReadMaxLines = 2000

type ReadTool struct{}

func NewReadTool() *ReadTool { return &ReadTool{} }

func (t *ReadTool) Name() string { return "read" }

func (t *ReadTool) Description() string { return "Read file contents." }

func (t *ReadTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string", "description": "Path to read. Relative paths resolve from the working directory."},
				"from":   map[string]any{"type": "number", "description": "1-based starting line."},
				"offset": map[string]any{"type": "number", "description": "1-based starting line (alias for from)."},
				"lines":  map[string]any{"type": "number", "description": "Maximum number of lines to read."},
			},
			"required":             []string{"path"},
			"additionalProperties": false,
		},
	}
}

func (t *ReadTool) Execute(_ context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	pathArg, err := ReadStringParam(args, "path", true)
	if err != nil {
		return core.ToolResult{}, err
	}
	_, relPath, absPath, err := resolveWorkspaceToolPath(toolCtx, pathArg)
	if err != nil {
		return core.ToolResult{}, err
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return core.ToolResult{}, err
	}
	if info.IsDir() {
		return core.ToolResult{}, ToolInputError{Message: "path points to a directory; use ls instead"}
	}
	from, _ := ReadOptionalIntParam(args, "from")
	if from <= 0 {
		from, _ = ReadOptionalIntParam(args, "offset")
	}
	linesLimit, _ := ReadOptionalIntParam(args, "lines")
	if linesLimit <= 0 {
		linesLimit = defaultReadMaxLines
	}
	lines, _, err := readWorkspaceFileLines(absPath)
	if err != nil {
		return core.ToolResult{}, err
	}
	start, end, selected := sliceLines(lines, from, linesLimit)

	content := strings.Join(selected, "\n")
	// Apply byte-level truncation (50 KB cap).
	tr := truncateOutputHead(content, 0, defaultMaxOutputBytes)
	if tr.Truncated {
		content = tr.Content
		end = start + tr.OutputLines - 1
	}

	truncated := end < len(lines)
	resp := map[string]any{
		"path":      relPath,
		"fromLine":  start,
		"toLine":    end,
		"truncated": truncated,
		"content":   content,
	}
	if truncated {
		resp["nextOffset"] = end + 1
		resp["totalLines"] = len(lines)
	}
	return JSONResult(resp)
}
