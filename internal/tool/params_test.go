package tool

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
)

// ---------------------------------------------------------------------------
// ReadStringParam
// ---------------------------------------------------------------------------

func TestReadStringParam(t *testing.T) {
	params := map[string]any{
		"name":  "alice",
		"empty": "",
		"num":   42,
	}

	t.Run("required_present", func(t *testing.T) {
		v, err := ReadStringParam(params, "name", true)
		if err != nil || v != "alice" {
			t.Errorf("got %q, err=%v", v, err)
		}
	})
	t.Run("required_missing", func(t *testing.T) {
		_, err := ReadStringParam(params, "missing", true)
		if err == nil {
			t.Error("expected error")
		}
		if _, ok := err.(ToolInputError); !ok {
			t.Errorf("expected ToolInputError, got %T", err)
		}
	})
	t.Run("required_empty_string", func(t *testing.T) {
		_, err := ReadStringParam(params, "empty", true)
		if err == nil {
			t.Error("expected error for empty required param")
		}
	})
	t.Run("optional_missing", func(t *testing.T) {
		v, err := ReadStringParam(params, "missing", false)
		if err != nil || v != "" {
			t.Errorf("got %q, err=%v", v, err)
		}
	})
	t.Run("wrong_type", func(t *testing.T) {
		_, err := ReadStringParam(params, "num", false)
		if err == nil {
			t.Error("expected error for non-string value")
		}
	})
	t.Run("nil_value", func(t *testing.T) {
		p := map[string]any{"k": nil}
		v, err := ReadStringParam(p, "k", false)
		if err != nil || v != "" {
			t.Errorf("got %q, err=%v", v, err)
		}
	})
}

// ---------------------------------------------------------------------------
// ReadBoolParam
// ---------------------------------------------------------------------------

