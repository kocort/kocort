package task

import (
	"context"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
)

func TestSubagentRegistryBasic(t *testing.T) {
	r := NewSubagentRegistry()
	if r == nil {
		t.Fatal("expected non-nil registry")
	}
	if r.Count() != 0 {
		t.Errorf("expected 0 count, got %d", r.Count())
	}
}

func TestSubagentRegistryRegisterAndGet(t *testing.T) {
	r := NewSubagentRegistry()
	r.Register(SubagentRunRecord{
		RunID:               "run_1",
		ChildSessionKey:     "child_key",
		RequesterSessionKey: "parent_key",
		Task:                "do something",
		CreatedAt:           time.Now().UTC(),
		StartedAt:           time.Now().UTC(),
	})
	if r.Count() != 1 {
		t.Errorf("expected 1, got %d", r.Count())
	}
	rec := r.Get("run_1")
	if rec == nil {
		t.Fatal("expected non-nil record")
	}
	if rec.Task != "do something" {
		t.Errorf("got task=%q", rec.Task)
	}
}

func TestSubagentRegistryGetMissing(t *testing.T) {
	r := NewSubagentRegistry()
	if rec := r.Get("nonexistent"); rec != nil {
		t.Error("expected nil for missing run")
	}
}

func TestSubagentRegistryComplete(t *testing.T) {
	r := NewSubagentRegistry()
	r.Register(SubagentRunRecord{
		RunID:               "run_1",
		ChildSessionKey:     "child_key",
		RequesterSessionKey: "parent_key",
		Task:                "build",
		CreatedAt:           time.Now().UTC(),
		StartedAt:           time.Now().UTC(),
	})

	result := core.AgentRunResult{
		Payloads: []core.ReplyPayload{{Text: "build succeeded"}},
	}
	rec, changed := r.Complete("run_1", result, nil)
	if !changed {
		t.Error("expected changed=true")
	}
	if rec.Outcome == nil || rec.Outcome.Status != SubagentOutcomeOK {
		t.Errorf("expected outcome=ok, got %+v", rec.Outcome)
	}
	if rec.FrozenResultText != "build succeeded" {
		t.Errorf("got frozenResult=%q", rec.FrozenResultText)
	}
}

func TestSubagentRegistryCompleteWithError(t *testing.T) {
	r := NewSubagentRegistry()
	r.Register(SubagentRunRecord{
		RunID:     "run_2",
		CreatedAt: time.Now().UTC(),
		StartedAt: time.Now().UTC(),
	})

	_, changed := r.Complete("run_2", core.AgentRunResult{}, context.DeadlineExceeded)
	if !changed {
		t.Error("expected changed=true")
	}
	rec := r.Get("run_2")
	if rec.Outcome == nil || rec.Outcome.Status != SubagentOutcomeError {
		t.Errorf("expected outcome=error, got %+v", rec.Outcome)
	}
}

func TestSubagentRegistryCompleteIdempotent(t *testing.T) {
	r := NewSubagentRegistry()
	r.Register(SubagentRunRecord{
		RunID:     "run_3",
		CreatedAt: time.Now().UTC(),
		StartedAt: time.Now().UTC(),
	})
	r.Complete("run_3", core.AgentRunResult{}, nil)
	_, changed := r.Complete("run_3", core.AgentRunResult{}, nil)
	if changed {
		t.Error("expected changed=false for already completed")
	}
}

func TestSubagentRegistryListByRequester(t *testing.T) {
	r := NewSubagentRegistry()
	r.Register(SubagentRunRecord{
		RunID:               "r1",
		RequesterSessionKey: "parent",
		CreatedAt:           time.Now().UTC(),
		StartedAt:           time.Now().UTC(),
	})
	r.Register(SubagentRunRecord{
		RunID:               "r2",
		RequesterSessionKey: "parent",
		CreatedAt:           time.Now().UTC(),
		StartedAt:           time.Now().UTC(),
	})
	r.Register(SubagentRunRecord{
		RunID:               "r3",
		RequesterSessionKey: "other",
		CreatedAt:           time.Now().UTC(),
		StartedAt:           time.Now().UTC(),
	})

	list := r.ListByRequester("parent")
	if len(list) != 2 {
		t.Errorf("expected 2, got %d", len(list))
	}
}

