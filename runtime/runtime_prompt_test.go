package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/infra"
	memorypkg "github.com/kocort/kocort/internal/memory"
	"github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/skill"
	"github.com/kocort/kocort/internal/task"
	"github.com/kocort/kocort/internal/tool"
)

func TestRuntimePersistsTranscriptAndMemoryAwarePrompt(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	deliverer := &delivery.MemoryDeliverer{}
	runtime := &Runtime{
		Config: config.AppConfig{
			Session: config.SessionConfig{DMScope: "per-peer"},
		},
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				Name:            "kocort",
				PersonaPrompt:   "You are kocort.",
				WorkspaceDir:    filepath.Join(baseDir, "workspace"),
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1",
				ModelFallbacks:  []string{"openai/gpt-4.1-mini"},
			},
		}),
		Memory: infra.NullMemoryProvider{},
		Backend: fakeBackend{
			onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
				if !strings.Contains(runCtx.SystemPrompt, "You are kocort.") {
					t.Fatalf("unexpected system prompt: %q", runCtx.SystemPrompt)
				}
				// OmitUserMessageInSystemPrompt is active: user message is only
				// in the messages array, not duplicated in the system prompt.
				if strings.Contains(runCtx.SystemPrompt, "Current user message:") {
					t.Fatalf("system prompt should NOT contain user message (OmitUserMessageInSystemPrompt=true), got %q", runCtx.SystemPrompt)
				}
				if !strings.Contains(runCtx.Request.Message, "ship checklist") || !strings.HasPrefix(runCtx.Request.Message, "[") {
					t.Fatalf("expected timestamp-injected request message, got %q", runCtx.Request.Message)
				}
				if runCtx.ModelSelection.Provider != "openai" || runCtx.ModelSelection.Model != "gpt-4.1" {
					t.Fatalf("unexpected model selection: %+v", runCtx.ModelSelection)
				}
				runCtx.ReplyDispatcher.SendBlockReply(core.ReplyPayload{Text: "working"})
				runCtx.ReplyDispatcher.SendFinalReply(core.ReplyPayload{Text: "done"})
				return core.AgentRunResult{
					Payloads: []core.ReplyPayload{{Text: "done"}},
				}, nil
			},
		},
		Deliverer:  deliverer,
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewSessionsSpawnTool()),
	}

	result, err := runtime.Run(context.Background(), core.AgentRunRequest{
		Message: "ship checklist",
		To:      "user-1",
		Channel: "webchat",
	})
	if err != nil {
		t.Fatalf("runtime run: %v", err)
	}
	if result.RunID == "" {
		t.Fatal("expected run ID")
	}
	if len(deliverer.Records) != 2 {
		t.Fatalf("expected streamed + final delivery, got %d", len(deliverer.Records))
	}
	sessionKey := session.BuildDirectSessionKey("main", "webchat", "user-1")
	history, err := store.LoadTranscript(sessionKey)
	if err != nil {
		t.Fatalf("load transcript: %v", err)
	}
	if len(history) < 2 {
		t.Fatalf("expected transcript entries, got %d", len(history))
	}
	var sawUser, sawAssistant bool
	for _, entry := range history {
		if entry.Role == "user" && strings.Contains(entry.Text, "ship checklist") {
			sawUser = true
		}
		if entry.Role == "assistant" && entry.Final && entry.Text == "done" {
			sawAssistant = true
		}
	}
	if !sawUser || !sawAssistant {
		t.Fatalf("expected user and final assistant transcript entries, got %+v", history)
	}
}

func TestRuntimePromptListsOnlyAllowedTools(t *testing.T) {
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
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1",
				ToolAllowlist:   []string{"memory_search"},
			},
		}),
		Memory: infra.NullMemoryProvider{},
		Backend: fakeBackend{
			onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
				if !strings.Contains(runCtx.SystemPrompt, "memory_search") {
					t.Fatalf("expected allowed tool in prompt, got %q", runCtx.SystemPrompt)
				}
				if strings.Contains(runCtx.SystemPrompt, "sessions_spawn") {
					t.Fatalf("unexpected disallowed tool in prompt, got %q", runCtx.SystemPrompt)
				}
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "done"}}}, nil
			},
		},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewMemorySearchTool(), tool.NewSessionsSpawnTool()),
	}
	if _, err := runtime.Run(context.Background(), core.AgentRunRequest{Message: "hello"}); err != nil {
		t.Fatalf("runtime run: %v", err)
	}
}

