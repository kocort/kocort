package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/heartbeat"
	"github.com/kocort/kocort/internal/infra"
	sessionpkg "github.com/kocort/kocort/internal/session"

	"github.com/kocort/kocort/utils"
)

// HandleScheduledSystemEvent enqueues a system event and optionally triggers a heartbeat.
func (r *Runtime) HandleScheduledSystemEvent(ctx context.Context, task core.TaskRecord) error {
	if r == nil || r.SystemEvents == nil {
		return core.ErrSystemEventsNotConfigured
	}
	sessionKey := strings.TrimSpace(task.SessionKey)
	if sessionKey == "" {
		sessionKey = sessionpkg.BuildMainSessionKey(utils.NonEmpty(task.AgentID, sessionpkg.DefaultAgentID))
	}
	if !r.SystemEvents.Enqueue(sessionKey, task.Message, "task:"+task.ID) {
		event.RecordAudit(ctx, r.Audit, r.Logger, core.AuditEvent{
			Level:      "info",
			AgentID:    utils.NonEmpty(task.AgentID, sessionpkg.DefaultAgentID),
			SessionKey: sessionKey,
			TaskID:     task.ID,
			Message:    "scheduled system event deduped before heartbeat wake",
		})
		return nil
	}
	event.RecordAudit(ctx, r.Audit, r.Logger, core.AuditEvent{
		Level:      "info",
		AgentID:    utils.NonEmpty(task.AgentID, sessionpkg.DefaultAgentID),
		SessionKey: sessionKey,
		TaskID:     task.ID,
		Message:    "scheduled system event enqueued for heartbeat wake",
	})
	if r.Heartbeats != nil && task.WakeMode == core.TaskWakeNow {
		r.Heartbeats.RequestNow(heartbeat.HeartbeatWakeRequest{
			Reason:     "cron:" + task.ID,
			AgentID:    utils.NonEmpty(task.AgentID, sessionpkg.DefaultAgentID),
			SessionKey: sessionKey,
		})
	}
	return nil
}

