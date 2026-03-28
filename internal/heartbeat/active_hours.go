package heartbeat

import (
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
)

const minutesPerDay = 24 * 60

func IsWithinActiveHours(identity core.AgentIdentity, now time.Time) bool {
	startMin := parseActiveHoursMinute(identity.HeartbeatActiveHoursStart, false)
	endMin := parseActiveHoursMinute(identity.HeartbeatActiveHoursEnd, true)
	if startMin < 0 || endMin < 0 {
		return true
	}
	if startMin == endMin {
		return false
	}

	location := resolveActiveHoursLocation(identity)
	current := resolveMinutesInLocation(now, location)
	if current < 0 {
		return true
	}
	if endMin > startMin {
		return current >= startMin && current < endMin
	}
	return current >= startMin || current < endMin
}

func parseActiveHoursMinute(raw string, allow24 bool) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return -1
	}
	parts := strings.Split(raw, ":")
	if len(parts) != 2 {
		return -1
	}
	hour := parseTwoDigitInt(parts[0])
	minute := parseTwoDigitInt(parts[1])
	if hour < 0 || minute < 0 || minute > 59 {
		return -1
	}
	if hour == 24 {
		if !allow24 || minute != 0 {
			return -1
		}
		return minutesPerDay
	}
	if hour > 23 {
		return -1
	}
	return hour*60 + minute
}

func parseTwoDigitInt(raw string) int {
	if len(raw) != 2 {
		return -1
	}
	value := 0
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return -1
		}
		value = value*10 + int(ch-'0')
	}
	return value
}

func resolveActiveHoursLocation(identity core.AgentIdentity) *time.Location {
	raw := strings.TrimSpace(identity.HeartbeatActiveHoursTimezone)
	switch strings.ToLower(raw) {
	case "", "user":
		raw = strings.TrimSpace(identity.UserTimezone)
	case "local":
		return time.Now().Location()
	}
	if raw == "" {
		return time.UTC
	}
	location, err := time.LoadLocation(raw)
	if err != nil {
		fallback := strings.TrimSpace(identity.UserTimezone)
		if fallback == "" || strings.EqualFold(fallback, raw) {
			return time.UTC
		}
		location, err = time.LoadLocation(fallback)
		if err != nil {
			return time.UTC
		}
	}
	return location
}

func resolveMinutesInLocation(now time.Time, location *time.Location) int {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if location == nil {
		location = time.UTC
	}
	local := now.In(location)
	return local.Hour()*60 + local.Minute()
}
