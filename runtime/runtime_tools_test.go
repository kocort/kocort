package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/task"
	"github.com/kocort/kocort/internal/tool"
	toolfn "github.com/kocort/kocort/internal/tool"

	"github.com/kocort/kocort/utils"
)

func TestToolPolicyProfileAllowsSessionsSpawn(t *testing.T) {
	identity := core.AgentIdentity{
		ID:          "main",
		ToolProfile: "coding",
	}
	runCtx := rtypes.AgentRunContext{
		Request: core.AgentRunRequest{},
		Session: core.SessionResolution{SessionKey: session.BuildMainSessionKey("main")},
	}
	if !toolfn.IsToolAllowedByIdentity(identity, runCtx, core.ToolRegistrationMeta{}, "sessions_spawn") {
		t.Fatal("expected sessions_spawn allowed by coding profile")
	}
}

func TestToolPolicyProfileAllowsSessionsSend(t *testing.T) {
	identity := core.AgentIdentity{
		ID:          "main",
		ToolProfile: "coding",
	}
	runCtx := rtypes.AgentRunContext{
		Request: core.AgentRunRequest{},
		Session: core.SessionResolution{SessionKey: session.BuildMainSessionKey("main")},
	}
	if !toolfn.IsToolAllowedByIdentity(identity, runCtx, core.ToolRegistrationMeta{}, "sessions_send") {
		t.Fatal("expected sessions_send allowed by coding profile")
	}
}

func TestToolPolicyProfileAllowsMemorySearch(t *testing.T) {
	identity := core.AgentIdentity{
		ID:          "main",
		ToolProfile: "coding",
	}
	runCtx := rtypes.AgentRunContext{
		Request: core.AgentRunRequest{},
		Session: core.SessionResolution{SessionKey: session.BuildMainSessionKey("main")},
	}
	if !toolfn.IsToolAllowedByIdentity(identity, runCtx, core.ToolRegistrationMeta{}, "memory_search") {
		t.Fatal("expected memory_search allowed by coding profile")
	}
	if !toolfn.IsToolAllowedByIdentity(identity, runCtx, core.ToolRegistrationMeta{}, "memory_get") {
		t.Fatal("expected memory_get allowed by coding profile")
	}
	if !toolfn.IsToolAllowedByIdentity(identity, runCtx, core.ToolRegistrationMeta{}, "session_status") {
		t.Fatal("expected session_status allowed by coding profile")
	}
	if !toolfn.IsToolAllowedByIdentity(identity, runCtx, core.ToolRegistrationMeta{}, "subagents") {
		t.Fatal("expected subagents allowed by coding profile")
	}
}

func TestToolPolicyDenyOverridesAllow(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				WorkspaceDir:    filepath.Join(baseDir, "workspace"),
				ToolProfile:     "coding",
				ToolDenylist:    []string{"sessions_spawn"},
				DefaultModel:    "gpt-4.1",
				DefaultProvider: "openai",
			},
		}),
		Memory: infra.NullMemoryProvider{},
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			return core.AgentRunResult{}, nil
		}},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewSessionsSpawnTool()),
	}
	session, err := runtime.Sessions.Resolve(context.Background(), "main", session.BuildMainSessionKey("main"), "", "", "")
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	identity, _ := runtime.Identities.Resolve(context.Background(), "main")
	_, err = runtime.ExecuteTool(context.Background(), rtypes.AgentRunContext{
		Runtime:      runtime,
		Request:      core.AgentRunRequest{AgentID: "main", SessionKey: session.SessionKey},
		Session:      session,
		Identity:     identity,
		WorkspaceDir: identity.WorkspaceDir,
	}, "sessions_spawn", map[string]any{"task": "blocked"})
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected deny to block tool, got %v", err)
	}
}

