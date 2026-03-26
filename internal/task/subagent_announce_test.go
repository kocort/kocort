package task

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
)

func TestShouldDeferSubagentCompletionAnnouncement(t *testing.T) {
	registry := NewSubagentRegistry()
	now := time.Now().UTC()
	registry.Register(SubagentRunRecord{
		RunID:                    "run-done",
		ChildSessionKey:          "agent:main:subagent:done",
		RequesterSessionKey:      "agent:main:subagent:parent",
		ExpectsCompletionMessage: true,
		CreatedAt:                now,
		StartedAt:                now,
	})
	registry.Register(SubagentRunRecord{
		RunID:               "run-child",
		ChildSessionKey:     "agent:main:subagent:child",
		RequesterSessionKey: "agent:main:subagent:parent:subagent:leaf",
		CreatedAt:           now,
		StartedAt:           now,
	})
	registry.Complete("run-done", core.AgentRunResult{}, nil)

	entry := registry.Get("run-done")
	if !ShouldDeferSubagentCompletionAnnouncement(registry, entry) {
		t.Fatal("expected completion announce to defer while descendant run is active")
	}
}

func TestPreparePendingSubagentAnnouncements(t *testing.T) {
	registry := NewSubagentRegistry()
	now := time.Now().UTC()
	registry.Register(SubagentRunRecord{
		RunID:                    "run-1",
		ChildSessionKey:          "agent:main:subagent:one",
		RequesterSessionKey:      "agent:main:main",
		Label:                    "worker-1",
		ExpectsCompletionMessage: true,
		CreatedAt:                now,
		StartedAt:                now,
	})
	registry.Complete("run-1", core.AgentRunResult{}, nil)

	items := PreparePendingSubagentAnnouncements(registry, "agent:main:main", now)
	if len(items) != 1 {
		t.Fatalf("expected 1 pending announcement, got %d", len(items))
	}
	if items[0].RunID != "run-1" || items[0].PrimaryRequest.SessionKey != "agent:main:main" {
		t.Fatalf("unexpected announcement: %+v", items[0])
	}
	if items[0].PrimaryRequest.AgentID != "main" || !items[0].PrimaryRequest.ShouldFollowup || items[0].PrimaryRequest.QueueMode != "collect" {
		t.Fatalf("unexpected request fields: %+v", items[0].PrimaryRequest)
	}
	if items[0].PrimaryPath != "queued" || items[0].FallbackPath != "none" {
		t.Fatalf("unexpected delivery paths: %+v", items[0])
	}
	if !strings.Contains(items[0].PrimaryRequest.Message, "worker-1") {
		t.Fatalf("expected announcement message to include label, got %q", items[0].PrimaryRequest.Message)
	}
}

func TestPreparePendingSubagentAnnouncementsPrefersDirectForCompletionMessages(t *testing.T) {
	registry := NewSubagentRegistry()
	now := time.Now().UTC()
	registry.Register(SubagentRunRecord{
		RunID:                    "run-1",
		ChildSessionKey:          "agent:main:subagent:one",
		RequesterSessionKey:      "agent:main:main",
		ExpectsCompletionMessage: true,
		RequesterOrigin: &core.DeliveryContext{
			Channel:   "discord",
			To:        "chan-1",
			AccountID: "acct-1",
			ThreadID:  "thread-1",
		},
		CreatedAt: now,
		StartedAt: now,
	})
	registry.Complete("run-1", core.AgentRunResult{}, nil)

	items := PreparePendingSubagentAnnouncements(registry, "agent:main:main", now)
	if len(items) != 1 {
		t.Fatalf("expected 1 pending announcement, got %d", len(items))
	}
	if items[0].PrimaryPath != "direct" || items[0].FallbackPath != "queued" {
		t.Fatalf("unexpected delivery paths: %+v", items[0])
	}
	if !items[0].PrimaryRequest.Deliver || items[0].PrimaryRequest.Channel != "discord" || items[0].PrimaryRequest.ThreadID != "thread-1" {
		t.Fatalf("expected direct primary request, got %+v", items[0].PrimaryRequest)
	}
	if items[0].FallbackRequest == nil || !items[0].FallbackRequest.ShouldFollowup {
		t.Fatalf("expected queued fallback request, got %+v", items[0].FallbackRequest)
	}
}

