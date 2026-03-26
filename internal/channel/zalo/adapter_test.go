package zalo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/channel/adapter"
	"github.com/kocort/kocort/internal/infra"
)

func TestZaloAdapter_ID(t *testing.T) {
	a := NewZaloChannelAdapter(ZaloChannelID)
	if a.ID() != "zalo" {
		t.Errorf("expected 'zalo', got %q", a.ID())
	}
}

func TestZaloAdapter_Schema(t *testing.T) {
	a := NewZaloChannelAdapter(ZaloChannelID)
	s := a.Schema()
	if s.ID != "zalo" {
		t.Errorf("expected schema ID 'zalo', got %q", s.ID)
	}
	if len(s.Fields) == 0 {
		t.Error("expected at least one field")
	}
}

func TestZaloAdapter_ServeHTTP_Unauthorized(t *testing.T) {
	a := NewZaloChannelAdapter(ZaloChannelID)
	_ = a.StartBackground(context.Background(), "ch1", adapter.ChannelConfig{
		InboundToken: "secret",
	}, nil, adapter.Callbacks{
		OnMessage: func(ctx context.Context, msg adapter.InboundMessage) error {
			return nil
		},
	})

	body := `{"text":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/zalo", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestZaloAdapter_ServeHTTP_WebhookEvent(t *testing.T) {
	a := NewZaloChannelAdapter(ZaloChannelID)
	var captured adapter.InboundMessage
	_ = a.StartBackground(context.Background(), "ch1", adapter.ChannelConfig{
		Agent: "bot1",
	}, nil, adapter.Callbacks{
		OnMessage: func(ctx context.Context, msg adapter.InboundMessage) error {
			captured = msg
			return nil
		},
	})

	body := `{
		"app_id": "app1",
		"event_name": "user_send_text",
		"sender": {"id": "user123"},
		"recipient": {"id": "oa456"},
		"message": {
			"msg_id": "msg789",
			"text": "Hello from Zalo"
		},
		"timestamp": "1234567890"
	}`
	req := httptest.NewRequest(http.MethodPost, "/zalo", strings.NewReader(body))
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if captured.From != "user123" {
		t.Errorf("from: expected 'user123', got %q", captured.From)
	}
	if captured.To != "oa456" {
		t.Errorf("to: expected 'oa456', got %q", captured.To)
	}
	if captured.ChatType != adapter.ChatTypeDirect {
		t.Errorf("chatType: expected direct, got %q", captured.ChatType)
	}
	if captured.Text != "Hello from Zalo" {
		t.Errorf("text: expected 'Hello from Zalo', got %q", captured.Text)
	}
	if captured.Channel != "ch1" {
		t.Errorf("channel: expected 'ch1', got %q", captured.Channel)
	}
}

func TestZaloAdapter_ServeHTTP_IgnoredEvent(t *testing.T) {
	a := NewZaloChannelAdapter(ZaloChannelID)
	callCount := 0
	_ = a.StartBackground(context.Background(), "ch1", adapter.ChannelConfig{}, nil, adapter.Callbacks{
		OnMessage: func(ctx context.Context, msg adapter.InboundMessage) error {
			callCount++
			return nil
		},
	})

	body := `{"event_name": "user_seen_message", "sender": {"id": "u"}, "recipient": {"id": "o"}, "message": {"msg_id": "m"}}`
	req := httptest.NewRequest(http.MethodPost, "/zalo", strings.NewReader(body))
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if callCount != 0 {
		t.Errorf("expected 0 calls for ignored event, got %d", callCount)
	}
}

func TestZaloAdapter_SendText(t *testing.T) {
	var receivedPayload map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedPayload)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":   0,
			"message": "Success",
			"data":    map[string]any{"message_id": "zalo-msg-001"},
		})
	}))
	defer ts.Close()

	a := NewZaloChannelAdapter(ZaloChannelID)
	a.dc = infra.NewDynamicHTTPClient(nil, 5*time.Second)
	origBase := zaloAPIBase
	defer func() { zaloAPIBase = origBase }()
	zaloAPIBase = ts.URL

	result, err := a.SendText(context.Background(), adapter.OutboundMessage{
		To: "user123",
		Payload: adapter.ReplyPayload{
			Text: "hello zalo",
		},
	}, adapter.ChannelConfig{
		Config: map[string]any{"access_token": "test-token"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.MessageID != "zalo-msg-001" {
		t.Errorf("expected MessageID 'zalo-msg-001', got %q", result.MessageID)
	}
}

func TestZaloAdapter_SendMedia(t *testing.T) {
	var receivedPayload map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedPayload)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":   0,
			"message": "Success",
			"data":    map[string]any{"message_id": "zalo-img-001"},
		})
	}))
	defer ts.Close()

	a := NewZaloChannelAdapter(ZaloChannelID)
	a.dc = infra.NewDynamicHTTPClient(nil, 5*time.Second)
	origBase := zaloAPIBase
	defer func() { zaloAPIBase = origBase }()
	zaloAPIBase = ts.URL

	result, err := a.SendMedia(context.Background(), adapter.OutboundMessage{
		To: "user123",
		Payload: adapter.ReplyPayload{
			Text:     "check this",
			MediaURL: "https://example.com/img.jpg",
		},
	}, adapter.ChannelConfig{
		Config: map[string]any{"access_token": "test-token"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.MessageID != "zalo-img-001" {
		t.Errorf("expected MessageID 'zalo-img-001', got %q", result.MessageID)
	}
}

func TestZaloAdapter_SendText_NoToken(t *testing.T) {
	a := NewZaloChannelAdapter(ZaloChannelID)
	_, err := a.SendText(context.Background(), adapter.OutboundMessage{
		To: "user123", Payload: adapter.ReplyPayload{Text: "hello"},
	}, adapter.ChannelConfig{})
	if err != adapter.ErrTokenRequired {
		t.Errorf("expected ErrTokenRequired, got %v", err)
	}
}

func TestZaloAdapter_SendText_NoTarget(t *testing.T) {
	a := NewZaloChannelAdapter(ZaloChannelID)
	_, err := a.SendText(context.Background(), adapter.OutboundMessage{
		Payload: adapter.ReplyPayload{Text: "hello"},
	}, adapter.ChannelConfig{
		Config: map[string]any{"access_token": "test-token"},
	})
	if err != adapter.ErrTargetRequired {
		t.Errorf("expected ErrTargetRequired, got %v", err)
	}
}

func TestStripMarkdown(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"**bold**", "bold"},
		{"__italic__", "italic"},
		{"`code`", "code"},
		{"```go\nfmt.Println()\n```", "fmt.Println()\n"},
		{"normal text", "normal text"},
		{"**bold** and `code`", "bold and code"},
	}
	for _, tt := range tests {
		got := StripMarkdown(tt.in)
		if got != tt.want {
			t.Errorf("StripMarkdown(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestZaloAdapter_ChunkText(t *testing.T) {
	a := NewZaloChannelAdapter(ZaloChannelID)
	short := "Hello"
	chunks := a.ChunkText(short, 100)
	if len(chunks) != 1 || chunks[0] != short {
		t.Errorf("expected single chunk %q, got %v", short, chunks)
	}

	long := strings.Repeat("A", 300)
	chunks = a.ChunkText(long, 100)
	if len(chunks) < 3 {
		t.Errorf("expected >=3 chunks, got %d", len(chunks))
	}
}
