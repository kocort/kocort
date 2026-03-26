package tool

import (
	"context"
	"sort"
	"strings"

	"github.com/kocort/kocort/internal/core"
)

type AgentsListTool struct{}

func NewAgentsListTool() *AgentsListTool { return &AgentsListTool{} }

func (t *AgentsListTool) Name() string { return "agents_list" }

func (t *AgentsListTool) Description() string { return "List agent ids allowed for sessions_spawn." }

func (t *AgentsListTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
	}
}

type agentIdentityLister interface {
	IdentitySnapshot() []core.AgentIdentity
}

func (t *AgentsListTool) Execute(_ context.Context, toolCtx ToolContext, _ map[string]any) (core.ToolResult, error) {
	allowed := map[string]core.AgentIdentity{}
	current := toolCtx.Run.Identity
	if strings.TrimSpace(current.ID) != "" {
		allowed[current.ID] = current
	}
	if lister, ok := toolCtx.Runtime.(agentIdentityLister); ok {
		for _, identity := range lister.IdentitySnapshot() {
			if strings.TrimSpace(identity.ID) == "" {
				continue
			}
			allowed[identity.ID] = identity
		}
	}
	allowList := append([]string{}, current.SubagentAllowAgents...)
	if len(allowList) > 0 {
		filtered := map[string]core.AgentIdentity{}
		for _, agentID := range allowList {
			agentID = strings.TrimSpace(agentID)
			if agentID == "" {
				continue
			}
			if identity, ok := allowed[agentID]; ok {
				filtered[agentID] = identity
				continue
			}
			filtered[agentID] = core.AgentIdentity{ID: agentID, Name: agentID}
		}
		allowed = filtered
	}
	type row struct {
		ID   string `json:"id"`
		Name string `json:"name,omitempty"`
	}
	items := make([]row, 0, len(allowed))
	for _, identity := range allowed {
		items = append(items, row{
			ID:   strings.TrimSpace(identity.ID),
			Name: strings.TrimSpace(identity.Name),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return JSONResult(map[string]any{
		"agents": items,
		"count":  len(items),
	})
}