func TestPreparePendingSubagentAnnouncementsKeepsSubagentRequesterInternal(t *testing.T) {
	registry := NewSubagentRegistry()
	now := time.Now().UTC()
	registry.Register(SubagentRunRecord{
		RunID:                    "run-1",
		ChildSessionKey:          "agent:main:subagent:one",
		RequesterSessionKey:      "agent:main:subagent:parent",
		ExpectsCompletionMessage: true,
		RequesterOrigin: &core.DeliveryContext{
			Channel:   "discord",
			To:        "chan-1",
			AccountID: "acct-1",
			ThreadID:  "thread-1",
		},
		CreatedAt: now,
		StartedAt: now,
	})
	registry.Complete("run-1", core.AgentRunResult{}, nil)

	items := PreparePendingSubagentAnnouncements(registry, "agent:main:subagent:parent", now)
	if len(items) != 1 {
		t.Fatalf("expected 1 pending announcement, got %d", len(items))
	}
	if items[0].PrimaryPath != "queued" || items[0].FallbackPath != "none" {
		t.Fatalf("expected subagent requester to stay internal, got %+v", items[0])
	}
	if items[0].PrimaryRequest.Deliver {
		t.Fatalf("expected internal follow-up request, got %+v", items[0].PrimaryRequest)
	}
}

func TestPreparePendingSubagentAnnouncementsSkipsDisabledCompletionMessages(t *testing.T) {
	registry := NewSubagentRegistry()
	now := time.Now().UTC()
	registry.Register(SubagentRunRecord{
		RunID:                    "run-1",
		ChildSessionKey:          "agent:main:subagent:one",
		RequesterSessionKey:      "agent:main:main",
		ExpectsCompletionMessage: false,
		CreatedAt:                now,
		StartedAt:                now,
	})
	registry.Complete("run-1", core.AgentRunResult{}, nil)

	items := PreparePendingSubagentAnnouncements(registry, "agent:main:main", now)
	if len(items) != 0 {
		t.Fatalf("expected disabled completion messages to skip announcements, got %d", len(items))
	}
	record := registry.Get("run-1")
	if record == nil || !record.CleanupHandled || record.CompletionMessageSentAt.IsZero() {
		t.Fatalf("expected disabled completion message to mark cleanup handled, got %+v", record)
	}
}

func TestPreparePendingSubagentAnnouncementsSkipsSuppressedRuns(t *testing.T) {
	registry := NewSubagentRegistry()
	now := time.Now().UTC()
	registry.Register(SubagentRunRecord{
		RunID:                    "run-1",
		ChildSessionKey:          "agent:main:subagent:one",
		RequesterSessionKey:      "agent:main:main",
		ExpectsCompletionMessage: true,
		CreatedAt:                now,
		StartedAt:                now,
	})
	registry.Complete("run-1", core.AgentRunResult{}, nil)
	registry.SuppressCompletionAnnouncement("run-1", "steer-restart")

	items := PreparePendingSubagentAnnouncements(registry, "agent:main:main", now)
	if len(items) != 0 {
		t.Fatalf("expected suppressed run to skip announcements, got %d", len(items))
	}
	record := registry.Get("run-1")
	if record == nil || !record.CleanupHandled || record.CompletionMessageSentAt.IsZero() || record.SuppressAnnounceReason != "steer-restart" {
		t.Fatalf("expected suppressed run to be terminally handled, got %+v", record)
	}
}

func TestMarkCompletionAnnounceAttemptSchedulesRetryBackoff(t *testing.T) {
	registry := NewSubagentRegistry()
	now := time.Now().UTC()
	registry.Register(SubagentRunRecord{
		RunID:               "run-1",
		ChildSessionKey:     "agent:main:subagent:one",
		RequesterSessionKey: "agent:main:main",
		CreatedAt:           now,
		StartedAt:           now,
	})
	registry.Complete("run-1", core.AgentRunResult{}, nil)
	registry.MarkCompletionAnnounceAttempt("run-1", errors.New("transient"))
	record := registry.Get("run-1")
	if record == nil || record.AnnounceRetryCount != 1 || record.LastAnnounceRetryAt.IsZero() || record.CleanupHandled {
		t.Fatalf("expected retry metadata and cleanup reset, got %+v", record)
	}

	if pending := registry.PendingAnnouncementsForRequester("agent:main:main", now); len(pending) != 0 {
		t.Fatalf("expected retry delay to suppress immediate re-announce, got %d pending", len(pending))
	}
	if pending := registry.PendingAnnouncementsForRequester("agent:main:main", now.Add(2*time.Second)); len(pending) != 1 {
		t.Fatalf("expected pending announce after retry delay, got %d", len(pending))
	}
}

