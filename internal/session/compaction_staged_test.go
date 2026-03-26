package session

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/core"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

type fakeCompactionRunner struct {
	calls   []CompactionTurnParams
	results []string
	err     error
}

func (f *fakeCompactionRunner) RunCompactionTurn(_ context.Context, params CompactionTurnParams) (string, error) {
	f.calls = append(f.calls, params)
	if f.err != nil {
		return "", f.err
	}
	idx := len(f.calls) - 1
	if idx < len(f.results) {
		return f.results[idx], nil
	}
	return "summary", nil
}

func (f *fakeCompactionRunner) RunMemoryFlushTurn(_ context.Context, _ MemoryFlushTurnParams) error {
	return nil
}

func (f *fakeCompactionRunner) EmitDebugEvent(_, _, _ string, _ map[string]any) {}

func makeMessages(n int, charsEach int) []core.TranscriptMessage {
	msgs := make([]core.TranscriptMessage, n)
	for i := range msgs {
		msgs[i] = core.TranscriptMessage{
			Role: "user",
			Text: strings.Repeat("x", charsEach),
		}
	}
	return msgs
}

// ---------------------------------------------------------------------------
// Token estimation tests
// ---------------------------------------------------------------------------

func TestEstimateTokensForText(t *testing.T) {
	tests := []struct {
		text   string
		expect int
	}{
		{"", 0},
		{"a", 1},              // 1 char → ceil(1/4) = 1
		{"abcd", 1},           // 4 chars → 1 token
		{"abcde", 2},          // 5 chars → ceil(5/4) = 2
		{strings.Repeat("x", 100), 25}, // 100 chars → 25 tokens
	}
	for _, tc := range tests {
		got := estimateTokensForText(tc.text)
		if got != tc.expect {
			t.Errorf("estimateTokensForText(%q) = %d, want %d", tc.text, got, tc.expect)
		}
	}
}

