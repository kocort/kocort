// Package mock provides a test-friendly channel adapter that captures sent
// messages in memory. It self-registers with the channel driver registry on init.
package mock

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/kocort/kocort/internal/channel/adapter"
	"github.com/kocort/kocort/internal/infra"
)

func init() {
	adapter.Register("mock", func() adapter.ChannelAdapter {
		return NewMockChannelAdapter("mock")
	})
}

// MockChannelSentMessage records a single outbound message sent via the mock.
type MockChannelSentMessage struct {
	Message adapter.OutboundMessage
}

// MockChannelAdapter is an in-memory channel adapter for testing.
type MockChannelAdapter struct {
	adapter.BaseAdapter

	mu        sync.Mutex
	channelID string
	cfg       adapter.ChannelConfig
	cb        adapter.Callbacks
	started   bool
	sent      []MockChannelSentMessage
}

// NewMockChannelAdapter returns a new MockChannelAdapter for the given ID.
func NewMockChannelAdapter(id string) *MockChannelAdapter {
	return &MockChannelAdapter{
		BaseAdapter: adapter.NewBaseAdapter(normalizeID(id)),
	}
}

// Schema returns the driver schema describing identity and configurable fields.
func (a *MockChannelAdapter) Schema() adapter.ChannelDriverSchema {
	return adapter.ChannelDriverSchema{
		ID:          "mock",
		Name:        "Mock (Testing)",
		Description: "In-memory channel for testing purposes",
		Fields: []adapter.ChannelConfigField{
			{
				Key:         "defaultTo",
				Label:       "Default Target",
				Type:        adapter.FieldTypeText,
				Required:    true,
				Placeholder: "@test-user",
				Help:        "Mock target for outbound messages",
			},
			{
				Key:         "inboundToken",
				Label:       "Test Token (Optional)",
				Type:        adapter.FieldTypePassword,
				Required:    false,
				Placeholder: "test-secret",
				Help:        "Optional token for testing inbound authentication",
			},
		},
	}
}

// StartBackground begins the inbound message lifecycle.
func (a *MockChannelAdapter) StartBackground(_ context.Context, channelID string, cfg adapter.ChannelConfig, _ *infra.DynamicHTTPClient, cb adapter.Callbacks) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.channelID = channelID
	a.cfg = cfg
	a.cb = cb
	a.started = true
	return nil
}

// StopBackground stops all background goroutines.
func (a *MockChannelAdapter) StopBackground() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.started = false
}

// ServeHTTP processes inbound HTTP JSON messages.
func (a *MockChannelAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	cb := a.cb
	cfg := a.cfg
	started := a.started
	a.mu.Unlock()

	if !started || cb.OnMessage == nil {
		http.Error(w, adapter.ErrNotStarted.Error(), http.StatusServiceUnavailable)
		return
	}

	if strings.TrimSpace(cfg.InboundToken) != "" {
		token := strings.TrimSpace(r.Header.Get("Authorization"))
		token = strings.TrimPrefix(token, "Bearer ")
		if token != strings.TrimSpace(cfg.InboundToken) {
			http.Error(w, adapter.ErrUnauthorized.Error(), http.StatusUnauthorized)
			return
		}
	}

	var payload struct {
		AgentID   string           `json:"agentId,omitempty"`
		AccountID string           `json:"accountId,omitempty"`
		From      string           `json:"from,omitempty"`
		To        string           `json:"to,omitempty"`
		ThreadID  string           `json:"threadId,omitempty"`
		ChatType  adapter.ChatType `json:"chatType,omitempty"`
		Text      string           `json:"text"`
		MessageID string           `json:"messageId,omitempty"`
		Raw       map[string]any   `json:"raw,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	msg := adapter.InboundMessage{
		Channel:   a.inboundChannelID(),
		AccountID: strings.TrimSpace(payload.AccountID),
		From:      strings.TrimSpace(payload.From),
		To:        strings.TrimSpace(payload.To),
		ThreadID:  strings.TrimSpace(payload.ThreadID),
		ChatType:  payload.ChatType,
		Text:      strings.TrimSpace(payload.Text),
		AgentID:   normalizeAgentID(nonEmpty(payload.AgentID, cfg.Agent)),
		MessageID: strings.TrimSpace(payload.MessageID),
		Raw:       payload.Raw,
	}

	if err := cb.OnMessage(r.Context(), msg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// SendText captures a text message.
func (a *MockChannelAdapter) SendText(_ context.Context, message adapter.OutboundMessage, _ adapter.ChannelConfig) (adapter.DeliveryResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sent = append(a.sent, MockChannelSentMessage{Message: message})
	return adapter.DeliveryResult{MessageID: "mock-text"}, nil
}

// SendMedia captures a media message.
func (a *MockChannelAdapter) SendMedia(_ context.Context, message adapter.OutboundMessage, _ adapter.ChannelConfig) (adapter.DeliveryResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sent = append(a.sent, MockChannelSentMessage{Message: message})
	return adapter.DeliveryResult{MessageID: "mock-media"}, nil
}

// Sent returns a snapshot of all messages sent through this adapter.
func (a *MockChannelAdapter) Sent() []MockChannelSentMessage {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]MockChannelSentMessage, len(a.sent))
	copy(out, a.sent)
	return out
}

// =========================================================================
// Local helpers
// =========================================================================

func normalizeID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

func (a *MockChannelAdapter) inboundChannelID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if id := normalizeID(a.channelID); id != "" {
		return id
	}
	return a.ID()
}

func normalizeAgentID(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "main"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			if b.Len() == 0 || b.String()[b.Len()-1] == '-' {
				continue
			}
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "main"
	}
	if len(out) > 64 {
		return out[:64]
	}
	return out
}

func nonEmpty(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	return fallback
}