// RunHeartbeatTurn executes a heartbeat turn for the given agent/session.
func (r *Runtime) RunHeartbeatTurn(ctx context.Context, req heartbeat.HeartbeatWakeRequest) (heartbeat.HeartbeatRunResult, error) {
	if r == nil {
		return heartbeat.HeartbeatRunResult{Status: "skipped", Reason: "runtime-missing"}, nil
	}
	startedAt := time.Now().UTC()
	event.RecordAudit(ctx, r.Audit, r.Logger, core.AuditEvent{
		Level:      "info",
		AgentID:    sessionpkg.NormalizeAgentID(req.AgentID),
		SessionKey: strings.TrimSpace(req.SessionKey),
		Message:    "heartbeat run started",
		Data: map[string]any{
			"reason": req.Reason,
		},
	})
	agentID := sessionpkg.NormalizeAgentID(req.AgentID)
	if agentID == "" {
		agentID = config.ResolveDefaultConfiguredAgentID(r.Config)
	}
	identity, err := r.Identities.Resolve(ctx, agentID)
	if err != nil {
		return heartbeat.HeartbeatRunResult{}, err
	}
	if !heartbeat.AreHeartbeatsEnabled() {
		return heartbeat.HeartbeatRunResult{Status: "skipped", Reason: "disabled"}, nil
	}
	if strings.TrimSpace(identity.HeartbeatEvery) == "" {
		return heartbeat.HeartbeatRunResult{Status: "skipped", Reason: "disabled"}, nil
	}
	if _, err := time.ParseDuration(strings.TrimSpace(identity.HeartbeatEvery)); err != nil {
		return heartbeat.HeartbeatRunResult{Status: "skipped", Reason: "disabled"}, nil
	}
	sessionKey := strings.TrimSpace(req.SessionKey)
	if sessionKey == "" {
		sessionKey = sessionpkg.BuildMainSessionKeyWithMain(agentID, config.ResolveSessionMainKey(r.Config))
	}
	sessionKey = strings.TrimSpace(heartbeat.ResolveHeartbeatSessionKeyForRuntime(identity, sessionKey, r.Config))
	req.SessionKey = sessionKey
	entry := r.Sessions.Entry(sessionKey)
	events := r.SystemEvents.Peek(sessionKey)
	heartbeatFile, heartbeatFileErr := infra.LoadWorkspaceTextFile(identity.WorkspaceDir, "HEARTBEAT.md")
	heartbeatFileExists := false
	if _, statErr := os.Stat(filepath.Join(identity.WorkspaceDir, "HEARTBEAT.md")); statErr == nil {
		heartbeatFileExists = true
	} else if !errors.Is(statErr, os.ErrNotExist) && heartbeatFileErr == nil {
		heartbeatFileExists = true
	}
	sessionBusy := r.ActiveRuns != nil && r.ActiveRuns.TotalCount() > 0
	queueDepth := 0
	if r.Queue != nil {
		queueDepth = r.Queue.TotalDepth()
	}
	plan := heartbeat.BuildTurnPlan(heartbeat.TurnPlanInput{
		Identity:             identity,
		Request:              req,
		Config:               r.Config,
		HeartbeatFileContent: heartbeatFile,
		HeartbeatFileExists:  heartbeatFileExists,
		WorkspaceDir:         identity.WorkspaceDir,
		Events:               events,
		SessionBusy:          sessionBusy,
		QueueDepth:           queueDepth,
		Now:                  time.Now().UTC(),
		SessionEntry:         entry,
	})
	if plan.Skip {
		event.RecordAudit(ctx, r.Audit, r.Logger, core.AuditEvent{
			Data: map[string]any{
				"reason": plan.SkipReason,
				"status": "skipped",
			},
		})
		r.emitHeartbeatEvent(sessionKey, "", heartbeat.Event{
			Status:   "skipped",
			Reason:   plan.SkipReason,
			To:       plan.DeliveryTarget.To,
			Duration: time.Since(startedAt),
		})
		return heartbeat.HeartbeatRunResult{Status: "skipped", Reason: plan.SkipReason}, nil
	}
	events = r.SystemEvents.Drain(sessionKey)
	plan = heartbeat.BuildTurnPlan(heartbeat.TurnPlanInput{
		Identity:             identity,
		Request:              req,
		Config:               r.Config,
		HeartbeatFileContent: heartbeatFile,
		HeartbeatFileExists:  heartbeatFileExists,
		WorkspaceDir:         identity.WorkspaceDir,
		Events:               events,
		SessionBusy:          false,
		QueueDepth:           0,
		Now:                  time.Now().UTC(),
		SessionEntry:         entry,
	})
	var previousUpdatedAt time.Time
	if entry != nil {
		previousUpdatedAt = entry.UpdatedAt
	}
	targetPlan := r.resolveHeartbeatReadyDeliveryTarget(ctx, plan.DeliveryTarget, core.ReplyPayload{Text: heartbeat.HeartbeatToken})
	if plan.Deliver && !targetPlan.Enabled {
		plan.Deliver = false
	}
	plan.DeliveryTarget = targetPlan
	plan.Visibility = heartbeat.ResolveVisibility(r.Config, targetPlan.Channel, targetPlan.AccountID)
	target := core.DeliveryContext{
		Channel:   targetPlan.Channel,
		To:        targetPlan.To,
		AccountID: targetPlan.AccountID,
		ThreadID:  targetPlan.ThreadID,
	}
	runRuntime := r
	var buffered *heartbeat.BufferedDeliverer
	if plan.Deliver {
		buffered = &heartbeat.BufferedDeliverer{}
		copyRuntime := *r
		copyRuntime.Deliverer = buffered
		runRuntime = &copyRuntime
	}
	runSessionKey := sessionKey
	if plan.IsolatedRun && strings.TrimSpace(plan.RunSessionKey) != "" && r.Sessions != nil {
		resolved, err := r.Sessions.ResolveForRequest(ctx, sessionpkg.SessionResolveOptions{
			AgentID:        agentID,
			SessionKey:     plan.RunSessionKey,
			To:             target.To,
			Channel:        target.Channel,
			ThreadID:       target.ThreadID,
			MainKey:        config.ResolveSessionMainKey(r.Config),
			DMScope:        config.ResolveSessionDMScope(r.Config),
			Now:            time.Now().UTC(),
			ForceNew:       true,
			ForceNewReason: "heartbeat",
		})
		if err != nil {
			return heartbeat.HeartbeatRunResult{}, err
		}
		runSessionKey = resolved.SessionKey
	} else if strings.TrimSpace(plan.RunSessionKey) != "" {
		runSessionKey = plan.RunSessionKey
	}
	transcriptSnapshot := heartbeat.CaptureTranscriptSnapshot(r.Sessions, runSessionKey)
	runReq := core.AgentRunRequest{
		RunID:                 sessionpkg.NewRunID(),
		AgentID:               agentID,
		SessionKey:            runSessionKey,
		Message:               plan.Prompt,
		Channel:               target.Channel,
		To:                    target.To,
		AccountID:             target.AccountID,
		ThreadID:              target.ThreadID,
		UserTimezone:          utils.NonEmpty(identity.UserTimezone, "UTC"),
		Timeout:               time.Duration(identity.TimeoutSeconds) * time.Second,
		Deliver:               plan.Deliver,
		IsHeartbeat:           true,
		ExtraSystemPrompt:     heartbeatExtraSystemPrompt(identity),
		InternalEvents:        plan.InternalEvents,
		SessionModelOverride:  plan.Model,
		HeartbeatLightContext: identity.HeartbeatLightContext,
	}
	result, err := runRuntime.Run(ctx, runReq)
	if err != nil {
		event.RecordAudit(ctx, r.Audit, r.Logger, core.AuditEvent{
			AgentID:    agentID,
			SessionKey: sessionKey,
			Message:    "heartbeat run failed",
			Data: map[string]any{
				"reason": req.Reason,
				"status": "failed",
				"error":  err.Error(),
			},
		})
		r.emitHeartbeatEvent(sessionKey, runReq.RunID, heartbeat.Event{
			Status:        "failed",
			Reason:        err.Error(),
			To:            plan.DeliveryTarget.To,
			Channel:       plan.DeliveryTarget.Channel,
			AccountID:     plan.DeliveryTarget.AccountID,
			Duration:      time.Since(startedAt),
			IndicatorType: heartbeat.IndicatorError,
		})
		return heartbeat.HeartbeatRunResult{}, err
	}
	mainIndex := heartbeatMainPayloadIndex(result.Payloads)
	mainText := ""
	mainHasMedia := false
	if mainIndex >= 0 {
		mainText = strings.TrimSpace(result.Payloads[mainIndex].Text)
		mainHasMedia = payloadHasMedia(result.Payloads[mainIndex])
	}
	if len(result.Payloads) == 0 {
		heartbeat.RestoreTranscriptSnapshot(r.Sessions, transcriptSnapshot)
		r.restoreHeartbeatUpdatedAt(sessionKey, previousUpdatedAt)
		if plan.Deliver && plan.Visibility.ShowOK {
			_ = r.deliverHeartbeatOK(ctx, sessionKey, runReq.RunID, plan.DeliveryTarget)
		}
		event.RecordAudit(ctx, r.Audit, r.Logger, core.AuditEvent{
			Data: map[string]any{
				"reason": req.Reason,
				"status": "ran",
			},
		})
		r.emitHeartbeatEvent(sessionKey, runReq.RunID, heartbeat.Event{
			Status:        "ok-empty",
			Reason:        req.Reason,
			To:            plan.DeliveryTarget.To,
			Channel:       plan.DeliveryTarget.Channel,
			AccountID:     plan.DeliveryTarget.AccountID,
			Duration:      time.Since(startedAt),
			Silent:        !plan.Visibility.ShowOK,
			IndicatorType: heartbeat.IndicatorOK,
		})
		return heartbeat.HeartbeatRunResult{Status: "ran"}, nil
	}
	skipMain := false
	if mainIndex >= 0 {
		if stripped, skip := heartbeat.StripHeartbeatToken(mainText, plan.AckMaxChars); skip {
			skipMain = true
			result.Payloads[mainIndex].Text = ""
		} else if stripped != mainText && stripped != "" {
			result.Payloads[mainIndex].Text = stripped
			mainText = stripped
		}
	}
	normalizedText := strings.TrimSpace(mainText)
	hasMedia := mainHasMedia
	if skipMain && !heartbeatHasDeliverablePayloads(result.Payloads, mainIndex, identity.HeartbeatIncludeReasoning, identity.HeartbeatSuppressToolErr) {
		heartbeat.RestoreTranscriptSnapshot(r.Sessions, transcriptSnapshot)
		r.restoreHeartbeatUpdatedAt(sessionKey, previousUpdatedAt)
		if plan.Deliver && plan.Visibility.ShowOK {
			_ = r.deliverHeartbeatOK(ctx, sessionKey, runReq.RunID, plan.DeliveryTarget)
		}
		r.emitHeartbeatEvent(sessionKey, runReq.RunID, heartbeat.Event{
			Status:        "ok-token",
			Reason:        req.Reason,
			To:            plan.DeliveryTarget.To,
			Channel:       plan.DeliveryTarget.Channel,
			AccountID:     plan.DeliveryTarget.AccountID,
			Duration:      time.Since(startedAt),
			Silent:        !plan.Visibility.ShowOK,
			IndicatorType: heartbeat.IndicatorOK,
		})
		return heartbeat.HeartbeatRunResult{Status: "ran"}, nil
	}
	if normalizedText != "" && heartbeat.ShouldSuppressDuplicate(entry, normalizedText, hasMedia, time.Now().UTC()) {
		heartbeat.RestoreTranscriptSnapshot(r.Sessions, transcriptSnapshot)
		r.restoreHeartbeatUpdatedAt(sessionKey, previousUpdatedAt)
		event.RecordAudit(ctx, r.Audit, r.Logger, core.AuditEvent{
			AgentID:    agentID,
			SessionKey: sessionKey,
			Message:    "heartbeat delivery suppressed as duplicate",
			Data: map[string]any{
				"reason": req.Reason,
				"status": "skipped",
			},
		})
		r.emitHeartbeatEvent(sessionKey, runReq.RunID, heartbeat.Event{
			Status:    "skipped",
			Reason:    "duplicate",
			To:        plan.DeliveryTarget.To,
			Preview:   normalizedText,
			Channel:   plan.DeliveryTarget.Channel,
			AccountID: plan.DeliveryTarget.AccountID,
			Duration:  time.Since(startedAt),
		})
		return heartbeat.HeartbeatRunResult{Status: "ran"}, nil
	}
	if plan.Deliver && !plan.Visibility.ShowAlerts {
		heartbeat.RestoreTranscriptSnapshot(r.Sessions, transcriptSnapshot)
		r.restoreHeartbeatUpdatedAt(sessionKey, previousUpdatedAt)
		r.emitHeartbeatEvent(sessionKey, runReq.RunID, heartbeat.Event{
			Status:        "skipped",
			Reason:        "alerts-disabled",
			To:            plan.DeliveryTarget.To,
			Preview:       normalizedText,
			Channel:       plan.DeliveryTarget.Channel,
			AccountID:     plan.DeliveryTarget.AccountID,
			Duration:      time.Since(startedAt),
			IndicatorType: heartbeat.IndicatorAlert,
		})
		return heartbeat.HeartbeatRunResult{Status: "ran"}, nil
	}
	if !plan.Deliver {
		r.emitHeartbeatEvent(sessionKey, runReq.RunID, heartbeat.Event{
			Status:    "skipped",
			Reason:    plan.DeliveryTarget.Reason,
			To:        plan.DeliveryTarget.To,
			Preview:   normalizedText,
			HasMedia:  hasMedia,
			Channel:   plan.DeliveryTarget.Channel,
			AccountID: plan.DeliveryTarget.AccountID,
			Duration:  time.Since(startedAt),
		})
		return heartbeat.HeartbeatRunResult{Status: "ran"}, nil
	}
	if buffered != nil && r.Deliverer != nil {
		records := buffered.RecordsSnapshot()
		deliveredFinal := false
		for _, record := range records {
			if record.Kind != core.ReplyKindFinal {
				continue
			}
			payload := record.Payload
			if mainIndex >= 0 && !payload.IsReasoning && strings.TrimSpace(payload.Text) == strings.TrimSpace(mainText) && skipMain {
				continue
			}
			if payload.IsError && identity.HeartbeatSuppressToolErr {
				continue
			}
			if payload.IsReasoning {
				if !identity.HeartbeatIncludeReasoning {
					continue
				}
				if text := strings.TrimSpace(payload.Text); text != "" && !strings.HasPrefix(text, "Reasoning:") {
					payload.Text = "Reasoning:\n" + text
				}
			}
			targetRecord := record.Target
			targetRecord.SessionKey = sessionKey
			if mainIndex >= 0 && strings.TrimSpace(payload.Text) == mainText && strings.TrimSpace(result.Payloads[mainIndex].Text) != mainText {
				payload.Text = result.Payloads[mainIndex].Text
			}
			if strings.TrimSpace(payload.Text) == "" && !payloadHasMedia(payload) {
				continue
			}
			if err := r.Deliverer.Deliver(ctx, core.ReplyKindFinal, payload, targetRecord); err != nil {
				return heartbeat.HeartbeatRunResult{}, err
			}
			deliveredFinal = true
		}
		if !deliveredFinal {
			payload, ok := heartbeatFallbackDeliveryPayload(result.Payloads, identity.HeartbeatIncludeReasoning, identity.HeartbeatSuppressToolErr, skipMain)
			if ok {
				if err := r.Deliverer.Deliver(ctx, core.ReplyKindFinal, payload, core.DeliveryTarget{
					SessionKey: sessionKey,
					Channel:    target.Channel,
					To:         target.To,
					AccountID:  target.AccountID,
					ThreadID:   target.ThreadID,
					RunID:      runReq.RunID,
				}); err != nil {
					return heartbeat.HeartbeatRunResult{}, err
				}
			}
		}
	}
	if plan.Deliver && strings.TrimSpace(normalizedText) != "" {
		r.recordHeartbeatDeliveryState(sessionKey, normalizedText)
	}
	event.RecordAudit(ctx, r.Audit, r.Logger, core.AuditEvent{
		Data: map[string]any{
			"reason": req.Reason,
			"status": "ran",
			"text":   strings.TrimSpace(result.Payloads[len(result.Payloads)-1].Text),
		},
	})
	r.emitHeartbeatEvent(sessionKey, runReq.RunID, heartbeat.Event{
		Status:        "sent",
		Reason:        req.Reason,
		To:            plan.DeliveryTarget.To,
		Preview:       normalizedText,
		HasMedia:      hasMedia,
		Channel:       plan.DeliveryTarget.Channel,
		AccountID:     plan.DeliveryTarget.AccountID,
		Duration:      time.Since(startedAt),
		IndicatorType: heartbeat.IndicatorAlert,
	})
	return heartbeat.HeartbeatRunResult{Status: "ran"}, nil
}