func TestRuntimeDispatchesExplicitSkillCommandsToExecTool(t *testing.T) {
	baseDir := t.TempDir()
	workspace := filepath.Join(baseDir, "workspace")
	if _, err := infra.EnsureWorkspaceDir(workspace); err != nil {
		t.Fatalf("ensure workspace: %v", err)
	}
	for _, sk := range []struct {
		Name string
		Body string
	}{
		{
			Name: "alpha",
			Body: "---\nname: alpha\ndescription: Run alpha\ncommand-dispatch: tool\ncommand-tool: exec\n---\n# Alpha\n",
		},
		{
			Name: "beta",
			Body: "---\nname: beta\ndescription: Run beta\ncommand-dispatch: tool\ncommand-tool: exec\n---\n# Beta\n",
		},
	} {
		skillDir := filepath.Join(workspace, "skills", sk.Name)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatalf("mkdir skill dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, skill.DefaultSkillFilename), []byte(sk.Body), 0o644); err != nil {
			t.Fatalf("write skill: %v", err)
		}
	}
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	deliverer := &delivery.MemoryDeliverer{}
	runtime := &Runtime{
		Config: config.AppConfig{
			Session: config.SessionConfig{DMScope: "per-peer"},
		},
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				WorkspaceDir:    workspace,
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1",
				ToolAllowlist:   []string{"exec"},
			},
		}),
		Memory: infra.NullMemoryProvider{},
		Backend: fakeBackend{
			onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
				t.Fatalf("backend should not be called for explicit skill tool dispatch")
				return core.AgentRunResult{}, nil
			},
		},
		Deliverer:  deliverer,
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewExecTool()),
	}

	alpha, err := runtime.Run(context.Background(), core.AgentRunRequest{
		Message: "/alpha echo ALPHA",
		Channel: "skill-test",
		To:      "user-1",
	})
	if err != nil {
		t.Fatalf("alpha run: %v", err)
	}
	if len(alpha.Payloads) != 1 || alpha.Payloads[0].Text != "ALPHA" {
		t.Fatalf("unexpected alpha result: %+v", alpha)
	}

	beta, err := runtime.Run(context.Background(), core.AgentRunRequest{
		Message: "/beta echo BETA",
		Channel: "skill-test",
		To:      "user-2",
	})
	if err != nil {
		t.Fatalf("beta run: %v", err)
	}
	if len(beta.Payloads) != 1 || beta.Payloads[0].Text != "BETA" {
		t.Fatalf("unexpected beta result: %+v", beta)
	}

	if len(deliverer.Records) != 2 {
		t.Fatalf("expected two final deliveries, got %+v", deliverer.Records)
	}
	if deliverer.Records[0].Payload.Text != "ALPHA" || deliverer.Records[1].Payload.Text != "BETA" {
		t.Fatalf("unexpected delivery records: %+v", deliverer.Records)
	}

	alphaHistory, err := store.LoadTranscript(session.BuildDirectSessionKey("main", "skill-test", "user-1"))
	if err != nil {
		t.Fatalf("load alpha history: %v", err)
	}
	if !containsTranscriptFinal(alphaHistory, "ALPHA") || !containsTranscriptTool(alphaHistory, "exec") {
		t.Fatalf("unexpected alpha transcript: %+v", alphaHistory)
	}
	betaHistory, err := store.LoadTranscript(session.BuildDirectSessionKey("main", "skill-test", "user-2"))
	if err != nil {
		t.Fatalf("load beta history: %v", err)
	}
	if !containsTranscriptFinal(betaHistory, "BETA") || !containsTranscriptTool(betaHistory, "exec") {
		t.Fatalf("unexpected beta transcript: %+v", betaHistory)
	}
}

