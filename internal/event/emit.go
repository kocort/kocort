package event

import (
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/utils"
)

// EmitAgentEvent publishes an agent event to the event bus.
// It is a no-op when bus is nil or the session key is empty.
func EmitAgentEvent(bus EventBus, event core.AgentEvent) {
	if bus == nil {
		return
	}
	sessionKey := strings.TrimSpace(event.SessionKey)
	if sessionKey == "" {
		return
	}
	bus.EmitAgentEvent(sessionKey, event)
}

// EmitDebugEvent publishes a debug event to the event bus.
// It is a no-op when bus is nil or sessionKey is empty.
func EmitDebugEvent(bus EventBus, sessionKey, runID, stream string, data map[string]any) {
	if bus == nil {
		return
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}
	EmitAgentEvent(bus, core.AgentEvent{
		RunID:      strings.TrimSpace(runID),
		Stream:     strings.TrimSpace(stream),
		OccurredAt: time.Now().UTC(),
		SessionKey: sessionKey,
		Data:       utils.CloneAnyMap(data),
	})
}
