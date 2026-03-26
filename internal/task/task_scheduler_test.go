package task

import (
	"context"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
)

func newTestScheduler(t *testing.T) *TaskScheduler {
	t.Helper()
	dir := t.TempDir()
	s, err := NewTaskScheduler(dir, config.TasksConfig{})
	if err != nil {
		t.Fatalf("NewTaskScheduler: %v", err)
	}
	return s
}

// ---------------------------------------------------------------------------
// NewTaskScheduler
// ---------------------------------------------------------------------------

func TestNewTaskScheduler(t *testing.T) {
	s := newTestScheduler(t)
	if s == nil {
		t.Fatal("expected non-nil scheduler")
	}
	list := s.List()
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d", len(list))
	}
}

// ---------------------------------------------------------------------------
// Schedule
// ---------------------------------------------------------------------------

func TestTaskSchedulerSchedule(t *testing.T) {
	s := newTestScheduler(t)
	record, err := s.Schedule(TaskScheduleRequest{
		Title:        "test-task",
		Message:      "do something",
		ScheduleKind: core.TaskScheduleAt,
		ScheduleAt:   time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if record.ID == "" {
		t.Error("expected non-empty ID")
	}
	if record.Title != "test-task" {
		t.Errorf("got title=%q", record.Title)
	}
	if record.Status != core.TaskStatusScheduled {
		t.Errorf("expected status=scheduled, got %q", record.Status)
	}
}

func TestTaskSchedulerScheduleEvery(t *testing.T) {
	s := newTestScheduler(t)
	record, err := s.Schedule(TaskScheduleRequest{
		Title:           "recurring",
		ScheduleKind:    core.TaskScheduleEvery,
		ScheduleEveryMs: 60000,
	})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if record.ScheduleKind != core.TaskScheduleEvery {
		t.Errorf("expected kind=every, got %q", record.ScheduleKind)
	}
	if record.NextRunAt.IsZero() {
		t.Error("expected non-zero NextRunAt")
	}
}

// ---------------------------------------------------------------------------
// Get
// ---------------------------------------------------------------------------

func TestTaskSchedulerGet(t *testing.T) {
	s := newTestScheduler(t)
	record, _ := s.Schedule(TaskScheduleRequest{
		Title:        "task1",
		ScheduleKind: core.TaskScheduleAt,
		ScheduleAt:   time.Now().UTC().Add(time.Hour),
	})
	got := s.Get(record.ID)
	if got == nil {
		t.Fatal("expected non-nil task")
	}
	if got.Title != "task1" {
		t.Errorf("got title=%q", got.Title)
	}
}

func TestTaskSchedulerGetMissing(t *testing.T) {
	s := newTestScheduler(t)
	if got := s.Get("nonexistent"); got != nil {
		t.Error("expected nil for missing task")
	}
}

// ---------------------------------------------------------------------------
// Cancel
// ---------------------------------------------------------------------------

func TestTaskSchedulerCancel(t *testing.T) {
	s := newTestScheduler(t)
	record, _ := s.Schedule(TaskScheduleRequest{
		Title:        "cancel-me",
		ScheduleKind: core.TaskScheduleAt,
		ScheduleAt:   time.Now().UTC().Add(time.Hour),
	})
	canceled, ok, err := s.Cancel(record.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if !ok {
		t.Error("expected ok=true")
	}
	if canceled.Status != core.TaskStatusCanceled {
		t.Errorf("expected status=canceled, got %q", canceled.Status)
	}
}

func TestTaskSchedulerCancelMissing(t *testing.T) {
	s := newTestScheduler(t)
	_, ok, err := s.Cancel("nonexistent")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if ok {
		t.Error("expected ok=false for missing task")
	}
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

func TestTaskSchedulerDelete(t *testing.T) {
	s := newTestScheduler(t)
	record, _ := s.Schedule(TaskScheduleRequest{
		Title:        "delete-me",
		ScheduleKind: core.TaskScheduleAt,
		ScheduleAt:   time.Now().UTC().Add(time.Hour),
	})
	deleted, ok, err := s.Delete(record.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !ok {
		t.Error("expected ok=true")
	}
	if deleted.Title != "delete-me" {
		t.Errorf("got title=%q", deleted.Title)
	}
	if s.Get(record.ID) != nil {
		t.Error("expected task to be gone")
	}
}

// ---------------------------------------------------------------------------
// Due
// ---------------------------------------------------------------------------

func TestTaskSchedulerDue(t *testing.T) {
	s := newTestScheduler(t)
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	s.SetNow(func() time.Time { return now })
	// Schedule one in the past (due) and one in the future (not due).
	s.Schedule(TaskScheduleRequest{
		Title:        "past",
		ScheduleKind: core.TaskScheduleAt,
		ScheduleAt:   now.Add(-time.Hour),
	})
	s.Schedule(TaskScheduleRequest{
		Title:        "future",
		ScheduleKind: core.TaskScheduleAt,
		ScheduleAt:   now.Add(time.Hour),
	})

	due := s.Due(now)
	if len(due) != 1 {
		t.Fatalf("expected 1 due task, got %d", len(due))
	}
	if due[0].Title != "past" {
		t.Errorf("expected past task to be due, got %q", due[0].Title)
	}
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func TestTaskSchedulerUpdate(t *testing.T) {
	s := newTestScheduler(t)
	record, _ := s.Schedule(TaskScheduleRequest{
		Title:        "original",
		Message:      "do this",
		ScheduleKind: core.TaskScheduleAt,
		ScheduleAt:   time.Now().UTC().Add(time.Hour),
	})

	updated, ok, err := s.Update(record.ID, TaskScheduleRequest{
		Title:   "updated-title",
		Message: "do that",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !ok {
		t.Error("expected ok=true")
	}
	if updated.Title != "updated-title" {
		t.Errorf("got title=%q", updated.Title)
	}
	if updated.Message != "do that" {
		t.Errorf("got message=%q", updated.Message)
	}
}

func TestTaskSchedulerUpdateMissing(t *testing.T) {
	s := newTestScheduler(t)
	_, ok, err := s.Update("nonexistent", TaskScheduleRequest{Title: "x"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if ok {
		t.Error("expected ok=false for missing task")
	}
}

// ---------------------------------------------------------------------------
// MarkRunStarted / MarkRunFinished
// ---------------------------------------------------------------------------

func TestTaskSchedulerMarkRunStartedFinished(t *testing.T) {
	s := newTestScheduler(t)
	record, _ := s.Schedule(TaskScheduleRequest{
		Title:        "lifecycle",
		ScheduleKind: core.TaskScheduleAt,
		ScheduleAt:   time.Now().UTC().Add(-time.Minute),
	})

	err := s.MarkRunStarted(record.ID, "run_1", "agent:main:main")
	if err != nil {
		t.Fatalf("markStarted: %v", err)
	}
	got := s.Get(record.ID)
	if got.Status != core.TaskStatusRunning {
		t.Errorf("expected status=running, got %q", got.Status)
	}

	err = s.MarkRunFinished(record.ID, core.AgentRunResult{
		Payloads: []core.ReplyPayload{{Text: "done"}},
	}, nil, time.Time{})
	if err != nil {
		t.Fatalf("markFinished: %v", err)
	}
	got = s.Get(record.ID)
	if got.Status != core.TaskStatusCompleted {
		t.Errorf("expected status=completed, got %q", got.Status)
	}
}

func TestTaskSchedulerMarkRunFinishedWithError(t *testing.T) {
	s := newTestScheduler(t)
	record, _ := s.Schedule(TaskScheduleRequest{
		Title:        "fail-task",
		ScheduleKind: core.TaskScheduleAt,
		ScheduleAt:   time.Now().UTC().Add(-time.Minute),
	})
	s.MarkRunStarted(record.ID, "run_1", "")

	err := s.MarkRunFinished(record.ID, core.AgentRunResult{}, context.DeadlineExceeded, time.Time{})
	if err != nil {
		t.Fatalf("markFinished: %v", err)
	}
	got := s.Get(record.ID)
	if got.Status != core.TaskStatusFailed {
		t.Errorf("expected status=failed, got %q", got.Status)
	}
	if got.LastError == "" {
		t.Error("expected non-empty lastError")
	}
}

// ---------------------------------------------------------------------------
// Summary
// ---------------------------------------------------------------------------

func TestTaskSchedulerSummary(t *testing.T) {
	s := newTestScheduler(t)
	s.Schedule(TaskScheduleRequest{
		Title:        "task1",
		ScheduleKind: core.TaskScheduleAt,
		ScheduleAt:   time.Now().UTC().Add(time.Hour),
	})
	s.Schedule(TaskScheduleRequest{
		Title:        "task2",
		ScheduleKind: core.TaskScheduleAt,
		ScheduleAt:   time.Now().UTC().Add(time.Hour),
	})

	summary := s.Summary()
	if summary.Total != 2 {
		t.Errorf("expected total=2, got %d", summary.Total)
	}
	if !summary.Enabled {
		t.Error("expected enabled=true")
	}
}

// ---------------------------------------------------------------------------
// Persistence
// ---------------------------------------------------------------------------

func TestTaskSchedulerPersistence(t *testing.T) {
	dir := t.TempDir()
	s1, err := NewTaskScheduler(dir, config.TasksConfig{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	s1.Schedule(TaskScheduleRequest{
		Title:        "persist-me",
		ScheduleKind: core.TaskScheduleAt,
		ScheduleAt:   time.Now().UTC().Add(time.Hour),
	})

	// Create second scheduler from same dir.
	s2, err := NewTaskScheduler(dir, config.TasksConfig{})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	list := s2.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 task after reload, got %d", len(list))
	}
	if list[0].Title != "persist-me" {
		t.Errorf("got title=%q", list[0].Title)
	}
}

// ---------------------------------------------------------------------------
// EventSink
// ---------------------------------------------------------------------------

func TestTaskSchedulerEventSink(t *testing.T) {
	s := newTestScheduler(t)
	var events []string
	s.SetEventSink(func(r core.TaskRecord, eventType string, data map[string]any) {
		events = append(events, eventType)
	})
	s.Schedule(TaskScheduleRequest{
		Title:        "evented",
		ScheduleKind: core.TaskScheduleAt,
		ScheduleAt:   time.Now().UTC().Add(time.Hour),
	})
	if len(events) != 1 || events[0] != "scheduled" {
		t.Errorf("expected [scheduled], got %v", events)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func TestExtractFinalText(t *testing.T) {
	t.Run("with_payloads", func(t *testing.T) {
		result := core.AgentRunResult{
			Payloads: []core.ReplyPayload{
				{Text: "first"},
				{Text: "last"},
			},
		}
		if got := ExtractFinalText(result); got != "last" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("empty_payloads", func(t *testing.T) {
		result := core.AgentRunResult{}
		if got := ExtractFinalText(result); got != "" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("skips_empty", func(t *testing.T) {
		result := core.AgentRunResult{
			Payloads: []core.ReplyPayload{
				{Text: "content"},
				{Text: ""},
			},
		}
		if got := ExtractFinalText(result); got != "content" {
			t.Errorf("got %q", got)
		}
	})
}
