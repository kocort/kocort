package backend

import (
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

// ---------------------------------------------------------------------------
// Layer 1: DropThinkingBlocksFromHistory
// ---------------------------------------------------------------------------

func TestDropThinkingBlocksFromHistory(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "<think>Let me think...</think>Hello there!"},
		{Role: "user", Content: "Bye"},
		{Role: "assistant", Content: "<think>Should I say goodbye?</think>Goodbye!"},
	}
	result := DropThinkingBlocksFromHistory(messages)
	if len(result) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(result))
	}
	if got := extractOpenAICompatContent(result[2].Content); got != "Hello there!" {
		t.Errorf("expected stripped content, got %q", got)
	}
	if got := extractOpenAICompatContent(result[4].Content); got != "Goodbye!" {
		t.Errorf("expected stripped content, got %q", got)
	}
}

func TestDropThinkingBlocksFromHistory_OnlyThinking(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{Role: "assistant", Content: "<think>Only thinking content</think>"},
	}
	result := DropThinkingBlocksFromHistory(messages)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if got := extractOpenAICompatContent(result[0].Content); got != "[thinking content omitted]" {
		t.Errorf("expected fallback text, got %q", got)
	}
}

func TestDropThinkingBlocksFromHistory_PreservesToolCalls(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{
			Role:    "assistant",
			Content: "<think>thinking</think>",
			ToolCalls: []openai.ToolCall{
				{ID: "call_1", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "read", Arguments: "{}"}},
			},
		},
	}
	result := DropThinkingBlocksFromHistory(messages)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	// When there are tool calls, empty text is not replaced
	if len(result[0].ToolCalls) != 1 {
		t.Errorf("expected tool calls preserved")
	}
}

func TestDropThinkingBlocksFromHistory_NoChange(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{Role: "assistant", Content: "No thinking here"},
	}
	result := DropThinkingBlocksFromHistory(messages)
	if extractOpenAICompatContent(result[0].Content) != "No thinking here" {
		t.Error("content should not change")
	}
}

// ---------------------------------------------------------------------------
// Layer 2: SanitizeToolCallInputs
// ---------------------------------------------------------------------------

func TestSanitizeToolCallInputs_RemovesDisallowed(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{Role: "user", Content: "Do something"},
		{
			Role: "assistant",
			ToolCalls: []openai.ToolCall{
				{ID: "c1", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "read", Arguments: "{}"}},
				{ID: "c2", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "exec", Arguments: "{}"}},
			},
		},
		{Role: "tool", ToolCallID: "c1", Name: "read", Content: "file content"},
		{Role: "tool", ToolCallID: "c2", Name: "exec", Content: "output"},
	}
	allowed := map[string]bool{"read": true}
	result := SanitizeToolCallInputs(messages, allowed)

	// Should have: user, assistant(1 call), tool(read), no tool(exec)
	if len(result) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result))
	}
	if len(result[1].ToolCalls) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(result[1].ToolCalls))
	}
	if result[1].ToolCalls[0].Function.Name != "read" {
		t.Errorf("expected read, got %s", result[1].ToolCalls[0].Function.Name)
	}
}

// ---------------------------------------------------------------------------
// Layer 3: RepairToolUseResultPairing
// ---------------------------------------------------------------------------

func TestRepairToolUseResultPairing_InjectsMissing(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{Role: "user", Content: "Do something"},
		{
			Role: "assistant",
			ToolCalls: []openai.ToolCall{
				{ID: "c1", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "read", Arguments: "{}"}},
				{ID: "c2", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "exec", Arguments: "{}"}},
			},
		},
		// Only c1 has a result; c2 is missing
		{Role: "tool", ToolCallID: "c1", Name: "read", Content: "file content"},
		{Role: "user", Content: "What happened?"},
	}
	result := RepairToolUseResultPairing(messages)

	// Should have: user, assistant, tool(c1), tool(c2 synthetic), user
	toolResults := 0
	for _, msg := range result {
		if msg.Role == "tool" {
			toolResults++
		}
	}
	if toolResults != 2 {
		t.Errorf("expected 2 tool results, got %d", toolResults)
	}
}

