package heartbeat

import (
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
)

func TestIsWithinActiveHoursUsesUserTimezoneByDefault(t *testing.T) {
	t.Parallel()

	identity := core.AgentIdentity{
		UserTimezone:              "Asia/Shanghai",
		HeartbeatActiveHoursStart: "09:00",
		HeartbeatActiveHoursEnd:   "18:00",
	}
	now := time.Date(2026, time.March, 28, 2, 0, 0, 0, time.UTC) // 10:00 Asia/Shanghai
	if !IsWithinActiveHours(identity, now) {
		t.Fatal("expected time to be within active hours")
	}
}

func TestIsWithinActiveHoursSupportsOvernightWindow(t *testing.T) {
	t.Parallel()

	identity := core.AgentIdentity{
		UserTimezone:                 "UTC",
		HeartbeatActiveHoursStart:    "22:00",
		HeartbeatActiveHoursEnd:      "06:00",
		HeartbeatActiveHoursTimezone: "UTC",
	}
	now := time.Date(2026, time.March, 28, 23, 30, 0, 0, time.UTC)
	if !IsWithinActiveHours(identity, now) {
		t.Fatal("expected overnight time to be active")
	}
}
