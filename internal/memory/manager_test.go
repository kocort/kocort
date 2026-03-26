package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
)

func TestQMDMemoryBackendRecallParsesJSONHits(t *testing.T) {
	dir := t.TempDir()
	commandPath := filepath.Join(dir, "fake-qmd.sh")
	script := "#!/bin/sh\nprintf '%s\\n' '[{\"path\":\"MEMORY.md\",\"snippet\":\"Atlas code BLUE-SPARROW-17\",\"score\":0.9,\"fromLine\":2,\"toLine\":2}]'\n"
	if err := os.WriteFile(commandPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write command: %v", err)
	}

	manager := NewManager(config.AppConfig{
		Memory: config.MemoryConfig{
			Backend: "qmd",
			QMD: &config.MemoryQMDConfig{
				Command: commandPath,
				Limits:  &config.MemoryQMDLimitsConfig{TimeoutMs: 5000},
			},
		},
	})
	hits, err := manager.Recall(context.Background(), core.AgentIdentity{
		WorkspaceDir:          dir,
		MemoryProvider:        "qmd",
		MemoryEnabled:         true,
		MemoryQueryMaxResults: 3,
	}, core.SessionResolution{}, "what is BLUE-SPARROW-17")
	if err != nil {
		t.Fatalf("qmd recall: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected one qmd hit, got %+v", hits)
	}
	if hits[0].Path != "MEMORY.md" || !strings.Contains(hits[0].Snippet, "BLUE-SPARROW-17") {
		t.Fatalf("unexpected qmd hit: %+v", hits[0])
	}
}

func TestMemoryManagerFallsBackFromQMDToBuiltin(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, DefaultMemoryFilename), []byte("Atlas code is BLUE-SPARROW-17.\n"), 0o644); err != nil {
		t.Fatalf("write memory: %v", err)
	}
	manager := NewManager(config.AppConfig{})
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

func TestQMDMemoryBackendRecallPassesResolvedLimit(t *testing.T) {
	dir := t.TempDir()
	commandPath := filepath.Join(dir, "fake-qmd.sh")
	argsPath := filepath.Join(dir, "args.txt")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >" + argsPath + "\necho '[]'\n"
	if err := os.WriteFile(commandPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write command: %v", err)
	}

	manager := NewManager(config.AppConfig{
		Memory: config.MemoryConfig{
			Backend: "qmd",
			QMD: &config.MemoryQMDConfig{
				Command: commandPath,
				Limits:  &config.MemoryQMDLimitsConfig{TimeoutMs: 5000, MaxResults: 2},
			},
		},
	})
	_, err := manager.Recall(context.Background(), core.AgentIdentity{
		WorkspaceDir:          dir,
		MemoryProvider:        "qmd",
		MemoryEnabled:         true,
		MemoryQueryMaxResults: 4,
	}, core.SessionResolution{}, "atlas")
	if err != nil {
		t.Fatalf("qmd recall: %v", err)
	}
	argsData, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	args := strings.Fields(string(argsData))
	got := strings.Join(args, " ")
	if got != "search atlas --json -n 2" {
		t.Fatalf("unexpected qmd args: %q", got)
	}
}

func TestParseQMDSearchHitsHandlesNoisyJSONArrayOutput(t *testing.T) {
	data := []byte("warn: warming cache\n[{\"path\":\"MEMORY.md\",\"snippet\":\"Atlas code BLUE-SPARROW-17\",\"score\":0.9,\"fromLine\":2,\"toLine\":2}]\n")
	hits, err := parseQMDSearchHits(data, MemorySearchQuery{MaxResults: 3}, config.AppConfig{})
	if err != nil {
		t.Fatalf("parse qmd noisy output: %v", err)
	}
	if len(hits) != 1 || hits[0].Path != "MEMORY.md" {
		t.Fatalf("unexpected hits: %+v", hits)
	}
}

