package infra

import (
	"strings"

	"github.com/kocort/kocort/internal/core"
)

const UnscheduledReminderNote = "Note: I did not schedule a reminder in this turn, so this will not trigger automatically."

var ReminderCommitmentPatterns = []string{
	"i'll remind",
	"i will remind",
	"i'll ping",
	"i will ping",
	"i'll follow up",
	"i will follow up",
	"i'll set a reminder",
	"i will set a reminder",
	"i'll schedule a reminder",
	"i will schedule a reminder",
}

func HasUnbackedReminderCommitment(text string) bool {
	normalized := strings.TrimSpace(strings.ToLower(text))
	if normalized == "" || strings.Contains(normalized, strings.ToLower(UnscheduledReminderNote)) {
		return false
	}
	for _, pattern := range ReminderCommitmentPatterns {
		if strings.Contains(normalized, pattern) {
			return true
		}
	}
	return false
}

func HasSessionRelatedScheduledTasks(tasks []core.TaskRecord, sessionKey string) bool {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return false
	}
	for _, task := range tasks {
		if task.Kind != core.TaskKindScheduled || task.Status == core.TaskStatusCanceled || task.Status == core.TaskStatusFailed || strings.TrimSpace(task.SessionKey) != sessionKey {
			continue
		}
		return true
	}
	return false
}

func AppendUnscheduledReminderNote(payloads []core.ReplyPayload) []core.ReplyPayload {
	appended := false
	out := make([]core.ReplyPayload, 0, len(payloads))
	for _, payload := range payloads {
		next := payload
		if !appended && !payload.IsError && HasUnbackedReminderCommitment(payload.Text) {
			appended = true
			next.Text = strings.TrimRight(strings.TrimSpace(payload.Text), "\n") + "\n\n" + UnscheduledReminderNote
		}
		out = append(out, next)
	}
	return out
}
