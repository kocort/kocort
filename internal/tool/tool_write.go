package tool

import (
	"context"
	"os"
	"path/filepath"

	"github.com/kocort/kocort/internal/core"
)

type WriteTool struct{}

func NewWriteTool() *WriteTool { return &WriteTool{} }

func (t *WriteTool) Name() string { return "write" }

func (t *WriteTool) Description() string { return "Create or overwrite files." }

func (t *WriteTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "Path to write. Relative paths resolve from the working directory."},
				"content": map[string]any{"type": "string", "description": "Full file contents to write."},
			},
			"required":             []string{"path", "content"},
			"additionalProperties": false,
		},
	}
}

func (t *WriteTool) Execute(_ context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	pathArg, err := ReadStringParam(args, "path", true)
	if err != nil {
		return core.ToolResult{}, err
	}
	content, err := ReadStringParam(args, "content", true)
	if err != nil {
		return core.ToolResult{}, err
	}
	_, relPath, absPath, err := resolveWorkspaceToolPath(toolCtx, pathArg)
	if err != nil {
		return core.ToolResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return core.ToolResult{}, err
	}
	if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
		return core.ToolResult{}, err
	}
	return JSONResult(map[string]any{
		"status": "ok",
		"path":   relPath,
		"bytes":  len(content),
	})
}