func TestSubagentRegistryMarkCompletionMessageSent(t *testing.T) {
	r := NewSubagentRegistry()
	r.Register(SubagentRunRecord{
		RunID:     "r1",
		Cleanup:   "delete",
		SpawnMode: "run",
		CreatedAt: time.Now().UTC(),
		StartedAt: time.Now().UTC(),
	})
	r.Complete("r1", core.AgentRunResult{}, nil)
	r.MarkCompletionMessageSent("r1")
	// With cleanup=delete and spawnMode=run, the record should be removed.
	if rec := r.Get("r1"); rec != nil {
		t.Error("expected record to be cleaned up")
	}
}

func TestSubagentRegistryPendingAnnouncements(t *testing.T) {
	r := NewSubagentRegistry()
	now := time.Now().UTC()
	r.Register(SubagentRunRecord{
		RunID:               "r1",
		RequesterSessionKey: "parent",
		CreatedAt:           now,
		StartedAt:           now,
	})
	r.Complete("r1", core.AgentRunResult{}, nil)

	pending := r.PendingAnnouncementsForRequester("parent", now)
	if len(pending) != 1 {
		t.Errorf("expected 1 pending announcement, got %d", len(pending))
	}
}

// ---------------------------------------------------------------------------
// IsRequesterDescendant
// ---------------------------------------------------------------------------

