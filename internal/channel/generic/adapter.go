// Package generic implements a simple JSON-over-HTTP channel adapter used as
// the default fallback for any channel without a specific driver.
// It self-registers with the channel driver registry on init.
package generic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/kocort/kocort/internal/channel/adapter"
	"github.com/kocort/kocort/internal/infra"
)

func init() {
	adapter.Register("generic", func() adapter.ChannelAdapter {
		return NewGenericJSONChannelAdapter("generic")
	})
}

// GenericJSONChannelAdapter handles inbound HTTP JSON messages and provides a
// stub outbound implementation for unrecognised channel types.
type GenericJSONChannelAdapter struct {
	adapter.BaseAdapter

	mu        sync.Mutex
	channelID string
	cfg       adapter.ChannelConfig
	cb        adapter.Callbacks
	started   bool
}

// NewGenericJSONChannelAdapter returns a new adapter for the given channel ID.
func NewGenericJSONChannelAdapter(id string) *GenericJSONChannelAdapter {
	return &GenericJSONChannelAdapter{
		BaseAdapter: adapter.NewBaseAdapter(normalizeID(id)),
	}
}

// Schema returns the driver schema describing identity and configurable fields.
func (a *GenericJSONChannelAdapter) Schema() adapter.ChannelDriverSchema {
	return adapter.ChannelDriverSchema{
		ID:          "generic",
		Name:        "Generic Webhook",
		Description: "Generic HTTP/JSON webhook integration",
		Fields: []adapter.ChannelConfigField{
			{
				Key:         "defaultTo",
				Label:       "Webhook URL",
				Type:        adapter.FieldTypeText,
				Required:    true,
				Placeholder: "https://your-webhook-endpoint.com/message",
				Help:        "Target webhook URL for outbound messages",
			},
			{
				Key:         "inboundToken",
				Label:       "Inbound Token (Optional)",
				Type:        adapter.FieldTypePassword,
				Required:    false,
				Placeholder: "your-webhook-secret",
				Help:        "Optional Bearer token for authenticating inbound requests",
			},
		},
	}
}

// StartBackground begins the inbound message lifecycle.
func (a *GenericJSONChannelAdapter) StartBackground(_ context.Context, channelID string, cfg adapter.ChannelConfig, _ *infra.DynamicHTTPClient, cb adapter.Callbacks) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.channelID = channelID
	a.cfg = cfg
	a.cb = cb
	a.started = true
	return nil
}

// StopBackground stops all background goroutines.
func (a *GenericJSONChannelAdapter) StopBackground() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.started = false
}

// ServeHTTP processes an incoming generic JSON HTTP request.
func (a *GenericJSONChannelAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

// SendText is not implemented for the generic adapter.
func (a *GenericJSONChannelAdapter) SendText(_ context.Context, _ adapter.OutboundMessage, _ adapter.ChannelConfig) (adapter.DeliveryResult, error) {
	return adapter.DeliveryResult{}, fmt.Errorf("channel %q outbound is not implemented", a.ID())
}

// SendMedia is not implemented for the generic adapter.
func (a *GenericJSONChannelAdapter) SendMedia(_ context.Context, _ adapter.OutboundMessage, _ adapter.ChannelConfig) (adapter.DeliveryResult, error) {
	return adapter.DeliveryResult{}, fmt.Errorf("channel %q outbound is not implemented", a.ID())
}

// =========================================================================
// Local helpers
// =========================================================================

func normalizeID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

func (a *GenericJSONChannelAdapter) inboundChannelID() string {
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
