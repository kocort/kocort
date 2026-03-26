package task

import (
	"testing"
	"time"
)

func TestPendingAnnouncementRecoveryRequestersIncludesFutureRetryAndPrunesExpired(t *testing.T) {
	registry := NewSubagentRegistry()
	now := time.Now().UTC()
	registry.Register(SubagentRunRecord{
		RunID:                    "run-future",
		RequesterSessionKey:      "agent:main:main",
		ChildSessionKey:          "agent:worker:subagent:future",
		ExpectsCompletionMessage: true,
		EndedAt:                  now.Add(-time.Minute),
		NextAnnounceAttemptAt:    now.Add(5 * time.Second),
	})
	registry.Register(SubagentRunRecord{
		RunID:                    "run-expired",
		RequesterSessionKey:      "agent:main:main",
		ChildSessionKey:          "agent:worker:subagent:expired",
		ExpectsCompletionMessage: true,
		EndedAt:                  now.Add(-subagentAnnounceCompletionHardExpiry).Add(-time.Minute),
	})

	requesters := PendingAnnouncementRecoveryRequesters(registry, now)
	if len(requesters) != 1 || requesters[0] != "agent:main:main" {
		t.Fatalf("expected requester needing recovery, got %+v", requesters)
	}
	expired := registry.Get("run-expired")
	if expired == nil || expired.CompletionMessageSentAt.IsZero() || expired.CleanupCompletedAt.IsZero() {
		t.Fatalf("expected expired announcement to be marked terminal, got %+v", expired)
	}
	if !expired.CleanupHandled {
		t.Fatalf("expected expired announcement to be marked cleanup handled, got %+v", expired)
	}
}
