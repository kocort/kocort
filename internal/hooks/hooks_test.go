package hooks

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRegisterAndTrigger(t *testing.T) {
	reg := NewRegistry()
	called := false
	reg.Register("tool:post_execute", func(_ context.Context, e *Event) error {
		called = true
		if e.Action != "post_execute" {
			t.Errorf("unexpected action: %s", e.Action)
		}
		return nil
	})
	event := NewEvent(EventTool, "post_execute", "sess:1", map[string]any{"toolName": "exec"})
	reg.Trigger(context.Background(), event)
	if !called {
		t.Error("handler was not called")
	}
}

func TestTypeWildcardHandler(t *testing.T) {
	reg := NewRegistry()
	var actions []string
	reg.Register("tool", func(_ context.Context, e *Event) error {
		actions = append(actions, e.Action)
		return nil
	})
	reg.Trigger(context.Background(), NewEvent(EventTool, "pre_execute", "s1", nil))
	reg.Trigger(context.Background(), NewEvent(EventTool, "post_execute", "s1", nil))
	if len(actions) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(actions))
	}
	if actions[0] != "pre_execute" || actions[1] != "post_execute" {
		t.Errorf("unexpected actions: %v", actions)
	}
}

func TestClear(t *testing.T) {
	reg := NewRegistry()
	reg.Register("agent:bootstrap", func(_ context.Context, _ *Event) error {
		t.Error("should not be called after clear")
		return nil
	})
	reg.Clear()
	reg.Trigger(context.Background(), NewEvent(EventAgent, "bootstrap", "s1", nil))
}

func TestHasHandlers(t *testing.T) {
	reg := NewRegistry()
	if reg.HasHandlers(EventTool, "post_execute") {
		t.Error("expected no handlers")
	}
	reg.Register("tool:post_execute", func(_ context.Context, _ *Event) error { return nil })
	if !reg.HasHandlers(EventTool, "post_execute") {
		t.Error("expected handlers")
	}
}

func TestEventMessagesMutable(t *testing.T) {
	reg := NewRegistry()
	reg.Register("agent:bootstrap", func(_ context.Context, e *Event) error {
		e.Messages = append(e.Messages, "Review .learnings/ for past errors")
		return nil
	})
	event := NewEvent(EventAgent, "bootstrap", "s1", nil)
	reg.Trigger(context.Background(), event)
	if len(event.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(event.Messages))
	}
	if event.Messages[0] != "Review .learnings/ for past errors" {
		t.Errorf("unexpected message: %s", event.Messages[0])
	}
}

// --- HOOK.md discovery tests ---

func TestParseHookMDEventsInline(t *testing.T) {
	dir := t.TempDir()
	hookMD := filepath.Join(dir, "HOOK.md")
	os.WriteFile(hookMD, []byte("---\nevents: [agent:bootstrap, tool:post_execute]\n---\n# My Hook\n"), 0644)

	events := parseHookMDEvents(hookMD)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0] != "agent:bootstrap" || events[1] != "tool:post_execute" {
		t.Errorf("unexpected events: %v", events)
	}
}

func TestParseHookMDEventsBlock(t *testing.T) {
	dir := t.TempDir()
	hookMD := filepath.Join(dir, "HOOK.md")
	content := "---\nname: my-hook\nevents:\n  - agent:bootstrap\n  - session:start\n---\n"
	os.WriteFile(hookMD, []byte(content), 0644)

	events := parseHookMDEvents(hookMD)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0] != "agent:bootstrap" || events[1] != "session:start" {
		t.Errorf("unexpected events: %v", events)
	}
}

func TestParseHookMDEventsQuoted(t *testing.T) {
	dir := t.TempDir()
	hookMD := filepath.Join(dir, "HOOK.md")
	os.WriteFile(hookMD, []byte("---\nevents: [\"agent:bootstrap\", 'tool:post_execute']\n---\n"), 0644)

	events := parseHookMDEvents(hookMD)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0] != "agent:bootstrap" || events[1] != "tool:post_execute" {
		t.Errorf("unexpected events: %v", events)
	}
}

func TestParseHookMDEventsNoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	hookMD := filepath.Join(dir, "HOOK.md")
	os.WriteFile(hookMD, []byte("# Just markdown\nNo frontmatter here.\n"), 0644)

	events := parseHookMDEvents(hookMD)
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %v", events)
	}
}

func TestParseHookMDEventsMissing(t *testing.T) {
	events := parseHookMDEvents(filepath.Join(t.TempDir(), "nonexistent.md"))
	if len(events) != 0 {
		t.Errorf("expected 0 events for missing file, got %v", events)
	}
}

