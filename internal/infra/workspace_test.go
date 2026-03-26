package infra

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestResolveDefaultAgentWorkspaceDir(t *testing.T) {
	t.Run("main_agent", func(t *testing.T) {
		result := ResolveDefaultAgentWorkspaceDir("main")
		if !filepath.IsAbs(result) && result != filepath.Join(".kocort", "workspace") {
			// Either absolute or relative form is fine
		}
		if filepath.Base(result) != "workspace" {
			t.Errorf("expected workspace dir to end with 'workspace', got %q", result)
		}
	})

	t.Run("other_agent", func(t *testing.T) {
		result := ResolveDefaultAgentWorkspaceDir("worker")
		base := filepath.Base(result)
		if base != "workspace-worker" {
			t.Errorf("expected workspace-worker, got %q", base)
		}
	})
}

func TestResolveDefaultAgentDir(t *testing.T) {
	dir := t.TempDir()
	result := ResolveDefaultAgentDir(dir, "main")
	expected := filepath.Join(dir, "agents", "main", "agent")
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestResolveDefaultAgentWorkspaceDirForState(t *testing.T) {
	t.Run("with_state_dir_main", func(t *testing.T) {
		result := ResolveDefaultAgentWorkspaceDirForState("/tmp/state", "main")
		if result != filepath.Join("/tmp/state", "workspace") {
			t.Errorf("got %q", result)
		}
	})

	t.Run("with_state_dir_other", func(t *testing.T) {
		result := ResolveDefaultAgentWorkspaceDirForState("/tmp/state", "worker")
		if result != filepath.Join("/tmp/state", "workspace-worker") {
			t.Errorf("got %q", result)
		}
	})
}

func TestEnsureWorkspaceDir(t *testing.T) {
	t.Run("creates_dir", func(t *testing.T) {
		base := t.TempDir()
		target := filepath.Join(base, "sub", "workspace")
		result, err := EnsureWorkspaceDir(target)
		if err != nil {
			t.Fatal(err)
		}
		if result == "" {
			t.Error("result should not be empty")
		}
		info, err := os.Stat(result)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			t.Error("expected directory")
		}
	})

	t.Run("empty_dir", func(t *testing.T) {
		result, err := EnsureWorkspaceDir("")
		if err != nil {
			t.Fatal(err)
		}
		if result != "" {
			t.Errorf("expected empty, got %q", result)
		}
	})

	t.Run("whitespace_dir", func(t *testing.T) {
		result, err := EnsureWorkspaceDir("   ")
		if err != nil {
			t.Fatal(err)
		}
		if result != "" {
			t.Errorf("expected empty, got %q", result)
		}
	})
}

func TestEnsureAgentDir(t *testing.T) {
	t.Run("creates_dir", func(t *testing.T) {
		base := t.TempDir()
		target := filepath.Join(base, "agents", "main")
		result, err := EnsureAgentDir(target)
		if err != nil {
			t.Fatal(err)
		}
		if result == "" {
			t.Error("result should not be empty")
		}
	})

	t.Run("empty", func(t *testing.T) {
		result, err := EnsureAgentDir("")
		if err != nil {
			t.Fatal(err)
		}
		if result != "" {
			t.Error("expected empty")
		}
	})
}

func TestLoadWorkspaceTextFile(t *testing.T) {
	t.Run("reads_file", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "test.md"), []byte("hello"), 0o644); err != nil {
			t.Fatal(err)
		}
		content, err := LoadWorkspaceTextFile(dir, "test.md")
		if err != nil {
			t.Fatal(err)
		}
		if content != "hello" {
			t.Errorf("got %q, want %q", content, "hello")
		}
	})

	t.Run("missing_file", func(t *testing.T) {
		dir := t.TempDir()
		content, err := LoadWorkspaceTextFile(dir, "nonexistent.md")
		if err != nil {
			t.Fatal(err)
		}
		if content != "" {
			t.Errorf("missing file should return empty string")
		}
	})

	t.Run("empty_workspace", func(t *testing.T) {
		content, err := LoadWorkspaceTextFile("", "test.md")
		if err != nil {
			t.Fatal(err)
		}
		if content != "" {
			t.Error("expected empty")
		}
	})

	t.Run("empty_filename", func(t *testing.T) {
		content, err := LoadWorkspaceTextFile(t.TempDir(), "")
		if err != nil {
			t.Fatal(err)
		}
		if content != "" {
			t.Error("expected empty")
		}
	})
}

func TestListWorkspaceMemoryFiles(t *testing.T) {
	t.Run("empty_dir", func(t *testing.T) {
		files, err := ListWorkspaceMemoryFiles("")
		if err != nil {
			t.Fatal(err)
		}
		if files != nil {
			t.Error("expected nil")
		}
	})

	t.Run("with_memory_files", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, DefaultMemoryFilename), []byte("mem1"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, DefaultMemoryAltFile), []byte("mem2"), 0o644); err != nil {
			t.Fatal(err)
		}
		files, err := ListWorkspaceMemoryFiles(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(files) != 2 {
			t.Fatalf("expected 2 files, got %d", len(files))
		}
	})

	t.Run("with_memory_directory", func(t *testing.T) {
		dir := t.TempDir()
		memDir := filepath.Join(dir, "memory")
		if err := os.MkdirAll(memDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(memDir, "notes.md"), []byte("note"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(memDir, ".hidden"), []byte("hidden"), 0o644); err != nil {
			t.Fatal(err)
		}
		files, err := ListWorkspaceMemoryFiles(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(files) != 1 {
			t.Fatalf("expected 1 (hidden excluded), got %d", len(files))
		}
	})

	t.Run("sorted_output", func(t *testing.T) {
		dir := t.TempDir()
		memDir := filepath.Join(dir, "memory")
		if err := os.MkdirAll(memDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"c.md", "a.md", "b.md"} {
			if err := os.WriteFile(filepath.Join(memDir, name), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		files, err := ListWorkspaceMemoryFiles(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !sort.StringsAreSorted(files) {
			t.Errorf("expected sorted output, got %v", files)
		}
	})
}
