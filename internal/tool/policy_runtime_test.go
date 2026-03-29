package tool

import (
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"

	"github.com/kocort/kocort/utils"
)

func TestToolPolicyProfileAllowsSessionsSpawn(t *testing.T) {
	identity := core.AgentIdentity{
		ID:          "main",
		ToolProfile: "coding",
	}
	runCtx := AgentRunContext{
		Request: core.AgentRunRequest{},
		Session: core.SessionResolution{SessionKey: session.BuildMainSessionKey("main")},
	}
	if !IsToolAllowedByIdentity(identity, runCtx, core.ToolRegistrationMeta{}, "sessions_spawn") {
		t.Fatal("expected sessions_spawn allowed by coding profile")
	}
}

func TestToolPolicyProfileAllowsSessionsSend(t *testing.T) {
	identity := core.AgentIdentity{
		ID:          "main",
		ToolProfile: "coding",
	}
	runCtx := AgentRunContext{
		Request: core.AgentRunRequest{},
		Session: core.SessionResolution{SessionKey: session.BuildMainSessionKey("main")},
	}
	if !IsToolAllowedByIdentity(identity, runCtx, core.ToolRegistrationMeta{}, "sessions_send") {
		t.Fatal("expected sessions_send allowed by coding profile")
	}
}

func TestToolPolicyProfileAllowsMemorySearch(t *testing.T) {
	identity := core.AgentIdentity{
		ID:          "main",
		ToolProfile: "coding",
	}
	runCtx := AgentRunContext{
		Request: core.AgentRunRequest{},
		Session: core.SessionResolution{SessionKey: session.BuildMainSessionKey("main")},
	}
	if !IsToolAllowedByIdentity(identity, runCtx, core.ToolRegistrationMeta{}, "memory_search") {
		t.Fatal("expected memory_search allowed by coding profile")
	}
	if !IsToolAllowedByIdentity(identity, runCtx, core.ToolRegistrationMeta{}, "memory_get") {
		t.Fatal("expected memory_get allowed by coding profile")
	}
	if !IsToolAllowedByIdentity(identity, runCtx, core.ToolRegistrationMeta{}, "session_status") {
		t.Fatal("expected session_status allowed by coding profile")
	}
	if !IsToolAllowedByIdentity(identity, runCtx, core.ToolRegistrationMeta{}, "subagents") {
		t.Fatal("expected subagents allowed by coding profile")
	}
}

func TestDetectToolCallLoopBlocksPingPongNoProgressAtCriticalThreshold(t *testing.T) {
	state := &ToolLoopSessionState{}
	cfg := core.ToolLoopDetectionConfig{
		Enabled:           utils.BoolPtr(true),
		HistorySize:       30,
		WarningThreshold:  2,
		CriticalThreshold: 4,
		Detectors: core.ToolLoopDetectionDetectorConfig{
			PingPong: utils.BoolPtr(true),
		},
	}
	RecordToolCall(state, "step_a", map[string]any{"value": "a"}, "call_a1", cfg)
	RecordToolCallOutcome(state, "step_a", map[string]any{"value": "a"}, "call_a1", core.ToolResult{Text: "same-a"}, nil, cfg)
	RecordToolCall(state, "step_b", map[string]any{"value": "b"}, "call_b1", cfg)
	RecordToolCallOutcome(state, "step_b", map[string]any{"value": "b"}, "call_b1", core.ToolResult{Text: "same-b"}, nil, cfg)
	RecordToolCall(state, "step_a", map[string]any{"value": "a"}, "call_a2", cfg)
	RecordToolCallOutcome(state, "step_a", map[string]any{"value": "a"}, "call_a2", core.ToolResult{Text: "same-a"}, nil, cfg)

	result := DetectToolCallLoop(state, "step_b", map[string]any{"value": "b"}, cfg)
	if !result.Stuck || result.Level != "critical" {
		t.Fatalf("expected critical ping-pong detection, got %+v", result)
	}
	if result.Detector != ToolLoopDetectorPingPong {
		t.Fatalf("expected ping-pong detector, got %+v", result)
	}
	if !strings.Contains(strings.ToLower(result.Message), "ping-pong loop") {
		t.Fatalf("expected ping-pong message, got %q", result.Message)
	}
}

func TestSandboxSessionVisibilityBlocksSessionToolsOutsideSpawnedTree(t *testing.T) {
	runCtx := AgentRunContext{
		Request: core.AgentRunRequest{Channel: "test", To: "user"},
		Session: core.SessionResolution{SessionKey: session.BuildMainSessionKey("main"), SessionID: "sess_main"},
		Identity: core.AgentIdentity{
			ID:                       "main",
			SandboxMode:              "all",
			SandboxWorkspaceAccess:   "ro",
			SandboxSessionVisibility: "spawned",
		},
	}
	sandbox := &SandboxContext{Enabled: true, Mode: "all", WorkspaceAccess: "ro"}
	if IsToolAllowedInSandbox(runCtx.Identity, runCtx, core.ToolRegistrationMeta{}, "sessions_history", sandbox) {
		t.Fatal("expected sessions_history blocked in sandbox for non-subagent session")
	}
	if !IsToolAllowedInSandbox(runCtx.Identity, runCtx, core.ToolRegistrationMeta{}, "exec", sandbox) {
		t.Fatal("expected exec allowed in sandbox")
	}
	runCtx.Request.Lane = core.LaneSubagent
	runCtx.Session.SessionKey = "agent:main:subagent:child"
	if !IsToolAllowedInSandbox(runCtx.Identity, runCtx, core.ToolRegistrationMeta{}, "sessions_history", sandbox) {
		t.Fatal("expected sessions_history allowed in spawned tree")
	}
}
