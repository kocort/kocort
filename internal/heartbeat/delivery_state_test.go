package heartbeat

import (
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
)

func TestShouldSuppressDuplicate(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	entry := &core.SessionEntry{
		LastHeartbeatText:   "Reminder delivered",
		LastHeartbeatSentAt: now.Add(-1 * time.Hour),
	}
	if !ShouldSuppressDuplicate(entry, "Reminder delivered", false, now) {
		t.Fatal("expected duplicate heartbeat to be suppressed")
	}
	if ShouldSuppressDuplicate(entry, "Reminder delivered", true, now) {
		t.Fatal("expected media heartbeat not to be suppressed")
	}
	if ShouldSuppressDuplicate(entry, "Different", false, now) {
		t.Fatal("expected different heartbeat text not to be suppressed")
	}
	if ShouldSuppressDuplicate(&core.SessionEntry{
		LastHeartbeatText:   "Reminder delivered",
		LastHeartbeatSentAt: now.Add(-25 * time.Hour),
	}, "Reminder delivered", false, now) {
		t.Fatal("expected expired duplicate window not to suppress")
	}
}