func TestIsRequesterDescendant(t *testing.T) {
	tests := []struct {
		name      string
		requester string
		root      string
		want      bool
	}{
		{"same_key", "agent:main:main", "agent:main:main", true},
		{"child_subagent", "agent:main:main:subagent:child", "agent:main:main", true},
		{"unrelated", "agent:other:main", "agent:main:main", false},
		{"empty_requester", "", "agent:main:main", false},
		{"empty_root", "agent:main:main", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRequesterDescendant(tt.requester, tt.root); got != tt.want {
				t.Errorf("IsRequesterDescendant(%q, %q) = %v, want %v", tt.requester, tt.root, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// SpawnSubagent
// ---------------------------------------------------------------------------

func TestSpawnSubagentBasic(t *testing.T) {
	r := NewSubagentRegistry()
	result, err := SpawnSubagent(context.Background(), r, SubagentSpawnRequest{
		RequesterSessionKey: "parent:main:main",
		RequesterAgentID:    "main",
		TargetAgentID:       "worker",
		Task:                "build the project",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if result.Status != "accepted" {
		t.Errorf("expected status=accepted, got %q", result.Status)
	}
	if result.ChildSessionKey == "" {
		t.Error("expected non-empty child session key")
	}
	if result.RunID == "" {
		t.Error("expected non-empty runID")
	}
	if r.Count() != 1 {
		t.Errorf("expected 1 registered run, got %d", r.Count())
	}
	record := r.Get(result.RunID)
	if record == nil || record.RequesterDisplayKey != "parent:main:main" || !record.ExpectsCompletionMessage {
		t.Fatalf("expected richer registry metadata, got %+v", record)
	}
}

func TestSpawnSubagentMissingTask(t *testing.T) {
	r := NewSubagentRegistry()
	_, err := SpawnSubagent(context.Background(), r, SubagentSpawnRequest{
		RequesterSessionKey: "parent",
	})
	if err == nil {
		t.Error("expected error for missing task")
	}
}

func TestSpawnSubagentDepthLimit(t *testing.T) {
	r := NewSubagentRegistry()
	_, err := SpawnSubagent(context.Background(), r, SubagentSpawnRequest{
		RequesterSessionKey: "parent",
		Task:                "deep spawn",
		MaxSpawnDepth:       3,
		CurrentDepth:        3,
	})
	if err == nil {
		t.Error("expected error for depth limit")
	}
}

func TestSpawnSubagentMaxChildren(t *testing.T) {
	r := NewSubagentRegistry()
	// Spawn max children.
	for i := 0; i < 5; i++ {
		SpawnSubagent(context.Background(), r, SubagentSpawnRequest{
			RequesterSessionKey: "parent",
			Task:                "task",
			MaxChildren:         5,
		})
	}
	_, err := SpawnSubagent(context.Background(), r, SubagentSpawnRequest{
		RequesterSessionKey: "parent",
		Task:                "one more",
		MaxChildren:         5,
	})
	if err == nil {
		t.Error("expected error for max children exceeded")
	}
}

func TestRegisterSteerRestartReplacement(t *testing.T) {
	r := NewSubagentRegistry()
	now := time.Now().UTC()
	r.Register(SubagentRunRecord{
		RunID:                    "run-old",
		ChildSessionKey:          "agent:worker:subagent:child",
		RequesterSessionKey:      "agent:main:main",
		RequesterDisplayKey:      "main",
		Task:                     "do work",
		Label:                    "worker",
		Model:                    "gpt-4.1-mini",
		SpawnMode:                "session",
		Cleanup:                  "keep",
		RouteChannel:             "discord",
		RouteThreadID:            "thread-1",
		WorkspaceDir:             "/tmp/worker",
		ExpectsCompletionMessage: true,
		CreatedAt:                now.Add(-time.Minute),
		StartedAt:                now.Add(-time.Minute),
	})

	replacement, ok, err := r.RegisterSteerRestartReplacement("run-old")
	if err != nil {
		t.Fatalf("register steer restart replacement: %v", err)
	}
	if !ok || replacement == nil {
		t.Fatal("expected replacement run")
	}
	if replacement.RunID == "run-old" || replacement.ChildSessionKey != "agent:worker:subagent:child" {
		t.Fatalf("unexpected replacement: %+v", replacement)
	}
	if replacement.SteeredFromRunID != "run-old" {
		t.Fatalf("expected replacement to track original run, got %+v", replacement)
	}
	old := r.Get("run-old")
	if old == nil || old.SuppressAnnounceReason != "steer-restart" || old.AnnounceDeliveryPath != "steered" || old.ReplacementRunID != replacement.RunID {
		t.Fatalf("expected old run to be suppressed, got %+v", old)
	}
}

func TestRestoreAfterFailedSteerRestart(t *testing.T) {
	r := NewSubagentRegistry()
	now := time.Now().UTC()
	r.Register(SubagentRunRecord{
		RunID:                    "run-old",
		ChildSessionKey:          "agent:worker:subagent:child",
		RequesterSessionKey:      "agent:main:main",
		ExpectsCompletionMessage: true,
		CreatedAt:                now,
		StartedAt:                now,
	})
	replacement, ok, err := r.RegisterSteerRestartReplacement("run-old")
	if err != nil || !ok || replacement == nil {
		t.Fatalf("register replacement: %v %+v", err, replacement)
	}
	if !r.RestoreAfterFailedSteerRestart("run-old", replacement.RunID) {
		t.Fatal("expected restore to report change")
	}
	old := r.Get("run-old")
	if old == nil || old.SuppressAnnounceReason != "" || old.AnnounceDeliveryPath != "" || old.ReplacementRunID != "" {
		t.Fatalf("expected old suppression to be cleared, got %+v", old)
	}
	if rec := r.Get(replacement.RunID); rec != nil {
		t.Fatalf("expected replacement run to be removed, got %+v", rec)
	}
}

func TestSpawnSubagentCanceled(t *testing.T) {
	r := NewSubagentRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := SpawnSubagent(ctx, r, SubagentSpawnRequest{
		RequesterSessionKey: "parent",
		Task:                "task",
	})
	if err == nil {
		t.Error("expected error for canceled context")
	}
}

// ---------------------------------------------------------------------------
// BuildChildCompletionAnnouncement
// ---------------------------------------------------------------------------

func TestBuildChildCompletionAnnouncement(t *testing.T) {
	entry := &SubagentRunRecord{
		Label:            "Deploy Worker",
		FrozenResultText: "deployed successfully",
		Outcome:          &SubagentRunOutcome{Status: SubagentOutcomeOK},
	}
	msg := BuildChildCompletionAnnouncement(entry)
	if msg == "" {
		t.Error("expected non-empty announcement")
	}
}