func TestParseQMDSearchHitsTreatsNoResultsMarkerAsEmpty(t *testing.T) {
	hits, err := parseQMDSearchHits([]byte("No results found.\n"), MemorySearchQuery{MaxResults: 3}, config.AppConfig{})
	if err != nil {
		t.Fatalf("parse qmd no results: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected empty hits, got %+v", hits)
	}
}

func TestParseQMDSearchHitsClampsSnippetLength(t *testing.T) {
	cfg := config.AppConfig{
		Memory: config.MemoryConfig{
			QMD: &config.MemoryQMDConfig{
				Limits: &config.MemoryQMDLimitsConfig{MaxSnippetChars: 8},
			},
		},
	}
	hits, err := parseQMDSearchHits([]byte(`[{"path":"MEMORY.md","snippet":"Atlas code BLUE-SPARROW-17","score":0.9}]`), MemorySearchQuery{MaxResults: 3}, cfg)
	if err != nil {
		t.Fatalf("parse qmd hits: %v", err)
	}
	if len(hits) != 1 || hits[0].Snippet != "Atlas co..." {
		payload, _ := json.Marshal(hits)
		t.Fatalf("unexpected snippet clamp: %s", string(payload))
	}
}

func TestMemoryManagerIncludesSessionTranscriptHitsWhenEnabled(t *testing.T) {
	workspace := t.TempDir()
	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	lines := []string{
		`{"type":"session","id":"s1"}`,
		`{"role":"user","text":"please remember BLUE-SPARROW-17"}`,
		`{"role":"assistant","text":"noted"}`,
	}
	if err := os.WriteFile(transcript, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	manager := NewManager(config.AppConfig{})
	hits, err := manager.Recall(context.Background(), core.AgentIdentity{
		WorkspaceDir:          workspace,
		MemoryEnabled:         true,
		MemorySources:         []string{"session"},
		MemoryQueryMaxResults: 3,
	}, core.SessionResolution{
		SessionID: "s1",
		Entry:     &core.SessionEntry{SessionFile: transcript},
	}, "what is BLUE-SPARROW-17")
	if err != nil {
		t.Fatalf("recall with transcript memory: %v", err)
	}
	if len(hits) == 0 || !strings.Contains(hits[0].Snippet, "BLUE-SPARROW-17") {
		t.Fatalf("expected transcript hit, got %+v", hits)
	}
}

func TestQMDMemoryBackendRecallUsesConfiguredCollections(t *testing.T) {
	dir := t.TempDir()
	commandPath := filepath.Join(dir, "fake-qmd.sh")
	argsPath := filepath.Join(dir, "args.txt")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >>" + argsPath + "\nprintf '\\n' >>" + argsPath + "\necho '[]'\n"
	if err := os.WriteFile(commandPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write command: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "notes"), 0o755); err != nil {
		t.Fatalf("mkdir notes: %v", err)
	}
	manager := NewManager(config.AppConfig{
		Memory: config.MemoryConfig{
			Backend: "qmd",
			QMD: &config.MemoryQMDConfig{
				Command: commandPath,
				Paths: []config.MemoryQMDIndexPath{
					{Name: "workspace", Path: dir, Pattern: "**/*.md"},
					{Name: "notes", Path: filepath.Join(dir, "notes"), Pattern: "**/*.md"},
				},
				Limits: &config.MemoryQMDLimitsConfig{TimeoutMs: 5000, MaxResults: 2},
			},
		},
	})
	_, err := manager.Recall(context.Background(), core.AgentIdentity{
		ID:                    "main",
		WorkspaceDir:          dir,
		MemoryProvider:        "qmd",
		MemoryEnabled:         true,
		MemoryQueryMaxResults: 4,
	}, core.SessionResolution{}, "atlas")
	if err != nil {
		t.Fatalf("qmd recall: %v", err)
	}
	argsData, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	got := strings.TrimSpace(string(argsData))
	if !strings.Contains(got, "workspace-main") || !strings.Contains(got, "notes-main") {
		t.Fatalf("expected collection-scoped qmd args, got %q", got)
	}
}

