package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/tool"
)

func TestExecToolBackgroundCanBeListedAndKilled(t *testing.T) {
	runtime := &Runtime{
		Tools:     tool.NewToolRegistry(tool.NewExecTool(), tool.NewProcessTool()),
		Processes: tool.NewProcessRegistry(),
	}
	runCtx := rtypes.AgentRunContext{
		Runtime:      runtime,
		WorkspaceDir: t.TempDir(),
		Identity: core.AgentIdentity{
			ID:          session.DefaultAgentID,
			ToolProfile: "coding",
		},
		Request: core.AgentRunRequest{
			RunID: "run_background_kill",
		},
		Session: core.SessionResolution{
			SessionKey: session.BuildMainSessionKey(session.DefaultAgentID),
		},
	}

	result, err := runtime.ExecuteTool(context.Background(), runCtx, "exec", map[string]any{
		"command":    "sleep 2",
		"background": true,
	})
	if err != nil {
		t.Fatalf("exec background: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(result.JSON, &payload); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	sessionID, _ := payload["sessionId"].(string)
	if strings.TrimSpace(sessionID) == "" {
		t.Fatalf("expected background sessionId, got %+v", payload)
	}

	listResult, err := runtime.ExecuteTool(context.Background(), runCtx, "process", map[string]any{
		"action": "list",
	})
	if err != nil {
		t.Fatalf("process list: %v", err)
	}
	if !strings.Contains(string(listResult.JSON), sessionID) {
		t.Fatalf("expected listed session %q in %s", sessionID, string(listResult.JSON))
	}

	killResult, err := runtime.ExecuteTool(context.Background(), runCtx, "process", map[string]any{
		"action":    "kill",
		"sessionId": sessionID,
	})
	if err != nil {
		t.Fatalf("process kill: %v", err)
	}
	if !strings.Contains(string(killResult.JSON), "\"status\":\"killed\"") &&
		!strings.Contains(string(killResult.JSON), "\"status\":\"failed\"") {
		pollResult, pollErr := runtime.ExecuteTool(context.Background(), runCtx, "process", map[string]any{
			"action":    "poll",
			"sessionId": sessionID,
			"timeout":   500.0,
		})
		if pollErr != nil {
			t.Fatalf("poll after kill: %v", pollErr)
		}
		if !strings.Contains(string(pollResult.JSON), "\"status\":\"killed\"") {
			t.Fatalf("expected killed process status, got %s", string(pollResult.JSON))
		}
	}
}

func TestExecToolYieldMsBackgroundsAndProcessPollCompletes(t *testing.T) {
	runtime := &Runtime{
		Tools:     tool.NewToolRegistry(tool.NewExecTool(), tool.NewProcessTool()),
		Processes: tool.NewProcessRegistry(),
	}
	runCtx := rtypes.AgentRunContext{
		Runtime:      runtime,
		WorkspaceDir: t.TempDir(),
		Identity: core.AgentIdentity{
			ID:          session.DefaultAgentID,
			ToolProfile: "coding",
		},
		Request: core.AgentRunRequest{
			RunID: "run_yield_background",
		},
		Session: core.SessionResolution{
			SessionKey: session.BuildMainSessionKey(session.DefaultAgentID),
		},
	}

	result, err := runtime.ExecuteTool(context.Background(), runCtx, "exec", map[string]any{
		"command": "printf start; sleep 1; printf end",
		"yieldMs": 10.0,
	})
	if err != nil {
		t.Fatalf("exec with yieldMs: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(result.JSON, &payload); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	sessionID, _ := payload["sessionId"].(string)
	if strings.TrimSpace(sessionID) == "" {
		t.Fatalf("expected yielded background sessionId, got %+v", payload)
	}

	pollResult, err := runtime.ExecuteTool(context.Background(), runCtx, "process", map[string]any{
		"action":    "poll",
		"sessionId": sessionID,
		"timeout":   float64((2500 * time.Millisecond) / time.Millisecond),
	})
	if err != nil {
		t.Fatalf("process poll: %v", err)
	}
	text := string(pollResult.JSON)
	if strings.Contains(text, "\"status\":\"running\"") {
		pollResult, err = runtime.ExecuteTool(context.Background(), runCtx, "process", map[string]any{
			"action":    "poll",
			"sessionId": sessionID,
			"timeout":   float64((2500 * time.Millisecond) / time.Millisecond),
		})
		if err != nil {
			t.Fatalf("process poll retry: %v", err)
		}
		text = string(pollResult.JSON)
	}
	if !strings.Contains(text, "\"status\":\"completed\"") {
		t.Fatalf("expected completed status, got %s", text)
	}
	if !strings.Contains(text, "start") || !strings.Contains(text, "end") {
		t.Fatalf("expected captured output in %s", text)
	}
}

func TestExecToolUsesDefaultBackgroundWindowWhenYieldNotSpecified(t *testing.T) {
	allowBackground := true
	runtime := &Runtime{
		Tools: tool.NewToolRegistry(tool.NewExecTool(&config.ToolExecConfig{
			AllowBackground: &allowBackground,
			BackgroundMs:    10,
			TimeoutSec:      60,
		}), tool.NewProcessTool()),
		Processes: tool.NewProcessRegistry(),
	}
	runCtx := rtypes.AgentRunContext{
		Runtime:      runtime,
		WorkspaceDir: t.TempDir(),
		Identity: core.AgentIdentity{
			ID:          session.DefaultAgentID,
			ToolProfile: "coding",
		},
		Request: core.AgentRunRequest{
			RunID: "run_default_background",
		},
		Session: core.SessionResolution{
			SessionKey: session.BuildMainSessionKey(session.DefaultAgentID),
		},
	}

	result, err := runtime.ExecuteTool(context.Background(), runCtx, "exec", map[string]any{
		"command": "printf alpha; sleep 1; printf omega",
	})
	if err != nil {
		t.Fatalf("exec default background window: %v", err)
	}
	if !strings.Contains(string(result.JSON), "\"backgrounded\":true") {
		t.Fatalf("expected backgrounded exec result, got %s", string(result.JSON))
	}
	var payload map[string]any
	if err := json.Unmarshal(result.JSON, &payload); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	sessionID, _ := payload["sessionId"].(string)
	if strings.TrimSpace(sessionID) == "" {
		t.Fatalf("expected sessionId in background result, got %+v", payload)
	}
	pollResult, err := runtime.ExecuteTool(context.Background(), runCtx, "process", map[string]any{
		"action":    "poll",
		"sessionId": sessionID,
		"timeout":   float64((2500 * time.Millisecond) / time.Millisecond),
	})
	if err != nil {
		t.Fatalf("process poll: %v", err)
	}
	text := string(pollResult.JSON)
	if !strings.Contains(text, "\"status\":\"completed\"") {
		t.Fatalf("expected completed status, got %s", text)
	}
	if !strings.Contains(text, "alpha") || !strings.Contains(text, "omega") {
		t.Fatalf("expected captured output, got %s", text)
	}
}
