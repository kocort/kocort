package session

import (
	"testing"

	"github.com/kocort/kocort/internal/core"
)

func TestApplySessionRouteBindingPersistsDeliveryContext(t *testing.T) {
	entry := ApplySessionRouteBinding(core.SessionEntry{}, SessionRouteBinding{
		Channel:   "slack",
		To:        "room-1",
		AccountID: "bot-1",
		ThreadID:  "thread-9",
	})
	if entry.LastChannel != "slack" || entry.LastTo != "room-1" || entry.LastAccountID != "bot-1" || entry.LastThreadID != "thread-9" {
		t.Fatalf("unexpected entry: %+v", entry)
	}
	if entry.DeliveryContext == nil || entry.DeliveryContext.ThreadID != "thread-9" {
		t.Fatalf("expected delivery context, got %+v", entry.DeliveryContext)
	}
}

func TestApplyRunRouteBindingCopiesFallbackRoute(t *testing.T) {
	req := ApplyRunRouteBinding(core.AgentRunRequest{}, SessionRouteBinding{
		Channel:  "telegram",
		To:       "chat-1",
		ThreadID: "42",
	})
	if req.Channel != "telegram" || req.To != "chat-1" || req.ThreadID != "42" {
		t.Fatalf("unexpected request: %+v", req)
	}
}
