package mock

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/channel/adapter"
)

func TestMockServeHTTP(t *testing.T) {
	a := NewMockChannelAdapter("mock")
	var received adapter.InboundMessage
	cb := adapter.Callbacks{
		OnMessage: func(ctx context.Context, msg adapter.InboundMessage) error {
			received = msg
			return nil
		},
	}
	_ = a.StartBackground(context.Background(), "mock-custom", adapter.ChannelConfig{}, nil, cb)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"from":"u1","to":"t1","text":"hello"}`))
	w := httptest.NewRecorder()
	a.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if received.Channel != "mock-custom" || received.From != "u1" || received.To != "t1" || received.Text != "hello" {
		t.Fatalf("unexpected message: %+v", received)
	}
}

func TestMockServeHTTPRejectsUnauthorized(t *testing.T) {
	a := NewMockChannelAdapter("mock")
	cb := adapter.Callbacks{
		OnMessage: func(ctx context.Context, msg adapter.InboundMessage) error { return nil },
	}
	_ = a.StartBackground(context.Background(), "mock", adapter.ChannelConfig{
		InboundToken: "correct",
	}, nil, cb)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"text":"hello"}`))
	req.Header.Set("Authorization", "Bearer wrong")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestMockSendTextAndMediaCaptureMessages(t *testing.T) {
	a := NewMockChannelAdapter("mock")
	if _, err := a.SendText(context.Background(), adapter.OutboundMessage{
		Payload: adapter.ReplyPayload{Text: "text"},
	}, adapter.ChannelConfig{}); err != nil {
		t.Fatalf("SendText failed: %v", err)
	}
	if _, err := a.SendMedia(context.Background(), adapter.OutboundMessage{
		Payload: adapter.ReplyPayload{Text: "media"},
	}, adapter.ChannelConfig{}); err != nil {
		t.Fatalf("SendMedia failed: %v", err)
	}
	sent := a.Sent()
	if len(sent) != 2 {
		t.Fatalf("expected 2 sent messages, got %d", len(sent))
	}
}
