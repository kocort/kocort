package discord

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

func TestDiscordChannelAdapterID(t *testing.T) {
	a := NewDiscordChannelAdapter("discord")
	if a.ID() != "discord" {
		t.Fatalf("expected 'discord', got %q", a.ID())
	}
}

func TestDiscordSchema(t *testing.T) {
	a := NewDiscordChannelAdapter("discord")
	s := a.Schema()
	if s.ID != DiscordChannelID {
		t.Fatalf("expected schema ID %q, got %q", DiscordChannelID, s.ID)
	}
	if len(s.Fields) == 0 {
		t.Fatal("expected at least one field in schema")
	}
}

func TestDiscordServeHTTP(t *testing.T) {
	a := NewDiscordChannelAdapter("discord")

	var received adapter.InboundMessage
	cb := adapter.Callbacks{
		OnMessage: func(ctx context.Context, msg adapter.InboundMessage) error {
			received = msg
			return nil
		},
	}
	_ = a.StartBackground(context.Background(), "discord-custom", adapter.ChannelConfig{}, nil, cb)

	body := `{"from":"user123","to":"channel456","text":"hello discord","messageId":"msg789"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if received.Channel != "discord-custom" {
		t.Errorf("expected channel 'discord-custom', got %q", received.Channel)
	}
	if received.From != "user123" {
		t.Errorf("expected from 'user123', got %q", received.From)
	}
	if received.To != "channel456" {
		t.Errorf("expected to 'channel456', got %q", received.To)
	}
	if received.Text != "hello discord" {
		t.Errorf("expected text 'hello discord', got %q", received.Text)
	}
	if received.MessageID != "msg789" {
		t.Errorf("expected messageId 'msg789', got %q", received.MessageID)
	}
}

func TestDiscordServeHTTPRejectsUnauthorized(t *testing.T) {
	a := NewDiscordChannelAdapter("discord")
	cb := adapter.Callbacks{
		OnMessage: func(ctx context.Context, msg adapter.InboundMessage) error { return nil },
	}
	_ = a.StartBackground(context.Background(), "discord", adapter.ChannelConfig{
		InboundToken: "correct-token",
	}, nil, cb)

	body := `{"text":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong-token")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestDiscordServeHTTPAcceptsValidToken(t *testing.T) {
	a := NewDiscordChannelAdapter("discord")
	cb := adapter.Callbacks{
		OnMessage: func(ctx context.Context, msg adapter.InboundMessage) error { return nil },
	}
	_ = a.StartBackground(context.Background(), "discord", adapter.ChannelConfig{
		InboundToken: "my-secret",
	}, nil, cb)

	body := `{"text":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer my-secret")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestDiscordSendTextCallsAPI(t *testing.T) {
	var capturedBody map[string]string
	var capturedAuth string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		data, _ := io.ReadAll(r.Body)
		json.Unmarshal(data, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"id":"msg-001"}`))
	}))
	defer ts.Close()

	a := NewDiscordChannelAdapter("discord")
	a.dc = infra.NewDynamicHTTPClient(nil, 5*time.Second)
	origBase := discordAPIBase
	defer func() { discordAPIBase = origBase }()
	discordAPIBase = ts.URL

	result, err := a.SendText(context.Background(), adapter.OutboundMessage{
		To:      "12345",
		Payload: adapter.ReplyPayload{Text: "Hello Discord!"},
	}, adapter.ChannelConfig{
		Config: map[string]any{"token": "bot-token-abc"},
	})
	if err != nil {
		t.Fatalf("SendText failed: %v", err)
	}
	if result.MessageID != "msg-001" {
		t.Errorf("expected message ID 'msg-001', got %q", result.MessageID)
	}
	if capturedAuth != "Bot bot-token-abc" {
		t.Errorf("expected auth 'Bot bot-token-abc', got %q", capturedAuth)
	}
	if capturedBody["content"] != "Hello Discord!" {
		t.Errorf("expected content 'Hello Discord!', got %q", capturedBody["content"])
	}
}

func TestDiscordSendMediaCallsAPI(t *testing.T) {
	var capturedBody map[string]string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		json.Unmarshal(data, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"id":"msg-media-001"}`))
	}))
	defer ts.Close()

	a := NewDiscordChannelAdapter("discord")
	a.dc = infra.NewDynamicHTTPClient(nil, 5*time.Second)
	origBase := discordAPIBase
	defer func() { discordAPIBase = origBase }()
	discordAPIBase = ts.URL

	result, err := a.SendMedia(context.Background(), adapter.OutboundMessage{
		To: "12345",
		Payload: adapter.ReplyPayload{
			Text:     "Look",
			MediaURL: "https://example.com/image.png",
		},
	}, adapter.ChannelConfig{
		Config: map[string]any{"token": "bot-token-abc"},
	})
	if err != nil {
		t.Fatalf("SendMedia failed: %v", err)
	}
	if result.MessageID != "msg-media-001" {
		t.Fatalf("expected media message ID, got %+v", result)
	}
	if capturedBody["content"] != "Look\nhttps://example.com/image.png" {
		t.Fatalf("unexpected content: %q", capturedBody["content"])
	}
}

func TestDiscordChunkText(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		maxLen   int
		expected int
	}{
		{"short", "hello", 2000, 1},
		{"exact", strings.Repeat("a", 2000), 2000, 1},
		{"two chunks", strings.Repeat("a", 2001), 2000, 2},
		{"multiple chunks", strings.Repeat("a", 6000), 2000, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := chunkText(tt.text, tt.maxLen)
			if len(chunks) != tt.expected {
				t.Errorf("expected %d chunks, got %d", tt.expected, len(chunks))
			}
			reconstructed := strings.Join(chunks, "")
			if reconstructed != tt.text {
				t.Error("chunks don't reconstruct to original text")
			}
		})
	}
}
