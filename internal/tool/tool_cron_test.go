package tool

import (
	"strings"
	"testing"
)

func TestCronToolDescriptionMatchesReminderGuidance(t *testing.T) {
	desc := NewCronTool().Description()
	if !strings.Contains(desc, "use for reminders") {
		t.Fatalf("expected reminder guidance in cron description, got %s", desc)
	}
	if !strings.Contains(desc, "read like a reminder when it fires") {
		t.Fatalf("expected firing-time reminder wording in cron description, got %s", desc)
	}
}
