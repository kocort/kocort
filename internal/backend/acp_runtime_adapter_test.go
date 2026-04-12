package backend

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
)

func TestACPClientRuntimeRoundTrip(t *testing.T) {
	helperReadPath := filepath.Join(t.TempDir(), "context.txt")
	if err := os.WriteFile(helperReadPath, []byte("runtime helper context"), 0o644); err != nil {
		t.Fatalf("write helper context: %v", err)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	runtime := NewACPClientRuntime(config.AppConfig{}, nil, "acp-test", core.CommandBackendConfig{
		Command: exe,
		Args:    []string{"-test.run=TestACPClientRuntimeHelperProcess", "--"},
		Env: map[string]string{
			"GO_WANT_HELPER_PROCESS": "1",
			"ACP_HELPER_READ_PATH":   helperReadPath,
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	handle, err := runtime.EnsureSession(ctx, core.AcpEnsureSessionInput{
		SessionKey:      "agent:main:test",
		Agent:           "main",
		Mode:            core.AcpSessionModePersistent,
		ResumeSessionID: "resume-session",
		Cwd:             t.TempDir(),
	})
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if handle.AgentSessionID != "resume-session" {
		t.Fatalf("expected resumed session id, got %q", handle.AgentSessionID)
	}
	defer func() {
		_ = runtime.Close(context.Background(), core.AcpCloseInput{Handle: handle, Reason: "test-cleanup"})
	}()

	if err := runtime.SetMode(ctx, core.AcpSetModeInput{Handle: handle, Mode: "review"}); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if err := runtime.SetConfigOption(ctx, core.AcpSetConfigOptionInput{Handle: handle, Key: "model", Value: "gpt-test"}); err != nil {
		t.Fatalf("SetConfigOption(model): %v", err)
	}
	if err := runtime.SetConfigOption(ctx, core.AcpSetConfigOptionInput{Handle: handle, Key: "approval_policy", Value: "on-request"}); err != nil {
		t.Fatalf("SetConfigOption(approval_policy): %v", err)
	}

	var events []core.AcpRuntimeEvent
	err = runtime.RunTurn(ctx, core.AcpRunTurnInput{
		Handle: handle,
		Text:   "hello",
		OnEvent: func(event core.AcpRuntimeEvent) error {
			events = append(events, event)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	var sawThought, sawOutput, sawDone bool
	for _, event := range events {
		switch event.Type {
		case "text_delta":
			if event.Stream == "thought" && strings.Contains(event.Text, "pondering") {
				sawThought = true
			}
			if event.Stream == "output" && strings.Contains(event.Text, "runtime helper context") {
				sawOutput = true
			}
		case "done":
			if event.StopReason == string(acp.StopReasonEndTurn) {
				sawDone = true
			}
		}
	}
	if !sawThought || !sawOutput || !sawDone {
		t.Fatalf("unexpected events: %+v", events)
	}

	status, err := runtime.GetStatus(ctx, handle)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if got := status.Details["currentMode"]; got != "review" {
		t.Fatalf("expected currentMode review, got %#v", got)
	}
	if got := status.Details["currentModel"]; got != "gpt-test" {
		t.Fatalf("expected currentModel gpt-test, got %#v", got)
	}

	if err := runtime.Cancel(ctx, core.AcpCancelInput{Handle: handle, Reason: "test"}); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
}

func TestACPClientRuntimeHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	helper := &acpRuntimeHelper{
		sessionID:    "resume-session",
		currentMode:  "default",
		currentModel: "helper-model",
		config:       map[string]string{},
		readPath:     os.Getenv("ACP_HELPER_READ_PATH"),
	}
	conn := acp.NewConnection(helper.handle, os.Stdout, os.Stdin)
	helper.conn = conn
	<-conn.Done()
	os.Exit(0)
}

type acpRuntimeHelper struct {
	conn         *acp.Connection
	sessionID    string
	currentMode  string
	currentModel string
	config       map[string]string
	readPath     string
}

func (h *acpRuntimeHelper) handle(ctx context.Context, method string, params json.RawMessage) (any, *acp.RequestError) {
	switch method {
	case acp.AgentMethodInitialize:
		return acp.InitializeResponse{
			ProtocolVersion: acp.ProtocolVersionNumber,
			AgentCapabilities: acp.AgentCapabilities{
				LoadSession: true,
			},
			AuthMethods: []acp.AuthMethod{},
		}, nil
	case acp.AgentMethodSessionLoad:
		var req acp.LoadSessionRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, acp.NewInvalidParams(map[string]any{"error": err.Error()})
		}
		h.sessionID = string(req.SessionId)
		return acp.LoadSessionResponse{
			Modes:  helperModes(h.currentMode),
			Models: helperModels(h.currentModel),
		}, nil
	case acp.AgentMethodSessionNew:
		h.sessionID = "new-session"
		return acp.NewSessionResponse{
			SessionId: acp.SessionId(h.sessionID),
			Modes:     helperModes(h.currentMode),
			Models:    helperModels(h.currentModel),
		}, nil
	case acp.AgentMethodSessionSetMode:
		var req acp.SetSessionModeRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, acp.NewInvalidParams(map[string]any{"error": err.Error()})
		}
		h.currentMode = string(req.ModeId)
		_ = h.conn.SendNotification(ctx, acp.ClientMethodSessionUpdate, acp.SessionNotification{
			SessionId: req.SessionId,
			Update: acp.SessionUpdate{
				CurrentModeUpdate: &acp.SessionCurrentModeUpdate{
					SessionUpdate: "current_mode_update",
					CurrentModeId: req.ModeId,
				},
			},
		})
		return acp.SetSessionModeResponse{}, nil
	case acp.AgentMethodSessionSetModel:
		var req acp.SetSessionModelRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, acp.NewInvalidParams(map[string]any{"error": err.Error()})
		}
		h.currentModel = string(req.ModelId)
		return acp.SetSessionModelResponse{}, nil
	case acpMethodSessionSetConfigOption:
		var req map[string]any
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, acp.NewInvalidParams(map[string]any{"error": err.Error()})
		}
		key, _ := req["key"].(string)
		value, _ := req["value"].(string)
		h.config[key] = value
		return nil, nil
	case acp.AgentMethodSessionPrompt:
		var req acp.PromptRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, acp.NewInvalidParams(map[string]any{"error": err.Error()})
		}
		_ = h.conn.SendNotification(ctx, acp.ClientMethodSessionUpdate, acp.SessionNotification{
			SessionId: req.SessionId,
			Update: acp.SessionUpdate{
				AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
					SessionUpdate: "agent_thought_chunk",
					Content:       acp.TextBlock("pondering"),
				},
			},
		})
		if h.readPath != "" {
			data, err := os.ReadFile(h.readPath)
			if err == nil {
				_ = h.conn.SendNotification(ctx, acp.ClientMethodSessionUpdate, acp.SessionNotification{
					SessionId: req.SessionId,
					Update: acp.SessionUpdate{
						AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
							SessionUpdate: "agent_message_chunk",
							Content:       acp.TextBlock(string(data)),
						},
					},
				})
			}
		}
		_ = h.conn.SendNotification(ctx, acp.ClientMethodSessionUpdate, acp.SessionNotification{
			SessionId: req.SessionId,
			Update: acp.SessionUpdate{
				ToolCall: &acp.SessionUpdateToolCall{
					SessionUpdate: "tool_call",
					ToolCallId:    "tool-1",
					Title:         "helper tool",
					Status:        acp.ToolCallStatusInProgress,
				},
			},
		})
		completed := acp.ToolCallStatusCompleted
		_ = h.conn.SendNotification(ctx, acp.ClientMethodSessionUpdate, acp.SessionNotification{
			SessionId: req.SessionId,
			Update: acp.SessionUpdate{
				ToolCallUpdate: &acp.SessionToolCallUpdate{
					SessionUpdate: "tool_call_update",
					ToolCallId:    "tool-1",
					Status:        &completed,
				},
			},
		})
		return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
	case acp.AgentMethodSessionCancel:
		return nil, nil
	default:
		return nil, acp.NewMethodNotFound(method)
	}
}

