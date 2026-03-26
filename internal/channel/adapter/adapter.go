// Package adapter defines the unified channel adapter interface.
//
// Value types (InboundMessage, OutboundMessage, DeliveryResult, ChannelConfig,
// etc.) are type aliases re-exporting the canonical core and config types.
// This means adapter implementations can use adapter.OutboundMessage or
// core.ChannelOutboundMessage interchangeably — they are the same type.
//
// # Architecture
//
// Every channel driver implements a single [ChannelAdapter] interface that
// covers identity, initialization, inbound message handling, and outbound
// delivery. Text chunking is provided by an optional [TextChunkProvider].
//
// Inbound message flow is unified through a background lifecycle:
//
//  1. Runtime calls [ChannelAdapter.StartBackground] to begin the inbound
//     message lifecycle and register [Callbacks] (including the [InboundCallback]).
//  2. All inbound messages are delivered through the registered OnMessage callback.
//  3. Outbound messages are sent via [ChannelAdapter.SendText] and
//     [ChannelAdapter.SendMedia].
//  4. [ChannelAdapter.StopBackground] performs graceful shutdown.
//
// # Adding a New Channel
//
//  1. Create internal/channel/<driver>/ with an adapter.go file.
//  2. Embed [BaseAdapter], override only the methods you support.
//  3. Optionally implement [TextChunkProvider] for custom chunking.
//  4. Call [Register]("<driver>", factory) in init().
//  5. Add a blank import in runtime/channels.go.
package adapter

import (
	"context"
	"errors"
	"net/http"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/infra"
)

// =========================================================================
// Type aliases — use core and config types directly.
//
// Adapter implementations can reference these aliases (e.g. adapter.OutboundMessage)
// or use the canonical core/config types interchangeably.
// =========================================================================

// ChatType is an alias for core.ChatType.
type ChatType = core.ChatType

// Re-export ChatType constants so adapter implementations can use adapter.ChatType*.
const (
	ChatTypeDirect = core.ChatTypeDirect
	ChatTypeGroup  = core.ChatTypeGroup
	ChatTypeThread = core.ChatTypeThread
	ChatTypeTopic  = core.ChatTypeTopic
)

// Attachment is an alias for core.Attachment.
type Attachment = core.Attachment

// InboundMessage is an alias for core.ChannelInboundMessage.
type InboundMessage = core.ChannelInboundMessage

// ReplyPayload is an alias for core.ReplyPayload.
type ReplyPayload = core.ReplyPayload

// OutboundMessage is an alias for core.ChannelOutboundMessage.
type OutboundMessage = core.ChannelOutboundMessage

// DeliveryResult is an alias for core.ChannelDeliveryResult.
type DeliveryResult = core.ChannelDeliveryResult

// ChannelConfig is an alias for config.ChannelConfig.
type ChannelConfig = config.ChannelConfig

// =========================================================================
// Enumerations (adapter-specific)
// =========================================================================

// DeliveryMode describes how a channel receives inbound messages.
type DeliveryMode string

const (
	DeliveryDirect    DeliveryMode = "direct"    // HTTP webhook-based
	DeliveryPolled    DeliveryMode = "polled"     // long-poll background
	DeliveryWebSocket DeliveryMode = "websocket"  // WebSocket background
)

// =========================================================================
// Adapter-specific types
// =========================================================================

// AuditEntry represents an audit/observability event emitted by adapters.
// The channel registry converts these into the system's core audit events.
type AuditEntry struct {
	Category string         // e.g. "channel"
	Type     string         // e.g. "feishu_websocket_started"
	Level    string         // "info", "warn", "error"
	Channel  string         // channel ID
	Message  string         // human-readable description
	Data     map[string]any // structured payload
}

// =========================================================================
// Callbacks — runtime injects these via StartBackground
// =========================================================================

// InboundCallback is the function signature for delivering an inbound
// message to the runtime pipeline. Adapters call this from background
// goroutines after parsing the platform payload.
//
// Returns an error if the runtime rejects or fails to process the message.
// Adapters may use the error to decide whether to retry.
type InboundCallback func(ctx context.Context, msg InboundMessage) error

// AuditCallback is the function signature for recording an audit event.
// Adapters call this to report connection state changes, errors, etc.
type AuditCallback func(ctx context.Context, entry AuditEntry)

// Callbacks aggregates all runtime callbacks injected into an adapter
// during [ChannelAdapter.StartBackground]. This is the adapter's only
// way to communicate back to the runtime.
type Callbacks struct {
	// OnMessage delivers an inbound message to the runtime pipeline.
	// Required — MUST NOT be nil.
	OnMessage InboundCallback

	// OnAudit records an audit/observability event.
	// Optional — may be nil; adapters should nil-check before calling.
	OnAudit AuditCallback
}

// =========================================================================
// Errors
// =========================================================================

var (
	// ErrUnauthorized indicates an inbound request failed authentication.
	ErrUnauthorized = errors.New("unauthorized")

	// ErrTargetRequired indicates a missing outbound target.
	ErrTargetRequired = errors.New("outbound target is required")

	// ErrTokenRequired indicates missing credentials.
	ErrTokenRequired = errors.New("token or credential is required")

	// ErrNotImplemented indicates a method is not supported by the adapter.
	ErrNotImplemented = errors.New("not implemented")

	// ErrNotStarted indicates StartBackground has not been called yet.
	ErrNotStarted = errors.New("adapter not started")
)

// =========================================================================
// Driver Schema — configuration metadata returned by Init
// =========================================================================

// ChannelConfigFieldType represents the data type of a configuration field.
type ChannelConfigFieldType string

