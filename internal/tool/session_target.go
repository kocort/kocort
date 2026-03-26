package tool

import (
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"
)

type resolvedSessionTarget struct {
	Key        string
	DisplayKey string
}

// resolveAccessibleSessionTarget resolves a user-facing session reference into
// a canonical key, applies sandbox-scoped visibility rules, and then runs the
// regular session ACL check for the requested action.
func resolveAccessibleSessionTarget(toolCtx ToolContext, action session.SessionAccessAction, reference string, requesterAgentID string) (resolvedSessionTarget, *core.ToolResult, error) {
	resolved := session.ResolveVisibleSessionReference(toolCtx.Runtime.GetSessions(), session.ResolveVisibleSessionReferenceOptions{
		Resolve: session.ResolveReferenceOptions{
			Reference:        reference,
			RequesterAgentID: requesterAgentID,
		},
		RequesterSessionKey:      toolCtx.Run.Session.SessionKey,
		SandboxEnabled:           toolCtx.Sandbox != nil && toolCtx.Sandbox.Enabled,
		SandboxSessionVisibility: toolCtx.Run.Identity.SandboxSessionVisibility,
	})
	if resolved.Status == "forbidden" {
		result, err := JSONResult(map[string]any{
			"status":     resolved.Status,
			"error":      resolved.Error,
			"sessionKey": resolved.DisplayKey,
		})
		if err != nil {
			return resolvedSessionTarget{}, nil, err
		}
		return resolvedSessionTarget{}, &result, nil
	}
	if !resolved.Found {
		result, err := JSONResult(map[string]any{
			"status": "error",
			"error":  resolved.Error,
		})
		if err != nil {
			return resolvedSessionTarget{}, nil, err
		}
		return resolvedSessionTarget{}, &result, nil
	}
	access := toolCtx.Runtime.CheckSessionAccess(action, toolCtx.Run.Session.SessionKey, resolved.Key)
	if !access.Allowed {
		payload := map[string]any{
			"status": access.Status,
			"error":  access.Error,
		}
		if resolved.DisplayKey != "" {
			payload["sessionKey"] = resolved.DisplayKey
		}
		result, err := JSONResult(payload)
		if err != nil {
			return resolvedSessionTarget{}, nil, err
		}
		return resolvedSessionTarget{}, &result, nil
	}
	return resolvedSessionTarget{
		Key:        resolved.Key,
		DisplayKey: resolved.DisplayKey,
	}, nil, nil
}
