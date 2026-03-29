package runtime

import (
	"context"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/acp"
	backendpkg "github.com/kocort/kocort/internal/backend"
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

// ---------------------------------------------------------------------------
// ACP session lifecycle — these implement ACP-flavored subagent spawning.
// ---------------------------------------------------------------------------

// SpawnACPSession delegates ACP child-session orchestration to the ACP
// control-plane layer so runtime only acts as a thin facade.
func (r *Runtime) SpawnACPSession(ctx context.Context, req acp.SessionSpawnRequest) (acp.SessionSpawnResult, error) {
	coordinator := acp.SpawnCoordinator{
		Sessions:   r.Sessions,
		Identities: r.Identities,
		Deliverer:  r.Deliverer,
	}
	return coordinator.SpawnSession(ctx, req, acp.SpawnHooks{
		ValidateTarget: task.ValidateSpawnTargetAgent,
		RegisterLaunch: func(req acp.SessionSpawnRequest, launch acp.SessionLaunchPlan) {
			if r.Subagents == nil {
				return
			}
			record := task.BuildACPChildRunRecord(req, launch)
			r.Subagents.Register(record)
			if entry := r.Sessions.Entry(launch.Result.ChildSessionKey); entry != nil {
				r.Subagents.UpdateACPChildRuntime(launch.Result.RunID, entry)
			}
			if r.Tasks != nil {
				_ = r.Tasks.RegisterSubagent(record, launch.Result.AgentID)
			}
		},
		Run: r.Run,
		FinalizeChildRun: func(ctx context.Context, req core.AgentRunRequest, result core.AgentRunResult, runErr error) {
			r.handleACPChildLifecycleCompletion(ctx, req, result, runErr)
		},
	})
}

// ResumeAllPersistentACPSessions keeps runtime startup thin by delegating the
// store scan to internal/acp and only handling backend/runtime resolution here.
func (r *Runtime) ResumeAllPersistentACPSessions(ctx context.Context) []acp.AcpSessionResumeResult {
	if r == nil || r.Sessions == nil {
		return nil
	}
	return acp.ResumePersistentSessions(ctx, r.Sessions, func(ctx context.Context, input acp.AcpSessionResumeInput) (acp.AcpSessionResumeResult, error) {
		backend, err := r.resolveBootstrapACPBackend(input.BackendID, input.SessionKey)
		if err != nil {
			return acp.AcpSessionResumeResult{}, err
		}
		manager, runtimeImpl, err := ensureBootstrapACPServices(backend)
		if err != nil {
			return acp.AcpSessionResumeResult{}, err
		}
		return manager.ResumeSession(ctx, r.Sessions, runtimeImpl, input)
	})
}

func (r *Runtime) newACPResetLifecycleStore() acp.ResetLifecycleStore {
	return acp.ResetLifecycleStore{
		LoadTranscriptFn: func(sessionKey string) ([]core.TranscriptMessage, error) {
			return r.Sessions.LoadTranscript(sessionKey)
		},
		ResetFn: func(sessionKey string, reason string) (string, error) {
			return r.Sessions.Reset(sessionKey, reason)
		},
		ResetACPBoundSessionFn: func(sess core.SessionResolution, reason string) (string, error) {
			if r == nil || r.Sessions == nil || sess.Entry == nil || sess.Entry.ACP == nil {
				return r.Sessions.Reset(sess.SessionKey, reason)
			}
			provider := strings.TrimSpace(sess.Entry.ACP.Backend)
			if provider == "" {
				return r.Sessions.Reset(sess.SessionKey, reason)
			}
			resolved, _, err := r.Backends.Resolve(provider)
			if err != nil {
				return r.Sessions.Reset(sess.SessionKey, reason)
			}
			acpBackend, ok := resolved.(*backendpkg.ACPBackend)
			if !ok || acpBackend.Mgr == nil || acpBackend.Runtime == nil {
				return r.Sessions.Reset(sess.SessionKey, reason)
			}
			_ = acpBackend.Mgr.CloseSession(context.Background(), r.Sessions, acpBackend.Runtime, sess.SessionKey, "session-reset", false)
			return r.Sessions.Reset(sess.SessionKey, reason)
		},
	}
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
		r.Subagents.ReleaseDeferredAnnouncementsForRequester(entry.RequesterSessionKey)
		r.Subagents.MarkWakeOnDescendantSettle(entry.RunID, false)
		_ = r.flushSubagentAnnouncements(ctx, entry.RequesterSessionKey)
		return
	}
	r.Subagents.MarkCompletionMessageSent(entry.RunID)
	r.Subagents.SweepOrphans(r.Sessions, r.ActiveRuns)
}

func (r *Runtime) resolveBootstrapACPBackend(explicitBackendID string, sessionKey string) (*backendpkg.ACPBackend, error) {
	for _, candidate := range r.bootstrapACPBackendCandidates(explicitBackendID, sessionKey) {
		if backend := r.resolveBootstrapACPBackendCandidate(candidate); backend != nil {
			return backend, nil
		}
	}
	return nil, core.ErrACPNotConfigured
}

func (r *Runtime) bootstrapACPBackendCandidates(explicitBackendID string, sessionKey string) []string {
	candidates := make([]string, 0, 4)
	seen := map[string]struct{}{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		candidates = append(candidates, value)
	}
	add(explicitBackendID)
	if r != nil && r.Sessions != nil && strings.TrimSpace(sessionKey) != "" {
		if entry := r.Sessions.Entry(sessionKey); entry != nil && entry.ACP != nil {
			add(entry.ACP.Backend)
		}
	}
	if r != nil {
		add(r.Config.ACP.Backend)
		for providerID, providerCfg := range r.Config.Models.Providers {
			if strings.EqualFold(strings.TrimSpace(providerCfg.API), "acp") {
				add(providerID)
			}
		}
		if backend, ok := r.Backend.(*backendpkg.ACPBackend); ok {
			add(backend.Provider)
		}
	}
	return candidates
}

func (r *Runtime) resolveBootstrapACPBackendCandidate(candidate string) *backendpkg.ACPBackend {
	if r == nil {
		return nil
	}
	candidate = strings.TrimSpace(candidate)
	if r.Backends != nil && candidate != "" {
		resolved, kind, err := r.Backends.Resolve(candidate)
		if err == nil && strings.EqualFold(strings.TrimSpace(kind), "acp") {
			if acpBackend, ok := resolved.(*backendpkg.ACPBackend); ok {
				return acpBackend
			}
		}
	}
	if acpBackend, ok := r.Backend.(*backendpkg.ACPBackend); ok {
		if candidate == "" || strings.EqualFold(strings.TrimSpace(acpBackend.Provider), candidate) {
			return acpBackend
		}
	}
	return nil
}

func ensureBootstrapACPServices(acpBackend *backendpkg.ACPBackend) (*acp.AcpSessionManager, core.AcpRuntime, error) {
	if acpBackend == nil {
		return nil, nil, core.ErrACPNotConfigured
	}
	manager := acpBackend.Mgr
	if manager == nil {
		manager = acp.NewAcpSessionManager()
		acpBackend.Mgr = manager
	}
	runtimeImpl := acpBackend.Runtime
	if runtimeImpl == nil {
		if acpBackend.Env == nil {
			return nil, nil, core.ErrACPNotConfigured
		}
		runtimeImpl = backendpkg.NewACPClientRuntime(acpBackend.Config, acpBackend.Env, acpBackend.Provider, acpBackend.Command)
		acpBackend.Runtime = runtimeImpl
	}
	return manager, runtimeImpl, nil
}
