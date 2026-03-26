package runtime

import (
	"context"
	"time"

	"github.com/kocort/kocort/internal/core"
	sessionpkg "github.com/kocort/kocort/internal/session"
	spawnpolicypkg "github.com/kocort/kocort/internal/spawnpolicy"
	"github.com/kocort/kocort/internal/task"
)

// SpawnSubagent creates and starts a child agent session.
func (r *Runtime) SpawnSubagent(ctx context.Context, req task.SubagentSpawnRequest) (task.SubagentSpawnResult, error) {
	if r.Subagents == nil {
		return task.SubagentSpawnResult{}, core.ErrSubagentRegistryNotConfigured
	}
	requesterIdentity, err := r.Identities.Resolve(ctx, req.RequesterAgentID)
	if err == nil {
		if policyErr := task.ValidateSpawnTargetAgent(requesterIdentity, req.RequesterAgentID, req.TargetAgentID); policyErr != nil {
			return task.SubagentSpawnResult{Status: "forbidden"}, policyErr
		}
	}
	result, err := task.SpawnSubagent(ctx, r.Subagents, req)
	if err != nil {
		return result, err
	}
	if r.Tasks != nil {
		record := r.Subagents.Get(result.RunID)
		if record != nil {
			_ = r.Tasks.RegisterSubagent(*record, sessionpkg.NormalizeAgentID(req.TargetAgentID)) // best-effort; failure is non-critical
		}
	}
	resolvedTargetAgentID := sessionpkg.NormalizeAgentID(req.TargetAgentID)
	if resolvedTargetAgentID == "" {
		resolvedTargetAgentID = sessionpkg.NormalizeAgentID(req.RequesterAgentID)
	}
	var targetIdentity *core.AgentIdentity
	if identity, targetErr := r.Identities.Resolve(ctx, resolvedTargetAgentID); targetErr == nil {
		targetIdentity = &identity
	}
	if requesterIdentity.ID != "" && targetIdentity != nil {
		if policyErr := spawnpolicypkg.ValidateSpawnRuntimePolicy(requesterIdentity, *targetIdentity, req.SandboxMode); policyErr != nil {
			return task.SubagentSpawnResult{Status: "forbidden"}, policyErr
		}
	}
	var existingEntry *core.SessionEntry
	if r.Sessions != nil {
		existingEntry = r.Sessions.Entry(result.ChildSessionKey)
	}
	launch, err := task.PrepareSubagentLaunch(req, result, targetIdentity, existingEntry)
	if err != nil {
		return task.SubagentSpawnResult{}, err
	}
	childReq := launch.ChildRequest
	if r.Sessions != nil {
		if err := r.Sessions.Upsert(result.ChildSessionKey, launch.SessionEntry); err != nil {
			return task.SubagentSpawnResult{}, err
		}
		if req.SpawnMode == "session" && req.ThreadRequested {
			idleTimeoutMs, maxAgeMs := sessionpkg.DefaultSessionBindingLifecycle()
			_ = sessionpkg.NewThreadBindingService(r.Sessions).BindThreadSession(sessionpkg.BindThreadSessionInput{
				TargetSessionKey:    result.ChildSessionKey,
				RequesterSessionKey: req.RequesterSessionKey,
				TargetKind:          "subagent",
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
	}
	if launch.AttachmentsDir != "" || launch.AttachmentsRootDir != "" {
		r.Subagents.SetAttachmentsMetadata(result.RunID, launch.AttachmentsDir, launch.AttachmentsRootDir, launch.RetainAttachmentsOnKeep)
	}
	result.Attachments = launch.AttachmentReceipt

	go func() {
		result, err := r.Run(context.Background(), childReq)
		if err != nil {
			r.handleSubagentLifecycleCompletion(context.Background(), childReq, result, err)
		}
	}()

	return result, nil
}

// handleSubagentLifecycleCompletion handles the completion of a subagent run.
func (r *Runtime) handleSubagentLifecycleCompletion(ctx context.Context, req core.AgentRunRequest, result core.AgentRunResult, runErr error) {
	if r == nil || r.Subagents == nil {
		return
	}
	entry, completed := r.Subagents.Complete(req.RunID, result, runErr)
	if !completed || entry == nil {
		return
	}
	if r.Tasks != nil {
		_ = r.Tasks.CompleteSubagent(req.RunID, result, runErr) // best-effort; failure is non-critical
	}
	if task.ShouldDeferSubagentCompletionAnnouncement(r.Subagents, entry) {
		r.Subagents.MarkWakeOnDescendantSettle(entry.RunID, true)
		r.Subagents.MarkCompletionDeferredUntil(entry.RunID, time.Now().UTC().Add(task.ResolveSubagentDescendantDeferDelay()))
		r.scheduleSubagentAnnouncementRetry(entry.RequesterSessionKey)
		return
	}
	// Release any deferred siblings so they can be batched with this completion.
	r.Subagents.ReleaseDeferredAnnouncementsForRequester(entry.RequesterSessionKey)
	r.Subagents.MarkWakeOnDescendantSettle(entry.RunID, false)
	_ = r.flushSubagentAnnouncements(ctx, entry.RequesterSessionKey) // best-effort; failure is non-critical
	r.Subagents.SweepOrphans(r.Sessions, r.ActiveRuns)
}

// flushSubagentAnnouncements sends pending subagent completion announcements
// to the requester. When multiple completions are pending they are batched
// into a single aggregated message to avoid pushing N separate results.
func (r *Runtime) flushSubagentAnnouncements(ctx context.Context, requesterSessionKey string) error {
	if r == nil || r.Subagents == nil {
		return nil
	}
	announcement, runIDs := task.PrepareBatchedSubagentAnnouncement(r.Subagents, requesterSessionKey, time.Now().UTC())
	if announcement == nil {
		return nil
	}
	for _, runID := range runIDs {
		r.Subagents.MarkCompletionAnnounceAttempt(runID, nil)
	}
	if err := r.dispatchSubagentAnnouncement(ctx, *announcement); err != nil {
		if task.ShouldRetrySubagentAnnouncementError(err) {
			for _, runID := range runIDs {
				r.Subagents.MarkCompletionAnnounceAttempt(runID, err)
			}
			r.scheduleSubagentAnnouncementRetry(requesterSessionKey)
		} else {
			for _, runID := range runIDs {
				r.Subagents.MarkCompletionAbandoned(runID, err.Error())
			}
		}
		return err
	}
	for _, runID := range runIDs {
		r.Subagents.MarkEndedHookEmitted(runID)
		r.Subagents.MarkCompletionMessageSent(runID)
	}
	return nil
}

func (r *Runtime) dispatchSubagentAnnouncement(ctx context.Context, announcement task.SubagentAnnouncement) error {
	if r == nil {
		return nil
	}
	result := task.RunSubagentAnnounceDispatch(ctx, task.SubagentAnnounceDispatchParams{
		RunID:                    announcement.RunID,
		RequesterSessionKey:      announcement.RequesterSessionKey,
		ExpectsCompletionMessage: announcement.PrimaryPath == "direct",
		Announcement:             announcement,
		Steer: func(ctx context.Context, sessionKey string, req core.AgentRunRequest) bool {
			if r.ActiveRuns == nil || !r.ActiveRuns.IsActive(sessionKey) {
				return false
			}
			if r.Queue == nil {
				return false
			}
			return r.Queue.Enqueue(task.FollowupRun{
				QueueKey: sessionKey,
				Request:  req,
				Prompt:   req.Message,
			}, task.QueueSettings{Mode: core.QueueModeSteer}, core.QueueDedupeNone)
		},
		Queue: func(ctx context.Context, req core.AgentRunRequest) (string, error) {
			if r.Queue == nil {
				return task.DeliveryPathNone, nil
			}
			req.ShouldFollowup = true
			if req.QueueMode == "" {
				req.QueueMode = core.QueueModeCollect
			}
			ok := r.Queue.Enqueue(task.FollowupRun{
				QueueKey: req.SessionKey,
				Request:  req,
				Prompt:   req.Message,
			}, task.QueueSettings{Mode: req.QueueMode}, core.QueueDedupeNone)
			if ok {
				// Kick drain so announcement is processed even when no parent run is active.
				r.Queue.ScheduleDrain(ctx, req.SessionKey, func(run task.FollowupRun) error {
					_, err := r.Run(ctx, run.Request)
					return err
				})
				return task.DeliveryPathQueued, nil
			}
			return task.DeliveryPathNone, nil
		},
		Direct: func(ctx context.Context, req core.AgentRunRequest) error {
			_, err := r.Run(ctx, req)
			return err
		},
	})
	if r.Subagents != nil {
		r.Subagents.MarkAnnouncementDeliveryPath(announcement.RunID, result.Path)
	}
	return result.Error
}

func (r *Runtime) scheduleSubagentAnnouncementRetry(requesterSessionKey string) {
	if r == nil || r.Subagents == nil {
		return
	}
	delay, ok := r.Subagents.NextAnnouncementRetryDelayForRequester(requesterSessionKey, time.Now().UTC())
	if !ok {
		return
	}
	r.Subagents.ScheduleAnnouncementRetry(requesterSessionKey, delay, func() {
		_ = r.flushSubagentAnnouncements(context.Background(), requesterSessionKey)
	})
}

func (r *Runtime) restoreSubagentAnnouncementRecovery() {
	if r == nil || r.Subagents == nil {
		return
	}
	for _, requesterSessionKey := range task.PendingAnnouncementRecoveryRequesters(r.Subagents, time.Now().UTC()) {
		_ = r.flushSubagentAnnouncements(context.Background(), requesterSessionKey)
		r.scheduleSubagentAnnouncementRetry(requesterSessionKey)
	}
}

// recoverOrphanedSubagentRuns attempts to resume subagent runs that were
// interrupted when the process last exited. This is called once during
// runtime startup after the registry has been restored from disk.
//
// Runs that are too old (>30 min) or whose session no longer exists are
// marked as terminated. Runs within the recovery window are re-launched
// with a synthetic resume message that includes the original task and the
// last user message from the transcript.
func (r *Runtime) recoverOrphanedSubagentRuns() {
	if r == nil || r.Subagents == nil {
		return
	}
	plan := task.BuildOrphanRecoveryPlan(r.Subagents, r.Sessions, r.ActiveRuns, time.Now().UTC())

	// Nothing to recover — fast path.
	if len(plan.Recoverable) == 0 && len(plan.TooOld) == 0 && len(plan.NoSession) == 0 {
		return
	}

	_ = task.ExecuteOrphanRecovery(r.Subagents, plan, func(req core.AgentRunRequest) (core.AgentRunResult, error) {
		result, err := r.Run(context.Background(), req)
		if err != nil {
			r.handleSubagentLifecycleCompletion(context.Background(), req, result, err)
		}
		return result, err
	})
}
