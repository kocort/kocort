package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kocort/kocort/internal/core"
)

func TestSessionsYieldTool_Name(t *testing.T) {
	tool := NewSessionsYieldTool()
	if tool.Name() != "sessions_yield" {
		t.Fatalf("expected name sessions_yield, got %s", tool.Name())
	}
}

func TestSessionsYieldTool_Schema(t *testing.T) {
	tool := NewSessionsYieldTool()
	schema := tool.OpenAIFunctionTool()
	if schema.Name != "sessions_yield" {
		t.Fatalf("schema name: %s", schema.Name)
	}
	props, ok := schema.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatal("missing properties in schema")
	}
	if _, ok := props["message"]; !ok {
		t.Fatal("missing message parameter in schema")
	}
}

func TestSessionsYieldTool_SetsYielded(t *testing.T) {
	tool := NewSessionsYieldTool()
	state := &core.AgentRunState{}
	result, err := tool.Execute(context.Background(), ToolContext{
		Run: AgentRunContext{
			Session:  core.SessionResolution{SessionKey: "agent:main:main"},
			RunState: state,
		},
	}, map[string]any{
		"message": "waiting for child agents",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !state.Yielded {
		t.Fatal("expected RunState.Yielded to be true")
	}
	if state.YieldMessage != "waiting for child agents" {
		t.Fatalf("unexpected yield message: %s", state.YieldMessage)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(result.Text), &m); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if m["status"] != "yielded" {
		t.Fatalf("expected status=yielded, got %v", m["status"])
	}
}

func TestSessionsYieldTool_DefaultMessage(t *testing.T) {
	tool := NewSessionsYieldTool()
	state := &core.AgentRunState{}
	_, err := tool.Execute(context.Background(), ToolContext{
		Run: AgentRunContext{
			Session:  core.SessionResolution{SessionKey: "agent:main:main"},
			RunState: state,
		},
	}, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.YieldMessage != "Turn yielded." {
		t.Fatalf("expected default yield message, got %q", state.YieldMessage)
	}
}

func TestSessionsYieldTool_NoSession(t *testing.T) {
	tool := NewSessionsYieldTool()
	state := &core.AgentRunState{}
	result, err := tool.Execute(context.Background(), ToolContext{
		Run: AgentRunContext{
			RunState: state,
		},
	}, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(result.Text), &m); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if m["status"] != "error" {
		t.Fatalf("expected status=error for missing session, got %v", m["status"])
	}
	if state.Yielded {
		t.Fatal("should not set Yielded when no session")
	}
}

func TestSessionsYieldTool_NilRunState(t *testing.T) {
	tool := NewSessionsYieldTool()
	result, err := tool.Execute(context.Background(), ToolContext{
		Run: AgentRunContext{
			Session: core.SessionResolution{SessionKey: "agent:main:main"},
		},
	}, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should still return success even if RunState is nil (no-op on state)
	var m map[string]any
	if err := json.Unmarshal([]byte(result.Text), &m); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if m["status"] != "yielded" {
		t.Fatalf("expected status=yielded, got %v", m["status"])
	}
}
