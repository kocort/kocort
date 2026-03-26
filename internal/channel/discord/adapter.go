// Package discord implements the Discord channel adapter.
// It handles inbound webhooks from Discord and sends outbound messages
// via the Discord REST API. It self-registers with the channel driver
// registry on init.
package discord

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
	DiscordChannelID = "discord"
	maxMessageLen    = 2000
)

var discordAPIBase = "https://discord.com/api/v10"

func init() {
	adapter.Register(DiscordChannelID, func() adapter.ChannelAdapter {
		return NewDiscordChannelAdapter(DiscordChannelID)
	})
}

// DiscordChannelAdapter implements the Discord channel integration.
type DiscordChannelAdapter struct {
	adapter.BaseAdapter
	dc *infra.DynamicHTTPClient

	mu        sync.Mutex
	channelID string
	cfg       adapter.ChannelConfig
	cb        adapter.Callbacks
	started   bool
}

// NewDiscordChannelAdapter returns a new adapter for the given channel ID.
func NewDiscordChannelAdapter(id string) *DiscordChannelAdapter {
	return &DiscordChannelAdapter{
		BaseAdapter: adapter.NewBaseAdapter(normalizeID(id)),
	}
}

// Schema returns the driver schema describing identity and configurable fields.
func (a *DiscordChannelAdapter) Schema() adapter.ChannelDriverSchema {
	return adapter.ChannelDriverSchema{
		ID:          DiscordChannelID,
		Name:        "Discord",
		Description: "Connect to Discord via Bot API",
		Fields: []adapter.ChannelConfigField{
			{
				Key:         "token",
				Label:       "Bot Token",
				Type:        adapter.FieldTypePassword,
				Required:    true,
				Placeholder: "Bot your-bot-token-here",
				Help:        "Discord bot token from the Developer Portal",
				Group:       "account",
			},
			{
				Key:         "defaultTo",
				Label:       "Default Channel ID",
				Type:        adapter.FieldTypeText,
				Required:    true,
				Placeholder: "1234567890",
				Help:        "Default Discord channel ID to send messages to",
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
func (a *DiscordChannelAdapter) StartBackground(_ context.Context, channelID string, cfg adapter.ChannelConfig, dc *infra.DynamicHTTPClient, cb adapter.Callbacks) error {
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
func (a *DiscordChannelAdapter) StopBackground() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.started = false
}

// ServeHTTP processes an incoming Discord webhook/interaction.
func (a *DiscordChannelAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

// SendText sends a text message to a Discord channel via the REST API.
func (a *DiscordChannelAdapter) SendText(ctx context.Context, message adapter.OutboundMessage, ch adapter.ChannelConfig) (adapter.DeliveryResult, error) {
	token := resolveDiscordToken(ch)
	if token == "" {
		return adapter.DeliveryResult{}, fmt.Errorf("discord bot token is required")
	}

	channelID := strings.TrimSpace(message.To)
	if channelID == "" {
		return adapter.DeliveryResult{}, adapter.ErrTargetRequired
	}

	text := strings.TrimSpace(message.Payload.Text)
	if text == "" {
		return adapter.DeliveryResult{}, nil
	}

	chunks := chunkText(text, maxMessageLen)
	var lastMsgID string
	for _, chunk := range chunks {
		msgID, err := a.sendMessage(ctx, token, channelID, chunk)
		if err != nil {
			return adapter.DeliveryResult{}, err
		}
		lastMsgID = msgID
	}

	return adapter.DeliveryResult{
		Channel:   a.ID(),
		MessageID: lastMsgID,
		ChatID:    channelID,
	}, nil
}

// SendMedia sends a message with media content to a Discord channel.
func (a *DiscordChannelAdapter) SendMedia(ctx context.Context, message adapter.OutboundMessage, ch adapter.ChannelConfig) (adapter.DeliveryResult, error) {
	token := resolveDiscordToken(ch)
	if token == "" {
		return adapter.DeliveryResult{}, fmt.Errorf("discord bot token is required")
	}

	channelID := strings.TrimSpace(message.To)
	if channelID == "" {
		return adapter.DeliveryResult{}, adapter.ErrTargetRequired
	}

	text := strings.TrimSpace(message.Payload.Text)
	mediaURL := strings.TrimSpace(message.Payload.MediaURL)

	content := text
	if mediaURL != "" {
		if content != "" {
			content += "\n"
		}
		content += mediaURL
	}
	for _, u := range message.Payload.MediaURLs {
		if u := strings.TrimSpace(u); u != "" {
			content += "\n" + u
		}
	}

	if content == "" {
		return adapter.DeliveryResult{}, nil
	}

	chunks := chunkText(content, maxMessageLen)
	var lastMsgID string
	for _, chunk := range chunks {
		msgID, err := a.sendMessage(ctx, token, channelID, chunk)
		if err != nil {
			return adapter.DeliveryResult{}, err
		}
		lastMsgID = msgID
	}

	return adapter.DeliveryResult{
		Channel:   a.ID(),
		MessageID: lastMsgID,
		ChatID:    channelID,
	}, nil
}

// ChunkText implements adapter.TextChunkProvider.
func (a *DiscordChannelAdapter) ChunkText(text string, limit int) []string {
	return chunkText(text, limit)
}

// ChunkerMode implements adapter.TextChunkProvider.
func (a *DiscordChannelAdapter) ChunkerMode() string { return "length" }

// sendMessage posts a message to a Discord channel via the REST API.
func (a *DiscordChannelAdapter) sendMessage(ctx context.Context, token, channelID, content string) (string, error) {
	apiURL := fmt.Sprintf("%s/channels/%s/messages", discordAPIBase, channelID)

	body, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		return "", fmt.Errorf("marshal discord message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create discord request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot "+token)

	resp, err := a.dc.Do(req)
	if err != nil {
		return "", fmt.Errorf("send discord message: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("discord API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decode discord response: %w", err)
	}
	return result.ID, nil
}

func (a *DiscordChannelAdapter) inboundChannelID() string {
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

func resolveDiscordToken(ch adapter.ChannelConfig) string {
	if ch.Config != nil {
		if t, ok := ch.Config["token"].(string); ok && t != "" {
			return strings.TrimSpace(t)
		}
	}
	if ch.Accounts != nil {
		for _, acct := range ch.Accounts {
			if m, ok := acct.(map[string]any); ok {
				if t, ok := m["token"].(string); ok && t != "" {
					return strings.TrimSpace(t)
				}
			}
		}
	}
	return ""
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