func TestEstimateTokensForMessages(t *testing.T) {
	msgs := []core.TranscriptMessage{
		{Text: strings.Repeat("a", 40)},  // 10 tokens
		{Text: strings.Repeat("b", 40)},  // 10 tokens
		{Summary: strings.Repeat("c", 20)}, // 5 tokens
	}
	got := estimateTokensForMessages(msgs)
	if got != 25 {
		t.Fatalf("expected 25, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// SplitMessagesByTokenShare tests
// ---------------------------------------------------------------------------

func TestSplitMessagesByTokenShare_Basic(t *testing.T) {
	msgs := makeMessages(10, 40) // Each 10 tokens → total 100 tokens
	parts := SplitMessagesByTokenShare(msgs, 2)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	// Should be roughly equal.
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	if total != 10 {
		t.Fatalf("expected total 10 messages, got %d", total)
	}
}

func TestSplitMessagesByTokenShare_SingleMessage(t *testing.T) {
	msgs := makeMessages(1, 100)
	parts := SplitMessagesByTokenShare(msgs, 3)
	if len(parts) != 1 {
		t.Fatalf("expected 1 part for single message, got %d", len(parts))
	}
}

func TestSplitMessagesByTokenShare_Empty(t *testing.T) {
	parts := SplitMessagesByTokenShare(nil, 3)
	if parts != nil {
		t.Fatalf("expected nil for empty, got %v", parts)
	}
}

func TestSplitMessagesByTokenShare_ZeroParts(t *testing.T) {
	msgs := makeMessages(5, 40)
	parts := SplitMessagesByTokenShare(msgs, 0)
	if len(parts) != 1 {
		t.Fatalf("expected 1 part for zero parts, got %d", len(parts))
	}
}

// ---------------------------------------------------------------------------
// ChunkMessagesByMaxTokens tests
// ---------------------------------------------------------------------------

func TestChunkMessagesByMaxTokens_Basic(t *testing.T) {
	// 10 messages × 40 chars = 10 tokens each → total 100 tokens
	// maxTokens=50, safetyFactor=1.0 → effective 50 → 2 chunks
	msgs := makeMessages(10, 40)
	chunks := ChunkMessagesByMaxTokens(msgs, 50, 1.0)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
}

func TestChunkMessagesByMaxTokens_WithSafety(t *testing.T) {
	// 10 messages × 40 chars = 10 tokens each → total 100 tokens
	// maxTokens=60, safetyFactor=1.2 → effective 50 → 2 chunks
	msgs := makeMessages(10, 40)
	chunks := ChunkMessagesByMaxTokens(msgs, 60, 1.2)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
}

func TestChunkMessagesByMaxTokens_SingleLargeMessage(t *testing.T) {
	// One message larger than max → still goes into a single chunk.
	msgs := makeMessages(1, 1000)
	chunks := ChunkMessagesByMaxTokens(msgs, 10, 1.0)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk for oversized single message, got %d", len(chunks))
	}
}

func TestChunkMessagesByMaxTokens_Empty(t *testing.T) {
	chunks := ChunkMessagesByMaxTokens(nil, 100, 1.0)
	if chunks != nil {
		t.Fatalf("expected nil for empty, got %v", chunks)
	}
}

// ---------------------------------------------------------------------------
// ComputeAdaptiveChunkRatio tests
// ---------------------------------------------------------------------------

func TestComputeAdaptiveChunkRatio(t *testing.T) {
	// 1000 tokens / 8000 target → 1 part.
	msgs := makeMessages(100, 40) // 100 × 10 tokens = 1000
	parts := ComputeAdaptiveChunkRatio(msgs, 8000)
	if parts != 1 {
		t.Fatalf("expected 1, got %d", parts)
	}

	// 10000 tokens / 4000 target → 3 parts.
	msgs = makeMessages(1000, 40) // 1000 × 10 = 10000
	parts = ComputeAdaptiveChunkRatio(msgs, 4000)
	if parts != 3 {
		t.Fatalf("expected 3, got %d", parts)
	}
}

// ---------------------------------------------------------------------------
// Identifier instruction tests
// ---------------------------------------------------------------------------

func TestBuildIdentifierInstruction(t *testing.T) {
	strict := buildIdentifierInstruction(IdentifierPreservationStrict, nil)
	if !strings.Contains(strict, "IMPORTANT") {
		t.Fatal("strict should contain IMPORTANT")
	}

	custom := buildIdentifierInstruction(IdentifierPreservationCustom, []string{"foo", "bar"})
	if !strings.Contains(custom, "foo") || !strings.Contains(custom, "bar") {
		t.Fatal("custom should include identifiers")
	}

	empty := buildIdentifierInstruction(IdentifierPreservationCustom, nil)
	if empty != "" {
		t.Fatal("custom with no identifiers should be empty")
	}

	off := buildIdentifierInstruction(IdentifierPreservationOff, nil)
	if off != "" {
		t.Fatal("off should be empty")
	}
}

// ---------------------------------------------------------------------------
// SummarizeWithFallback tests
// ---------------------------------------------------------------------------

func TestSummarizeWithFallback_Success(t *testing.T) {
	runner := &fakeCompactionRunner{results: []string{"test summary"}}
	msgs := makeMessages(5, 40)
	config := StagedCompactionConfig{}

	summary, err := SummarizeWithFallback(context.Background(), runner, CompactionTurnParams{}, msgs, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary != "test summary" {
		t.Fatalf("expected 'test summary', got %q", summary)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}
}

func TestSummarizeWithFallback_FallbackOnError(t *testing.T) {
	runner := &fakeCompactionRunner{err: fmt.Errorf("LLM failed")}
	msgs := makeMessages(3, 40)
	config := StagedCompactionConfig{MaxRetries: 1}

	summary, err := SummarizeWithFallback(context.Background(), runner, CompactionTurnParams{}, msgs, config)
	if err != nil {
		t.Fatalf("unexpected error (should fallback): %v", err)
	}
	if !strings.Contains(summary, "Summary of earlier conversation") {
		t.Fatalf("expected fallback summary, got %q", summary)
	}
}

// ---------------------------------------------------------------------------
// SummarizeInStages tests
// ---------------------------------------------------------------------------

func TestSummarizeInStages_SingleChunk(t *testing.T) {
	runner := &fakeCompactionRunner{results: []string{"one chunk summary"}}
	msgs := makeMessages(5, 40) // 50 tokens → fits in one chunk
	config := StagedCompactionConfig{MaxChunkTokens: 1000}

	summary, err := SummarizeInStages(context.Background(), runner, CompactionTurnParams{RunID: "r1"}, msgs, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary != "one chunk summary" {
		t.Fatalf("expected 'one chunk summary', got %q", summary)
	}
}

func TestSummarizeInStages_MultiChunk(t *testing.T) {
	// 10 messages × 400 chars = 100 tokens each → total 1000 tokens
	// MaxChunkTokens=300 (effective ~250 at 1.2x safety) → multiple chunks
	// Runner returns "summary" for each call; we just verify multi-stage behavior.
	runner := &fakeCompactionRunner{}
	msgs := makeMessages(10, 400)
	config := StagedCompactionConfig{MaxChunkTokens: 300, MaxRetries: 1}

	summary, err := SummarizeInStages(context.Background(), runner, CompactionTurnParams{RunID: "r1"}, msgs, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
	// Should have multiple calls (chunks + merge).
	if len(runner.calls) < 2 {
		t.Fatalf("expected at least 2 runner calls (chunks + merge), got %d", len(runner.calls))
	}
	// Last call should be the merge step.
	lastCall := runner.calls[len(runner.calls)-1]
	if !strings.Contains(lastCall.RunID, "compact-merge") {
		t.Fatalf("expected last call to be merge, got RunID=%s", lastCall.RunID)
	}
}

func TestSummarizeInStages_ChunkFailureFallback(t *testing.T) {
	// Runner fails on first call, succeeds on retries.
	callCount := 0
	runner := &fakeCompactionRunner{}
	origRun := runner.RunCompactionTurn
	_ = origRun
	// We'll use a custom runner that fails the first call.
	customRunner := &failFirstCompactionRunner{failCount: 1}
	msgs := makeMessages(10, 400)
	config := StagedCompactionConfig{MaxChunkTokens: 300, MaxRetries: 2}
	_ = callCount

	summary, err := SummarizeInStages(context.Background(), customRunner, CompactionTurnParams{RunID: "r1"}, msgs, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
}

func TestSummarizeInStages_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	runner := &fakeCompactionRunner{}
	msgs := makeMessages(10, 400)
	config := StagedCompactionConfig{MaxChunkTokens: 300}

	_, err := SummarizeInStages(ctx, runner, CompactionTurnParams{}, msgs, config)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

// ---------------------------------------------------------------------------
// formatMessagesForPrompt tests
// ---------------------------------------------------------------------------

func TestFormatMessagesForPrompt(t *testing.T) {
	msgs := []core.TranscriptMessage{
		{Role: "user", Text: "hello"},
		{Role: "assistant", Text: "hi there"},
		{Role: "", Text: "no role"},
		{Role: "user", Text: ""},          // Skipped
		{Role: "user", Summary: "fallback"}, // Uses Summary when Text empty
	}
	got := formatMessagesForPrompt(msgs)
	if !strings.Contains(got, "[user]: hello") {
		t.Fatal("missing user message")
	}
	if !strings.Contains(got, "[assistant]: hi there") {
		t.Fatal("missing assistant message")
	}
	if !strings.Contains(got, "[message]: no role") {
		t.Fatal("missing default role message")
	}
	if !strings.Contains(got, "[user]: fallback") {
		t.Fatal("missing fallback summary message")
	}
}

// ---------------------------------------------------------------------------
// splitByCount tests
// ---------------------------------------------------------------------------

func TestSplitByCount(t *testing.T) {
	msgs := makeMessages(10, 0) // Zero-length messages.
	parts := splitByCount(msgs, 3)
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(parts))
	}
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	if total != 10 {
		t.Fatalf("expected 10 total, got %d", total)
	}
}

// ---------------------------------------------------------------------------
// Custom test runner
// ---------------------------------------------------------------------------

type failFirstCompactionRunner struct {
	callCount int
	failCount int
}

func (f *failFirstCompactionRunner) RunCompactionTurn(_ context.Context, _ CompactionTurnParams) (string, error) {
	f.callCount++
	if f.callCount <= f.failCount {
		return "", fmt.Errorf("intentional failure %d", f.callCount)
	}
	return fmt.Sprintf("summary-%d", f.callCount), nil
}

func (f *failFirstCompactionRunner) RunMemoryFlushTurn(_ context.Context, _ MemoryFlushTurnParams) error {
	return nil
}

func (f *failFirstCompactionRunner) EmitDebugEvent(_, _, _ string, _ map[string]any) {}
