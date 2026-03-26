// notify.go — task failure alert delivery.
//
// Per RUNTIME_SLIM_PLAN P2-2: the failure-notification logic is extracted from
// runtime.Runtime.MaybeNotifyTaskFailure into a pure package-level function
// that only depends on core types.  The runtime method is deleted; callers use
// TaskScheduler.SetFailureNotifier to register a bound version of this function.
package task

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/event"
	sessionpkg "github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/utils"
)

// ---------------------------------------------------------------------------
// AuditLogger — minimal interface for recording audit events.
// Satisfied by *runtime.Runtime via its RecordAuditEvent method.
// ---------------------------------------------------------------------------

// NotifyTaskFailure sends a failure alert when a task has exceeded its failure
// threshold.  It is a pure function: all dependencies are passed explicitly so
// the logic can live in the task package without importing the runtime.
//
// Typical usage (from runtime_builder.go):
//
//	rt.Tasks.SetFailureNotifier(func(ctx context.Context, rec core.TaskRecord) error {
//	    return task.NotifyTaskFailure(ctx, rt.Deliverer, rt.Tasks, rt.Audit, rec)
//	})
func NotifyTaskFailure(
	ctx context.Context,
	deliverer core.Deliverer,
	tasks *TaskScheduler,
	audit event.AuditRecorder,
	t core.TaskRecord,
) error {
	if deliverer == nil {
		return nil
	}
	after := t.FailureAlertAfter
	if after <= 0 || t.ConsecutiveErrors < after {
		return nil
	}
	now := time.Now().UTC()
	if t.FailureAlertCooldownMs > 0 && !t.LastFailureAlertAt.IsZero() {
		if now.Before(t.LastFailureAlertAt.Add(time.Duration(t.FailureAlertCooldownMs) * time.Millisecond)) {
			return nil
		}
	}
	mode := strings.TrimSpace(t.FailureAlertMode)
	if mode == "" {
		mode = "announce"
	}
	if mode != "announce" {
		if audit != nil {
			event.RecordAudit(ctx, audit, nil, core.AuditEvent{
				Category:   core.AuditCategoryTask,
				Type:       "failure_alert_skipped",
				Level:      "warn",
				AgentID:    utils.NonEmpty(t.AgentID, sessionpkg.DefaultAgentID),
				SessionKey: t.SessionKey,
				TaskID:     t.ID,
				Message:    "unsupported failure alert mode",
				Data: map[string]any{
					"mode": mode,
				},
			})
		}
		return nil
	}
	target := core.DeliveryTarget{
		SessionKey: t.SessionKey,
		Channel:    utils.FirstNonEmpty(t.FailureAlertChannel, t.Channel),
		To:         utils.FirstNonEmpty(t.FailureAlertTo, t.To),
		AccountID:  utils.FirstNonEmpty(t.FailureAlertAccountID, t.AccountID),
		ThreadID:   t.ThreadID,
	}
	payload := core.ReplyPayload{
		Text:    fmt.Sprintf("Task failed: %s\n\n%s", utils.FirstNonEmpty(t.Title, t.ID), utils.FirstNonEmpty(t.LastError, "unknown error")),
		IsError: true,
	}
	if err := deliverer.Deliver(ctx, core.ReplyKindFinal, payload, target); err != nil {
		return err
	}
	if tasks != nil {
		_ = tasks.MarkFailureAlertSent(t.ID, now) // best-effort; failure is non-critical
	}
	if audit != nil {
		event.RecordAudit(ctx, audit, nil, core.AuditEvent{
			Category:   core.AuditCategoryTask,
			Type:       "failure_alert_sent",
			Level:      "warn",
			AgentID:    utils.NonEmpty(t.AgentID, sessionpkg.DefaultAgentID),
			SessionKey: t.SessionKey,
			TaskID:     t.ID,
			Message:    "task failure alert delivered",
			Data: map[string]any{
				"channel": target.Channel,
				"to":      target.To,
			},
		})
	}
	return nil
}
