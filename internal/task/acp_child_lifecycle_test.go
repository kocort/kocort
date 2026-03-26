package task

import (
	"testing"

	"github.com/kocort/kocort/internal/acp"
	"github.com/kocort/kocort/internal/core"
)

func TestBuildACPChildRunRecord(t *testing.T) {
	record := BuildACPChildRunRecord(acp.SessionSpawnRequest{
		RequesterSessionKey: "agent:main:main",
		TargetAgentID:       "worker",
		Task:                "inspect",
		Label:               "worker-acp",
		RunTimeoutSeconds:   30,
		SpawnMode:           "session",
		RouteChannel:        "discord",
		RouteThreadID:       "thread-1",
	}, acp.SessionLaunchPlan{
		ChildRequest: core.AgentRunRequest{
			SessionModelOverride: "gpt-5.4",
		},
		SessionEntry: core.SessionEntry{
			ACP: &core.AcpSessionMeta{
				Backend:        "acp-live",
				State:          "idle",
				Mode:           core.AcpSessionModePersistent,
				RuntimeStatus:  &core.AcpRuntimeStatus{Summary: "healthy"},
				RuntimeOptions: &core.AcpSessionRuntimeOptions{Model: "gpt-5.4"},
			},
		},
		Result: acp.SessionSpawnResult{
			RunID:           "run-1",
			ChildSessionKey: "agent:worker:acp:acp-live:test",
			WorkspaceDir:    "/tmp/worker",
		},
	})
	if record.ChildKind != "acp" || record.RunID != "run-1" || record.ChildSessionKey == "" {
		t.Fatalf("unexpected ACP child record: %+v", record)
	}
	if record.ExpectsCompletionMessage {
		t.Fatalf("expected ACP child record to default completion announce off, got %+v", record)
	}
	if record.Model != "gpt-5.4" || record.SpawnMode != "session" {
		t.Fatalf("unexpected ACP lifecycle metadata: %+v", record)
	}
	if record.RuntimeBackend != "acp-live" || record.RuntimeState != "idle" || record.RuntimeMode != "persistent" || record.RuntimeStatusSummary != "healthy" {
		t.Fatalf("unexpected ACP runtime metadata: %+v", record)
	}
}

func TestBuildACPChildRunRecordEnablesParentRelay(t *testing.T) {
	record := BuildACPChildRunRecord(acp.SessionSpawnRequest{
		RequesterSessionKey: "agent:main:main",
		Task:                "inspect",
		StreamTo:            "parent",
	}, acp.SessionLaunchPlan{
		Result: acp.SessionSpawnResult{
			RunID:           "run-2",
			ChildSessionKey: "agent:worker:acp:acp-live:test2",
		},
	})
	if !record.ExpectsCompletionMessage {
		t.Fatalf("expected parent relay to enable completion messaging, got %+v", record)
	}
}
