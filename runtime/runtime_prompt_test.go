package runtime

import (
	"context"
	"fmt"
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

func TestIdentityResolverLoadsIdentityMarkdownFromWorkspace(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	if _, err := infra.EnsureWorkspaceDir(workspace); err != nil {
		t.Fatalf("ensure workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, infra.DefaultIdentityFilename), []byte("Name: Library Bot\nEmoji: :books:\nTheme: paper\n"), 0o644); err != nil {
		t.Fatalf("write identity: %v", err)
	}
	resolver := infra.NewStaticIdentityResolver(map[string]core.AgentIdentity{
		"main": {
			ID:              "main",
			WorkspaceDir:    workspace,
			DefaultProvider: "openai",
			DefaultModel:    "gpt-4.1",
		},
	})
	identity, err := resolver.Resolve(context.Background(), "main")
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	if identity.Name != "Library Bot" {
		t.Fatalf("expected identity file name, got %+v", identity)
	}
	if !strings.Contains(identity.PersonaPrompt, "Identity theme: paper") {
		t.Fatalf("expected persona prompt from identity file, got %q", identity.PersonaPrompt)
	}
}

func TestWorkspaceMemoryProviderRecallsMemoryFiles(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	if _, err := infra.EnsureWorkspaceDir(workspace); err != nil {
		t.Fatalf("ensure workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, memorypkg.DefaultMemoryFilename), []byte("Project codename is Atlas.\nUse blue status labels."), 0o644); err != nil {
		t.Fatalf("write memory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "memory"), 0o755); err != nil {
		t.Fatalf("mkdir memory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "memory", "deploy.md"), []byte("Deployments happen every Friday at 18:00 UTC."), 0o644); err != nil {
		t.Fatalf("write memory file: %v", err)
	}
	provider := memorypkg.NewWorkspaceMemoryProvider()
	hits, err := provider.Recall(context.Background(), core.AgentIdentity{WorkspaceDir: workspace}, core.SessionResolution{}, "When is Atlas deployed?")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected at least one memory hit")
	}
	joined := hits[0].Snippet
	if !strings.Contains(strings.ToLower(joined), "atlas") && !strings.Contains(strings.ToLower(joined), "deploy") {
		t.Fatalf("unexpected top memory hit: %+v", hits[0])
	}
}

func TestWorkspaceMemoryProviderReturnsLineRanges(t *testing.T) {
	workspaceDir := t.TempDir()
	content := strings.Join([]string{
		"# Atlas",
		"",
		"Launch checklist:",
		"- confirm fuel",
		"- confirm BLUE-SPARROW-17",
		"- notify ops",
		"",
		"Fallback code is RED-KITE-2.",
	}, "\n")
	if err := os.WriteFile(filepath.Join(workspaceDir, memorypkg.DefaultMemoryFilename), []byte(content), 0o644); err != nil {
		t.Fatalf("write memory: %v", err)
	}
	provider := memorypkg.NewWorkspaceMemoryProvider()
	hits, err := provider.Recall(context.Background(), core.AgentIdentity{WorkspaceDir: workspaceDir}, core.SessionResolution{}, "what is BLUE-SPARROW-17")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected hits")
	}
	if hits[0].FromLine == 0 || hits[0].ToLine < hits[0].FromLine {
		t.Fatalf("expected line range metadata, got %+v", hits[0])
	}
	if hits[0].Path != memorypkg.DefaultMemoryFilename {
		t.Fatalf("expected path metadata, got %+v", hits[0])
	}
}

func TestBuildSystemPromptIncludesTranscriptAndMemory(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, infra.DefaultAgentsFilename), []byte("Be concise.\nUse checklists when needed."), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	prompt := infra.BuildSystemPrompt(infra.PromptBuildParams{
		Identity: core.AgentIdentity{
			ID:              "main",
			Name:            "Archivist",
			PersonaPrompt:   "You are kocort.",
			WorkspaceDir:    workspace,
			DefaultProvider: "openai",
			DefaultModel:    "gpt-4.1",
		},
		ModelSelection: core.ModelSelection{Provider: "openai", Model: "gpt-4.1-mini", ThinkLevel: "high"},
		History: []core.TranscriptMessage{
			{Role: "user", Text: "hello"},
			{Role: "assistant", Text: "hi"},
		},
		MemoryHits: []core.MemoryHit{
			{Source: "MEMORY.md", Snippet: "Project codename Atlas"},
		},
		Request: core.AgentRunRequest{Message: "[Tue 2026-03-10 10:00 UTC] hello"},
		Tools: []infra.PromptTool{
			tool.NewMemorySearchTool(),
		},
		Skills: &core.SkillSnapshot{
			Prompt: "<available_skills>\n<skill>\n<name>deploy</name>\n<description>Deploy checklist</description>\n<location>~/skills/deploy/SKILL.md</location>\n</skill>\n</available_skills>",
			Commands: []core.SkillCommandSpec{{
				Name:        "deploy",
				SkillName:   "deploy",
				Description: "Deploy checklist",
			}},
		},
	})
	if !strings.Contains(prompt, "Recent conversation:") {
		t.Fatalf("expected transcript section, got %q", prompt)
	}
	if !strings.Contains(prompt, "[MEMORY.md] Project codename Atlas") {
		t.Fatalf("expected memory section, got %q", prompt)
	}
	if !strings.Contains(prompt, "Agent name: Archivist") {
		t.Fatalf("expected identity section, got %q", prompt)
	}
	if !strings.Contains(prompt, "Workspace AGENTS.md:") {
		t.Fatalf("expected AGENTS.md section, got %q", prompt)
	}
	if !strings.Contains(prompt, "Current user message:") {
		t.Fatalf("expected current message section, got %q", prompt)
	}
	if !strings.Contains(prompt, "## Skills (mandatory)") {
		t.Fatalf("expected skills section, got %q", prompt)
	}
	if !strings.Contains(prompt, "## Tooling") || !strings.Contains(prompt, "memory_search") {
		t.Fatalf("expected tools section, got %q", prompt)
	}
	if !strings.Contains(prompt, "Runtime: agent=main") || !strings.Contains(prompt, "thinking=high") {
		t.Fatalf("expected runtime line, got %q", prompt)
	}
	if !strings.Contains(prompt, "User-invocable skill commands:") || !strings.Contains(prompt, "/deploy -> deploy") {
		t.Fatalf("expected skill commands section, got %q", prompt)
	}
}

func TestBuildSystemPromptIncludesInternalEventsContextFilesAndWarnings(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte(strings.Repeat("R", 9000)), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "CONTEXT.md"), []byte("Context rules.\nBe precise."), 0o644); err != nil {
		t.Fatalf("write CONTEXT.md: %v", err)
	}
	contextFiles, warnings := memorypkg.LoadPromptContextFiles(workspace, core.ChatTypeDirect, false)
	prompt := infra.BuildSystemPrompt(infra.PromptBuildParams{
		Identity: core.AgentIdentity{ID: "main"},
		Request:  core.AgentRunRequest{Message: "hello"},
		InternalEvents: []core.TranscriptMessage{
			{Type: "internal", Event: "subagent_wake", Text: "All descendants settled."},
			{Type: "system", Text: "Retry the final answer now."},
		},
		ContextFiles:      contextFiles,
		BootstrapWarnings: warnings,
	})
	if !strings.Contains(prompt, "## Internal Events") || !strings.Contains(prompt, "All descendants settled.") {
		t.Fatalf("expected internal events section, got %q", prompt)
	}
	if !strings.Contains(prompt, "## Context Files") || !strings.Contains(prompt, "Workspace README (README.md):") {
		t.Fatalf("expected context files section, got %q", prompt)
	}
	if !strings.Contains(prompt, "[truncated]") {
		t.Fatalf("expected truncated context file marker, got %q", prompt)
	}
	if !strings.Contains(prompt, "## Bootstrap Warnings") {
		t.Fatalf("expected bootstrap warnings section, got %q", prompt)
	}
}

