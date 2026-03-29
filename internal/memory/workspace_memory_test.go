package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/core"
)

func TestWorkspaceMemoryProviderRecallsMemoryFiles(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, DefaultMemoryFilename), []byte("Project codename is Atlas.\nUse blue status labels."), 0o644); err != nil {
		t.Fatalf("write memory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "memory"), 0o755); err != nil {
		t.Fatalf("mkdir memory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "memory", "deploy.md"), []byte("Deployments happen every Friday at 18:00 UTC."), 0o644); err != nil {
		t.Fatalf("write memory file: %v", err)
	}
	provider := NewWorkspaceMemoryProvider()
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
	if err := os.WriteFile(filepath.Join(workspaceDir, DefaultMemoryFilename), []byte(content), 0o644); err != nil {
		t.Fatalf("write memory: %v", err)
	}
	provider := NewWorkspaceMemoryProvider()
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
	if hits[0].Path != DefaultMemoryFilename {
		t.Fatalf("expected path metadata, got %+v", hits[0])
	}
}
