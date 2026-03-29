package infra

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
)

func TestResolveContextSourcePath(t *testing.T) {
	absPath := filepath.Join(filepath.VolumeName(os.TempDir())+string(os.PathSeparator), "data", "file.db")
	if filepath.VolumeName(os.TempDir()) == "" {
		absPath = filepath.Join(string(os.PathSeparator), "data", "file.db")
	}
	tests := []struct {
		name         string
		workspaceDir string
		rawPath      string
		wantAbs      bool
	}{
		{"absolute_path", filepath.Join("workspace"), absPath, true},
		{"relative_path", filepath.Join("workspace"), filepath.Join("data", "file.db"), false},
		{"empty_path", filepath.Join("workspace"), "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ResolveContextSourcePath(tt.workspaceDir, tt.rawPath)
			if tt.rawPath == "" {
				if result != "" {
					t.Errorf("empty path should return empty, got %q", result)
				}
				return
			}
			if tt.wantAbs {
				if result != tt.rawPath {
					t.Errorf("absolute path should be returned as-is, got %q", result)
				}
			} else {
				expected := filepath.Join(tt.workspaceDir, tt.rawPath)
				if result != expected {
					t.Errorf("got %q, want %q", result, expected)
				}
			}
		})
	}
}

func TestCollectContextSourceFiles(t *testing.T) {
	t.Run("empty_path", func(t *testing.T) {
		_, err := CollectContextSourceFiles("")
		if err == nil {
			t.Error("empty path should error")
		}
	})

	t.Run("single_file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.md")
		if err := writeTestFile(path, "content"); err != nil {
			t.Fatal(err)
		}
		files, err := CollectContextSourceFiles(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(files) != 1 {
			t.Errorf("expected 1 file, got %d", len(files))
		}
	})
}

func TestConvertMemoryHitsToContextHits(t *testing.T) {
	hits := []MemoryHit{
		{ID: "1", Source: "notes.md", Path: "notes.md", Snippet: "test", Score: 0.5, FromLine: 1, ToLine: 5},
	}
	result := ConvertMemoryHitsToContextHits("source1", "file", hits)
	if len(result) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(result))
	}
	if result[0].SourceID != "source1" {
		t.Errorf("SourceID = %q", result[0].SourceID)
	}
	if result[0].Type != "file" {
		t.Errorf("Type = %q", result[0].Type)
	}
	if result[0].Snippet != "test" {
		t.Errorf("Snippet = %q", result[0].Snippet)
	}
}

func TestSQLiteQuoteIdentifier(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"table_name", `"table_name"`},
		{`has"quote`, `"has""quote"`},
		{"  spaces  ", `"spaces"`},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SQLiteQuoteIdentifier(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSQLiteQuoteLiteral(t *testing.T) {
	if SQLiteQuoteLiteral("it's") != "it''s" {
		t.Errorf("got %q", SQLiteQuoteLiteral("it's"))
	}
	if SQLiteQuoteLiteral("normal") != "normal" {
		t.Error("normal string should be unchanged")
	}
}

func TestScoreSQLiteRow(t *testing.T) {
	t.Run("matching", func(t *testing.T) {
		row := map[string]string{"text": "golang testing framework", "rowid": "1"}
		score := ScoreSQLiteRow(row, []string{"golang", "testing"}, "code")
		if score <= 0 {
			t.Error("matching row should have positive score")
		}
	})

	t.Run("no_match", func(t *testing.T) {
		row := map[string]string{"text": "python ml", "rowid": "1"}
		score := ScoreSQLiteRow(row, []string{"golang"}, "code")
		if score != 0 {
			t.Errorf("non-matching should return 0, got %f", score)
		}
	})

	t.Run("empty_text", func(t *testing.T) {
		row := map[string]string{"text": "", "rowid": "1"}
		score := ScoreSQLiteRow(row, []string{"anything"}, "table")
		if score != 0 {
			t.Error("empty text should return 0")
		}
	})

	t.Run("table_name_bonus", func(t *testing.T) {
		row := map[string]string{"text": "data", "rowid": "1"}
		score1 := ScoreSQLiteRow(row, []string{"data"}, "unrelated")
		score2 := ScoreSQLiteRow(row, []string{"data"}, "data_table")
		if score2 <= score1 {
			t.Error("matching table name should give bonus")
		}
	})
}

func TestBuildContextSource(t *testing.T) {
	t.Run("file_type", func(t *testing.T) {
		source := BuildContextSource("test", config_DataSourceConfigStub("file"))
		if source == nil {
			t.Fatal("expected non-nil")
		}
		if source.Type() != "file" {
			t.Errorf("Type = %q", source.Type())
		}
		if source.ID() != "test" {
			t.Errorf("ID = %q", source.ID())
		}
	})

	t.Run("files_type", func(t *testing.T) {
		source := BuildContextSource("test", config_DataSourceConfigStub("files"))
		if source == nil {
			t.Fatal("expected non-nil")
		}
		if source.Type() != "file" {
			t.Errorf("Type = %q", source.Type())
		}
	})

	t.Run("sqlite_type", func(t *testing.T) {
		source := BuildContextSource("test", config_DataSourceConfigStub("sqlite"))
		if source == nil {
			t.Fatal("expected non-nil")
		}
		if source.Type() != "sqlite" {
			t.Errorf("Type = %q", source.Type())
		}
	})

	t.Run("unknown_type", func(t *testing.T) {
		source := BuildContextSource("test", config_DataSourceConfigStub("unknown"))
		if source != nil {
			t.Error("unknown type should return nil")
		}
	})
}

func TestNewContextSourceRegistry(t *testing.T) {
	registry := NewContextSourceRegistry(config_AppConfigStub())
	if registry == nil {
		t.Fatal("expected non-nil")
	}
}

// MemoryHit alias for local use
type MemoryHit = core.MemoryHit

// Helpers
func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func createTestFileHelper(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func config_DataSourceConfigStub(typ string) config.DataSourceConfig {
	return config.DataSourceConfig{Type: typ}
}

func config_AppConfigStub() config.AppConfig {
	return config.AppConfig{}
}
