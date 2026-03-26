package infra

import (
	"strings"
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{"empty", "", 0},
		{"short", "hi", 1},             // 2 chars → ceil(2/4) = 1
		{"exact4", "abcd", 1},          // 4 chars → 1
		{"five", "abcde", 2},           // 5 chars → ceil(5/4) = 2
		{"eight", "abcdefgh", 2},       // 8 chars → 2
		{"1000", strings.Repeat("x", 1000), 250}, // 1000/4 = 250
		{"1001", strings.Repeat("x", 1001), 251}, // ceil(1001/4) = 251
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateTokens(tt.text)
			if got != tt.want {
				t.Errorf("EstimateTokens(%d chars) = %d, want %d", len(tt.text), got, tt.want)
			}
		})
	}
}

func TestNewContextBudget_Defaults(t *testing.T) {
	b := NewContextBudget(0, 0)
	if b.MaxContextTokens != defaultMaxContextTokens {
		t.Errorf("MaxContextTokens = %d, want %d", b.MaxContextTokens, defaultMaxContextTokens)
	}
	if b.ReservedForOutput != defaultMaxOutputTokens {
		t.Errorf("ReservedForOutput = %d, want %d", b.ReservedForOutput, defaultMaxOutputTokens)
	}
	expected := defaultMaxContextTokens - defaultMaxOutputTokens
	if b.Remaining != expected {
		t.Errorf("Remaining = %d, want %d", b.Remaining, expected)
	}
}

func TestNewContextBudget_CustomValues(t *testing.T) {
	b := NewContextBudget(200_000, 16_384)
	if b.MaxContextTokens != 200_000 {
		t.Errorf("MaxContextTokens = %d, want 200000", b.MaxContextTokens)
	}
	if b.Remaining != 200_000-16_384 {
		t.Errorf("Remaining = %d, want %d", b.Remaining, 200_000-16_384)
	}
}

func TestNewContextBudget_SmallContext(t *testing.T) {
	// When maxContext - maxOutput < 1024, floor to 1024.
	b := NewContextBudget(2000, 1500)
	if b.Remaining != 1024 {
		t.Errorf("Remaining = %d, want 1024 (floor)", b.Remaining)
	}
}

func TestContextBudget_FitsInBudget(t *testing.T) {
	b := NewContextBudget(10_000, 2_000) // remaining = 8000
	if !b.FitsInBudget(8000) {
		t.Error("8000 should fit in budget of 8000")
	}
	if b.FitsInBudget(8001) {
		t.Error("8001 should not fit in budget of 8000")
	}
}

func TestContextBudget_Consume(t *testing.T) {
	b := NewContextBudget(10_000, 2_000) // remaining = 8000
	b.Consume(3000)
	if b.Remaining != 5000 {
		t.Errorf("after consume 3000: Remaining = %d, want 5000", b.Remaining)
	}
	b.Consume(10_000) // over-consume
	if b.Remaining != 0 {
		t.Errorf("after over-consume: Remaining = %d, want 0", b.Remaining)
	}
}

func TestContextBudget_SingleFileTokenLimit(t *testing.T) {
	b := NewContextBudget(128_000, 8_192) // remaining = 119808
	limit := b.SingleFileTokenLimit()
	remaining := float64(b.Remaining)
	expected := int(remaining * singleFileFraction)
	if limit != expected {
		t.Errorf("SingleFileTokenLimit = %d, want %d", limit, expected)
	}
}

func TestContextBudget_TotalFilesTokenLimit(t *testing.T) {
	b := NewContextBudget(128_000, 8_192) // remaining = 119808
	limit := b.TotalFilesTokenLimit()
	remaining := float64(b.Remaining)
	expected := int(remaining * totalFilesFraction)
	if limit != expected {
		t.Errorf("TotalFilesTokenLimit = %d, want %d", limit, expected)
	}
}

func TestContextBudget_ByteLimits(t *testing.T) {
	b := NewContextBudget(128_000, 8_192)
	singleByte := b.SingleFileByteLimit()
	singleToken := b.SingleFileTokenLimit()
	if singleByte != singleToken*charsPerToken {
		t.Errorf("SingleFileByteLimit = %d, want %d", singleByte, singleToken*charsPerToken)
	}
	totalByte := b.TotalFilesByteLimit()
	totalToken := b.TotalFilesTokenLimit()
	if totalByte != totalToken*charsPerToken {
		t.Errorf("TotalFilesByteLimit = %d, want %d", totalByte, totalToken*charsPerToken)
	}
}

func TestContextBudget_AllocateContextFiles_Empty(t *testing.T) {
	b := NewContextBudget(128_000, 8_192)
	result := b.AllocateContextFiles(nil)
	if result != nil {
		t.Errorf("expected nil, got %d files", len(result))
	}
}

