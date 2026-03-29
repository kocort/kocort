package delivery_test

import (
	"strings"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/task"
)

func TestApplyReminderReplyGuardSuppressesNoteWhenSessionHasScheduledTask(t *testing.T) {
	baseDir := t.TempDir()
	tasks, err := task.NewTaskScheduler(baseDir, config.TasksConfig{MaxConcurrent: 2})
	if err != nil {
		t.Fatalf("new task scheduler: %v", err)
	}
	_, err = tasks.Schedule(task.TaskScheduleRequest{
		AgentID:    "main",
		SessionKey: "agent:main:main",
		Title:      "existing reminder",
		Message:    "Reminder: ping",
		RunAt:      time.Now().UTC().Add(5 * time.Minute),
		Deliver:    true,
	})
	if err != nil {
		t.Fatalf("schedule task: %v", err)
	}
	result := delivery.ApplyReminderGuard(tasks, "agent:main:main", 0, core.AgentRunResult{
		Payloads: []core.ReplyPayload{{Text: "I'll remind you tomorrow morning."}},
	})
	if len(result.Payloads) != 1 {
		t.Fatalf("expected single payload, got %+v", result.Payloads)
	}
	if strings.Contains(result.Payloads[0].Text, infra.UnscheduledReminderNote) {
		t.Fatalf("expected no guard note when session already has scheduled task, got %+v", result.Payloads)
	}
}
