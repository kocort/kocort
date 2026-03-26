package session

import (
	"strings"
	"time"
)

// EvaluateSessionFreshness checks whether a session is still fresh based on the
// given policy.
func EvaluateSessionFreshness(updatedAt time.Time, now time.Time, policy SessionFreshnessPolicy) SessionFreshnessResult {
	if updatedAt.IsZero() {
		return SessionFreshnessResult{Fresh: false, Reason: "missing"}
	}
	mode := strings.TrimSpace(strings.ToLower(policy.Mode))
	if mode == "" {
		mode = "idle"
	}
	var expiries []struct {
		reason string
		at     time.Time
	}
	if mode == "daily" {
		expiries = append(expiries, struct {
			reason string
			at     time.Time
		}{
			reason: "daily",
			at:     NextDailyResetAfter(updatedAt, now.Location(), policy.AtHour),
		})
	}
	if policy.IdleMinutes > 0 {
		expiries = append(expiries, struct {
			reason string
			at     time.Time
		}{
			reason: "idle",
			at:     updatedAt.Add(time.Duration(policy.IdleMinutes) * time.Minute),
		})
	}
	if len(expiries) == 0 {
		return SessionFreshnessResult{Fresh: true}
	}
	earliest := expiries[0]
	for _, expiry := range expiries[1:] {
		if expiry.at.Before(earliest.at) {
			earliest = expiry
		}
	}
	if !now.Before(earliest.at) {
		return SessionFreshnessResult{Fresh: false, Reason: earliest.reason}
	}
	return SessionFreshnessResult{Fresh: true}
}

// NextDailyResetAfter computes the next daily reset boundary after updatedAt.
func NextDailyResetAfter(updatedAt time.Time, loc *time.Location, atHour int) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	if atHour < 0 || atHour > 23 {
		atHour = 0
	}
	base := updatedAt.In(loc)
	boundary := time.Date(base.Year(), base.Month(), base.Day(), atHour, 0, 0, 0, loc)
	if !updatedAt.Before(boundary) {
		boundary = boundary.Add(24 * time.Hour)
	}
	return boundary
}
