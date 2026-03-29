package skill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
)

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
	if err := os.WriteFile(filepath.Join(workspace, "skills", "deploy", DefaultSkillFilename), []byte(skillBody), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}
	snapshot, err := BuildWorkspaceSkillSnapshot(workspace, []string{"deploy"})
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
	if err := os.WriteFile(filepath.Join(workspace, "skills", "deploy-skill", DefaultSkillFilename), []byte(skillBody), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}
	snapshot, err := BuildWorkspaceSkillSnapshot(workspace, []string{"deploy-skill"})
	if err != nil {
		t.Fatalf("build skill snapshot: %v", err)
	}
	if len(snapshot.Commands) != 1 || snapshot.Commands[0].Name != "deploy_skill" {
		t.Fatalf("expected command derived from skill name, got %+v", snapshot.Commands)
	}
}

func TestBuildWorkspaceSkillSnapshotRespectsPromptLimitsAndTruncates(t *testing.T) {
	t.Cleanup(ResetSkillsWatchersForTests)
	workspace := t.TempDir()
	for i := 0; i < 4; i++ {
		name := fmt.Sprintf("skill-%d", i)
		dir := filepath.Join(workspace, "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir skill dir: %v", err)
		}
		body := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n# %s\n%s\n", name, strings.Repeat("desc", 20), name, strings.Repeat("body", 50))
		if err := os.WriteFile(filepath.Join(dir, DefaultSkillFilename), []byte(body), 0o644); err != nil {
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
	snapshot, err := BuildWorkspaceSkillSnapshot(workspace, nil, cfg)
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
	if err := os.WriteFile(filepath.Join(extra, "deploy", DefaultSkillFilename), []byte("---\nname: deploy\ndescription: extra\n---\nextra"), 0o644); err != nil {
		t.Fatalf("write extra skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundled, "deploy", DefaultSkillFilename), []byte("---\nname: deploy\ndescription: bundled\n---\nbundled"), 0o644); err != nil {
		t.Fatalf("write bundled skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(managed, "deploy", DefaultSkillFilename), []byte("---\nname: deploy\ndescription: managed\n---\nmanaged"), 0o644); err != nil {
		t.Fatalf("write managed skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "skills", "deploy", DefaultSkillFilename), []byte("---\nname: deploy\ndescription: workspace\n---\nworkspace"), 0o644); err != nil {
		t.Fatalf("write workspace skill: %v", err)
	}
	oversizedDir := filepath.Join(workspace, "skills", "big")
	if err := os.MkdirAll(oversizedDir, 0o755); err != nil {
		t.Fatalf("mkdir oversized skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oversizedDir, DefaultSkillFilename), []byte(strings.Repeat("x", 2048)), 0o644); err != nil {
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
	entries, err := LoadWorkspaceSkillEntries(workspace, &WorkspaceSkillBuildOptions{
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
	if err := os.WriteFile(filepath.Join(dir, DefaultSkillFilename), []byte(body), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	entries, err := LoadWorkspaceSkillEntries(workspace, nil)
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
	if err := os.WriteFile(filepath.Join(dir, DefaultSkillFilename), []byte(body), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	report, err := BuildWorkspaceSkillStatus(workspace, nil, &core.SkillEligibilityContext{
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
	if err := os.WriteFile(filepath.Join(dir, DefaultSkillFilename), []byte(body), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	origPath := os.Getenv("PATH")
	tmpBin := t.TempDir()
	stubName := "go"
	stubBody := "#!/bin/sh\necho GO-INSTALL-OK\n"
	if stdruntime.GOOS == "windows" {
		stubName = "go.cmd"
		stubBody = "@echo off\r\necho GO-INSTALL-OK\r\n"
	}
	if err := os.WriteFile(filepath.Join(tmpBin, stubName), []byte(stubBody), 0o755); err != nil {
		t.Fatalf("write fake go: %v", err)
	}
	if err := os.Setenv("PATH", tmpBin+string(os.PathListSeparator)+origPath); err != nil {
		t.Fatalf("set path: %v", err)
	}
	defer os.Setenv("PATH", origPath)

	result, err := InstallSkill(context.Background(), SkillInstallRequest{
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
	spec := ResolveInstallSpec(
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

func TestResolveSkillCommandInvocationParsesTimestampedSlashCommand(t *testing.T) {
	snapshot := &core.SkillSnapshot{
		Commands: []core.SkillCommandSpec{{
			Name:      "deploy",
			SkillName: "deploy",
		}},
	}
	invocation := ResolveSkillCommandInvocation(snapshot, "[Tue 2026-03-10 10:00 UTC] /deploy echo OK")
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
	invocation := ResolveSkillCommandInvocation(snapshot, "/skill deploy echo OK")
	if invocation == nil {
		t.Fatal("expected invocation")
	}
	if invocation.Command.Name != "deploy" || invocation.Args != "echo OK" {
		t.Fatalf("unexpected invocation: %+v", invocation)
	}
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
	if err := os.WriteFile(filepath.Join(workspace, "skills", "tool-dispatch", DefaultSkillFilename), []byte(skillBody), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}
	snapshot, err := BuildWorkspaceSkillSnapshot(workspace, []string{"tool-dispatch"})
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