func (r *Runtime) emitHeartbeatEvent(sessionKey, runID string, evt heartbeat.Event) {
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now().UTC()
	}
	heartbeat.EmitEvent(evt)
	if r == nil || r.EventHub == nil || strings.TrimSpace(sessionKey) == "" {
		return
	}
	data := map[string]any{
		"type":          "heartbeat",
		"status":        strings.TrimSpace(evt.Status),
		"reason":        strings.TrimSpace(evt.Reason),
		"to":            strings.TrimSpace(evt.To),
		"preview":       strings.TrimSpace(evt.Preview),
		"channel":       strings.TrimSpace(evt.Channel),
		"accountId":     strings.TrimSpace(evt.AccountID),
		"hasMedia":      evt.HasMedia,
		"silent":        evt.Silent,
		"indicatorType": string(evt.IndicatorType),
		"durationMs":    evt.Duration.Milliseconds(),
	}
	r.EventHub.EmitAgentEvent(sessionKey, core.AgentEvent{
		RunID:      strings.TrimSpace(runID),
		OccurredAt: evt.Timestamp,
		SessionKey: sessionKey,
		Stream:     "lifecycle",
		Data:       data,
	})
}

func (r *Runtime) restoreHeartbeatUpdatedAt(sessionKey string, previous time.Time) {
	if r == nil || r.Sessions == nil || strings.TrimSpace(sessionKey) == "" || previous.IsZero() {
		return
	}
	_ = r.Sessions.Mutate(sessionKey, func(entry *core.SessionEntry) error {
		entry.UpdatedAt = previous
		return nil
	})
}

