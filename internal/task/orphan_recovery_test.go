package task

import (
	"strings"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"
)

// ---------------------------------------------------------------------------
// BuildOrphanRecoveryPlan tests
// ---------------------------------------------------------------------------

func TestBuildOrphanRecoveryPlanClassifiesRecoverableRuns(t *testing.T) {
	store, _ := session.NewSessionStore(t.TempDir())
	childKey := "agent:worker:subagent:abc123"
	_ = store.Upsert(childKey, core.SessionEntry{
		SessionID: "sess_abc",
		SpawnedBy: "agent:main:main",
	})

	registry := NewSubagentRegistry()
	now := time.Now().UTC()
	registry.Register(SubagentRunRecord{
		RunID:               "run-active",
		ChildSessionKey:     childKey,
		RequesterSessionKey: "agent:main:main",
		Task:                "do something",
		CreatedAt:           now.Add(-2 * time.Minute),
		StartedAt:           now.Add(-2 * time.Minute),
		SpawnMode:           "run",
	})

	active := NewActiveRunRegistry()
	plan := BuildOrphanRecoveryPlan(registry, store, active, now)

	if len(plan.Recoverable) != 1 {
		t.Fatalf("expected 1 recoverable, got %d", len(plan.Recoverable))
	}
	if plan.Recoverable[0].Record.RunID != "run-active" {
		t.Fatalf("expected run-active, got %s", plan.Recoverable[0].Record.RunID)
	}
	if len(plan.TooOld) != 0 {
		t.Fatalf("expected 0 too-old, got %d", len(plan.TooOld))
	}
	if len(plan.NoSession) != 0 {
		t.Fatalf("expected 0 no-session, got %d", len(plan.NoSession))
	}
}

func TestBuildOrphanRecoveryPlanMarksTooOldRuns(t *testing.T) {
	store, _ := session.NewSessionStore(t.TempDir())
	childKey := "agent:worker:subagent:old1"
	_ = store.Upsert(childKey, core.SessionEntry{SessionID: "sess_old"})

	registry := NewSubagentRegistry()
	now := time.Now().UTC()
	registry.Register(SubagentRunRecord{
		RunID:               "run-old",
		ChildSessionKey:     childKey,
		RequesterSessionKey: "agent:main:main",
		Task:                "stale task",
		CreatedAt:           now.Add(-OrphanRecoveryMaxAge - time.Minute),
		StartedAt:           now.Add(-OrphanRecoveryMaxAge - time.Minute),
	})

	active := NewActiveRunRegistry()
	plan := BuildOrphanRecoveryPlan(registry, store, active, now)

	if len(plan.Recoverable) != 0 {
		t.Fatalf("expected 0 recoverable, got %d", len(plan.Recoverable))
	}
	if len(plan.TooOld) != 1 {
		t.Fatalf("expected 1 too-old, got %d", len(plan.TooOld))
	}
}

func TestBuildOrphanRecoveryPlanMarksNoSessionRuns(t *testing.T) {
	// No session created for this child key.
	store, _ := session.NewSessionStore(t.TempDir())

	registry := NewSubagentRegistry()
	now := time.Now().UTC()
	registry.Register(SubagentRunRecord{
		RunID:               "run-nosess",
		ChildSessionKey:     "agent:worker:subagent:gone",
		RequesterSessionKey: "agent:main:main",
		Task:                "lost task",
		CreatedAt:           now.Add(-time.Minute),
		StartedAt:           now.Add(-time.Minute),
	})

	active := NewActiveRunRegistry()
	plan := BuildOrphanRecoveryPlan(registry, store, active, now)

	if len(plan.Recoverable) != 0 {
		t.Fatalf("expected 0 recoverable, got %d", len(plan.Recoverable))
	}
	if len(plan.NoSession) != 1 {
		t.Fatalf("expected 1 no-session, got %d", len(plan.NoSession))
	}
}