func TestRepairToolUseResultPairing_DropsOrphans(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{Role: "user", Content: "Hello"},
		{Role: "tool", ToolCallID: "orphan_id", Name: "read", Content: "orphan result"},
		{Role: "assistant", Content: "Hi"},
	}
	result := RepairToolUseResultPairing(messages)
	for _, msg := range result {
		if msg.Role == "tool" {
			t.Error("orphan tool result should be dropped")
		}
	}
}

func TestRepairToolUseResultPairing_DropsDuplicates(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{
			Role: "assistant",
			ToolCalls: []openai.ToolCall{
				{ID: "c1", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "read", Arguments: "{}"}},
			},
		},
		{Role: "tool", ToolCallID: "c1", Name: "read", Content: "first"},
		{Role: "tool", ToolCallID: "c1", Name: "read", Content: "duplicate"},
	}
	result := RepairToolUseResultPairing(messages)
	toolCount := 0
	for _, msg := range result {
		if msg.Role == "tool" {
			toolCount++
		}
	}
	if toolCount != 1 {
		t.Errorf("expected 1 tool result, got %d (duplicate not dropped)", toolCount)
	}
}

// ---------------------------------------------------------------------------
// Layer 4: StripToolResultDetails
// ---------------------------------------------------------------------------

func TestStripToolResultDetails_Truncates(t *testing.T) {
	longContent := strings.Repeat("A", 200)
	messages := []openai.ChatCompletionMessage{
		{Role: "tool", ToolCallID: "c1", Content: longContent},
	}
	result := StripToolResultDetails(messages, 100)
	content := extractOpenAICompatContent(result[0].Content)
	if !strings.HasSuffix(content, "[... truncated]") {
		t.Error("expected truncation marker")
	}
	if len(content) > 120 { // 100 + marker
		t.Errorf("content too long: %d", len(content))
	}
}

func TestStripToolResultDetails_NoTruncation(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{Role: "tool", ToolCallID: "c1", Content: "short"},
	}
	result := StripToolResultDetails(messages, 100)
	if extractOpenAICompatContent(result[0].Content) != "short" {
		t.Error("should not modify short content")
	}
}

// ---------------------------------------------------------------------------
// Layer 5: ValidateAnthropicTurns
// ---------------------------------------------------------------------------

func TestValidateAnthropicTurns_StripsDangling(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{
			Role: "assistant",
			ToolCalls: []openai.ToolCall{
				{ID: "c1", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "read"}},
			},
		},
		// No tool result follows
		{Role: "user", Content: "Continue"},
	}
	result := ValidateAnthropicTurns(messages)
	if len(result[0].ToolCalls) != 0 {
		t.Error("dangling tool calls should be stripped")
	}
}

func TestValidateAnthropicTurns_MergesConsecutiveUser(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{Role: "user", Content: "First"},
		{Role: "user", Content: "Second"},
		{Role: "assistant", Content: "Response"},
	}
	result := ValidateAnthropicTurns(messages)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	content := extractOpenAICompatContent(result[0].Content)
	if !strings.Contains(content, "First") || !strings.Contains(content, "Second") {
		t.Errorf("user messages should be merged, got %q", content)
	}
}

// ---------------------------------------------------------------------------
// Layer 6: ValidateGeminiTurns
// ---------------------------------------------------------------------------

func TestValidateGeminiTurns_MergesConsecutiveAssistant(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "First part"},
		{Role: "assistant", Content: "Second part"},
	}
	result := ValidateGeminiTurns(messages)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	content := extractOpenAICompatContent(result[1].Content)
	if !strings.Contains(content, "First part") || !strings.Contains(content, "Second part") {
		t.Errorf("assistant messages should be merged, got %q", content)
	}
}

// ---------------------------------------------------------------------------
// Layer 7: LimitHistoryTurns
// ---------------------------------------------------------------------------

