// Package whatsapp implements the WhatsApp Cloud API channel adapter.
// It processes incoming webhooks from the WhatsApp Business Platform and sends
// messages via the Cloud API. It self-registers with the channel driver registry on init.
package whatsapp

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
	WhatsAppChannelID = "whatsapp"
	maxTextLen        = 4096
)

var whatsappAPIBase = "https://graph.facebook.com/v18.0"

func init() {
	adapter.Register(WhatsAppChannelID, func() adapter.ChannelAdapter {
		return NewWhatsAppChannelAdapter(WhatsAppChannelID)
	})
}

// WhatsAppChannelAdapter implements the WhatsApp Cloud API integration.
type WhatsAppChannelAdapter struct {
	adapter.BaseAdapter
	dc *infra.DynamicHTTPClient

	mu        sync.Mutex
	channelID string
	cfg       adapter.ChannelConfig
	cb        adapter.Callbacks
	started   bool
}

// NewWhatsAppChannelAdapter returns a new WhatsApp adapter.
func NewWhatsAppChannelAdapter(id string) *WhatsAppChannelAdapter {
	return &WhatsAppChannelAdapter{
		BaseAdapter: adapter.NewBaseAdapter(normalizeID(id)),
	}
}

// Schema returns the driver schema for WhatsApp.
func (a *WhatsAppChannelAdapter) Schema() adapter.ChannelDriverSchema {
	return adapter.ChannelDriverSchema{
		ID:          WhatsAppChannelID,
		Name:        "WhatsApp",
		Description: "Connect to WhatsApp via Cloud API",
		Fields: []adapter.ChannelConfigField{
			{
				Key:         "access_token",
				Label:       "Access Token",
				Type:        adapter.FieldTypePassword,
				Required:    true,
				Placeholder: "your-access-token",
				Help:        "WhatsApp Cloud API access token",
				Group:       "account",
			},
			{
				Key:         "phone_number_id",
				Label:       "Phone Number ID",
				Type:        adapter.FieldTypeText,
				Required:    true,
				Placeholder: "123456789012345",
				Help:        "WhatsApp business phone number ID",
				Group:       "account",
			},
			{
				Key:         "defaultTo",
				Label:       "Default Recipient",
				Type:        adapter.FieldTypeText,
				Required:    false,
				Placeholder: "+1234567890",
				Help:        "Default recipient phone number for outbound messages",
			},
			{
				Key:         "inboundToken",
				Label:       "Verify Token (Optional)",
				Type:        adapter.FieldTypePassword,
				Required:    false,
				Placeholder: "your-verify-token",
				Help:        "Token for webhook verification and inbound authentication",
			},
		},
	}
}

// StartBackground begins the inbound message lifecycle.
func (a *WhatsAppChannelAdapter) StartBackground(_ context.Context, channelID string, cfg adapter.ChannelConfig, dc *infra.DynamicHTTPClient, cb adapter.Callbacks) error {
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
func (a *WhatsAppChannelAdapter) StopBackground() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.started = false
}

