// pipeline_test.go — Unit tests for AgentPipeline stages.
//
// Each stage is tested independently by constructing a minimal Runtime
// and PipelineState, then verifying that the stage produces the expected
// outputs or errors.
package runtime

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/gateway"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/task"
	"github.com/kocort/kocort/internal/tool"
)

// ---------------------------------------------------------------------------
// Stage 1: validate
// ---------------------------------------------------------------------------

func TestPipelineValidate_MissingSubsystems(t *testing.T) {
	r := &Runtime{} // all fields nil
	p := newPipeline(r)
	state := &PipelineState{Request: core.AgentRunRequest{Message: "hello"}}
	err := p.validate(state)
	if err == nil {
		t.Fatal("expected error for unconfigured runtime")
	}
	if !strings.Contains(err.Error(), "not fully configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPipelineValidate_EmptyMessage(t *testing.T) {
	r := minimalRuntime(t)
	p := newPipeline(r)
	state := &PipelineState{Request: core.AgentRunRequest{Message: "   "}}
	err := p.validate(state)
	if err == nil {
		t.Fatal("expected error for blank message")
	}
	if !strings.Contains(err.Error(), "message is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPipelineValidate_DefaultsAgentIDAndRunID(t *testing.T) {
	r := minimalRuntime(t)
	p := newPipeline(r)
	state := &PipelineState{Request: core.AgentRunRequest{Message: "hello"}}

	if err := p.validate(state); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if state.Request.AgentID == "" {
		t.Fatal("expected AgentID to be populated")
	}
	if state.Request.RunID == "" {
		t.Fatal("expected RunID to be populated")
	}
}

func TestPipelineValidate_PreservesExistingAgentIDAndRunID(t *testing.T) {
	r := minimalRuntime(t)
	p := newPipeline(r)
	state := &PipelineState{Request: core.AgentRunRequest{
		Message: "hello",
		AgentID: "custom-agent",
		RunID:   "custom-run",
	}}

	if err := p.validate(state); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if state.Request.AgentID != "custom-agent" {
		t.Fatalf("expected AgentID to be preserved, got %q", state.Request.AgentID)
	}
	if state.Request.RunID != "custom-run" {
		t.Fatalf("expected RunID to be preserved, got %q", state.Request.RunID)
	}
}

// ---------------------------------------------------------------------------
// Stage 2: resolve
// ---------------------------------------------------------------------------

func TestPipelineResolve_ResolvesIdentityAndSession(t *testing.T) {
	r := minimalRuntime(t)
	p := newPipeline(r)
	state := &PipelineState{Request: core.AgentRunRequest{
		Message: "hello",
		AgentID: "main",
		RunID:   "run-1",
		Channel: "webchat",
		To:      "user",
	}}

	shortCircuit, err := p.resolve(context.Background(), state)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if shortCircuit != nil {
		t.Fatal("unexpected short-circuit from resolve")
	}
	if state.Identity.ID != "main" {
		t.Fatalf("expected identity ID 'main', got %q", state.Identity.ID)
	}
	if state.Session.SessionKey == "" {
		t.Fatal("expected session key to be set")
	}
	if state.RawMessage == "" {
		t.Fatal("expected raw message to be set")
	}
}

func TestPipelineResolve_ShortCircuitsOnResetCommand(t *testing.T) {
	r := minimalRuntime(t)
	p := newPipeline(r)
	state := &PipelineState{Request: core.AgentRunRequest{
		Message: "/reset",
		AgentID: "main",
		RunID:   "run-1",
		Channel: "webchat",
		To:      "user",
	}}

	shortCircuit, err := p.resolve(context.Background(), state)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if shortCircuit == nil {
		t.Fatal("expected short-circuit from /reset command")
	}
	if shortCircuit.Err != nil {
		t.Fatalf("unexpected error in short-circuit: %v", shortCircuit.Err)
	}
	if len(shortCircuit.Result.Payloads) == 0 {
		t.Fatal("expected reset reply payload")
	}
}

// ---------------------------------------------------------------------------
// Stage 3: gateQueue
// ---------------------------------------------------------------------------

func TestPipelineGateQueue_ProceedsWhenNoActiveRun(t *testing.T) {
	r := minimalRuntime(t)
	p := newPipeline(r)
	state := &PipelineState{
		Request:  core.AgentRunRequest{Message: "hello", AgentID: "main", RunID: "run-1"},
		Identity: core.AgentIdentity{ID: "main"},
		Session:  core.SessionResolution{SessionKey: "agent:main:webchat:user", SessionID: "s1"},
	}

	result, err := p.gateQueue(context.Background(), state)
	if err != nil {
		t.Fatalf("gateQueue: %v", err)
	}
	if !result.Proceed {
		t.Fatal("expected gateQueue to proceed when no active run")
	}
	if state.runCtxBase == nil {
		t.Fatal("expected run context to be set")
	}
	if state.cancelRun == nil {
		t.Fatal("expected cancelRun to be set")
	}
	if state.finishActiveRun == nil {
		t.Fatal("expected finishActiveRun to be set")
	}
}

func TestPipelineGateQueue_DropsWhenHeartbeatAndActive(t *testing.T) {
	r := minimalRuntime(t)
	// Simulate an active run.
	sessionKey := "agent:main:webchat:user"
	finish := r.ActiveRuns.Start(sessionKey)
	defer finish()

	p := newPipeline(r)
	state := &PipelineState{
		Request: core.AgentRunRequest{
			Message:     "heartbeat",
			AgentID:     "main",
			RunID:       "run-hb",
			IsHeartbeat: true,
		},
		Identity: core.AgentIdentity{ID: "main"},
		Session:  core.SessionResolution{SessionKey: sessionKey, SessionID: "s1"},
	}

	result, err := p.gateQueue(context.Background(), state)
	if err != nil {
		t.Fatalf("gateQueue: %v", err)
	}
	if result.Proceed {
		t.Fatal("expected heartbeat to be dropped when session is active")
	}
}

func TestPipelineGateQueue_EnqueuesFollowupWhenActive(t *testing.T) {
	r := minimalRuntime(t)
	sessionKey := "agent:main:webchat:user"
	finish := r.ActiveRuns.Start(sessionKey)
	defer finish()

	p := newPipeline(r)
	state := &PipelineState{
		Request: core.AgentRunRequest{
			Message:        "followup message",
			AgentID:        "main",
			RunID:          "run-followup",
			ShouldFollowup: true,
			QueueMode:      core.QueueModeFollowup,
		},
		Identity: core.AgentIdentity{ID: "main"},
		Session:  core.SessionResolution{SessionKey: sessionKey, SessionID: "s1"},
	}

	result, err := p.gateQueue(context.Background(), state)
	if err != nil {
		t.Fatalf("gateQueue: %v", err)
	}
	if result.Proceed {
		t.Fatal("expected followup to be enqueued when session is active")
	}
	if !result.Result.Queued {
		t.Fatal("expected result to indicate enqueued")
	}
}

// ---------------------------------------------------------------------------
// Stage 5: buildRunContext
// ---------------------------------------------------------------------------

func TestPipelineBuildRunContext_AssemblesRunContext(t *testing.T) {
	r := minimalRuntime(t)
	p := newPipeline(r)
	state := &PipelineState{
		Request: core.AgentRunRequest{
			Message: "hello",
			AgentID: "main",
			RunID:   "run-1",
			Channel: "webchat",
			To:      "user",
		},
		Identity:     core.AgentIdentity{ID: "main"},
		Session:      core.SessionResolution{SessionKey: "agent:main:webchat:user", SessionID: "s1"},
		WorkspaceDir: t.TempDir(),
		Transcript:   nil,
		Skills:       &core.SkillSnapshot{},
		Selection:    core.ModelSelection{Provider: "openai", Model: "gpt-4.1"},
	}

	if err := p.buildRunContext(context.Background(), state); err != nil {
		t.Fatalf("buildRunContext: %v", err)
	}
	if state.Dispatcher == nil {
		t.Fatal("expected dispatcher to be set")
	}
	if state.AgentRunCtx.WorkspaceDir == "" {
		t.Fatal("expected AgentRunCtx.WorkspaceDir to be set")
	}
	if state.AgentRunCtx.Runtime != r {
		t.Fatal("expected AgentRunCtx.Runtime to reference the runtime")
	}
	if state.RestoreSkillEnv == nil {
		t.Fatal("expected RestoreSkillEnv to be set")
	}
	// Clean up
	state.RestoreSkillEnv()
}

// ---------------------------------------------------------------------------
// Full pipeline integration (thin Run)
// ---------------------------------------------------------------------------

func TestPipelineFullRun_BasicMessage(t *testing.T) {
	r := minimalRuntime(t)
	r.Backend = pipelineFakeBackend{
		onRun: func(_ context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "pong"})
			return core.AgentRunResult{
				Payloads: []core.ReplyPayload{{Text: "pong"}},
			}, nil
		},
	}
	result, err := r.Run(context.Background(), core.AgentRunRequest{
		Message: "ping",
		Channel: "webchat",
		To:      "user",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Payloads) == 0 {
		t.Fatal("expected at least one payload")
	}
	if result.Payloads[0].Text != "pong" {
		t.Fatalf("expected 'pong', got %q", result.Payloads[0].Text)
	}
	if result.RunID == "" {
		t.Fatal("expected RunID to be set")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// minimalRuntime creates a Runtime with the minimum fields needed for
// pipeline stages to pass readiness checks.
func minimalRuntime(t *testing.T) *Runtime {
	t.Helper()
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	return &Runtime{
		Config:   defaultTestConfig(),
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				PersonaPrompt:   "You are a test agent.",
				WorkspaceDir:    filepath.Join(baseDir, "workspace"),
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1",
			},
		}),
		Memory:     infra.NullMemoryProvider{},
		Backend:    pipelineNoopBackend{},
		Deliverer:  &delivery.MemoryDeliverer{},
		EventHub:   gateway.NewWebchatHub(),
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		ToolLoops:  tool.NewToolLoopRegistry(),
		Processes:  tool.NewProcessRegistry(),
		Tools:      tool.NewToolRegistry(),
	}
}

// defaultTestConfig returns a minimal AppConfig for testing.
func defaultTestConfig() config.AppConfig {
	return config.AppConfig{}
}

// pipelineNoopBackend is a backend that returns an empty result.
type pipelineNoopBackend struct{}

func (pipelineNoopBackend) Run(_ context.Context, _ rtypes.AgentRunContext) (core.AgentRunResult, error) {
	return core.AgentRunResult{}, nil
}

// pipelineFakeBackend wraps a function to implement rtypes.Backend.
type pipelineFakeBackend struct {
	onRun func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error)
}

func (f pipelineFakeBackend) Run(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
	if f.onRun != nil {
		return f.onRun(ctx, runCtx)
	}
	return core.AgentRunResult{}, nil
}
