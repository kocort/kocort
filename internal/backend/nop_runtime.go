package backend

import (
	"context"

	"github.com/kocort/kocort/internal/acp"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
	sessionpkg "github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/task"
	"github.com/kocort/kocort/internal/tool"
)

// NopRuntimeServices is a no-op implementation of rtypes.RuntimeServices.
// It is used as a safe default when AgentRunContext.Runtime is nil.
type NopRuntimeServices struct{}

var _ rtypes.RuntimeServices = (*NopRuntimeServices)(nil)

func (n *NopRuntimeServices) Run(ctx context.Context, req core.AgentRunRequest) (core.AgentRunResult, error) {
	return core.AgentRunResult{}, nil
}
func (n *NopRuntimeServices) SpawnSubagent(ctx context.Context, req task.SubagentSpawnRequest) (task.SubagentSpawnResult, error) {
	return task.SubagentSpawnResult{}, nil
}
func (n *NopRuntimeServices) SpawnACPSession(ctx context.Context, req acp.SessionSpawnRequest) (acp.SessionSpawnResult, error) {
	return acp.SessionSpawnResult{}, nil
}
func (n *NopRuntimeServices) PushInbound(ctx context.Context, msg core.ChannelInboundMessage) (core.ChatSendResponse, error) {
	return core.ChatSendResponse{}, nil
}
func (n *NopRuntimeServices) ExecuteTool(ctx context.Context, runCtx rtypes.AgentRunContext, name string, args map[string]any) (core.ToolResult, error) {
	return core.ToolResult{}, nil
}
func (n *NopRuntimeServices) ScheduleTask(ctx context.Context, req task.TaskScheduleRequest) (core.TaskRecord, error) {
	return core.TaskRecord{}, nil
}
func (n *NopRuntimeServices) ListTasks(ctx context.Context) []core.TaskRecord { return nil }
func (n *NopRuntimeServices) GetTask(ctx context.Context, taskID string) *core.TaskRecord {
	return nil
}
func (n *NopRuntimeServices) CancelTask(ctx context.Context, taskID string) (*core.TaskRecord, error) {
	return nil, nil
}
func (n *NopRuntimeServices) GetAudit() event.AuditRecorder { return nil }
func (n *NopRuntimeServices) GetEventBus() event.EventBus   { return nil }
func (n *NopRuntimeServices) CheckSessionAccess(action sessionpkg.SessionAccessAction, requesterKey, targetKey string) sessionpkg.SessionAccessResult {
	return sessionpkg.SessionAccessResult{}
}
func (n *NopRuntimeServices) GetSessions() *sessionpkg.SessionStore     { return nil }
func (n *NopRuntimeServices) GetIdentities() core.IdentityResolver      { return nil }
func (n *NopRuntimeServices) GetProcesses() *tool.ProcessRegistry       { return nil }
func (n *NopRuntimeServices) GetMemory() core.MemoryProvider            { return nil }
func (n *NopRuntimeServices) GetSubagents() *task.SubagentRegistry      { return nil }
func (n *NopRuntimeServices) GetActiveRuns() *task.ActiveRunRegistry    { return nil }
func (n *NopRuntimeServices) GetQueue() *task.FollowupQueue             { return nil }
func (n *NopRuntimeServices) GetTasks() *task.TaskScheduler             { return nil }
func (n *NopRuntimeServices) GetEnvironment() *infra.EnvironmentRuntime { return nil }
func (n *NopRuntimeServices) ResolveModelSelection(ctx context.Context, identity core.AgentIdentity, req core.AgentRunRequest, session core.SessionResolution) (core.ModelSelection, error) {
	return core.ModelSelection{}, nil
}
func (n *NopRuntimeServices) ResolveChannelConfig(string) config.ChannelConfig {
	return config.ChannelConfig{}
}
func (n *NopRuntimeServices) GetSendPolicy() *sessionpkg.SendPolicyConfig { return nil }

// ensureRuntime returns runCtx.Runtime if non-nil, or a no-op implementation.
func ensureRuntime(runCtx rtypes.AgentRunContext) rtypes.RuntimeServices {
	if runCtx.Runtime != nil {
		return runCtx.Runtime
	}
	return &NopRuntimeServices{}
}