func TestReadBoolParam(t *testing.T) {
	t.Run("present_true", func(t *testing.T) {
		v, err := ReadBoolParam(map[string]any{"flag": true}, "flag")
		if err != nil || !v {
			t.Errorf("got %v, err=%v", v, err)
		}
	})
	t.Run("present_false", func(t *testing.T) {
		v, err := ReadBoolParam(map[string]any{"flag": false}, "flag")
		if err != nil || v {
			t.Errorf("got %v, err=%v", v, err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		v, err := ReadBoolParam(map[string]any{}, "flag")
		if err != nil || v {
			t.Errorf("got %v, err=%v", v, err)
		}
	})
	t.Run("wrong_type", func(t *testing.T) {
		_, err := ReadBoolParam(map[string]any{"flag": "yes"}, "flag")
		if err == nil {
			t.Error("expected error for string value")
		}
	})
}

// ---------------------------------------------------------------------------
// ReadOptionalPositiveDurationParam
// ---------------------------------------------------------------------------

func TestReadOptionalPositiveDurationParam(t *testing.T) {
	t.Run("float64_value", func(t *testing.T) {
		v, err := ReadOptionalPositiveDurationParam(map[string]any{"t": float64(5)}, "t", time.Second)
		if err != nil || v != 5*time.Second {
			t.Errorf("got %v, err=%v", v, err)
		}
	})
	t.Run("int_value", func(t *testing.T) {
		v, err := ReadOptionalPositiveDurationParam(map[string]any{"t": 10}, "t", time.Minute)
		if err != nil || v != 10*time.Minute {
			t.Errorf("got %v, err=%v", v, err)
		}
	})
	t.Run("int64_value", func(t *testing.T) {
		v, err := ReadOptionalPositiveDurationParam(map[string]any{"t": int64(3)}, "t", time.Hour)
		if err != nil || v != 3*time.Hour {
			t.Errorf("got %v, err=%v", v, err)
		}
	})
	t.Run("negative_ignored", func(t *testing.T) {
		v, err := ReadOptionalPositiveDurationParam(map[string]any{"t": float64(-1)}, "t", time.Second)
		if err != nil || v != 0 {
			t.Errorf("got %v, err=%v", v, err)
		}
	})
	t.Run("zero_ignored", func(t *testing.T) {
		v, err := ReadOptionalPositiveDurationParam(map[string]any{"t": float64(0)}, "t", time.Second)
		if err != nil || v != 0 {
			t.Errorf("got %v, err=%v", v, err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		v, err := ReadOptionalPositiveDurationParam(map[string]any{}, "t", time.Second)
		if err != nil || v != 0 {
			t.Errorf("got %v, err=%v", v, err)
		}
	})
	t.Run("wrong_type", func(t *testing.T) {
		_, err := ReadOptionalPositiveDurationParam(map[string]any{"t": "five"}, "t", time.Second)
		if err == nil {
			t.Error("expected error for string value")
		}
	})
}

// ---------------------------------------------------------------------------
// ReadOptionalStringMapParam
// ---------------------------------------------------------------------------

func TestReadOptionalStringMapParam(t *testing.T) {
	t.Run("valid_map", func(t *testing.T) {
		m, err := ReadOptionalStringMapParam(map[string]any{
			"env": map[string]any{"KEY": "val"},
		}, "env")
		if err != nil || m["KEY"] != "val" {
			t.Errorf("got %v, err=%v", m, err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		m, err := ReadOptionalStringMapParam(map[string]any{}, "env")
		if err != nil || m != nil {
			t.Errorf("got %v, err=%v", m, err)
		}
	})
	t.Run("wrong_type", func(t *testing.T) {
		_, err := ReadOptionalStringMapParam(map[string]any{"env": "bad"}, "env")
		if err == nil {
			t.Error("expected error for string value")
		}
	})
	t.Run("nested_wrong_type", func(t *testing.T) {
		_, err := ReadOptionalStringMapParam(map[string]any{
			"env": map[string]any{"KEY": 123},
		}, "env")
		if err == nil {
			t.Error("expected error for non-string nested value")
		}
	})
}

// ---------------------------------------------------------------------------
// JSONResult
// ---------------------------------------------------------------------------

func TestJSONResult(t *testing.T) {
	result, err := JSONResult(map[string]string{"status": "ok"})
	if err != nil {
		t.Fatalf("JSONResult: %v", err)
	}
	if len(result.JSON) == 0 {
		t.Error("expected non-empty JSON")
	}
	var decoded map[string]string
	if err := json.Unmarshal(result.JSON, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", decoded["status"])
	}
	if result.Text != string(result.JSON) {
		t.Error("expected Text == JSON string")
	}
}

// ---------------------------------------------------------------------------
// ResolveToolResultText
// ---------------------------------------------------------------------------

func TestResolveToolResultText(t *testing.T) {
	t.Run("text_preferred", func(t *testing.T) {
		r := core.ToolResult{Text: "hello", JSON: json.RawMessage(`{"x":1}`)}
		if got := ResolveToolResultText(r); got != "hello" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("falls_back_to_json", func(t *testing.T) {
		r := core.ToolResult{JSON: json.RawMessage(`{"x":1}`)}
		if got := ResolveToolResultText(r); got != `{"x":1}` {
			t.Errorf("got %q", got)
		}
	})
	t.Run("empty", func(t *testing.T) {
		if got := ResolveToolResultText(core.ToolResult{}); got != "" {
			t.Errorf("got %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// ResolveToolResultHistoryContent
// ---------------------------------------------------------------------------

func TestResolveToolResultHistoryContent(t *testing.T) {
	t.Run("with_text", func(t *testing.T) {
		if got := ResolveToolResultHistoryContent(core.ToolResult{Text: "data"}); got != "data" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("empty_returns_braces", func(t *testing.T) {
		if got := ResolveToolResultHistoryContent(core.ToolResult{}); got != "{}" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("with_media_url", func(t *testing.T) {
		result := core.ToolResult{MediaURL: "file:///tmp/image.png"}
		got := ResolveToolResultHistoryContent(result)
		if got == "{}" {
			t.Error("expected media URL to be included in history")
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(got), &decoded); err != nil {
			t.Fatalf("expected valid JSON, got %q: %v", got, err)
		}
		if decoded["mediaUrl"] != "file:///tmp/image.png" {
			t.Errorf("expected mediaUrl, got %v", decoded)
		}
	})
	t.Run("with_media_urls_array", func(t *testing.T) {
		result := core.ToolResult{MediaURLs: []string{"file:///tmp/a.png", "file:///tmp/b.png"}}
		got := ResolveToolResultHistoryContent(result)
		var decoded map[string]any
		if err := json.Unmarshal([]byte(got), &decoded); err != nil {
			t.Fatalf("expected valid JSON, got %q: %v", got, err)
		}
		urls, ok := decoded["mediaUrls"].([]any)
		if !ok || len(urls) != 2 {
			t.Errorf("expected mediaUrls array with 2 items, got %v", decoded["mediaUrls"])
		}
	})
	t.Run("text_and_media_merged", func(t *testing.T) {
		result := core.ToolResult{
			Text:     `{"status":"ok"}`,
			MediaURL: "file:///tmp/file.txt",
		}
		got := ResolveToolResultHistoryContent(result)
		var decoded map[string]any
		if err := json.Unmarshal([]byte(got), &decoded); err != nil {
			t.Fatalf("expected valid JSON, got %q: %v", got, err)
		}
		if decoded["status"] != "ok" {
			t.Errorf("expected status=ok, got %v", decoded)
		}
		if decoded["mediaUrl"] != "file:///tmp/file.txt" {
			t.Errorf("expected mediaUrl, got %v", decoded)
		}
	})
}

// ---------------------------------------------------------------------------
// IsRecoverableToolFailureMessage
// ---------------------------------------------------------------------------

func TestIsRecoverableToolFailureMessage(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"missing required parameter", true},
		{"invalid input provided", true},
		{"parameter required but empty", true},
		{"unknown error occurred", false},
		{"timeout", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.msg, func(t *testing.T) {
			if got := IsRecoverableToolFailureMessage(tc.msg); got != tc.want {
				t.Errorf("IsRecoverableToolFailureMessage(%q) = %v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ExtractReservedToolRuntimeArgs
// ---------------------------------------------------------------------------

func TestExtractReservedToolRuntimeArgs(t *testing.T) {
	t.Run("extracts_toolCallId", func(t *testing.T) {
		args := map[string]any{
			"__toolCallId": "call_123",
			"command":      "ls",
		}
		id, cleaned := ExtractReservedToolRuntimeArgs(args)
		if id != "call_123" {
			t.Errorf("got id=%q", id)
		}
		if _, ok := cleaned["__toolCallId"]; ok {
			t.Error("expected __toolCallId to be removed")
		}
		if cleaned["command"] != "ls" {
			t.Error("expected normal args to be preserved")
		}
	})
	t.Run("empty_args", func(t *testing.T) {
		id, cleaned := ExtractReservedToolRuntimeArgs(nil)
		if id != "" || len(cleaned) != 0 {
			t.Errorf("got id=%q, cleaned=%v", id, cleaned)
		}
	})
	t.Run("no_reserved_args", func(t *testing.T) {
		args := map[string]any{"a": "b"}
		id, cleaned := ExtractReservedToolRuntimeArgs(args)
		if id != "" {
			t.Errorf("got id=%q", id)
		}
		if cleaned["a"] != "b" {
			t.Error("expected args preserved")
		}
	})
}
