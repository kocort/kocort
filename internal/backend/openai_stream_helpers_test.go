package backend

import (
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

// ---------------------------------------------------------------------------
// AccumulateOpenAIToolCalls
// ---------------------------------------------------------------------------

func TestAccumulateOpenAIToolCallsSingle(t *testing.T) {
	idx := 0
	chunks := []openai.ToolCall{
		{Index: &idx, ID: "call_1", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "read_file", Arguments: `{"path"`}},
		{Index: &idx, Function: openai.FunctionCall{Arguments: `: "/tmp"}`}},
	}
	acc := AccumulateOpenAIToolCalls(nil, chunks)
	if len(acc) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(acc))
	}
	if acc[0].ID != "call_1" {
		t.Errorf("got ID=%q", acc[0].ID)
	}
	if acc[0].Function.Name != "read_file" {
		t.Errorf("got Name=%q", acc[0].Function.Name)
	}
	if acc[0].Function.Arguments != `{"path": "/tmp"}` {
		t.Errorf("got Args=%q", acc[0].Function.Arguments)
	}
}

func TestAccumulateOpenAIToolCallsMultiple(t *testing.T) {
	idx0, idx1 := 0, 1
	chunks := []openai.ToolCall{
		{Index: &idx0, ID: "call_1", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "foo"}},
		{Index: &idx1, ID: "call_2", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "bar"}},
	}
	acc := AccumulateOpenAIToolCalls(nil, chunks)
	if len(acc) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(acc))
	}
}

// ---------------------------------------------------------------------------
// CompactOpenAIToolCalls
// ---------------------------------------------------------------------------

func TestCompactOpenAIToolCalls(t *testing.T) {
	accumulators := []*openai.ToolCall{
		{ID: "call_1"},
		nil,
		{ID: "call_3"},
	}
	compacted := CompactOpenAIToolCalls(accumulators)
	if len(compacted) != 2 {
		t.Fatalf("expected 2, got %d", len(compacted))
	}
	if compacted[0].ID != "call_1" || compacted[1].ID != "call_3" {
		t.Errorf("unexpected IDs: %v", compacted)
	}
}

func TestCompactOpenAIToolCallsEmpty(t *testing.T) {
	if got := CompactOpenAIToolCalls(nil); len(got) != 0 {
		t.Errorf("expected empty, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// ValidateOpenAICompatToolCalls
// ---------------------------------------------------------------------------

func TestValidateOpenAICompatToolCallsValid(t *testing.T) {
	calls := []openai.ToolCall{
		{ID: "call_1", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "read_file", Arguments: "{}"}},
	}
	validated, err := ValidateOpenAICompatToolCalls(calls)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(validated) != 1 {
		t.Errorf("expected 1, got %d", len(validated))
	}
}

func TestValidateOpenAICompatToolCallsEmptyID(t *testing.T) {
	calls := []openai.ToolCall{
		{ID: "", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "foo"}},
	}
	_, err := ValidateOpenAICompatToolCalls(calls)
	if err == nil {
		t.Error("expected error for empty ID")
	}
}

func TestValidateOpenAICompatToolCallsEmptyName(t *testing.T) {
	calls := []openai.ToolCall{
		{ID: "call_1", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: ""}},
	}
	_, err := ValidateOpenAICompatToolCalls(calls)
	if err == nil {
		t.Error("expected error for empty function name")
	}
}

func TestValidateOpenAICompatToolCallsAutoFillType(t *testing.T) {
	calls := []openai.ToolCall{
		{ID: "call_1", Function: openai.FunctionCall{Name: "foo"}},
	}
	validated, err := ValidateOpenAICompatToolCalls(calls)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if validated[0].Type != openai.ToolTypeFunction {
		t.Errorf("expected type=function, got %q", validated[0].Type)
	}
}

// ---------------------------------------------------------------------------
// MergeUsageMaps
// ---------------------------------------------------------------------------

func TestMergeUsageMaps(t *testing.T) {
	dst := map[string]any{"a": 1}
	src := map[string]any{"b": 2, "c": 3}
	MergeUsageMaps(dst, src)
	if dst["b"] != 2 || dst["c"] != 3 {
		t.Errorf("got %v", dst)
	}
	if dst["a"] != 1 {
		t.Error("existing key should be preserved")
	}
}

func TestMergeUsageMapsEmpty(t *testing.T) {
	dst := map[string]any{"a": 1}
	MergeUsageMaps(dst, nil)
	if len(dst) != 1 {
		t.Error("empty src should not modify dst")
	}
}

// ---------------------------------------------------------------------------
// UsageToMap
// ---------------------------------------------------------------------------

func TestUsageToMap(t *testing.T) {
	usage := openai.Usage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	}
	m := UsageToMap(usage)
	if m["prompt_tokens"] != 100 {
		t.Errorf("got prompt_tokens=%v", m["prompt_tokens"])
	}
	if m["total_tokens"] != 150 {
		t.Errorf("got total_tokens=%v", m["total_tokens"])
	}
}

func TestUsageToMapWithReasoningTokens(t *testing.T) {
	usage := openai.Usage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
		CompletionTokensDetails: &openai.CompletionTokensDetails{
			ReasoningTokens: 30,
		},
	}
	m := UsageToMap(usage)
	if m["reasoning_tokens"] != 30 {
		t.Errorf("got reasoning_tokens=%v", m["reasoning_tokens"])
	}
}

// ---------------------------------------------------------------------------
// ResolveOpenAICompatBaseURL
// ---------------------------------------------------------------------------

func TestResolveOpenAICompatBaseURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
		err   bool
	}{
		{"plain", "https://api.openai.com/v1", "https://api.openai.com/v1", false},
		{"strip_completions", "https://api.openai.com/v1/chat/completions", "https://api.openai.com/v1", false},
		{"strip_with_trailing_slash", "https://api.openai.com/v1/chat/completions/", "https://api.openai.com/v1", false},
		{"empty", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveOpenAICompatBaseURL(tt.input)
			if tt.err {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ResolveAnthropicCompatBaseURL
// ---------------------------------------------------------------------------

func TestResolveAnthropicCompatBaseURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
		err   bool
	}{
		{"plain", "https://api.anthropic.com", "https://api.anthropic.com", false},
		{"strip_v1_messages", "https://api.anthropic.com/v1/messages", "https://api.anthropic.com", false},
		{"strip_messages", "https://api.anthropic.com/messages", "https://api.anthropic.com", false},
		{"strip_v1", "https://api.anthropic.com/v1", "https://api.anthropic.com", false},
		{"empty", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveAnthropicCompatBaseURL(tt.input)
			if tt.err {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ExtractOpenAICompatContent
// ---------------------------------------------------------------------------

func TestExtractOpenAICompatContent(t *testing.T) {
	if got := ExtractOpenAICompatContent("hello"); got != "hello" {
		t.Errorf("got %q", got)
	}
	if got := ExtractOpenAICompatContent(42); got != "" {
		t.Errorf("got %q for int", got)
	}
	if got := ExtractOpenAICompatContent(nil); got != "" {
		t.Errorf("got %q for nil", got)
	}
}
