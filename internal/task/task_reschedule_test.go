package task

import (
	"testing"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
)

func TestTaskSchedulerReschedulesEveryTaskAfterRun(t *testing.T) {
	baseDir := t.TempDir()
	tasks, err := NewTaskScheduler(baseDir, config.TasksConfig{MaxConcurrent: 2})
	if err != nil {
		t.Fatalf("new task scheduler: %v", err)
	}
	now := time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC)
	tasks.SetNow(func() time.Time { return now })
	task, err := tasks.Schedule(TaskScheduleRequest{
		AgentID:          "main",
		Title:            "Every 2 min",
		Message:          "ping",
		ScheduleKind:     core.TaskScheduleEvery,
		ScheduleEveryMs:  120000,
		ScheduleAnchorMs: now.Add(-2 * time.Minute).UnixMilli(),
	})
	if err != nil {
		t.Fatalf("schedule every: %v", err)
	}
	if err := tasks.MarkRunFinished(task.ID, core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "ok"}}}, nil, time.Time{}); err != nil {
		t.Fatalf("mark finished: %v", err)
	}
	got := tasks.Get(task.ID)
	if got == nil {
		t.Fatalf("expected task")
	}
	if got.Status != core.TaskStatusScheduled {
		t.Fatalf("expected task to be rescheduled, got %+v", *got)
	}
	if !got.NextRunAt.After(now) {
		t.Fatalf("expected next run after now, got %+v", *got)
	}
}
