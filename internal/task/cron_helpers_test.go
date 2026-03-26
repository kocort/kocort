package task

import (
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
)

// ---------------------------------------------------------------------------
// CronScheduleFromJob
// ---------------------------------------------------------------------------

func TestCronScheduleFromJobAt(t *testing.T) {
	now := time.Now().UTC()
	future := now.Add(time.Hour).Format(time.RFC3339)
	job := NormalizedCronJob{
		Schedule: map[string]any{"kind": "at", "at": future},
	}
	sched, err := CronScheduleFromJob(job, now)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sched.Kind != core.TaskScheduleAt {
		t.Errorf("expected kind=at, got %q", sched.Kind)
	}
}

func TestCronScheduleFromJobEvery(t *testing.T) {
	now := time.Now().UTC()
	job := NormalizedCronJob{
		Schedule: map[string]any{
			"kind":    "every",
			"everyMs": float64(60000),
		},
	}
	sched, err := CronScheduleFromJob(job, now)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sched.Kind != core.TaskScheduleEvery {
		t.Errorf("expected kind=every, got %q", sched.Kind)
	}
	if sched.EveryMs != 60000 {
		t.Errorf("expected everyMs=60000, got %d", sched.EveryMs)
	}
}

func TestCronScheduleFromJobCron(t *testing.T) {
	now := time.Now().UTC()
	job := NormalizedCronJob{
		Schedule: map[string]any{
			"kind": "cron",
			"expr": "*/5 * * * *",
			"tz":   "UTC",
		},
	}
	sched, err := CronScheduleFromJob(job, now)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sched.Kind != core.TaskScheduleCron {
		t.Errorf("expected kind=cron, got %q", sched.Kind)
	}
	if sched.Expr != "*/5 * * * *" {
		t.Errorf("expected expr=*/5 * * * *, got %q", sched.Expr)
	}
}

func TestCronScheduleFromJobEmpty(t *testing.T) {
	_, err := CronScheduleFromJob(NormalizedCronJob{}, time.Now())
	if err == nil {
		t.Error("expected error for empty schedule")
	}
}

func TestCronScheduleFromJobUnsupportedKind(t *testing.T) {
	job := NormalizedCronJob{
		Schedule: map[string]any{"kind": "unknown"},
	}
	_, err := CronScheduleFromJob(job, time.Now())
	if err == nil {
		t.Error("expected error for unsupported kind")
	}
}

// ---------------------------------------------------------------------------
// CronTaskMessageFromJob
// ---------------------------------------------------------------------------