func TestRuntimeRewritesExplicitSkillInvocationForModel(t *testing.T) {
	baseDir := t.TempDir()
	workspace := filepath.Join(baseDir, "workspace")
	if _, err := infra.EnsureWorkspaceDir(workspace); err != nil {
		t.Fatalf("ensure workspace: %v", err)
	}
	skillDir := filepath.Join(workspace, "skills", "deploy")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillBody := `---
name: deploy
description: Deployment checklist
---
# Deploy

Use this skill when the user asks to deploy.
`
	if err := os.WriteFile(filepath.Join(skillDir, skill.DefaultSkillFilename), []byte(skillBody), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				WorkspaceDir:    workspace,
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1",
			},
		}),
		Memory: infra.NullMemoryProvider{},
		Backend: fakeBackend{
			onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
				if strings.Contains(runCtx.SystemPrompt, "## Selected Skill") {
					t.Fatalf("did not expect selected skill injection: %q", runCtx.SystemPrompt)
				}
				if runCtx.Request.Message != "Use the \"deploy\" skill for this request.\n\nUser input:\nship now" {
					t.Fatalf("unexpected rewritten request: %q", runCtx.Request.Message)
				}
				return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "done"}}}, nil
			},
		},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(),
	}
	if _, err := runtime.Run(context.Background(), core.AgentRunRequest{
		Message: "/skill deploy ship now",
		Channel: "skill-test",
		To:      "user-1",
	}); err != nil {
		t.Fatalf("runtime run: %v", err)
	}
}

func containsTranscriptFinal(history []core.TranscriptMessage, text string) bool {
	for _, entry := range history {
		if entry.Role == "assistant" && entry.Final && entry.Text == text {
			return true
		}
	}
	return false
}

func containsTranscriptTool(history []core.TranscriptMessage, toolName string) bool {
	for _, entry := range history {
		if entry.Type == "tool_call" && entry.ToolName == toolName {
			return true
		}
	}
	return false
}

func TestRuntimePersistsSkillsSnapshot(t *testing.T) {
	baseDir := t.TempDir()
	workspace := filepath.Join(baseDir, "workspace")
	if _, err := infra.EnsureWorkspaceDir(workspace); err != nil {
		t.Fatalf("ensure workspace: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "skills", "triage"), 0o755); err != nil {
		t.Fatalf("mkdir skills: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "skills", "triage", skill.DefaultSkillFilename), []byte("# Triage\nUse for bug triage."), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	store, err := session.NewSessionStore(baseDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	runtime := &Runtime{
		Sessions: store,
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				WorkspaceDir:    workspace,
				SkillFilter:     []string{"triage"},
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1",
			},
		}),
		Memory: infra.NullMemoryProvider{},
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "done"}}}, nil
		}},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(),
	}
	if _, err := runtime.Run(context.Background(), core.AgentRunRequest{AgentID: "main", Message: "hello"}); err != nil {
		t.Fatalf("runtime run: %v", err)
	}
	entry := runtime.Sessions.Entry(session.BuildMainSessionKey("main"))
	if entry == nil || entry.SkillsSnapshot == nil {
		t.Fatalf("expected persisted skills snapshot, got %+v", entry)
	}
	found := false
	for _, skill := range entry.SkillsSnapshot.Skills {
		if skill.Name == "triage" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected triage skill in snapshot, got %+v", entry.SkillsSnapshot.Skills)
	}
}

func TestStaticIdentityResolverUsesDefaultWorkspaceForNonDefaultAgent(t *testing.T) {
	resolver := infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
		"worker": {
			ID:              "worker",
			DefaultProvider: "openai",
			DefaultModel:    "gpt-4.1-mini",
		},
	})
	identity, err := resolver.Resolve(context.Background(), "worker")
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	if !strings.Contains(identity.WorkspaceDir, "workspace-worker") {
		t.Fatalf("expected per-agent default workspace dir, got %q", identity.WorkspaceDir)
	}
}

