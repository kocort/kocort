package generic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/channel/adapter"
)

func TestGenericServeHTTP(t *testing.T) {
	a := NewGenericJSONChannelAdapter("generic")
	var received adapter.InboundMessage
	cb := adapter.Callbacks{
		OnMessage: func(ctx context.Context, msg adapter.InboundMessage) error {
			received = msg
			return nil
		},
	}
	_ = a.StartBackground(context.Background(), "generic-custom", adapter.ChannelConfig{}, nil, cb)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"from":"u1","to":"t1","text":"hello"}`))
	w := httptest.NewRecorder()
	a.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if received.Channel != "generic-custom" || received.From != "u1" || received.To != "t1" || received.Text != "hello" {
		t.Fatalf("unexpected message: %+v", received)
	}
}

func TestGenericServeHTTPRejectsUnauthorized(t *testing.T) {
	a := NewGenericJSONChannelAdapter("generic")
	cb := adapter.Callbacks{
		OnMessage: func(ctx context.Context, msg adapter.InboundMessage) error { return nil },
	}
	_ = a.StartBackground(context.Background(), "generic", adapter.ChannelConfig{
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

func TestGenericSendTextReturnsNotImplemented(t *testing.T) {
	a := NewGenericJSONChannelAdapter("generic")
	_, err := a.SendText(context.Background(), adapter.OutboundMessage{}, adapter.ChannelConfig{})
	if err == nil {
		t.Fatal("expected outbound not implemented error")
	}
}