func TestCronTaskMessageFromJob(t *testing.T) {
	t.Run("text_preferred", func(t *testing.T) {
		job := NormalizedCronJob{Text: "check status", Message: "fallback"}
		if got := CronTaskMessageFromJob(job); got != "check status" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("falls_back_to_message", func(t *testing.T) {
		job := NormalizedCronJob{Message: "fallback msg"}
		if got := CronTaskMessageFromJob(job); got != "fallback msg" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("empty", func(t *testing.T) {
		if got := CronTaskMessageFromJob(NormalizedCronJob{}); got != "" {
			t.Errorf("got %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// CronWakeModeFromJob
// ---------------------------------------------------------------------------

func TestCronWakeModeFromJob(t *testing.T) {
	tests := []struct {
		input string
		want  core.TaskWakeMode
	}{
		{"now", core.TaskWakeNow},
		{"next-heartbeat", core.TaskWakeNextHeartbeat},
		{"", core.TaskWakeNow},
		{"invalid", core.TaskWakeNow},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			job := NormalizedCronJob{WakeMode: tt.input}
			if got := CronWakeModeFromJob(job); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// CronTaskIDArg
// ---------------------------------------------------------------------------

func TestCronTaskIDArg(t *testing.T) {
	t.Run("jobId", func(t *testing.T) {
		if got := CronTaskIDArg(map[string]any{"jobId": "j1"}); got != "j1" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("id_fallback", func(t *testing.T) {
		if got := CronTaskIDArg(map[string]any{"id": "i1"}); got != "i1" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("jobId_preferred", func(t *testing.T) {
		if got := CronTaskIDArg(map[string]any{"jobId": "j1", "id": "i1"}); got != "j1" {
			t.Errorf("got %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// TruncateCronText
// ---------------------------------------------------------------------------

func TestTruncateCronText(t *testing.T) {
	t.Run("short_text", func(t *testing.T) {
		if got := TruncateCronText("hi", 10); got != "hi" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("long_text", func(t *testing.T) {
		got := TruncateCronText("this is a long message", 10)
		if len(got) > 10 {
			t.Errorf("expected truncated, got %q", got)
		}
		if got[len(got)-3:] != "..." {
			t.Errorf("expected ellipsis, got %q", got)
		}
	})
	t.Run("exact_length", func(t *testing.T) {
		if got := TruncateCronText("exact", 5); got != "exact" {
			t.Errorf("got %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// InferCronDeliveryFromSessionKey
// ---------------------------------------------------------------------------

func TestInferCronDeliveryFromSessionKey(t *testing.T) {
	t.Run("valid_direct", func(t *testing.T) {
		d := InferCronDeliveryFromSessionKey("agent:main:slack:direct:user1")
		if d == nil {
			t.Fatal("expected non-nil delivery")
		}
		if d.Channel != "slack" || d.To != "user1" {
			t.Errorf("got channel=%q, to=%q", d.Channel, d.To)
		}
	})
	t.Run("valid_group", func(t *testing.T) {
		d := InferCronDeliveryFromSessionKey("agent:main:telegram:group:chat123")
		if d == nil || d.Channel != "telegram" || d.To != "chat123" {
			t.Errorf("unexpected: %+v", d)
		}
	})
	t.Run("empty", func(t *testing.T) {
		if d := InferCronDeliveryFromSessionKey(""); d != nil {
			t.Error("expected nil for empty key")
		}
	})
	t.Run("short_key", func(t *testing.T) {
		if d := InferCronDeliveryFromSessionKey("agent:main:main"); d != nil {
			t.Error("expected nil for short key")
		}
	})
	t.Run("strips_thread", func(t *testing.T) {
		d := InferCronDeliveryFromSessionKey("agent:main:slack:direct:user1:thread:t1")
		if d == nil || d.To != "user1" {
			t.Errorf("expected thread stripped, got %+v", d)
		}
	})
}

// ---------------------------------------------------------------------------
// ReadCronJobMap
// ---------------------------------------------------------------------------

func TestReadCronJobMap(t *testing.T) {
	t.Run("from_job_key", func(t *testing.T) {
		args := map[string]any{
			"job": map[string]any{"name": "test-job"},
		}
		m := ReadCronJobMap(args)
		if m["name"] != "test-job" {
			t.Errorf("got %v", m)
		}
	})
	t.Run("top_level_fallback", func(t *testing.T) {
		args := map[string]any{"name": "top-level"}
		m := ReadCronJobMap(args)
		if m["name"] != "top-level" {
			t.Errorf("got %v", m)
		}
	})
	t.Run("empty", func(t *testing.T) {
		m := ReadCronJobMap(map[string]any{})
		if m != nil {
			t.Errorf("expected nil, got %v", m)
		}
	})
}

// ---------------------------------------------------------------------------
// NormalizeCronAddArgs
// ---------------------------------------------------------------------------

func TestNormalizeCronAddArgsBasic(t *testing.T) {
	args := map[string]any{
		"job": map[string]any{
			"name": "deploy-check",
			"schedule": map[string]any{
				"kind":    "every",
				"everyMs": float64(60000),
			},
			"payload": map[string]any{
				"kind": "systemevent",
				"text": "check deployment status",
			},
		},
	}
	job, err := NormalizeCronAddArgs(args)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if job.Name != "deploy-check" {
		t.Errorf("got name=%q", job.Name)
	}
	if job.Text != "check deployment status" {
		t.Errorf("got text=%q", job.Text)
	}
	if job.PayloadKind != "systemevent" {
		t.Errorf("got payloadKind=%q", job.PayloadKind)
	}
}

func TestNormalizeCronAddArgsEmptyJob(t *testing.T) {
	_, err := NormalizeCronAddArgs(map[string]any{})
	if err == nil {
		t.Error("expected error for empty args")
	}
}
