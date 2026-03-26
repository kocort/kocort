package task

import (
	"strings"
	"time"
)

func (r *SubagentRegistry) ListByRequester(requesterSessionKey string) []SubagentRunRecord {
	r.SweepExpired()
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []SubagentRunRecord
	for _, record := range r.runs {
		if record.RequesterSessionKey == requesterSessionKey && (!record.CleanupHandled || strings.TrimSpace(record.SpawnMode) == "session") {
			out = append(out, *record)
		}
	}
	return out
}

func (r *SubagentRegistry) PendingAnnouncementsForRequester(requesterSessionKey string, now time.Time) []SubagentRunRecord {
	r.SweepExpired()
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []SubagentRunRecord
	for _, record := range r.runs {
		if record == nil || record.RequesterSessionKey != requesterSessionKey {
			continue
		}
		if record.EndedAt.IsZero() || record.CleanupHandled {
			continue
		}
		if !record.CompletionDeferredAt.IsZero() && record.CompletionDeferredAt.After(now) {
			continue
		}
		if !record.NextAnnounceAttemptAt.IsZero() && record.NextAnnounceAttemptAt.After(now) {
			continue
		}
		out = append(out, *record)
	}
	return out
}

func (r *SubagentRegistry) CountPendingDescendantRuns(sessionKey string) int {
	r.SweepExpired()
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, record := range r.runs {
		if record == nil || record.EndedAt.IsZero() == false {
			continue
		}
		if IsRequesterDescendant(record.RequesterSessionKey, sessionKey) {
			count++
		}
	}
	return count
}

func (r *SubagentRegistry) CountPendingDescendantRunsExcludingRun(sessionKey string, runID string) int {
	r.SweepExpired()
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, record := range r.runs {
		if record == nil || record.RunID == runID || !record.EndedAt.IsZero() {
			continue
		}
		if IsRequesterDescendant(record.RequesterSessionKey, sessionKey) {
			count++
		}
	}
	return count
}
