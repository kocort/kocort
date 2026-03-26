package llamawrapper

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── Think Parser Tests ───────────────────────────────────────────────────────

func TestThinkParser_BasicThinkBlock(t *testing.T) {
	p := newThinkParser()
	thinking, content := p.Add("<think>reasoning here</think>answer")
	if thinking != "reasoning here" {
		t.Errorf("got thinking=%q, want %q", thinking, "reasoning here")
	}
	if content != "answer" {
		t.Errorf("got content=%q, want %q", content, "answer")
	}
}

func TestThinkParser_Streaming(t *testing.T) {
	p := newThinkParser()
	var allThinking, allContent string

	chunks := []string{"<thi", "nk>", "hello", " world", "</thi", "nk>", "final"}
	for _, c := range chunks {
		thinking, content := p.Add(c)
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

func TestThinkParser_NoThinkBlock(t *testing.T) {
	p := newThinkParser()
	thinking, content := p.Add("just normal text")
	if thinking != "" {
		t.Errorf("expected no thinking, got %q", thinking)
	}
	if content != "just normal text" {
		t.Errorf("got content=%q, want %q", content, "just normal text")
	}
}

func TestThinkParser_EmptyThink(t *testing.T) {
	p := newThinkParser()
	thinking, content := p.Add("<think></think>answer")
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

// ── Stop Sequence Tests ──────────────────────────────────────────────────────

func TestMatchStop(t *testing.T) {
	tests := []struct {
		text  string
		stops []string
		found bool
		which string
	}{
		{"hello world", []string{"world"}, true, "world"},
		{"hello world", []string{"xyz"}, false, ""},
		{"<|im_end|>", []string{"<|im_end|>"}, true, "<|im_end|>"},
		{"no match", nil, false, ""},
	}

	for _, tc := range tests {
		found, which := matchStop(tc.text, tc.stops)
		if found != tc.found || which != tc.which {
			t.Errorf("matchStop(%q, %v) = (%v, %q), want (%v, %q)",
				tc.text, tc.stops, found, which, tc.found, tc.which)
		}
	}
}

func TestHasStopSuffix(t *testing.T) {
	tests := []struct {
		text   string
		stops  []string
		result bool
	}{
		{"hello<|im_e", []string{"<|im_end|>"}, true},
		{"hello<|", []string{"<|im_end|>"}, true},
		{"hello", []string{"<|im_end|>"}, false},
	}

	for _, tc := range tests {
		got := hasStopSuffix(tc.text, tc.stops)
		if got != tc.result {
			t.Errorf("hasStopSuffix(%q, %v) = %v, want %v", tc.text, tc.stops, got, tc.result)
		}
	}
}

func TestTrimStop(t *testing.T) {
	pieces := []string{"hello", " wor", "ld<|im_end|>"}
	result, truncated := trimStop(pieces, "<|im_end|>")
	joined := strings.Join(result, "")
	if joined != "hello world" {
		t.Errorf("got %q, want %q", joined, "hello world")
	}
	if !truncated {
		t.Error("expected truncated=true")
	}
}

func TestHasIncompleteUTF8(t *testing.T) {
	tests := []struct {
		text   string
		result bool
	}{
		{"hello", false},
		{string([]byte{0xc3}), true},        // start of 2-byte sequence
		{string([]byte{0xe4, 0xb8}), true},  // start of 3-byte, missing 1
		{"完整", false},
	}

	for _, tc := range tests {
		got := hasIncompleteUTF8(tc.text)
		if got != tc.result {
			t.Errorf("hasIncompleteUTF8(%q) = %v, want %v", tc.text, got, tc.result)
		}
	}
}

// ── Stream Parser Tests ──────────────────────────────────────────────────────

func TestStreamParser_LegacyNoThinking(t *testing.T) {
	p := newStreamParser("", nil, nil, false)
	thinking, content, toolCalls := p.Add("hello world")
	if thinking != "" {
		t.Errorf("expected no thinking, got %q", thinking)
	}
	if content != "hello world" {
		t.Errorf("got content=%q, want 'hello world'", content)
	}
	if len(toolCalls) != 0 {
		t.Errorf("expected no tool calls")
	}
}

func TestStreamParser_LegacyWithThinking(t *testing.T) {
	p := newStreamParser("", nil, nil, true)
	allThinking := ""
	allContent := ""

	for _, chunk := range []string{"<think>", "reason", "</think>", "answer"} {
		thinking, content, _ := p.Add(chunk)
		allThinking += thinking
		allContent += content
	}
	if allThinking != "reason" {
		t.Errorf("got thinking=%q, want 'reason'", allThinking)
	}
	if allContent != "answer" {
		t.Errorf("got content=%q, want 'answer'", allContent)
	}
}

// ── KV Cache Tests ───────────────────────────────────────────────────────────
// Note: kvCache tests require a real llama.Context, so we test the slot logic only.

func TestKVSlotPrefixReuse(t *testing.T) {
	// Test the prefix-matching logic conceptually.
	// kvCache.acquire picks the slot with longest common prefix.
	// We can't test the full flow without llama.Context, but we verify
	// the slot data structure works.
	slot := &kvSlot{
		id:     0,
		inputs: []input{{token: 1}, {token: 2}, {token: 3}},
		inUse:  false,
	}

	wanted := []input{{token: 1}, {token: 2}, {token: 4}}

	// Compute prefix length.
	prefix := 0
	for i := 0; i < len(slot.inputs) && i < len(wanted); i++ {
		if slot.inputs[i].token != wanted[i].token {
			break
		}
		prefix++
	}

	if prefix != 2 {
		t.Errorf("prefix=%d, want 2", prefix)
	}
}

// ── Type Serialization Tests ─────────────────────────────────────────────────

func TestChatCompletionChunkJSON(t *testing.T) {
	chunk := ChatCompletionChunk{
		ID: "chatcmpl-123", Object: "chat.completion.chunk", Created: 1000,
		Model: "test", SystemFingerprint: "fp_local",
		Choices: []ChunkChoice{{
			Index: 0,
			Delta: ChunkDelta{Content: "hello"},
		}},
	}

	data, err := json.Marshal(chunk)
	if err != nil {
		t.Fatal(err)
	}

	var decoded map[string]any
	json.Unmarshal(data, &decoded)

	if decoded["id"] != "chatcmpl-123" {
		t.Errorf("id=%v, want chatcmpl-123", decoded["id"])
	}
	if decoded["object"] != "chat.completion.chunk" {
		t.Errorf("object=%v", decoded["object"])
	}
}

func TestChatCompletionRequestJSON(t *testing.T) {
	raw := `{
		"model": "gpt-4",
		"messages": [{"role": "user", "content": "hello"}],
		"stream": true,
		"temperature": 0.7,
		"max_tokens": 100
	}`

	var req ChatCompletionRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatal(err)
	}

	if req.Model != "gpt-4" {
		t.Errorf("model=%q", req.Model)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("messages=%d", len(req.Messages))
	}
	if !req.Stream {
		t.Error("expected stream=true")
	}
	if req.Temperature == nil || *req.Temperature != 0.7 {
		t.Errorf("temperature=%v", req.Temperature)
	}
	if req.MaxTokens == nil || *req.MaxTokens != 100 {
		t.Errorf("max_tokens=%v", req.MaxTokens)
	}
}

func TestTextCompletionRequestJSON(t *testing.T) {
	raw := `{"model": "test", "prompt": "Hello", "max_tokens": 50, "stream": false}`
	var req TextCompletionRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatal(err)
	}
	if req.Prompt != "Hello" {
		t.Errorf("prompt=%q", req.Prompt)
	}
	if req.MaxTokens == nil || *req.MaxTokens != 50 {
		t.Errorf("max_tokens=%v", req.MaxTokens)
	}
}

func TestModelListJSON(t *testing.T) {
	ml := ModelList{
		Object: "list",
		Data: []ModelEntry{{
			ID: "qwen3-8b", Object: "model", Created: 1000, OwnedBy: "local",
		}},
	}
	data, err := json.Marshal(ml)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"qwen3-8b"`) {
		t.Error("missing model id in JSON")
	}
}

func TestAPIErrorJSON(t *testing.T) {
	e := APIError{Error: APIErrorDetail{Message: "bad request", Type: "invalid_request_error"}}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "bad request") {
		t.Error("missing error message")
	}
}

// ── HTTP Handler Mock Tests ──────────────────────────────────────────────────
// These test the handler request parsing without a real engine.

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusBadRequest, "test error")

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "test error") {
		t.Errorf("body=%s, want 'test error'", body)
	}
}

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, map[string]string{"hello": "world"})

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type=%q", ct)
	}
}

// ── parseStopSequences Tests ─────────────────────────────────────────────────

func TestParseStopSequences(t *testing.T) {
	tests := []struct {
		input    any
		expected []string
	}{
		{nil, nil},
		{"stop1", []string{"stop1"}},
		{[]any{"stop1", "stop2"}, []string{"stop1", "stop2"}},
	}

	for _, tc := range tests {
		got := parseStopSequences(tc.input)
		if len(got) != len(tc.expected) {
			t.Errorf("parseStopSequences(%v) = %v, want %v", tc.input, got, tc.expected)
			continue
		}
		for i := range got {
			if got[i] != tc.expected[i] {
				t.Errorf("parseStopSequences(%v)[%d] = %q, want %q", tc.input, i, got[i], tc.expected[i])
			}
		}
	}
}

// ── SamplingConfig Tests ─────────────────────────────────────────────────────

func TestDefaultSamplingConfig(t *testing.T) {
	cfg := DefaultSamplingConfig()
	if cfg.TopK != 40 {
		t.Errorf("TopK=%d, want 40", cfg.TopK)
	}
	if cfg.TopP != 0.9 {
		t.Errorf("TopP=%f, want 0.9", cfg.TopP)
	}
	if cfg.Temperature != 0.8 {
		t.Errorf("Temperature=%f, want 0.8", cfg.Temperature)
	}
}

// ── EngineStatus Tests ───────────────────────────────────────────────────────

func TestEngineStatusString(t *testing.T) {
	tests := []struct {
		status EngineStatus
		str    string
	}{
		{StatusCreated, "created"},
		{StatusLoading, "loading"},
		{StatusReady, "ready"},
		{StatusClosed, "closed"},
	}
	for _, tc := range tests {
		if got := tc.status.String(); got != tc.str {
			t.Errorf("%d.String()=%q, want %q", tc.status, got, tc.str)
		}
	}
}

func TestDoneReasonString(t *testing.T) {
	if DoneStop.String() != "stop" {
		t.Error("DoneStop")
	}
	if DoneLength.String() != "length" {
		t.Error("DoneLength")
	}
}

// ── Helper Tests ─────────────────────────────────────────────────────────────

func TestBoolPtr(t *testing.T) {
	p := BoolPtr(true)
	if p == nil || *p != true {
		t.Error("BoolPtr(true) failed")
	}
}

func TestIntPtr(t *testing.T) {
	p := IntPtr(42)
	if p == nil || *p != 42 {
		t.Error("IntPtr(42) failed")
	}
}

func TestFloat64Ptr(t *testing.T) {
	p := Float64Ptr(3.14)
	if p == nil || *p != 3.14 {
		t.Error("Float64Ptr(3.14) failed")
	}
}

// ── toFinishReason Tests ─────────────────────────────────────────────────────

func TestToFinishReason(t *testing.T) {
	s := toFinishReason(DoneStop)
	if s == nil || *s != "stop" {
		t.Error("expected 'stop'")
	}

	l := toFinishReason(DoneLength)
	if l == nil || *l != "length" {
		t.Error("expected 'length'")
	}

	d := toFinishReason(DoneDisconnect)
	if d != nil {
		t.Error("expected nil for disconnect")
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

// ── orDefault Tests ──────────────────────────────────────────────────────────

func TestOrDefault(t *testing.T) {
	if orDefault("", "fallback") != "fallback" {
		t.Error("empty string should return fallback")
	}
	if orDefault("value", "fallback") != "value" {
		t.Error("non-empty string should return value")
	}
}

// ── Context-aware test: verify Engine refuses work when not ready ────────────

func TestEngine_NotReady(t *testing.T) {
	// Create an engine without loading a model — it should refuse requests.
	e := &Engine{status: StatusCreated}

	_, err := e.ChatCompletion(context.Background(), ChatCompletionRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Error("expected error for not-ready engine")
	}
	if !strings.Contains(err.Error(), "not ready") {
		t.Errorf("unexpected error: %v", err)
	}

	_, err = e.TextCompletion(context.Background(), TextCompletionRequest{Prompt: "hi"})
	if err == nil {
		t.Error("expected error for not-ready engine")
	}

	_, _, err = e.Embedding(context.Background(), "hi")
	if err == nil {
		t.Error("expected error for not-ready engine")
	}
}

// ── Qwen3 Prompt Rendering Tests ─────────────────────────────────────────────

func TestRenderQwen3_ThinkingEnabled(t *testing.T) {
	msgs := []renderMsg{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "Hello"},
	}
	prompt, err := renderQwen3(msgs, nil, true)
	if err != nil {
		t.Fatal(err)
	}

	// System message should end with /think
	if !strings.Contains(prompt, "/think<|im_end|>") {
		t.Error("expected /think in system message")
	}
	if strings.Contains(prompt, "/no_think") {
		t.Error("should not contain /no_think when thinking is enabled")
	}
	// Should end with assistant\n (no <think> prefix)
	if !strings.HasSuffix(prompt, "assistant\n") {
		t.Errorf("expected prompt to end with 'assistant\\n', got suffix: %q", prompt[len(prompt)-30:])
	}
}

func TestRenderQwen3_ThinkingDisabled(t *testing.T) {
	msgs := []renderMsg{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "Hello"},
	}
	prompt, err := renderQwen3(msgs, nil, false)
	if err != nil {
		t.Fatal(err)
	}

	// System message should end with /no_think
	if !strings.Contains(prompt, "/no_think<|im_end|>") {
		t.Error("expected /no_think in system message")
	}
	if strings.Contains(prompt, "\n/think<|im_end|>") {
		t.Error("should not contain bare /think when thinking is disabled")
	}
}

func TestRenderQwen3_NoSystemMessage(t *testing.T) {
	msgs := []renderMsg{
		{Role: "user", Content: "Hello"},
	}
	prompt, err := renderQwen3(msgs, nil, true)
	if err != nil {
		t.Fatal(err)
	}

	// Should inject a system message with /think
	if !strings.Contains(prompt, "<|im_start|>system\n/think<|im_end|>") {
		t.Errorf("expected injected system message with /think, got: %s", prompt)
	}
}

func TestRenderQwen3_WithTools(t *testing.T) {
	msgs := []renderMsg{
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
	prompt, err := renderQwen3(msgs, tools, true)
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

// ── Qwen3 Stream Parser Tests ────────────────────────────────────────────────

func TestStreamParser_Qwen3ThinkingEnabled(t *testing.T) {
	p := newStreamParser("qwen3", nil, nil, true)
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

func TestStreamParser_Qwen3ThinkingDisabled(t *testing.T) {
	p := newStreamParser("qwen3", nil, nil, false)
	allContent := ""

	for _, chunk := range []string{"Hello", " world"} {
		_, content, _ := p.Add(chunk)
		allContent += content
	}
	if allContent != "Hello world" {
		t.Errorf("got content=%q, want 'Hello world'", allContent)
	}
}

func TestStreamParser_Qwen3ToolCall(t *testing.T) {
	p := newStreamParser("qwen3", nil, nil, false)
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

func TestStreamParser_Qwen3ThinkingWithToolCall(t *testing.T) {
	p := newStreamParser("qwen3", nil, nil, true)
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

// ── Qwen3 JSON Tool Call Parsing Tests ───────────────────────────────────────

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
