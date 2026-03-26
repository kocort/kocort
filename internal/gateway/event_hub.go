// Package gateway — EventHub: in-memory SSE event hub for session events.
package gateway

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/core"
)

// EventRecord stores a single delivered reply for session history.
type EventRecord struct {
	Kind      core.ReplyKind      `json:"kind"`
	Payload   core.ReplyPayload   `json:"payload"`
	Target    core.DeliveryTarget `json:"target"`
	RunID     string              `json:"runId,omitempty"`
	CreatedAt time.Time           `json:"createdAt"`
}

// SSE event type constants for agent events.
const (
	// SSEEventMessage is for delivery record events (chat history).
	SSEEventMessage = "message"
	// SSEEventThinking is for reasoning/thinking stream deltas.
	SSEEventThinking = "thinking"
	// SSEEventThinkingComplete is for reasoning/thinking completion.
	SSEEventThinkingComplete = "thinking_complete"
	// SSEEventStreaming is for assistant text stream deltas.
	SSEEventStreaming = "streaming"
	// SSEEventMessageComplete is for finalized assistant messages.
	SSEEventMessageComplete = "message_complete"
	// SSEEventToolCall is for tool invocation events.
	SSEEventToolCall = "tool_call"
	// SSEEventDelivery is for message delivery events.
	SSEEventDelivery = "delivery"
	// SSEEventLifecycle is for lifecycle and system events.
	SSEEventLifecycle = "lifecycle"
)

// SSEEvent is an SSE event pushed to subscribers.
type SSEEvent struct {
	Event      string           `json:"event"`
	CreatedAt  time.Time        `json:"createdAt"`
	Record     *EventRecord     `json:"record,omitempty"`
	AgentEvent *core.AgentEvent `json:"agentEvent,omitempty"`
}

// EventHub is a thread-safe, in-memory pub/sub hub for session events.
// It supports SSE streaming to subscribers and maintains event history per session.
type EventHub struct {
	mu          sync.Mutex
	records     map[string][]EventRecord
	subscribers map[string]map[string]chan SSEEvent
	nextSubID   uint64
}

// NewEventHub creates a new, empty EventHub.
func NewEventHub() *EventHub {
	return &EventHub{
		records:     map[string][]EventRecord{},
		subscribers: map[string]map[string]chan SSEEvent{},
	}
}

// Record stores a reply and broadcasts it to all subscribers of the session.
func (h *EventHub) Record(target core.DeliveryTarget, kind core.ReplyKind, payload core.ReplyPayload) {
	if h == nil {
		return
	}
	sessionKey := strings.TrimSpace(target.SessionKey)
	if sessionKey == "" {
		return
	}
	h.mu.Lock()
	record := EventRecord{
		Kind:      kind,
		Payload:   payload,
		Target:    target,
		RunID:     target.RunID,
		CreatedAt: time.Now().UTC(),
	}
	h.records[sessionKey] = append(h.records[sessionKey], record)
	// Create SSE event for broadcasting
	sseEvent := SSEEvent{
		Event:     "message",
		CreatedAt: record.CreatedAt,
		Record:    &record,
	}
	subscribers := make([]chan SSEEvent, 0, len(h.subscribers[sessionKey]))
	for _, ch := range h.subscribers[sessionKey] {
		subscribers = append(subscribers, ch)
	}
	h.mu.Unlock()
	for _, ch := range subscribers {
		select {
		case ch <- sseEvent:
		default:
		}
	}
}

