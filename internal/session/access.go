// Canonical implementation of session access control types and pure
// policy-evaluation functions.
//
// Pure functions only — no *Runtime dependency.
package session

import (
	"context"
	"strings"

	"github.com/kocort/kocort/internal/core"
)

// SessionAccessAction describes an access-control action on a session.
type SessionAccessAction string

const (
	SessionAccessHistory SessionAccessAction = "history"
	SessionAccessSend    SessionAccessAction = "send"
	SessionAccessList    SessionAccessAction = "list"
)

// SessionAccessResult holds the outcome of a session access check.
type SessionAccessResult struct {
	Allowed bool
	Status  string
	Error   string
}

// IsForbidden returns true when the result is an explicit denial.
func (r SessionAccessResult) IsForbidden() bool {
	return !r.Allowed && r.Status == "forbidden"
}

// NormalizeSessionVisibility maps a raw visibility value to a canonical
// SessionToolsVisibility constant.
func NormalizeSessionVisibility(value core.SessionToolsVisibility) core.SessionToolsVisibility {
	switch value {
	case core.SessionVisibilitySelf, core.SessionVisibilityTree, core.SessionVisibilityAgent, core.SessionVisibilityAll:
		return value
	default:
		return core.SessionVisibilityTree
	}
}

// MakeSessionAccessError builds a forbidden SessionAccessResult with an
// appropriate message for the given visibility level.
func MakeSessionAccessError(action SessionAccessAction, visibility core.SessionToolsVisibility) SessionAccessResult {
	switch visibility {
	case core.SessionVisibilitySelf:
		return SessionAccessResult{Status: "forbidden", Error: ActionPrefix(action) + " visibility is restricted to the current session (tools.sessions.visibility=self)."}
	case core.SessionVisibilityTree:
		return SessionAccessResult{Status: "forbidden", Error: ActionPrefix(action) + " visibility is restricted to the current session tree (tools.sessions.visibility=tree)."}
	default:
		return SessionAccessResult{Status: "forbidden", Error: ActionPrefix(action) + " visibility is restricted."}
	}
}

// ActionPrefix returns a human-readable prefix for the given access action.
func ActionPrefix(action SessionAccessAction) string {
	switch action {
	case SessionAccessHistory:
		return "Session history"
	case SessionAccessSend:
		return "Session send"
	default:
		return "Session list"
	}
}

// A2ADisabledMessage returns the error message shown when agent-to-agent
// access is disabled for the given action.
func A2ADisabledMessage(action SessionAccessAction) string {
	switch action {
	case SessionAccessHistory:
		return "Agent-to-agent history is disabled. Set tools.agentToAgent.enabled=true to allow cross-agent access."
	case SessionAccessSend:
		return "Agent-to-agent messaging is disabled. Set tools.agentToAgent.enabled=true to allow cross-agent sends."
	default:
		return "Agent-to-agent listing is disabled. Set tools.agentToAgent.enabled=true to allow cross-agent visibility."
	}
}

// A2ADeniedMessage returns the error message shown when agent-to-agent
// access is denied by the allow list.
func A2ADeniedMessage(action SessionAccessAction) string {
	switch action {
	case SessionAccessHistory:
		return "Agent-to-agent history denied by tools.agentToAgent.allow."
	case SessionAccessSend:
		return "Agent-to-agent messaging denied by tools.agentToAgent.allow."
	default:
		return "Agent-to-agent listing denied by tools.agentToAgent.allow."
	}
}

// MatchAgentPattern matches an agent ID against a glob-like pattern
// supporting *, prefix*, *suffix, and *contains* forms.
func MatchAgentPattern(pattern string, agentID string) bool {
	raw := strings.TrimSpace(strings.ToLower(pattern))
	agentID = strings.TrimSpace(strings.ToLower(agentID))
	if raw == "" || agentID == "" {
		return false
	}
	if raw == "*" {
		return true
	}
	if !strings.Contains(raw, "*") {
		return raw == agentID
	}
	if strings.HasPrefix(raw, "*") && strings.HasSuffix(raw, "*") && len(raw) > 2 {
		return strings.Contains(agentID, strings.Trim(raw, "*"))
	}
	if strings.HasPrefix(raw, "*") {
		return strings.HasSuffix(agentID, strings.TrimPrefix(raw, "*"))
	}
	if strings.HasSuffix(raw, "*") {
		return strings.HasPrefix(agentID, strings.TrimSuffix(raw, "*"))
	}
	parts := strings.Split(raw, "*")
	cursor := 0
	for _, part := range parts {
		if part == "" {
			continue
		}
		idx := strings.Index(agentID[cursor:], part)
		if idx < 0 {
			return false
		}
		cursor += idx + len(part)
	}
	return true
}

