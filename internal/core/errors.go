package core

import "errors"

// ---------------------------------------------------------------------------
// Sentinel Errors — Runtime
// ---------------------------------------------------------------------------

var (
	// ErrRuntimeNotReady indicates that the runtime has not been fully
	// initialised and cannot serve requests yet.
	ErrRuntimeNotReady = errors.New("runtime is not fully configured")

	// ErrMessageRequired is returned when a chat request contains an
	// empty or blank message.
	ErrMessageRequired = errors.New("message is required")

	// ErrAgentNotFound is returned when the requested agent identity
	// cannot be resolved.
	ErrAgentNotFound = errors.New("agent not found")

	// ErrSessionNotFound is returned when a session key does not map
	// to an existing session.
	ErrSessionNotFound = errors.New("session not found")

	// ErrSessionExpired is returned when a session exists but has been
	// marked as expired.
	ErrSessionExpired = errors.New("session expired")

	// ErrUnauthorized is returned when authentication/authorization
	// checks fail.
	ErrUnauthorized = errors.New("unauthorized")
)

// ---------------------------------------------------------------------------
// Sentinel Errors — Tool
// ---------------------------------------------------------------------------

var (
	// ErrToolNotFound is returned when a tool name cannot be resolved.
	ErrToolNotFound = errors.New("tool not found")

	// ErrToolExecutionFailed indicates a generic tool execution failure.
	ErrToolExecutionFailed = errors.New("tool execution failed")

	// ErrToolApprovalRequired is returned when a tool invocation
	// requires user approval before proceeding.
	ErrToolApprovalRequired = errors.New("tool requires approval")

	// ErrToolLoopDetected is returned when the tool-loop detector
	// identifies a repetitive call pattern.
	ErrToolLoopDetected = errors.New("tool loop detected")

	// ErrToolNotAllowed is returned when a tool is denied by
	// policy (allowlist, denylist, elevated, sandbox, plugin disabled).
	ErrToolNotAllowed = errors.New("tool not allowed")

	// ErrProcessNotConfigured is returned when a tool requires the
	// process registry but it has not been initialised.
	ErrProcessNotConfigured = errors.New("process registry is not configured")

	// ErrToolRegistryNotConfigured is returned when the tool registry
	// is nil.
	ErrToolRegistryNotConfigured = errors.New("tool registry is not configured")
)

// ---------------------------------------------------------------------------
// Sentinel Errors — Backend / Model
// ---------------------------------------------------------------------------

var (
	// ErrProviderNotFound is returned when a provider ID does not match
	// any configured provider.
	ErrProviderNotFound = errors.New("provider not found")

	// ErrModelNotFound is returned when a model ID does not match any
	// configured model within a provider.
	ErrModelNotFound = errors.New("model not found")

	// ErrContextOverflow is returned when the context window has been
	// exceeded and compaction/pruning cannot recover.
	ErrContextOverflow = errors.New("context window overflow")

	// ErrProviderEmptyResponse is returned when a provider returns an
	// empty (nil/blank) response.
	ErrProviderEmptyResponse = errors.New("provider returned empty response")

	// ErrNoConfiguredProviders is returned when no model providers are
	// configured.
	ErrNoConfiguredProviders = errors.New("no configured providers")

	// ErrNoDefaultModelConfigured is returned when no usable default model
	// is configured for the current agent/session.
	ErrNoDefaultModelConfigured = errors.New("no default model configured")
)

// ---------------------------------------------------------------------------
// Sentinel Errors — Config
// ---------------------------------------------------------------------------

var (
	// ErrConfigInvalid indicates a structural validation failure in the
	// application configuration.
	ErrConfigInvalid = errors.New("invalid configuration")

	// ErrConfigLoadFailed indicates that loading a configuration file
	// from disk failed.
	ErrConfigLoadFailed = errors.New("failed to load configuration")
)

// ---------------------------------------------------------------------------
// Sentinel Errors — Task
// ---------------------------------------------------------------------------

var (
	// ErrTaskNotFound is returned when a task ID cannot be resolved.
	ErrTaskNotFound = errors.New("task not found")

	// ErrTaskAlreadyRunning is returned when a duplicate task execution
	// is attempted.
	ErrTaskAlreadyRunning = errors.New("task is already running")

	// ErrTaskSchedulerNotConfigured is returned when the task scheduler
	// has not been initialised.
	ErrTaskSchedulerNotConfigured = errors.New("task scheduler is not configured")
)

// ---------------------------------------------------------------------------
// Sentinel Errors — Channel / Delivery
// ---------------------------------------------------------------------------

var (
	// ErrChannelRegistryNotConfigured is returned when the channel
	// registry is nil.
	ErrChannelRegistryNotConfigured = errors.New("channel registry is not configured")

	// ErrChannelRequired is returned when a channel identifier is
	// missing from the request.
	ErrChannelRequired = errors.New("channel is required")

	// ErrChannelNotRegistered is returned when a channel ID does not
	// match any registered channel transport.
	ErrChannelNotRegistered = errors.New("channel not registered")

	// ErrDelivererNotConfigured is returned when the delivery subsystem
	// has not been initialised.
	ErrDelivererNotConfigured = errors.New("router deliverer is not configured")
)

// ---------------------------------------------------------------------------
// Sentinel Errors — Subsystem
// ---------------------------------------------------------------------------

var (
	// ErrSystemEventsNotConfigured is returned when the system event
	// queue is nil.
	ErrSystemEventsNotConfigured = errors.New("system event queue is not configured")

	// ErrSubagentRegistryNotConfigured is returned when the subagent
	// registry is nil.
	ErrSubagentRegistryNotConfigured = errors.New("subagent registry is not configured")
)

// ---------------------------------------------------------------------------
// Sentinel Errors — ACP
// ---------------------------------------------------------------------------

var (
	// ErrACPNotConfigured is returned when ACP runtime is not
	// initialised.
	ErrACPNotConfigured = errors.New("ACP runtime is not configured")

	// ErrACPSessionKeyRequired is returned when an ACP operation is
	// missing the session key.
	ErrACPSessionKeyRequired = errors.New("ACP session key is required")

	// ErrACPDisabledByPolicy is returned when ACP is disabled by the
	// current security policy.
	ErrACPDisabledByPolicy = errors.New("ACP is disabled by policy")
)