func (r *Runtime) recordHeartbeatDeliveryState(sessionKey, text string) {
	if r == nil || r.Sessions == nil || strings.TrimSpace(sessionKey) == "" {
		return
	}
	now := time.Now().UTC()
	_ = r.Sessions.Mutate(sessionKey, func(entry *core.SessionEntry) error {
		*entry = heartbeat.ApplyHeartbeatDelivery(*entry, text, now)
		return nil
	})
}

func payloadHasMedia(payload core.ReplyPayload) bool {
	return strings.TrimSpace(payload.MediaURL) != "" || len(payload.MediaURLs) > 0
}

func (r *Runtime) pruneHeartbeatRunTranscript(sessionKey, runID string) {
	if r == nil || r.Sessions == nil || strings.TrimSpace(sessionKey) == "" || strings.TrimSpace(runID) == "" {
		return
	}
	history, err := r.Sessions.LoadTranscript(sessionKey)
	if err != nil || len(history) == 0 {
		return
	}
	pruned := heartbeat.PruneRunTranscript(history, runID)
	if len(pruned) == len(history) {
		return
	}
	entry := r.Sessions.Entry(sessionKey)
	sessionID := ""
	if entry != nil {
		sessionID = entry.SessionID
	}
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	_ = r.Sessions.RewriteTranscript(sessionKey, sessionID, pruned)
}