func TestMemorySearchAndGetToolsUseWorkspaceMemoryFiles(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	if _, err := infra.EnsureWorkspaceDir(workspace); err != nil {
		t.Fatalf("ensure workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, memorypkg.DefaultMemoryFilename), []byte("Project Atlas ships on Fridays.\nKeep customer IDs out of replies."), 0o644); err != nil {
		t.Fatalf("write memory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "memory"), 0o755); err != nil {
		t.Fatalf("mkdir memory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "memory", "ops.md"), []byte("Line one\nLine two\nLine three\nLine four"), 0o644); err != nil {
		t.Fatalf("write ops memory: %v", err)
	}
	runtime := &Runtime{
		Sessions: storeForTests(t),
		Identities: infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
			"main": {
				ID:              "main",
				WorkspaceDir:    workspace,
				ToolProfile:     "coding",
				DefaultProvider: "openai",
				DefaultModel:    "gpt-4.1",
			},
		}),
		Memory: memorypkg.NewWorkspaceMemoryProvider(),
		Backend: fakeBackend{onRun: func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
			return core.AgentRunResult{}, nil
		}},
		Deliverer:  &delivery.MemoryDeliverer{},
		Subagents:  task.NewSubagentRegistry(),
		Queue:      task.NewFollowupQueue(),
		ActiveRuns: task.NewActiveRunRegistry(),
		Tools:      tool.NewToolRegistry(tool.NewMemorySearchTool(), tool.NewMemoryGetTool()),
	}
	session, err := runtime.Sessions.Resolve(context.Background(), "main", session.BuildMainSessionKey("main"), "", "", "")
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	identity, err := runtime.Identities.Resolve(context.Background(), "main")
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	runCtx := rtypes.AgentRunContext{
		Runtime: runtime,
		Request: core.AgentRunRequest{AgentID: "main", SessionKey: session.SessionKey},
		Session: session, Identity: identity, WorkspaceDir: identity.WorkspaceDir,
	}
	searchResult, err := runtime.ExecuteTool(context.Background(), runCtx, "memory_search", map[string]any{"query": "When does Atlas ship?"})
	if err != nil {
		t.Fatalf("memory_search: %v", err)
	}
	if !strings.Contains(searchResult.Text, "Atlas") {
		t.Fatalf("expected atlas memory hit, got %s", searchResult.Text)
	}
	getResult, err := runtime.ExecuteTool(context.Background(), runCtx, "memory_get", map[string]any{"path": "memory/ops.md", "from": float64(2), "lines": float64(2)})
	if err != nil {
		t.Fatalf("memory_get: %v", err)
	}
	if !strings.Contains(getResult.Text, "Line two") || !strings.Contains(getResult.Text, "Line three") {
		t.Fatalf("expected selected memory lines, got %s", getResult.Text)
	}
}

func TestNewRuntimeFromConfigUsesConfiguredReasoningDefault(t *testing.T) {
	cfg := config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"nvidia": {
					BaseURL: "https://example.com/v1",
					API:     "openai-completions",
					Models: []config.ProviderModelConfig{{
						ID:        "nvidia/glm-4-9b",
						Reasoning: true,
					}},
				},
			},
		},
	}
	rt, err := NewRuntimeFromConfig(cfg, config.RuntimeConfigParams{
		StateDir: t.TempDir(),
		Provider: "nvidia",
		Model:    "nvidia/glm-4-9b",
	})
	if err != nil {
		t.Fatalf("new runtime from config: %v", err)
	}
	identity, err := rt.Identities.Resolve(context.Background(), "main")
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	if identity.DefaultProvider != "nvidia" || identity.DefaultModel != "nvidia/glm-4-9b" {
		t.Fatalf("unexpected identity: %+v", identity)
	}
	if identity.ThinkingDefault != "low" {
		t.Fatalf("expected reasoning-capable model to default to low thinking, got %+v", identity)
	}
}

