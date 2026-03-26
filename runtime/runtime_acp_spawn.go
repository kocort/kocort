package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/acp"
	"github.com/kocort/kocort/internal/core"
	sessionpkg "github.com/kocort/kocort/internal/session"
	spawnpolicypkg "github.com/kocort/kocort/internal/spawnpolicy"
	"github.com/kocort/kocort/internal/task"
)

// SpawnACPSession creates and starts an ACP child session. The runtime remains
// a thin orchestrator; request normalization and launch planning live in
// internal/acp.
func (r *Runtime) SpawnACPSession(ctx context.Context, req acp.SessionSpawnRequest) (acp.SessionSpawnResult, error) {
	if r.Sessions == nil {
		return acp.SessionSpawnResult{}, fmt.Errorf("session store is not configured")
	}
	requesterIdentity, err := r.Identities.Resolve(ctx, req.RequesterAgentID)
	if err == nil {
		if policyErr := task.ValidateSpawnTargetAgent(requesterIdentity, req.RequesterAgentID, req.TargetAgentID); policyErr != nil {
			return acp.SessionSpawnResult{Status: "forbidden"}, policyErr
		}
	}
	resolvedTargetAgentID := sessionpkg.NormalizeAgentID(req.TargetAgentID)
	if resolvedTargetAgentID == "" {
		resolvedTargetAgentID = sessionpkg.NormalizeAgentID(req.RequesterAgentID)
	}
	targetIdentity, targetErr := r.Identities.Resolve(ctx, resolvedTargetAgentID)
	if targetErr != nil {
		return acp.SessionSpawnResult{}, targetErr
	}
	if !strings.EqualFold(strings.TrimSpace(targetIdentity.RuntimeType), "acp") {
		return acp.SessionSpawnResult{Status: "error"}, fmt.Errorf("sessions_spawn runtime=acp requires an ACP-configured target agent")
	}
	if requesterIdentity.ID != "" {
		if policyErr := spawnpolicypkg.ValidateSpawnRuntimePolicy(requesterIdentity, targetIdentity, req.SandboxMode); policyErr != nil {
			return acp.SessionSpawnResult{Status: "forbidden"}, policyErr
		}
	}
	launch, err := acp.PrepareSessionLaunch(req, &targetIdentity, nil)
	if err != nil {
		return acp.SessionSpawnResult{}, err
	}
	if err := r.Sessions.Upsert(launch.Result.ChildSessionKey, launch.SessionEntry); err != nil {
		return acp.SessionSpawnResult{}, err
	}
	if r.Subagents != nil {
		record := task.BuildACPChildRunRecord(req, launch)
		r.Subagents.Register(record)
		if entry := r.Sessions.Entry(launch.Result.ChildSessionKey); entry != nil {
			r.Subagents.UpdateACPChildRuntime(launch.Result.RunID, entry)
		}
		if r.Tasks != nil {
			_ = r.Tasks.RegisterSubagent(record, sessionpkg.NormalizeAgentID(req.TargetAgentID))
		}
	}
	if req.SpawnMode == "session" && req.ThreadRequested {
		idleTimeoutMs, maxAgeMs := sessionpkg.DefaultSessionBindingLifecycle()
		_ = sessionpkg.NewThreadBindingService(r.Sessions).BindThreadSession(sessionpkg.BindThreadSessionInput{
			TargetSessionKey:    launch.Result.ChildSessionKey,
			RequesterSessionKey: req.RequesterSessionKey,
			TargetKind:          "session",
			Placement:           sessionpkg.ThreadBindingPlacementChild,
			Channel:             req.RouteChannel,
			To:                  req.RouteTo,
			AccountID:           req.RouteAccountID,
			ThreadID:            req.RouteThreadID,
			IdleTimeoutMs:       idleTimeoutMs,
			MaxAgeMs:            maxAgeMs,
			Label:               req.Label,
			AgentID:             req.TargetAgentID,
		})
	}
	relay := acp.StartParentStreamRelay(r.Deliverer, req, launch.Result)
	go func() {
		result, err := r.Run(context.Background(), launch.ChildRequest)
		if relay != nil {
			relay.NotifyCompleted(result, err)
		}
		if err != nil || r.Subagents != nil {
			r.handleACPChildLifecycleCompletion(context.Background(), launch.ChildRequest, result, err)
		}
	}()
	return launch.Result, nil
}

func (r *Runtime) handleACPChildLifecycleCompletion(ctx context.Context, req core.AgentRunRequest, result core.AgentRunResult, runErr error) {
	if r == nil || r.Subagents == nil {
		return
	}
	entry, completed := r.Subagents.Complete(req.RunID, result, runErr)
	if !completed || entry == nil {
		return
	}
	if sessionEntry := r.Sessions.Entry(req.SessionKey); sessionEntry != nil {
		r.Subagents.UpdateACPChildRuntime(req.RunID, sessionEntry)
	}
	if r.Tasks != nil {
		_ = r.Tasks.CompleteSubagent(req.RunID, result, runErr)
	}
	if entry.ExpectsCompletionMessage {
		if task.ShouldDeferSubagentCompletionAnnouncement(r.Subagents, entry) {
			r.Subagents.MarkWakeOnDescendantSettle(entry.RunID, true)
			r.Subagents.MarkCompletionDeferredUntil(entry.RunID, time.Now().UTC().Add(task.ResolveSubagentDescendantDeferDelay()))
			r.scheduleSubagentAnnouncementRetry(entry.RequesterSessionKey)
			return
		}
		// Release deferred siblings so they can be batched with this completion.
		r.Subagents.ReleaseDeferredAnnouncementsForRequester(entry.RequesterSessionKey)
		r.Subagents.MarkWakeOnDescendantSettle(entry.RunID, false)
		_ = r.flushSubagentAnnouncements(ctx, entry.RequesterSessionKey)
		return
	}
	r.Subagents.MarkCompletionMessageSent(entry.RunID)
	_ = ctx
}
