package tool

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kocort/kocort/internal/core"
)

type LSTool struct{}

func NewLSTool() *LSTool { return &LSTool{} }

func (t *LSTool) Name() string { return "ls" }

func (t *LSTool) Description() string { return "List directory contents." }

func (t *LSTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":          map[string]any{"type": "string", "description": "Directory path (default: working directory)."},
				"includeHidden": map[string]any{"type": "boolean", "description": "Include hidden files/dirs."},
				"limit":         map[string]any{"type": "number", "description": "Maximum number of entries (default: 500)."},
			},
			"additionalProperties": false,
		},
	}
}

func (t *LSTool) Execute(_ context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	pathArg, _ := ReadStringParam(args, "path", false)
	if strings.TrimSpace(pathArg) == "" {
		pathArg = "."
	}
	includeHidden, _ := ReadBoolParam(args, "includeHidden")
	limit, _ := ReadOptionalIntParam(args, "limit")
	if limit <= 0 {
		limit = 500
	}
	_, relPath, absPath, err := resolveWorkspaceToolPath(toolCtx, pathArg)
	if err != nil {
		return core.ToolResult{}, err
	}
	entries, err := os.ReadDir(absPath)
	if err != nil {
		return core.ToolResult{}, err
	}
	type item struct {
		Name string `json:"name"`
		Path string `json:"path"`
		Type string `json:"type"`
	}
	items := make([]item, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !includeHidden && strings.HasPrefix(name, ".") {
			continue
		}
		kind := "file"
		if entry.IsDir() {
			kind = "dir"
		}
		items = append(items, item{
			Name: name,
			Path: filepath.ToSlash(filepath.Join(relPath, name)),
			Type: kind,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })

	limitHit := false
	if len(items) > limit {
		items = items[:limit]
		limitHit = true
	}

	resp := map[string]any{
		"path":    relPath,
		"entries": items,
		"count":   len(items),
	}
	if limitHit {
		resp["limitReached"] = true
	}
	return JSONResult(resp)
}
