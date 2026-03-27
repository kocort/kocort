package qq

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kocort/kocort/internal/channel/adapter"
)

func TestQQAdapter_ID(t *testing.T) {
	a := NewQQChannelAdapter(QQChannelID)
	if a.ID() != "qq" {
		t.Errorf("expected 'qq', got %q", a.ID())
	}
}

func TestQQAdapter_Schema(t *testing.T) {
	a := NewQQChannelAdapter(QQChannelID)
	s := a.Schema()
	if s.ID != "qq" {
		t.Errorf("expected schema ID 'qq', got %q", s.ID)
	}
	if len(s.Fields) < 3 {
		t.Error("expected at least 3 fields")
	}
}

func TestQQAdapter_ServeHTTP_NotStarted(t *testing.T) {
	a := NewQQChannelAdapter(QQChannelID)
	req := httptest.NewRequest(http.MethodPost, "/qq", strings.NewReader(`{"text":"hello"}`))
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

func TestQQAdapter_ServeHTTP_Unauthorized(t *testing.T) {
	a := NewQQChannelAdapter(QQChannelID)
	_ = a.StartBackground(context.Background(), "ch1", adapter.ChannelConfig{
		InboundToken: "secret",
	}, nil, adapter.Callbacks{
		OnMessage: func(ctx context.Context, msg adapter.InboundMessage) error {
			return nil
		},
	})
	defer a.StopBackground()

	body := `{"text":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/qq", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestQQAdapter_ServeHTTP_WithAuth(t *testing.T) {
	a := NewQQChannelAdapter(QQChannelID)
	var captured adapter.InboundMessage
	_ = a.StartBackground(context.Background(), "ch1", adapter.ChannelConfig{
		InboundToken: "secret",
		Agent:        "bot1",
	}, nil, adapter.Callbacks{
		OnMessage: func(ctx context.Context, msg adapter.InboundMessage) error {
			captured = msg
			return nil
		},
	})
	defer a.StopBackground()

	body := `{"text":"hello qq","from":"user1","to":"group:g123"}`
	req := httptest.NewRequest(http.MethodPost, "/qq", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if captured.Text != "hello qq" {
		t.Errorf("text: expected 'hello qq', got %q", captured.Text)
	}
	if captured.AgentID != "bot1" {
		t.Errorf("agentID: expected 'bot1', got %q", captured.AgentID)
	}
	if captured.Channel != "ch1" {
		t.Errorf("channel: expected 'ch1', got %q", captured.Channel)
	}
}

func TestQQAdapter_SendText_NoToken(t *testing.T) {
	a := NewQQChannelAdapter(QQChannelID)
	_, err := a.SendText(context.Background(), adapter.OutboundMessage{
		To: "user:123", Payload: adapter.ReplyPayload{Text: "hello"},
	}, adapter.ChannelConfig{})
	if err != adapter.ErrTokenRequired {
		t.Errorf("expected ErrTokenRequired, got %v", err)
	}
}

func TestQQAdapter_SendText_NoTarget(t *testing.T) {
	a := NewQQChannelAdapter(QQChannelID)
	_, err := a.SendText(context.Background(), adapter.OutboundMessage{
		Payload: adapter.ReplyPayload{Text: "hello"},
	}, adapter.ChannelConfig{
		Config: map[string]any{"app_id": "123", "app_secret": "s", "token": "t"},
	})
	if err != adapter.ErrTargetRequired {
		t.Errorf("expected ErrTargetRequired, got %v", err)
	}
}

func TestParseTarget(t *testing.T) {
	tests := []struct {
		in       string
		wantType string
		wantID   string
	}{
		{"user:123", "user", "123"},
		{"group:456", "group", "456"},
		{"channel:789", "channel", "789"},
		{"just-id", "channel", "just-id"},
	}
	for _, tt := range tests {
		gotType, gotID := parseTarget(tt.in)
		if gotType != tt.wantType || gotID != tt.wantID {
			t.Errorf("parseTarget(%q) = (%q, %q), want (%q, %q)",
				tt.in, gotType, gotID, tt.wantType, tt.wantID)
		}
	}
}

func TestStripAtMentions(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"<@!123> hello", "hello"},
		{"<@123> hi <@456>", "hi"},
		{"no mentions", "no mentions"},
		{"", ""},
	}
	for _, tt := range tests {
		got := stripAtMentions(tt.in)
		if got != tt.want {
			t.Errorf("stripAtMentions(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestQQAccountsFromChannel_Config(t *testing.T) {
	accounts := qqAccountsFromChannel(adapter.ChannelConfig{
		Config: map[string]any{
			"app_id":     "app1",
			"app_secret": "secret1",
			"token":      "tok1",
			"sandbox":    true,
		},
	})
	if len(accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(accounts))
	}
	if accounts[0].appID != "app1" {
		t.Errorf("expected appID 'app1', got %q", accounts[0].appID)
	}
	if !accounts[0].sandbox {
		t.Error("expected sandbox=true")
	}
}

func TestQQAccountsFromChannel_Accounts(t *testing.T) {
	accounts := qqAccountsFromChannel(adapter.ChannelConfig{
		Accounts: map[string]any{
			"app1": map[string]any{
				"app_id":     "app1",
				"app_secret": "secret1",
			},
			"app2": map[string]any{
				"app_id":     "app2",
				"app_secret": "secret2",
			},
		},
	})
	if len(accounts) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(accounts))
	}
	got := map[string]bool{}
	for _, account := range accounts {
		got[account.appID] = true
	}
	if !got["app1"] || !got["app2"] {
		t.Fatalf("expected app1 and app2, got %+v", accounts)
	}
}

func TestStringFromMap(t *testing.T) {
	m := map[string]any{
		"str":  "hello",
		"num":  42.5,
		"int":  123,
		"miss": nil,
	}
	if got := stringFromMap(m, "str"); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
	if got := stringFromMap(m, "num"); got != "42.5" {
		t.Errorf("expected '42.5', got %q", got)
	}
	if got := stringFromMap(nil, "key"); got != "" {
		t.Errorf("expected '', got %q", got)
	}
}

func TestBoolFromMap(t *testing.T) {
	m := map[string]any{
		"yes":  true,
		"no":   false,
		"stry": "true",
		"strn": "false",
	}
	if !boolFromMap(m, "yes") {
		t.Error("expected true for 'yes'")
	}
	if boolFromMap(m, "no") {
		t.Error("expected false for 'no'")
	}
	if !boolFromMap(m, "stry") {
		t.Error("expected true for 'stry'")
	}
	if boolFromMap(nil, "key") {
		t.Error("expected false for nil map")
	}
}
