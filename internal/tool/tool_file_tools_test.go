package tool

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/core"
)

func testFileToolContext(t *testing.T) (string, ToolContext) {
	t.Helper()
	workspace := t.TempDir()
	return workspace, ToolContext{
		Run: AgentRunContext{
			WorkspaceDir: workspace,
			Identity: core.AgentIdentity{
				WorkspaceDir: workspace,
			},
		},
	}
}

func withMockToolUserHomeDir(t *testing.T, homeDir string) {
	t.Helper()
	prev := toolUserHomeDir
	toolUserHomeDir = func() (string, error) { return homeDir, nil }
	t.Cleanup(func() {
		toolUserHomeDir = prev
	})
}

func TestReadWriteEditTools(t *testing.T) {
	workspace, toolCtx := testFileToolContext(t)
	writeResult, err := NewWriteTool().Execute(context.Background(), toolCtx, map[string]any{
		"path":    "notes.txt",
		"content": "alpha\nbeta\ngamma\n",
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.Contains(string(writeResult.JSON), `"status":"ok"`) {
		t.Fatalf("unexpected write result: %s", writeResult.Text)
	}
	readResult, err := NewReadTool().Execute(context.Background(), toolCtx, map[string]any{
		"path":  "notes.txt",
		"from":  float64(2),
		"lines": float64(1),
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(readResult.Text, `"content":"beta"`) {
		t.Fatalf("unexpected read result: %s", readResult.Text)
	}
	_, err = NewEditTool().Execute(context.Background(), toolCtx, map[string]any{
		"path":    "notes.txt",
		"oldText": "beta",
		"newText": "delta",
	})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "notes.txt"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if !strings.Contains(string(data), "delta") {
		t.Fatalf("expected edited content, got %q", string(data))
	}
}

func TestLSToolAndFindTool(t *testing.T) {
	workspace, toolCtx := testFileToolContext(t)
	if err := os.MkdirAll(filepath.Join(workspace, "dir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "dir", "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "dir", "b.md"), []byte("world"), 0o644); err != nil {
		t.Fatalf("write b.md: %v", err)
	}
	lsResult, err := NewLSTool().Execute(context.Background(), toolCtx, map[string]any{"path": "dir"})
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	if !strings.Contains(lsResult.Text, `dir/a.txt`) || !strings.Contains(lsResult.Text, `dir/b.md`) {
		t.Fatalf("unexpected ls result: %s", lsResult.Text)
	}
	findResult, err := NewFindTool().Execute(context.Background(), toolCtx, map[string]any{
		"path": "dir",
		"glob": "*.txt",
	})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if !strings.Contains(findResult.Text, `dir/a.txt`) {
		t.Fatalf("unexpected find result: %s", findResult.Text)
	}
}

func TestGrepTool(t *testing.T) {
	workspace, toolCtx := testFileToolContext(t)
	if err := os.WriteFile(filepath.Join(workspace, "atlas.md"), []byte("Atlas\nBLUE-SPARROW-17\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	result, err := NewGrepTool().Execute(context.Background(), toolCtx, map[string]any{
		"pattern": "BLUE-.*",
	})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if !strings.Contains(result.Text, `BLUE-SPARROW-17`) {
		t.Fatalf("unexpected grep result: %s", result.Text)
	}
}

func TestApplyPatchTool(t *testing.T) {
	workspace, toolCtx := testFileToolContext(t)
	path := filepath.Join(workspace, "hello.txt")
	if err := os.WriteFile(path, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: hello.txt",
		"@@",
		" hello",
		"-world",
		"+kocort",
		"*** End Patch",
	}, "\n")
	result, err := NewApplyPatchTool().Execute(context.Background(), toolCtx, map[string]any{"patch": patch})
	if err != nil {
		t.Fatalf("apply_patch: %v", err)
	}
	if !strings.Contains(result.Text, `"status":"ok"`) {
		t.Fatalf("unexpected apply_patch result: %s", result.Text)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "hello\nkocort\n" {
		t.Fatalf("unexpected patched content: %q", string(data))
	}
}

func TestDisabledSandboxDoesNotOverrideWorkspaceDirectory(t *testing.T) {
	workspace := t.TempDir()
	sandboxDir := t.TempDir()
	toolCtx := ToolContext{
		Run: AgentRunContext{
			WorkspaceDir: workspace,
			Identity: core.AgentIdentity{
				WorkspaceDir:   workspace,
				SandboxEnabled: false,
			},
		},
		Sandbox: &SandboxContext{
			Enabled:      false,
			WorkspaceDir: sandboxDir,
		},
	}

	if _, err := NewWriteTool().Execute(context.Background(), toolCtx, map[string]any{
		"path":    "notes.txt",
		"content": "hello sandbox off",
	}); err != nil {
		t.Fatalf("write with disabled sandbox: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "notes.txt")); err != nil {
		t.Fatalf("expected file in workspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sandboxDir, "notes.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected no file in sandbox dir, err=%v", err)
	}

	patchTarget := filepath.Join(workspace, "patch.txt")
	if err := os.WriteFile(patchTarget, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("seed patch file: %v", err)
	}
	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: patch.txt",
		"@@",
		" hello",
		"-world",
		"+workspace",
		"*** End Patch",
	}, "\n")
	if _, err := NewApplyPatchTool().Execute(context.Background(), toolCtx, map[string]any{"patch": patch}); err != nil {
		t.Fatalf("apply_patch with disabled sandbox: %v", err)
	}
	data, err := os.ReadFile(patchTarget)
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if string(data) != "hello\nworkspace\n" {
		t.Fatalf("unexpected patched content in workspace file: %q", string(data))
	}
}

func TestWorkingDirectoryIsBaseNotBoundary(t *testing.T) {
	workspace, toolCtx := testFileToolContext(t)
	externalDir := t.TempDir()
	externalFile := filepath.Join(externalDir, "outside.txt")

	if _, err := NewWriteTool().Execute(context.Background(), toolCtx, map[string]any{
		"path":    externalFile,
		"content": "outside workspace",
	}); err != nil {
		t.Fatalf("write absolute path outside workdir: %v", err)
	}
	data, err := os.ReadFile(externalFile)
	if err != nil {
		t.Fatalf("read external file: %v", err)
	}
	if string(data) != "outside workspace" {
		t.Fatalf("unexpected external file content: %q", string(data))
	}

	result, err := NewLSTool().Execute(context.Background(), toolCtx, map[string]any{"path": externalDir})
	if err != nil {
		t.Fatalf("ls absolute external dir: %v", err)
	}
	if !strings.Contains(result.Text, filepath.ToSlash(externalFile)) {
		t.Fatalf("expected absolute external path in ls result, got %s", result.Text)
	}

	if _, err := NewWriteTool().Execute(context.Background(), toolCtx, map[string]any{
		"path":    "nested/inside.txt",
		"content": "inside workdir",
	}); err != nil {
		t.Fatalf("write relative path in workdir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "nested", "inside.txt")); err != nil {
		t.Fatalf("expected relative file under workdir: %v", err)
	}
}

func TestSandboxDirectoriesRestrictAccess(t *testing.T) {
	workspace := t.TempDir()
	allowedOne := t.TempDir()
	allowedTwo := t.TempDir()
	blockedDir := t.TempDir()
	toolCtx := ToolContext{
		Run: AgentRunContext{
			WorkspaceDir: workspace,
			Identity: core.AgentIdentity{
				WorkspaceDir:   workspace,
				SandboxEnabled: true,
				SandboxDirs:    []string{allowedOne, allowedTwo},
			},
		},
		Sandbox: &SandboxContext{
			Enabled:     true,
			SandboxDirs: []string{allowedOne, allowedTwo},
		},
	}

	if _, err := NewWriteTool().Execute(context.Background(), toolCtx, map[string]any{
		"path":    filepath.Join(allowedOne, "one.txt"),
		"content": "allowed one",
	}); err != nil {
		t.Fatalf("write allowed sandbox dir one: %v", err)
	}
	if _, err := NewWriteTool().Execute(context.Background(), toolCtx, map[string]any{
		"path":    filepath.Join(allowedTwo, "two.txt"),
		"content": "allowed two",
	}); err != nil {
		t.Fatalf("write allowed sandbox dir two: %v", err)
	}

	if _, err := NewWriteTool().Execute(context.Background(), toolCtx, map[string]any{
		"path":    filepath.Join(blockedDir, "blocked.txt"),
		"content": "blocked",
	}); err == nil || !strings.Contains(err.Error(), "outside sandbox directories") {
		t.Fatalf("expected sandbox restriction for blocked dir, got %v", err)
	}

	if _, err := NewWriteTool().Execute(context.Background(), toolCtx, map[string]any{
		"path":    "relative.txt",
		"content": "allowed in workdir",
	}); err != nil {
		t.Fatalf("expected workdir access even with sandbox enabled, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "relative.txt")); err != nil {
		t.Fatalf("expected relative file under workdir: %v", err)
	}
}

func TestExpandToolUserPathSupportsHomePrefixes(t *testing.T) {
	homeDir := t.TempDir()
	withMockToolUserHomeDir(t, homeDir)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "tilde only", input: "~", want: homeDir},
		{name: "unix separator", input: "~/Desktop", want: filepath.Join(homeDir, "Desktop")},
		{name: "windows separator", input: `~\\Desktop`, want: filepath.Join(homeDir, "Desktop")},
		{name: "nested mixed separators", input: `~/Documents\\Projects/demo`, want: filepath.Join(homeDir, "Documents", "Projects", "demo")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := expandToolUserPath(tc.input)
			if err != nil {
				t.Fatalf("expandToolUserPath(%q): %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("expandToolUserPath(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestLSToolResolvesHomeRelativeDirectory(t *testing.T) {
	_, toolCtx := testFileToolContext(t)
	homeDir := t.TempDir()
	withMockToolUserHomeDir(t, homeDir)

	desktopDir := filepath.Join(homeDir, "Desktop")
	if err := os.MkdirAll(desktopDir, 0o755); err != nil {
		t.Fatalf("mkdir desktop: %v", err)
	}
	notePath := filepath.Join(desktopDir, "note.txt")
	if err := os.WriteFile(notePath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}

	result, err := NewLSTool().Execute(context.Background(), toolCtx, map[string]any{"path": "~/Desktop"})
	if err != nil {
		t.Fatalf("ls home-relative dir: %v", err)
	}
	if !strings.Contains(result.Text, filepath.ToSlash(notePath)) {
		t.Fatalf("expected ls result to include %q, got %s", filepath.ToSlash(notePath), result.Text)
	}
}

func TestSandboxDirectoriesSupportHomeRelativeConfig(t *testing.T) {
	workspace := t.TempDir()
	homeDir := t.TempDir()
	withMockToolUserHomeDir(t, homeDir)

	allowedDir := filepath.Join(homeDir, "Desktop")
	if err := os.MkdirAll(allowedDir, 0o755); err != nil {
		t.Fatalf("mkdir allowed dir: %v", err)
	}
	toolCtx := ToolContext{
		Run: AgentRunContext{
			WorkspaceDir: workspace,
			Identity: core.AgentIdentity{
				WorkspaceDir:   workspace,
				SandboxEnabled: true,
				SandboxDirs:    []string{"~/Desktop"},
			},
		},
		Sandbox: &SandboxContext{
			Enabled:     true,
			SandboxDirs: []string{"~/Desktop"},
		},
	}

	targetPath := filepath.Join(allowedDir, "sandbox.txt")
	if _, err := NewWriteTool().Execute(context.Background(), toolCtx, map[string]any{
		"path":    targetPath,
		"content": "allowed",
	}); err != nil {
		t.Fatalf("write in home-relative sandbox dir: %v", err)
	}
	if _, err := os.Stat(targetPath); err != nil {
		t.Fatalf("expected file in expanded sandbox dir: %v", err)
	}
}

func TestNormalizeToolInputPathUsesAbsoluteWindowsPathsOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only path semantics")
	}
	workspace := filepath.Join(`C:\\workspace`, "demo")
	input := `D:\\Users\\alice\\Desktop`
	got, err := normalizeToolInputPath(workspace, input)
	if err != nil {
		t.Fatalf("normalizeToolInputPath: %v", err)
	}
	if got != filepath.Clean(input) {
		t.Fatalf("normalizeToolInputPath(%q) = %q, want %q", input, got, filepath.Clean(input))
	}
}