func TestNewRuntimeFromConfigAppliesAgentDefaultsAndOverrides(t *testing.T) {
	baseDir := t.TempDir()
	extraMemoryFile := filepath.Join(baseDir, "extra-memory.txt")
	if err := os.WriteFile(extraMemoryFile, []byte("Extra launch token is ORBIT-99.\n"), 0o644); err != nil {
		t.Fatalf("write extra memory: %v", err)
	}
	cfg := config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {
					BaseURL: "https://example.com/v1",
					API:     "openai-completions",
					Models: []config.ProviderModelConfig{
						{ID: "gpt-4.1", Reasoning: true},
						{ID: "gpt-4.1-mini", Reasoning: false},
					},
				},
				"acp-live": {
					API: "acp",
					Command: &core.CommandBackendConfig{
						Command: "/bin/echo",
					},
				},
			},
		},
		Agents: config.AgentsConfig{
			Defaults: &config.AgentDefaultsConfig{
				Model:           config.AgentModelConfig{Primary: "openai/gpt-4.1", Fallbacks: []string{"openai/gpt-4.1-mini"}},
				Workspace:       filepath.Join(baseDir, "workspace-default"),
				UserTimezone:    "Asia/Shanghai",
				TimeoutSeconds:  91,
				ThinkingDefault: "medium",
				Skills:          []string{"triage"},
				MemorySearch: config.AgentMemorySearchConfig{
					ExtraPaths: []string{extraMemoryFile},
				},
				Subagents: config.AgentSubagentConfig{
					AllowAgents:         []string{"worker", "helper"},
					MaxSpawnDepth:       3,
					MaxChildrenPerAgent: 2,
					TimeoutSeconds:      44,
					Thinking:            "high",
				},
				Tools: config.AgentToolPolicyConfig{
					Profile:   "minimal",
					AlsoAllow: []string{"memory_search"},
				},
			},
			List: []config.AgentConfig{{
				ID:              "worker",
				Default:         true,
				Name:            "Worker Agent",
				Workspace:       filepath.Join(baseDir, "workspace-worker"),
				Model:           config.AgentModelConfig{Primary: "openai/gpt-4.1-mini"},
				Skills:          []string{"deploy"},
				ThinkingDefault: "low",
				Tools: config.AgentToolPolicyConfig{
					Allow: []string{"sessions_send"},
					Deny:  []string{"memory_search"},
				},
				Runtime: &config.AgentRuntimeConfig{
					Type: "acp",
					ACP: &config.AgentRuntimeACPConfig{
						Backend: "acp-live",
						Mode:    "persistent",
						Cwd:     filepath.Join(baseDir, "runtime-cwd"),
						Agent:   "codex",
					},
				},
			}},
		},
	}
	rt, err := NewRuntimeFromConfig(cfg, config.RuntimeConfigParams{
		StateDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new runtime from config: %v", err)
	}
	identity, err := rt.Identities.Resolve(context.Background(), "worker")
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	if identity.Name != "Worker Agent" {
		t.Fatalf("expected configured name, got %+v", identity)
	}
	if identity.DefaultProvider != "openai" || identity.DefaultModel != "gpt-4.1-mini" {
		t.Fatalf("unexpected model defaults: %+v", identity)
	}
	if identity.ThinkingDefault != "low" || identity.UserTimezone != "Asia/Shanghai" || identity.TimeoutSeconds != 91 {
		t.Fatalf("unexpected agent defaults merged into identity: %+v", identity)
	}
	if identity.RuntimeType != "acp" || identity.RuntimeBackend != "acp-live" || identity.RuntimeAgent != "codex" {
		t.Fatalf("expected runtime overrides in identity, got %+v", identity)
	}
	if identity.SubagentMaxSpawnDepth != 3 || identity.SubagentMaxChildren != 2 || identity.SubagentTimeoutSeconds != 44 {
		t.Fatalf("expected subagent defaults, got %+v", identity)
	}
	if got := strings.Join(identity.SkillFilter, ","); got != "deploy" {
		t.Fatalf("expected per-agent skill filter override, got %q", got)
	}
	if got := strings.Join(identity.ToolAllowlist, ","); got != "sessions_send,memory_search" {
		t.Fatalf("expected per-agent tool allow override, got %q", got)
	}
	if got := strings.Join(identity.ToolDenylist, ","); got != "memory_search" {
		t.Fatalf("expected per-agent tool deny override, got %q", got)
	}
	if len(identity.MemoryExtraPaths) != 1 || identity.MemoryExtraPaths[0] != extraMemoryFile {
		t.Fatalf("expected inherited memory extra path, got %+v", identity.MemoryExtraPaths)
	}
}

