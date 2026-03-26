// Package zalo implements the Zalo OA (Official Account) channel adapter.
// It processes Zalo webhook events and sends outbound messages via the Zalo OA API.
// It self-registers with the channel driver registry on init.
package zalo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/kocort/kocort/internal/channel/adapter"
	"github.com/kocort/kocort/internal/infra"
)

const (
	ZaloChannelID = "zalo"
	maxTextLen    = 2000 // Zalo text limit
)

var zaloAPIBase = "https://openapi.zalo.me/v3.0/oa"

func init() {
	adapter.Register(ZaloChannelID, func() adapter.ChannelAdapter {
		return NewZaloChannelAdapter(ZaloChannelID)
	})
}

// ZaloChannelAdapter implements the Zalo OA integration.
type ZaloChannelAdapter struct {
	adapter.BaseAdapter
	dc *infra.DynamicHTTPClient

	mu        sync.Mutex
	channelID string
	cfg       adapter.ChannelConfig
	cb        adapter.Callbacks
	started   bool
}

// NewZaloChannelAdapter returns a new Zalo adapter.
func NewZaloChannelAdapter(id string) *ZaloChannelAdapter {
	return &ZaloChannelAdapter{
		BaseAdapter: adapter.NewBaseAdapter(normalizeID(id)),
	}
}

// Schema returns the driver schema for Zalo.
func (a *ZaloChannelAdapter) Schema() adapter.ChannelDriverSchema {
	return adapter.ChannelDriverSchema{
		ID:          ZaloChannelID,
		Name:        "Zalo OA",
		Description: "Connect to Zalo Official Account API",
		Fields: []adapter.ChannelConfigField{
			{
				Key:         "access_token",
				Label:       "Access Token",
				Type:        adapter.FieldTypePassword,
				Required:    true,
				Placeholder: "your-zalo-access-token",
				Help:        "Zalo OA access token",
				Group:       "account",
			},
			{
				Key:         "defaultTo",
				Label:       "Default Recipient",
				Type:        adapter.FieldTypeText,
				Required:    false,
				Placeholder: "user-id",
				Help:        "Default Zalo user ID for outbound messages",
			},
			{
				Key:         "inboundToken",
				Label:       "Webhook Secret (Optional)",
				Type:        adapter.FieldTypePassword,
				Required:    false,
				Placeholder: "your-webhook-secret",
				Help:        "Secret for authenticating inbound webhooks",
			},
		},
	}
}

// StartBackground begins the inbound message lifecycle.
func (a *ZaloChannelAdapter) StartBackground(_ context.Context, channelID string, cfg adapter.ChannelConfig, dc *infra.DynamicHTTPClient, cb adapter.Callbacks) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.channelID = channelID
	a.cfg = cfg
	a.cb = cb
	a.started = true
	a.dc = dc
	return nil
}

// StopBackground stops the adapter.
func (a *ZaloChannelAdapter) StopBackground() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.started = false
}