func TestDiscoverHandlerScript(t *testing.T) {
	dir := t.TempDir()

	// No handler files → empty
	if got := discoverHandlerScript(dir); got != "" {
		t.Errorf("expected empty, got %s", got)
	}

	// Create handler.sh → should be found
	handlerPath := filepath.Join(dir, "handler.sh")
	os.WriteFile(handlerPath, []byte("#!/bin/bash\necho hello"), 0755)
	if got := discoverHandlerScript(dir); got != handlerPath {
		t.Errorf("expected %s, got %s", handlerPath, got)
	}
}

func TestDiscoverSkillHooksIntegration(t *testing.T) {
	// Build a fake skill directory with hooks/my-hook/{HOOK.md, handler.sh}
	skillDir := t.TempDir()
	hookDir := filepath.Join(skillDir, "hooks", "my-hook")
	os.MkdirAll(hookDir, 0755)
	os.WriteFile(filepath.Join(hookDir, "HOOK.md"),
		[]byte("---\nevents: [agent:bootstrap]\n---\n"), 0644)
	os.WriteFile(filepath.Join(hookDir, "handler.sh"),
		[]byte("#!/bin/bash\necho activated"), 0755)

	entries := DiscoverSkillHooks(map[string]string{
		"test-skill": skillDir,
	}, nil)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.SkillName != "test-skill" {
		t.Errorf("unexpected skill name: %s", e.SkillName)
	}
	if len(e.Events) != 1 || e.Events[0] != "agent:bootstrap" {
		t.Errorf("unexpected events: %v", e.Events)
	}
	if filepath.Base(e.ScriptPath) != "handler.sh" {
		t.Errorf("unexpected script: %s", e.ScriptPath)
	}
	if e.BaseDir != hookDir {
		t.Errorf("unexpected base dir: %s", e.BaseDir)
	}
}

func TestDiscoverSkillHooksNoHookMD(t *testing.T) {
	// Hook directory without HOOK.md should be skipped.
	skillDir := t.TempDir()
	hookDir := filepath.Join(skillDir, "hooks", "orphan")
	os.MkdirAll(hookDir, 0755)
	os.WriteFile(filepath.Join(hookDir, "handler.sh"),
		[]byte("#!/bin/bash\necho orphan"), 0755)

	entries := DiscoverSkillHooks(map[string]string{
		"test-skill": skillDir,
	}, nil)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for hookdir without HOOK.md, got %d", len(entries))
	}
}

func TestDiscoverSkillHooksNoHandler(t *testing.T) {
	// HOOK.md without handler script should be skipped.
	skillDir := t.TempDir()
	hookDir := filepath.Join(skillDir, "hooks", "no-handler")
	os.MkdirAll(hookDir, 0755)
	os.WriteFile(filepath.Join(hookDir, "HOOK.md"),
		[]byte("---\nevents: [agent:bootstrap]\n---\n"), 0644)

	entries := DiscoverSkillHooks(map[string]string{
		"test-skill": skillDir,
	}, nil)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for HOOK.md without handler, got %d", len(entries))
	}
}

func TestRegisterSkillHooksCount(t *testing.T) {
	reg := NewRegistry()
	entries := []SkillHookEntry{
		{SkillName: "s1", HookName: "test-hook", BaseDir: "/tmp", ScriptPath: "/tmp/h.sh", Events: []string{"agent:bootstrap", "tool:post_execute"}},
	}
	count := RegisterSkillHooks(reg, entries, nil)
	if count != 2 {
		t.Errorf("expected 2 registrations, got %d", count)
	}
	if !reg.HasHandlers(EventAgent, "bootstrap") {
		t.Error("expected agent:bootstrap handler")
	}
	if !reg.HasHandlers(EventTool, "post_execute") {
		t.Error("expected tool:post_execute handler")
	}
}

// --- OS / requires / per-hook config tests ---

func TestIsOSEligible(t *testing.T) {
	// Empty list = all eligible.
	if !isOSEligible(nil) {
		t.Error("nil OS list should be eligible")
	}
	if !isOSEligible([]string{}) {
		t.Error("empty OS list should be eligible")
	}
	// Current OS should match.
	if !isOSEligible([]string{currentOS()}) {
		t.Errorf("current OS %q should be eligible", currentOS())
	}
	// Non-matching OS only.
	if isOSEligible([]string{"plan9"}) {
		t.Error("plan9 should not be eligible on current OS")
	}
}