func TestToolPolicyAllowlistBlocksUnlistedTool(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	noopTool := &testTool{name: "other_tool", resultText: "ok"}
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				WorkspaceDir:    filepath.Join(baseDir, "workspace"),
				ToolAllowlist:   []string{"sessions_spawn"},
				DefaultModel:    "gpt-4.1",
				DefaultProvider: "openai",
			},
		}),
		Memory: infra.NullMemoryProvider{},
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			return core.AgentRunResult{}, nil
		}},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewSessionsSpawnTool(), noopTool),
	}
	session, err := runtime.Sessions.Resolve(context.Background(), "main", session.BuildMainSessionKey("main"), "", "", "")
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	identity, _ := runtime.Identities.Resolve(context.Background(), "main")
	_, err = runtime.ExecuteTool(context.Background(), rtypes.AgentRunContext{
		Runtime:      runtime,
		Request:      core.AgentRunRequest{AgentID: "main", SessionKey: session.SessionKey},
		Session:      session,
		Identity:     identity,
		WorkspaceDir: identity.WorkspaceDir,
	}, "other_tool", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected allowlist to block other_tool, got %v", err)
	}
}

func TestRuntimeExecuteToolMirrorsToolTranscript(t *testing.T) {
	store := storeForTests(t)
	sessionKey := session.BuildMainSessionKey("main")
	if err := store.Upsert(sessionKey, core.SessionEntry{SessionID: "sess_tools"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	runtime := &Runtime{
		Sessions: store,
		Tools: tool.NewToolRegistry(&stubTool{
			name: "echo_tool",
			execute: func(ctx context.Context, toolCtx rtypes.ToolContext, args map[string]any) (core.ToolResult, error) {
				return core.ToolResult{Text: "tool-ok"}, nil
			},
		}),
	}
	runCtx := rtypes.AgentRunContext{
		Session:  core.SessionResolution{SessionKey: sessionKey, SessionID: "sess_tools"},
		Identity: core.AgentIdentity{ID: "main"},
	}
	if _, err := runtime.ExecuteTool(context.Background(), runCtx, "echo_tool", map[string]any{"message": "hi"}); err != nil {
		t.Fatalf("execute tool: %v", err)
	}
	history, err := store.LoadTranscript(sessionKey)
	if err != nil {
		t.Fatalf("load transcript: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected tool call + result transcript, got %+v", history)
	}
	if history[0].Type != "tool_call" || history[0].ToolName != "echo_tool" {
		t.Fatalf("unexpected tool call entry: %+v", history[0])
	}
	if history[1].Type != "tool_result" || history[1].Text != "tool-ok" {
		t.Fatalf("unexpected tool result entry: %+v", history[1])
	}
	if history[0].ToolCallID == "" || history[1].ToolCallID != history[0].ToolCallID {
		t.Fatalf("expected tool call/result to share toolCallId, got %+v", history)
	}
}

func TestRuntimePluginRegistryRespectsConfigAndOptionalAllowlist(t *testing.T) {
	registry := NewRuntimePluginRegistry(config.PluginsConfig{
		Allow: []string{"demo"},
		Entries: map[string]config.PluginEntryConfig{
			"demo": {Enabled: utils.BoolPtr(true)},
		},
	}, stubRuntimePlugin{
		id: "demo",
		tools: []tool.Tool{
			&stubTool{name: "demo_required"},
			&stubTool{name: "demo_optional", meta: core.ToolRegistrationMeta{PluginID: "demo", OptionalPlugin: true}},
		},
	})
	tools, err := registry.ResolveTools(RuntimePluginToolContext{
		ExistingToolNames: map[string]struct{}{},
		ToolAllowlist:     []string{"demo_optional"},
	})
	if err != nil {
		t.Fatalf("resolve plugin tools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected required + allowed optional tools, got %+v", tools)
	}
}

func TestExecuteToolRespectsProviderChannelAndOwnerMetadata(t *testing.T) {
	runtime := &Runtime{
		Tools: tool.NewToolRegistry(&stubTool{
			name: "guarded",
			meta: core.ToolRegistrationMeta{
				OwnerOnly:        true,
				AllowedProviders: []string{"openai"},
				AllowedChannels:  []string{"test"},
			},
			execute: func(ctx context.Context, toolCtx rtypes.ToolContext, args map[string]any) (core.ToolResult, error) {
				return core.ToolResult{Text: "ok"}, nil
			},
		}),
		Sessions: storeForTests(t),
	}
	runCtx := rtypes.AgentRunContext{
		Request:        core.AgentRunRequest{To: "user-1", Channel: "test"},
		Session:        core.SessionResolution{SessionKey: session.BuildMainSessionKey("main"), SessionID: "sess_meta"},
		Identity:       core.AgentIdentity{ID: "main"},
		ModelSelection: core.ModelSelection{Provider: "openai"},
	}
	if _, err := runtime.ExecuteTool(context.Background(), runCtx, "guarded", nil); err != nil {
		t.Fatalf("expected guarded tool allowed, got %v", err)
	}
	runCtx.ModelSelection.Provider = "anthropic"
	if _, err := runtime.ExecuteTool(context.Background(), runCtx, "guarded", nil); err == nil {
		t.Fatal("expected provider gate to reject tool")
	}
}

func TestExecuteToolRequiresApprovalForElevatedTool(t *testing.T) {
	runtime := &Runtime{
		Sessions: storeForTests(t),
		Approvals: stubToolApprovalRunner{approve: func(ctx context.Context, req tool.ToolApprovalRequest) (tool.ToolApprovalDecision, error) {
			if req.ToolName != "guarded_elevated" || !req.Elevated {
				t.Fatalf("unexpected approval request: %+v", req)
			}
			return tool.ToolApprovalDecision{Allowed: false, Reason: "manual approval required"}, nil
		}},
		Tools: tool.NewToolRegistry(&stubTool{
			name: "guarded_elevated",
			meta: core.ToolRegistrationMeta{Elevated: true},
			execute: func(ctx context.Context, toolCtx rtypes.ToolContext, args map[string]any) (core.ToolResult, error) {
				t.Fatal("elevated tool should not execute when approval is denied")
				return core.ToolResult{}, nil
			},
		}),
	}
	runCtx := rtypes.AgentRunContext{
		Request:        core.AgentRunRequest{To: "user-1", Channel: "test"},
		Session:        core.SessionResolution{SessionKey: session.BuildMainSessionKey("main"), SessionID: "sess_meta"},
		Identity:       core.AgentIdentity{ID: "main", ElevatedEnabled: true},
		ModelSelection: core.ModelSelection{Provider: "openai"},
	}
	_, err := runtime.ExecuteTool(context.Background(), runCtx, "guarded_elevated", nil)
	if err == nil || !strings.Contains(err.Error(), "requires approval") {
		t.Fatalf("expected approval error, got %v", err)
	}
}

func TestExecuteToolAppliesMetaDefaultTimeout(t *testing.T) {
	runtime := &Runtime{
		Sessions: storeForTests(t),
		Tools: tool.NewToolRegistry(&stubTool{
			name: "slow_tool",
			meta: core.ToolRegistrationMeta{DefaultTimeoutMs: 25},
			execute: func(ctx context.Context, toolCtx rtypes.ToolContext, args map[string]any) (core.ToolResult, error) {
				<-ctx.Done()
				return core.ToolResult{}, ctx.Err()
			},
		}),
	}
	runCtx := rtypes.AgentRunContext{
		Request:        core.AgentRunRequest{To: "user-1", Channel: "test", RunID: "run_timeout_meta"},
		Session:        core.SessionResolution{SessionKey: session.BuildMainSessionKey("main"), SessionID: "sess_timeout_meta"},
		Identity:       core.AgentIdentity{ID: "main"},
		ModelSelection: core.ModelSelection{Provider: "openai"},
	}
	_, err := runtime.ExecuteTool(context.Background(), runCtx, "slow_tool", nil)
	if err == nil {
		t.Fatal("expected timeout failure")
	}
	var toolErr *core.ToolExecutionFailure
	if !errors.As(err, &toolErr) {
		t.Fatalf("expected ToolExecutionFailure, got %T", err)
	}
	if !strings.Contains(strings.ToLower(toolErr.Message), "timed out") {
		t.Fatalf("expected timeout message, got %+v", toolErr)
	}
}

func TestExecuteToolGenericRepeatOnlyWarnsAndStillExecutes(t *testing.T) {
	store := storeForTests(t)
	executed := 0
	runtime := &Runtime{
		Sessions:  store,
		ToolLoops: tool.NewToolLoopRegistry(),
		Tools: tool.NewToolRegistry(&stubTool{
			name: "repeat_tool",
			execute: func(ctx context.Context, toolCtx rtypes.ToolContext, args map[string]any) (core.ToolResult, error) {
				executed++
				return core.ToolResult{Text: "ok"}, nil
			},
		}),
	}
	runCtx := rtypes.AgentRunContext{
		Request: core.AgentRunRequest{RunID: "run_repeat_warning"},
		Session: core.SessionResolution{SessionKey: session.BuildMainSessionKey("main"), SessionID: "sess_repeat_warning"},
		Identity: core.AgentIdentity{
			ID: "main",
			ToolLoopDetection: core.ToolLoopDetectionConfig{
				Enabled:           utils.BoolPtr(true),
				HistorySize:       30,
				WarningThreshold:  3,
				CriticalThreshold: 5,
			},
		},
	}

	for i := 0; i < 5; i++ {
		if _, err := runtime.ExecuteTool(context.Background(), runCtx, "repeat_tool", map[string]any{"value": "same"}); err != nil {
			t.Fatalf("call %d should not be blocked, got %v", i+1, err)
		}
	}
	if executed != 5 {
		t.Fatalf("expected 5 executions, got %d", executed)
	}
}

func TestExecuteToolKnownPollNoProgressBlocksAtCriticalThreshold(t *testing.T) {
	store := storeForTests(t)
	executed := 0
	runtime := &Runtime{
		Sessions:  store,
		ToolLoops: tool.NewToolLoopRegistry(),
		Tools: tool.NewToolRegistry(&stubTool{
			name: "process",
			execute: func(ctx context.Context, toolCtx rtypes.ToolContext, args map[string]any) (core.ToolResult, error) {
				executed++
				return core.ToolResult{JSON: []byte(`{"status":"running"}`)}, nil
			},
		}),
	}
	runCtx := rtypes.AgentRunContext{
		Request: core.AgentRunRequest{RunID: "run_poll_loop"},
		Session: core.SessionResolution{SessionKey: session.BuildMainSessionKey("main"), SessionID: "sess_poll_loop"},
		Identity: core.AgentIdentity{
			ID: "main",
			ToolLoopDetection: core.ToolLoopDetectionConfig{
				Enabled:           utils.BoolPtr(true),
				HistorySize:       30,
				WarningThreshold:  2,
				CriticalThreshold: 3,
				Detectors: core.ToolLoopDetectionDetectorConfig{
					KnownPollNoProgress: utils.BoolPtr(true),
				},
			},
		},
	}

	args := map[string]any{"action": "poll", "sessionId": "proc_1"}
	for i := 0; i < 3; i++ {
		if _, err := runtime.ExecuteTool(context.Background(), runCtx, "process", args); err != nil {
			t.Fatalf("poll call %d should not be blocked yet, got %v", i+1, err)
		}
	}
	_, err := runtime.ExecuteTool(context.Background(), runCtx, "process", args)
	if err == nil {
		t.Fatal("expected critical tool-loop failure on repeated no-progress poll")
	}
	var toolErr *core.ToolExecutionFailure
	if !errors.As(err, &toolErr) {
		t.Fatalf("expected ToolExecutionFailure, got %T", err)
	}
	if !strings.Contains(strings.ToLower(toolErr.Message), "stuck polling loop") {
		t.Fatalf("expected stuck polling loop error, got %q", toolErr.Message)
	}
	if executed != 3 {
		t.Fatalf("expected 3 successful executions before block, got %d", executed)
	}
}

func TestDetectToolCallLoopBlocksPingPongNoProgressAtCriticalThreshold(t *testing.T) {
	state := &tool.ToolLoopSessionState{}
	cfg := core.ToolLoopDetectionConfig{
		Enabled:           utils.BoolPtr(true),
		HistorySize:       30,
		WarningThreshold:  2,
		CriticalThreshold: 4,
		Detectors: core.ToolLoopDetectionDetectorConfig{
			PingPong: utils.BoolPtr(true),
		},
	}
	tool.RecordToolCall(state, "step_a", map[string]any{"value": "a"}, "call_a1", cfg)
	tool.RecordToolCallOutcome(state, "step_a", map[string]any{"value": "a"}, "call_a1", core.ToolResult{Text: "same-a"}, nil, cfg)
	tool.RecordToolCall(state, "step_b", map[string]any{"value": "b"}, "call_b1", cfg)
	tool.RecordToolCallOutcome(state, "step_b", map[string]any{"value": "b"}, "call_b1", core.ToolResult{Text: "same-b"}, nil, cfg)
	tool.RecordToolCall(state, "step_a", map[string]any{"value": "a"}, "call_a2", cfg)
	tool.RecordToolCallOutcome(state, "step_a", map[string]any{"value": "a"}, "call_a2", core.ToolResult{Text: "same-a"}, nil, cfg)

	result := tool.DetectToolCallLoop(state, "step_b", map[string]any{"value": "b"}, cfg)
	if !result.Stuck || result.Level != "critical" {
		t.Fatalf("expected critical ping-pong detection, got %+v", result)
	}
	if result.Detector != tool.ToolLoopDetectorPingPong {
		t.Fatalf("expected ping-pong detector, got %+v", result)
	}
	if !strings.Contains(strings.ToLower(result.Message), "ping-pong loop") {
		t.Fatalf("expected ping-pong message, got %q", result.Message)
	}
}

func TestExecToolKeepsAgentWorkspaceAsDefaultPwdWhenSandboxEnabled(t *testing.T) {
	store := storeForTests(t)
	runtime := &Runtime{Sessions: store}
	workspace := t.TempDir()
	runCtx := rtypes.AgentRunContext{
		Request: core.AgentRunRequest{Channel: "test", To: "user"},
		Session: core.SessionResolution{SessionKey: session.BuildMainSessionKey("worker"), SessionID: "sess_sandbox"},
		Identity: core.AgentIdentity{
			ID:                     "worker",
			SandboxMode:            "all",
			SandboxWorkspaceAccess: "ro",
			SandboxScope:           "session",
		},
		WorkspaceDir: workspace,
	}
	sandboxWorkspace := filepath.Join(store.BaseDir(), "sandboxes", "agent_main_main")
	if err := os.MkdirAll(sandboxWorkspace, 0o755); err != nil {
		t.Fatalf("mkdir sandbox workspace: %v", err)
	}
	result, err := tool.NewExecTool().Execute(context.Background(), rtypes.ToolContext{
		Runtime: runtime,
		Run:     runCtx,
		Sandbox: &rtypes.SandboxContext{
			Enabled:         true,
			WorkspaceAccess: "ro",
			Scope:           "session",
			WorkspaceDir:    sandboxWorkspace,
			AgentWorkspace:  workspace,
		},
	}, map[string]any{"command": newTestShellHelper(t).PwdAndSandboxEnvScript()})
	if err != nil {
		t.Fatalf("exec in sandbox: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(result.Text), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected pwd/workspace/dirs output, got %q", result.Text)
	}
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		resolvedWorkspace = workspace
	}
	resolvedPwd, err := filepath.EvalSymlinks(strings.TrimSpace(lines[0]))
	if err != nil {
		resolvedPwd = strings.TrimSpace(lines[0])
	}
	if resolvedPwd != resolvedWorkspace {
		t.Fatalf("expected agent workspace pwd %q, got %q", resolvedWorkspace, resolvedPwd)
	}
	if got := strings.TrimSpace(lines[1]); got != sandboxWorkspace {
		t.Fatalf("expected sandbox workspace env %q, got %q", sandboxWorkspace, got)
	}
	dirs := filepath.SplitList(strings.TrimSpace(lines[2]))
	if len(dirs) == 0 {
		t.Fatalf("expected sandbox dirs to be populated, got %q", lines[2])
	}
	var foundWorkspace bool
	for _, dir := range dirs {
		if strings.TrimSpace(dir) == workspace {
			foundWorkspace = true
			break
		}
	}
	if !foundWorkspace {
		t.Fatalf("expected sandbox dirs to include workdir %q, got %+v", workspace, dirs)
	}
}

func TestExecToolHonorsTimeoutParameter(t *testing.T) {
	store := storeForTests(t)
	allowBackground := false
	runtime := &Runtime{Sessions: store}
	runCtx := rtypes.AgentRunContext{
		Request: core.AgentRunRequest{Channel: "test", To: "user"},
		Session: core.SessionResolution{SessionKey: session.BuildMainSessionKey("main"), SessionID: "sess_exec_timeout"},
		Identity: core.AgentIdentity{
			ID: "main",
		},
		WorkspaceDir: t.TempDir(),
	}
	_, err := tool.NewExecTool(&config.ToolExecConfig{AllowBackground: &allowBackground}).Execute(context.Background(), rtypes.ToolContext{
		Runtime: runtime,
		Run:     runCtx,
	}, map[string]any{"command": "sleep 1", "timeout": float64(0.05)})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}
}