func TestBuildSystemPromptIncludesAttachmentContents(t *testing.T) {
	prompt := infra.BuildSystemPrompt(infra.PromptBuildParams{
		Identity: core.AgentIdentity{ID: "main"},
		Request: core.AgentRunRequest{
			Message: "review attachments",
			Attachments: []core.Attachment{
				{
					Type:     "file",
					Name:     "notes.md",
					MIMEType: "text/markdown",
					Content:  []byte("# Notes\nShip it.\n"),
				},
				{
					Type:     "file",
					Name:     "archive.zip",
					MIMEType: "application/zip",
					Content:  []byte{0x50, 0x4b, 0x03, 0x04},
				},
			},
		},
	})
	if !strings.Contains(prompt, "Attachments:") || !strings.Contains(prompt, "notes.md [text/markdown]") {
		t.Fatalf("expected attachment summary, got %q", prompt)
	}
	if !strings.Contains(prompt, "Attachment content: notes.md") || !strings.Contains(prompt, "# Notes\nShip it.") {
		t.Fatalf("expected text attachment content, got %q", prompt)
	}
	if !strings.Contains(prompt, "archive.zip [application/zip] (binary") {
		t.Fatalf("expected binary attachment note, got %q", prompt)
	}
}

func TestBuildWorkspaceSkillSnapshotLoadsWorkspaceSkills(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "skills", "deploy"), 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillBody := `---
name: deploy
description: Deployment checklist
---
# Deploy

Use this when deploying.
`
	if err := os.WriteFile(filepath.Join(workspace, "skills", "deploy", skill.DefaultSkillFilename), []byte(skillBody), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}
	snapshot, err := skill.BuildWorkspaceSkillSnapshot(workspace, []string{"deploy"})
	if err != nil {
		t.Fatalf("build skill snapshot: %v", err)
	}
	if snapshot == nil || len(snapshot.Skills) != 1 {
		t.Fatalf("expected one skill, got %+v", snapshot)
	}
	if snapshot.Skills[0].Name != "deploy" {
		t.Fatalf("unexpected skill entry: %+v", snapshot.Skills[0])
	}
	if snapshot.Skills[0].ResolvedPath == "" {
		t.Fatalf("expected resolved path, got %+v", snapshot.Skills[0])
	}
	if !strings.Contains(snapshot.Prompt, "<available_skills>") || !strings.Contains(snapshot.Prompt, "Deployment checklist") {
		t.Fatalf("unexpected skills prompt: %q", snapshot.Prompt)
	}
	if len(snapshot.Commands) != 1 || snapshot.Commands[0].Name != "deploy" {
		t.Fatalf("unexpected skill commands: %+v", snapshot.Commands)
	}
	if len(snapshot.ResolvedName) != 1 || snapshot.ResolvedName[0] != "deploy" {
		t.Fatalf("unexpected resolved names: %+v", snapshot.ResolvedName)
	}
}