func TestCheckRequires(t *testing.T) {
	// nil requires = ok.
	if reason := checkRequires(nil); reason != "" {
		t.Errorf("nil requires should pass, got %q", reason)
	}
	// Empty requires = ok.
	if reason := checkRequires(&HookMDRequires{}); reason != "" {
		t.Errorf("empty requires should pass, got %q", reason)
	}
	// Missing binary.
	if reason := checkRequires(&HookMDRequires{Bins: []string{"__nonexistent_binary_xyz__"}}); reason == "" {
		t.Error("should fail for missing binary")
	}
	// Missing env var.
	if reason := checkRequires(&HookMDRequires{Env: []string{"__NONEXISTENT_ENV_VAR_XYZ__"}}); reason == "" {
		t.Error("should fail for missing env var")
	}
	// anyBins: none available.
	if reason := checkRequires(&HookMDRequires{AnyBins: []string{"__nonexistent_a__", "__nonexistent_b__"}}); reason == "" {
		t.Error("should fail when no anyBins are available")
	}
}

func TestDiscoverSkillHooksDisabledByConfig(t *testing.T) {
	skillDir := t.TempDir()
	hookDir := filepath.Join(skillDir, "hooks", "disabled-hook")
	os.MkdirAll(hookDir, 0755)
	os.WriteFile(filepath.Join(hookDir, "HOOK.md"),
		[]byte("---\nevents: [agent:bootstrap]\n---\n"), 0644)
	os.WriteFile(filepath.Join(hookDir, "handler.sh"),
		[]byte("#!/bin/bash\necho disabled"), 0755)

	disabled := false
	entries := DiscoverSkillHooks(map[string]string{
		"test-skill": skillDir,
	}, map[string]HookEntryConfig{
		"disabled-hook": {Enabled: &disabled},
	})
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for disabled hook, got %d", len(entries))
	}
}

func TestDiscoverSkillHooksOSFilter(t *testing.T) {
	skillDir := t.TempDir()
	hookDir := filepath.Join(skillDir, "hooks", "os-filtered")
	os.MkdirAll(hookDir, 0755)
	os.WriteFile(filepath.Join(hookDir, "HOOK.md"),
		[]byte("---\nevents: [agent:bootstrap]\nos: [plan9]\n---\n"), 0644)
	os.WriteFile(filepath.Join(hookDir, "handler.sh"),
		[]byte("#!/bin/bash\necho filtered"), 0755)

	entries := DiscoverSkillHooks(map[string]string{
		"test-skill": skillDir,
	}, nil)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for OS-filtered hook, got %d", len(entries))
	}
}

func TestDiscoverSkillHooksRequiresMissing(t *testing.T) {
	skillDir := t.TempDir()
	hookDir := filepath.Join(skillDir, "hooks", "needs-bin")
	os.MkdirAll(hookDir, 0755)
	os.WriteFile(filepath.Join(hookDir, "HOOK.md"),
		[]byte("---\nevents: [agent:bootstrap]\nrequires:\n  bins: [__nonexistent_binary_xyz__]\n---\n"), 0644)
	os.WriteFile(filepath.Join(hookDir, "handler.sh"),
		[]byte("#!/bin/bash\necho needs-bin"), 0755)

	entries := DiscoverSkillHooks(map[string]string{
		"test-skill": skillDir,
	}, nil)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for hook with missing binary requirement, got %d", len(entries))
	}
}

func TestParseHookMDFullMetadata(t *testing.T) {
	dir := t.TempDir()
	hookMD := filepath.Join(dir, "HOOK.md")
	content := "---\nevents: [agent:bootstrap]\nos: [linux, darwin]\nrequires:\n  bins: [git]\n  env: [HOME]\n---\n"
	os.WriteFile(hookMD, []byte(content), 0644)

	fm := parseHookMDFull(hookMD)
	if len(fm.Events) != 1 || fm.Events[0] != "agent:bootstrap" {
		t.Errorf("unexpected events: %v", fm.Events)
	}
	if len(fm.Metadata.OS) != 2 {
		t.Errorf("expected 2 OS entries, got %v", fm.Metadata.OS)
	}
	if fm.Metadata.Requires == nil {
		t.Fatal("expected requires to be non-nil")
	}
	if len(fm.Metadata.Requires.Bins) != 1 || fm.Metadata.Requires.Bins[0] != "git" {
		t.Errorf("unexpected bins: %v", fm.Metadata.Requires.Bins)
	}
	if len(fm.Metadata.Requires.Env) != 1 || fm.Metadata.Requires.Env[0] != "HOME" {
		t.Errorf("unexpected env: %v", fm.Metadata.Requires.Env)
	}
}
