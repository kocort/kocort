// interfaces.go — Domain service interfaces for the runtime layer.
//
// These interfaces decouple the Runtime orchestrator from concrete
// implementations, enabling independent testing and future substitution
// of subsystems (e.g. in-memory session stores, mock backends, etc.).
//
// Go convention: interfaces are defined by the consumer. The runtime/
// package consumes these abstractions, while the concrete implementations
// live in their respective internal/* packages.
package runtime

import (
	"context"

	"github.com/kocort/kocort/internal/backend"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/gateway"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/internal/session"
)

// ---------------------------------------------------------------------------
// SessionManager — abstracts session.SessionStore
// ---------------------------------------------------------------------------

// SessionManager defines the session operations needed by the runtime
// pipeline. The canonical implementation is *session.SessionStore.
type SessionManager interface {
	ResolveForRequest(ctx context.Context, opts session.SessionResolveOptions) (core.SessionResolution, error)
	LoadTranscript(key string) ([]core.TranscriptMessage, error)
	AppendTranscript(key, sessionID string, msgs ...core.TranscriptMessage) error
	RewriteTranscript(key, sessionID string, msgs []core.TranscriptMessage) error
	Upsert(key string, entry core.SessionEntry) error
	Mutate(key string, fn func(*core.SessionEntry) error) error
	Reset(key, reason string) (string, error)
	Delete(key string) error
	BaseDir() string
	Entry(key string) *core.SessionEntry
	ListSessions() []session.SessionListItem
	IsSpawnedSessionVisible(requesterKey, targetKey string) bool
	AllEntries() map[string]core.SessionEntry
	ResolveSessionKeyReference(reference string) (string, bool)
	ResolveSessionLabel(agentID, label, spawnedBy string) (string, bool)
	SetMaintenanceConfig(cfg session.SessionStoreMaintenanceConfig)
}

// ---------------------------------------------------------------------------
// BackendResolver — abstracts backend.BackendRegistry
// ---------------------------------------------------------------------------

// BackendResolver resolves a model backend by provider identifier.
// The canonical implementation is *backend.BackendRegistry.
type BackendResolver interface {
	Resolve(provider string) (rtypes.Backend, string, error)
}

// ---------------------------------------------------------------------------
// EventBus — abstracts gateway.EventHub for event publishing
// ---------------------------------------------------------------------------

// EventBus publishes agent events and debug events to subscribers.
// Defined in internal/event and re-exported here as a type alias.
type EventBus = event.EventBus

// ---------------------------------------------------------------------------
// AuditRecorder — abstracts infra.AuditLog
// ---------------------------------------------------------------------------

// AuditRecorder records and queries audit events.
// Defined in internal/event and re-exported here as a type alias.
type AuditRecorder = event.AuditRecorder

// ---------------------------------------------------------------------------
// RuntimeLogReloader — abstracts infra.SlogAuditLogger
// ---------------------------------------------------------------------------

// RuntimeLogReloader describes the logging operations needed by the runtime.
// The canonical implementation is *infra.SlogAuditLogger.
type RuntimeLogReloader interface {
	LogAuditEvent(event core.AuditEvent)
	Reload(cfg config.LoggingConfig, stateDir string) error
}

// ---------------------------------------------------------------------------
// EnvironmentResolver — abstracts infra.EnvironmentRuntime
// ---------------------------------------------------------------------------

// EnvironmentResolver resolves environment variable references.
// The canonical implementation is *infra.EnvironmentRuntime.
type EnvironmentResolver interface {
	Resolve(key string) (string, bool)
	ResolveString(raw string) (string, error)
	ResolveMap(values map[string]string) (map[string]string, error)
	Snapshot(masked bool) map[string]string
	Reload(cfg config.EnvironmentConfig)
}

// ---------------------------------------------------------------------------
// Compile-time interface satisfaction assertions
// ---------------------------------------------------------------------------

var (
	_ SessionManager      = (*session.SessionStore)(nil)
	_ BackendResolver     = (*backend.BackendRegistry)(nil)
	_ EventBus            = (*gateway.EventHub)(nil)
	_ AuditRecorder       = (*infra.AuditLog)(nil)
	_ RuntimeLogReloader  = (*infra.SlogAuditLogger)(nil)
	_ EnvironmentResolver = (*infra.EnvironmentRuntime)(nil)
)
