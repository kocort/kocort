package telegram

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

func TestTelegramAdapter_ID(t *testing.T) {
	a := NewTelegramChannelAdapter(TelegramChannelID)
	if a.ID() != "telegram" {
		t.Errorf("expected 'telegram', got %q", a.ID())
	}
}

func TestTelegramAdapter_Schema(t *testing.T) {
	a := NewTelegramChannelAdapter(TelegramChannelID)
	s := a.Schema()
	if s.ID != "telegram" {
		t.Errorf("expected schema ID 'telegram', got %q", s.ID)
	}
	if len(s.Fields) == 0 {
		t.Error("expected at least one field")
	}
}

func TestTelegramAdapter_ServeHTTP_Unauthorized(t *testing.T) {
	a := NewTelegramChannelAdapter(TelegramChannelID)
	_ = a.StartBackground(context.Background(), "ch1", adapter.ChannelConfig{
		InboundToken: "secret",
	}, nil, adapter.Callbacks{
		OnMessage: func(ctx context.Context, msg adapter.InboundMessage) error {
			return nil
		},
	})

	body := `{"text":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/telegram", strings.NewReader(body))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "wrong")
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestTelegramAdapter_ServeHTTP_Update(t *testing.T) {
	a := NewTelegramChannelAdapter(TelegramChannelID)
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
		"update_id": 12345,
		"message": {
			"message_id": 42,
			"from": {"id": 111, "username": "alice"},
			"chat": {"id": 222, "type": "private"},
			"text": "Hello Telegram"
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/telegram", strings.NewReader(body))
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if captured.From != "111" {
		t.Errorf("from: expected '111', got %q", captured.From)
	}
	if captured.To != "222" {
		t.Errorf("to: expected '222', got %q", captured.To)
	}
	if captured.ChatType != adapter.ChatTypeDirect {
		t.Errorf("chatType: expected direct, got %q", captured.ChatType)
	}
	if captured.Text != "Hello Telegram" {
		t.Errorf("text: expected 'Hello Telegram', got %q", captured.Text)
	}
	if captured.AgentID != "bot1" {
		t.Errorf("agentID: expected 'bot1', got %q", captured.AgentID)
	}
	if captured.Channel != "ch1" {
		t.Errorf("channel: expected 'ch1', got %q", captured.Channel)
	}
}

func TestTelegramAdapter_ServeHTTP_GroupChat(t *testing.T) {
	a := NewTelegramChannelAdapter(TelegramChannelID)
	var captured adapter.InboundMessage
	_ = a.StartBackground(context.Background(), "ch1", adapter.ChannelConfig{}, nil, adapter.Callbacks{
		OnMessage: func(ctx context.Context, msg adapter.InboundMessage) error {
			captured = msg
			return nil
		},
	})

	body := `{
		"update_id": 12346,
		"message": {
			"message_id": 43,
			"from": {"id": 333},
			"chat": {"id": 444, "type": "supergroup"},
			"text": "Group message",
			"message_thread_id": 99
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/telegram", strings.NewReader(body))
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if captured.ChatType != adapter.ChatTypeGroup {
		t.Errorf("chatType: expected group, got %q", captured.ChatType)
	}
	if captured.ThreadID != "99" {
		t.Errorf("threadID: expected '99', got %q", captured.ThreadID)
	}
	if captured.Channel != "ch1" {
		t.Errorf("channel: expected 'ch1', got %q", captured.Channel)
	}
}

func TestTelegramAdapter_SendText(t *testing.T) {
	var receivedPayload map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedPayload)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": map[string]any{"message_id": 42},
		})
	}))
	defer ts.Close()

	a := NewTelegramChannelAdapter(TelegramChannelID)
	a.dc = infra.NewDynamicHTTPClient(nil, 5*time.Second)
	origBase := telegramAPIBase
	defer func() { telegramAPIBase = origBase }()
	telegramAPIBase = ts.URL

	result, err := a.SendText(context.Background(), adapter.OutboundMessage{
		To: "12345",
		Payload: adapter.ReplyPayload{
			Text: "hello telegram",
		},
	}, adapter.ChannelConfig{
		Config: map[string]any{"token": "bot-token-123"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.MessageID != "42" {
		t.Errorf("expected MessageID '42', got %q", result.MessageID)
	}
	if receivedPayload["chat_id"] != "12345" {
		t.Errorf("expected chat_id '12345', got %v", receivedPayload["chat_id"])
	}
	if receivedPayload["text"] != "hello telegram" {
		t.Errorf("expected text 'hello telegram', got %v", receivedPayload["text"])
	}
}

func TestTelegramAdapter_SendPhoto(t *testing.T) {
	var receivedPayload map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedPayload)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": map[string]any{"message_id": 99},
		})
	}))
	defer ts.Close()

	a := NewTelegramChannelAdapter(TelegramChannelID)
	a.dc = infra.NewDynamicHTTPClient(nil, 5*time.Second)
	origBase := telegramAPIBase
	defer func() { telegramAPIBase = origBase }()
	telegramAPIBase = ts.URL

	result, err := a.SendMedia(context.Background(), adapter.OutboundMessage{
		To: "12345",
		Payload: adapter.ReplyPayload{
			Text:     "look at this",
			MediaURL: "https://example.com/image.jpg",
		},
	}, adapter.ChannelConfig{
		Config: map[string]any{"token": "bot-token-123"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.MessageID != "99" {
		t.Errorf("expected MessageID '99', got %q", result.MessageID)
	}
	if receivedPayload["photo"] != "https://example.com/image.jpg" {
		t.Errorf("expected photo URL, got %v", receivedPayload["photo"])
	}
}

func TestTelegramAdapter_SendText_NoToken(t *testing.T) {
	a := NewTelegramChannelAdapter(TelegramChannelID)
	_, err := a.SendText(context.Background(), adapter.OutboundMessage{
		To: "12345", Payload: adapter.ReplyPayload{Text: "hello"},
	}, adapter.ChannelConfig{})
	if err != adapter.ErrTokenRequired {
		t.Errorf("expected ErrTokenRequired, got %v", err)
	}
}

func TestTelegramAdapter_SendText_NoTarget(t *testing.T) {
	a := NewTelegramChannelAdapter(TelegramChannelID)
	_, err := a.SendText(context.Background(), adapter.OutboundMessage{
		Payload: adapter.ReplyPayload{Text: "hello"},
	}, adapter.ChannelConfig{
		Config: map[string]any{"token": "bot-token-123"},
	})
	if err != adapter.ErrTargetRequired {
		t.Errorf("expected ErrTargetRequired, got %v", err)
	}
}

func TestResolveTelegramChatType(t *testing.T) {
	tests := []struct {
		in   string
		want adapter.ChatType
	}{
		{"private", adapter.ChatTypeDirect},
		{"group", adapter.ChatTypeGroup},
		{"supergroup", adapter.ChatTypeGroup},
		{"channel", adapter.ChatTypeTopic},
		{"unknown", adapter.ChatTypeDirect},
	}
	for _, tt := range tests {
		got := resolveTelegramChatType(tt.in)
		if got != tt.want {
			t.Errorf("resolveTelegramChatType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTelegramAdapter_ChunkText(t *testing.T) {
	a := NewTelegramChannelAdapter(TelegramChannelID)
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
