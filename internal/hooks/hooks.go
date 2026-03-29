// Package hooks provides an extensible internal hook system for agent
// lifecycle events (bootstrap, tool execution, message processing, etc.).
//
// Hooks are registered via RegisterHook and dispatched with Trigger.
// attach behaviour to lifecycle events (e.g. self-improving-agent).
package hooks

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// EventType represents the high-level category of a hook event.
type EventType string

const (
	EventAgent   EventType = "agent"
	EventTool    EventType = "tool"
	EventMessage EventType = "message"
	EventSession EventType = "session"
	EventGateway EventType = "gateway"
	EventCommand EventType = "command"
)

// Event carries the payload dispatched to hook handlers.
type Event struct {
	// Type is the high-level event category (agent, tool, message, session, gateway, command).
	Type EventType `json:"type"`

	// Action is the specific action within the event type.
	// Examples: "bootstrap", "pre_execute", "post_execute", "received", "sent".
	Action string `json:"action"`

	// SessionKey identifies the session that triggered this event.
	SessionKey string `json:"sessionKey"`

	// Timestamp records when the event was created.
	Timestamp time.Time `json:"timestamp"`

	// Context carries action-specific data (mutable by handlers).
	Context map[string]any `json:"context"`

	// Messages is an output slice that handlers may append to.
	// The caller can inject the collected messages into the agent prompt.
	Messages []string `json:"messages,omitempty"`
}

// Handler is a function invoked when an event is triggered.
// Handlers must be safe for concurrent use; Trigger serialises calls
// within a single dispatch but the same handler may be invoked from
// multiple goroutines for different events.
type Handler func(ctx context.Context, event *Event) error

// Registry holds registered hook handlers keyed by event key.
// Event keys follow the "type" or "type:action" convention.
type Registry struct {
	mu       sync.RWMutex
	handlers map[string][]Handler
}

// NewRegistry creates an empty hook registry.
func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string][]Handler)}
}

// Register adds a handler for the given event key.
// The key can be a bare type ("tool") to match all actions, or a
// qualified "type:action" string ("tool:post_execute") for a specific action.
func (r *Registry) Register(eventKey string, h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[eventKey] = append(r.handlers[eventKey], h)
}

// Unregister removes a specific handler for the given event key.
func (r *Registry) Unregister(eventKey string, h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.handlers[eventKey]
	for i, existing := range list {
		// Compare function pointers.
		if &existing == &h {
			r.handlers[eventKey] = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(r.handlers[eventKey]) == 0 {
		delete(r.handlers, eventKey)
	}
}

// Clear removes all registered handlers.
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers = make(map[string][]Handler)
}

// RegisteredKeys returns all event keys that have handlers. Useful for debugging.
func (r *Registry) RegisteredKeys() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.handlers))
	for k := range r.handlers {
		keys = append(keys, k)
	}
	return keys
}

// Trigger dispatches an event to all matching handlers.
// Handlers registered for the bare type and for "type:action" are both invoked.
// Errors from individual handlers are logged but do not prevent subsequent
// handlers from running.
func (r *Registry) Trigger(ctx context.Context, event *Event) {
	r.mu.RLock()
	typeKey := string(event.Type)
	specificKey := typeKey + ":" + event.Action
	typeHandlers := copyHandlers(r.handlers[typeKey])
	specificHandlers := copyHandlers(r.handlers[specificKey])
	r.mu.RUnlock()

	all := append(typeHandlers, specificHandlers...)
	if len(all) == 0 {
		return
	}

	for _, h := range all {
		if err := h(ctx, event); err != nil {
			slog.Warn("[hooks] handler error",
				"type", event.Type,
				"action", event.Action,
				"error", err)
		}
	}
}

// HasHandlers reports whether any handlers are registered for the event's
// type or type:action key. Useful for callers that want to skip building
// the Event when no handlers are listening.
func (r *Registry) HasHandlers(eventType EventType, action string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.handlers[string(eventType)]) > 0 {
		return true
	}
	return len(r.handlers[string(eventType)+":"+action]) > 0
}

func copyHandlers(src []Handler) []Handler {
	if len(src) == 0 {
		return nil
	}
	dst := make([]Handler, len(src))
	copy(dst, src)
	return dst
}

// NewEvent is a convenience constructor for creating an Event.
func NewEvent(eventType EventType, action, sessionKey string, eventCtx map[string]any) *Event {
	if eventCtx == nil {
		eventCtx = make(map[string]any)
	}
	return &Event{
		Type:       eventType,
		Action:     action,
		SessionKey: sessionKey,
		Timestamp:  time.Now(),
		Context:    eventCtx,
	}
}
