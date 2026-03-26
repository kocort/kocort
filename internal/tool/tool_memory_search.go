package tool

import (
	"context"
	"fmt"
	"math"

	"github.com/kocort/kocort/internal/core"
	memorypkg "github.com/kocort/kocort/internal/memory"

	"strings"

	"github.com/kocort/kocort/utils"
)

type MemorySearchTool struct{}

func NewMemorySearchTool() *MemorySearchTool {
	return &MemorySearchTool{}
}

func (t *MemorySearchTool) Name() string {
	return "memory_search"
}

func (t *MemorySearchTool) Description() string {
	return "Search durable workspace memory."
}

func (t *MemorySearchTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "What to search for in MEMORY.md and memory/*.",
				},
				"maxResults": map[string]any{
					"type":        "number",
					"description": "Optional maximum number of results to return.",
				},
				"minScore": map[string]any{
					"type":        "number",
					"description": "Optional minimum relevance score to keep.",
				},
			},
			"required":             []string{"query"},
			"additionalProperties": false,
		},
	}
}

func (t *MemorySearchTool) Execute(ctx context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	query, err := ReadStringParam(args, "query", true)
	if err != nil {
		return core.ToolResult{}, err
	}
	maxResults, err := readOptionalIntParam(args, "maxResults")
	if err != nil {
		return core.ToolResult{}, err
	}
	minScore, err := readOptionalFloatParam(args, "minScore")
	if err != nil {
		return core.ToolResult{}, err
	}
	status := resolveMemorySearchStatus(toolCtx)
	hits, err := toolCtx.Runtime.GetMemory().Recall(ctx, toolCtx.Run.Identity, toolCtx.Run.Session, query)
	if err != nil {
		return JSONResult(map[string]any{
			"status":   "unavailable",
			"disabled": true,
			"error":    err.Error(),
			"count":    0,
			"results":  []any{},
			"backend":  status.Backend,
			"provider": status.Provider,
			"fallback": status.Fallback,
			"mode":     status.Mode,
		})
	}
	hits = memorypkg.ClipMemoryHits(hits, maxResults, minScore)
	type row struct {
		ID       string  `json:"id,omitempty"`
		Source   string  `json:"source"`
		Path     string  `json:"path,omitempty"`
		FromLine int     `json:"fromLine,omitempty"`
		ToLine   int     `json:"toLine,omitempty"`
		Snippet  string  `json:"snippet"`
		Score    float64 `json:"score"`
	}
	results := make([]row, 0, len(hits))
	for _, hit := range hits {
		results = append(results, row{
			ID:       hit.ID,
			Source:   hit.Source,
			Path:     utils.NonEmpty(hit.Path, hit.Source),
			FromLine: hit.FromLine,
			ToLine:   hit.ToLine,
			Snippet:  strings.TrimSpace(hit.Snippet),
			Score:    hit.Score,
		})
	}
	return JSONResult(map[string]any{
		"count":    len(results),
		"results":  results,
		"backend":  status.Backend,
		"provider": status.Provider,
		"fallback": status.Fallback,
		"mode":     status.Mode,
	})
}

func resolveMemorySearchStatus(toolCtx ToolContext) memorypkg.SearchStatus {
	if toolCtx.Runtime == nil {
		return memorypkg.SearchStatus{}
	}
	memoryProvider := toolCtx.Runtime.GetMemory()
	if provider, ok := memoryProvider.(memorypkg.StatusProvider); ok {
		return provider.SearchStatus(toolCtx.Run.Identity)
	}
	return memorypkg.SearchStatus{}
}

func readOptionalIntParam(args map[string]any, key string) (int, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return 0, nil
	}
	switch value := raw.(type) {
	case float64:
		if value <= 0 || math.Trunc(value) != value {
			return 0, core.ToolInputError{Message: fmt.Sprintf("parameter %q must be a positive integer", key)}
		}
		return int(value), nil
	case int:
		if value <= 0 {
			return 0, core.ToolInputError{Message: fmt.Sprintf("parameter %q must be a positive integer", key)}
		}
		return value, nil
	default:
		return 0, core.ToolInputError{Message: fmt.Sprintf("parameter %q must be a positive integer", key)}
	}
}

func readOptionalFloatParam(args map[string]any, key string) (float64, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return 0, nil
	}
	switch value := raw.(type) {
	case float64:
		return value, nil
	case int:
		return float64(value), nil
	default:
		return 0, core.ToolInputError{Message: fmt.Sprintf("parameter %q must be a number", key)}
	}
}