func TestBuildWorkspaceSkillSnapshotUsesSkillNameForCommandGeneration(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "skills", "deploy-skill"), 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillBody := `---
name: deploy-skill
command-name: deploy
description: Deployment checklist
---
# Deploy
`
	if err := os.WriteFile(filepath.Join(workspace, "skills", "deploy-skill", skill.DefaultSkillFilename), []byte(skillBody), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}
	snapshot, err := skill.BuildWorkspaceSkillSnapshot(workspace, []string{"deploy-skill"})
	if err != nil {
		t.Fatalf("build skill snapshot: %v", err)
	}
	if len(snapshot.Commands) != 1 || snapshot.Commands[0].Name != "deploy_skill" {
		t.Fatalf("expected command derived from skill name, got %+v", snapshot.Commands)
	}
}

func TestBuildSystemPromptDoesNotInjectSelectedSkillContent(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "skills", "deploy"), 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillPath := filepath.Join(workspace, "skills", "deploy", skill.DefaultSkillFilename)
	skillBody := `---
name: deploy
description: Deployment checklist
---
# Deploy

When invoked, reply with exactly AGENT-OK and nothing else.
`
	if err := os.WriteFile(skillPath, []byte(skillBody), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}
	snapshot, err := skill.BuildWorkspaceSkillSnapshot(workspace, []string{"deploy"})
	if err != nil {
		t.Fatalf("build skill snapshot: %v", err)
	}
	prompt := infra.BuildSystemPrompt(infra.PromptBuildParams{
		Identity: core.AgentIdentity{ID: "main"},
		Request:  core.AgentRunRequest{Message: "[Tue 2026-03-10 10:00 UTC] /deploy now"},
		Skills:   snapshot,
	})
	if strings.Contains(prompt, "## Selected Skill") {
		t.Fatalf("did not expect selected skill section, got %q", prompt)
	}
}

