// Package telegram implements the Telegram channel adapter.
// It processes Telegram Update webhooks and sends outbound messages via the
// Telegram Bot API. It self-registers with the channel driver registry on init.
package telegram

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
	TelegramChannelID = "telegram"
	maxTextLen        = 4096 // Telegram text limit
)

var telegramAPIBase = "https://api.telegram.org"

func init() {
	adapter.Register(TelegramChannelID, func() adapter.ChannelAdapter {
		return NewTelegramChannelAdapter(TelegramChannelID)
	})
}

// TelegramChannelAdapter implements the Telegram Bot integration.
type TelegramChannelAdapter struct {
	adapter.BaseAdapter
	dc *infra.DynamicHTTPClient

	mu        sync.Mutex
	channelID string
	cfg       adapter.ChannelConfig
	cb        adapter.Callbacks
	started   bool
}

// NewTelegramChannelAdapter returns a new Telegram adapter.
func NewTelegramChannelAdapter(id string) *TelegramChannelAdapter {
	return &TelegramChannelAdapter{
		BaseAdapter: adapter.NewBaseAdapter(normalizeID(id)),
	}
}

// Schema returns the driver schema for Telegram.
func (a *TelegramChannelAdapter) Schema() adapter.ChannelDriverSchema {
	return adapter.ChannelDriverSchema{
		ID:          TelegramChannelID,
		Name:        "Telegram",
		Description: "Connect to Telegram via Bot API",
		Fields: []adapter.ChannelConfigField{
			{
				Key:         "token",
				Label:       "Bot Token",
				Type:        adapter.FieldTypePassword,
				Required:    true,
				Placeholder: "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11",
				Help:        "Token from @BotFather",
				Group:       "account",
			},
			{
				Key:         "defaultTo",
				Label:       "Default Chat ID",
				Type:        adapter.FieldTypeText,
				Required:    false,
				Placeholder: "12345678",
				Help:        "Default Telegram chat ID for outbound messages",
			},
			{
				Key:         "inboundToken",
				Label:       "Webhook Secret (Optional)",
				Type:        adapter.FieldTypePassword,
				Required:    false,
				Placeholder: "your-webhook-secret",
				Help:        "Secret token for authenticating inbound webhooks",
			},
		},
	}
}

// StartBackground begins the inbound message lifecycle.
func (a *TelegramChannelAdapter) StartBackground(_ context.Context, channelID string, cfg adapter.ChannelConfig, dc *infra.DynamicHTTPClient, cb adapter.Callbacks) error {
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
func (a *TelegramChannelAdapter) StopBackground() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.started = false
}

// ServeHTTP processes incoming Telegram Update webhooks.
func (a *TelegramChannelAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		token := strings.TrimSpace(r.Header.Get("X-Telegram-Bot-Api-Secret-Token"))
		if token == "" {
			token = strings.TrimSpace(r.Header.Get("Authorization"))
			token = strings.TrimPrefix(token, "Bearer ")
		}
		if token != strings.TrimSpace(cfg.InboundToken) {
			http.Error(w, adapter.ErrUnauthorized.Error(), http.StatusUnauthorized)
			return
		}
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("read telegram request body: %v", err), http.StatusBadRequest)
		return
	}

	// Try to parse as a generic JSON payload first (forwarded format).
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

	// Try as a Telegram Update.
	var update telegramUpdate
	if err := json.Unmarshal(body, &update); err == nil && update.Message != nil && update.Message.Chat.ID != 0 {
		msg := buildInboundFromUpdate(a.inboundChannelID(), cfg, &update)
		if err := cb.OnMessage(r.Context(), msg); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	// Fall back to generic JSON envelope.
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

// ---------- Telegram types ----------

type telegramUpdate struct {
	UpdateID int              `json:"update_id"`
	Message  *telegramMessage `json:"message,omitempty"`
}

type telegramMessage struct {
	MessageID       int          `json:"message_id"`
	From            telegramUser `json:"from"`
	Chat            telegramChat `json:"chat"`
	Text            string       `json:"text"`
	MessageThreadID int          `json:"message_thread_id,omitempty"`
}

type telegramUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username,omitempty"`
}

type telegramChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"` // private, group, supergroup, channel
}

func buildInboundFromUpdate(channelID string, cfg adapter.ChannelConfig, update *telegramUpdate) adapter.InboundMessage {
	m := update.Message
	return adapter.InboundMessage{
		Channel:   nonEmpty(normalizeID(channelID), TelegramChannelID),
		From:      fmt.Sprintf("%d", m.From.ID),
		To:        fmt.Sprintf("%d", m.Chat.ID),
		ThreadID:  telegramThreadID(m.MessageThreadID),
		ChatType:  resolveTelegramChatType(m.Chat.Type),
		Text:      strings.TrimSpace(m.Text),
		AgentID:   normalizeAgentID(cfg.Agent),
		MessageID: fmt.Sprintf("%d", m.MessageID),
	}
}

func telegramThreadID(id int) string {
	if id == 0 {
		return ""
	}
	return fmt.Sprintf("%d", id)
}