func TestSandboxSessionVisibilityBlocksSessionToolsOutsideSpawnedTree(t *testing.T) {
	runCtx := rtypes.AgentRunContext{
		Request: core.AgentRunRequest{Channel: "test", To: "user"},
		Session: core.SessionResolution{SessionKey: session.BuildMainSessionKey("main"), SessionID: "sess_main"},
		Identity: core.AgentIdentity{
			ID:                       "main",
			SandboxMode:              "all",
			SandboxWorkspaceAccess:   "ro",
			SandboxSessionVisibility: "spawned",
		},
	}
	sandbox := &rtypes.SandboxContext{Enabled: true, Mode: "all", WorkspaceAccess: "ro"}
	if toolfn.IsToolAllowedInSandbox(runCtx.Identity, runCtx, core.ToolRegistrationMeta{}, "sessions_history", sandbox) {
		t.Fatal("expected sessions_history blocked in sandbox for non-subagent session")
	}
	if !toolfn.IsToolAllowedInSandbox(runCtx.Identity, runCtx, core.ToolRegistrationMeta{}, "exec", sandbox) {
		t.Fatal("expected exec allowed in sandbox")
	}
	runCtx.Request.Lane = core.LaneSubagent
	runCtx.Session.SessionKey = "agent:main:subagent:child"
	if !toolfn.IsToolAllowedInSandbox(runCtx.Identity, runCtx, core.ToolRegistrationMeta{}, "sessions_history", sandbox) {
		t.Fatal("expected sessions_history allowed in spawned tree")
	}
}

