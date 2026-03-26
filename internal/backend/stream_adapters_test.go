package backend

import (
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestToolCallNameTrimmer(t *testing.T) {
	adapter := &toolCallNameTrimmer{}
	choice := openai.ChatCompletionStreamChoice{
		Delta: openai.ChatCompletionStreamChoiceDelta{
			ToolCalls: []openai.ToolCall{
				{Function: openai.FunctionCall{Name: "  read  "}},
				{Function: openai.FunctionCall{Name: "exec\n"}},
			},
		},
	}
	adapter.ProcessChoice(&choice)
	if choice.Delta.ToolCalls[0].Function.Name != "read" {
		t.Errorf("expected trimmed name 'read', got %q", choice.Delta.ToolCalls[0].Function.Name)
	}
	if choice.Delta.ToolCalls[1].Function.Name != "exec" {
		t.Errorf("expected trimmed name 'exec', got %q", choice.Delta.ToolCalls[1].Function.Name)
	}
}

func TestToolCallNameTrimmer_FinalizeToolCalls(t *testing.T) {
	adapter := &toolCallNameTrimmer{}
	calls := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: " write "}},
	}
	result := adapter.FinalizeToolCalls(calls)
	if result[0].Function.Name != "write" {
		t.Errorf("expected trimmed name, got %q", result[0].Function.Name)
	}
}

func TestMalformedArgsRepairer_ValidJSON(t *testing.T) {
	adapter := &malformedArgsRepairer{}
	calls := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: "read", Arguments: `{"path":"a.txt"}`}},
	}
	result := adapter.FinalizeToolCalls(calls)
	if result[0].Function.Arguments != `{"path":"a.txt"}` {
		t.Errorf("valid JSON should not change, got %q", result[0].Function.Arguments)
	}
}

func TestMalformedArgsRepairer_FixesMalformed(t *testing.T) {
	adapter := &malformedArgsRepairer{}
	calls := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: "read", Arguments: `{"path":"a.txt"}garbage extra stuff`}},
	}
	result := adapter.FinalizeToolCalls(calls)
	if result[0].Function.Arguments != `{"path":"a.txt"}` {
		t.Errorf("expected repaired JSON, got %q", result[0].Function.Arguments)
	}
}

func TestMalformedArgsRepairer_UnfixableMalformed(t *testing.T) {
	adapter := &malformedArgsRepairer{}
	calls := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: "read", Arguments: `not json at all`}},
	}
	result := adapter.FinalizeToolCalls(calls)
	// Should not modify unfixable content
	if result[0].Function.Arguments != "not json at all" {
		t.Errorf("unfixable content should be unchanged")
	}
}

func TestHTMLEntityArgsDecoder(t *testing.T) {
	adapter := &htmlEntityArgsDecoder{}
	calls := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: "write", Arguments: `{"content":"a &amp; b &lt; c"}`}},
	}
	result := adapter.FinalizeToolCalls(calls)
	expected := `{"content":"a & b < c"}`
	if result[0].Function.Arguments != expected {
		t.Errorf("expected decoded HTML entities, got %q", result[0].Function.Arguments)
	}
}

func TestHTMLEntityArgsDecoder_NoEntities(t *testing.T) {
	adapter := &htmlEntityArgsDecoder{}
	calls := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: "read", Arguments: `{"path":"a.txt"}`}},
	}
	result := adapter.FinalizeToolCalls(calls)
	if result[0].Function.Arguments != `{"path":"a.txt"}` {
		t.Errorf("should not modify args without HTML entities")
	}
}

func TestChainAdapters(t *testing.T) {
	chain := ChainAdapters(
		&toolCallNameTrimmer{},
		&htmlEntityArgsDecoder{},
	)

	calls := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: " write ", Arguments: `{"content":"a &amp; b"}`}},
	}
	result := chain.FinalizeToolCalls(calls)
	if result[0].Function.Name != "write" {
		t.Errorf("expected trimmed name")
	}
	if result[0].Function.Arguments != `{"content":"a & b"}` {
		t.Errorf("expected decoded entities, got %q", result[0].Function.Arguments)
	}
}

func TestResolveStreamAdapters_Default(t *testing.T) {
	policy := TranscriptPolicy{TrimToolCallNames: true}
	adapter := ResolveStreamAdapters(policy)
	if adapter == nil {
		t.Fatal("should not return nil")
	}
}

func TestResolveStreamAdapters_Kimi(t *testing.T) {
	policy := ResolveTranscriptPolicy("kimi", "", "moonshot-v1-8k")
	adapter := ResolveStreamAdapters(policy)
	// Should include malformed args repairer
	calls := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: "read", Arguments: `{"path":"a.txt"}trailing junk`}},
	}
	result := adapter.FinalizeToolCalls(calls)
	if result[0].Function.Arguments != `{"path":"a.txt"}` {
		t.Errorf("kimi adapter should repair args, got %q", result[0].Function.Arguments)
	}
}

func TestResolveStreamAdapters_Xai(t *testing.T) {
	policy := ResolveTranscriptPolicy("xai", "", "grok-2")
	adapter := ResolveStreamAdapters(policy)
	calls := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: "write", Arguments: `{"content":"a &amp; b"}`}},
	}
	result := adapter.FinalizeToolCalls(calls)
	if result[0].Function.Arguments != `{"content":"a & b"}` {
		t.Errorf("xai adapter should decode HTML entities, got %q", result[0].Function.Arguments)
	}
}

func TestExtractBalancedJSONPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`{"key":"value"}extra`, `{"key":"value"}`},
		{`{"nested":{"a":1}}more`, `{"nested":{"a":1}}`},
		{`not json`, ""},
		{`{"key": "val\"ue"}rest`, `{"key": "val\"ue"}`},
		{`{}garbage`, `{}`},
		{`{"unclosed": "string`, ""},
	}
	for _, tt := range tests {
		got := extractBalancedJSONPrefix(tt.input)
		if got != tt.want {
			t.Errorf("extractBalancedJSONPrefix(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
