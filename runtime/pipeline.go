// pipeline.go — AgentPipeline orchestrator and PipelineState.
//
// AgentPipeline breaks the former monolithic Run() method (~628 lines) into
// independently testable stages. Each stage operates on PipelineState,
// reading results from earlier stages and writing its own outputs.
//
// Stages:
//  1. validate      — readiness checks, input defaults (pipeline_validate.go)
//  2. resolve       — identity, session, commands, timestamp (pipeline_resolve.go)
//  3. gateQueue     — active-run mutex, queue/drop decisions (pipeline_queue.go)
//  4. loadContext   — workspace, transcript, skills, memory, model (pipeline_context.go)
//  5. buildRunCtx   — tool filtering, plugin injection, RunContext (pipeline_tools.go)
//  6. execute       — skill dispatch or model-call loop + retry  (pipeline_execute.go)
package runtime

import (
	"context"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
)

// AgentPipeline orchestrates the stages of a single agent run.
// It holds a reference to the runtime for accessing subsystem services.
type AgentPipeline struct {
	runtime *Runtime
}

// newPipeline creates a new AgentPipeline bound to the given runtime.
func newPipeline(r *Runtime) *AgentPipeline {
	return &AgentPipeline{runtime: r}
}

// PipelineState carries all intermediate state through the pipeline stages.
// Each stage reads from previous stages' outputs and writes its own.
type PipelineState struct {
	// ---------------------------------------------------------------
	// Input (set by caller / validate stage)
	// ---------------------------------------------------------------

	// Request is the original agent run request. The validate stage may
	// mutate it (e.g. populating AgentID, RunID).
	Request core.AgentRunRequest

	// ---------------------------------------------------------------
	// Set by resolve stage
	// ---------------------------------------------------------------

	// Identity is the resolved agent identity.
	Identity core.AgentIdentity

	// Session is the resolved session (may be new or existing).
	Session core.SessionResolution

	// RawMessage is the trimmed user message before timestamp injection.
	RawMessage string

	// ---------------------------------------------------------------
	// Set by gateQueue stage
	// ---------------------------------------------------------------

	// runCtxBase is the context for the run, potentially with a timeout.
	runCtxBase context.Context

	// cancelRun cancels the run context.
	cancelRun func()

	// finishActiveRun deregisters the run from the active-run registry.
	finishActiveRun func()

	// StartedAt is the wall-clock time when the run was started.
	StartedAt time.Time

	// ---------------------------------------------------------------
	// Set by loadContext stage
	// ---------------------------------------------------------------

	// WorkspaceDir is the resolved workspace directory.
	WorkspaceDir string

	// AgentDir is the resolved agent-specific directory.
	AgentDir string

	// Transcript is the sanitised chat history for the session.
	Transcript []core.TranscriptMessage

	// InternalEvents are system/internal transcript messages.
	InternalEvents []core.TranscriptMessage

	// Skills is the resolved skill snapshot from the workspace.
	Skills *core.SkillSnapshot

	// ContextFiles are prompt-context files loaded from the workspace.
	ContextFiles []infra.PromptContextFile

	// BootstrapWarnings are non-fatal warnings from context loading.
	BootstrapWarnings []string

	// MemoryHits are recalled memory entries.
	MemoryHits []core.MemoryHit

	// Selection is the resolved model provider/model pair.
	Selection core.ModelSelection

	// ---------------------------------------------------------------
	// Set by buildRunCtx (tools) stage
	// ---------------------------------------------------------------

	// Tools is the filtered list of tools available for this run.
	Tools []rtypes.Tool

	// Target is the delivery target for replies.
	Target core.DeliveryTarget

	// Dispatcher is the reply dispatcher for this run.
	Dispatcher *delivery.ReplyDispatcher

	// AgentRunCtx is the fully assembled per-turn context.
	AgentRunCtx rtypes.AgentRunContext

	// RestoreSkillEnv undoes skill env-var overrides; called via defer.
	RestoreSkillEnv func()
}