func resolveTelegramChatType(chatType string) adapter.ChatType {
	switch chatType {
	case "private":
		return adapter.ChatTypeDirect
	case "group", "supergroup":
		return adapter.ChatTypeGroup
	case "channel":
		return adapter.ChatTypeTopic
	default:
		return adapter.ChatTypeDirect
	}
}

// ---------- Outbound ----------

// SendText sends a text message via the Telegram Bot API.
func (a *TelegramChannelAdapter) SendText(ctx context.Context, message adapter.OutboundMessage, ch adapter.ChannelConfig) (adapter.DeliveryResult, error) {
	token := resolveTelegramToken(ch)
	if token == "" {
		return adapter.DeliveryResult{}, adapter.ErrTokenRequired
	}

	chatID := strings.TrimSpace(message.To)
	if chatID == "" {
		return adapter.DeliveryResult{}, adapter.ErrTargetRequired
	}

	text := strings.TrimSpace(message.Payload.Text)
	if text == "" {
		return adapter.DeliveryResult{}, nil
	}

	chunks := chunkText(text, maxTextLen)
	var lastMsgID int
	threadID := strings.TrimSpace(message.ThreadID)
	for _, chunk := range chunks {
		msgID, err := a.sendMessage(ctx, token, chatID, chunk, threadID)
		if err != nil {
			return adapter.DeliveryResult{}, err
		}
		lastMsgID = msgID
	}

	return adapter.DeliveryResult{
		Channel:   a.ID(),
		MessageID: fmt.Sprintf("%d", lastMsgID),
		ChatID:    chatID,
	}, nil
}

// SendMedia sends a media message via the Telegram Bot API.
func (a *TelegramChannelAdapter) SendMedia(ctx context.Context, message adapter.OutboundMessage, ch adapter.ChannelConfig) (adapter.DeliveryResult, error) {
	token := resolveTelegramToken(ch)
	if token == "" {
		return adapter.DeliveryResult{}, adapter.ErrTokenRequired
	}

	chatID := strings.TrimSpace(message.To)
	if chatID == "" {
		return adapter.DeliveryResult{}, adapter.ErrTargetRequired
	}

	mediaURL := strings.TrimSpace(message.Payload.MediaURL)
	caption := strings.TrimSpace(message.Payload.Text)

	if mediaURL != "" {
		msgID, err := a.sendPhoto(ctx, token, chatID, mediaURL, caption, strings.TrimSpace(message.ThreadID))
		if err != nil {
			return adapter.DeliveryResult{}, err
		}
		return adapter.DeliveryResult{
			Channel:   a.ID(),
			MessageID: fmt.Sprintf("%d", msgID),
			ChatID:    chatID,
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
	var lastMsgID int
	for _, chunk := range chunks {
		id, err := a.sendMessage(ctx, token, chatID, chunk, strings.TrimSpace(message.ThreadID))
		if err != nil {
			return adapter.DeliveryResult{}, err
		}
		lastMsgID = id
	}

	return adapter.DeliveryResult{
		Channel:   a.ID(),
		MessageID: fmt.Sprintf("%d", lastMsgID),
		ChatID:    chatID,
	}, nil
}

// ChunkText implements adapter.TextChunkProvider.
func (a *TelegramChannelAdapter) ChunkText(text string, limit int) []string {
	return chunkText(text, limit)
}

// ChunkerMode implements adapter.TextChunkProvider.
func (a *TelegramChannelAdapter) ChunkerMode() string { return "length" }

func (a *TelegramChannelAdapter) sendMessage(ctx context.Context, token, chatID, text, threadID string) (int, error) {
	apiURL := fmt.Sprintf("%s/bot%s/sendMessage", telegramAPIBase, token)

	payload := map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}
	if threadID != "" {
		payload["message_thread_id"] = threadID
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("marshal telegram message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("create telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.dc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("send telegram message: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int `json:"message_id"`
		} `json:"result"`
		Description string `json:"description,omitempty"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return 0, fmt.Errorf("decode telegram response: %w", err)
	}
	if !result.OK {
		return 0, fmt.Errorf("telegram API error: %s", result.Description)
	}
	return result.Result.MessageID, nil
}

func (a *TelegramChannelAdapter) inboundChannelID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if id := normalizeID(a.channelID); id != "" {
		return id
	}
	return a.ID()
}

func (a *TelegramChannelAdapter) sendPhoto(ctx context.Context, token, chatID, photoURL, caption, threadID string) (int, error) {
	apiURL := fmt.Sprintf("%s/bot%s/sendPhoto", telegramAPIBase, token)

	payload := map[string]any{
		"chat_id": chatID,
		"photo":   photoURL,
	}
	if caption != "" {
		payload["caption"] = caption
		payload["parse_mode"] = "Markdown"
	}
	if threadID != "" {
		payload["message_thread_id"] = threadID
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("marshal telegram photo: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("create telegram photo request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.dc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("send telegram photo: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int `json:"message_id"`
		} `json:"result"`
		Description string `json:"description,omitempty"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return 0, fmt.Errorf("decode telegram photo response: %w", err)
	}
	if !result.OK {
		return 0, fmt.Errorf("telegram API error: %s", result.Description)
	}
	return result.Result.MessageID, nil
}

// =========================================================================
// Local helpers
// =========================================================================

func resolveTelegramToken(ch adapter.ChannelConfig) string {
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
