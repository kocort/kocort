package task

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/session"
)

func TestMaybeNotifyTaskFailureSendsAlertAndRespectsCooldown(t *testing.T) {
	baseDir := t.TempDir()
	audit, err := infra.NewAuditLog(baseDir)
	if err != nil {
		t.Fatalf("new audit log: %v", err)
	}
	tasks, err := NewTaskScheduler(baseDir, config.TasksConfig{MaxConcurrent: 2})
	if err != nil {
		t.Fatalf("new task scheduler: %v", err)
	}
	mem := &delivery.MemoryDeliverer{}
	record, err := tasks.Schedule(TaskScheduleRequest{
		AgentID:                "main",
		SessionKey:             session.BuildMainSessionKey("main"),
		Title:                  "Reminder task",
		Message:                "Reminder failed",
		Channel:                "webchat",
		To:                     "webchat-user",
		Deliver:                true,
		FailureAlertAfter:      2,
		FailureAlertCooldownMs: 60_000,
		FailureAlertMode:       "announce",
		ScheduleKind:           core.TaskScheduleAt,
		ScheduleAt:             time.Now().UTC().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("schedule task: %v", err)
	}
	if err := tasks.MarkRunFinished(record.ID, core.AgentRunResult{}, fmt.Errorf("first failure"), time.Time{}); err != nil {
		t.Fatalf("mark first failure: %v", err)
	}
	if err := tasks.MarkRunFinished(record.ID, core.AgentRunResult{}, fmt.Errorf("second failure"), time.Time{}); err != nil {
		t.Fatalf("mark second failure: %v", err)
	}
	taskRecord := tasks.Get(record.ID)
	if taskRecord == nil {
		t.Fatalf("expected task")
	}
	if taskRecord.ConsecutiveErrors < 2 {
		t.Fatalf("expected consecutive errors to increment, got %+v", *taskRecord)
	}
	if err := NotifyTaskFailure(context.Background(), mem, tasks, audit, *taskRecord); err != nil {
		t.Fatalf("maybe notify task failure: %v", err)
	}
	if len(mem.Records) != 1 {
		t.Fatalf("expected one failure alert delivery, got %+v", mem.Records)
	}
	if !strings.Contains(mem.Records[0].Payload.Text, "Task failed: Reminder task") || !strings.Contains(mem.Records[0].Payload.Text, "second failure") {
		t.Fatalf("expected failure alert message, got %+v", mem.Records[0])
	}
	updated := tasks.Get(record.ID)
	if updated == nil || updated.LastFailureAlertAt.IsZero() {
		t.Fatalf("expected failure alert timestamp to persist, got %+v", updated)
	}
	if err := NotifyTaskFailure(context.Background(), mem, tasks, audit, *updated); err != nil {
		t.Fatalf("maybe notify task failure second call: %v", err)
	}
	if len(mem.Records) != 1 {
		t.Fatalf("expected cooldown to suppress duplicate alert, got %+v", mem.Records)
	}
}
