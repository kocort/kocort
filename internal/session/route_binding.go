package session

import (
	"strings"

	"github.com/kocort/kocort/internal/core"
)

// SessionRouteBinding captures requester-side delivery metadata that a child
// session may need to persist for future follow-up routing.
type SessionRouteBinding struct {
	Channel   string
	To        string
	AccountID string
	ThreadID  string
}

// ResolveSessionRouteBinding normalizes a run request's current route.
func ResolveSessionRouteBinding(req core.AgentRunRequest) SessionRouteBinding {
	return SessionRouteBinding{
		Channel:   strings.TrimSpace(req.Channel),
		To:        strings.TrimSpace(req.To),
		AccountID: strings.TrimSpace(req.AccountID),
		ThreadID:  strings.TrimSpace(req.ThreadID),
	}
}

// ApplySessionRouteBinding writes route metadata onto a session entry so later
// delivery can recover the child session's stable route.
func ApplySessionRouteBinding(entry core.SessionEntry, binding SessionRouteBinding) core.SessionEntry {
	if binding.Channel == "" && binding.To == "" && binding.AccountID == "" && binding.ThreadID == "" {
		return entry
	}
	entry.LastChannel = firstNonEmptyRoute(strings.TrimSpace(entry.LastChannel), binding.Channel)
	entry.LastTo = firstNonEmptyRoute(strings.TrimSpace(entry.LastTo), binding.To)
	entry.LastAccountID = firstNonEmptyRoute(strings.TrimSpace(entry.LastAccountID), binding.AccountID)
	entry.LastThreadID = firstNonEmptyRoute(strings.TrimSpace(entry.LastThreadID), binding.ThreadID)
	if entry.DeliveryContext == nil {
		entry.DeliveryContext = &core.DeliveryContext{}
	}
	if entry.DeliveryContext.Channel == "" {
		entry.DeliveryContext.Channel = binding.Channel
	}
	if entry.DeliveryContext.To == "" {
		entry.DeliveryContext.To = binding.To
	}
	if entry.DeliveryContext.AccountID == "" {
		entry.DeliveryContext.AccountID = binding.AccountID
	}
	if entry.DeliveryContext.ThreadID == "" {
		entry.DeliveryContext.ThreadID = binding.ThreadID
	}
	return entry
}

// ApplyRunRouteBinding copies normalized route metadata onto a child request.
func ApplyRunRouteBinding(req core.AgentRunRequest, binding SessionRouteBinding) core.AgentRunRequest {
	req.Channel = firstNonEmptyRoute(strings.TrimSpace(req.Channel), binding.Channel)
	req.To = firstNonEmptyRoute(strings.TrimSpace(req.To), binding.To)
	req.AccountID = firstNonEmptyRoute(strings.TrimSpace(req.AccountID), binding.AccountID)
	req.ThreadID = firstNonEmptyRoute(strings.TrimSpace(req.ThreadID), binding.ThreadID)
	return req
}

func firstNonEmptyRoute(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}
