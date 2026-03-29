package infra_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/infra"
	memorypkg "github.com/kocort/kocort/internal/memory"
	"github.com/kocort/kocort/internal/skill"
	"github.com/kocort/kocort/internal/tool"
)

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
