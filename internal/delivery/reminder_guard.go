// reminder_guard.go — standalone reminder-guard logic extracted from runtime.
//
// ApplyReminderGuard checks whether agent replies contain unbacked
// reminder commitments and appends a caveat note when appropriate.
package delivery

import (
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/infra"
)

// TaskLister returns the current list of scheduled tasks. Satisfied by
// *task.TaskScheduler and test doubles.
type TaskLister interface {
	List() []core.TaskRecord
}

// ApplyReminderGuard inspects result payloads for unbacked reminder
// commitments and appends a note when no cron task was actually
// scheduled. It is a pure function — no runtime fields are read.
func ApplyReminderGuard(tasks TaskLister, sessionKey string, successfulCronAdds int, result core.AgentRunResult) core.AgentRunResult {
	payloads := append([]core.ReplyPayload{}, result.Payloads...)
	hasCommitment := false
	for _, payload := range payloads {
		if payload.IsError {
			continue
		}
		if infra.HasUnbackedReminderCommitment(payload.Text) {
			hasCommitment = true
			break
		}
	}
	if hasCommitment && successfulCronAdds == 0 {
		covered := false
		if tasks != nil {
			covered = infra.HasSessionRelatedScheduledTasks(tasks.List(), sessionKey)
		}
		if !covered {
			payloads = infra.AppendUnscheduledReminderNote(payloads)
		}
	}
	result.Payloads = payloads
	return result
}
