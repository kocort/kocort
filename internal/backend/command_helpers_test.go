package backend

import (
	"encoding/json"
	"testing"

	"github.com/kocort/kocort/internal/core"
)

// ---------------------------------------------------------------------------
// CloneAnyMap
// ---------------------------------------------------------------------------

func TestCloneAnyMap(t *testing.T) {
	original := map[string]any{"key": "value", "num": 42}
	cloned := CloneAnyMap(original)
	if cloned["key"] != "value" || cloned["num"] != 42 {
		t.Errorf("clone does not match original: %v", cloned)
	}
	cloned["key"] = "modified"
	if original["key"] == "modified" {
		t.Error("mutation of clone affected original")
	}
}

func TestCloneAnyMapNil(t *testing.T) {
	if got := CloneAnyMap(nil); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// AsString
// ---------------------------------------------------------------------------

func TestAsString(t *testing.T) {
	if got := AsString("hello"); got != "hello" {
		t.Errorf("got %q", got)
	}
	if got := AsString(42); got != "" {
		t.Errorf("got %q for int", got)
	}
	if got := AsString(nil); got != "" {
		t.Errorf("got %q for nil", got)
	}
}

// ---------------------------------------------------------------------------
// AsBool
// ---------------------------------------------------------------------------

func TestAsBool(t *testing.T) {
	if got := AsBool(true); !got {
		t.Error("expected true")
	}
	if got := AsBool(false); got {
		t.Error("expected false")
	}
	if got := AsBool("true"); got {
		t.Error("expected false for string")
	}
	if got := AsBool(nil); got {
		t.Error("expected false for nil")
	}
}

// ---------------------------------------------------------------------------
// MustDecodeMap
// ---------------------------------------------------------------------------

func TestMustDecodeMap(t *testing.T) {
	data := []byte(`{"key": "value", "num": 42}`)
	m := MustDecodeMap(data)
	if m["key"] != "value" {
		t.Errorf("got %v", m)
	}
}

func TestMustDecodeMapInvalid(t *testing.T) {
	m := MustDecodeMap([]byte("not json"))
	if m != nil {
		t.Errorf("expected nil for invalid JSON, got %v", m)
	}
}

func TestMustDecodeMapEmpty(t *testing.T) {
	m := MustDecodeMap([]byte("{}"))
	if m == nil || len(m) != 0 {
		t.Errorf("expected empty map, got %v", m)
	}
}

// ---------------------------------------------------------------------------
// ExtractSessionIDFromMap
// ---------------------------------------------------------------------------

func TestExtractSessionIDFromMap(t *testing.T) {
	t.Run("session_id", func(t *testing.T) {
		m := map[string]any{"session_id": "s1"}
		if got := ExtractSessionIDFromMap(m, nil); got != "s1" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("conversationId", func(t *testing.T) {
		m := map[string]any{"conversationId": "c1"}
		if got := ExtractSessionIDFromMap(m, nil); got != "c1" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("custom_fields", func(t *testing.T) {
		m := map[string]any{"myField": "f1"}
		if got := ExtractSessionIDFromMap(m, []string{"myField"}); got != "f1" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("empty", func(t *testing.T) {
		if got := ExtractSessionIDFromMap(map[string]any{}, nil); got != "" {
			t.Errorf("got %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// ParseCommandJSONOutput
// ---------------------------------------------------------------------------

func TestParseCommandJSONOutputWithText(t *testing.T) {
	output := []byte(`{"text": "hello world"}`)
	result, err := ParseCommandJSONOutput(output, defaultCmdConfig())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(result.Payloads) != 1 || result.Payloads[0].Text != "hello world" {
		t.Errorf("got %+v", result.Payloads)
	}
}

func TestParseCommandJSONOutputWithPayloads(t *testing.T) {
	output := []byte(`{"payloads": [{"text": "p1"}, {"text": "p2"}]}`)
	result, err := ParseCommandJSONOutput(output, defaultCmdConfig())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(result.Payloads) != 2 {
		t.Errorf("expected 2 payloads, got %d", len(result.Payloads))
	}
}

func TestParseCommandJSONOutputWithSessionID(t *testing.T) {
	output := []byte(`{"text": "ok", "session_id": "sess_123"}`)
	result, err := ParseCommandJSONOutput(output, defaultCmdConfig())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if result.Usage == nil || result.Usage["sessionId"] != "sess_123" {
		t.Errorf("expected sessionId in usage, got %v", result.Usage)
	}
}

func TestParseCommandJSONOutputInvalid(t *testing.T) {
	_, err := ParseCommandJSONOutput([]byte("not json"), defaultCmdConfig())
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseCommandJSONOutputWithStopReason(t *testing.T) {
	output := []byte(`{"text": "done", "stopReason": "end_turn"}`)
	result, err := ParseCommandJSONOutput(output, defaultCmdConfig())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if result.StopReason != "end_turn" {
		t.Errorf("got stopReason=%q", result.StopReason)
	}
}

func defaultCmdConfig() core.CommandBackendConfig {
	var cfg core.CommandBackendConfig
	_ = json.Unmarshal([]byte(`{}`), &cfg)
	return cfg
}
