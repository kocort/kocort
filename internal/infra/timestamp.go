package infra

import (
	"fmt"
	"strings"
	"time"
)

func InjectTimestamp(message string, timezone string, now time.Time) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return message
	}
	if strings.HasPrefix(message, "[") && strings.Contains(message, "] ") {
		return message
	}
	location := time.UTC
	ianaName := ""
	if trimmed := strings.TrimSpace(timezone); trimmed != "" {
		if loaded, err := time.LoadLocation(trimmed); err == nil {
			location = loaded
			if trimmed != "UTC" {
				ianaName = trimmed
			}
		}
	}
	current := now.In(location)
	label := fmt.Sprintf("%s %s", current.Format("Mon"), current.Format("2006-01-02 15:04 MST"))
	if ianaName != "" {
		label = fmt.Sprintf("%s (%s)", label, ianaName)
	}
	return fmt.Sprintf("[%s] %s", label, message)
}

// StripTimestampPrefix removes the leading "[...] " timestamp injected by
// InjectTimestamp from a message string. Returns the original string trimmed
// if no prefix is found.
func stripTimestampPrefix(message string) string {
	trimmed := strings.TrimSpace(message)
	if strings.HasPrefix(trimmed, "[") {
		if end := strings.Index(trimmed, "] "); end > 0 {
			return strings.TrimSpace(trimmed[end+2:])
		}
	}
	return trimmed
}
