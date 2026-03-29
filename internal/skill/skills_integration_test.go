package skill

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/config"
)

func TestLoadWorkspaceSkillEntriesRespectsSkillConfigPathRequirements(t *testing.T) {
	t.Cleanup(ResetSkillsWatchersForTests)
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
	entries, err := LoadWorkspaceSkillEntries(workspace, &WorkspaceSkillBuildOptions{
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

func TestBuildWorkspaceSkillSnapshotWatchVersionBumpsOnSkillChange(t *testing.T) {
	t.Cleanup(ResetSkillsWatchersForTests)
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
	first, err := BuildWorkspaceSkillSnapshot(workspace, nil, cfg)
	if err != nil {
		t.Fatalf("build first snapshot: %v", err)
	}
	if err := os.WriteFile(skillPath, []byte("name: deploy\ndescription: changed\n"), 0o644); err != nil {
		t.Fatalf("rewrite skill: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		next, buildErr := BuildWorkspaceSkillSnapshot(workspace, nil, cfg)
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
	t.Cleanup(ResetSkillsWatchersForTests)
	workspace := t.TempDir()
	skillDir := filepath.Join(workspace, "skills", "deploy")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("name: deploy\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	first, err := BuildWorkspaceSkillSnapshot(workspace, nil, &config.AppConfig{})
	if err != nil {
		t.Fatalf("build first snapshot: %v", err)
	}
	if err := os.WriteFile(skillPath, []byte("name: deploy\ndescription: changed\n"), 0o644); err != nil {
		t.Fatalf("rewrite skill: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		next, buildErr := BuildWorkspaceSkillSnapshot(workspace, nil, &config.AppConfig{})
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
	if !IsSkillConfigPathTruthy(cfg, "skills.entries.deploy.config.flags.enabled") {
		t.Fatal("expected nested config path to resolve truthy")
	}
}
