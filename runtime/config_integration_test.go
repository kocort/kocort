package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/backend"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/skill"
	"github.com/kocort/kocort/internal/task"
	"github.com/kocort/kocort/internal/tool"

	"github.com/kocort/kocort/utils"
)

type backendFunc func(context.Context, rtypes.AgentRunContext) (core.AgentRunResult, error)

func (f backendFunc) Run(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
	return f(ctx, runCtx)
}

// latestResultText extracts the last non-empty text payload from a run result.
func latestResultText(result core.AgentRunResult) string {
	for i := len(result.Payloads) - 1; i >= 0; i-- {
		text := strings.TrimSpace(result.Payloads[i].Text)
		if text != "" {
			return text
		}
	}
	return ""
}

func TestLoadWorkspaceSkillEntriesRespectsSkillConfigPathRequirements(t *testing.T) {
	t.Cleanup(skill.ResetSkillsWatchersForTests)
	workspace := t.TempDir()
	skillDir := filepath.Join(workspace, "skills", "deploy")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	content := `---
name: deploy
requires-config: skills.entries.deploy.config.enabled
---
deploy skill
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	entries, err := skill.LoadWorkspaceSkillEntries(workspace, &skill.WorkspaceSkillBuildOptions{
		SkillFilter: []string{"deploy"},
		Config: &config.AppConfig{
			Skills: config.SkillsConfig{
				Entries: map[string]config.SkillConfigLite{
					"deploy": {Config: map[string]any{"enabled": true}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("load skills: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "deploy" {
		t.Fatalf("expected config-gated skill to load, got %+v", entries)
	}
}

func TestRuntimeAppliesSkillEnvOverridesForExplicitSkillDispatch(t *testing.T) {
	t.Cleanup(skill.ResetSkillsWatchersForTests)
	workspace := t.TempDir()
	skillDir := filepath.Join(workspace, "skills", "deploy")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	content := `---
name: deploy
command-name: deploy
command-dispatch: tool
command-tool: exec
primary-env: DEPLOY_API_KEY
requires-env: DEPLOY_API_KEY
---
deploy skill
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
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
		Skills: config.SkillsConfig{
			Entries: map[string]config.SkillConfigLite{
				"deploy": {APIKey: "SKILL-CONFIG-KEY"},
			},
		},
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{{
				ID:        "main",
				Default:   true,
				Workspace: workspace,
				Model:     config.AgentModelConfig{Primary: "openai/gpt-4.1"},
				Skills:    []string{"deploy"},
				Tools:     config.AgentToolPolicyConfig{Allow: []string{"exec"}},
			}},
		},
	}
	rt, err := NewRuntimeFromConfig(cfg, config.RuntimeConfigParams{StateDir: t.TempDir(), AgentID: "main", Deliverer: &delivery.MemoryDeliverer{}})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	rt.Backend = backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		t.Fatalf("backend should not run for explicit skill tool dispatch")
		return core.AgentRunResult{}, nil
	})
	result, err := rt.Run(context.Background(), core.AgentRunRequest{
		AgentID: "main",
		Message: "/deploy printf %s \"$DEPLOY_API_KEY\"",
		Channel: "test",
		To:      "user",
		Deliver: false,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.TrimSpace(latestResultText(result)); got != "SKILL-CONFIG-KEY" {
		t.Fatalf("expected skill api key env override, got %q", got)
	}
}

func TestCommandBackendInjectsAgentDirEnv(t *testing.T) {
	agentDir := filepath.Join(t.TempDir(), "agent")
	backend := &backend.CommandBackend{
		Config: core.CommandBackendConfig{
			Command:    "/bin/sh",
			Args:       []string{"-lc", "printf %s \"$KOCORT_AGENT_DIR|$PI_CODING_AGENT_DIR\""},
			InputMode:  core.CommandBackendInputStdin,
			OutputMode: core.CommandBackendOutputText,
		},
	}
	result, err := backend.Run(context.Background(), rtypes.AgentRunContext{
		Identity: core.AgentIdentity{ID: "main", AgentDir: agentDir},
		Request:  core.AgentRunRequest{Message: "noop", Timeout: 5 * time.Second},
		Session:  core.SessionResolution{SessionID: "sess-1", SessionKey: "agent:main:main"},
		ReplyDispatcher: delivery.NewReplyDispatcher(&delivery.MemoryDeliverer{}, core.DeliveryTarget{
			SessionKey: "agent:main:main",
		}),
	})
	if err != nil {
		t.Fatalf("command backend run: %v", err)
	}
	if got := strings.TrimSpace(latestResultText(result)); got != agentDir+"|"+agentDir {
		t.Fatalf("expected agent env injection, got %q", got)
	}
}

func TestSessionsSpawnUsesConfiguredSubagentModel(t *testing.T) {
	cfg := config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {
					BaseURL: "https://example.com/v1",
					API:     "openai-completions",
					Models: []config.ProviderModelConfig{
						{ID: "gpt-4.1"},
						{ID: "gpt-4.1-mini"},
					},
				},
			},
		},
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{
					ID:      "main",
					Default: true,
					Model:   config.AgentModelConfig{Primary: "openai/gpt-4.1"},
					Subagents: config.AgentSubagentConfig{
						AllowAgents: []string{"worker"},
					},
				},
				{
					ID:    "worker",
					Model: config.AgentModelConfig{Primary: "openai/gpt-4.1"},
					Subagents: config.AgentSubagentConfig{
						Model: config.AgentModelConfig{Primary: "openai/gpt-4.1-mini"},
					},
				},
			},
		},
	}
	rt, err := NewRuntimeFromConfig(cfg, config.RuntimeConfigParams{StateDir: t.TempDir(), AgentID: "main", Deliverer: &delivery.MemoryDeliverer{}})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	rt.Backends = nil
	seen := make(chan core.ModelCandidate, 1)
	rt.Backend = backendFunc(func(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
		if runCtx.Request.Lane == core.LaneSubagent {
			seen <- core.ModelCandidate{Provider: runCtx.ModelSelection.Provider, Model: runCtx.ModelSelection.Model}
		}
		return core.AgentRunResult{Payloads: []core.ReplyPayload{{Text: "ok"}}}, nil
	})
	identity, _ := rt.Identities.Resolve(context.Background(), "main")
	parentSession, _ := rt.Sessions.Resolve(context.Background(), "main", session.BuildMainSessionKey("main"), "", "", "")
	runCtx := rtypes.AgentRunContext{
		Runtime:      rt,
		Request:      core.AgentRunRequest{AgentID: "main"},
		Session:      parentSession,
		Identity:     identity,
		WorkspaceDir: t.TempDir(),
	}
	toolResult, err := tool.NewSessionsSpawnTool().Execute(context.Background(), rtypes.ToolContext{Runtime: rt, Run: runCtx}, map[string]any{
		"task":    "child",
		"agentId": "worker",
	})
	if err != nil {
		t.Fatalf("sessions_spawn: %v", err)
	}
	var payload task.SubagentSpawnResult
	if err := json.Unmarshal(toolResult.JSON, &payload); err != nil {
		t.Fatalf("unmarshal spawn result: %v", err)
	}
	select {
	case model := <-seen:
		if model.Provider != "openai" || model.Model != "gpt-4.1-mini" {
			t.Fatalf("expected configured subagent model, got %+v", model)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for subagent run")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		record := rt.Subagents.Get(payload.RunID)
		if record != nil && !record.EndedAt.IsZero() && !rt.ActiveRuns.IsActive(record.ChildSessionKey) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestSubagentRegistrySweepExpiresArchivedRunAndDeletesSession(t *testing.T) {
	rt, err := NewRuntimeFromConfig(config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {BaseURL: "https://example.com/v1", API: "openai-completions", Models: []config.ProviderModelConfig{{ID: "gpt-4.1"}}},
			},
		},
	}, config.RuntimeConfigParams{StateDir: t.TempDir(), Deliverer: &delivery.MemoryDeliverer{}})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	sessionKey, err := session.BuildSubagentSessionKey("main")
	if err != nil {
		t.Fatalf("build subagent session key: %v", err)
	}
	if err := rt.Sessions.Upsert(sessionKey, core.SessionEntry{SessionID: "sess-archive"}); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	rt.Subagents.Register(task.SubagentRunRecord{
		RunID:               "run-archive",
		ChildSessionKey:     sessionKey,
		RequesterSessionKey: session.BuildMainSessionKey("main"),
		Task:                "archive me",
		ArchiveAt:           time.Now().UTC().Add(-time.Minute),
	})
	rt.Subagents.SweepExpired()
	if entry := rt.Sessions.Entry(sessionKey); entry != nil {
		t.Fatalf("expected archived subagent session to be deleted, got %+v", entry)
	}
}