func TestBuildSystemPromptMinimalModeKeepsSkillsButSkipsExtendedSections(t *testing.T) {
	prompt := infra.BuildSystemPrompt(infra.PromptBuildParams{
		Identity: core.AgentIdentity{ID: "main"},
		Request:  core.AgentRunRequest{Message: "hello", ExtraSystemPrompt: "Subagent details"},
		Skills: &core.SkillSnapshot{
			Prompt: "<available_skills>\n<skill>\n<name>deploy</name>\n</skill>\n</available_skills>",
		},
		Mode: infra.PromptModeMinimal,
	})
	if !strings.Contains(prompt, "## Skills (mandatory)") {
		t.Fatalf("expected skills section, got %q", prompt)
	}
	if strings.Contains(prompt, "## Reply Tags") || strings.Contains(prompt, "## Messaging") || strings.Contains(prompt, "## Documentation") {
		t.Fatalf("did not expect extended sections in minimal mode, got %q", prompt)
	}
	if !strings.Contains(prompt, "## Subagent Context") || !strings.Contains(prompt, "Subagent details") {
		t.Fatalf("expected subagent context section, got %q", prompt)
	}
}

func TestBuildWorkspaceSkillSnapshotRespectsPromptLimitsAndTruncates(t *testing.T) {
	t.Cleanup(skill.ResetSkillsWatchersForTests)
	workspace := t.TempDir()
	for i := 0; i < 4; i++ {
		name := fmt.Sprintf("skill-%d", i)
		dir := filepath.Join(workspace, "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir skill dir: %v", err)
		}
		body := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n# %s\n%s\n", name, strings.Repeat("desc", 20), name, strings.Repeat("body", 50))
		if err := os.WriteFile(filepath.Join(dir, skill.DefaultSkillFilename), []byte(body), 0o644); err != nil {
			t.Fatalf("write skill: %v", err)
		}
	}
	cfg := &config.AppConfig{
		Skills: config.SkillsConfig{
			Limits: config.SkillsLimitsConfig{
				MaxSkillsInPrompt:    2,
				MaxSkillsPromptChars: 300,
			},
		},
	}
	snapshot, err := skill.BuildWorkspaceSkillSnapshot(workspace, nil, cfg)
	if err != nil {
		t.Fatalf("build skill snapshot: %v", err)
	}
	if !strings.Contains(snapshot.Prompt, "⚠️ Skills truncated") {
		t.Fatalf("expected truncated prompt warning, got %q", snapshot.Prompt)
	}
	for i := 0; i < 4; i++ {
		name := fmt.Sprintf("skill-%d", i)
		found := false
		for _, entry := range snapshot.Skills {
			if entry.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected snapshot to include %s, got %+v", name, snapshot.Skills)
		}
	}
}

