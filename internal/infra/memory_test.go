package infra

import (
	"testing"

	"github.com/kocort/kocort/internal/core"
)

func TestTokenizeSearchText(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{"basic", "hello world", []string{"hello", "world"}},
		{"punctuation", "hello, world! test.", []string{"hello", "world", "test"}},
		{"dedupes", "hello hello world", []string{"hello", "world"}},
		{"short_words_excluded", "a b c de fg", []string{"de", "fg"}},
		{"empty", "", nil},
		{"mixed_separators", "one/two\\three-four_five", []string{"one", "two", "three", "four", "five"}},
		{"case_insensitive", "Hello WORLD", []string{"hello", "world"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TokenizeSearchText(tt.text)
			if len(got) != len(tt.want) {
				t.Errorf("len = %d, want %d: %v", len(got), len(tt.want), got)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestToCharacterNgrams(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		n     int
		count int
	}{
		{"basic", "hello", 3, 3}, // hel, ell, llo
		{"short", "hi", 3, 1},    // "hi" itself
		{"empty", "", 3, 0},
		{"dedupes", "aaa", 3, 1}, // only "aaa"
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToCharacterNgrams(tt.text, tt.n)
			if len(got) != tt.count {
				t.Errorf("len = %d, want %d: %v", len(got), tt.count, got)
			}
		})
	}
}

func TestJaccardSimilarity(t *testing.T) {
	t.Run("identical", func(t *testing.T) {
		score := JaccardSimilarity([]string{"a", "b", "c"}, []string{"a", "b", "c"})
		if score != 1.0 {
			t.Errorf("identical sets should have similarity 1.0, got %f", score)
		}
	})

	t.Run("disjoint", func(t *testing.T) {
		score := JaccardSimilarity([]string{"a", "b"}, []string{"c", "d"})
		if score != 0.0 {
			t.Errorf("disjoint sets should have similarity 0.0, got %f", score)
		}
	})

	t.Run("partial", func(t *testing.T) {
		score := JaccardSimilarity([]string{"a", "b"}, []string{"b", "c"})
		// intersection=1, union=3 -> 0.333
		if score < 0.3 || score > 0.4 {
			t.Errorf("expected ~0.333, got %f", score)
		}
	})

	t.Run("empty_a", func(t *testing.T) {
		if JaccardSimilarity(nil, []string{"a"}) != 0 {
			t.Error("empty a should return 0")
		}
	})

	t.Run("empty_b", func(t *testing.T) {
		if JaccardSimilarity([]string{"a"}, nil) != 0 {
			t.Error("empty b should return 0")
		}
	})
}

func TestScoreMemoryChunk(t *testing.T) {
	chunk := MemoryChunk{
		Text:     "This is a document about golang testing frameworks.",
		Path:     "testing.md",
		FromLine: 1,
	}

	t.Run("matching_terms", func(t *testing.T) {
		score := ScoreMemoryChunk(chunk, "golang testing", []string{"golang", "testing"})
		if score <= 0 {
			t.Error("matching terms should produce positive score")
		}
	})

	t.Run("no_match", func(t *testing.T) {
		score := ScoreMemoryChunk(chunk, "kubernetes", []string{"kubernetes"})
		if score != 0 {
			t.Errorf("no match should return 0, got %f", score)
		}
	})

	t.Run("exact_query_match_bonus", func(t *testing.T) {
		score1 := ScoreMemoryChunk(chunk, "golang", []string{"golang"})
		score2 := ScoreMemoryChunk(chunk, "golang testing", []string{"golang", "testing"})
		if score2 <= score1 {
			t.Error("more term matches should produce higher score")
		}
	})

	t.Run("path_bonus", func(t *testing.T) {
		score := ScoreMemoryChunk(chunk, "testing", []string{"testing"})
		chunkNoPath := MemoryChunk{Text: chunk.Text, Path: "unrelated.md", FromLine: 1}
		scoreNoPath := ScoreMemoryChunk(chunkNoPath, "testing", []string{"testing"})
		if score <= scoreNoPath {
			t.Error("path match should give bonus score")
		}
	})

	t.Run("empty_terms", func(t *testing.T) {
		if ScoreMemoryChunk(chunk, "", nil) != 0 {
			t.Error("empty terms should return 0")
		}
	})
}

func TestBuildMemoryChunksForFile(t *testing.T) {
	content := ""
	for i := 0; i < 30; i++ {
		content += "line " + string(rune('A'+i%26)) + "\n"
	}
	identity := core.AgentIdentity{}
	chunks := BuildMemoryChunksForFile("test.md", content, identity)
	if len(chunks) == 0 {
		t.Fatal("expected chunks")
	}
	// Verify structure
	for _, chunk := range chunks {
		if chunk.Source != "test.md" {
			t.Errorf("source = %q, want test.md", chunk.Source)
		}
		if chunk.FromLine < 1 {
			t.Error("FromLine should be >= 1")
		}
		if chunk.ToLine < chunk.FromLine {
			t.Error("ToLine should be >= FromLine")
		}
		if chunk.Text == "" {
			t.Error("chunk text should not be empty")
		}
	}
}

func TestMergeAdjacentTinyChunks(t *testing.T) {
	t.Run("merges_small_chunks", func(t *testing.T) {
		chunks := []MemoryChunk{
			{ID: "1", Path: "f.md", FromLine: 1, ToLine: 2, Text: "hi"},
			{ID: "2", Path: "f.md", FromLine: 3, ToLine: 4, Text: "there"},
		}
		merged := MergeAdjacentTinyChunks(chunks)
		if len(merged) != 1 {
			t.Errorf("expected 1 merged chunk, got %d", len(merged))
		}
	})

	t.Run("keeps_large_chunks", func(t *testing.T) {
		largeText := ""
		for i := 0; i < 200; i++ {
			largeText += "x"
		}
		chunks := []MemoryChunk{
			{ID: "1", Path: "f.md", FromLine: 1, ToLine: 10, Text: largeText},
			{ID: "2", Path: "f.md", FromLine: 11, ToLine: 20, Text: "small"},
		}
		merged := MergeAdjacentTinyChunks(chunks)
		if len(merged) != 2 {
			t.Errorf("expected 2 chunks, got %d", len(merged))
		}
	})

	t.Run("single_chunk", func(t *testing.T) {
		chunks := []MemoryChunk{{ID: "1", Text: "only one"}}
		merged := MergeAdjacentTinyChunks(chunks)
		if len(merged) != 1 {
			t.Error("single chunk should remain")
		}
	})
}

func TestSearchMemoryIndex(t *testing.T) {
	chunks := []MemoryChunk{
		{ID: "1", Source: "a.md", Path: "a.md", FromLine: 1, ToLine: 5, Text: "golang is great for testing"},
		{ID: "2", Source: "b.md", Path: "b.md", FromLine: 1, ToLine: 5, Text: "python is also popular"},
		{ID: "3", Source: "c.md", Path: "c.md", FromLine: 1, ToLine: 5, Text: "testing in golang is fun"},
	}

	t.Run("finds_matches", func(t *testing.T) {
		hits := SearchMemoryIndex(chunks, "golang testing", []string{"golang", "testing"}, 5)
		if len(hits) < 2 {
			t.Errorf("expected at least 2 hits, got %d", len(hits))
		}
	})

	t.Run("respects_limit", func(t *testing.T) {
		hits := SearchMemoryIndex(chunks, "golang", []string{"golang"}, 1)
		if len(hits) > 1 {
			t.Errorf("expected max 1 hit, got %d", len(hits))
		}
	})

	t.Run("no_match", func(t *testing.T) {
		hits := SearchMemoryIndex(chunks, "kubernetes", []string{"kubernetes"}, 5)
		if len(hits) != 0 {
			t.Errorf("expected 0 hits, got %d", len(hits))
		}
	})
}

func TestClipMemoryHits(t *testing.T) {
	hits := []core.MemoryHit{
		{ID: "1", Score: 5},
		{ID: "2", Score: 3},
		{ID: "3", Score: 1},
	}

	t.Run("limit", func(t *testing.T) {
		result := ClipMemoryHits(hits, 2, 0)
		if len(result) != 2 {
			t.Errorf("expected 2, got %d", len(result))
		}
	})

	t.Run("min_score", func(t *testing.T) {
		result := ClipMemoryHits(hits, 0, 4)
		if len(result) != 1 {
			t.Errorf("expected 1 hit above minScore 4, got %d", len(result))
		}
	})

	t.Run("both", func(t *testing.T) {
		result := ClipMemoryHits(hits, 5, 2)
		if len(result) != 2 {
			t.Errorf("expected 2 hits above minScore 2, got %d", len(result))
		}
	})
}

func TestFallbackInt(t *testing.T) {
	if FallbackInt(5, 10) != 5 {
		t.Error("should return value when positive")
	}
	if FallbackInt(0, 10) != 10 {
		t.Error("should return fallback when zero")
	}
	if FallbackInt(-1, 10) != 10 {
		t.Error("should return fallback when negative")
	}
}

func TestFallbackFloat(t *testing.T) {
	if FallbackFloat(0.5, 1.0) != 0.5 {
		t.Error("should return value when positive")
	}
	if FallbackFloat(0, 1.0) != 1.0 {
		t.Error("should return fallback when zero")
	}
	if FallbackFloat(-0.1, 1.0) != 1.0 {
		t.Error("should return fallback when negative")
	}
}

func TestMemoryEnabled(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		enabled  bool
		want     bool
	}{
		{"default", "", false, true},
		{"explicit_enabled", "", true, true},
		{"off", "off", false, false},
		{"none", "none", false, false},
		{"disabled", "disabled", false, false},
		{"builtin", "builtin", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity := core.AgentIdentity{
				MemoryProvider: tt.provider,
				MemoryEnabled:  tt.enabled,
			}
			if got := MemoryEnabled(identity); got != tt.want {
				t.Errorf("MemoryEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDedupeMemoryBackends(t *testing.T) {
	t.Run("dedupes", func(t *testing.T) {
		backends := []MemoryBackend{
			&QMDMemoryBackend{},
			&QMDMemoryBackend{},
		}
		deduped := DedupeMemoryBackends(backends)
		if len(deduped) != 1 {
			t.Errorf("expected 1, got %d", len(deduped))
		}
	})

	t.Run("nil_entries", func(t *testing.T) {
		backends := []MemoryBackend{nil, nil}
		deduped := DedupeMemoryBackends(backends)
		if len(deduped) != 0 {
			t.Errorf("expected 0, got %d", len(deduped))
		}
	})
}

func TestTrimSnippet(t *testing.T) {
	t.Run("under_limit", func(t *testing.T) {
		if TrimSnippet("short", 100) != "short" {
			t.Error("should not trim")
		}
	})

	t.Run("over_limit", func(t *testing.T) {
		long := "this is a long string that exceeds the limit"
		result := TrimSnippet(long, 10)
		if len(result) > 15 { // 10 + "..."
			t.Errorf("should be trimmed, got len %d", len(result))
		}
	})

	t.Run("zero_limit", func(t *testing.T) {
		if TrimSnippet("text", 0) != "text" {
			t.Error("zero limit should not trim")
		}
	})
}

func TestCompactChunkID(t *testing.T) {
	if CompactChunkID(1) != "0001" {
		t.Errorf("got %q", CompactChunkID(1))
	}
	if CompactChunkID(42) != "0042" {
		t.Errorf("got %q", CompactChunkID(42))
	}
}
