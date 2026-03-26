package task

import (
	"sync"
	"testing"
)

func TestNewActiveRunRegistry(t *testing.T) {
	r := NewActiveRunRegistry()
	if r == nil {
		t.Fatal("expected non-nil registry")
	}
	if r.TotalCount() != 0 {
		t.Error("expected 0 total count")
	}
}

func TestActiveRunRegistryStartAndDone(t *testing.T) {
	r := NewActiveRunRegistry()
	done := r.Start("key1")
	if !r.IsActive("key1") {
		t.Error("expected active after Start")
	}
	if r.Count("key1") != 1 {
		t.Errorf("expected count=1, got %d", r.Count("key1"))
	}
	if r.TotalCount() != 1 {
		t.Errorf("expected total=1, got %d", r.TotalCount())
	}
	done()
	if r.IsActive("key1") {
		t.Error("expected not active after done")
	}
	if r.Count("key1") != 0 {
		t.Errorf("expected count=0, got %d", r.Count("key1"))
	}
}

func TestActiveRunRegistryMultipleStarts(t *testing.T) {
	r := NewActiveRunRegistry()
	done1 := r.Start("key1")
	done2 := r.Start("key1")
	if r.Count("key1") != 2 {
		t.Errorf("expected 2, got %d", r.Count("key1"))
	}
	done1()
	if r.Count("key1") != 1 {
		t.Errorf("expected 1 after first done, got %d", r.Count("key1"))
	}
	done2()
	if r.Count("key1") != 0 {
		t.Errorf("expected 0 after both done, got %d", r.Count("key1"))
	}
}

func TestActiveRunRegistryDoneIdempotent(t *testing.T) {
	r := NewActiveRunRegistry()
	done := r.Start("key1")
	done()
	done() // Should not panic or double-decrement.
	if r.Count("key1") != 0 {
		t.Errorf("expected 0, got %d", r.Count("key1"))
	}
}

func TestActiveRunRegistryStartRunWithCancel(t *testing.T) {
	r := NewActiveRunRegistry()
	canceled := false
	done := r.StartRun("key1", "run_1", func() { canceled = true })
	if !r.IsRunActive("key1", "run_1") {
		t.Error("expected run_1 active")
	}
	found := r.CancelRun("key1", "run_1")
	if !found {
		t.Error("expected CancelRun to find run")
	}
	if !canceled {
		t.Error("expected cancel function called")
	}
	done()
	if r.IsRunActive("key1", "run_1") {
		t.Error("expected run_1 not active after done")
	}
}

func TestActiveRunRegistryCancelRunNotFound(t *testing.T) {
	r := NewActiveRunRegistry()
	found := r.CancelRun("key1", "nonexistent")
	if found {
		t.Error("expected not found")
	}
}

func TestActiveRunRegistryCancelSession(t *testing.T) {
	r := NewActiveRunRegistry()
	canceled := map[string]bool{}
	var mu sync.Mutex
	r.StartRun("key1", "r1", func() { mu.Lock(); canceled["r1"] = true; mu.Unlock() })
	r.StartRun("key1", "r2", func() { mu.Lock(); canceled["r2"] = true; mu.Unlock() })
	runIDs := r.CancelSession("key1")
	if len(runIDs) != 2 {
		t.Errorf("expected 2 canceled runs, got %d", len(runIDs))
	}
	mu.Lock()
	if !canceled["r1"] || !canceled["r2"] {
		t.Error("expected both cancel functions called")
	}
	mu.Unlock()
}

func TestActiveRunRegistryCancelSessionEmpty(t *testing.T) {
	r := NewActiveRunRegistry()
	runIDs := r.CancelSession("nonexistent")
	if runIDs != nil {
		t.Errorf("expected nil, got %v", runIDs)
	}
}

func TestActiveRunRegistrySnapshot(t *testing.T) {
	r := NewActiveRunRegistry()
	r.StartRun("key1", "r1", nil)
	r.Start("key2")
	snap := r.Snapshot()
	if snap.Total != 2 {
		t.Errorf("expected total=2, got %d", snap.Total)
	}
	if snap.BySession["key1"] != 1 {
		t.Errorf("expected key1=1, got %d", snap.BySession["key1"])
	}
	if snap.BySession["key2"] != 1 {
		t.Errorf("expected key2=1, got %d", snap.BySession["key2"])
	}
}

func TestActiveRunRegistryConcurrent(t *testing.T) {
	r := NewActiveRunRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done := r.Start("concurrent")
			done()
		}()
	}
	wg.Wait()
	if r.Count("concurrent") != 0 {
		t.Errorf("expected 0 after concurrent ops, got %d", r.Count("concurrent"))
	}
}
