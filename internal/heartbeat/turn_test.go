package heartbeat

import (
	"strings"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/infra"
)

func TestBuildTurnPlanSkipsWhenNoEventsAndNoActionableHeartbeatFile(t *testing.T) {
	t.Parallel()

	plan := BuildTurnPlan(TurnPlanInput{
		Identity:             core.AgentIdentity{ID: "main"},
		Request:              HeartbeatWakeRequest{AgentID: "main"},
		HeartbeatFileContent: "# Heartbeat\n- [ ]",
		HeartbeatFileExists:  true,
	})
	if !plan.Skip {
		t.Fatal("expected heartbeat turn to be skipped")
	}
	if plan.SkipReason != "no-events" {
		t.Fatalf("unexpected skip reason: %q", plan.SkipReason)
	}
}

func TestBuildTurnPlanUsesCronPromptAndModelOverride(t *testing.T) {
	t.Parallel()

	plan := BuildTurnPlan(TurnPlanInput{
		Identity: core.AgentIdentity{
			ID:                   "main",
			HeartbeatPrompt:      "default prompt",
			HeartbeatTarget:      "last",
			HeartbeatModel:       "gpt-5.4",
			HeartbeatAckMaxChars: 123,
		},
		Request: HeartbeatWakeRequest{
			AgentID:    "main",
			SessionKey: "agent:main:webchat:direct:user",
			Reason:     "cron:test",
		},
		SessionEntry: &core.SessionEntry{
			LastChannel: "webchat",
			LastTo:      "user",
		},
		WorkspaceDir: "E:/workspace/kocort",
		Events: []infra.SystemEvent{{
			Text:       "Reminder: review alerts",
			Timestamp:  time.Now().UTC(),
			ContextKey: "cron:test",
		}},
	})

	if plan.Skip {
		t.Fatal("expected heartbeat turn plan to run")
	}
	if !plan.Deliver {
		t.Fatal("expected cron heartbeat to be deliverable")
	}
	if plan.Model != "gpt-5.4" {
		t.Fatalf("expected heartbeat model override, got %q", plan.Model)
	}
	if plan.AckMaxChars != 123 {
		t.Fatalf("expected ack max chars 123, got %d", plan.AckMaxChars)
	}
	if plan.Prompt == "default prompt" {
		t.Fatalf("expected cron prompt override, got %q", plan.Prompt)
	}
	if len(plan.InternalEvents) != 1 {
		t.Fatalf("expected one internal event, got %d", len(plan.InternalEvents))
	}
}

func TestBuildTurnPlanSkipsWhenRequestsAreInFlight(t *testing.T) {
	t.Parallel()

	plan := BuildTurnPlan(TurnPlanInput{
		Identity:    core.AgentIdentity{ID: "main"},
		Request:     HeartbeatWakeRequest{AgentID: "main", Reason: "interval"},
		SessionBusy: true,
	})
	if !plan.Skip {
		t.Fatal("expected busy heartbeat turn to be skipped")
	}
	if plan.SkipReason != "requests-in-flight" {
		t.Fatalf("unexpected skip reason: %q", plan.SkipReason)
	}
}

func TestBuildTurnPlanAddsHeartbeatPathHintForDefaultPrompt(t *testing.T) {
	t.Parallel()

	plan := BuildTurnPlan(TurnPlanInput{
		Identity: core.AgentIdentity{
			ID:              "main",
			HeartbeatPrompt: HeartbeatPromptDefault,
		},
		Request:              HeartbeatWakeRequest{AgentID: "main"},
		HeartbeatFileContent: "Check the inbox",
		WorkspaceDir:         "E:/workspace/kocort",
	})
	if plan.Skip {
		t.Fatal("expected heartbeat plan to run")
	}
	if !strings.Contains(plan.Prompt, "E:/workspace/kocort/HEARTBEAT.md") {
		t.Fatalf("expected heartbeat path hint, got %q", plan.Prompt)
	}
}

func TestBuildTurnPlanSkipsOutsideActiveHours(t *testing.T) {
	t.Parallel()

	plan := BuildTurnPlan(TurnPlanInput{
		Identity: core.AgentIdentity{
			ID:                        "main",
			UserTimezone:              "UTC",
			HeartbeatActiveHoursStart: "09:00",
			HeartbeatActiveHoursEnd:   "17:00",
		},
		Request:              HeartbeatWakeRequest{AgentID: "main", Reason: "interval"},
		HeartbeatFileContent: "check queue",
		HeartbeatFileExists:  true,
		Now:                  time.Date(2026, time.March, 28, 2, 0, 0, 0, time.UTC),
	})
	if !plan.Skip {
		t.Fatal("expected heartbeat plan to skip outside active hours")
	}
	if plan.SkipReason != "quiet-hours" {
		t.Fatalf("unexpected skip reason: %q", plan.SkipReason)
	}
}

func TestBuildTurnPlanAllowsMissingHeartbeatFile(t *testing.T) {
	t.Parallel()

	plan := BuildTurnPlan(TurnPlanInput{
		Identity: core.AgentIdentity{
			ID:              "main",
			HeartbeatPrompt: HeartbeatPromptDefault,
		},
		Request:             HeartbeatWakeRequest{AgentID: "main", Reason: "interval"},
		HeartbeatFileExists: false,
	})
	if plan.Skip {
		t.Fatalf("expected missing HEARTBEAT.md to remain runnable, got skip=%q", plan.SkipReason)
	}
}

func TestBuildTurnPlanUsesIsolatedHeartbeatSession(t *testing.T) {
	t.Parallel()

	plan := BuildTurnPlan(TurnPlanInput{
		Identity: core.AgentIdentity{
			ID:                       "main",
			HeartbeatIsolatedSession: true,
		},
		Request: HeartbeatWakeRequest{
			AgentID:    "main",
			SessionKey: "agent:main:webchat:direct:user",
		},
		HeartbeatFileContent: "check inbox",
	})
	if plan.RunSessionKey != "agent:main:webchat:direct:user:heartbeat" {
		t.Fatalf("unexpected run session key: %q", plan.RunSessionKey)
	}
	if !plan.IsolatedRun {
		t.Fatal("expected isolated heartbeat session to be enabled")
	}
}