func helperModes(current string) *acp.SessionModeState {
	return &acp.SessionModeState{
		AvailableModes: []acp.SessionMode{
			{Id: "default", Name: "Default"},
			{Id: "review", Name: "Review"},
		},
		CurrentModeId: acp.SessionModeId(current),
	}
}

func helperModels(current string) *acp.SessionModelState {
	return &acp.SessionModelState{
		AvailableModels: []acp.ModelInfo{
			{ModelId: "helper-model", Name: "Helper Model"},
			{ModelId: "gpt-test", Name: "GPT Test"},
		},
		CurrentModelId: acp.ModelId(current),
	}
}

func TestChoosePermissionOptionAutoApprovesScopedRead(t *testing.T) {
	cwd := t.TempDir()
	title := "read: notes/todo.txt"
	req := acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{
			{Kind: acp.PermissionOptionKindAllowOnce, OptionId: "allow"},
			{Kind: acp.PermissionOptionKindRejectOnce, OptionId: "reject"},
		},
		ToolCall: acp.RequestPermissionToolCall{
			Title: &title,
		},
	}
	resp := choosePermissionOption(req, cwd)
	if resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "allow" {
		t.Fatalf("expected scoped read to auto-approve, got %+v", resp)
	}
}

func TestChoosePermissionOptionRejectsUnsafeToolByDefault(t *testing.T) {
	title := "write: ../secrets.txt"
	req := acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{
			{Kind: acp.PermissionOptionKindAllowOnce, OptionId: "allow"},
			{Kind: acp.PermissionOptionKindRejectOnce, OptionId: "reject"},
		},
		ToolCall: acp.RequestPermissionToolCall{
			Title: &title,
			RawInput: map[string]any{
				"path": "../secrets.txt",
				"name": "write",
			},
		},
	}
	resp := choosePermissionOption(req, t.TempDir())
	if resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "reject" {
		t.Fatalf("expected unsafe tool to reject, got %+v", resp)
	}
}