func TestLimitHistoryTurns(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "First"},
		{Role: "assistant", Content: "R1"},
		{Role: "user", Content: "Second"},
		{Role: "assistant", Content: "R2"},
		{Role: "user", Content: "Third"},
		{Role: "assistant", Content: "R3"},
	}
	result := LimitHistoryTurns(messages, 2)

	// Should keep system + last 2 user turns + their responses
	if result[0].Role != "system" {
		t.Error("system message should be preserved")
	}
	userCount := 0
	for _, m := range result {
		if m.Role == "user" {
			userCount++
		}
	}
	if userCount != 2 {
		t.Errorf("expected 2 user turns, got %d", userCount)
	}
	// First user message should be "Second"
	for _, m := range result {
		if m.Role == "user" {
			if extractOpenAICompatContent(m.Content) == "First" {
				t.Error("first user turn should be truncated")
			}
			break
		}
	}
}

func TestLimitHistoryTurns_NoLimit(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{Role: "user", Content: "Hello"},
	}
	result := LimitHistoryTurns(messages, 0)
	if len(result) != 1 {
		t.Errorf("expected no truncation, got %d messages", len(result))
	}
}

// ---------------------------------------------------------------------------
// Layer 8: SanitizeToolCallIDs
// ---------------------------------------------------------------------------

func TestSanitizeToolCallIDs_Strict(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{
			Role: "assistant",
			ToolCalls: []openai.ToolCall{
				{ID: "call_abc-123!@#", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "read"}},
			},
		},
		{Role: "tool", ToolCallID: "call_abc-123!@#", Name: "read", Content: "result"},
	}
	result := SanitizeToolCallIDs(messages, "strict")

	newID := result[0].ToolCalls[0].ID
	if strings.ContainsAny(newID, "-!@#") {
		t.Errorf("strict mode should remove non-alphanumeric chars, got %q", newID)
	}
	// Tool result should have matching ID
	if result[1].ToolCallID != newID {
		t.Errorf("tool result ID should match: %q != %q", result[1].ToolCallID, newID)
	}
}

func TestSanitizeToolCallIDs_Strict9(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{
			Role: "assistant",
			ToolCalls: []openai.ToolCall{
				{ID: "call_abc", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "read"}},
			},
		},
		{Role: "tool", ToolCallID: "call_abc", Name: "read", Content: "result"},
	}
	result := SanitizeToolCallIDs(messages, "strict9")
	newID := result[0].ToolCalls[0].ID
	if len(newID) != 9 {
		t.Errorf("strict9 mode should produce 9-char ID, got %d: %q", len(newID), newID)
	}
}

// ---------------------------------------------------------------------------
// Full Pipeline
// ---------------------------------------------------------------------------

func TestSanitizeHistoryPipeline_Integration(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "First"},
		{Role: "assistant", Content: "<think>thinking</think>Response 1"},
		{Role: "user", Content: "Second"},
		{
			Role:    "assistant",
			Content: "Let me check",
			ToolCalls: []openai.ToolCall{
				{ID: "c1", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "read", Arguments: `{"path":"a.txt"}`}},
			},
		},
		{Role: "tool", ToolCallID: "c1", Name: "read", Content: "file content"},
		{Role: "assistant", Content: "Done!"},
		{Role: "user", Content: "Third"},
	}

	policy := TranscriptPolicy{
		DropThinkingBlocks:         true,
		RepairToolUseResultPairing: true,
		SanitizeToolCallIDs:        true,
		ToolCallIDMode:             "strict",
		TrimToolCallNames:         true,
	}
	allowed := map[string]bool{"read": true}

	result := SanitizeHistoryPipeline(messages, policy, allowed)

	// Verify thinking blocks are removed
	for _, msg := range result {
		if msg.Role == "assistant" {
			content := extractOpenAICompatContent(msg.Content)
			if strings.Contains(content, "<think>") {
				t.Error("thinking blocks should be removed")
			}
		}
	}

	// Verify structure is valid
	if len(result) == 0 {
		t.Fatal("pipeline should produce non-empty result")
	}
	if result[0].Role != "system" {
		t.Error("system message should be preserved")
	}
}
