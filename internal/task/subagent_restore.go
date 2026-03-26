package task

import (
	"sort"
	"strings"
	"time"
)

// PendingAnnouncementRecoveryRequesters returns requester session keys that
// still have unsent completion announcements after state restore. Hard-expired
// or exhausted items are marked terminal before returning.
func PendingAnnouncementRecoveryRequesters(registry *SubagentRegistry, now time.Time) []string {
	if registry == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	registry.SweepExpired()
	registry.mu.Lock()
	defer registry.mu.Unlock()

	changed := false
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, record := range registry.runs {
		if record == nil || record.EndedAt.IsZero() || record.CleanupHandled {
			continue
		}
		if !record.ExpectsCompletionMessage || strings.TrimSpace(record.SuppressAnnounceReason) != "" {
			record.CompletionMessageSentAt = now
			record.CleanupCompletedAt = now
			record.CleanupHandled = true
			record.NextAnnounceAttemptAt = time.Time{}
			changed = true
			continue
		}
		if shouldGiveUpSubagentAnnouncement(*record, now) {
			record.CompletionMessageSentAt = now
			record.CleanupCompletedAt = now
			record.CleanupHandled = true
			record.NextAnnounceAttemptAt = time.Time{}
			changed = true
			continue
		}
		if _, ok := seen[record.RequesterSessionKey]; ok {
			continue
		}
		seen[record.RequesterSessionKey] = struct{}{}
		out = append(out, record.RequesterSessionKey)
	}
	if changed {
		registry.persistSnapshotLocked()
	}
	sort.Strings(out)
	return out
}