func TestPreparePendingSubagentAnnouncementsGivesUpAfterRetryLimit(t *testing.T) {
	registry := NewSubagentRegistry()
	now := time.Now().UTC()
	registry.Register(SubagentRunRecord{
		RunID:                    "run-1",
		ChildSessionKey:          "agent:main:subagent:one",
		RequesterSessionKey:      "agent:main:main",
		SpawnMode:                "session",
		Cleanup:                  "keep",
		ExpectsCompletionMessage: true,
		CreatedAt:                now,
		StartedAt:                now,
	})
	registry.Complete("run-1", core.AgentRunResult{}, nil)
	registry.MarkCompletionAnnounceAttempt("run-1", errors.New("retry-1"))
	registry.MarkCompletionAnnounceAttempt("run-1", errors.New("retry-2"))
	registry.MarkCompletionAnnounceAttempt("run-1", errors.New("retry-3"))

	items := PreparePendingSubagentAnnouncements(registry, "agent:main:main", now.Add(10*time.Second))
	if len(items) != 0 {
		t.Fatalf("expected give-up to produce no ready announcements, got %d", len(items))
	}
	record := registry.Get("run-1")
	if record == nil || record.CompletionMessageSentAt.IsZero() {
		t.Fatalf("expected give-up to mark completion as handled, got %+v", record)
	}
}

func TestShouldRetrySubagentAnnouncementError(t *testing.T) {
	if !ShouldRetrySubagentAnnouncementError(errors.New("transient channel unavailable")) {
		t.Fatal("expected transient error to remain retryable")
	}
	if ShouldRetrySubagentAnnouncementError(errors.New("runtime is not fully configured")) {
		t.Fatal("expected permanent runtime configuration failure to stop retries")
	}
	if ShouldRetrySubagentAnnouncementError(ErrPermanentAnnouncementFailure) {
		t.Fatal("expected sentinel permanent error to stop retries")
	}
}

func TestNextAnnouncementRetryDelayForRequester(t *testing.T) {
	registry := NewSubagentRegistry()
	now := time.Now().UTC()
	registry.Register(SubagentRunRecord{
		RunID:                    "run-1",
		ChildSessionKey:          "agent:main:subagent:one",
		RequesterSessionKey:      "agent:main:main",
		ExpectsCompletionMessage: true,
		CreatedAt:                now,
		StartedAt:                now,
		EndedAt:                  now,
		NextAnnounceAttemptAt:    now.Add(2 * time.Second),
		CompletionDeferredAt:     now.Add(3 * time.Second),
	})

	delay, ok := registry.NextAnnouncementRetryDelayForRequester("agent:main:main", now)
	if !ok || delay < time.Second || delay > 3*time.Second {
		t.Fatalf("unexpected retry delay: %v %v", delay, ok)
	}
}

func TestBuildBatchedChildCompletionAnnouncementSingle(t *testing.T) {
	entries := []SubagentRunRecord{
		{
			Label:           "worker-1",
			ChildSessionKey: "agent:main:subagent:one",
			FrozenResultText: "result-1",
			Outcome:         &SubagentRunOutcome{Status: SubagentOutcomeOK},
		},
	}
	msg := BuildBatchedChildCompletionAnnouncement(entries)
	if !strings.Contains(msg, "worker-1") || !strings.Contains(msg, "result-1") {
		t.Fatalf("single-entry batch should produce same output as non-batched, got %q", msg)
	}
	if strings.Contains(msg, "child tasks finished") {
		t.Fatal("single-entry batch should not contain batch header")
	}
}

func TestBuildBatchedChildCompletionAnnouncementMultiple(t *testing.T) {
	entries := []SubagentRunRecord{
		{
			Label:           "worker-1",
			ChildSessionKey: "agent:main:subagent:one",
			FrozenResultText: "result-1",
			Outcome:         &SubagentRunOutcome{Status: SubagentOutcomeOK},
		},
		{
			Label:           "worker-2",
			ChildSessionKey: "agent:main:subagent:two",
			FrozenResultText: "result-2",
			Outcome:         &SubagentRunOutcome{Status: SubagentOutcomeError, Error: "timeout"},
		},
	}
	msg := BuildBatchedChildCompletionAnnouncement(entries)
	if !strings.Contains(msg, "[Subagent completions] 2 child tasks finished") {
		t.Fatalf("expected batch header, got %q", msg)
	}
	if !strings.Contains(msg, "1. [Subagent completion] worker-1") {
		t.Fatal("expected numbered first entry")
	}
	if !strings.Contains(msg, "2. [Subagent completion] worker-2") {
		t.Fatal("expected numbered second entry")
	}
	if !strings.Contains(msg, "result-1") || !strings.Contains(msg, "result-2") {
		t.Fatal("expected both results in batched message")
	}
	if !strings.Contains(msg, "error: timeout") {
		t.Fatal("expected error status for second entry")
	}
}

