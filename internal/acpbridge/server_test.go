package acpbridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/gateway"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/runtime"
)

func TestFlattenACPPrompt(t *testing.T) {
	imageData := base64.StdEncoding.EncodeToString([]byte("img"))
	text, attachments, err := flattenACPPrompt([]acpsdk.ContentBlock{
		acpsdk.TextBlock("hello"),
		acpsdk.ResourceLinkBlock("doc", "https://example.com"),
		acpsdk.ImageBlock(imageData, "image/png"),
	})
	if err != nil {
		t.Fatalf("flatten prompt: %v", err)
	}
	if text != "hello\n[Resource link (doc)] https://example.com" {
		t.Fatalf("unexpected prompt text: %q", text)
	}
	if len(attachments) != 1 || attachments[0].MIMEType != "image/png" {
		t.Fatalf("unexpected attachments: %+v", attachments)
	}
}

func TestFlattenACPPromptRejectsOversizedPrompt(t *testing.T) {
	huge := strings.Repeat("a", acpMaxPromptBytes+1)
	_, _, err := flattenACPPrompt([]acpsdk.ContentBlock{acpsdk.TextBlock(huge)})
	if err == nil {
		t.Fatal("expected oversized prompt error")
	}
}

func TestBuildToolTitleFormatsStructuredArgs(t *testing.T) {
	title := buildToolTitle(map[string]any{
		"toolName": "exec",
		"args": map[string]any{
			"command": "echo hello",
			"timeout": 30,
		},
	})
	if !strings.Contains(title, "exec:") || !strings.Contains(title, "command:") || !strings.Contains(title, "timeout:") {
		t.Fatalf("unexpected title: %q", title)
	}
}

func TestExtractToolLocationsFindsNestedPathsAndLines(t *testing.T) {
	locations := extractToolLocations(map[string]any{
		"args": map[string]any{
			"targetPath": "C:/tmp/out.txt",
			"startLine":  12,
			"nested": map[string]any{
				"source_path": "C:/tmp/in.txt",
			},
		},
	})
	if len(locations) != 2 {
		t.Fatalf("expected 2 locations, got %+v", locations)
	}
}