// ServeHTTP handles inbound WhatsApp webhooks.
func (a *WhatsAppChannelAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Handle GET for webhook verification.
	if r.Method == http.MethodGet {
		a.handleVerification(w, r)
		return
	}

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
		http.Error(w, fmt.Sprintf("read whatsapp request body: %v", err), http.StatusBadRequest)
		return
	}

	// Try WhatsApp webhook format.
	var webhook whatsappWebhook
	if err := json.Unmarshal(body, &webhook); err == nil && len(webhook.Entry) > 0 {
		msgs := extractWhatsAppMessages(a.inboundChannelID(), cfg, &webhook)
		for _, msg := range msgs {
			if err := cb.OnMessage(r.Context(), msg); err != nil {
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

func (a *WhatsAppChannelAdapter) handleVerification(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()

	mode := r.URL.Query().Get("hub.mode")
	token := r.URL.Query().Get("hub.verify_token")
	challenge := r.URL.Query().Get("hub.challenge")

	if mode == "subscribe" && token != "" && token == strings.TrimSpace(cfg.InboundToken) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(challenge))
		return
	}
	http.Error(w, adapter.ErrUnauthorized.Error(), http.StatusForbidden)
}

// ---------- WhatsApp webhook types ----------

type whatsappWebhook struct {
	Object string          `json:"object"`
	Entry  []whatsappEntry `json:"entry"`
}

type whatsappEntry struct {
	ID      string           `json:"id"`
	Changes []whatsappChange `json:"changes"`
}

type whatsappChange struct {
	Value whatsappValue `json:"value"`
	Field string        `json:"field"`
}

type whatsappValue struct {
	MessagingProduct string            `json:"messaging_product"`
	Metadata         whatsappMetadata  `json:"metadata"`
	Messages         []whatsappMessage `json:"messages"`
	Contacts         []whatsappContact `json:"contacts,omitempty"`
}

type whatsappMetadata struct {
	DisplayPhoneNumber string `json:"display_phone_number"`
	PhoneNumberID      string `json:"phone_number_id"`
}

type whatsappMessage struct {
	ID        string   `json:"id"`
	From      string   `json:"from"`
	Timestamp string   `json:"timestamp"`
	Type      string   `json:"type"`
	Text      *waText  `json:"text,omitempty"`
	Image     *waMedia `json:"image,omitempty"`
	Document  *waMedia `json:"document,omitempty"`
}

type waText struct {
	Body string `json:"body"`
}

type waMedia struct {
	Caption  string `json:"caption,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	ID       string `json:"id"`
	Link     string `json:"link,omitempty"`
}

type whatsappContact struct {
	WaID    string          `json:"wa_id"`
	Profile whatsappProfile `json:"profile"`
}

type whatsappProfile struct {
	Name string `json:"name"`
}

func extractWhatsAppMessages(channelID string, cfg adapter.ChannelConfig, webhook *whatsappWebhook) []adapter.InboundMessage {
	var msgs []adapter.InboundMessage
	for _, entry := range webhook.Entry {
		for _, change := range entry.Changes {
			phoneNumberID := change.Value.Metadata.PhoneNumberID
			for _, m := range change.Value.Messages {
				text := ""
				if m.Text != nil {
					text = m.Text.Body
				} else if m.Image != nil {
					text = nonEmpty(m.Image.Caption, "[image]")
				} else if m.Document != nil {
					text = nonEmpty(m.Document.Caption, "[document]")
				}

				msgs = append(msgs, adapter.InboundMessage{
					Channel:   nonEmpty(normalizeID(channelID), WhatsAppChannelID),
					AccountID: phoneNumberID,
					From:      strings.TrimSpace(m.From),
					To:        phoneNumberID,
					ChatType:  adapter.ChatTypeDirect,
					Text:      strings.TrimSpace(text),
					AgentID:   normalizeAgentID(cfg.Agent),
					MessageID: strings.TrimSpace(m.ID),
				})
			}
		}
	}
	return msgs
}

// ---------- Outbound ----------

// SendText sends a text message via the WhatsApp Cloud API.
func (a *WhatsAppChannelAdapter) SendText(ctx context.Context, message adapter.OutboundMessage, ch adapter.ChannelConfig) (adapter.DeliveryResult, error) {
	creds := resolveWhatsAppCredentials(ch, message.AccountID)
	if creds.accessToken == "" {
		return adapter.DeliveryResult{}, adapter.ErrTokenRequired
	}
	if creds.phoneNumberID == "" {
		return adapter.DeliveryResult{}, fmt.Errorf("whatsapp phone_number_id is required")
	}

	to := strings.TrimSpace(message.To)
	if to == "" {
		return adapter.DeliveryResult{}, adapter.ErrTargetRequired
	}

	text := strings.TrimSpace(message.Payload.Text)
	if text == "" {
		return adapter.DeliveryResult{}, nil
	}

	chunks := chunkText(text, maxTextLen)
	var lastMsgID string
	for _, chunk := range chunks {
		msgID, err := a.sendTextMessage(ctx, creds, to, chunk)
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

// SendMedia sends a media message via the WhatsApp Cloud API.
func (a *WhatsAppChannelAdapter) SendMedia(ctx context.Context, message adapter.OutboundMessage, ch adapter.ChannelConfig) (adapter.DeliveryResult, error) {
	creds := resolveWhatsAppCredentials(ch, message.AccountID)
	if creds.accessToken == "" {
		return adapter.DeliveryResult{}, adapter.ErrTokenRequired
	}
	if creds.phoneNumberID == "" {
		return adapter.DeliveryResult{}, fmt.Errorf("whatsapp phone_number_id is required")
	}

	to := strings.TrimSpace(message.To)
	if to == "" {
		return adapter.DeliveryResult{}, adapter.ErrTargetRequired
	}

	mediaURL := strings.TrimSpace(message.Payload.MediaURL)
	caption := strings.TrimSpace(message.Payload.Text)

	if mediaURL != "" {
		msgID, err := a.sendImageMessage(ctx, creds, to, mediaURL, caption)
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
		id, err := a.sendTextMessage(ctx, creds, to, chunk)
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
func (a *WhatsAppChannelAdapter) ChunkText(text string, limit int) []string {
	return chunkText(text, limit)
}

// ChunkerMode implements adapter.TextChunkProvider.
func (a *WhatsAppChannelAdapter) ChunkerMode() string { return "length" }

type waCredentials struct {
	accessToken   string
	phoneNumberID string
}

func (a *WhatsAppChannelAdapter) sendTextMessage(ctx context.Context, creds waCredentials, to, text string) (string, error) {
	apiURL := fmt.Sprintf("%s/%s/messages", whatsappAPIBase, creds.phoneNumberID)

	payload := map[string]any{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              "text",
		"text":              map[string]any{"body": text},
	}

	return a.doWhatsAppRequest(ctx, apiURL, creds.accessToken, payload)
}

func (a *WhatsAppChannelAdapter) inboundChannelID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if id := normalizeID(a.channelID); id != "" {
		return id
	}
	return a.ID()
}

func (a *WhatsAppChannelAdapter) sendImageMessage(ctx context.Context, creds waCredentials, to, imageURL, caption string) (string, error) {
	apiURL := fmt.Sprintf("%s/%s/messages", whatsappAPIBase, creds.phoneNumberID)

	imageObj := map[string]any{"link": imageURL}
	if caption != "" {
		imageObj["caption"] = caption
	}

	payload := map[string]any{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              "image",
		"image":             imageObj,
	}

	return a.doWhatsAppRequest(ctx, apiURL, creds.accessToken, payload)
}

func (a *WhatsAppChannelAdapter) doWhatsAppRequest(ctx context.Context, apiURL, token string, payload map[string]any) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal whatsapp message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create whatsapp request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := a.dc.Do(req)
	if err != nil {
		return "", fmt.Errorf("send whatsapp message: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var result struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
		Error *struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decode whatsapp response: %w", err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("whatsapp API error (%d): %s", result.Error.Code, result.Error.Message)
	}
	if len(result.Messages) > 0 {
		return result.Messages[0].ID, nil
	}
	return "", nil
}

// =========================================================================
// Local helpers
// =========================================================================

func resolveWhatsAppCredentials(ch adapter.ChannelConfig, accountID string) waCredentials {
	// Try account-specific credentials first.
	if accountID != "" && ch.Accounts != nil {
		for _, acct := range ch.Accounts {
			if m, ok := acct.(map[string]any); ok {
				pid, _ := m["phone_number_id"].(string)
				if pid == accountID {
					token, _ := m["access_token"].(string)
					if token != "" {
						return waCredentials{
							accessToken:   strings.TrimSpace(token),
							phoneNumberID: strings.TrimSpace(pid),
						}
					}
				}
			}
		}
	}

	// Fall back to global config.
	if ch.Config != nil {
		token, _ := ch.Config["access_token"].(string)
		pid, _ := ch.Config["phone_number_id"].(string)
		if token != "" && pid != "" {
			return waCredentials{
				accessToken:   strings.TrimSpace(token),
				phoneNumberID: strings.TrimSpace(pid),
			}
		}
	}

	// Last resort: first account.
	if ch.Accounts != nil {
		for _, acct := range ch.Accounts {
			if m, ok := acct.(map[string]any); ok {
				token, _ := m["access_token"].(string)
				pid, _ := m["phone_number_id"].(string)
				if token != "" && pid != "" {
					return waCredentials{
						accessToken:   strings.TrimSpace(token),
						phoneNumberID: strings.TrimSpace(pid),
					}
				}
			}
		}
	}

	return waCredentials{}
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