// ResolveSSEEventType maps an AgentEvent to a specific SSE event type based
// on its Stream and Data["type"] fields.
func ResolveSSEEventType(event core.AgentEvent) string {
	stream := strings.TrimSpace(strings.ToLower(event.Stream))
	dataType := ""
	if t, ok := event.Data["type"]; ok {
		if s, ok := t.(string); ok {
			dataType = strings.TrimSpace(strings.ToLower(s))
		}
	}

	switch stream {
	case "assistant":
		switch dataType {
		case "reasoning_delta":
			return SSEEventThinking
		case "reasoning_complete":
			return SSEEventThinkingComplete
		case "text_delta":
			return SSEEventStreaming
		case "final":
			return SSEEventMessageComplete
		default:
			return SSEEventStreaming
		}
	case "tool":
		return SSEEventToolCall
	case "delivery":
		return SSEEventDelivery
	default:
		// lifecycle, memory_flush, compaction, inbound, etc.
		return SSEEventLifecycle
	}
}

// EmitAgentEvent sends a typed agent event to all subscribers.
// The SSE event type is automatically resolved from the AgentEvent fields.
func (h *EventHub) EmitAgentEvent(sessionKey string, event core.AgentEvent) {
	if h == nil {
		return
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}
	h.mu.Lock()
	subscribers := make([]chan SSEEvent, 0, len(h.subscribers[sessionKey]))
	for _, ch := range h.subscribers[sessionKey] {
		subscribers = append(subscribers, ch)
	}
	h.mu.Unlock()
	out := SSEEvent{
		Event:      ResolveSSEEventType(event),
		CreatedAt:  time.Now().UTC(),
		AgentEvent: &event,
	}
	for _, ch := range subscribers {
		select {
		case ch <- out:
		default:
		}
	}
}

// History returns all recorded events for a session, sorted by time.
func (h *EventHub) History(sessionKey string) []EventRecord {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	history := append([]EventRecord{}, h.records[strings.TrimSpace(sessionKey)]...)
	sort.SliceStable(history, func(i, j int) bool {
		return history[i].CreatedAt.Before(history[j].CreatedAt)
	})
	return history
}

// Subscribe returns a channel that receives future events for the session
// and a cancel function to unsubscribe.
func (h *EventHub) Subscribe(sessionKey string) (<-chan SSEEvent, func()) {
	if h == nil {
		ch := make(chan SSEEvent)
		close(ch)
		return ch, func() {}
	}
	sessionKey = strings.TrimSpace(sessionKey)
	ch := make(chan SSEEvent, 128)
	h.mu.Lock()
	h.nextSubID++
	id := fmt.Sprintf("sub-%d", h.nextSubID)
	if h.subscribers[sessionKey] == nil {
		h.subscribers[sessionKey] = map[string]chan SSEEvent{}
	}
	h.subscribers[sessionKey][id] = ch
	h.mu.Unlock()
	cancel := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		subs := h.subscribers[sessionKey]
		if subs == nil {
			return
		}
		existing, ok := subs[id]
		if !ok {
			return
		}
		delete(subs, id)
		if len(subs) == 0 {
			delete(h.subscribers, sessionKey)
		}
		close(existing)
	}
	return ch, cancel
}

// EncodeSSEEvent serialises an SSEEvent to JSON bytes.
func EncodeSSEEvent(event SSEEvent) ([]byte, error) {
	return json.Marshal(event)
}

// ---------------------------------------------------------------------------
// Backward compatibility aliases
// ---------------------------------------------------------------------------

// WebchatRecord is an alias for EventRecord for backward compatibility.
// Deprecated: Use EventRecord instead.
type WebchatRecord = EventRecord

// WebchatEvent is an alias for SSEEvent for backward compatibility.
// Deprecated: Use SSEEvent instead.
type WebchatEvent = SSEEvent

// WebchatHub is an alias for EventHub for backward compatibility.
// Deprecated: Use EventHub instead.
type WebchatHub = EventHub

// NewWebchatHub creates a new EventHub (alias for backward compatibility).
// Deprecated: Use NewEventHub instead.
func NewWebchatHub() *EventHub {
	return NewEventHub()
}

// EncodeWebchatEvent is an alias for EncodeSSEEvent for backward compatibility.
// Deprecated: Use EncodeSSEEvent instead.
func EncodeWebchatEvent(event SSEEvent) ([]byte, error) {
	return EncodeSSEEvent(event)
}
