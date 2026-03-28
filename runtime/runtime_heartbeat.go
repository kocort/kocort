package runtime

import (
	"context"
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
	sessionKey := strings.TrimSpace(req.SessionKey)
	if sessionKey == "" {
		sessionKey = sessionpkg.BuildMainSessionKeyWithMain(agentID, config.ResolveSessionMainKey(r.Config))
	}
	events := r.SystemEvents.Drain(sessionKey)
	heartbeatFile, _ := infra.LoadWorkspaceTextFile(identity.WorkspaceDir, "HEARTBEAT.md") // optional; missing file is acceptable
	heartbeatFile = strings.TrimSpace(heartbeatFile)
	if len(events) == 0 && strings.TrimSpace(heartbeatFile) == "" {
		event.RecordAudit(ctx, r.Audit, r.Logger, core.AuditEvent{
			Data: map[string]any{
				"reason": "no-events",
				"status": "skipped",
			},
		})
		return heartbeat.HeartbeatRunResult{Status: "skipped", Reason: "no-events"}, nil
	}
	var internalEvents []core.TranscriptMessage
	pendingEventTexts := make([]string, 0, len(events))
	for _, event := range events {
		pendingEventTexts = append(pendingEventTexts, strings.TrimSpace(event.Text))
		internalEvents = append(internalEvents, core.TranscriptMessage{
			Type:      "system_event",
			Role:      "system",
			Text:      strings.TrimSpace(event.Text),
			Timestamp: event.Timestamp,
			Event:     "heartbeat",
		})
	}
	entry := r.Sessions.Entry(sessionKey)
	target := core.DeliveryContext{}
	if entry != nil && entry.DeliveryContext != nil {
		target = *entry.DeliveryContext
	}
	if strings.TrimSpace(req.SessionKey) != "" && entry != nil && entry.DeliveryContext == nil {
		target.Channel = entry.LastChannel
		target.To = entry.LastTo
		target.AccountID = entry.LastAccountID
		target.ThreadID = entry.LastThreadID
	}
	deliverHeartbeat := strings.EqualFold(utils.NonEmpty(identity.HeartbeatTarget, "none"), "last")
	if strings.TrimSpace(req.SessionKey) != "" && (strings.HasPrefix(strings.TrimSpace(req.Reason), "cron:") || strings.EqualFold(strings.TrimSpace(req.Reason), "wake")) {
		deliverHeartbeat = true
	}
	cronEvents := make([]string, 0, len(pendingEventTexts))
	hasExecCompletion := false
	for _, text := range pendingEventTexts {
		if heartbeat.IsExecCompletionEvent(text) {
			hasExecCompletion = true
		}
		if (strings.HasPrefix(strings.TrimSpace(req.Reason), "cron:") || strings.EqualFold(strings.TrimSpace(req.Reason), "wake")) && heartbeat.IsCronSystemEvent(text) {
			cronEvents = append(cronEvents, text)
		}
	}
	heartbeatPrompt := heartbeat.ResolveHeartbeatPrompt(identity.HeartbeatPrompt)
	if hasExecCompletion {
		heartbeatPrompt = heartbeat.BuildExecEventPrompt(deliverHeartbeat)
	} else if len(cronEvents) > 0 {
		heartbeatPrompt = heartbeat.BuildCronEventPrompt(cronEvents, deliverHeartbeat)
	}
	runRuntime := r
	var buffered *heartbeat.BufferedDeliverer
	if deliverHeartbeat {
		buffered = &heartbeat.BufferedDeliverer{}
		copyRuntime := *r
		copyRuntime.Deliverer = buffered
		runRuntime = &copyRuntime
	}
	runReq := core.AgentRunRequest{
		RunID:             sessionpkg.NewRunID(),
		AgentID:           agentID,
		SessionKey:        sessionKey,
		Message:           heartbeatPrompt,
		Channel:           target.Channel,
		To:                target.To,
		AccountID:         target.AccountID,
		ThreadID:          target.ThreadID,
		UserTimezone:      utils.NonEmpty(identity.UserTimezone, "UTC"),
		Timeout:           time.Duration(identity.TimeoutSeconds) * time.Second,
		Deliver:           deliverHeartbeat,
		IsHeartbeat:       true,
		ExtraSystemPrompt: "",
		InternalEvents:    internalEvents,
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
		return heartbeat.HeartbeatRunResult{}, err
	}
	if len(result.Payloads) == 0 {
		event.RecordAudit(ctx, r.Audit, r.Logger, core.AuditEvent{
			Data: map[string]any{
				"reason": req.Reason,
				"status": "ran",
			},
		})
		return heartbeat.HeartbeatRunResult{Status: "ran"}, nil
	}
	lastText := strings.TrimSpace(result.Payloads[len(result.Payloads)-1].Text)
	if stripped, skip := heartbeat.StripHeartbeatToken(lastText, identity.HeartbeatAckMaxChars); skip {
		return heartbeat.HeartbeatRunResult{Status: "ran"}, nil
	} else if stripped != lastText && stripped != "" {
		result.Payloads[len(result.Payloads)-1].Text = stripped
	}
	if buffered != nil && r.Deliverer != nil {
		records := buffered.RecordsSnapshot()
		deliveredFinal := false
		for _, record := range records {
			if record.Kind != core.ReplyKindFinal {
				continue
			}
			payload := record.Payload
			if strings.TrimSpace(payload.Text) == lastText && strings.TrimSpace(result.Payloads[len(result.Payloads)-1].Text) != lastText {
				payload.Text = result.Payloads[len(result.Payloads)-1].Text
			}
			if strings.TrimSpace(payload.Text) == "" {
				continue
			}
			if err := r.Deliverer.Deliver(ctx, core.ReplyKindFinal, payload, record.Target); err != nil {
				return heartbeat.HeartbeatRunResult{}, err
			}
			deliveredFinal = true
		}
		if !deliveredFinal {
			payload := result.Payloads[len(result.Payloads)-1]
			if strings.TrimSpace(payload.Text) != "" {
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
	event.RecordAudit(ctx, r.Audit, r.Logger, core.AuditEvent{
		Data: map[string]any{
			"reason": req.Reason,
			"status": "ran",
			"text":   strings.TrimSpace(result.Payloads[len(result.Payloads)-1].Text),
		},
	})
	return heartbeat.HeartbeatRunResult{Status: "ran"}, nil
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
