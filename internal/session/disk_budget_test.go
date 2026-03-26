package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnforceSessionDiskBudget_NoBudget(t *testing.T) {
	result, err := EnforceSessionDiskBudget(nil, "", SessionDiskBudgetConfig{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result for disabled budget")
	}
}

func TestEnforceSessionDiskBudget_UnderBudget(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSessionStore(dir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Create a small file.
	transcriptsDir := filepath.Join(dir, "transcripts")
	_ = os.MkdirAll(transcriptsDir, 0o755)
	_ = os.WriteFile(filepath.Join(transcriptsDir, "test.jsonl"), []byte("hello"), 0o644)

	result, err := EnforceSessionDiskBudget(store, "", SessionDiskBudgetConfig{
		MaxDiskBytes:   1024 * 1024, // 1MB
		HighWaterRatio: 0.8,
	}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.OverBudget {
		t.Fatal("should not be over budget")
	}
	if result.RemovedFiles != 0 || result.RemovedEntries != 0 {
		t.Fatalf("should not have removed anything: files=%d entries=%d", result.RemovedFiles, result.RemovedEntries)
	}
}

func TestEnforceSessionDiskBudget_WarnOnly(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSessionStore(dir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	transcriptsDir := filepath.Join(dir, "transcripts")
	_ = os.MkdirAll(transcriptsDir, 0o755)
	// Create a large file to exceed budget.
	data := make([]byte, 2000)
	_ = os.WriteFile(filepath.Join(transcriptsDir, "big.jsonl"), data, 0o644)

	result, err := EnforceSessionDiskBudget(store, "", SessionDiskBudgetConfig{
		MaxDiskBytes:   100, // Very small budget.
		HighWaterRatio: 0.8,
	}, true) // warnOnly
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.OverBudget {
		t.Fatal("should be over budget")
	}
	if result.RemovedFiles != 0 {
		t.Fatalf("warnOnly should not remove files, got %d", result.RemovedFiles)
	}
}