// ServeHTTP handles inbound Zalo webhook events.
func (a *ZaloChannelAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, fmt.Sprintf("read zalo request body: %v", err), http.StatusBadRequest)
		return
	}

	// Try Zalo webhook event format.
	var event zaloWebhookEvent
	if err := json.Unmarshal(body, &event); err == nil && event.EventName != "" {
		msg := parseZaloEvent(a.inboundChannelID(), cfg, &event)
		if msg != nil {
			if err := cb.OnMessage(r.Context(), *msg); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	// Fall back to generic JSON envelope.
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
	if err := json.Unmarshal(body, &payload); err != nil {
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

// ---------- Zalo webhook types ----------

type zaloWebhookEvent struct {
	AppID     string      `json:"app_id"`
	EventName string      `json:"event_name"`
	Sender    zaloSender  `json:"sender"`
	Recipient zaloSender  `json:"recipient"`
	Message   zaloMessage `json:"message"`
	Timestamp string      `json:"timestamp"`
}

type zaloSender struct {
	ID string `json:"id"`
}

type zaloMessage struct {
	MsgID       string       `json:"msg_id"`
	Text        string       `json:"text"`
	Attachments []zaloAttach `json:"attachments,omitempty"`
}

type zaloAttach struct {
	Type    string         `json:"type"`
	Payload zaloAttPayload `json:"payload"`
}

type zaloAttPayload struct {
	URL       string `json:"url,omitempty"`
	Thumbnail string `json:"thumbnail,omitempty"`
}

func parseZaloEvent(channelID string, cfg adapter.ChannelConfig, event *zaloWebhookEvent) *adapter.InboundMessage {
	if event.EventName != "user_send_text" && event.EventName != "user_send_image" &&
		event.EventName != "user_send_file" && event.EventName != "user_send_sticker" {
		return nil
	}

	text := strings.TrimSpace(event.Message.Text)
	if text == "" && len(event.Message.Attachments) > 0 {
		att := event.Message.Attachments[0]
		text = nonEmpty(att.Payload.URL, "[attachment]")
	}

	return &adapter.InboundMessage{
		Channel:   nonEmpty(normalizeID(channelID), ZaloChannelID),
		From:      strings.TrimSpace(event.Sender.ID),
		To:        strings.TrimSpace(event.Recipient.ID),
		ChatType:  adapter.ChatTypeDirect, // Zalo OA is DM only.
		Text:      text,
		AgentID:   normalizeAgentID(cfg.Agent),
		MessageID: strings.TrimSpace(event.Message.MsgID),
	}
}

// ---------- Outbound ----------

// SendText sends a text message via the Zalo OA API.
func (a *ZaloChannelAdapter) SendText(ctx context.Context, message adapter.OutboundMessage, ch adapter.ChannelConfig) (adapter.DeliveryResult, error) {
	token := resolveZaloToken(ch)
	if token == "" {
		return adapter.DeliveryResult{}, adapter.ErrTokenRequired
	}

	to := strings.TrimSpace(message.To)
	if to == "" {
		return adapter.DeliveryResult{}, adapter.ErrTargetRequired
	}

	text := StripMarkdown(strings.TrimSpace(message.Payload.Text))
	if text == "" {
		return adapter.DeliveryResult{}, nil
	}

	chunks := chunkText(text, maxTextLen)
	var lastMsgID string
	for _, chunk := range chunks {
		msgID, err := a.sendMessage(ctx, token, to, chunk)
		if err != nil {
			return adapter.DeliveryResult{}, err
		}
		lastMsgID = msgID
	}

	return adapter.DeliveryResult{
		Channel:   a.ID(),
		MessageID: lastMsgID,
		ChatID:    to,
	}, nil
}

// SendMedia sends a media message via the Zalo OA API.
func (a *ZaloChannelAdapter) SendMedia(ctx context.Context, message adapter.OutboundMessage, ch adapter.ChannelConfig) (adapter.DeliveryResult, error) {
	token := resolveZaloToken(ch)
	if token == "" {
		return adapter.DeliveryResult{}, adapter.ErrTokenRequired
	}

	to := strings.TrimSpace(message.To)
	if to == "" {
		return adapter.DeliveryResult{}, adapter.ErrTargetRequired
	}

	mediaURL := strings.TrimSpace(message.Payload.MediaURL)
	caption := StripMarkdown(strings.TrimSpace(message.Payload.Text))

	if mediaURL != "" {
		msgID, err := a.sendImageMessage(ctx, token, to, mediaURL, caption)
		if err != nil {
			return adapter.DeliveryResult{}, err
		}
		return adapter.DeliveryResult{
			Channel:   a.ID(),
			MessageID: msgID,
			ChatID:    to,
		}, nil
	}

	// Fallback to text with media URLs inline.
	text := caption
	for _, u := range message.Payload.MediaURLs {
		if u := strings.TrimSpace(u); u != "" {
			if text != "" {
				text += "\n"
			}
			text += u
		}
	}
	if text == "" {
		return adapter.DeliveryResult{}, nil
	}

	chunks := chunkText(text, maxTextLen)
	var lastMsgID string
	for _, chunk := range chunks {
		id, err := a.sendMessage(ctx, token, to, chunk)
		if err != nil {
			return adapter.DeliveryResult{}, err
		}
		lastMsgID = id
	}

	return adapter.DeliveryResult{
		Channel:   a.ID(),
		MessageID: lastMsgID,
		ChatID:    to,
	}, nil
}

// ChunkText implements adapter.TextChunkProvider.
func (a *ZaloChannelAdapter) ChunkText(text string, limit int) []string {
	return chunkText(text, limit)
}

// ChunkerMode implements adapter.TextChunkProvider.
func (a *ZaloChannelAdapter) ChunkerMode() string { return "length" }

func (a *ZaloChannelAdapter) sendMessage(ctx context.Context, token, to, text string) (string, error) {
	apiURL := zaloAPIBase + "/message/cs"

	payload := map[string]any{
		"recipient": map[string]any{"user_id": to},
		"message":   map[string]any{"text": text},
	}

	return a.doZaloRequest(ctx, apiURL, token, payload)
}

func (a *ZaloChannelAdapter) inboundChannelID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if id := normalizeID(a.channelID); id != "" {
		return id
	}
	return a.ID()
}

func (a *ZaloChannelAdapter) sendImageMessage(ctx context.Context, token, to, imageURL, caption string) (string, error) {
	apiURL := zaloAPIBase + "/message/cs"

	// Zalo uses template-based image messages.
	elements := []map[string]any{
		{
			"media_type": "image",
			"url":        imageURL,
		},
	}
	if caption != "" {
		elements = append(elements, map[string]any{
			"media_type": "text",
			"text":       caption,
		})
	}

	payload := map[string]any{
		"recipient": map[string]any{"user_id": to},
		"message": map[string]any{
			"attachment": map[string]any{
				"type": "template",
				"payload": map[string]any{
					"template_type": "media",
					"elements":      elements,
				},
			},
		},
	}

	return a.doZaloRequest(ctx, apiURL, token, payload)
}

func (a *ZaloChannelAdapter) doZaloRequest(ctx context.Context, apiURL, token string, payload map[string]any) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal zalo message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create zalo request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("access_token", token)

	resp, err := a.dc.Do(req)
	if err != nil {
		return "", fmt.Errorf("send zalo message: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var result struct {
		Error   int    `json:"error"`
		Message string `json:"message"`
		Data    struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decode zalo response: %w", err)
	}
	if result.Error != 0 {
		return "", fmt.Errorf("zalo API error (%d): %s", result.Error, result.Message)
	}
	return result.Data.MessageID, nil
}

// =========================================================================
// Markdown stripping
// =========================================================================

var (
	reBold      = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reItalic    = regexp.MustCompile(`__(.+?)__`)
	reCode      = regexp.MustCompile("`([^`]+)`")
	reCodeBlock = regexp.MustCompile("(?s)```[a-zA-Z]*\n?(.*?)```")
)

// StripMarkdown removes common Markdown formatting.
func StripMarkdown(text string) string {
	text = reCodeBlock.ReplaceAllString(text, "$1")
	text = reBold.ReplaceAllString(text, "$1")
	text = reItalic.ReplaceAllString(text, "$1")
	text = reCode.ReplaceAllString(text, "$1")
	return text
}

// =========================================================================
// Local helpers
// =========================================================================

func resolveZaloToken(ch adapter.ChannelConfig) string {
	if ch.Config != nil {
		if t, ok := ch.Config["access_token"].(string); ok && t != "" {
			return strings.TrimSpace(t)
		}
	}
	if ch.Accounts != nil {
		for _, acct := range ch.Accounts {
			if m, ok := acct.(map[string]any); ok {
				if t, ok := m["access_token"].(string); ok && t != "" {
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