func (r *Runtime) deliverHeartbeatOK(ctx context.Context, sessionKey, runID string, target heartbeat.DeliveryTargetPlan) error {
	if r == nil || r.Deliverer == nil || !target.Enabled {
		return nil
	}
	target = r.resolveHeartbeatReadyDeliveryTarget(ctx, target, core.ReplyPayload{Text: heartbeat.HeartbeatToken})
	if !target.Enabled {
		return nil
	}
	return r.Deliverer.Deliver(ctx, core.ReplyKindFinal, core.ReplyPayload{Text: heartbeat.HeartbeatToken}, core.DeliveryTarget{
		SessionKey: sessionKey,
		Channel:    target.Channel,
		To:         target.To,
		AccountID:  target.AccountID,
		ThreadID:   target.ThreadID,
		RunID:      runID,
	})
}

func heartbeatMainPayloadIndex(payloads []core.ReplyPayload) int {
	last := -1
	for i, payload := range payloads {
		if payload.IsReasoning {
			continue
		}
		last = i
	}
	return last
}

func heartbeatHasDeliverablePayloads(payloads []core.ReplyPayload, mainIndex int, includeReasoning, suppressToolErr bool) bool {
	_, ok := heartbeatFallbackDeliveryPayload(payloads, includeReasoning, suppressToolErr, mainIndex >= 0)
	return ok
}