func TestQMDMemoryBackendRepairsMissingCollectionAndRetriesOnce(t *testing.T) {
	dir := t.TempDir()
	commandPath := filepath.Join(dir, "fake-qmd.sh")
	argsPath := filepath.Join(dir, "args.txt")
	searchStatePath := filepath.Join(dir, "search_state")
	addStatePath := filepath.Join(dir, "add_state")
	script := strings.Join([]string{
		"#!/bin/sh",
		"printf '%s\\n' \"$@\" >>" + argsPath,
		"printf '\\n' >>" + argsPath,
		"if [ \"$1\" = \"collection\" ] && [ \"$2\" = \"list\" ]; then",
		"  echo '[]'",
		"  exit 0",
		"fi",
		"if [ \"$1\" = \"collection\" ] && [ \"$2\" = \"add\" ]; then",
		"  touch " + addStatePath,
		"  exit 0",
		"fi",
		"if [ \"$1\" = \"search\" ]; then",
		"  if [ ! -f " + searchStatePath + " ]; then",
		"    touch " + searchStatePath,
		"    echo 'Collection not found: workspace-main' 1>&2",
		"    exit 1",
		"  fi",
		"  echo '[{\"path\":\"MEMORY.md\",\"snippet\":\"Atlas code BLUE-SPARROW-17\",\"score\":0.9}]'",
		"  exit 0",
		"fi",
		"echo '[]'",
	}, "\n")
	if err := os.WriteFile(commandPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write command: %v", err)
	}
	manager := NewManager(config.AppConfig{
		Memory: config.MemoryConfig{
			Backend: "qmd",
			QMD: &config.MemoryQMDConfig{
				Command: commandPath,
				Paths: []config.MemoryQMDIndexPath{
					{Name: "workspace", Path: dir, Pattern: "**/*.md"},
				},
				Limits: &config.MemoryQMDLimitsConfig{TimeoutMs: 5000, MaxResults: 2},
			},
		},
	})
	hits, err := manager.Recall(context.Background(), core.AgentIdentity{
		ID:                    "main",
		WorkspaceDir:          dir,
		MemoryProvider:        "qmd",
		MemoryEnabled:         true,
		MemoryQueryMaxResults: 2,
	}, core.SessionResolution{}, "atlas")
	if err != nil {
		t.Fatalf("qmd recall after repair: %v", err)
	}
	if len(hits) != 1 || !strings.Contains(hits[0].Snippet, "BLUE-SPARROW-17") {
		t.Fatalf("unexpected repaired hits: %+v", hits)
	}
	argsData, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	got := string(argsData)
	if strings.Count(got, "collection\nadd") < 2 {
		t.Fatalf("expected collection add to rerun after missing collection repair, got %q", got)
	}
}

func TestEnsureSessionTranscriptExportWritesMarkdown(t *testing.T) {
	workspace := t.TempDir()
	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	lines := []string{
		`{"type":"session","id":"s1"}`,
		`{"role":"user","text":"remember BLUE-SPARROW-17"}`,
		`{"role":"assistant","text":"ok"}`,
	}
	if err := os.WriteFile(transcript, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	exportPath, err := EnsureSessionTranscriptExport(workspace, core.SessionResolution{
		SessionID: "s1",
		Entry:     &core.SessionEntry{SessionFile: transcript},
	})
	if err != nil {
		t.Fatalf("ensure session export: %v", err)
	}
	data, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if !strings.Contains(string(data), "user: remember BLUE-SPARROW-17") {
		t.Fatalf("unexpected export content: %q", string(data))
	}
}

func TestResolveQMDManagedCollectionsIncludesSessionCollection(t *testing.T) {
	workspace := t.TempDir()
	collections := resolveQMDManagedCollections(core.AgentIdentity{
		ID:            "main",
		WorkspaceDir:  workspace,
		MemorySources: []string{"session"},
	}, core.SessionResolution{
		SessionID: "s1",
	}, config.AppConfig{})
	found := false
	for _, entry := range collections {
		if entry.Name == "sessions-main" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected session collection, got %+v", collections)
	}
}
