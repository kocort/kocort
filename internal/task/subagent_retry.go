package task

import "time"

func (r *SubagentRegistry) NextAnnouncementRetryDelayForRequester(requesterSessionKey string, now time.Time) (time.Duration, bool) {
	r.SweepExpired()
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var best time.Time
	for _, record := range r.runs {
		if record == nil || record.RequesterSessionKey != requesterSessionKey || record.EndedAt.IsZero() || record.CleanupHandled {
			continue
		}
		candidate := time.Time{}
		if !record.CompletionDeferredAt.IsZero() && record.CompletionDeferredAt.After(now) {
			candidate = record.CompletionDeferredAt
		}
		if !record.NextAnnounceAttemptAt.IsZero() && record.NextAnnounceAttemptAt.After(now) && (candidate.IsZero() || record.NextAnnounceAttemptAt.Before(candidate)) {
			candidate = record.NextAnnounceAttemptAt
		}
		if candidate.IsZero() {
			continue
		}
		if best.IsZero() || candidate.Before(best) {
			best = candidate
		}
	}
	if best.IsZero() {
		return 0, false
	}
	return best.Sub(now), true
}

func (r *SubagentRegistry) ScheduleAnnouncementRetry(requesterSessionKey string, delay time.Duration, callback func()) {
	if r == nil || callback == nil {
		return
	}
	if delay < 0 {
		delay = 0
	}
	r.mu.Lock()
	if existing := r.announceTimers[requesterSessionKey]; existing != nil {
		existing.Stop()
		delete(r.announceTimers, requesterSessionKey)
	}
	timer := time.AfterFunc(delay, func() {
		r.mu.Lock()
		delete(r.announceTimers, requesterSessionKey)
		r.mu.Unlock()
		callback()
	})
	r.announceTimers[requesterSessionKey] = timer
	r.mu.Unlock()
}