func TestExecuteToolAppliesPluginEnvOverride(t *testing.T) {
	store := storeForTests(t)
	runtime := &Runtime{
		Config: config.AppConfig{
			Plugins: config.PluginsConfig{
				Entries: map[string]config.PluginEntryConfig{
					"demo": {
						APIKey: "plugin-secret",
						Env:    map[string]string{"DEMO_PLUGIN_ENV": "set"},
					},
				},
			},
		},
		Sessions: store,
		Tools: tool.NewToolRegistry(&stubTool{
			name: "plugin_tool",
			meta: core.ToolRegistrationMeta{PluginID: "demo"},
			execute: func(ctx context.Context, toolCtx rtypes.ToolContext, args map[string]any) (core.ToolResult, error) {
				return core.ToolResult{
					Text: os.Getenv("KOCORT_PLUGIN_API_KEY") + ":" + os.Getenv("DEMO_PLUGIN_ENV"),
				}, nil
			},
		}),
	}
	runCtx := rtypes.AgentRunContext{
		Session:  core.SessionResolution{SessionKey: session.BuildMainSessionKey("main"), SessionID: "sess_plugin"},
		Identity: core.AgentIdentity{ID: "main"},
	}
	result, err := runtime.ExecuteTool(context.Background(), runCtx, "plugin_tool", nil)
	if err != nil {
		t.Fatalf("execute plugin tool: %v", err)
	}
	if result.Text != "plugin-secret:set" {
		t.Fatalf("expected plugin env injection, got %q", result.Text)
	}
}

