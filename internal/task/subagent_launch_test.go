package task

import (
	"testing"

	"github.com/kocort/kocort/internal/core"
)

func TestPrepareSubagentLaunch(t *testing.T) {
	identity := &core.AgentIdentity{
		ID:                   "worker",
		DefaultProvider:      "openai",
		WorkspaceDir:         "/tmp/worker",
		SubagentModelPrimary: "openai/gpt-5.4-mini",
	}
	plan, err := PrepareSubagentLaunch(
		SubagentSpawnRequest{
			RequesterSessionKey: "agent:main:main",
			RequesterAgentID:    "main",
			TargetAgentID:       "worker",
			Task:                "do it",
			Thinking:            "high",
			RunTimeoutSeconds:   30,
			MaxSpawnDepth:       5,
			ModelOverride:       "openai/gpt-5.4",
		},
		SubagentSpawnResult{
			RunID:           "run-1",
			ChildSessionKey: "agent:worker:subagent:abc",
			SpawnDepth:      1,
			WorkspaceDir:    "/tmp/from-spawn",
		},
		identity,
		nil,
	)
	if err != nil {
		t.Fatalf("prepare launch: %v", err)
	}
	if plan.ChildRequest.AgentID != "worker" || plan.ChildRequest.SessionKey != "agent:worker:subagent:abc" {
		t.Fatalf("unexpected child request: %+v", plan.ChildRequest)
	}
	if plan.ChildRequest.SessionProviderOverride != "openai" || plan.ChildRequest.SessionModelOverride != "gpt-5.4" {
		t.Fatalf("unexpected model override: %+v", plan.ChildRequest)
	}
	if plan.ChildRequest.WorkspaceOverride != "/tmp/from-spawn" {
		t.Fatalf("unexpected workspace override: %q", plan.ChildRequest.WorkspaceOverride)
	}
	if plan.SessionEntry.SpawnedBy != "agent:main:main" || plan.SessionEntry.SpawnDepth != 1 {
		t.Fatalf("unexpected session entry: %+v", plan.SessionEntry)
	}
}

func TestPrepareSubagentLaunchFallsBackToTargetDefaults(t *testing.T) {
	identity := &core.AgentIdentity{
		ID:                   "worker",
		DefaultProvider:      "openai",
		WorkspaceDir:         "/tmp/worker",
		SubagentModelPrimary: "gpt-5.4-mini",
	}
	plan, err := PrepareSubagentLaunch(
		SubagentSpawnRequest{
			RequesterSessionKey: "agent:main:main",
			RequesterAgentID:    "main",
			Task:                "do it",
		},
		SubagentSpawnResult{
			RunID:           "run-1",
			ChildSessionKey: "agent:main:subagent:abc",
			SpawnDepth:      1,
		},
		identity,
		&core.SessionEntry{SessionID: "sess-1"},
	)
	if err != nil {
		t.Fatalf("prepare launch: %v", err)
	}
	if plan.ChildRequest.SessionProviderOverride != "openai" || plan.ChildRequest.SessionModelOverride != "gpt-5.4-mini" {
		t.Fatalf("unexpected fallback model override: %+v", plan.ChildRequest)
	}
	if plan.ChildRequest.WorkspaceOverride != "/tmp/worker" {
		t.Fatalf("unexpected workspace fallback: %q", plan.ChildRequest.WorkspaceOverride)
	}
	if plan.SessionEntry.SessionID != "sess-1" {
		t.Fatalf("expected existing session id preserved, got %+v", plan.SessionEntry)
	}
}

func TestPrepareSubagentLaunchPersistsRouteBinding(t *testing.T) {
	plan, err := PrepareSubagentLaunch(
		SubagentSpawnRequest{
			RequesterSessionKey: "agent:main:main",
			RequesterAgentID:    "main",
			Task:                "do it",
			RouteChannel:        "slack",
			RouteTo:             "room-1",
			RouteAccountID:      "bot-1",
			RouteThreadID:       "thread-9",
		},
		SubagentSpawnResult{
			RunID:           "run-1",
			ChildSessionKey: "agent:main:subagent:abc",
			SpawnDepth:      1,
		},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("prepare launch: %v", err)
	}
	if plan.ChildRequest.Channel != "slack" || plan.ChildRequest.ThreadID != "thread-9" {
		t.Fatalf("unexpected child route binding: %+v", plan.ChildRequest)
	}
	if plan.SessionEntry.DeliveryContext == nil || plan.SessionEntry.DeliveryContext.ThreadID != "thread-9" {
		t.Fatalf("expected session delivery context, got %+v", plan.SessionEntry)
	}
}

func TestPrepareSubagentLaunchPersistsSpawnMode(t *testing.T) {
	plan, err := PrepareSubagentLaunch(
		SubagentSpawnRequest{
			RequesterSessionKey: "agent:main:main",
			RequesterAgentID:    "main",
			Task:                "do it",
			SpawnMode:           "session",
			ThreadRequested:     true,
			RouteThreadID:       "thread-9",
		},
		SubagentSpawnResult{
			RunID:           "run-1",
			ChildSessionKey: "agent:main:subagent:abc",
			SpawnDepth:      1,
		},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("prepare launch: %v", err)
	}
	if plan.SessionEntry.SpawnMode != "session" {
		t.Fatalf("expected spawn mode persisted, got %+v", plan.SessionEntry)
	}
}

func TestPrepareSubagentLaunchMaterializesAttachments(t *testing.T) {
	workspace := t.TempDir()
	plan, err := PrepareSubagentLaunch(
		SubagentSpawnRequest{
			RequesterSessionKey:     "agent:main:main",
			RequesterAgentID:        "main",
			Task:                    "do it",
			WorkspaceDir:            workspace,
			Attachments:             []SubagentInlineAttachment{{Name: "a.txt", Content: "hello"}},
			AttachMountPath:         "docs",
			RetainAttachmentsOnKeep: true,
		},
		SubagentSpawnResult{
			RunID:           "run-1",
			ChildSessionKey: "agent:main:subagent:abc",
			SpawnDepth:      1,
			WorkspaceDir:    workspace,
		},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("prepare launch with attachments: %v", err)
	}
	if plan.AttachmentReceipt == nil || plan.AttachmentReceipt.Count != 1 {
		t.Fatalf("expected attachment receipt, got %+v", plan.AttachmentReceipt)
	}
	if plan.AttachmentsDir == "" || plan.AttachmentsRootDir == "" {
		t.Fatalf("expected attachment dirs, got %+v", plan)
	}
	if plan.ChildRequest.ExtraSystemPrompt == "" {
		t.Fatalf("expected extra system prompt for attachments, got %+v", plan.ChildRequest)
	}
	if !plan.RetainAttachmentsOnKeep {
		t.Fatalf("expected retain-on-keep policy to propagate, got %+v", plan)
	}
}
