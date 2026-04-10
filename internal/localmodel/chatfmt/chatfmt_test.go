package chatfmt

import (
	"encoding/json"
	"strings"
	"testing"
)

// ── Think Parser Tests (legacy/ChatML) ───────────────────────────────────────

func TestLegacyParser_BasicThinkBlock(t *testing.T) {
	f := &ChatML{}
	p := f.NewParser(nil, nil, ThinkingOn)
	thinking, content, _ := p.Add("<think>reasoning here</think>answer")
	if thinking != "reasoning here" {
		t.Errorf("got thinking=%q, want %q", thinking, "reasoning here")
	}
	if content != "answer" {
		t.Errorf("got content=%q, want %q", content, "answer")
	}
}

func TestLegacyParser_Streaming(t *testing.T) {
	f := &ChatML{}
	p := f.NewParser(nil, nil, ThinkingOn)
	var allThinking, allContent string

	chunks := []string{"<thi", "nk>", "hello", " world", "</thi", "nk>", "final"}
	for _, c := range chunks {
		thinking, content, _ := p.Add(c)
		allThinking += thinking
		allContent += content
	}
	if allThinking != "hello world" {
		t.Errorf("got thinking=%q, want %q", allThinking, "hello world")
	}
	if allContent != "final" {
		t.Errorf("got content=%q, want %q", allContent, "final")
	}
}

func TestLegacyParser_NoThinkBlock(t *testing.T) {
	f := &ChatML{}
	p := f.NewParser(nil, nil, ThinkingOff)
	_, content, _ := p.Add("just normal text")
	if content != "just normal text" {
		t.Errorf("got content=%q, want %q", content, "just normal text")
	}
}

func TestLegacyParser_EmptyThink(t *testing.T) {
	f := &ChatML{}
	p := f.NewParser(nil, nil, ThinkingOn)
	thinking, content, _ := p.Add("<think></think>answer")
	if thinking != "" {
		t.Errorf("expected empty thinking, got %q", thinking)
	}
	if content != "answer" {
		t.Errorf("got content=%q, want %q", content, "answer")
	}
}

// ── Tool Call Parser Tests ───────────────────────────────────────────────────