func TestBuildOrphanRecoveryPlanSkipsActiveRuns(t *testing.T) {
	store, _ := session.NewSessionStore(t.TempDir())
	childKey := "agent:worker:subagent:live"
	_ = store.Upsert(childKey, core.SessionEntry{SessionID: "sess_live"})

	registry := NewSubagentRegistry()
	now := time.Now().UTC()
	registry.Register(SubagentRunRecord{
		RunID:               "run-live",
		ChildSessionKey:     childKey,
		RequesterSessionKey: "agent:main:main",
		Task:                "running task",
		CreatedAt:           now.Add(-time.Minute),
		StartedAt:           now.Add(-time.Minute),
	})

	active := NewActiveRunRegistry()
	active.StartRun(childKey, "run-live", nil)

	plan := BuildOrphanRecoveryPlan(registry, store, active, now)

	if len(plan.Recoverable) != 0 {
		t.Fatalf("expected 0 recoverable (run is still active), got %d", len(plan.Recoverable))
	}
}

func TestBuildOrphanRecoveryPlanSkipsEndedRuns(t *testing.T) {
	store, _ := session.NewSessionStore(t.TempDir())
	childKey := "agent:worker:subagent:done1"
	_ = store.Upsert(childKey, core.SessionEntry{SessionID: "sess_done"})

	registry := NewSubagentRegistry()
	now := time.Now().UTC()
	registry.Register(SubagentRunRecord{
		RunID:               "run-done",
		ChildSessionKey:     childKey,
		RequesterSessionKey: "agent:main:main",
		Task:                "done task",
		CreatedAt:           now.Add(-5 * time.Minute),
		StartedAt:           now.Add(-5 * time.Minute),
		EndedAt:             now.Add(-3 * time.Minute),
		Outcome:             &SubagentRunOutcome{Status: SubagentOutcomeOK},
	})

	active := NewActiveRunRegistry()
	plan := BuildOrphanRecoveryPlan(registry, store, active, now)

	if len(plan.Recoverable) != 0 {
		t.Fatalf("expected 0 recoverable (run already ended), got %d", len(plan.Recoverable))
	}
}

// ---------------------------------------------------------------------------
// BuildOrphanResumeRequest tests
// ---------------------------------------------------------------------------

func TestBuildOrphanResumeRequestIncludesTaskAndRoute(t *testing.T) {
	candidate := OrphanRecoveryCandidate{
		Record: SubagentRunRecord{
			RunID:               "run-r1",
			ChildSessionKey:     "agent:worker:subagent:x",
			RequesterSessionKey: "agent:main:main",
			Task:                "Analyse the logs",
			RouteChannel:        "telegram",
			RouteThreadID:       "thread-42",
			SpawnDepth:          2,
			WorkspaceDir:        "/workspace",
			RunTimeoutSeconds:   60,
			Model:               "anthropic/claude-sonnet-4-20250514",
		},
		SessionEntry: &core.SessionEntry{SessionID: "sess_x"},
		LastUserMsg:  "Check the error",
	}

	req := BuildOrphanResumeRequest(candidate)

	if req.Lane != core.LaneSubagent {
		t.Fatalf("expected LaneSubagent, got %s", req.Lane)
	}
	if req.SessionKey != "agent:worker:subagent:x" {
		t.Fatalf("unexpected session key: %s", req.SessionKey)
	}
	if req.SpawnedBy != "agent:main:main" {
		t.Fatalf("unexpected SpawnedBy: %s", req.SpawnedBy)
	}
	if req.Channel != "telegram" {
		t.Fatalf("unexpected channel: %s", req.Channel)
	}
	if req.Timeout != 60*time.Second {
		t.Fatalf("unexpected timeout: %v", req.Timeout)
	}
	if !strings.Contains(req.Message, "interrupted by a process restart") {
		t.Fatalf("resume message missing interruption notice: %s", req.Message)
	}
	if !strings.Contains(req.Message, "Analyse the logs") {
		t.Fatalf("resume message missing original task: %s", req.Message)
	}
	if !strings.Contains(req.Message, "Check the error") {
		t.Fatalf("resume message missing last user message: %s", req.Message)
	}
	if req.SessionProviderOverride != "anthropic" {
		t.Fatalf("expected provider anthropic, got %s", req.SessionProviderOverride)
	}
	if req.SessionModelOverride != "claude-sonnet-4-20250514" {
		t.Fatalf("expected model claude-sonnet-4-20250514, got %s", req.SessionModelOverride)
	}
}

