package slack

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

func TestSlackAdapter_ID(t *testing.T) {
	a := NewSlackChannelAdapter(SlackChannelID)
	if a.ID() != "slack" {
		t.Errorf("expected 'slack', got %q", a.ID())
	}
}

func TestSlackAdapter_Schema(t *testing.T) {
	a := NewSlackChannelAdapter(SlackChannelID)
	s := a.Schema()
	if s.ID != "slack" {
		t.Errorf("expected schema ID 'slack', got %q", s.ID)
	}
	if len(s.Fields) == 0 {
		t.Error("expected at least one field")
	}
}

func TestSlackAdapter_URLVerificationChallenge(t *testing.T) {
	a := NewSlackChannelAdapter(SlackChannelID)
	var captured adapter.InboundMessage
	err := a.StartBackground(context.Background(), "ch1", adapter.ChannelConfig{}, nil, adapter.Callbacks{
		OnMessage: func(ctx context.Context, msg adapter.InboundMessage) error {
			captured = msg
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	challengePayload := `{"type":"url_verification","challenge":"test_challenge_xyz"}`
	req := httptest.NewRequest(http.MethodPost, "/slack", strings.NewReader(challengePayload))
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "test_challenge_xyz" {
		t.Errorf("expected challenge echo, got %q", rec.Body.String())
	}
	if captured.Text != "test_challenge_xyz" {
		t.Errorf("expected captured challenge text, got %q", captured.Text)
	}
	if captured.Channel != "ch1" {
		t.Errorf("expected channel 'ch1', got %q", captured.Channel)
	}
}

func TestSlackAdapter_ServeHTTP_Unauthorized(t *testing.T) {
	a := NewSlackChannelAdapter(SlackChannelID)
	_ = a.StartBackground(context.Background(), "ch1", adapter.ChannelConfig{
		InboundToken: "secret",
	}, nil, adapter.Callbacks{
		OnMessage: func(ctx context.Context, msg adapter.InboundMessage) error {
			return nil
		},
	})

	body := `{"text":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/slack", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestSlackAdapter_ServeHTTP_WithAuth(t *testing.T) {
	a := NewSlackChannelAdapter(SlackChannelID)
	var captured adapter.InboundMessage
	_ = a.StartBackground(context.Background(), "ch1", adapter.ChannelConfig{
		InboundToken: "secret",
	}, nil, adapter.Callbacks{
		OnMessage: func(ctx context.Context, msg adapter.InboundMessage) error {
			captured = msg
			return nil
		},
	})

	body := `{"text":"hello","from":"U123","to":"C456"}`
	req := httptest.NewRequest(http.MethodPost, "/slack", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if captured.Text != "hello" {
		t.Errorf("expected 'hello', got %q", captured.Text)
	}
	if captured.From != "U123" {
		t.Errorf("expected 'U123', got %q", captured.From)
	}
	if captured.Channel != "ch1" {
		t.Errorf("expected channel 'ch1', got %q", captured.Channel)
	}
}

func TestSlackAdapter_ServeHTTP_EventPayload(t *testing.T) {
	a := NewSlackChannelAdapter(SlackChannelID)
	var captured adapter.InboundMessage
	_ = a.StartBackground(context.Background(), "ch1", adapter.ChannelConfig{}, nil, adapter.Callbacks{
		OnMessage: func(ctx context.Context, msg adapter.InboundMessage) error {
			captured = msg
			return nil
		},
	})

	body := `{
		"event": {
			"type": "message",
			"user": "U12345",
			"text": "Greetings from Slack",
			"channel": "C99999",
			"channel_type": "im",
			"thread_ts": "1234.5678",
			"client_msg_id": "msg-001"
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/slack", strings.NewReader(body))
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if captured.From != "U12345" {
		t.Errorf("from: expected 'U12345', got %q", captured.From)
	}
	if captured.To != "C99999" {
		t.Errorf("to: expected 'C99999', got %q", captured.To)
	}
	if captured.ChatType != adapter.ChatTypeDirect {
		t.Errorf("chatType: expected direct, got %q", captured.ChatType)
	}
	if captured.ThreadID != "1234.5678" {
		t.Errorf("threadID: expected '1234.5678', got %q", captured.ThreadID)
	}
	if captured.Text != "Greetings from Slack" {
		t.Errorf("text: expected 'Greetings from Slack', got %q", captured.Text)
	}
	if captured.Channel != "ch1" {
		t.Errorf("channel: expected 'ch1', got %q", captured.Channel)
	}
}

func TestSlackAdapter_SendText(t *testing.T) {
	var receivedPayload map[string]string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedPayload)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ts": "1234.5678"})
	}))
	defer ts.Close()

	a := NewSlackChannelAdapter(SlackChannelID)
	a.dc = infra.NewDynamicHTTPClient(nil, 5*time.Second)
	origBase := slackAPIBase
	defer func() { slackAPIBase = origBase }()
	slackAPIBase = ts.URL

	result, err := a.SendText(context.Background(), adapter.OutboundMessage{
		To: "#general",
		Payload: adapter.ReplyPayload{
			Text: "hello world",
		},
	}, adapter.ChannelConfig{
		Config: map[string]any{"bot_token": "xoxb-test"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.MessageID != "1234.5678" {
		t.Errorf("expected MessageID '1234.5678', got %q", result.MessageID)
	}
	if receivedPayload["channel"] != "#general" {
		t.Errorf("expected channel '#general', got %q", receivedPayload["channel"])
	}
	if receivedPayload["text"] != "hello world" {
		t.Errorf("expected text 'hello world', got %q", receivedPayload["text"])
	}
}

func TestSlackAdapter_SendMedia(t *testing.T) {
	var receivedPayload map[string]string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedPayload)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ts": "9876.5432"})
	}))
	defer ts.Close()

	a := NewSlackChannelAdapter(SlackChannelID)
	a.dc = infra.NewDynamicHTTPClient(nil, 5*time.Second)
	origBase := slackAPIBase
	defer func() { slackAPIBase = origBase }()
	slackAPIBase = ts.URL

	result, err := a.SendMedia(context.Background(), adapter.OutboundMessage{
		To: "#media-test",
		Payload: adapter.ReplyPayload{
			Text:     "check this out",
			MediaURL: "https://example.com/image.png",
		},
	}, adapter.ChannelConfig{
		Config: map[string]any{"bot_token": "xoxb-test"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.MessageID != "9876.5432" {
		t.Errorf("expected MessageID '9876.5432', got %q", result.MessageID)
	}
	expected := "check this out\nhttps://example.com/image.png"
	if receivedPayload["text"] != expected {
		t.Errorf("expected text %q, got %q", expected, receivedPayload["text"])
	}
}

func TestSlackAdapter_SendText_NoToken(t *testing.T) {
	a := NewSlackChannelAdapter(SlackChannelID)
	_, err := a.SendText(context.Background(), adapter.OutboundMessage{
		To:      "#general",
		Payload: adapter.ReplyPayload{Text: "hello"},
	}, adapter.ChannelConfig{})
	if err == nil || !strings.Contains(err.Error(), "bot token is required") {
		t.Errorf("expected token error, got %v", err)
	}
}

func TestSlackAdapter_SendText_NoTarget(t *testing.T) {
	a := NewSlackChannelAdapter(SlackChannelID)
	_, err := a.SendText(context.Background(), adapter.OutboundMessage{
		Payload: adapter.ReplyPayload{Text: "hello"},
	}, adapter.ChannelConfig{
		Config: map[string]any{"bot_token": "xoxb-test"},
	})
	if err != adapter.ErrTargetRequired {
		t.Errorf("expected ErrTargetRequired, got %v", err)
	}
}

func TestSlackAdapter_ChunkText(t *testing.T) {
	a := NewSlackChannelAdapter(SlackChannelID)
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

func TestResolveSlackChatType(t *testing.T) {
	tests := []struct {
		in   string
		want adapter.ChatType
	}{
		{"im", adapter.ChatTypeDirect},
		{"channel", adapter.ChatTypeGroup},
		{"group", adapter.ChatTypeGroup},
		{"", adapter.ChatTypeDirect},
	}
	for _, tt := range tests {
		got := resolveSlackChatType(tt.in)
		if got != tt.want {
			t.Errorf("resolveSlackChatType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
