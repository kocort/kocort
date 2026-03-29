package delivery

import (
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/infra"
)

func TestApplyReminderReplyGuardAppendsNoteWhenReminderUnbacked(t *testing.T) {
	result := ApplyReminderGuard(nil, "agent:main:main", 0, core.AgentRunResult{
		Payloads: []core.ReplyPayload{{Text: "I'll remind you tomorrow morning."}},
	})
	if len(result.Payloads) != 1 || !strings.Contains(result.Payloads[0].Text, infra.UnscheduledReminderNote) {
		t.Fatalf("expected unscheduled reminder note, got %+v", result.Payloads)
	}
}

func TestApplyReminderReplyGuardKeepsReminderWhenCronAddSucceeded(t *testing.T) {
	result := ApplyReminderGuard(nil, "agent:main:main", 1, core.AgentRunResult{
		Payloads:           []core.ReplyPayload{{Text: "I'll remind you tomorrow morning."}},
		SuccessfulCronAdds: 1,
	})
	if len(result.Payloads) != 1 {
		t.Fatalf("expected single payload, got %+v", result.Payloads)
	}
	if strings.Contains(result.Payloads[0].Text, infra.UnscheduledReminderNote) {
		t.Fatalf("expected no guard note when cron add succeeded, got %+v", result.Payloads)
	}
}