const (
	FieldTypeText     ChannelConfigFieldType = "text"
	FieldTypePassword ChannelConfigFieldType = "password"
	FieldTypeSelect   ChannelConfigFieldType = "select"
	FieldTypeCheckbox ChannelConfigFieldType = "checkbox"
	FieldTypeNumber   ChannelConfigFieldType = "number"
)

// ChannelConfigField defines a configuration field for a channel.
type ChannelConfigField struct {
	Key          string                 `json:"key"`
	Label        string                 `json:"label"`
	Type         ChannelConfigFieldType `json:"type"`
	Required     bool                   `json:"required"`
	Placeholder  string                 `json:"placeholder,omitempty"`
	DefaultValue string                 `json:"defaultValue,omitempty"`
	Options      []SelectOption         `json:"options,omitempty"` // for select type
	Help         string                 `json:"help,omitempty"`
	Group        string                 `json:"group,omitempty"` // for grouping fields (e.g., "account", "webhook")
}

// SelectOption represents an option in a select field.
type SelectOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// ChannelDriverSchema defines the configuration schema for a channel driver.
type ChannelDriverSchema struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	Fields      []ChannelConfigField `json:"fields"`
}

// =========================================================================
// ChannelAdapter — the unified adapter interface
// =========================================================================

// ChannelAdapter is the single interface every channel driver must implement.
// It unifies identity, initialization, inbound handling, and outbound
// delivery into one contract.
//
// Because the value types (OutboundMessage, DeliveryResult, etc.) are
// aliases for core/config types, adapters automatically satisfy the
// delivery layer's ChannelTextSender / ChannelMediaSender interfaces
// without any bridge or wrapper.
//
// # Lifecycle
//
//  1. Runtime calls StartBackground(ctx, channelID, cfg, dc, cb)
//     to begin the inbound message lifecycle.
//     ALL received messages MUST be delivered via cb.OnMessage.
//
//  2. For WebSocket/long-poll adapters (DeliveryWebSocket / DeliveryPolled):
//     background goroutines receive platform messages and deliver them via
//     cb.OnMessage.
//
//  3. StopBackground performs graceful shutdown of all goroutines.
//
// # HTTP Ingress
//
// Adapters that receive inbound messages via HTTP webhooks implement
// ServeHTTP. The runtime mounts the adapter at the channel's webhook
// path after StartBackground is called.
//
// # Text Chunking
//
// Adapters that need custom text chunking should also implement
// [TextChunkProvider].
//
// # Default Implementation
//
// Embed [BaseAdapter] to get safe no-op defaults for every method.
// Override only the methods your adapter actually supports.
type ChannelAdapter interface {
	// ---- Identity ----

	// ID returns the adapter's driver identifier (e.g. "feishu", "discord").
	ID() string

	// ---- Schema ----

	// Schema returns the driver schema describing identity and configurable fields.
	Schema() ChannelDriverSchema

	// ---- Inbound Lifecycle ----

	// StartBackground begins the inbound message lifecycle.
	// ALL received messages MUST be delivered via cb.OnMessage.
	//
	// For background adapters (WebSocket / long-poll):
	//   Starts background goroutines that connect to the platform.
	//   Should return immediately; ctx cancellation signals shutdown.
	//
	// Calling StartBackground on an already-started adapter should be
	// safe (idempotent or restart with new config).
	StartBackground(ctx context.Context, channelID string, cfg ChannelConfig, dc *infra.DynamicHTTPClient, cb Callbacks) error

	// StopBackground gracefully stops all background goroutines and
	// releases resources. Called when channel config is updated (before
	// a fresh StartBackground) or when the system is shutting down.
	StopBackground()

	// ---- HTTP Ingress ----

	// ServeHTTP handles inbound HTTP webhook requests from the platform.
	// The runtime mounts this at the channel's webhook path.
	ServeHTTP(w http.ResponseWriter, r *http.Request)

	// ---- Outbound ----

	// SendText sends a text message to the platform.
	SendText(ctx context.Context, msg OutboundMessage, cfg ChannelConfig) (DeliveryResult, error)

	// SendMedia sends a media message (images, files, audio, video, etc.)
	// to the platform. May also include accompanying text in the payload.
	SendMedia(ctx context.Context, msg OutboundMessage, cfg ChannelConfig) (DeliveryResult, error)
}

// =========================================================================
// TextChunkProvider — optional text chunking interface
// =========================================================================

// TextChunkProvider is an optional interface that adapters may implement
// to provide custom text chunking logic. The delivery layer checks whether
// the adapter satisfies this interface and delegates accordingly.
type TextChunkProvider interface {
	// ChunkText splits text into platform-appropriate chunks.
	// The delivery layer delegates to this instead of its built-in splitter.
	ChunkText(text string, limit int) []string

	// ChunkerMode returns the preferred chunking strategy
	// (e.g. "markdown", "text", "length").
	ChunkerMode() string
}

// =========================================================================
// ChannelIntegrationSummary — snapshot for API / dashboard
// =========================================================================

// ChannelIntegrationSummary is a read-only snapshot of a channel's state,
// used by the API and dashboard to display channel status.
type ChannelIntegrationSummary struct {
	ID                     string   `json:"id"`
	Enabled                bool     `json:"enabled"`
	Agent                  string   `json:"agent,omitempty"`
	DefaultTo              string   `json:"defaultTo,omitempty"`
	DefaultAccount         string   `json:"defaultAccount,omitempty"`
	AllowFrom              []string `json:"allowFrom,omitempty"`
	TextChunkLimit         int      `json:"textChunkLimit,omitempty"`
	ChunkMode              string   `json:"chunkMode,omitempty"`
}