func heartbeatFallbackDeliveryPayload(payloads []core.ReplyPayload, includeReasoning, suppressToolErr, skipMain bool) (core.ReplyPayload, bool) {
	for i := len(payloads) - 1; i >= 0; i-- {
		payload := payloads[i]
		if payload.IsError && suppressToolErr {
			continue
		}
		if payload.IsReasoning {
			if !includeReasoning {
				continue
			}
			if text := strings.TrimSpace(payload.Text); text != "" && !strings.HasPrefix(text, "Reasoning:") {
				payload.Text = "Reasoning:\n" + text
			}
		} else if skipMain {
			continue
		}
		if strings.TrimSpace(payload.Text) == "" && !payloadHasMedia(payload) {
			continue
		}
		return payload, true
	}
	return core.ReplyPayload{}, false
}

func heartbeatExtraSystemPrompt(identity core.AgentIdentity) string {
	if !identity.HeartbeatSuppressToolErr {
		return ""
	}
	return "If a tool encounters a recoverable warning or noisy error detail during a heartbeat, do not surface it to the user unless it materially changes the required follow-up."
}

func (r *Runtime) resolveHeartbeatReadyDeliveryTarget(ctx context.Context, plan heartbeat.DeliveryTargetPlan, payload core.ReplyPayload) heartbeat.DeliveryTargetPlan {
	if r == nil || r.Channels == nil || !plan.Enabled {
		return plan
	}
	resolved, _, err := r.Channels.ResolveOutboundMessage(ctx, core.DeliveryTarget{
		Channel:   plan.Channel,
		To:        plan.To,
		AccountID: plan.AccountID,
		ThreadID:  plan.ThreadID,
	}, payload)
	if err != nil {
		reason := "not-ready"
		lower := strings.ToLower(err.Error())
		switch {
		case strings.Contains(lower, "missing outbound target"):
			reason = "no-target"
		case strings.Contains(lower, "not registered"):
			reason = "channel-unavailable"
		}
		return heartbeat.DeliveryTargetPlan{Enabled: false, Reason: reason}
	}
	plan.Channel = strings.TrimSpace(resolved.Channel)
	plan.To = strings.TrimSpace(resolved.To)
	plan.AccountID = strings.TrimSpace(resolved.AccountID)
	plan.ThreadID = strings.TrimSpace(resolved.ThreadID)
	if plan.Channel == "" || plan.To == "" {
		return heartbeat.DeliveryTargetPlan{Enabled: false, Reason: "no-target"}
	}
	return plan
}

// RunDiskBudgetSweep enforces the session disk budget.
func (r *Runtime) RunDiskBudgetSweep() {
	if r == nil || r.Sessions == nil {
		return
	}
	budget := r.Sessions.DiskBudgetConfig()
	if budget.MaxDiskBytes <= 0 {
		return
	}
	result, err := sessionpkg.EnforceSessionDiskBudget(r.Sessions, "", budget, false)
	if err != nil {
		event.RecordAudit(context.Background(), r.Audit, r.Logger, core.AuditEvent{
			Level:   "warn",
			Message: "disk budget sweep failed: " + err.Error(),
		})
		return
	}
	if result != nil && result.RemovedEntries > 0 {
		event.RecordAudit(context.Background(), r.Audit, r.Logger, core.AuditEvent{
			Level:   "info",
			Message: "disk budget sweep completed",
			Data: map[string]any{
				"removedEntries": result.RemovedEntries,
				"freedBytes":     result.FreedBytes,
			},
		})
	}
}
