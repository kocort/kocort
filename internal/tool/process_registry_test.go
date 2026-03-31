package tool

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestNewProcessRegistry(t *testing.T) {
	r := NewProcessRegistry()
	if r == nil {
		t.Fatal("expected non-nil registry")
	}
	if list := r.List(); len(list) != 0 {
		t.Fatalf("expected empty list, got %d", len(list))
	}
}

func TestProcessRegistryStartAndPollChildSession(t *testing.T) {
	r := NewProcessRegistry()
	rec, err := r.Start(context.Background(), ProcessStartOptions{
		Command: "echo hello",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if rec.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	got, ok := r.Poll(rec.ID, 5*time.Second)
	if !ok {
		t.Fatal("expected to find process")
	}
	if got.Status != "completed" {
		t.Fatalf("expected completed, got %q", got.Status)
	}
	if !strings.Contains(got.Output, "hello") {
		t.Fatalf("expected output, got %q", got.Output)
	}
}

func TestProcessRegistryStartEmptyCommand(t *testing.T) {
	r := NewProcessRegistry()
	_, err := r.Start(context.Background(), ProcessStartOptions{})
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestProcessRegistryNil(t *testing.T) {
	var r *ProcessRegistry
	if _, err := r.Start(context.Background(), ProcessStartOptions{Command: "echo hi"}); err == nil {
		t.Fatal("expected error from nil registry")
	}
	if list := r.List(); list != nil {
		t.Fatalf("expected nil list, got %v", list)
	}
	if _, ok := r.Get("id"); ok {
		t.Fatal("expected missing session")
	}
}

func TestProcessRegistryListShowsBackgroundSessions(t *testing.T) {
	r := NewProcessRegistry()
	rec, err := r.Start(context.Background(), ProcessStartOptions{
		Command:      "sleep 1",
		Timeout:      5 * time.Second,
		Backgrounded: true,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	list := r.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 listed session, got %d", len(list))
	}
	if list[0].ID != rec.ID {
		t.Fatalf("expected session %q, got %+v", rec.ID, list[0])
	}
	_, _, _ = r.Kill(rec.ID)
}

func TestProcessRegistryWriteAndSubmitWithPTY(t *testing.T) {
	r := NewProcessRegistry()
	rec, err := r.Start(context.Background(), ProcessStartOptions{
		Command:      `IFS= read -r line; printf 'got:%s\n' "$line"`,
		Timeout:      5 * time.Second,
		Backgrounded: true,
		PTY:          true,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, ok, err := r.Write(rec.ID, "abc", false); err != nil || !ok {
		t.Fatalf("write: ok=%v err=%v", ok, err)
	}
	if _, ok, err := r.Submit(rec.ID); err != nil || !ok {
		t.Fatalf("submit: ok=%v err=%v", ok, err)
	}
	got, ok := r.Poll(rec.ID, 5*time.Second)
	if !ok {
		t.Fatal("expected session")
	}
	if got.Status != "completed" {
		t.Fatalf("expected completed, got %q", got.Status)
	}
	if !strings.Contains(got.Output, "got:abc") {
		t.Fatalf("expected PTY output, got %q", got.Output)
	}
}

func TestProcessRegistryClearAndRemove(t *testing.T) {
	r := NewProcessRegistry()
	rec, err := r.Start(context.Background(), ProcessStartOptions{
		Command:      "echo clear-me",
		Timeout:      5 * time.Second,
		Backgrounded: true,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	got, ok := r.Poll(rec.ID, 5*time.Second)
	if !ok || got.Status != "completed" {
		t.Fatalf("expected completed background session, got %+v ok=%v", got, ok)
	}
	if !r.Clear(rec.ID) {
		t.Fatal("expected clear to succeed")
	}
	if _, ok := r.Get(rec.ID); ok {
		t.Fatal("expected session removed after clear")
	}

	rec2, err := r.Start(context.Background(), ProcessStartOptions{
		Command:      "sleep 5",
		Timeout:      5 * time.Second,
		Backgrounded: true,
	})
	if err != nil {
		t.Fatalf("start second: %v", err)
	}
	removed, ok, err := r.Remove(rec2.ID)
	if err != nil || !ok {
		t.Fatalf("remove: record=%+v ok=%v err=%v", removed, ok, err)
	}
	if removed.Status != "killed" && removed.Status != "failed" {
		t.Fatalf("expected killed/failed remove result, got %q", removed.Status)
	}
}

func TestTailString(t *testing.T) {
	if got := tailString("abcdefghij", 5); got != "fghij" {
		t.Fatalf("got %q", got)
	}
}

func TestExitCodeOf(t *testing.T) {
	code := exitCodeOf(nil)
	if code == nil || *code != 0 {
		t.Fatalf("expected exit code 0, got %v", code)
	}
}

func TestCloneProcessRecord(t *testing.T) {
	now := time.Now()
	rec := ProcessSessionRecord{ID: "proc_1", EndedAt: &now}
	cloned := cloneProcessRecord(rec)
	if cloned.EndedAt == rec.EndedAt {
		t.Fatal("expected EndedAt to be copied")
	}
}
