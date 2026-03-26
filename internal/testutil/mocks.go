package testutil

import (
	"context"

	"github.com/kocort/kocort/internal/acp"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/task"
	"github.com/kocort/kocort/internal/tool"
)

// ---------------------------------------------------------------------------
// MockBackend — implements tool.Backend
// ---------------------------------------------------------------------------

// MockBackend is a test double for tool.Backend.
type MockBackend struct {
	RunFunc func(ctx context.Context, runCtx tool.AgentRunContext) (core.AgentRunResult, error)
}

func (m *MockBackend) Run(ctx context.Context, runCtx tool.AgentRunContext) (core.AgentRunResult, error) {
	if m.RunFunc != nil {
		return m.RunFunc(ctx, runCtx)
	}
	return core.AgentRunResult{}, nil
}

// ---------------------------------------------------------------------------
// MockIdentityResolver — implements core.IdentityResolver
// ---------------------------------------------------------------------------

// MockIdentityResolver is a test double for core.IdentityResolver.
type MockIdentityResolver struct {
	ResolveFunc func(ctx context.Context, agentID string) (core.AgentIdentity, error)
}

func (m *MockIdentityResolver) Resolve(ctx context.Context, agentID string) (core.AgentIdentity, error) {
	if m.ResolveFunc != nil {
		return m.ResolveFunc(ctx, agentID)
	}
	return core.AgentIdentity{ID: agentID, Name: agentID}, nil
}

// ---------------------------------------------------------------------------
// MockMemoryProvider — implements core.MemoryProvider
// ---------------------------------------------------------------------------

// MockMemoryProvider is a test double for core.MemoryProvider.
type MockMemoryProvider struct {
	RecallFunc func(ctx context.Context, identity core.AgentIdentity, session core.SessionResolution, message string) ([]core.MemoryHit, error)
}

func (m *MockMemoryProvider) Recall(ctx context.Context, identity core.AgentIdentity, session core.SessionResolution, message string) ([]core.MemoryHit, error) {
	if m.RecallFunc != nil {
		return m.RecallFunc(ctx, identity, session, message)
	}
	return nil, nil
}

// ---------------------------------------------------------------------------
// MockDeliverer — implements core.Deliverer
// ---------------------------------------------------------------------------

// MockDeliverer is a test double for core.Deliverer.
type MockDeliverer struct {
	DeliverFunc func(ctx context.Context, kind core.ReplyKind, payload core.ReplyPayload, target core.DeliveryTarget) error
	Delivered   []DeliveredMessage
}

// DeliveredMessage records a single delivery for assertion.
type DeliveredMessage struct {
	Kind    core.ReplyKind
	Payload core.ReplyPayload
	Target  core.DeliveryTarget
}

func (m *MockDeliverer) Deliver(ctx context.Context, kind core.ReplyKind, payload core.ReplyPayload, target core.DeliveryTarget) error {
	m.Delivered = append(m.Delivered, DeliveredMessage{Kind: kind, Payload: payload, Target: target})
	if m.DeliverFunc != nil {
		return m.DeliverFunc(ctx, kind, payload, target)
	}
	return nil
}

// ---------------------------------------------------------------------------
// MockRuntimeServices — implements tool.RuntimeServices
// ---------------------------------------------------------------------------

// MockRuntimeServices is a test double for tool.RuntimeServices.
// Each method can be overridden via the corresponding Func field.
type MockRuntimeServices struct {
	RunFunc                  func(ctx context.Context, req core.AgentRunRequest) (core.AgentRunResult, error)
	SpawnSubagentFunc        func(ctx context.Context, req task.SubagentSpawnRequest) (task.SubagentSpawnResult, error)
	SpawnACPSessionFunc      func(ctx context.Context, req acp.SessionSpawnRequest) (acp.SessionSpawnResult, error)
	PushInboundFunc          func(ctx context.Context, msg core.ChannelInboundMessage) (core.ChatSendResponse, error)
	ExecuteToolFunc          func(ctx context.Context, runCtx tool.AgentRunContext, name string, args map[string]any) (core.ToolResult, error)
	ScheduleTaskFunc         func(ctx context.Context, req task.TaskScheduleRequest) (core.TaskRecord, error)
	ListTasksFunc            func(ctx context.Context) []core.TaskRecord
	GetTaskFunc              func(ctx context.Context, taskID string) *core.TaskRecord
	CancelTaskFunc           func(ctx context.Context, taskID string) (*core.TaskRecord, error)
	CheckSessionAccessFunc   func(action session.SessionAccessAction, requesterKey, targetKey string) session.SessionAccessResult
	ResolveModelFunc         func(ctx context.Context, identity core.AgentIdentity, req core.AgentRunRequest, sess core.SessionResolution) (core.ModelSelection, error)
	ResolveChannelConfigFunc func(channelID string) config.ChannelConfig

	// Subsystem stubs
	SessionStore     *session.SessionStore
	ProcessRegistry  *tool.ProcessRegistry
	MemoryProv       core.MemoryProvider
	IdentityResolver core.IdentityResolver
	SubagentReg      *task.SubagentRegistry
	ActiveRunReg     *task.ActiveRunRegistry
	FollowupQ        *task.FollowupQueue
	TaskSched        *task.TaskScheduler
	EnvRuntime       *infra.EnvironmentRuntime
	AuditRec         event.AuditRecorder
	EventBus         event.EventBus
}