func TestACPBridgeConnectionSmoke(t *testing.T) {
	store, err := session.NewSessionStore(t.TempDir())
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	rt := &runtime.Runtime{
		Config: config.AppConfig{
			Models: config.ModelsConfig{
				Providers: map[string]config.ProviderConfig{
					"openai": {
						Models: []config.ProviderModelConfig{
							{ID: "gpt-5.4", Name: "GPT-5.4"},
						},
					},
				},
			},
		},
		Sessions: store,
		EventHub: gateway.NewEventHub(),
	}

	c2aR, c2aW := io.Pipe()
	a2cR, a2cW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = ServeACPBridge(ctx, rt, ACPBridgeOptions{PrefixCwd: true, ProvenanceMode: "meta+receipt"}, a2cW, c2aR)
	}()

	updates := make(chan map[string]any, 16)
	clientConn := acpsdk.NewConnection(func(_ context.Context, method string, params json.RawMessage) (any, *acpsdk.RequestError) {
		if method == acpsdk.ClientMethodSessionUpdate {
			var payload map[string]any
			if err := json.Unmarshal(params, &payload); err == nil {
				updates <- payload
			}
			return nil, nil
		}
		return nil, acpsdk.NewMethodNotFound(method)
	}, c2aW, a2cR)

	initResp, err := acpsdk.SendRequest[map[string]any](clientConn, context.Background(), acpsdk.AgentMethodInitialize, acpsdk.InitializeRequest{})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	agentCaps, _ := initResp["agentCapabilities"].(map[string]any)
	if load, _ := agentCaps["loadSession"].(bool); !load {
		t.Fatal("expected loadSession capability")
	}
	if _, ok := agentCaps["sessionCapabilities"].(map[string]any); !ok {
		t.Fatalf("expected sessionCapabilities in initialize response: %+v", initResp)
	}

	newResp, err := acpsdk.SendRequest[map[string]any](clientConn, context.Background(), acpsdk.AgentMethodSessionNew, acpsdk.NewSessionRequest{Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	sessionID, _ := newResp["sessionId"].(string)
	if sessionID == "" || newResp["modes"] == nil || newResp["configOptions"] == nil {
		t.Fatalf("unexpected new session response: %+v", newResp)
	}
	configOptions, _ := newResp["configOptions"].([]any)
	if got := findConfigOptionCurrentValue(configOptions, "reasoning_level"); got != "off" {
		t.Fatalf("expected reasoning_level default off, got %q", got)
	}
	if got := findConfigOptionCurrentValue(configOptions, "response_usage"); got != "off" {
		t.Fatalf("expected response_usage default off, got %q", got)
	}

	if _, err := acpsdk.SendRequest[acpSetConfigOptionResponse](clientConn, context.Background(), acpMethodSessionSetConfigOption, acpSetConfigOptionRequest{
		SessionID: acpsdk.SessionId(sessionID),
		ConfigID:  "thought_level",
		Value:     "high",
	}); err != nil {
		t.Fatalf("set config option: %v", err)
	}

	if _, err := acpsdk.SendRequest[acpsdk.SetSessionModeResponse](clientConn, context.Background(), acpsdk.AgentMethodSessionSetMode, acpsdk.SetSessionModeRequest{
		SessionId: acpsdk.SessionId(sessionID),
		ModeId:    "medium",
	}); err != nil {
		t.Fatalf("set session mode: %v", err)
	}

	deadline := time.After(3 * time.Second)
	sawAvailable := false
	sawCurrentMode := false
	for !(sawAvailable && sawCurrentMode) {
		select {
		case payload := <-updates:
			update, _ := payload["update"].(map[string]any)
			switch update["sessionUpdate"] {
			case "available_commands_update":
				sawAvailable = true
			case "current_mode_update":
				sawCurrentMode = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for session updates: available=%v currentMode=%v", sawAvailable, sawCurrentMode)
		}
	}

	_ = c2aW.Close()
	_ = a2cW.Close()
}

func TestMapStopReason(t *testing.T) {
	tests := map[string]acpsdk.StopReason{
		"aborted":    acpsdk.StopReasonCancelled,
		"cancelled":  acpsdk.StopReasonCancelled,
		"max_tokens": acpsdk.StopReasonMaxTokens,
		"stop":       acpsdk.StopReasonEndTurn,
	}
	for input, want := range tests {
		if got := mapStopReason(input); got != want {
			t.Fatalf("mapStopReason(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNewSessionUsesPersistedThinkingLevel(t *testing.T) {
	store, err := session.NewSessionStore(t.TempDir())
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	if err := store.Upsert("existing-session", core.SessionEntry{
		SessionID:        "sess_existing",
		Label:            "Existing",
		ThinkingLevel:    "high",
		VerboseLevel:     "full",
		ModelOverride:    "gpt-5.4",
		ProviderOverride: "openai",
		UpdatedAt:        time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	rt := &runtime.Runtime{
		Config: config.AppConfig{
			Models: config.ModelsConfig{
				Providers: map[string]config.ProviderConfig{
					"openai": {Models: []config.ProviderModelConfig{{ID: "gpt-5.4", Name: "GPT-5.4"}}},
				},
			},
		},
		Sessions: store,
		EventHub: gateway.NewEventHub(),
	}
	server := NewACPBridgeServer(rt, ACPBridgeOptions{PrefixCwd: true})
	resp, reqErr := server.newSession(context.Background(), acpsdk.NewSessionRequest{
		Cwd: t.TempDir(),
		Meta: map[string]any{
			"sessionKey": "existing-session",
		},
	})
	if reqErr != nil {
		t.Fatalf("newSession error: %+v", reqErr)
	}
	modes, _ := resp["modes"].(*acpsdk.SessionModeState)
	if modes == nil || string(modes.CurrentModeId) != "high" {
		t.Fatalf("expected persisted current mode high, got %+v", modes)
	}
}

func TestSetSessionConfigOptionPersistsGatewaySessionFields(t *testing.T) {
	store, err := session.NewSessionStore(t.TempDir())
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	rt := &runtime.Runtime{
		Config: config.AppConfig{
			Models: config.ModelsConfig{
				Providers: map[string]config.ProviderConfig{
					"openai": {Models: []config.ProviderModelConfig{{ID: "gpt-5.4", Name: "GPT-5.4"}}},
				},
			},
		},
		Sessions: store,
		EventHub: gateway.NewEventHub(),
	}
	server := NewACPBridgeServer(rt, ACPBridgeOptions{PrefixCwd: true})
	resp, reqErr := server.newSession(context.Background(), acpsdk.NewSessionRequest{Cwd: t.TempDir()})
	if reqErr != nil {
		t.Fatalf("newSession error: %+v", reqErr)
	}
	sessionID, _ := resp["sessionId"].(string)
	sessionItem := server.getBridgeSession(sessionID)
	if sessionItem == nil {
		server.mu.Lock()
		for _, item := range server.sessions {
			sessionItem = item
			break
		}
		server.mu.Unlock()
	}
	if sessionItem == nil {
		t.Fatalf("expected bridge session, got response %+v", resp)
	}

	testCases := []struct {
		id    string
		value string
	}{
		{id: "fast_mode", value: "on"},
		{id: "reasoning_level", value: "stream"},
		{id: "response_usage", value: "full"},
		{id: "elevated_level", value: "ask"},
	}
	for _, tc := range testCases {
		if _, reqErr = server.setSessionConfigOption(context.Background(), acpSetConfigOptionRequest{
			SessionID: acpsdk.SessionId(sessionItem.SessionID),
			ConfigID:  tc.id,
			Value:     tc.value,
		}); reqErr != nil {
			t.Fatalf("setSessionConfigOption(%s): %+v", tc.id, reqErr)
		}
	}

	row := server.gateway.GetSessionRow(sessionItem.SessionKey)
	if row == nil {
		t.Fatal("expected gateway session row")
	}
	if !row.FastMode {
		t.Fatal("expected fast mode to persist")
	}
	if row.ReasoningLevel != "stream" {
		t.Fatalf("expected reasoning level stream, got %q", row.ReasoningLevel)
	}
	if row.ResponseUsage != "full" {
		t.Fatalf("expected response usage full, got %q", row.ResponseUsage)
	}
	if row.ElevatedLevel != "ask" {
		t.Fatalf("expected elevated level ask, got %q", row.ElevatedLevel)
	}
}

func TestShortenHomePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		t.Skip("home directory unavailable")
	}
	if got := shortenHomePath(home); got != "~" {
		t.Fatalf("expected home path to shorten to ~, got %q", got)
	}
	child := home + string(os.PathSeparator) + "workspace"
	if got := shortenHomePath(child); got != "~"+string(os.PathSeparator)+"workspace" {
		t.Fatalf("expected child path to shorten, got %q", got)
	}
}

func findConfigOptionCurrentValue(options []any, id string) string {
	for _, item := range options {
		record, _ := item.(map[string]any)
		if record == nil {
			continue
		}
		if strings.TrimSpace(stringValue(record["id"])) != id {
			continue
		}
		return stringValue(record["currentValue"])
	}
	return ""
}
