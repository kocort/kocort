package acp

import "testing"

func TestNormalizeSessionSpawnRequestDefaultsToRun(t *testing.T) {
	req := NormalizeSessionSpawnRequest(SessionSpawnRequest{
		Task: "inspect",
	}, SessionSpawnDefaults{
		RequesterSessionKey: "agent:main:main",
		RequesterAgentID:    "main",
		RouteChannel:        "discord",
		RouteThreadID:       "thread-1",
		DefaultTimeoutSec:   90,
	})
	if req.SpawnMode != "run" || req.SandboxMode != "inherit" || req.RunTimeoutSeconds != 90 || req.RouteChannel != "discord" || req.RouteThreadID != "thread-1" {
		t.Fatalf("unexpected normalized request: %+v", req)
	}
}

func TestValidateSessionSpawnRequestRejectsThreadSessionModeMismatch(t *testing.T) {
	err := ValidateSessionSpawnRequest(SessionSpawnRequest{SpawnMode: "session"})
	if err == nil || err.Error() != `mode="session" requires thread=true` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateSessionSpawnRequestAllowsSessionModeThreadContext(t *testing.T) {
	if err := ValidateSessionSpawnRequest(SessionSpawnRequest{
		ThreadRequested: true,
		SpawnMode:       "session",
		RouteThreadID:   "thread-1",
	}); err != nil {
		t.Fatalf("expected session-mode thread request to pass, got %v", err)
	}
}