func TestParseToolCallXML_Basic(t *testing.T) {
	raw := `<function name="get_weather"><parameter name="city">Tokyo</parameter></function>`

	tools := []Tool{{
		Type: "function",
		Function: ToolDefFunc{
			Name: "get_weather",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"city": {"type": "string"}
				}
			}`),
		},
	}}

	call, err := parseToolCallXML(raw, tools)
	if err != nil {
		t.Fatal(err)
	}
	if call.Function.Name != "get_weather" {
		t.Errorf("got name=%q, want %q", call.Function.Name, "get_weather")
	}
	var args map[string]any
	json.Unmarshal([]byte(call.Function.Arguments), &args)
	if args["city"] != "Tokyo" {
		t.Errorf("got city=%v, want 'Tokyo'", args["city"])
	}
}

func TestParseToolCallXML_NoToolCalls(t *testing.T) {
	_, err := parseToolCallXML("just text, no tool calls", nil)
	if err == nil {
		t.Error("expected error for non-XML input")
	}
}

func TestParseToolCallJSON_Basic(t *testing.T) {
	raw := `{"name": "get_weather", "arguments": {"city": "Tokyo", "units": "celsius"}}`
	call, err := parseToolCallJSON(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if call.Function.Name != "get_weather" {
		t.Errorf("got name=%q, want 'get_weather'", call.Function.Name)
	}
	var args map[string]any
	json.Unmarshal([]byte(call.Function.Arguments), &args)
	if args["city"] != "Tokyo" {
		t.Errorf("got city=%v, want 'Tokyo'", args["city"])
	}
	if args["units"] != "celsius" {
		t.Errorf("got units=%v, want 'celsius'", args["units"])
	}
}

func TestParseToolCallJSON_EmptyName(t *testing.T) {
	raw := `{"name": "", "arguments": {}}`
	_, err := parseToolCallJSON(raw, nil)
	if err == nil {
		t.Error("expected error for empty function name")
	}
}

func TestParseToolCallJSON_InvalidJSON(t *testing.T) {
	_, err := parseToolCallJSON("not json", nil)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// ── suffixPrefixOverlap Tests ────────────────────────────────────────────────

func TestSuffixPrefixOverlap(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"abc<th", "<think>", 3},
		{"hello", "<think>", 0},
		{"<think>", "<think>", 7},
		{"no overlap", "xyz", 0},
	}
	for _, tc := range tests {
		got := suffixPrefixOverlap(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("suffixPrefixOverlap(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// ── Qwen3 Stream Parser Tests ────────────────────────────────────────────────

func TestQwen3Parser_ThinkingEnabled(t *testing.T) {
	f := &Qwen3{}
	p := f.NewParser(nil, nil, ThinkingOn)
	allThinking := ""
	allContent := ""

	for _, chunk := range []string{"<think>", "reason", "ing", "</think>", "answer"} {
		thinking, content, _ := p.Add(chunk)
		allThinking += thinking
		allContent += content
	}
	if allThinking != "reasoning" {
		t.Errorf("got thinking=%q, want 'reasoning'", allThinking)
	}
	if allContent != "answer" {
		t.Errorf("got content=%q, want 'answer'", allContent)
	}
}

func TestQwen3Parser_ThinkingDisabled(t *testing.T) {
	f := &Qwen3{}
	p := f.NewParser(nil, nil, ThinkingOff)
	allContent := ""

	for _, chunk := range []string{"Hello", " world"} {
		_, content, _ := p.Add(chunk)
		allContent += content
	}
	if allContent != "Hello world" {
		t.Errorf("got content=%q, want 'Hello world'", allContent)
	}
}

func TestQwen3Parser_ToolCall(t *testing.T) {
	f := &Qwen3{}
	p := f.NewParser(nil, nil, ThinkingOff)
	var allCalls []ToolCall
	allContent := ""

	for _, chunk := range []string{
		"Let me check. ",
		"<tool_call>",
		`{"name": "get_weather", "arguments": {"city": "Tokyo"}}`,
		"</tool_call>",
	} {
		_, content, calls := p.Add(chunk)
		allContent += content
		allCalls = append(allCalls, calls...)
	}
	if !strings.Contains(allContent, "Let me check.") {
		t.Errorf("expected content before tool call, got %q", allContent)
	}
	if len(allCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(allCalls))
	}
	if allCalls[0].Function.Name != "get_weather" {
		t.Errorf("got name=%q, want 'get_weather'", allCalls[0].Function.Name)
	}
}

func TestQwen3Parser_ThinkingWithToolCall(t *testing.T) {
	f := &Qwen3{}
	p := f.NewParser(nil, nil, ThinkingOn)
	allThinking := ""
	var allCalls []ToolCall

	for _, chunk := range []string{
		"<think>",
		"I need to get weather",
		"</think>",
		"\n",
		"<tool_call>",
		`{"name": "get_weather", "arguments": {"city": "London"}}`,
		"</tool_call>",
	} {
		thinking, _, calls := p.Add(chunk)
		allThinking += thinking
		allCalls = append(allCalls, calls...)
	}
	if allThinking != "I need to get weather" {
		t.Errorf("got thinking=%q, want 'I need to get weather'", allThinking)
	}
	if len(allCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(allCalls))
	}
	if allCalls[0].Function.Name != "get_weather" {
		t.Errorf("got name=%q, want 'get_weather'", allCalls[0].Function.Name)
	}
}

// ── Qwen3 Rendering Tests ────────────────────────────────────────────────────

func TestQwen3Render_ThinkingEnabled(t *testing.T) {
	f := &Qwen3{}
	msgs := []Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "Hello"},
	}
	prompt, err := f.Render(msgs, nil, ThinkingOn)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(prompt, "/think<|im_end|>") {
		t.Error("expected /think in system message")
	}
	if strings.Contains(prompt, "/no_think") {
		t.Error("should not contain /no_think when thinking is enabled")
	}
	if !strings.HasSuffix(prompt, "assistant\n") {
		t.Errorf("expected prompt to end with 'assistant\\n', got suffix: %q", prompt[len(prompt)-30:])
	}
}

func TestQwen3Render_ThinkingDisabled(t *testing.T) {
	f := &Qwen3{}
	msgs := []Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "Hello"},
	}
	prompt, err := f.Render(msgs, nil, ThinkingOff)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(prompt, "/no_think<|im_end|>") {
		t.Error("expected /no_think in system message")
	}
}

func TestQwen3Render_NoSystemMessage(t *testing.T) {
	f := &Qwen3{}
	msgs := []Message{
		{Role: "user", Content: "Hello"},
	}
	prompt, err := f.Render(msgs, nil, ThinkingOn)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(prompt, "<|im_start|>system\n/think<|im_end|>") {
		t.Errorf("expected injected system message with /think, got: %s", prompt)
	}
}

func TestQwen3Render_WithTools(t *testing.T) {
	f := &Qwen3{}
	msgs := []Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "What's the weather?"},
	}
	tools := []Tool{{
		Type: "function",
		Function: ToolDefFunc{
			Name:        "get_weather",
			Description: "Get weather",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
		},
	}}
	prompt, err := f.Render(msgs, tools, ThinkingOn)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(prompt, "# Tools") {
		t.Error("expected tool system prompt")
	}
	if !strings.Contains(prompt, "get_weather") {
		t.Error("expected tool name in prompt")
	}
	if !strings.Contains(prompt, "/think<|im_end|>") {
		t.Error("expected /think directive in system message with tools")
	}
}

// ── Format detection Tests ───────────────────────────────────────────────────

func TestDetect(t *testing.T) {
	tests := []struct {
		tpl  string
		arch string
		want string
	}{
		{"", "qwen3", "qwen3"},
		{"", "qwen35", "qwen3.5"},
		{"", "gemma4", "gemma4"},
		{"", "llama", "chatml"},
		{"something with <|channel> and <turn|>", "", "gemma4"},
		{"something with <function=tool>", "", "qwen3.5"},
		{"something with /think and <tool_call>", "", "qwen3"},
	}
	for _, tc := range tests {
		f := Detect(tc.tpl, tc.arch)
		if f.Name() != tc.want {
			t.Errorf("Detect(%q, %q) = %q, want %q", tc.tpl, tc.arch, f.Name(), tc.want)
		}
	}
}

func TestByArch(t *testing.T) {
	if byArch("qwen3").Name() != "qwen3" {
		t.Error("qwen3")
	}
	if byArch("qwen3moe").Name() != "qwen3" {
		t.Error("qwen3moe")
	}
	if byArch("gemma4").Name() != "gemma4" {
		t.Error("gemma4")
	}
	if byArch("llama").Name() != "chatml" {
		t.Error("llama should default to chatml")
	}
}

// ── marshalSpaced Tests ──────────────────────────────────────────────────────

func TestMarshalSpaced(t *testing.T) {
	v := map[string]any{"key": "value", "num": 42}
	b, err := marshalSpaced(v)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, ": ") {
		t.Error("expected space after colon")
	}
}

// ── splitReasoning Tests ─────────────────────────────────────────────────────

func TestSplitReasoning(t *testing.T) {
	// Pre-extracted reasoning
	r, c := splitReasoning("content", "reasoning", true)
	if r != "reasoning" || c != "content" {
		t.Errorf("pre-extracted: got r=%q c=%q", r, c)
	}

	// Inline think tags
	r, c = splitReasoning("<think>inner</think>after", "", false)
	if r != "inner" || c != "after" {
		t.Errorf("inline: got r=%q c=%q", r, c)
	}
}