func (m *MockRuntimeServices) Run(ctx context.Context, req core.AgentRunRequest) (core.AgentRunResult, error) {
	if m.RunFunc != nil {
		return m.RunFunc(ctx, req)
	}
	return core.AgentRunResult{}, nil
}

func (m *MockRuntimeServices) SpawnSubagent(ctx context.Context, req task.SubagentSpawnRequest) (task.SubagentSpawnResult, error) {
	if m.SpawnSubagentFunc != nil {
		return m.SpawnSubagentFunc(ctx, req)
	}
	return task.SubagentSpawnResult{}, nil
}

func (m *MockRuntimeServices) SpawnACPSession(ctx context.Context, req acp.SessionSpawnRequest) (acp.SessionSpawnResult, error) {
	if m.SpawnACPSessionFunc != nil {
		return m.SpawnACPSessionFunc(ctx, req)
	}
	return acp.SessionSpawnResult{}, nil
}

func (m *MockRuntimeServices) PushInbound(ctx context.Context, msg core.ChannelInboundMessage) (core.ChatSendResponse, error) {
	if m.PushInboundFunc != nil {
		return m.PushInboundFunc(ctx, msg)
	}
	return core.ChatSendResponse{}, nil
}

func (m *MockRuntimeServices) ExecuteTool(ctx context.Context, runCtx tool.AgentRunContext, name string, args map[string]any) (core.ToolResult, error) {
	if m.ExecuteToolFunc != nil {
		return m.ExecuteToolFunc(ctx, runCtx, name, args)
	}
	return core.ToolResult{}, nil
}

func (m *MockRuntimeServices) ScheduleTask(ctx context.Context, req task.TaskScheduleRequest) (core.TaskRecord, error) {
	if m.ScheduleTaskFunc != nil {
		return m.ScheduleTaskFunc(ctx, req)
	}
	return core.TaskRecord{}, nil
}

func (m *MockRuntimeServices) ListTasks(ctx context.Context) []core.TaskRecord {
	if m.ListTasksFunc != nil {
		return m.ListTasksFunc(ctx)
	}
	return nil
}

func (m *MockRuntimeServices) GetTask(ctx context.Context, taskID string) *core.TaskRecord {
	if m.GetTaskFunc != nil {
		return m.GetTaskFunc(ctx, taskID)
	}
	return nil
}

func (m *MockRuntimeServices) CancelTask(ctx context.Context, taskID string) (*core.TaskRecord, error) {
	if m.CancelTaskFunc != nil {
		return m.CancelTaskFunc(ctx, taskID)
	}
	return nil, nil
}

func (m *MockRuntimeServices) GetAudit() event.AuditRecorder { return m.AuditRec }
func (m *MockRuntimeServices) GetEventBus() event.EventBus   { return m.EventBus }

func (m *MockRuntimeServices) CheckSessionAccess(action session.SessionAccessAction, requesterKey, targetKey string) session.SessionAccessResult {
	if m.CheckSessionAccessFunc != nil {
		return m.CheckSessionAccessFunc(action, requesterKey, targetKey)
	}
	return session.SessionAccessResult{Allowed: true}
}

func (m *MockRuntimeServices) ResolveModelSelection(ctx context.Context, identity core.AgentIdentity, req core.AgentRunRequest, sess core.SessionResolution) (core.ModelSelection, error) {
	if m.ResolveModelFunc != nil {
		return m.ResolveModelFunc(ctx, identity, req, sess)
	}
	return core.ModelSelection{Provider: "mock", Model: "mock-model"}, nil
}

func (m *MockRuntimeServices) GetSessions() *session.SessionStore        { return m.SessionStore }
func (m *MockRuntimeServices) GetIdentities() core.IdentityResolver      { return m.IdentityResolver }
func (m *MockRuntimeServices) GetProcesses() *tool.ProcessRegistry       { return m.ProcessRegistry }
func (m *MockRuntimeServices) GetMemory() core.MemoryProvider            { return m.MemoryProv }
func (m *MockRuntimeServices) GetSubagents() *task.SubagentRegistry      { return m.SubagentReg }
func (m *MockRuntimeServices) GetActiveRuns() *task.ActiveRunRegistry    { return m.ActiveRunReg }
func (m *MockRuntimeServices) GetQueue() *task.FollowupQueue             { return m.FollowupQ }
func (m *MockRuntimeServices) GetTasks() *task.TaskScheduler             { return m.TaskSched }
func (m *MockRuntimeServices) GetEnvironment() *infra.EnvironmentRuntime { return m.EnvRuntime }
func (m *MockRuntimeServices) GetSendPolicy() *session.SendPolicyConfig  { return nil }

func (m *MockRuntimeServices) ResolveChannelConfig(channelID string) config.ChannelConfig {
	if m.ResolveChannelConfigFunc != nil {
		return m.ResolveChannelConfigFunc(channelID)
	}
	return config.ChannelConfig{}
}
