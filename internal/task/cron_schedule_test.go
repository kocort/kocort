package task

import (
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
)

// ---------------------------------------------------------------------------
// NormalizeTaskSchedule
// ---------------------------------------------------------------------------

func TestNormalizeTaskScheduleAt(t *testing.T) {
	now := time.Now().UTC()
	record := &core.TaskRecord{
		ScheduleAt: now.Add(time.Hour),
	}
	if err := NormalizeTaskSchedule(record, now); err != nil {
		t.Fatalf("err: %v", err)
	}
	if record.ScheduleKind != core.TaskScheduleAt {
		t.Errorf("expected kind=at, got %q", record.ScheduleKind)
	}
}

func TestNormalizeTaskScheduleEvery(t *testing.T) {
	now := time.Now().UTC()
	record := &core.TaskRecord{
		ScheduleEveryMs: 60000,
	}
	if err := NormalizeTaskSchedule(record, now); err != nil {
		t.Fatalf("err: %v", err)
	}
	if record.ScheduleKind != core.TaskScheduleEvery {
		t.Errorf("expected kind=every, got %q", record.ScheduleKind)
	}
	if record.IntervalSeconds != 60 {
		t.Errorf("expected intervalSeconds=60, got %d", record.IntervalSeconds)
	}
	if record.ScheduleAnchorMs <= 0 {
		t.Error("expected positive anchorMs")
	}
}

func TestNormalizeTaskScheduleEveryFromIntervalSeconds(t *testing.T) {
	now := time.Now().UTC()
	record := &core.TaskRecord{
		IntervalSeconds: 30,
	}
	if err := NormalizeTaskSchedule(record, now); err != nil {
		t.Fatalf("err: %v", err)
	}
	if record.ScheduleKind != core.TaskScheduleEvery {
		t.Errorf("expected kind=every, got %q", record.ScheduleKind)
	}
	if record.ScheduleEveryMs != 30000 {
		t.Errorf("expected everyMs=30000, got %d", record.ScheduleEveryMs)
	}
}

func TestNormalizeTaskScheduleCron(t *testing.T) {
	now := time.Now().UTC()
	record := &core.TaskRecord{
		ScheduleExpr: "*/5 * * * *",
	}
	if err := NormalizeTaskSchedule(record, now); err != nil {
		t.Fatalf("err: %v", err)
	}
	if record.ScheduleKind != core.TaskScheduleCron {
		t.Errorf("expected kind=cron, got %q", record.ScheduleKind)
	}
}

func TestNormalizeTaskScheduleEveryMissing(t *testing.T) {
	now := time.Now().UTC()
	record := &core.TaskRecord{
		ScheduleKind: core.TaskScheduleEvery,
	}
	if err := NormalizeTaskSchedule(record, now); err == nil {
		t.Error("expected error for every without everyMs")
	}
}

func TestNormalizeTaskScheduleNil(t *testing.T) {
	if err := NormalizeTaskSchedule(nil, time.Now()); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// ComputeTaskNextRunAt
// ---------------------------------------------------------------------------

func TestComputeTaskNextRunAtAtSchedule(t *testing.T) {
	now := time.Now().UTC()
	future := now.Add(time.Hour)
	next, err := ComputeTaskNextRunAt(core.TaskRecord{
		ScheduleKind: core.TaskScheduleAt,
		ScheduleAt:   future,
	}, now)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !next.Equal(future) {
		t.Errorf("expected %v, got %v", future, next)
	}
}

func TestComputeTaskNextRunAtAtPast(t *testing.T) {
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	next, err := ComputeTaskNextRunAt(core.TaskRecord{
		ScheduleKind: core.TaskScheduleAt,
		ScheduleAt:   past,
	}, now)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !next.IsZero() {
		t.Errorf("expected zero time for past schedule, got %v", next)
	}
}

func TestComputeTaskNextRunAtEvery(t *testing.T) {
	now := time.Now().UTC()
	anchor := now.Add(-5 * time.Minute)
	next, err := ComputeTaskNextRunAt(core.TaskRecord{
		ScheduleKind:     core.TaskScheduleEvery,
		ScheduleEveryMs:  60000,
		ScheduleAnchorMs: anchor.UnixMilli(),
	}, now)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if next.IsZero() {
		t.Fatal("expected non-zero next run time")
	}
	if !next.After(now) {
		t.Error("expected next run time to be in the future")
	}
}

func TestComputeTaskNextRunAtCron(t *testing.T) {
	now := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	next, err := ComputeTaskNextRunAt(core.TaskRecord{
		ScheduleKind: core.TaskScheduleCron,
		ScheduleExpr: "0 * * * *", // every hour
	}, now)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	expected := time.Date(2025, 3, 15, 11, 0, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Errorf("expected %v, got %v", expected, next)
	}
}

func TestComputeTaskNextRunAtUnsupported(t *testing.T) {
	_, err := ComputeTaskNextRunAt(core.TaskRecord{
		ScheduleKind: "invalid",
	}, time.Now())
	if err == nil {
		t.Error("expected error for unsupported kind")
	}
}

// ---------------------------------------------------------------------------
// ParseCronSchedule
// ---------------------------------------------------------------------------

func TestParseCronSchedule5Fields(t *testing.T) {
	sched, err := ParseCronSchedule("*/5 * * * *", "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	now := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	next := sched.Next(now)
	expected := time.Date(2025, 3, 15, 10, 5, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Errorf("expected %v, got %v", expected, next)
	}
}

func TestParseCronSchedule6Fields(t *testing.T) {
	sched, err := ParseCronSchedule("0 */5 * * * *", "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sched == nil {
		t.Fatal("expected non-nil schedule")
	}
}

func TestParseCronScheduleWithTimezone(t *testing.T) {
	sched, err := ParseCronSchedule("0 12 * * *", "America/New_York")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sched == nil {
		t.Fatal("expected non-nil schedule")
	}
}

func TestParseCronScheduleInvalidExpr(t *testing.T) {
	_, err := ParseCronSchedule("invalid", "")
	if err == nil {
		t.Error("expected error for invalid cron expr")
	}
}

func TestParseCronScheduleInvalidTimezone(t *testing.T) {
	_, err := ParseCronSchedule("*/5 * * * *", "Invalid/Zone")
	if err == nil {
		t.Error("expected error for invalid timezone")
	}
}

func TestParseCronScheduleEmpty(t *testing.T) {
	_, err := ParseCronSchedule("", "")
	if err == nil {
		t.Error("expected error for empty expr")
	}
}
