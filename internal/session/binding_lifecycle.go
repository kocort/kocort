package session

import (
	"strings"
	"time"
)

const (
	defaultSessionBindingIdleTimeout = 24 * time.Hour
	defaultSessionBindingMaxAge      = 0 * time.Hour
)

func DefaultSessionBindingLifecycle() (idleTimeoutMs int64, maxAgeMs int64) {
	return defaultSessionBindingIdleTimeout.Milliseconds(), defaultSessionBindingMaxAge.Milliseconds()
}

func normalizeSessionBindingDurationMs(raw int64) int64 {
	if raw <= 0 {
		return 0
	}
	return raw
}

func isSessionBindingExpired(record SessionBindingRecord, now time.Time) bool {
	_, expired := resolveSessionBindingExpiryReason(record, now)
	return expired
}

func resolveSessionBindingExpiryReason(record SessionBindingRecord, now time.Time) (string, bool) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if record.MaxAgeMs > 0 {
		boundAt := record.BoundAt
		if boundAt.IsZero() {
			boundAt = now
		}
		if now.After(boundAt.Add(time.Duration(record.MaxAgeMs) * time.Millisecond)) {
			return "max-age-expired", true
		}
	}
	if record.IdleTimeoutMs > 0 {
		lastActivityAt := record.LastActivityAt
		if lastActivityAt.IsZero() {
			lastActivityAt = record.BoundAt
		}
		if !lastActivityAt.IsZero() && now.After(lastActivityAt.Add(time.Duration(record.IdleTimeoutMs)*time.Millisecond)) {
			return "idle-expired", true
		}
	}
	if !record.ExpiresAt.IsZero() && now.After(record.ExpiresAt) {
		return "max-age-expired", true
	}
	return "", false
}

func NormalizeSessionBindingBoundBy(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "system"
	}
	return value
}

func NormalizeSessionBindingPlacement(raw string) string {
	value := strings.TrimSpace(strings.ToLower(raw))
	if value == "" {
		return "child"
	}
	switch value {
	case "current", "child":
		return value
	default:
		return "child"
	}
}

func normalizeSessionBindingTargetKind(raw string) string {
	value := strings.TrimSpace(strings.ToLower(raw))
	switch value {
	case "subagent", "session":
		return value
	default:
		return "session"
	}
}

func normalizeSessionBindingStatus(raw string) string {
	value := strings.TrimSpace(strings.ToLower(raw))
	switch value {
	case "active", "ending", "ended":
		return value
	default:
		return "active"
	}
}