func TestWorkspaceMemoryProviderUsesAgentExtraPaths(t *testing.T) {
	workspace := t.TempDir()
	extraDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(extraDir, "facts.txt"), []byte("Outside fact token is OUTSIDE-42.\n"), 0o644); err != nil {
		t.Fatalf("write extra memory file: %v", err)
	}
	provider := memorypkg.NewWorkspaceMemoryProvider()
	hits, err := provider.Recall(context.Background(), core.AgentIdentity{
		WorkspaceDir:     workspace,
		MemoryExtraPaths: []string{extraDir},
	}, core.SessionResolution{}, "what is outside-42")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(hits) == 0 || !strings.Contains(hits[0].Snippet, "OUTSIDE-42") {
		t.Fatalf("expected hit from extra paths, got %+v", hits)
	}
}

func TestMemoryManagerFallsBackFromQMDToBuiltin(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, memorypkg.DefaultMemoryFilename), []byte("Atlas code is BLUE-SPARROW-17.\n"), 0o644); err != nil {
		t.Fatalf("write memory: %v", err)
	}
	manager := memorypkg.NewManager(config.AppConfig{})
	hits, err := manager.Recall(context.Background(), core.AgentIdentity{
		WorkspaceDir:          workspace,
		MemoryProvider:        "qmd",
		MemoryFallback:        "builtin",
		MemoryEnabled:         true,
		MemoryQueryMaxResults: 3,
	}, core.SessionResolution{}, "what is BLUE-SPARROW-17")
	if err != nil {
		t.Fatalf("recall fallback: %v", err)
	}
	if len(hits) == 0 || !strings.Contains(hits[0].Snippet, "BLUE-SPARROW-17") {
		t.Fatalf("expected builtin fallback hits, got %+v", hits)
	}
}

func TestMemoryManagerUsesHybridBuiltinRecall(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, memorypkg.DefaultMemoryFilename), []byte("Alpha release note.\nAtlas launch code BLUE-SPARROW-17.\n"), 0o644); err != nil {
		t.Fatalf("write memory: %v", err)
	}
	manager := memorypkg.NewManager(config.AppConfig{})
	hits, err := manager.Recall(context.Background(), core.AgentIdentity{
		WorkspaceDir:                workspace,
		MemoryEnabled:               true,
		MemoryHybridEnabled:         true,
		MemoryVectorEnabled:         true,
		MemoryHybridTextWeight:      0.6,
		MemoryHybridVectorWeight:    0.4,
		MemoryHybridCandidateFactor: 3,
		MemoryQueryMaxResults:       3,
	}, core.SessionResolution{}, "blue sparrow launch code")
	if err != nil {
		t.Fatalf("hybrid recall: %v", err)
	}
	if len(hits) == 0 || hits[0].Score <= 0 {
		t.Fatalf("expected hybrid hits, got %+v", hits)
	}
}

func TestBuildSystemPromptRespectsMemoryCitationModeOff(t *testing.T) {
	prompt := infra.BuildSystemPrompt(infra.PromptBuildParams{
		Identity: core.AgentIdentity{
			ID:                  "main",
			MemoryCitationsMode: "off",
		},
		MemoryHits: []core.MemoryHit{
			{Source: "MEMORY.md", FromLine: 2, ToLine: 4, Snippet: "Atlas code BLUE-SPARROW-17"},
		},
	})
	if strings.Contains(prompt, "[MEMORY.md:2-4]") {
		t.Fatalf("expected citations omitted when mode=off, got %q", prompt)
	}
	if !strings.Contains(prompt, "Atlas code BLUE-SPARROW-17") {
		t.Fatalf("expected snippet retained, got %q", prompt)
	}
}

