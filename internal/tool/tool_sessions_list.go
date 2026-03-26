package tool

import (
	"context"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"
)

type SessionsListTool struct{}

func NewSessionsListTool() *SessionsListTool {
	return &SessionsListTool{}
}

func (t *SessionsListTool) Name() string {
	return "sessions_list"
}

func (t *SessionsListTool) Description() string {
	return "List other sessions (incl. sub-agents) with filters/last."
}

func (t *SessionsListTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit":         map[string]any{"type": "number"},
				"activeMinutes": map[string]any{"type": "number"},
				"spawnedBy":     map[string]any{"type": "string"},
				"rootsOnly":     map[string]any{"type": "boolean"},
				"kinds": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
			},
			"additionalProperties": false,
		},
	}
}

func (t *SessionsListTool) Execute(ctx context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	_ = ctx // reserved for future use
	limit := 0
	activeMinutes := 0
	spawnedBy := ""
	rootsOnly := false
	var kinds map[string]struct{}
	if raw, ok := args["limit"]; ok {
		if value, ok := raw.(float64); ok && value > 0 {
			limit = int(value)
		}
	}
	if raw, ok := args["activeMinutes"]; ok {
		if value, ok := raw.(float64); ok && value > 0 {
			activeMinutes = int(value)
		}
	}
	if raw, ok := args["spawnedBy"]; ok {
		if value, ok := raw.(string); ok {
			spawnedBy = strings.TrimSpace(value)
		}
	}
	if raw, ok := args["rootsOnly"]; ok {
		if value, ok := raw.(bool); ok {
			rootsOnly = value
		}
	}
	if raw, ok := args["kinds"]; ok {
		if list, ok := raw.([]any); ok {
			kinds = map[string]struct{}{}
			for _, item := range list {
				value, ok := item.(string)
				if !ok {
					continue
				}
				value = strings.TrimSpace(strings.ToLower(value))
				if value != "" {
					kinds[value] = struct{}{}
				}
			}
		}
	}
	items := toolCtx.Runtime.GetSessions().ListSessions()
	items = session.FilterSessionListItems(items, session.SessionListFilterOptions{
		Now:           time.Now().UTC(),
		ActiveMinutes: activeMinutes,
		Limit:         limit,
		AllowedKinds:  kinds,
		MainKey:       session.DefaultMainKey,
		Allow: func(item session.SessionListItem) bool {
			if spawnedBy != "" && strings.TrimSpace(item.SpawnedBy) != spawnedBy {
				return false
			}
			if rootsOnly && strings.TrimSpace(item.ParentSessionKey) != "" {
				return false
			}
			access := toolCtx.Runtime.CheckSessionAccess(session.SessionAccessList, toolCtx.Run.Session.SessionKey, item.Key)
			return access.Allowed
		},
	})
	items = session.DecorateSessionListItems(items, session.DefaultMainKey)
	return JSONResult(map[string]any{
		"count":    len(items),
		"sessions": items,
	})
}
