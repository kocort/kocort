package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/core"
)

func TestMemoryGetToolReadsWorkspaceMemoryFile(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "MEMORY.md"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("write memory: %v", err)
	}
	result, err := NewMemoryGetTool().Execute(context.Background(), ToolContext{
		Run: AgentRunContext{
			Identity: core.AgentIdentity{WorkspaceDir: workspace},
		},
	}, map[string]any{
		"path":  "MEMORY.md",
		"from":  float64(2),
		"lines": float64(1),
	})
	if err != nil {
		t.Fatalf("execute memory_get: %v", err)
	}
	var payload struct {
		Path  string `json:"path"`
		Text  string `json:"text"`
		From  int    `json:"from"`
		Lines int    `json:"lines"`
	}
	if unmarshalErr := json.Unmarshal(result.JSON, &payload); unmarshalErr != nil {
		t.Fatalf("unmarshal result: %v", unmarshalErr)
	}
	if payload.Path != "MEMORY.md" || payload.Text != "two" || payload.From != 2 || payload.Lines != 1 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestMemoryGetToolReadsConfiguredExtraFile(t *testing.T) {
	workspace := t.TempDir()
	extraFile := filepath.Join(t.TempDir(), "extra.md")
	if err := os.WriteFile(extraFile, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatalf("write extra file: %v", err)
	}
	result, err := NewMemoryGetTool().Execute(context.Background(), ToolContext{
		Run: AgentRunContext{
			Identity: core.AgentIdentity{
				WorkspaceDir:     workspace,
				MemoryExtraPaths: []string{extraFile},
			},
		},
	}, map[string]any{
		"path": extraFile,
	})
	if err != nil {
		t.Fatalf("execute memory_get: %v", err)
	}
	var payload struct {
		Path string `json:"path"`
		Text string `json:"text"`
	}
	if unmarshalErr := json.Unmarshal(result.JSON, &payload); unmarshalErr != nil {
		t.Fatalf("unmarshal result: %v", unmarshalErr)
	}
	if payload.Path != filepath.ToSlash(extraFile) || !strings.Contains(payload.Text, "alpha") {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestMemoryGetToolReadsConfiguredExtraDirectoryChild(t *testing.T) {
	workspace := t.TempDir()
	extraDir := t.TempDir()
	child := filepath.Join(extraDir, "notes.md")
	if err := os.WriteFile(child, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("write extra child: %v", err)
	}
	result, err := NewMemoryGetTool().Execute(context.Background(), ToolContext{
		Run: AgentRunContext{
			Identity: core.AgentIdentity{
				WorkspaceDir:     workspace,
				MemoryExtraPaths: []string{extraDir},
			},
		},
	}, map[string]any{
		"path": child,
	})
	if err != nil {
		t.Fatalf("execute memory_get: %v", err)
	}
	var payload struct {
		Path string `json:"path"`
		Text string `json:"text"`
	}
	if unmarshalErr := json.Unmarshal(result.JSON, &payload); unmarshalErr != nil {
		t.Fatalf("unmarshal result: %v", unmarshalErr)
	}
	if payload.Path != filepath.ToSlash(child) || !strings.Contains(payload.Text, "world") {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestMemoryGetToolReturnsEmptyTextForMissingAllowedExtraFile(t *testing.T) {
	workspace := t.TempDir()
	extraFile := filepath.Join(t.TempDir(), "future.md")
	result, err := NewMemoryGetTool().Execute(context.Background(), ToolContext{
		Run: AgentRunContext{
			Identity: core.AgentIdentity{
				WorkspaceDir:     workspace,
				MemoryExtraPaths: []string{extraFile},
			},
		},
	}, map[string]any{
		"path": extraFile,
	})
	if err != nil {
		t.Fatalf("execute memory_get: %v", err)
	}
	var payload struct {
		Path  string `json:"path"`
		Text  string `json:"text"`
		Lines int    `json:"lines"`
	}
	if unmarshalErr := json.Unmarshal(result.JSON, &payload); unmarshalErr != nil {
		t.Fatalf("unmarshal result: %v", unmarshalErr)
	}
	if payload.Path != filepath.ToSlash(extraFile) || payload.Text != "" || payload.Lines != 0 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}