func TestExecuteToolBlocksDisabledPluginTool(t *testing.T) {
	store := storeForTests(t)
	runtime := &Runtime{
		Config: config.AppConfig{
			Plugins: config.PluginsConfig{
				Entries: map[string]config.PluginEntryConfig{
					"demo": {Enabled: utils.BoolPtr(false)},
				},
			},
		},
		Sessions: store,
		Tools: tool.NewToolRegistry(&stubTool{
			name: "plugin_tool",
			meta: core.ToolRegistrationMeta{PluginID: "demo"},
			execute: func(ctx context.Context, toolCtx rtypes.ToolContext, args map[string]any) (core.ToolResult, error) {
				t.Fatal("plugin tool should not execute when plugin is disabled")
				return core.ToolResult{}, nil
			},
		}),
	}
	runCtx := rtypes.AgentRunContext{
		Session:  core.SessionResolution{SessionKey: session.BuildMainSessionKey("main"), SessionID: "sess_plugin"},
		Identity: core.AgentIdentity{ID: "main"},
	}
	_, err := runtime.ExecuteTool(context.Background(), runCtx, "plugin_tool", nil)
	if err == nil || !strings.Contains(err.Error(), "plugin") {
		t.Fatalf("expected plugin disabled error, got %v", err)
	}
}
