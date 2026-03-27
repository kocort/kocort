package tool

import (
	"context"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// NewProcessRegistry
// ---------------------------------------------------------------------------

func TestNewProcessRegistry(t *testing.T) {
	r := NewProcessRegistry()
	if r == nil {
		t.Fatal("expected non-nil registry")
	}
	if list := r.List(); len(list) != 0 {
		t.Errorf("expected empty list, got %d", len(list))
	}
}

// ---------------------------------------------------------------------------
// Start / Get / List
// ---------------------------------------------------------------------------

func TestProcessRegistryStartAndGet(t *testing.T) {
	r := NewProcessRegistry()
	rec, err := r.Start(context.Background(), ProcessStartOptions{
		Command: "echo hello",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if rec.ID == "" {
		t.Error("expected non-empty ID")
	}
	if rec.Status != "running" {
		t.Errorf("expected status=running, got %q", rec.Status)
	}

	got, ok := r.Poll(rec.ID, 2*time.Second)
	if !ok {
		t.Fatal("expected to find process")
	}
	if got.Status != "completed" {
		t.Errorf("expected status=completed, got %q", got.Status)
	}
}

func TestProcessRegistryStartEmptyCommand(t *testing.T) {
	r := NewProcessRegistry()
	_, err := r.Start(context.Background(), ProcessStartOptions{
		Command: "",
	})
	if err == nil {
		t.Error("expected error for empty command")
	}
}

func TestProcessRegistryNil(t *testing.T) {
	var r *ProcessRegistry
	_, err := r.Start(context.Background(), ProcessStartOptions{Command: "echo hi"})
	if err == nil {
		t.Error("expected error from nil registry")
	}
	if list := r.List(); list != nil {
		t.Errorf("expected nil list from nil registry, got %v", list)
	}
	_, ok := r.Get("id")
	if ok {
		t.Error("expected not-ok from nil registry Get")
	}
}

// ---------------------------------------------------------------------------
// Poll
// ---------------------------------------------------------------------------

func TestProcessRegistryPoll(t *testing.T) {
	r := NewProcessRegistry()
	rec, err := r.Start(context.Background(), ProcessStartOptions{
		Command: "echo poll_test",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	got, ok := r.Poll(rec.ID, 2*time.Second)
	if !ok {
		t.Fatal("expected to find process")
	}
	if got.Status != "completed" {
		t.Errorf("expected completed after poll, got %q", got.Status)
	}
}

func TestProcessRegistryPollNonexistent(t *testing.T) {
	r := NewProcessRegistry()
	_, ok := r.Poll("nonexistent", 100*time.Millisecond)
	if ok {
		t.Error("expected not-ok for nonexistent process")
	}
}

func TestProcessRegistryPollEmptyID(t *testing.T) {
	r := NewProcessRegistry()
	_, ok := r.Poll("", 100*time.Millisecond)
	if ok {
		t.Error("expected not-ok for empty ID")
	}
}

// ---------------------------------------------------------------------------
// Kill
// ---------------------------------------------------------------------------

func TestProcessRegistryKill(t *testing.T) {
	r := NewProcessRegistry()
	rec, err := r.Start(context.Background(), ProcessStartOptions{
		Command: "sleep 5",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	got, found, err := r.Kill(rec.ID)
	if err != nil {
		t.Fatalf("kill: %v", err)
	}
	if !found {
		t.Error("expected found=true")
	}
	if got.Status != "killed" {
		t.Errorf("expected status=killed, got %q", got.Status)
	}
}

func TestProcessRegistryKillNonexistent(t *testing.T) {
	r := NewProcessRegistry()
	_, found, err := r.Kill("nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Error("expected found=false")
	}
}

func TestProcessRegistryKillNil(t *testing.T) {
	var r *ProcessRegistry
	_, _, err := r.Kill("id")
	if err == nil {
		t.Error("expected error from nil registry")
	}
}

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

func TestProcessRegistryList(t *testing.T) {
	r := NewProcessRegistry()
	_, _ = r.Start(context.Background(), ProcessStartOptions{
		Command: "echo first",
		Timeout: 5 * time.Second,
	})
	_, _ = r.Start(context.Background(), ProcessStartOptions{
		Command: "echo second",
		Timeout: 5 * time.Second,
	})

	time.Sleep(500 * time.Millisecond)
	list := r.List()
	if len(list) < 2 {
		t.Errorf("expected at least 2 processes, got %d", len(list))
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func TestTailString(t *testing.T) {
	t.Run("short_string", func(t *testing.T) {
		if got := tailString("hello", 100); got != "hello" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("truncates", func(t *testing.T) {
		if got := tailString("abcdefghij", 5); got != "fghij" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("empty", func(t *testing.T) {
		if got := tailString("", 10); got != "" {
			t.Errorf("got %q", got)
		}
	})
}

func TestExitCodeOf(t *testing.T) {
	t.Run("nil_error", func(t *testing.T) {
		code := exitCodeOf(nil)
		if code == nil || *code != 0 {
			t.Errorf("expected exit code 0, got %v", code)
		}
	})
}

func TestCloneProcessRecord(t *testing.T) {
	now := time.Now()
	rec := ProcessSessionRecord{
		ID:      "proc_1",
		EndedAt: &now,
	}
	cloned := cloneProcessRecord(rec)
	if cloned.ID != rec.ID {
		t.Error("expected same ID")
	}
	if cloned.EndedAt == rec.EndedAt {
		t.Error("expected different EndedAt pointer")
	}
	if !cloned.EndedAt.Equal(*rec.EndedAt) {
		t.Error("expected same EndedAt value")
	}
}