func TestNewRuntimeFromConfigAppliesGlobalToolElevatedAndSandboxConfig(t *testing.T) {
	cfg := config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"openai": {BaseURL: "https://example.com/v1", API: "openai-completions", Models: []config.ProviderModelConfig{{ID: "gpt-4.1"}}},
			},
		},
		Tools: config.ToolsConfig{
			Elevated: &config.ToolElevatedConfig{
				Enabled: utils.BoolPtr(true),
				AllowFrom: map[string][]string{
					"test": {"user-1"},
				},
			},
			Sandbox: &config.ToolSandboxConfig{
				Mode:                   "all",
				WorkspaceAccess:        "rw",
				SessionToolsVisibility: "spawned",
				Scope:                  "agent",
				WorkspaceRoot:          "/tmp/sandbox-root",
			},
		},
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{{
				ID:      "main",
				Default: true,
				Model:   config.AgentModelConfig{Primary: "openai/gpt-4.1"},
				Tools: config.AgentToolPolicyConfig{
					Profile: "coding",
				},
			}},
		},
	}
	rt, err := NewRuntimeFromConfig(cfg, config.RuntimeConfigParams{StateDir: t.TempDir(), Deliverer: &delivery.MemoryDeliverer{}})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	identity, err := rt.Identities.Resolve(context.Background(), "main")
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	if !identity.ElevatedEnabled || len(identity.ElevatedAllowFrom["test"]) != 1 {
		t.Fatalf("expected elevated config in identity, got %+v", identity)
	}
	if identity.SandboxMode != "all" || identity.SandboxSessionVisibility != "spawned" || identity.SandboxWorkspaceRoot != "/tmp/sandbox-root" {
		t.Fatalf("expected sandbox config in identity, got %+v", identity)
	}
}

