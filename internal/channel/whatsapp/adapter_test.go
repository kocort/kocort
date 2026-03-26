package whatsapp

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

func TestWhatsAppAdapter_ID(t *testing.T) {
	a := NewWhatsAppChannelAdapter(WhatsAppChannelID)
	if a.ID() != "whatsapp" {
		t.Errorf("expected 'whatsapp', got %q", a.ID())
	}
}

func TestWhatsAppAdapter_Schema(t *testing.T) {
	a := NewWhatsAppChannelAdapter(WhatsAppChannelID)
	s := a.Schema()
	if s.ID != "whatsapp" {
		t.Errorf("expected schema ID 'whatsapp', got %q", s.ID)
	}
	if len(s.Fields) < 2 {
		t.Error("expected at least 2 fields")
	}
}

func TestWhatsAppAdapter_WebhookVerification(t *testing.T) {
	a := NewWhatsAppChannelAdapter(WhatsAppChannelID)
	_ = a.StartBackground(context.Background(), "ch1", adapter.ChannelConfig{
		InboundToken: "my-verify-token",
	}, nil, adapter.Callbacks{
		OnMessage: func(ctx context.Context, msg adapter.InboundMessage) error {
			return nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/whatsapp?hub.mode=subscribe&hub.verify_token=my-verify-token&hub.challenge=challenge123", nil)
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "challenge123" {
		t.Errorf("expected challenge echo, got %q", rec.Body.String())
	}
}

func TestWhatsAppAdapter_WebhookVerification_Fail(t *testing.T) {
	a := NewWhatsAppChannelAdapter(WhatsAppChannelID)
	_ = a.StartBackground(context.Background(), "ch1", adapter.ChannelConfig{
		InboundToken: "my-verify-token",
	}, nil, adapter.Callbacks{
		OnMessage: func(ctx context.Context, msg adapter.InboundMessage) error {
			return nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/whatsapp?hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=challenge123", nil)
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestWhatsAppAdapter_ServeHTTP_Unauthorized(t *testing.T) {
	a := NewWhatsAppChannelAdapter(WhatsAppChannelID)
	_ = a.StartBackground(context.Background(), "ch1", adapter.ChannelConfig{
		InboundToken: "secret",
	}, nil, adapter.Callbacks{
		OnMessage: func(ctx context.Context, msg adapter.InboundMessage) error {
			return nil
		},
	})

	body := `{"text":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/whatsapp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestWhatsAppAdapter_ServeHTTP_WebhookPayload(t *testing.T) {
	a := NewWhatsAppChannelAdapter(WhatsAppChannelID)
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
		"object": "whatsapp_business_account",
		"entry": [{
			"id": "entry1",
			"changes": [{
				"value": {
					"messaging_product": "whatsapp",
					"metadata": {
						"display_phone_number": "+1234567890",
						"phone_number_id": "phone123"
					},
					"messages": [{
						"id": "wamid.abc123",
						"from": "5511999999999",
						"timestamp": "1234567890",
						"type": "text",
						"text": {"body": "Hello from WhatsApp"}
					}]
				},
				"field": "messages"
			}]
		}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/whatsapp", strings.NewReader(body))
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if captured.From != "5511999999999" {
		t.Errorf("from: expected '5511999999999', got %q", captured.From)
	}
	if captured.To != "phone123" {
		t.Errorf("to: expected 'phone123', got %q", captured.To)
	}
	if captured.ChatType != adapter.ChatTypeDirect {
		t.Errorf("chatType: expected direct, got %q", captured.ChatType)
	}
	if captured.Text != "Hello from WhatsApp" {
		t.Errorf("text: expected 'Hello from WhatsApp', got %q", captured.Text)
	}
	if captured.MessageID != "wamid.abc123" {
		t.Errorf("messageID: expected 'wamid.abc123', got %q", captured.MessageID)
	}
	if captured.Channel != "ch1" {
		t.Errorf("channel: expected 'ch1', got %q", captured.Channel)
	}
}

func TestWhatsAppAdapter_SendText(t *testing.T) {
	var receivedPayload map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedPayload)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"messages": []map[string]any{{"id": "wamid.out123"}},
		})
	}))
	defer ts.Close()

	a := NewWhatsAppChannelAdapter(WhatsAppChannelID)
	a.dc = infra.NewDynamicHTTPClient(nil, 5*time.Second)
	origBase := whatsappAPIBase
	defer func() { whatsappAPIBase = origBase }()
	whatsappAPIBase = ts.URL

	result, err := a.SendText(context.Background(), adapter.OutboundMessage{
		To: "+1234567890",
		Payload: adapter.ReplyPayload{
			Text: "hello whatsapp",
		},
	}, adapter.ChannelConfig{
		Config: map[string]any{
			"access_token":    "test-token",
			"phone_number_id": "phone123",
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.MessageID != "wamid.out123" {
		t.Errorf("expected MessageID 'wamid.out123', got %q", result.MessageID)
	}
	if receivedPayload["to"] != "+1234567890" {
		t.Errorf("expected to '+1234567890', got %v", receivedPayload["to"])
	}
}

func TestWhatsAppAdapter_SendMedia(t *testing.T) {
	var receivedPayload map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedPayload)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"messages": []map[string]any{{"id": "wamid.img123"}},
		})
	}))
	defer ts.Close()

	a := NewWhatsAppChannelAdapter(WhatsAppChannelID)
	a.dc = infra.NewDynamicHTTPClient(nil, 5*time.Second)
	origBase := whatsappAPIBase
	defer func() { whatsappAPIBase = origBase }()
	whatsappAPIBase = ts.URL

	result, err := a.SendMedia(context.Background(), adapter.OutboundMessage{
		To: "+1234567890",
		Payload: adapter.ReplyPayload{
			Text:     "check this image",
			MediaURL: "https://example.com/img.jpg",
		},
	}, adapter.ChannelConfig{
		Config: map[string]any{
			"access_token":    "test-token",
			"phone_number_id": "phone123",
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.MessageID != "wamid.img123" {
		t.Errorf("expected MessageID 'wamid.img123', got %q", result.MessageID)
	}
	if receivedPayload["type"] != "image" {
		t.Errorf("expected type 'image', got %v", receivedPayload["type"])
	}
}

func TestWhatsAppAdapter_SendText_NoToken(t *testing.T) {
	a := NewWhatsAppChannelAdapter(WhatsAppChannelID)
	_, err := a.SendText(context.Background(), adapter.OutboundMessage{
		To: "+1234567890", Payload: adapter.ReplyPayload{Text: "hello"},
	}, adapter.ChannelConfig{})
	if err != adapter.ErrTokenRequired {
		t.Errorf("expected ErrTokenRequired, got %v", err)
	}
}

func TestWhatsAppAdapter_SendText_NoTarget(t *testing.T) {
	a := NewWhatsAppChannelAdapter(WhatsAppChannelID)
	_, err := a.SendText(context.Background(), adapter.OutboundMessage{
		Payload: adapter.ReplyPayload{Text: "hello"},
	}, adapter.ChannelConfig{
		Config: map[string]any{
			"access_token":    "test-token",
			"phone_number_id": "phone123",
		},
	})
	if err != adapter.ErrTargetRequired {
		t.Errorf("expected ErrTargetRequired, got %v", err)
	}
}

func TestResolveWhatsAppCredentials_GlobalConfig(t *testing.T) {
	creds := resolveWhatsAppCredentials(adapter.ChannelConfig{
		Config: map[string]any{
			"access_token":    "global-token",
			"phone_number_id": "global-phone",
		},
	}, "")
	if creds.accessToken != "global-token" {
		t.Errorf("expected 'global-token', got %q", creds.accessToken)
	}
	if creds.phoneNumberID != "global-phone" {
		t.Errorf("expected 'global-phone', got %q", creds.phoneNumberID)
	}
}

func TestResolveWhatsAppCredentials_AccountSpecific(t *testing.T) {
	creds := resolveWhatsAppCredentials(adapter.ChannelConfig{
		Config: map[string]any{
			"access_token":    "global-token",
			"phone_number_id": "global-phone",
		},
		Accounts: map[string]any{
			"acct-phone": map[string]any{
				"access_token":    "acct-token",
				"phone_number_id": "acct-phone",
			},
		},
	}, "acct-phone")
	if creds.accessToken != "acct-token" {
		t.Errorf("expected 'acct-token', got %q", creds.accessToken)
	}
	if creds.phoneNumberID != "acct-phone" {
		t.Errorf("expected 'acct-phone', got %q", creds.phoneNumberID)
	}
}
