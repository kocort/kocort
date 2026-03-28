// runtime_services.go — exported wrappers and getters that make *Runtime
// satisfy the rtypes.RuntimeServices interface.
//
// Existing callers inside runtime/ continue using the unexported methods;
// callers that move to internal/* use the exported interface instead.
package runtime

import (
	"context"
	"strings"

	"github.com/kocort/kocort/internal/backend"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/heartbeat"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
	sessionpkg "github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/task"
	"github.com/kocort/kocort/internal/tool"
)

// compile-time assertion
var _ rtypes.RuntimeServices = (*Runtime)(nil)

// ----- CheckSessionAccess and other exported service methods -----

func (r *Runtime) CheckSessionAccess(action sessionpkg.SessionAccessAction, requesterKey, targetKey string) sessionpkg.SessionAccessResult {
	return sessionpkg.CheckAccess(
		sessionpkg.AccessPolicy{
			Visibility: r.Policy.SessionToolsVisibility,
			A2A:        r.Policy.AgentToAgent,
		},
		r.Identities,
		r.Sessions,
		action,
		requesterKey,
		targetKey,
	)
}

func (r *Runtime) ResolveModelSelection(ctx context.Context, identity core.AgentIdentity, req core.AgentRunRequest, session core.SessionResolution) (core.ModelSelection, error) {
	return backend.ResolveModelSelection(ctx, identity, req, session)
}

// ----- Subsystem getters -----

func (r *Runtime) GetSessions() *sessionpkg.SessionStore  { return r.Sessions }
func (r *Runtime) GetIdentities() core.IdentityResolver   { return r.Identities }
func (r *Runtime) GetProcesses() *tool.ProcessRegistry    { return r.Processes }
func (r *Runtime) GetMemory() core.MemoryProvider         { return r.Memory }
func (r *Runtime) GetSubagents() *task.SubagentRegistry   { return r.Subagents }
func (r *Runtime) GetActiveRuns() *task.ActiveRunRegistry { return r.ActiveRuns }
func (r *Runtime) GetQueue() *task.FollowupQueue          { return r.Queue }
func (r *Runtime) GetTasks() *task.TaskScheduler          { return r.Tasks }
func (r *Runtime) GetEnvironment() *infra.EnvironmentRuntime {
	if env, ok := r.Environment.(*infra.EnvironmentRuntime); ok {
		return env
	}
	return nil
}

func (r *Runtime) ResolveChannelConfig(channelID string) config.ChannelConfig {
	if r.Channels == nil {
		return config.ChannelConfig{}
	}
	return r.Channels.ResolveConfig(channelID)
}

// GetSendPolicy returns the configured send policy, converting from config
// types to session types.  Returns nil when no policy is configured.
func (r *Runtime) GetSendPolicy() *sessionpkg.SendPolicyConfig {
	sp := r.Config.Session.SendPolicy
	if sp == nil {
		return nil
	}
	out := &sessionpkg.SendPolicyConfig{
		DefaultAction: sessionpkg.SendPolicyAction(sp.DefaultAction),
	}
	for _, rule := range sp.Rules {
		out.Rules = append(out.Rules, sessionpkg.SendPolicyRule{
			Action:    sessionpkg.SendPolicyAction(rule.Action),
			Channel:   rule.Channel,
			ChatType:  rule.ChatType,
			KeyPrefix: rule.KeyPrefix,
			Reason:    rule.Reason,
		})
	}
	return out
}

// ----- Task management (implements RuntimeServices + task.TaskRuntime) -----

// ScheduleTask registers or updates a scheduled/subagent task.
func (r *Runtime) ScheduleTask(_ context.Context, req task.TaskScheduleRequest) (core.TaskRecord, error) {
	if r == nil || r.Tasks == nil {
		return core.TaskRecord{}, core.ErrTaskSchedulerNotConfigured
	}
	return r.Tasks.Schedule(req)
}

// ListTasks returns all known task records.
func (r *Runtime) ListTasks(_ context.Context) []core.TaskRecord {
	if r == nil || r.Tasks == nil {
		return nil
	}
	return r.Tasks.List()
}

// GetTask returns a single task by ID (nil when not found).
func (r *Runtime) GetTask(_ context.Context, taskID string) *core.TaskRecord {
	if r == nil || r.Tasks == nil {
		return nil
	}
	return r.Tasks.Get(taskID)
}

// CancelTask cancels a running or scheduled task.
func (r *Runtime) CancelTask(_ context.Context, taskID string) (*core.TaskRecord, error) {
	if r == nil || r.Tasks == nil {
		return nil, core.ErrTaskSchedulerNotConfigured
	}
	record, ok, err := r.Tasks.Cancel(taskID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	if r.ActiveRuns != nil && strings.TrimSpace(record.SessionKey) != "" && strings.TrimSpace(record.RunID) != "" {
		_ = r.ActiveRuns.CancelRun(record.SessionKey, record.RunID) // best-effort; failure is non-critical
	}
	return &record, nil
}

// DeleteTask deletes a task by ID.
func (r *Runtime) DeleteTask(_ context.Context, taskID string) (*core.TaskRecord, error) {
	if r == nil || r.Tasks == nil {
		return nil, core.ErrTaskSchedulerNotConfigured
	}
	record, ok, err := r.Tasks.Delete(taskID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	if r.ActiveRuns != nil && strings.TrimSpace(record.SessionKey) != "" && strings.TrimSpace(record.RunID) != "" {
		_ = r.ActiveRuns.CancelRun(record.SessionKey, record.RunID) // best-effort; failure is non-critical
	}
	return &record, nil
}

// ActiveRunsTotalCount satisfies task.TaskRuntime.
func (r *Runtime) ActiveRunsTotalCount() int {
	if r == nil || r.ActiveRuns == nil {
		return 0
	}
	return r.ActiveRuns.TotalCount()
}

func (r *Runtime) HeartbeatsEnabled() bool {
	return heartbeat.AreHeartbeatsEnabled()
}

func (r *Runtime) SetHeartbeatsEnabled(enabled bool) {
	heartbeat.SetHeartbeatsEnabled(enabled)
	if r == nil || r.Heartbeats == nil {
		return
	}
	if enabled {
		r.Heartbeats.Start()
		r.Heartbeats.RunIntervals()
		return
	}
	r.Heartbeats.Stop()
}
