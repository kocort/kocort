// Package event provides domain-agnostic event-emission and audit-recording
// utilities consumed by the runtime and other packages.
//
// AuditRecorder and EventBus are the two primary service interfaces.
// Concrete implementations live in their respective packages:
//   - AuditRecorder → internal/infra.AuditLog
//   - EventBus      → internal/gateway.EventHub
//   - AuditLogger   → internal/infra.SlogAuditLogger
package event

import (
	"context"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/gateway"
)

// AuditRecorder records and queries audit events.
// The canonical implementation is *infra.AuditLog.
type AuditRecorder interface {
	Record(ctx context.Context, event core.AuditEvent) error
	List(ctx context.Context, query core.AuditQuery) ([]core.AuditEvent, error)
}

// EventBus publishes agent events and debug events to subscribers.
// The canonical implementation is *gateway.EventHub.
type EventBus interface {
	EmitAgentEvent(sessionKey string, event core.AgentEvent)
	Subscribe(sessionKey string) (<-chan gateway.SSEEvent, func())
}

// AuditLogger logs audit events to structured log output.
// The canonical implementation is *infra.SlogAuditLogger.
type AuditLogger interface {
	LogAuditEvent(event core.AuditEvent)
}