func TestLoadWorkspaceSkillEntriesRespectsSourcePrecedenceAndFileSizeLimit(t *testing.T) {
	workspace := t.TempDir()
	managed := filepath.Join(t.TempDir(), "managed")
	bundled := filepath.Join(t.TempDir(), "bundled")
	extra := filepath.Join(t.TempDir(), "extra")
	for _, root := range []string{managed, bundled, extra, filepath.Join(workspace, "skills")} {
		dir := filepath.Join(root, "deploy")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir skill dir: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(extra, "deploy", skill.DefaultSkillFilename), []byte("---\nname: deploy\ndescription: extra\n---\nextra"), 0o644); err != nil {
		t.Fatalf("write extra skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundled, "deploy", skill.DefaultSkillFilename), []byte("---\nname: deploy\ndescription: bundled\n---\nbundled"), 0o644); err != nil {
		t.Fatalf("write bundled skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(managed, "deploy", skill.DefaultSkillFilename), []byte("---\nname: deploy\ndescription: managed\n---\nmanaged"), 0o644); err != nil {
		t.Fatalf("write managed skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "skills", "deploy", skill.DefaultSkillFilename), []byte("---\nname: deploy\ndescription: workspace\n---\nworkspace"), 0o644); err != nil {
		t.Fatalf("write workspace skill: %v", err)
	}
	oversizedDir := filepath.Join(workspace, "skills", "big")
	if err := os.MkdirAll(oversizedDir, 0o755); err != nil {
		t.Fatalf("mkdir oversized skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oversizedDir, skill.DefaultSkillFilename), []byte(strings.Repeat("x", 2048)), 0o644); err != nil {
		t.Fatalf("write oversized skill: %v", err)
	}
	cfg := &config.AppConfig{
		Skills: config.SkillsConfig{
			Load: config.SkillsLoadConfig{ExtraDirs: []string{extra}},
			Limits: config.SkillsLimitsConfig{
				MaxSkillFileBytes: 256,
			},
		},
	}
	entries, err := skill.LoadWorkspaceSkillEntries(workspace, &skill.WorkspaceSkillBuildOptions{
		Config:           cfg,
		ManagedSkillsDir: managed,
		BundledSkillsDir: bundled,
	})
	if err != nil {
		t.Fatalf("load skills: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one merged skill entry, got %+v", entries)
	}
	if entries[0].Description != "workspace" {
		t.Fatalf("expected workspace precedence, got %+v", entries[0])
	}
}

func TestLoadWorkspaceSkillEntriesParsesMetadataAndInstallSpec(t *testing.T) {
	workspace := t.TempDir()
	dir := filepath.Join(workspace, "skills", "ops")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	body := `---
name: ops
description: Ops helper
primary-env: OPS_TOKEN
emoji: 🛠
homepage: https://example.com/ops
os: darwin,linux
requires-bins: git,rg
requires-any-bins: fd,fdfind
requires-env: OPS_TOKEN
install-kind: go
install-id: install-go
install-module: example.com/ops@latest
---
# Ops
`
	if err := os.WriteFile(filepath.Join(dir, skill.DefaultSkillFilename), []byte(body), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	entries, err := skill.LoadWorkspaceSkillEntries(workspace, nil)
	if err != nil {
		t.Fatalf("load skills: %v", err)
	}
	var ops *core.SkillEntry
	for i := range entries {
		if entries[i].Name == "ops" {
			ops = &entries[i]
			break
		}
	}
	if ops == nil || ops.Metadata == nil {
		t.Fatalf("expected ops metadata, got %+v", entries)
	}
	if ops.Metadata.PrimaryEnv != "OPS_TOKEN" || ops.Metadata.Emoji != "🛠" {
		t.Fatalf("unexpected metadata: %+v", ops.Metadata)
	}
	if len(ops.Metadata.Install) != 1 || ops.Metadata.Install[0].Kind != "go" || ops.Metadata.Install[0].Module != "example.com/ops@latest" {
		t.Fatalf("unexpected install spec: %+v", ops.Metadata.Install)
	}
}

func TestBuildWorkspaceSkillStatusUsesRemoteEligibilityAndInstallOptions(t *testing.T) {
	workspace := t.TempDir()
	dir := filepath.Join(workspace, "skills", "mac-skill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	body := `---
name: mac-skill
description: Remote-only skill
os: darwin
requires-bins: safari
install-kind: brew
install-formula: safari-helper
---
# Mac
`
	if err := os.WriteFile(filepath.Join(dir, skill.DefaultSkillFilename), []byte(body), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	report, err := skill.BuildWorkspaceSkillStatus(workspace, nil, &core.SkillEligibilityContext{
		Remote: &core.SkillRemoteEligibility{
			Platforms: []string{"darwin"},
			HasBin: func(bin string) bool {
				return bin == "safari"
			},
			HasAnyBin: func(bins []string) bool { return false },
		},
	})
	if err != nil {
		t.Fatalf("build status: %v", err)
	}
	var found *core.SkillStatusEntry
	for i := range report.Skills {
		if report.Skills[i].Name == "mac-skill" {
			found = &report.Skills[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected mac-skill in report: %+v", report)
	}
	if !found.Eligible {
		t.Fatalf("expected remote eligibility to satisfy bins, got %+v", found)
	}
	if len(found.Install) != 1 || found.Install[0].Kind != "brew" {
		t.Fatalf("unexpected install options: %+v", found.Install)
	}
}

func TestInstallSkillRunsConfiguredInstaller(t *testing.T) {
	workspace := t.TempDir()
	dir := filepath.Join(workspace, "skills", "echo-skill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	body := `---
name: echo-skill
description: Echo installer
install-kind: go
install-id: install-go
install-module: example.com/echo@latest
---
# Echo
`
	if err := os.WriteFile(filepath.Join(dir, skill.DefaultSkillFilename), []byte(body), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	origPath := os.Getenv("PATH")
	tmpBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpBin, "go"), []byte("#!/bin/sh\necho GO-INSTALL-OK\n"), 0o755); err != nil {
		t.Fatalf("write fake go: %v", err)
	}
	if err := os.Setenv("PATH", tmpBin+string(os.PathListSeparator)+origPath); err != nil {
		t.Fatalf("set path: %v", err)
	}
	defer os.Setenv("PATH", origPath)

	result, err := skill.InstallSkill(context.Background(), skill.SkillInstallRequest{
		WorkspaceDir: workspace,
		SkillName:    "echo-skill",
		InstallID:    "install-go",
		Timeout:      10 * time.Second,
	})
	if err != nil {
		t.Fatalf("install skill: %v", err)
	}
	if !result.OK || !strings.Contains(result.Stdout, "GO-INSTALL-OK") {
		t.Fatalf("unexpected install result: %+v", result)
	}
}

func TestResolveInstallSpecPrefersConfiguredInstallerChainWhenInstallIDEmpty(t *testing.T) {
	preferBrew := false
	spec := skill.ResolveInstallSpec(
		core.SkillEntry{
			Name: "echo-skill",
			Metadata: &core.SkillMetadata{
				Install: []core.SkillInstallSpec{
					{Kind: "brew", ID: "install-brew", Formula: "fake-tool"},
					{Kind: "node", ID: "install-node", Package: "fake-node"},
				},
			},
		},
		"",
		core.SkillInstallPreferences{PreferBrew: preferBrew, NodeManager: "npm"},
	)
	if spec == nil {
		t.Fatal("expected preferred install spec")
	}
	if spec.Kind != "node" || spec.ID != "install-node" {
		t.Fatalf("expected node installer to win, got %+v", *spec)
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

func TestResolveSkillCommandInvocationParsesTimestampedSlashCommand(t *testing.T) {
	snapshot := &core.SkillSnapshot{
		Commands: []core.SkillCommandSpec{{
			Name:      "deploy",
			SkillName: "deploy",
		}},
	}
	invocation := skill.ResolveSkillCommandInvocation(snapshot, "[Tue 2026-03-10 10:00 UTC] /deploy echo OK")
	if invocation == nil {
		t.Fatal("expected invocation")
	}
	if invocation.Command.Name != "deploy" || invocation.Args != "echo OK" {
		t.Fatalf("unexpected invocation: %+v", invocation)
	}
}

func TestResolveSkillCommandInvocationParsesSlashSkillAlias(t *testing.T) {
	snapshot := &core.SkillSnapshot{
		Commands: []core.SkillCommandSpec{{
			Name:      "deploy",
			SkillName: "deploy",
		}},
	}
	invocation := skill.ResolveSkillCommandInvocation(snapshot, "/skill deploy echo OK")
	if invocation == nil {
		t.Fatal("expected invocation")
	}
	if invocation.Command.Name != "deploy" || invocation.Args != "echo OK" {
		t.Fatalf("unexpected invocation: %+v", invocation)
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

func TestBuildWorkspaceSkillSnapshotParsesCommandDispatch(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "skills", "tool-dispatch"), 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillBody := `---
name: tool-dispatch
description: Dispatch to a tool
command-dispatch: tool
command-tool: sessions_send
---
# Tool Dispatch
`
	if err := os.WriteFile(filepath.Join(workspace, "skills", "tool-dispatch", skill.DefaultSkillFilename), []byte(skillBody), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}
	snapshot, err := skill.BuildWorkspaceSkillSnapshot(workspace, []string{"tool-dispatch"})
	if err != nil {
		t.Fatalf("build skill snapshot: %v", err)
	}
	if len(snapshot.Commands) != 1 {
		t.Fatalf("expected one filtered command spec, got %+v", snapshot.Commands)
	}
	if snapshot.Commands[0].Dispatch == nil {
		t.Fatalf("expected dispatch metadata, got %+v", snapshot.Commands[0])
	}
	if snapshot.Commands[0].Dispatch.Kind != "tool" || snapshot.Commands[0].Dispatch.ToolName != "sessions_send" || snapshot.Commands[0].Dispatch.ArgMode != "raw" {
		t.Fatalf("unexpected dispatch: %+v", snapshot.Commands[0].Dispatch)
	}
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
