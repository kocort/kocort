package acp

import (
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/core"
)

func TestPrepareSessionLaunchBuildsAcpSessionMetadata(t *testing.T) {
	plan, err := PrepareSessionLaunch(SessionSpawnRequest{
		RequesterSessionKey: "agent:main:main",
		RequesterAgentID:    "main",
		TargetAgentID:       "worker",
		Task:                "inspect logs",
		RunTimeoutSeconds:   45,
	}, &core.AgentIdentity{
		ID:             "worker",
		Name:           "Worker",
		RuntimeType:    "acp",
		RuntimeBackend: "acp-live",
		RuntimeCwd:     "/tmp/worker",
		DefaultModel:   "gpt-5.4",
	}, nil)
	if err != nil {
		t.Fatalf("prepare launch: %v", err)
	}
	if !strings.Contains(plan.Result.ChildSessionKey, ":acp:acp-live:") {
		t.Fatalf("unexpected child session key: %s", plan.Result.ChildSessionKey)
	}
	if plan.ChildRequest.Lane != core.LaneNested {
		t.Fatalf("unexpected lane: %+v", plan.ChildRequest)
	}
	if plan.SessionEntry.ACP == nil || plan.SessionEntry.ACP.Backend != "acp-live" || plan.SessionEntry.ACP.Mode != core.AcpSessionModeOneShot {
		t.Fatalf("unexpected acp metadata: %+v", plan.SessionEntry.ACP)
	}
}

func TestPrepareSessionLaunchPersistsRouteBinding(t *testing.T) {
	plan, err := PrepareSessionLaunch(SessionSpawnRequest{
		RequesterSessionKey: "agent:main:main",
		RequesterAgentID:    "main",
		TargetAgentID:       "worker",
		Task:                "inspect logs",
		RouteChannel:        "discord",
		RouteTo:             "chan-1",
		RouteThreadID:       "thread-2",
	}, &core.AgentIdentity{
		ID:             "worker",
		RuntimeType:    "acp",
		RuntimeBackend: "acp-live",
	}, nil)
	if err != nil {
		t.Fatalf("prepare launch: %v", err)
	}
	if plan.ChildRequest.Channel != "discord" || plan.ChildRequest.ThreadID != "thread-2" {
		t.Fatalf("unexpected child route binding: %+v", plan.ChildRequest)
	}
	if plan.SessionEntry.DeliveryContext == nil || plan.SessionEntry.DeliveryContext.ThreadID != "thread-2" {
		t.Fatalf("expected session delivery context, got %+v", plan.SessionEntry)
	}
}

func TestPrepareSessionLaunchUsesPersistentModeForSessionSpawn(t *testing.T) {
	plan, err := PrepareSessionLaunch(SessionSpawnRequest{
		RequesterSessionKey: "agent:main:main",
		RequesterAgentID:    "main",
		TargetAgentID:       "worker",
		Task:                "inspect logs",
		SpawnMode:           "session",
		ThreadRequested:     true,
		RouteThreadID:       "thread-2",
	}, &core.AgentIdentity{
		ID:             "worker",
		RuntimeType:    "acp",
		RuntimeBackend: "acp-live",
	}, nil)
	if err != nil {
		t.Fatalf("prepare launch: %v", err)
	}
	if plan.ChildRequest.Lane != core.LaneDefault || plan.SessionEntry.ACP == nil || plan.SessionEntry.ACP.Mode != core.AcpSessionModePersistent || plan.Result.Mode != "session" {
		t.Fatalf("unexpected persistent session launch: %+v / %+v", plan.ChildRequest, plan.SessionEntry)
	}
}

func TestPrepareSessionLaunchEnablesDirectDeliveryForParentStream(t *testing.T) {
	plan, err := PrepareSessionLaunch(SessionSpawnRequest{
		RequesterSessionKey: "agent:main:main",
		RequesterAgentID:    "main",
		TargetAgentID:       "worker",
		Task:                "inspect logs",
		StreamTo:            "parent",
		RouteChannel:        "discord",
		RouteTo:             "chan-1",
		RouteAccountID:      "acct-1",
		RouteThreadID:       "thread-2",
	}, &core.AgentIdentity{
		ID:             "worker",
		RuntimeType:    "acp",
		RuntimeBackend: "acp-live",
	}, nil)
	if err != nil {
		t.Fatalf("prepare launch: %v", err)
	}
	if !plan.ChildRequest.Deliver {
		t.Fatalf("expected child request delivery enabled for parent stream, got %+v", plan.ChildRequest)
	}
	if strings.TrimSpace(plan.Result.StreamLogPath) == "" {
		t.Fatalf("expected stream log path, got %+v", plan.Result)
	}
}
