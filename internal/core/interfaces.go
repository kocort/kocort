package core

import "context"

// ---------------------------------------------------------------------------
// Core Interfaces — These interfaces have NO dependency on *Runtime and can
// be implemented by any package without creating circular imports.
// ---------------------------------------------------------------------------

// IdentityResolver resolves an agent identity by ID.
type IdentityResolver interface {
	Resolve(ctx context.Context, agentID string) (AgentIdentity, error)
}

// MemoryProvider recalls relevant memory for a given agent context.
type MemoryProvider interface {
	Recall(ctx context.Context, identity AgentIdentity, session SessionResolution, message string) ([]MemoryHit, error)
}

// Deliverer delivers a reply payload to a target.
type Deliverer interface {
	Deliver(ctx context.Context, kind ReplyKind, payload ReplyPayload, target DeliveryTarget) error
}

// ToolMetadataProvider provides registration metadata for a tool.
type ToolMetadataProvider interface {
	ToolRegistrationMeta() ToolRegistrationMeta
}

// OpenAIFunctionToolProvider provides OpenAI function-calling schema.
type OpenAIFunctionToolProvider interface {
	OpenAIFunctionTool() *OpenAIFunctionToolSchema
}

// AcpRuntime defines the Agent Communication Protocol runtime interface.
type AcpRuntime interface {
	EnsureSession(ctx context.Context, input AcpEnsureSessionInput) (AcpRuntimeHandle, error)
	RunTurn(ctx context.Context, input AcpRunTurnInput) error
	GetCapabilities(ctx context.Context, handle *AcpRuntimeHandle) (AcpRuntimeCapabilities, error)
	GetStatus(ctx context.Context, handle AcpRuntimeHandle) (AcpRuntimeStatus, error)
	SetMode(ctx context.Context, input AcpSetModeInput) error
	SetConfigOption(ctx context.Context, input AcpSetConfigOptionInput) error
	Cancel(ctx context.Context, input AcpCancelInput) error
	Close(ctx context.Context, input AcpCloseInput) error
}
