package heartbeat

import (
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
)

const heartbeatDuplicateWindow = 24 * time.Hour

func ShouldSuppressDuplicate(entry *core.SessionEntry, text string, hasMedia bool, now time.Time) bool {
	if entry == nil || hasMedia {
		return false
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	if strings.TrimSpace(entry.LastHeartbeatText) == "" {
		return false
	}
	if !entry.LastHeartbeatSentAt.IsZero() && now.Sub(entry.LastHeartbeatSentAt) > heartbeatDuplicateWindow {
		return false
	}
	return strings.TrimSpace(entry.LastHeartbeatText) == text
}

func ApplyHeartbeatDelivery(entry core.SessionEntry, text string, now time.Time) core.SessionEntry {
	entry.LastHeartbeatText = strings.TrimSpace(text)
	entry.LastHeartbeatSentAt = now.UTC()
	return entry
}