func TestPrepareBatchedSubagentAnnouncementSingle(t *testing.T) {
	registry := NewSubagentRegistry()
	now := time.Now().UTC()
	registry.Register(SubagentRunRecord{
		RunID:                    "run-1",
		ChildSessionKey:          "agent:main:subagent:one",
		RequesterSessionKey:      "agent:main:main",
		Label:                    "worker-1",
		ExpectsCompletionMessage: true,
		CreatedAt:                now,
		StartedAt:                now,
	})
	registry.Complete("run-1", core.AgentRunResult{}, nil)

	ann, runIDs := PrepareBatchedSubagentAnnouncement(registry, "agent:main:main", now)
	if ann == nil {
		t.Fatal("expected non-nil announcement")
	}
	if len(runIDs) != 1 || runIDs[0] != "run-1" {
		t.Fatalf("unexpected runIDs: %v", runIDs)
	}
	if !strings.Contains(ann.PrimaryRequest.Message, "worker-1") {
		t.Fatalf("expected message to include label, got %q", ann.PrimaryRequest.Message)
	}
}

func TestPrepareBatchedSubagentAnnouncementBatchesMultiple(t *testing.T) {
	registry := NewSubagentRegistry()
	now := time.Now().UTC()
	for i, label := range []string{"worker-1", "worker-2", "worker-3"} {
		runID := fmt.Sprintf("run-%d", i+1)
		registry.Register(SubagentRunRecord{
			RunID:                    runID,
			ChildSessionKey:          fmt.Sprintf("agent:main:subagent:%s", label),
			RequesterSessionKey:      "agent:main:main",
			Label:                    label,
			ExpectsCompletionMessage: true,
			CreatedAt:                now,
			StartedAt:                now,
		})
		registry.Complete(runID, core.AgentRunResult{}, nil)
	}

	ann, runIDs := PrepareBatchedSubagentAnnouncement(registry, "agent:main:main", now)
	if ann == nil {
		t.Fatal("expected non-nil announcement")
	}
	if len(runIDs) != 3 {
		t.Fatalf("expected 3 runIDs, got %d", len(runIDs))
	}
	if !strings.Contains(ann.PrimaryRequest.Message, "3 child tasks finished") {
		t.Fatalf("expected batched header in message, got %q", ann.PrimaryRequest.Message)
	}
	for _, label := range []string{"worker-1", "worker-2", "worker-3"} {
		if !strings.Contains(ann.PrimaryRequest.Message, label) {
			t.Fatalf("expected label %q in batched message", label)
		}
	}
}

func TestReleaseDeferredAnnouncementsForRequester(t *testing.T) {
	registry := NewSubagentRegistry()
	now := time.Now().UTC()
	registry.Register(SubagentRunRecord{
		RunID:                    "run-1",
		ChildSessionKey:          "agent:main:subagent:one",
		RequesterSessionKey:      "agent:main:main",
		ExpectsCompletionMessage: true,
		CreatedAt:                now,
		StartedAt:                now,
	})
	registry.Register(SubagentRunRecord{
		RunID:                    "run-2",
		ChildSessionKey:          "agent:main:subagent:two",
		RequesterSessionKey:      "agent:main:main",
		ExpectsCompletionMessage: true,
		CreatedAt:                now,
		StartedAt:                now,
	})
	registry.Complete("run-1", core.AgentRunResult{}, nil)
	registry.Complete("run-2", core.AgentRunResult{}, nil)
	registry.MarkCompletionDeferredUntil("run-1", now.Add(10*time.Minute))
	registry.MarkCompletionDeferredUntil("run-2", now.Add(10*time.Minute))

	// Before release: both should be filtered out since they are deferred.
	pending := registry.PendingAnnouncementsForRequester("agent:main:main", now)
	if len(pending) != 0 {
		t.Fatalf("expected 0 pending before release, got %d", len(pending))
	}

	// Release.
	registry.ReleaseDeferredAnnouncementsForRequester("agent:main:main")

	// After release: both should be visible.
	pending = registry.PendingAnnouncementsForRequester("agent:main:main", now)
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending after release, got %d", len(pending))
	}
}