func TestBuildWorkspaceSkillSnapshotWatchVersionBumpsOnSkillChange(t *testing.T) {
	t.Cleanup(skill.ResetSkillsWatchersForTests)
	workspace := t.TempDir()
	skillDir := filepath.Join(workspace, "skills", "deploy")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("name: deploy\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	watchEnabled := true
	cfg := &config.AppConfig{
		Skills: config.SkillsConfig{
			Load: config.SkillsLoadConfig{
				Watch:           &watchEnabled,
				WatchDebounceMs: 25,
			},
		},
	}
	first, err := skill.BuildWorkspaceSkillSnapshot(workspace, nil, cfg)
	if err != nil {
		t.Fatalf("build first snapshot: %v", err)
	}
	if err := os.WriteFile(skillPath, []byte("name: deploy\ndescription: changed\n"), 0o644); err != nil {
		t.Fatalf("rewrite skill: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		next, buildErr := skill.BuildWorkspaceSkillSnapshot(workspace, nil, cfg)
		if buildErr != nil {
			t.Fatalf("build next snapshot: %v", buildErr)
		}
		if next.Version > first.Version {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("expected watcher to bump snapshot version above %d", first.Version)
}

func TestBuildWorkspaceSkillSnapshotWatchDefaultsEnabled(t *testing.T) {
	t.Cleanup(skill.ResetSkillsWatchersForTests)
	workspace := t.TempDir()
	skillDir := filepath.Join(workspace, "skills", "deploy")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("name: deploy\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	first, err := skill.BuildWorkspaceSkillSnapshot(workspace, nil, &config.AppConfig{})
	if err != nil {
		t.Fatalf("build first snapshot: %v", err)
	}
	if err := os.WriteFile(skillPath, []byte("name: deploy\ndescription: changed\n"), 0o644); err != nil {
		t.Fatalf("rewrite skill: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		next, buildErr := skill.BuildWorkspaceSkillSnapshot(workspace, nil, &config.AppConfig{})
		if buildErr != nil {
			t.Fatalf("build next snapshot: %v", buildErr)
		}
		if next.Version > first.Version {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("expected default watcher to bump snapshot version above %d", first.Version)
}

func TestConfiguredAgentAllowsArchiveAfterMinutesZeroOverride(t *testing.T) {
	archiveDefault := 60
	archiveDisabled := 0
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
		Agents: config.AgentsConfig{
			Defaults: &config.AgentDefaultsConfig{
				Model: config.AgentModelConfig{Primary: "openai/gpt-4.1"},
				Subagents: config.AgentSubagentConfig{
					ArchiveAfterMinutes: &archiveDefault,
				},
			},
			List: []config.AgentConfig{
				{
					ID:      "main",
					Default: true,
				},
				{
					ID: "worker",
					Subagents: config.AgentSubagentConfig{
						ArchiveAfterMinutes: &archiveDisabled,
					},
				},
			},
		},
	}
	identity, err := config.BuildConfiguredAgentIdentity(cfg, t.TempDir(), "worker", "", "", "")
	if err != nil {
		t.Fatalf("build identity: %v", err)
	}
	if identity.SubagentArchiveAfterMinutes != 0 {
		t.Fatalf("expected explicit zero archive override, got %d", identity.SubagentArchiveAfterMinutes)
	}
}

func TestSkillConfigPathTruthySupportsNestedInterfaceMaps(t *testing.T) {
	cfg := &config.AppConfig{
		Skills: config.SkillsConfig{
			Entries: map[string]config.SkillConfigLite{
				"deploy": {
					Config: map[string]any{
						"flags": map[string]any{
							"enabled": true,
						},
					},
				},
			},
		},
	}
	if !skill.IsSkillConfigPathTruthy(cfg, "skills.entries.deploy.config.flags.enabled") {
		t.Fatal("expected nested config path to resolve truthy")
	}
}
