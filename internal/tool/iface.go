// iface.go — canonical definitions of the runtime-facing types that were
// originally in internal/rtypes.  They live here now so that built-in tool
// implementations (also in package tool) can reference them without creating
// an import cycle (tool → rtypes → tool).
//
// The rtypes package re-exports these as type aliases for backward
// compatibility.
package tool

import (
	"context"

	"github.com/kocort/kocort/internal/acp"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/task"
)

// ---------------------------------------------------------------------------
// RuntimeServices — the narrow interface that tools, backends and planners
// use to call back into the runtime.  Replaces the former *runtime.Runtime
// concrete pointer that made cross-package imports impossible.
// ---------------------------------------------------------------------------

// RuntimeServices exposes the subset of runtime functionality needed by
// tools, backends, and planners.  The concrete *runtime.Runtime satisfies
// this interface.
type RuntimeServices interface {
	// ---- Agent orchestration ----

	// Run executes a full agent turn.
	Run(ctx context.Context, req core.AgentRunRequest) (core.AgentRunResult, error)

	// SpawnSubagent creates and starts a child agent session.
	SpawnSubagent(ctx context.Context, req task.SubagentSpawnRequest) (task.SubagentSpawnResult, error)

	// SpawnACPSession creates and starts an ACP child session.
	SpawnACPSession(ctx context.Context, req acp.SessionSpawnRequest) (acp.SessionSpawnResult, error)

	// PushInbound routes an inbound channel message through the runtime.
	PushInbound(ctx context.Context, msg core.ChannelInboundMessage) (core.ChatSendResponse, error)

	// ---- Tool execution ----

	// ExecuteTool invokes a registered tool by name.
	ExecuteTool(ctx context.Context, runCtx AgentRunContext, name string, args map[string]any) (core.ToolResult, error)

	// ---- Task management ----

	// ScheduleTask registers or updates a scheduled/subagent task.
	ScheduleTask(ctx context.Context, req task.TaskScheduleRequest) (core.TaskRecord, error)

	// ListTasks returns all known task records.
	ListTasks(ctx context.Context) []core.TaskRecord

	// GetTask returns a single task by ID (nil when not found).
	GetTask(ctx context.Context, taskID string) *core.TaskRecord

	// CancelTask cancels a running or scheduled task.
	CancelTask(ctx context.Context, taskID string) (*core.TaskRecord, error)

	// ---- Event / audit ----

	// GetAudit returns the audit recorder used to persist audit events.
	GetAudit() event.AuditRecorder

	// GetEventBus returns the event bus used to publish agent/debug events.
	GetEventBus() event.EventBus

	// ---- Session access control ----

	// CheckSessionAccess evaluates whether a requester session may perform
	// the given action on a target session.
	CheckSessionAccess(action session.SessionAccessAction, requesterKey, targetKey string) session.SessionAccessResult

	// ---- Model resolution ----

	// ResolveModelSelection resolves the model selection for a given
	// identity, request and session.
	ResolveModelSelection(ctx context.Context, identity core.AgentIdentity, req core.AgentRunRequest, session core.SessionResolution) (core.ModelSelection, error)

	// ---- Subsystem accessors ----
	// These return the concrete subsystem instances so that tools and
	// backends can call subsystem-specific methods (e.g. Sessions.LoadTranscript).

	// GetSessions returns the session store (may be nil).
	GetSessions() *session.SessionStore

	// GetIdentities returns the identity resolver (may be nil).
	GetIdentities() core.IdentityResolver

	// GetProcesses returns the background-process registry (may be nil).
	GetProcesses() *ProcessRegistry

	// GetMemory returns the memory provider (may be nil).
	GetMemory() core.MemoryProvider

	// GetSubagents returns the subagent registry (may be nil).
	GetSubagents() *task.SubagentRegistry

	// GetActiveRuns returns the active-run registry (may be nil).
	GetActiveRuns() *task.ActiveRunRegistry

	// GetQueue returns the followup queue (may be nil).
	GetQueue() *task.FollowupQueue

	// GetTasks returns the task scheduler (may be nil).
	GetTasks() *task.TaskScheduler

	// GetEnvironment returns the environment runtime (may be nil).
	GetEnvironment() *infra.EnvironmentRuntime

	// ResolveChannelConfig returns the resolved channel configuration for the
	// given channel ID.
	ResolveChannelConfig(channelID string) config.ChannelConfig

	// GetSendPolicy returns the configured send policy (nil if no policy set).
	GetSendPolicy() *session.SendPolicyConfig
}

// ---------------------------------------------------------------------------
// AgentRunContext — per-turn context threaded through backends and tools.
// ---------------------------------------------------------------------------

// AgentRunContext carries all per-turn state for a single agent execution.
// It is the primary "environment" passed to Backend.Run and ToolPlanner.Next.
type AgentRunContext struct {
	Runtime         RuntimeServices
	Request         core.AgentRunRequest
	Session         core.SessionResolution
	Identity        core.AgentIdentity
	ModelSelection  core.ModelSelection
	Transcript      []core.TranscriptMessage
	Memory          []core.MemoryHit
	ContextHits     []core.ContextSourceHit
	Skills          *core.SkillSnapshot
	AvailableTools  []Tool
	SystemPrompt    string
	WorkspaceDir    string
	ReplyDispatcher *delivery.ReplyDispatcher
	RunState        *core.AgentRunState
}

// ---------------------------------------------------------------------------
// ToolContext — per-tool-invocation context.
// ---------------------------------------------------------------------------

// ToolContext wraps the run context plus sandbox information for a single
// tool invocation.
type ToolContext struct {
	Runtime RuntimeServices
	Run     AgentRunContext
	Sandbox *SandboxContext
}

// ---------------------------------------------------------------------------
// SandboxContext — sandbox resolution result.
// ---------------------------------------------------------------------------

// SandboxContext describes the resolved sandbox environment for a tool
// invocation.
type SandboxContext struct {
	Enabled         bool
	Mode            string
	WorkspaceAccess string
	Scope           string
	ScopeKey        string
	WorkspaceRoot   string
	WorkspaceDir    string
	SandboxDirs     []string // multiple sandbox directories (user-configurable)
	AgentWorkspace  string
}

// ---------------------------------------------------------------------------
// Backend / Tool / ToolPlanner interfaces.
// ---------------------------------------------------------------------------

// Backend runs a complete agent turn (model call + tool loop).
type Backend interface {
	Run(ctx context.Context, runCtx AgentRunContext) (core.AgentRunResult, error)
}

// Tool describes a single tool that can be invoked by an agent.
type Tool interface {
	Name() string
	Description() string
	Execute(ctx context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error)
}

// ToolPlanner decides which tool to call next (or whether to emit final
// replies) during a planning-based backend loop.
type ToolPlanner interface {
	Next(ctx context.Context, runCtx AgentRunContext, state core.ToolPlannerState) (core.ToolPlan, error)
}