func TestBuildOrphanResumeRequestDeduplicatesTaskAndLastMsg(t *testing.T) {
	candidate := OrphanRecoveryCandidate{
		Record: SubagentRunRecord{
			RunID:               "run-dedup",
			ChildSessionKey:     "agent:worker:subagent:y",
			RequesterSessionKey: "agent:main:main",
			Task:                "same message",
		},
		SessionEntry: &core.SessionEntry{SessionID: "sess_y"},
		LastUserMsg:  "same message",
	}

	req := BuildOrphanResumeRequest(candidate)

	// Should NOT repeat the same text twice.
	count := strings.Count(req.Message, "same message")
	if count != 1 {
		t.Fatalf("expected task text to appear once (deduplicated with lastUserMsg), got %d occurrences", count)
	}
}

// ---------------------------------------------------------------------------
// ExecuteOrphanRecovery tests
// ---------------------------------------------------------------------------

func TestExecuteOrphanRecoveryRecoversCandidates(t *testing.T) {
	store, _ := session.NewSessionStore(t.TempDir())
	childKey := "agent:worker:subagent:rec1"
	_ = store.Upsert(childKey, core.SessionEntry{SessionID: "sess_rec1", SpawnedBy: "agent:main:main"})

	registry := NewSubagentRegistry()
	now := time.Now().UTC()
	registry.Register(SubagentRunRecord{
		RunID:                    "run-rec1",
		ChildSessionKey:          childKey,
		RequesterSessionKey:      "agent:main:main",
		Task:                     "do it",
		CreatedAt:                now.Add(-time.Minute),
		StartedAt:                now.Add(-time.Minute),
		SpawnMode:                "run",
		ExpectsCompletionMessage: true,
	})

	active := NewActiveRunRegistry()
	plan := BuildOrphanRecoveryPlan(registry, store, active, now)

	recoveredCh := make(chan string, 1)
	result := ExecuteOrphanRecovery(registry, plan, func(req core.AgentRunRequest) (core.AgentRunResult, error) {
		recoveredCh <- req.RunID
		return core.AgentRunResult{RunID: req.RunID}, nil
	})

	if result.Recovered != 1 {
		t.Fatalf("expected 1 recovered, got %d", result.Recovered)
	}

	// Wait for the goroutine to complete.
	select {
	case runID := <-recoveredCh:
		// The run should have been called with a replacement RunID (not the original).
		if runID == "run-rec1" {
			t.Fatalf("expected a replacement RunID, got original: %s", runID)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for recovery goroutine")
	}
}

func TestExecuteOrphanRecoveryMarksTooOldAsFailed(t *testing.T) {
	registry := NewSubagentRegistry()
	now := time.Now().UTC()
	registry.Register(SubagentRunRecord{
		RunID:               "run-toolong",
		ChildSessionKey:     "agent:worker:subagent:tl",
		RequesterSessionKey: "agent:main:main",
		Task:                "ancient task",
		CreatedAt:           now.Add(-OrphanRecoveryMaxAge - time.Hour),
	})

	plan := OrphanRecoveryPlan{
		TooOld: []SubagentRunRecord{*registry.Get("run-toolong")},
	}
	result := ExecuteOrphanRecovery(registry, plan, nil)

	if result.Skipped != 1 {
		t.Fatalf("expected 1 skipped, got %d", result.Skipped)
	}
	record := registry.Get("run-toolong")
	if record == nil || record.EndedAt.IsZero() {
		t.Fatalf("expected too-old run to be terminated")
	}
	if record.EndedReason != "orphan-too-old" {
		t.Fatalf("expected endedReason=orphan-too-old, got %s", record.EndedReason)
	}
}

func TestExecuteOrphanRecoveryWithNilRunFnMarksFailed(t *testing.T) {
	store, _ := session.NewSessionStore(t.TempDir())
	childKey := "agent:worker:subagent:norunner"
	_ = store.Upsert(childKey, core.SessionEntry{SessionID: "sess_nr", SpawnedBy: "agent:main:main"})

	registry := NewSubagentRegistry()
	now := time.Now().UTC()
	registry.Register(SubagentRunRecord{
		RunID:               "run-norunner",
		ChildSessionKey:     childKey,
		RequesterSessionKey: "agent:main:main",
		Task:                "lonely task",
		CreatedAt:           now.Add(-time.Minute),
		StartedAt:           now.Add(-time.Minute),
		SpawnMode:           "run",
	})

	active := NewActiveRunRegistry()
	plan := BuildOrphanRecoveryPlan(registry, store, active, now)
	result := ExecuteOrphanRecovery(registry, plan, nil)

	if result.Failed != 1 {
		t.Fatalf("expected 1 failed, got %d", result.Failed)
	}
}

// ---------------------------------------------------------------------------
// SweepOrphans recovery window integration test
// ---------------------------------------------------------------------------

func TestSweepOrphansPreservesRecentRunsForRecovery(t *testing.T) {
	store, _ := session.NewSessionStore(t.TempDir())
	recentKey := "agent:worker:subagent:recent"
	_ = store.Upsert(recentKey, core.SessionEntry{SessionID: "sess_recent"})

	oldKey := "agent:worker:subagent:old"
	_ = store.Upsert(oldKey, core.SessionEntry{SessionID: "sess_old"})

	registry := NewSubagentRegistry()
	now := time.Now().UTC()

	// Recent run — should be preserved for recovery.
	registry.Register(SubagentRunRecord{
		RunID:           "run-recent",
		ChildSessionKey: recentKey,
		CreatedAt:       now.Add(-5 * time.Minute),
		StartedAt:       now.Add(-5 * time.Minute),
	})
	// Old run — should be cleaned up immediately.
	registry.Register(SubagentRunRecord{
		RunID:           "run-old",
		ChildSessionKey: oldKey,
		CreatedAt:       now.Add(-OrphanRecoveryMaxAge - time.Hour),
		StartedAt:       now.Add(-OrphanRecoveryMaxAge - time.Hour),
	})

	active := NewActiveRunRegistry()
	registry.SweepOrphans(store, active)

	recent := registry.Get("run-recent")
	if recent == nil {
		t.Fatalf("expected recent orphan to be preserved for recovery")
	}
	if !recent.EndedAt.IsZero() {
		t.Fatalf("expected recent orphan EndedAt to remain zero, got %v", recent.EndedAt)
	}

	old := registry.Get("run-old")
	if old == nil {
		t.Fatalf("expected old orphan to still be in registry (marked as ended)")
	}
	if old.EndedAt.IsZero() {
		t.Fatalf("expected old orphan to be marked as ended")
	}
}

// ---------------------------------------------------------------------------
// Helper tests
// ---------------------------------------------------------------------------

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Fatalf("expected hello, got %s", got)
	}
	if got := truncate("hello world", 8); got != "hello..." {
		t.Fatalf("expected hello..., got %s", got)
	}
	if got := truncate("abc", 3); got != "abc" {
		t.Fatalf("expected abc, got %s", got)
	}
}

func TestBuildResumeMessage(t *testing.T) {
	msg := buildResumeMessage("analyze data", "check the logs")
	if !strings.Contains(msg, "interrupted by a process restart") {
		t.Fatalf("missing interruption notice")
	}
	if !strings.Contains(msg, "analyze data") {
		t.Fatalf("missing task snippet")
	}
	if !strings.Contains(msg, "check the logs") {
		t.Fatalf("missing last user message")
	}
}

func TestBuildResumeMessageDeduplicates(t *testing.T) {
	msg := buildResumeMessage("same text", "same text")
	count := strings.Count(msg, "same text")
	if count != 1 {
		t.Fatalf("expected deduplication, got %d occurrences", count)
	}
}
