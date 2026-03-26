// Package slack implements the Slack channel adapter.
// It handles inbound webhooks from Slack's Events API and sends outbound
// messages via the Slack Web API. It self-registers with the channel driver
// registry on init.
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/kocort/kocort/internal/channel/adapter"
	"github.com/kocort/kocort/internal/infra"
)

const (
	SlackChannelID = "slack"
	maxTextLen     = 4000 // Slack mrkdwn text limit
)

// slackAPIBase is a variable so tests can override it.
var slackAPIBase = "https://slack.com/api"

func init() {
	adapter.Register(SlackChannelID, func() adapter.ChannelAdapter {
		return NewSlackChannelAdapter(SlackChannelID)
	})
}

// SlackChannelAdapter implements the Slack channel integration.
type SlackChannelAdapter struct {
	adapter.BaseAdapter
	dc *infra.DynamicHTTPClient

	mu        sync.Mutex
	channelID string
	cfg       adapter.ChannelConfig
	cb        adapter.Callbacks
	started   bool
}

// NewSlackChannelAdapter returns a new adapter for the given channel ID.
func NewSlackChannelAdapter(id string) *SlackChannelAdapter {
	return &SlackChannelAdapter{
		BaseAdapter: adapter.NewBaseAdapter(normalizeID(id)),
	}
}

// Schema returns the driver schema describing identity and configurable fields.
func (a *SlackChannelAdapter) Schema() adapter.ChannelDriverSchema {
	return adapter.ChannelDriverSchema{
		ID:          SlackChannelID,
		Name:        "Slack",
		Description: "Connect to Slack workspace via Bot Token",
		Fields: []adapter.ChannelConfigField{
			{
				Key:         "bot_token",
				Label:       "Bot Token",
				Type:        adapter.FieldTypePassword,
				Required:    true,
				Placeholder: "xoxb-your-bot-token",
				Help:        "Slack bot token from OAuth & Permissions",
				Group:       "account",
			},
			{
				Key:         "defaultTo",
				Label:       "Default Channel",
				Type:        adapter.FieldTypeText,
				Required:    true,
				Placeholder: "#general or C1234567890",
				Help:        "Default Slack channel name or ID",
			},
			{
				Key:         "inboundToken",
				Label:       "Webhook Token (Optional)",
				Type:        adapter.FieldTypePassword,
				Required:    false,
				Placeholder: "your-webhook-secret",
				Help:        "Optional token for authenticating inbound webhooks",
			},
		},
	}
}

// StartBackground begins the inbound message lifecycle.
func (a *SlackChannelAdapter) StartBackground(_ context.Context, channelID string, cfg adapter.ChannelConfig, dc *infra.DynamicHTTPClient, cb adapter.Callbacks) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.channelID = channelID
	a.cfg = cfg
	a.cb = cb
	a.started = true
	a.dc = dc
	return nil
}

// StopBackground stops all background goroutines.
func (a *SlackChannelAdapter) StopBackground() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.started = false
}