func TestNewRuntimeFromConfigUsesSessionVisibilityAndA2AConfig(t *testing.T) {
	cfg := config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {
					BaseURL: "https://example.com/v1",
					API:     "openai-completions",
					Models:  []config.ProviderModelConfig{{ID: "gpt-4.1"}},
				},
			},
		},
		Session: config.SessionConfig{
			ToolsVisibility: core.SessionVisibilityAll,
			AgentToAgent: config.SessionAgentToAgentConfig{
				Enabled: true,
				Allow:   []string{"main", "worker"},
			},
		},
	}
	rt, err := NewRuntimeFromConfig(cfg, config.RuntimeConfigParams{StateDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new runtime from config: %v", err)
	}
	if rt.Policy.SessionToolsVisibility != core.SessionVisibilityAll {
		t.Fatalf("expected session visibility from config, got %q", rt.Policy.SessionToolsVisibility)
	}
	if !rt.Policy.AgentToAgent.Enabled || len(rt.Policy.AgentToAgent.Allow) != 2 {
		t.Fatalf("expected A2A policy from config, got %+v", rt.Policy.AgentToAgent)
	}
}

func TestBuildWorkspaceSkillStatusUsesConfigEntriesAndInstallPreferences(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "skills", "deploy"), 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	content := `---
name: deploy
description: Deploy helper
primary-env: DEPLOY_API_KEY
requires-env: DEPLOY_API_KEY
install-kind: node
install-package: deploy-helper
---
Use this skill.
`
	if err := os.WriteFile(filepath.Join(workspace, "skills", "deploy", "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	enabled := true
	cfg := &config.AppConfig{
		Skills: config.SkillsConfig{
			Install: config.SkillsInstallConfigLite{NodeManager: "pnpm"},
			Entries: map[string]config.SkillConfigLite{
				"deploy": {
					Enabled: &enabled,
					APIKey:  "secret-token",
				},
			},
		},
	}
	report, err := skill.BuildWorkspaceSkillStatus(workspace, &skill.WorkspaceSkillBuildOptions{Config: cfg, SkillFilter: []string{"deploy"}}, nil)
	if err != nil {
		t.Fatalf("build skill status: %v", err)
	}
	if len(report.Skills) != 1 {
		t.Fatalf("expected one skill, got %+v", report.Skills)
	}
	entry := report.Skills[0]
	if !entry.Eligible || entry.Disabled {
		t.Fatalf("expected eligible enabled skill, got %+v", entry)
	}
	if len(entry.Install) != 1 || !strings.Contains(entry.Install[0].Label, "pnpm") {
		t.Fatalf("expected install label to use configured node manager, got %+v", entry.Install)
	}
}

func TestLoadWorkspaceSkillEntriesSkipsDisabledConfiguredSkill(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "skills", "deploy"), 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "skills", "deploy", "SKILL.md"), []byte("description: Deploy\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	disabled := false
	entries, err := skill.LoadWorkspaceSkillEntries(workspace, &skill.WorkspaceSkillBuildOptions{
		SkillFilter: []string{"deploy"},
		Config: &config.AppConfig{
			Skills: config.SkillsConfig{
				Entries: map[string]config.SkillConfigLite{
					"deploy": {Enabled: &disabled},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("load skills: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected disabled skill to be skipped, got %+v", entries)
	}
}

func TestBuildWorkspaceSkillStatusIncludesDisabledConfiguredSkill(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "skills", "deploy"), 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "skills", "deploy", "SKILL.md"), []byte("description: Deploy\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	disabled := false
	report, err := skill.BuildWorkspaceSkillStatus(workspace, &skill.WorkspaceSkillBuildOptions{
		SkillFilter: []string{"deploy"},
		Config: &config.AppConfig{
			Skills: config.SkillsConfig{
				Entries: map[string]config.SkillConfigLite{
					"deploy": {Enabled: &disabled},
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("build skill status: %v", err)
	}
	if len(report.Skills) != 1 {
		t.Fatalf("expected disabled skill to still appear in status, got %+v", report.Skills)
	}
	if !report.Skills[0].Disabled {
		t.Fatalf("expected disabled skill in status report, got %+v", report.Skills[0])
	}
}

func TestLoadAppConfigParsesCommandBackendConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	raw := `{
  "models": {
    "providers": {
      "cli-live": {
        "api": "cli",
        "baseUrl": "https://example.com/v1",
        "apiKey": "test",
        "models": [{"id":"wrapped/live","name":"Wrapped","reasoning":true}],
        "command": {
          "command": "/bin/echo",
          "args": ["hello"],
          "resumeArgs": ["--resume", "{sessionId}"],
          "input": "stdin",
          "output": "jsonl",
          "promptArg": "--prompt",
          "systemPromptArg": "--system",
          "modelArg": "--model",
          "sessionArg": "--session",
          "sessionIdFields": ["session_id"],
          "systemPromptMode": "append",
          "workingDir": "/tmp",
          "overallTimeoutMs": 120000,
          "noOutputTimeout": "45s",
          "streamText": true,
          "sessionExpiredText": ["session expired"]
        }
      }
    }
  }
}`
	if err := os.WriteFile(configPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.LoadAppConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	command := cfg.Models.Providers["cli-live"].Command
	if command == nil {
		t.Fatalf("expected command backend config")
	}
	if command.InputMode != core.CommandBackendInputStdin || command.OutputMode != core.CommandBackendOutputJSONL {
		t.Fatalf("unexpected command modes: %+v", command)
	}
	if command.OverallTimeout != 120*time.Second || command.NoOutputTimeout != 45*time.Second {
		t.Fatalf("unexpected command timeouts: %+v", command)
	}
	if command.SessionArg != "--session" || len(command.SessionIDFields) != 1 {
		t.Fatalf("unexpected command config: %+v", command)
	}
}

func TestLoadWorkspaceSkillEntriesAppliesBundledAllowlist(t *testing.T) {
	workspace := t.TempDir()
	bundled := t.TempDir()
	for _, name := range []string{"keep-skill", "drop-skill"} {
		dir := filepath.Join(bundled, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir bundled skill: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("name: "+name+"\n"), 0o644); err != nil {
			t.Fatalf("write bundled skill: %v", err)
		}
	}
	entries, err := skill.LoadWorkspaceSkillEntries(workspace, &skill.WorkspaceSkillBuildOptions{
		BundledSkillsDir: bundled,
		SkillFilter:      []string{"keep-skill"},
		Config: &config.AppConfig{
			Skills: config.SkillsConfig{
				AllowBundled: []string{"keep-skill"},
			},
		},
	})
	if err != nil {
		t.Fatalf("load workspace skills: %v", err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	if strings.Join(names, ",") != "keep-skill" {
		t.Fatalf("expected bundled allowlist to filter skills, got %+v", names)
	}
}

func TestBuildWorkspaceSkillStatusMarksBundledSkillBlockedByAllowlist(t *testing.T) {
	entry := core.SkillEntry{
		Name:        "drop-skill",
		Description: "blocked",
		FilePath:    "bundled/drop-skill/SKILL.md",
		Metadata: &core.SkillMetadata{
			Source: "bundled",
		},
	}
	report, err := skill.BuildWorkspaceSkillStatus(t.TempDir(), &skill.WorkspaceSkillBuildOptions{
		Entries: []core.SkillEntry{entry},
		Config: &config.AppConfig{
			Skills: config.SkillsConfig{
				AllowBundled: []string{"other-skill"},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("build workspace skill status: %v", err)
	}
	if len(report.Skills) != 1 {
		t.Fatalf("expected one bundled skill in status, got %+v", report.Skills)
	}
	if !report.Skills[0].BlockedByAllowlist || report.Skills[0].Eligible {
		t.Fatalf("expected bundled skill to be blocked by allowlist, got %+v", report.Skills[0])
	}
}
