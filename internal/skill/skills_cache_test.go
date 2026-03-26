package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/config"
)

func TestBuildWorkspaceSkillSnapshotUsesVersionedCache(t *testing.T) {
	ResetSkillsWatchersForTests()

	workspace := t.TempDir()
	skillPath := filepath.Join(workspace, "skills", "deploy", DefaultSkillFilename)
	writeTestSkillFile(t, skillPath, "First description")

	cfg := &config.AppConfig{}
	first, err := BuildWorkspaceSkillSnapshot(workspace, nil, cfg)
	if err != nil {
		t.Fatalf("BuildWorkspaceSkillSnapshot first: %v", err)
	}
	if !strings.Contains(first.Prompt, "First description") {
		t.Fatalf("expected first prompt to include initial skill content, got %q", first.Prompt)
	}

	writeTestSkillFile(t, skillPath, "Second description")

	cached, err := BuildWorkspaceSkillSnapshot(workspace, nil, cfg)
	if err != nil {
		t.Fatalf("BuildWorkspaceSkillSnapshot cached: %v", err)
	}
	if !strings.Contains(cached.Prompt, "First description") {
		t.Fatalf("expected cached snapshot to retain initial content before version bump, got %q", cached.Prompt)
	}

	BumpSkillsSnapshotVersion(workspace, skillPath, "test")

	reloaded, err := BuildWorkspaceSkillSnapshot(workspace, nil, cfg)
	if err != nil {
		t.Fatalf("BuildWorkspaceSkillSnapshot reloaded: %v", err)
	}
	if !strings.Contains(reloaded.Prompt, "Second description") {
		t.Fatalf("expected bumped snapshot to include updated content, got %q", reloaded.Prompt)
	}
	if reloaded.Version == cached.Version {
		t.Fatalf("expected version bump to change snapshot version, got %d", reloaded.Version)
	}
}

func TestBuildWorkspaceSkillStatusUsesVersionedCache(t *testing.T) {
	ResetSkillsWatchersForTests()

	workspace := t.TempDir()
	skillPath := filepath.Join(workspace, "skills", "triage", DefaultSkillFilename)
	writeTestSkillFile(t, skillPath, "Initial triage description")

	cfg := &config.AppConfig{}
	first, err := BuildWorkspaceSkillStatus(workspace, &WorkspaceSkillBuildOptions{Config: cfg}, nil)
	if err != nil {
		t.Fatalf("BuildWorkspaceSkillStatus first: %v", err)
	}
	if len(first.Skills) != 1 || first.Skills[0].Description != "Initial triage description" {
		t.Fatalf("unexpected initial status report: %+v", first)
	}

	writeTestSkillFile(t, skillPath, "Updated triage description")

	cached, err := BuildWorkspaceSkillStatus(workspace, &WorkspaceSkillBuildOptions{Config: cfg}, nil)
	if err != nil {
		t.Fatalf("BuildWorkspaceSkillStatus cached: %v", err)
	}
	if len(cached.Skills) != 1 || cached.Skills[0].Description != "Initial triage description" {
		t.Fatalf("expected cached report to retain initial content before version bump, got %+v", cached)
	}

	BumpSkillsSnapshotVersion(workspace, skillPath, "test")

	reloaded, err := BuildWorkspaceSkillStatus(workspace, &WorkspaceSkillBuildOptions{Config: cfg}, nil)
	if err != nil {
		t.Fatalf("BuildWorkspaceSkillStatus reloaded: %v", err)
	}
	if len(reloaded.Skills) != 1 || reloaded.Skills[0].Description != "Updated triage description" {
		t.Fatalf("expected bumped report to include updated content, got %+v", reloaded)
	}
	if reloaded.Version == cached.Version {
		t.Fatalf("expected status version to change after bump, got %d", reloaded.Version)
	}
}

func writeTestSkillFile(t *testing.T, path string, description string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	content := strings.Join([]string{
		"---",
		"name: test-skill",
		"description: " + description,
		"---",
		"",
		"# Test Skill",
		"",
		"Body",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