func TestContextBudget_AllocateContextFiles_AllFit(t *testing.T) {
	b := NewContextBudget(128_000, 8_192)
	files := []PromptContextFile{
		{Path: "AGENTS.md", Title: "Agents", Content: strings.Repeat("a", 1000)},
		{Path: "README.md", Title: "Readme", Content: strings.Repeat("b", 2000)},
	}
	result := b.AllocateContextFiles(files)
	if len(result) != 2 {
		t.Fatalf("expected 2 files, got %d", len(result))
	}
	if result[0].Truncated || result[1].Truncated {
		t.Error("files should not be truncated when they fit")
	}
	if len(result[0].Content) != 1000 || len(result[1].Content) != 2000 {
		t.Error("content should be unchanged")
	}
}

func TestContextBudget_AllocateContextFiles_SingleFileTruncation(t *testing.T) {
	b := NewContextBudget(128_000, 8_192)
	singleLimit := b.SingleFileByteLimit()
	oversized := strings.Repeat("x", singleLimit+1000)
	files := []PromptContextFile{
		{Path: "big.md", Title: "Big", Content: oversized},
	}
	result := b.AllocateContextFiles(files)
	if len(result) != 1 {
		t.Fatalf("expected 1 file, got %d", len(result))
	}
	if !result[0].Truncated {
		t.Error("oversized file should be marked truncated")
	}
	if len(result[0].Content) != singleLimit {
		t.Errorf("content length = %d, want %d", len(result[0].Content), singleLimit)
	}
}

func TestContextBudget_AllocateContextFiles_TotalBudgetExceeded(t *testing.T) {
	// Use a small context window to make total budget manageable.
	b := NewContextBudget(4_000, 1_000) // remaining = 3000
	totalLimit := b.TotalFilesByteLimit()
	// Single file limit is 0.15 * 3000 * 4 = 1800 bytes
	// Total limit is 0.40 * 3000 * 4 = 4800 bytes

	fileSize := totalLimit / 3 // each file is 1/3 of total budget
	files := []PromptContextFile{
		{Path: "a.md", Title: "A", Content: strings.Repeat("a", fileSize)},
		{Path: "b.md", Title: "B", Content: strings.Repeat("b", fileSize)},
		{Path: "c.md", Title: "C", Content: strings.Repeat("c", fileSize)},
		{Path: "d.md", Title: "D", Content: strings.Repeat("d", fileSize)}, // should be dropped or truncated
	}
	result := b.AllocateContextFiles(files)

	totalContent := 0
	for _, f := range result {
		totalContent += len(f.Content)
	}
	if totalContent > totalLimit {
		t.Errorf("total content %d exceeds total budget %d", totalContent, totalLimit)
	}
	// At least the first 3 files should be present (they fill the budget).
	if len(result) < 3 {
		t.Errorf("expected at least 3 files, got %d", len(result))
	}
}

func TestContextBudget_AllocateContextFiles_SkipsEmpty(t *testing.T) {
	b := NewContextBudget(128_000, 8_192)
	files := []PromptContextFile{
		{Path: "empty.md", Title: "Empty", Content: ""},
		{Path: "ok.md", Title: "OK", Content: "hello"},
	}
	result := b.AllocateContextFiles(files)
	if len(result) != 1 {
		t.Fatalf("expected 1 file, got %d", len(result))
	}
	if result[0].Path != "ok.md" {
		t.Errorf("expected ok.md, got %s", result[0].Path)
	}
}

func TestContextBudget_AllocateContextFiles_PreservesTruncatedFlag(t *testing.T) {
	b := NewContextBudget(128_000, 8_192)
	files := []PromptContextFile{
		{Path: "pre.md", Title: "Pre", Content: "content", Truncated: true},
	}
	result := b.AllocateContextFiles(files)
	if len(result) != 1 {
		t.Fatalf("expected 1 file, got %d", len(result))
	}
	if !result[0].Truncated {
		t.Error("pre-existing Truncated flag should be preserved")
	}
}

func TestContextBudget_SingleFileFloor(t *testing.T) {
	// Very small budget: SingleFileTokenLimit should floor at 256.
	b := NewContextBudget(1100, 100) // remaining = 1024 (floor)
	limit := b.SingleFileTokenLimit()
	if limit < 256 {
		t.Errorf("SingleFileTokenLimit = %d, want >= 256", limit)
	}
}

func TestContextBudget_TotalFilesFloor(t *testing.T) {
	// Very small budget: TotalFilesTokenLimit should floor at 512.
	b := NewContextBudget(1100, 100) // remaining = 1024 (floor)
	limit := b.TotalFilesTokenLimit()
	if limit < 512 {
		t.Errorf("TotalFilesTokenLimit = %d, want >= 512", limit)
	}
}
