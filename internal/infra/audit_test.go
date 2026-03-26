package infra

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
)

func TestAuditLog_RecordAndList(t *testing.T) {
	dir := t.TempDir()
	log, err := NewAuditLog(dir)
	if err != nil {
		t.Fatal(err)
	}

	event := core.AuditEvent{
		Category:   "test",
		Type:       "unit_test",
		Level:      "info",
		Message:    "hello world",
		SessionKey: "session-1",
		RunID:      "run-1",
		TaskID:     "task-1",
		ToolName:   "shell",
		Channel:    "cli",
		Data:       map[string]any{"key": "value"},
	}
	if err := log.Record(context.Background(), event); err != nil {
		t.Fatal(err)
	}

	events, err := log.List(context.Background(), core.AuditQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Message != "hello world" {
		t.Errorf("message = %q", events[0].Message)
	}
	if events[0].ID == "" {
		t.Error("ID should be auto-generated")
	}
	if events[0].OccurredAt.IsZero() {
		t.Error("OccurredAt should be set")
	}
}

func TestAuditLog_ListFilters(t *testing.T) {
	dir := t.TempDir()
	log, err := NewAuditLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	events := []core.AuditEvent{
		{Category: "system", Type: "boot", Level: "info", Message: "startup", SessionKey: "s1", RunID: "r1"},
		{Category: "tool", Type: "call", Level: "debug", Message: "tool call", SessionKey: "s2", RunID: "r2", TaskID: "t1"},
		{Category: "system", Type: "shutdown", Level: "warn", Message: "shutting down"},
	}
	for _, event := range events {
		if err := log.Record(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("filter_by_category", func(t *testing.T) {
		result, err := log.List(ctx, core.AuditQuery{Category: "system"})
		if err != nil {
			t.Fatal(err)
		}
		if len(result) != 2 {
			t.Errorf("expected 2 events, got %d", len(result))
		}
	})

	t.Run("filter_by_type", func(t *testing.T) {
		result, err := log.List(ctx, core.AuditQuery{Type: "boot"})
		if err != nil {
			t.Fatal(err)
		}
		if len(result) != 1 {
			t.Errorf("expected 1 event, got %d", len(result))
		}
	})

	t.Run("filter_by_level", func(t *testing.T) {
		result, err := log.List(ctx, core.AuditQuery{Level: "warn"})
		if err != nil {
			t.Fatal(err)
		}
		if len(result) != 1 {
			t.Errorf("expected 1 event, got %d", len(result))
		}
	})

	t.Run("filter_by_session_key", func(t *testing.T) {
		result, err := log.List(ctx, core.AuditQuery{SessionKey: "s1"})
		if err != nil {
			t.Fatal(err)
		}
		if len(result) != 1 {
			t.Errorf("expected 1 event, got %d", len(result))
		}
	})

	t.Run("filter_by_run_id", func(t *testing.T) {
		result, err := log.List(ctx, core.AuditQuery{RunID: "r2"})
		if err != nil {
			t.Fatal(err)
		}
		if len(result) != 1 {
			t.Errorf("expected 1 event, got %d", len(result))
		}
	})

	t.Run("filter_by_task_id", func(t *testing.T) {
		result, err := log.List(ctx, core.AuditQuery{TaskID: "t1"})
		if err != nil {
			t.Fatal(err)
		}
		if len(result) != 1 {
			t.Errorf("expected 1 event, got %d", len(result))
		}
	})

	t.Run("filter_by_text", func(t *testing.T) {
		result, err := log.List(ctx, core.AuditQuery{Text: "shut"})
		if err != nil {
			t.Fatal(err)
		}
		if len(result) != 1 {
			t.Errorf("expected 1 event, got %d", len(result))
		}
	})

	t.Run("limit", func(t *testing.T) {
		result, err := log.List(ctx, core.AuditQuery{Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(result) != 2 {
			t.Errorf("expected 2 events with limit, got %d", len(result))
		}
	})
}

func TestAuditLog_NilSafe(t *testing.T) {
	var log *AuditLog
	if err := log.Record(context.Background(), core.AuditEvent{}); err != nil {
		t.Error("nil audit log Record should not error")
	}
	events, err := log.List(context.Background(), core.AuditQuery{})
	if err != nil {
		t.Error("nil audit log List should not error")
	}
	if events != nil {
		t.Error("nil audit log should return nil events")
	}
}

func TestAuditLog_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	log, err := NewAuditLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	events, err := log.List(context.Background(), core.AuditQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if events != nil {
		t.Error("empty log should return nil")
	}
}

func TestAuditLog_TextSearchData(t *testing.T) {
	dir := t.TempDir()
	log, err := NewAuditLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := log.Record(ctx, core.AuditEvent{
		Category: "test",
		Type:     "data-test",
		Message:  "basic",
		Data:     map[string]any{"secret": "hidden-value"},
	}); err != nil {
		t.Fatal(err)
	}

	result, err := log.List(ctx, core.AuditQuery{Text: "hidden-value"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Errorf("text search should match data values, got %d", len(result))
	}
}

// ---------------------------------------------------------------------------
// Rotation tests
// ---------------------------------------------------------------------------

func TestAuditLog_FileNaming(t *testing.T) {
	dir := t.TempDir()
	log, err := NewAuditLog(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Record an event
	if err := log.Record(context.Background(), core.AuditEvent{
		Category: "test",
		Type:     "naming",
		Level:    "info",
		Message:  "test file naming",
	}); err != nil {
		t.Fatal(err)
	}

	// Verify file is created with date-based name
	today := time.Now().UTC().Format("2006-01-02")
	expectedName := "events-" + today + ".jsonl"
	expectedPath := filepath.Join(dir, "audit", expectedName)

	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		// Check if any file exists in the audit directory
		entries, _ := os.ReadDir(filepath.Join(dir, "audit"))
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected file %s, found files: %v", expectedPath, names)
	}
}

func TestAuditLog_RotationBySize(t *testing.T) {
	dir := t.TempDir()
	// Create audit log with very small max size (1 KB)
	log, err := NewAuditLogWithSize(dir, 1) // 1 MB = 1024 KB, but we want smaller
	if err != nil {
		t.Fatal(err)
	}

	// Override maxFileSize for testing (set to 100 bytes)
	log.mu.Lock()
	log.maxFileSize = 100
	log.mu.Unlock()

	ctx := context.Background()

	// Write multiple events that will trigger rotation
	for i := 0; i < 10; i++ {
		if err := log.Record(ctx, core.AuditEvent{
			Category: "test",
			Type:     "rotation",
			Level:    "info",
			Message:  "this is a test message that is somewhat long to trigger rotation",
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Verify multiple files were created
	entries, err := os.ReadDir(filepath.Join(dir, "audit"))
	if err != nil {
		t.Fatal(err)
	}

	// Count files for today
	today := time.Now().UTC().Format("2006-01-02")
	var todayFiles int
	for _, e := range entries {
		if filepath.Base(e.Name())[:len("events-"+today)] == "events-"+today {
			todayFiles++
		}
	}

	if todayFiles < 2 {
		t.Errorf("expected at least 2 files for today after rotation, got %d", todayFiles)
	}
}

func TestAuditLog_TimeRangeFilter(t *testing.T) {
	dir := t.TempDir()
	log, err := NewAuditLog(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	now := time.Now().UTC()

	// Record events with specific times
	events := []struct {
		time    time.Time
		message string
	}{
		{now.Add(-3 * time.Hour), "3 hours ago"},
		{now.Add(-2 * time.Hour), "2 hours ago"},
		{now.Add(-1 * time.Hour), "1 hour ago"},
		{now, "now"},
	}

	for _, e := range events {
		if err := log.Record(ctx, core.AuditEvent{
			Category:   "test",
			Type:       "timefilter",
			Level:      "info",
			Message:    e.message,
			OccurredAt: e.time,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Query for events in the last 90 minutes
	start := now.Add(-90 * time.Minute)
	result, err := log.List(ctx, core.AuditQuery{
		StartTime: start,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Should get "1 hour ago" and "now"
	if len(result) != 2 {
		t.Errorf("expected 2 events with time filter, got %d", len(result))
	}
}

func TestAuditLog_CrossDateQuery(t *testing.T) {
	dir := t.TempDir()
	log, err := NewAuditLog(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	today := time.Now().UTC()

	// Create a file for yesterday manually
	yesterday := today.AddDate(0, 0, -1)
	yesterdayFile := filepath.Join(dir, "audit", "events-"+yesterday.Format("2006-01-02")+".jsonl")
	if err := os.MkdirAll(filepath.Dir(yesterdayFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(yesterdayFile, []byte(`{"category":"test","type":"yesterday","level":"info","message":"yesterday event","occurredAt":"`+yesterday.Format(time.RFC3339)+`"}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Record an event for today
	if err := log.Record(ctx, core.AuditEvent{
		Category: "test",
		Type:     "today",
		Level:    "info",
		Message:  "today event",
	}); err != nil {
		t.Fatal(err)
	}

	// Query all events
	result, err := log.List(ctx, core.AuditQuery{})
	if err != nil {
		t.Fatal(err)
	}

	// Should get both yesterday and today events
	if len(result) < 2 {
		t.Errorf("expected at least 2 events across dates, got %d", len(result))
	}
}