// ServeHTTP processes an incoming Slack event.
func (a *SlackChannelAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("read slack request body: %v", err), http.StatusBadRequest)
		return
	}

	// Check for URL verification challenge (Slack Events API setup).
	var envelope struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Type == "url_verification" {
		msg := adapter.InboundMessage{
			Channel: a.inboundChannelID(),
			Text:    envelope.Challenge,
			Raw:     map[string]any{"type": "url_verification", "challenge": envelope.Challenge},
		}
		if err := cb.OnMessage(r.Context(), msg); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(envelope.Challenge))
		return
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
		Event     *slackEvent      `json:"event,omitempty"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var msg adapter.InboundMessage
	if payload.Event != nil {
		evt := payload.Event
		msg = adapter.InboundMessage{
			Channel:   a.inboundChannelID(),
			AccountID: strings.TrimSpace(payload.AccountID),
			From:      strings.TrimSpace(evt.User),
			To:        strings.TrimSpace(evt.Channel),
			ThreadID:  strings.TrimSpace(evt.ThreadTS),
			ChatType:  resolveSlackChatType(evt.ChannelType),
			Text:      strings.TrimSpace(evt.Text),
			AgentID:   normalizeAgentID(nonEmpty(payload.AgentID, cfg.Agent)),
			MessageID: strings.TrimSpace(evt.ClientMsgID),
			Raw:       payload.Raw,
		}
	} else {
		msg = adapter.InboundMessage{
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
	}

	if err := cb.OnMessage(r.Context(), msg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// slackEvent represents the event object within a Slack Events API callback.
type slackEvent struct {
	Type        string `json:"type"`
	User        string `json:"user"`
	Text        string `json:"text"`
	Channel     string `json:"channel"`
	ChannelType string `json:"channel_type"`
	ThreadTS    string `json:"thread_ts"`
	ClientMsgID string `json:"client_msg_id"`
}

// SendText sends a text message to a Slack channel via the Web API.
func (a *SlackChannelAdapter) SendText(ctx context.Context, message adapter.OutboundMessage, ch adapter.ChannelConfig) (adapter.DeliveryResult, error) {
	token := resolveSlackToken(ch)
	if token == "" {
		return adapter.DeliveryResult{}, fmt.Errorf("slack bot token is required")
	}

	channelID := strings.TrimSpace(message.To)
	if channelID == "" {
		return adapter.DeliveryResult{}, adapter.ErrTargetRequired
	}

	text := strings.TrimSpace(message.Payload.Text)
	if text == "" {
		return adapter.DeliveryResult{}, nil
	}

	chunks := chunkText(text, maxTextLen)
	var lastTS string
	threadTS := strings.TrimSpace(message.ThreadID)
	for _, chunk := range chunks {
		ts, err := a.postMessage(ctx, token, channelID, chunk, threadTS)
		if err != nil {
			return adapter.DeliveryResult{}, err
		}
		lastTS = ts
		if threadTS == "" {
			threadTS = ts
		}
	}

	return adapter.DeliveryResult{
		Channel:   a.ID(),
		MessageID: lastTS,
		ChatID:    channelID,
	}, nil
}

// SendMedia sends a message with media URLs to a Slack channel.
func (a *SlackChannelAdapter) SendMedia(ctx context.Context, message adapter.OutboundMessage, ch adapter.ChannelConfig) (adapter.DeliveryResult, error) {
	token := resolveSlackToken(ch)
	if token == "" {
		return adapter.DeliveryResult{}, fmt.Errorf("slack bot token is required")
	}

	channelID := strings.TrimSpace(message.To)
	if channelID == "" {
		return adapter.DeliveryResult{}, adapter.ErrTargetRequired
	}

	text := strings.TrimSpace(message.Payload.Text)
	mediaURL := strings.TrimSpace(message.Payload.MediaURL)
	if mediaURL != "" {
		if text != "" {
			text += "\n"
		}
		text += mediaURL
	}
	for _, u := range message.Payload.MediaURLs {
		if u := strings.TrimSpace(u); u != "" {
			text += "\n" + u
		}
	}

	if text == "" {
		return adapter.DeliveryResult{}, nil
	}

	chunks := chunkText(text, maxTextLen)
	var lastTS string
	threadTS := strings.TrimSpace(message.ThreadID)
	for _, chunk := range chunks {
		ts, err := a.postMessage(ctx, token, channelID, chunk, threadTS)
		if err != nil {
			return adapter.DeliveryResult{}, err
		}
		lastTS = ts
		if threadTS == "" {
			threadTS = ts
		}
	}

	return adapter.DeliveryResult{
		Channel:   a.ID(),
		MessageID: lastTS,
		ChatID:    channelID,
	}, nil
}

// ChunkText implements adapter.TextChunkProvider.
func (a *SlackChannelAdapter) ChunkText(text string, limit int) []string {
	return chunkText(text, limit)
}

// ChunkerMode implements adapter.TextChunkProvider.
func (a *SlackChannelAdapter) ChunkerMode() string { return "length" }

// postMessage posts a message to Slack using the chat.postMessage API.
func (a *SlackChannelAdapter) postMessage(ctx context.Context, token, channelID, text, threadTS string) (string, error) {
	apiURL := slackAPIBase + "/chat.postMessage"

	payload := map[string]string{
		"channel": channelID,
		"text":    text,
	}
	if threadTS != "" {
		payload["thread_ts"] = threadTS
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal slack message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create slack request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := a.dc.Do(req)
	if err != nil {
		return "", fmt.Errorf("send slack message: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var result struct {
		OK    bool   `json:"ok"`
		TS    string `json:"ts"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decode slack response: %w", err)
	}
	if !result.OK {
		return "", fmt.Errorf("slack API error: %s", result.Error)
	}
	return result.TS, nil
}

func (a *SlackChannelAdapter) inboundChannelID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if id := normalizeID(a.channelID); id != "" {
		return id
	}
	return a.ID()
}

// =========================================================================
// Local helpers
// =========================================================================

func resolveSlackToken(ch adapter.ChannelConfig) string {
	if ch.Config != nil {
		if t, ok := ch.Config["bot_token"].(string); ok && t != "" {
			return strings.TrimSpace(t)
		}
		if t, ok := ch.Config["token"].(string); ok && t != "" {
			return strings.TrimSpace(t)
		}
	}
	if ch.Accounts != nil {
		for _, acct := range ch.Accounts {
			if m, ok := acct.(map[string]any); ok {
				if t, ok := m["bot_token"].(string); ok && t != "" {
					return strings.TrimSpace(t)
				}
				if t, ok := m["token"].(string); ok && t != "" {
					return strings.TrimSpace(t)
				}
			}
		}
	}
	return ""
}

func resolveSlackChatType(channelType string) adapter.ChatType {
	switch channelType {
	case "im":
		return adapter.ChatTypeDirect
	case "channel", "group":
		return adapter.ChatTypeGroup
	default:
		return adapter.ChatTypeDirect
	}
}

func chunkText(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}
	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}
		cutAt := maxLen
		if idx := strings.LastIndex(text[:maxLen], "\n"); idx > maxLen/2 {
			cutAt = idx + 1
		}
		chunks = append(chunks, text[:cutAt])
		text = text[cutAt:]
	}
	return chunks
}

func normalizeID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
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
