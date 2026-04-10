package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/localmodel/chatfmt"
)

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
		{string([]byte{0xc3}), true},       // start of 2-byte sequence
		{string([]byte{0xe4, 0xb8}), true}, // start of 3-byte, missing 1
		{"完整", false},
	}

	for _, tc := range tests {
		got := hasIncompleteUTF8(tc.text)
		if got != tc.result {
			t.Errorf("hasIncompleteUTF8(%q) = %v, want %v", tc.text, got, tc.result)
		}
	}
}

// ── KV Cache Tests ───────────────────────────────────────────────────────────

func TestKVSlotPrefixReuse(t *testing.T) {
	slot := &kvSlot{
		id:     0,
		inputs: []input{{token: 1}, {token: 2}, {token: 3}},
		inUse:  false,
	}

	wanted := []input{{token: 1}, {token: 2}, {token: 4}}

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

// ── orDefault Tests ──────────────────────────────────────────────────────────

// ── thinkingMode Tests ───────────────────────────────────────────────────────

func TestThinkingMode(t *testing.T) {
	e := &Engine{enableThinking: false}

	// Default: off
	req := &ChatCompletionRequest{}
	if m := e.thinkingMode(req); m != chatfmt.ThinkingOff {
		t.Errorf("default: got %v, want ThinkingOff", m)
	}

	// Enabled via EnableThinking
	req = &ChatCompletionRequest{EnableThinking: BoolPtr(true)}
	if m := e.thinkingMode(req); m != chatfmt.ThinkingOn {
		t.Errorf("enable: got %v, want ThinkingOn", m)
	}

	// Disabled via EnableThinking
	req = &ChatCompletionRequest{EnableThinking: BoolPtr(false)}
	if m := e.thinkingMode(req); m != chatfmt.ThinkingDisabled {
		t.Errorf("disable: got %v, want ThinkingDisabled", m)
	}

	// JSON grammar → always off
	req = &ChatCompletionRequest{
		ResponseFormat: &ResponseFormat{Type: "json_object"},
		EnableThinking: BoolPtr(true),
	}
	if m := e.thinkingMode(req); m != chatfmt.ThinkingOff {
		t.Errorf("json_grammar: got %v, want ThinkingOff", m)
	}

	// Engine default on
	e2 := &Engine{enableThinking: true}
	req = &ChatCompletionRequest{}
	if m := e2.thinkingMode(req); m != chatfmt.ThinkingOn {
		t.Errorf("engine_default: got %v, want ThinkingOn", m)
	}
}

// ── normalizeMessages Tests ──────────────────────────────────────────────────

func TestNormalizeMessages_Simple(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hello"},
	}
	out, images, err := normalizeMessages(msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 0 {
		t.Errorf("expected no images, got %d", len(images))
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}
	if out[0].Role != "system" || out[0].Content != "You are helpful." {
		t.Errorf("msg[0] = %+v", out[0])
	}
	if out[1].Role != "user" || out[1].Content != "Hello" {
		t.Errorf("msg[1] = %+v", out[1])
	}
}

// ── Context-aware test: verify Engine refuses work when not ready ────────────

func TestEngine_NotReady(t *testing.T) {
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
