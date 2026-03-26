package task

import "testing"

func TestNormalizeSubagentSpawnRequest(t *testing.T) {
	got := NormalizeSubagentSpawnRequest(SubagentSpawnRequest{
		Task: "do it",
	}, SubagentSpawnDefaults{
		RequesterSessionKey:     "agent:main:main",
		RequesterAgentID:        "main",
		WorkspaceDir:            "/tmp/main",
		RouteChannel:            "slack",
		RouteThreadID:           "thread-9",
		DefaultThinking:         "high",
		DefaultTimeoutSec:       30,
		MaxSpawnDepth:           5,
		MaxChildren:             5,
		CurrentDepth:            2,
		ArchiveAfterMinutes:     60,
		AttachmentMaxFiles:      4,
		AttachmentMaxFileBytes:  1024,
		AttachmentMaxTotalBytes: 2048,
		RetainAttachmentsOnKeep: true,
	})
	if got.RequesterSessionKey != "agent:main:main" || got.RequesterAgentID != "main" {
		t.Fatalf("unexpected requester defaults: %+v", got)
	}
	if got.Thinking != "high" || got.RunTimeoutSeconds != 30 {
		t.Fatalf("unexpected run defaults: %+v", got)
	}
	if got.MaxSpawnDepth != 5 || got.MaxChildren != 5 || got.CurrentDepth != 2 {
		t.Fatalf("unexpected limit defaults: %+v", got)
	}
	if got.Cleanup != "keep" || got.SpawnMode != "run" || got.RouteChannel != "slack" || got.RouteThreadID != "thread-9" {
		t.Fatalf("unexpected mode defaults: %+v", got)
	}
	if got.WorkspaceDir != "/tmp/main" || got.ArchiveAfterMinutes != 60 {
		t.Fatalf("unexpected workspace/archive defaults: %+v", got)
	}
	if !got.ExpectsCompletionMessage || got.RequesterDisplayKey != "agent:main:main" {
		t.Fatalf("unexpected completion/display defaults: %+v", got)
	}
	if got.AttachmentMaxFiles != 4 || got.AttachmentMaxFileBytes != 1024 || got.AttachmentMaxTotalBytes != 2048 || !got.RetainAttachmentsOnKeep {
		t.Fatalf("unexpected attachment defaults: %+v", got)
	}
}

func TestNormalizeSubagentSpawnRequestDefaultsThreadToSessionMode(t *testing.T) {
	got := NormalizeSubagentSpawnRequest(SubagentSpawnRequest{
		Task:            "do it",
		ThreadRequested: true,
	}, SubagentSpawnDefaults{})
	if got.SpawnMode != "session" {
		t.Fatalf("expected session mode for thread request, got %+v", got)
	}
}

func TestNormalizeSubagentSpawnRequestPreservesExplicitCompletionOptOut(t *testing.T) {
	got := NormalizeSubagentSpawnRequest(SubagentSpawnRequest{
		Task:                        "do it",
		ExpectsCompletionMessage:    false,
		ExpectsCompletionMessageSet: true,
	}, SubagentSpawnDefaults{})
	if got.ExpectsCompletionMessage {
		t.Fatalf("expected explicit completion opt-out to survive normalization, got %+v", got)
	}
}

func TestValidateSubagentSpawnRequest(t *testing.T) {
	tests := []struct {
		name string
		req  SubagentSpawnRequest
	}{
		{
			name: "session requires thread",
			req:  SubagentSpawnRequest{SpawnMode: "session"},
		},
		{
			name: "thread not yet supported",
			req:  SubagentSpawnRequest{ThreadRequested: true, SpawnMode: "session"},
		},
		{
			name: "thread run requires session mode",
			req:  SubagentSpawnRequest{ThreadRequested: true, SpawnMode: "run", RouteThreadID: "thread-1"},
		},
		{
			name: "sandbox require not yet supported",
			req:  SubagentSpawnRequest{SandboxMode: "require"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantErr := tt.name != "sandbox require not yet supported"
			err := ValidateSubagentSpawnRequest(tt.req)
			if wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !wantErr && err != nil {
				t.Fatalf("expected sandbox=require to pass request-level validation, got %v", err)
			}
		})
	}
}

func TestValidateSubagentSpawnRequestAllowsSessionModeThreadContext(t *testing.T) {
	if err := ValidateSubagentSpawnRequest(SubagentSpawnRequest{
		ThreadRequested: true,
		SpawnMode:       "session",
		RouteThreadID:   "thread-1",
	}); err != nil {
		t.Fatalf("expected session-mode thread request to pass, got %v", err)
	}
}