// MatchesA2AAllow returns whether the agent ID passes the A2A allow list.
func MatchesA2AAllow(p core.AgentToAgentPolicy, agentID string) bool {
	if len(p.Allow) == 0 {
		return true
	}
	for _, pattern := range p.Allow {
		if MatchAgentPattern(pattern, agentID) {
			return true
		}
	}
	return false
}

// IsA2AAllowed returns whether a cross-agent access from requester to
// target is permitted under the given policy.
func IsA2AAllowed(p core.AgentToAgentPolicy, requesterAgentID string, targetAgentID string) bool {
	if requesterAgentID == targetAgentID {
		return true
	}
	if !p.Enabled {
		return false
	}
	return MatchesA2AAllow(p, requesterAgentID) && MatchesA2AAllow(p, targetAgentID)
}

// ---------------------------------------------------------------------------
// Session access control — extracted from runtime/runtime_session.go
// ---------------------------------------------------------------------------

// SpawnTreeChecker abstracts the session store's ability to test whether
// a target session is visible in the requester's spawn tree.  The canonical
// implementation is *SessionStore.
type SpawnTreeChecker interface {
	IsSpawnedSessionVisible(requesterKey, targetKey string) bool
}

// IdentityResolver abstracts the ability to look up an agent identity by ID.
// The canonical implementation is core.IdentityResolver.
type IdentityResolver interface {
	Resolve(ctx context.Context, agentID string) (core.AgentIdentity, error)
}

// AccessPolicy carries the policy values needed by CheckAccess.
type AccessPolicy struct {
	Visibility core.SessionToolsVisibility
	A2A        core.AgentToAgentPolicy
}

// EffectiveVisibility determines the effective session visibility for a
// given requester, considering per-agent identity overrides.
func EffectiveVisibility(defaultVis core.SessionToolsVisibility, identities IdentityResolver, requesterSessionKey string) core.SessionToolsVisibility {
	visibility := defaultVis
	agentID := ResolveAgentIDFromSessionKey(requesterSessionKey)
	if identities != nil && agentID != "" {
		if identity, err := identities.Resolve(context.Background(), agentID); err == nil {
			switch strings.ToLower(strings.TrimSpace(identity.SandboxSessionVisibility)) {
			case "all":
				visibility = core.SessionVisibilityAll
			case "spawned":
				visibility = core.SessionVisibilityTree
			}
		}
	}
	return visibility
}

// CheckAccess evaluates whether a requester session may perform the given
// action on a target session.  This is a pure function: it does not
// depend on *Runtime, only on the provided policy, identity resolver, and
// spawn-tree checker.
func CheckAccess(
	policy AccessPolicy,
	identities IdentityResolver,
	spawnTree SpawnTreeChecker,
	action SessionAccessAction,
	requesterSessionKey, targetSessionKey string,
) SessionAccessResult {
	if strings.TrimSpace(targetSessionKey) == "" {
		return SessionAccessResult{Status: "error", Error: "missing target session"}
	}
	requesterSessionKey = strings.TrimSpace(requesterSessionKey)
	targetSessionKey = strings.TrimSpace(targetSessionKey)
	if requesterSessionKey == "" {
		return SessionAccessResult{Allowed: true}
	}
	visibility := NormalizeSessionVisibility(
		EffectiveVisibility(policy.Visibility, identities, requesterSessionKey),
	)
	requesterAgentID := ResolveAgentIDFromSessionKey(requesterSessionKey)
	targetAgentID := ResolveAgentIDFromSessionKey(targetSessionKey)
	isCrossAgent := requesterAgentID != targetAgentID
	if isCrossAgent {
		if visibility != core.SessionVisibilityAll {
			return SessionAccessResult{Status: "forbidden", Error: ActionPrefix(action) + " visibility is restricted. Set tools.sessions.visibility=all to allow cross-agent access."}
		}
		if !policy.A2A.Enabled {
			return SessionAccessResult{Status: "forbidden", Error: A2ADisabledMessage(action)}
		}
		if !IsA2AAllowed(policy.A2A, requesterAgentID, targetAgentID) {
			return SessionAccessResult{Status: "forbidden", Error: A2ADeniedMessage(action)}
		}
		return SessionAccessResult{Allowed: true}
	}
	switch visibility {
	case core.SessionVisibilitySelf:
		if requesterSessionKey != targetSessionKey {
			return MakeSessionAccessError(action, visibility)
		}
	case core.SessionVisibilityTree:
		if spawnTree != nil && requesterSessionKey != targetSessionKey && !spawnTree.IsSpawnedSessionVisible(requesterSessionKey, targetSessionKey) {
			return MakeSessionAccessError(action, visibility)
		}
	case core.SessionVisibilityAgent, core.SessionVisibilityAll:
		return SessionAccessResult{Allowed: true}
	}
	return SessionAccessResult{Allowed: true}
}
