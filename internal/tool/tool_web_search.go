package tool

import (
	"context"
	"strings"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/infra"
	webpkg "github.com/kocort/kocort/internal/web"
)

type WebSearchTool struct {
	dc     *infra.DynamicHTTPClient
	client *webpkg.Client
}

func NewWebSearchTool(dc *infra.DynamicHTTPClient) *WebSearchTool {
	return &WebSearchTool{dc: dc}
}

func (t *WebSearchTool) Name() string { return "web_search" }

func (t *WebSearchTool) Description() string { return "Search the web (Brave API)." }

func (t *WebSearchTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Search query."},
				"count": map[string]any{"type": "number", "description": "Maximum number of search results."},
			},
			"required":             []string{"query"},
			"additionalProperties": false,
		},
	}
}

func (t *WebSearchTool) Execute(_ context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	query, err := ReadStringParam(args, "query", true)
	if err != nil {
		return core.ToolResult{}, err
	}
	count, err := ReadOptionalIntParam(args, "count")
	if err != nil {
		return core.ToolResult{}, err
	}
	endpoint := resolveToolEnv(toolCtx, "KOCORT_WEB_SEARCH_ENDPOINT")
	apiKey := resolveToolEnv(toolCtx, "BRAVE_SEARCH_API_KEY")
	client := t.client
	if client == nil {
		client = webpkg.NewDynamicClient(t.dc)
	}
	results, err := client.SearchBrave(endpoint, apiKey, query, count)
	if err != nil {
		return JSONResult(map[string]any{
			"status":  "unavailable",
			"error":   err.Error(),
			"results": []any{},
			"count":   0,
		})
	}
	return JSONResult(map[string]any{
		"status":  "ok",
		"query":   query,
		"results": results,
		"count":   len(results),
	})
}

func resolveToolEnv(toolCtx ToolContext, key string) string {
	if toolCtx.Runtime == nil || toolCtx.Runtime.GetEnvironment() == nil {
		return ""
	}
	value, _ := toolCtx.Runtime.GetEnvironment().Resolve(strings.TrimSpace(key))
	return strings.TrimSpace(value)
}
