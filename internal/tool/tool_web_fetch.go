package tool

import (
	"context"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/infra"
	webpkg "github.com/kocort/kocort/internal/web"
)

type WebFetchTool struct {
	dc     *infra.DynamicHTTPClient
	client *webpkg.Client
}

func NewWebFetchTool(dc *infra.DynamicHTTPClient) *WebFetchTool {
	return &WebFetchTool{dc: dc}
}

func (t *WebFetchTool) Name() string { return "web_fetch" }

func (t *WebFetchTool) Description() string { return "Fetch and extract readable content from a URL." }

func (t *WebFetchTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":      map[string]any{"type": "string", "description": "URL to fetch."},
				"maxChars": map[string]any{"type": "number", "description": "Maximum readable characters to return."},
			},
			"required":             []string{"url"},
			"additionalProperties": false,
		},
	}
}

func (t *WebFetchTool) Execute(_ context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	urlValue, err := ReadStringParam(args, "url", true)
	if err != nil {
		return core.ToolResult{}, err
	}
	maxChars, err := ReadOptionalIntParam(args, "maxChars")
	if err != nil {
		return core.ToolResult{}, err
	}
	client := t.client
	if client == nil {
		client = webpkg.NewDynamicClient(t.dc)
	}
	result, err := client.Fetch(urlValue, resolveToolEnv(toolCtx, "KOCORT_WEB_USER_AGENT"), maxChars)
	if err != nil {
		return core.ToolResult{}, err
	}
	return JSONResult(result)
}
