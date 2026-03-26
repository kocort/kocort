// Package qq implements the QQ channel adapter using the QQ Bot open platform.
// It maintains a WebSocket connection for real-time message ingress and sends
// outbound messages via the QQ Bot HTTP API. It self-registers with the
// channel driver registry on init.
//
// This adapter depends ONLY on internal/channel/adapter and external packages
// (gorilla/websocket, tencent-connect/botgo/dto).
package qq

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kocort/kocort/internal/channel/adapter"
	"github.com/kocort/kocort/internal/infra"
	"github.com/tencent-connect/botgo/dto"
)

const (
	QQChannelID = "qq"
)

var qqAPIBase = "https://api.sgroup.qq.com"
var qqSandboxAPIBase = "https://sandbox.api.sgroup.qq.com"

func init() {
	adapter.Register(QQChannelID, func() adapter.ChannelAdapter {
		return NewQQChannelAdapter(QQChannelID)
	})
}

// QQChannelAdapter implements the QQ Bot platform integration.
type QQChannelAdapter struct {
	adapter.BaseAdapter
	dc *infra.DynamicHTTPClient

	mu        sync.Mutex
	channelID string
	cfg       adapter.ChannelConfig
	cb        adapter.Callbacks
	started   bool
	cancel    context.CancelFunc

	// Token cache per app.
	tokenMu    sync.Mutex
	tokenCache map[string]*qqCachedToken
}

type qqCachedToken struct {
	AccessToken string
	ExpiresAt   time.Time
}

// NewQQChannelAdapter returns a new QQ adapter.
func NewQQChannelAdapter(id string) *QQChannelAdapter {
	return &QQChannelAdapter{
		BaseAdapter: adapter.NewBaseAdapter(normalizeID(id)),
		tokenCache:  make(map[string]*qqCachedToken),
	}
}

// Schema returns the driver schema for QQ.
func (a *QQChannelAdapter) Schema() adapter.ChannelDriverSchema {
	return adapter.ChannelDriverSchema{
		ID:          QQChannelID,
		Name:        "QQ Bot",
		Description: "Connect to QQ via the Bot Open Platform (WebSocket)",
		Fields: []adapter.ChannelConfigField{
			{
				Key:         "app_id",
				Label:       "App ID",
				Type:        adapter.FieldTypeText,
				Required:    true,
				Placeholder: "102000000",
				Help:        "Bot application ID from QQ open platform",
				Group:       "account",
			},
			{
				Key:         "app_secret",
				Label:       "App Secret",
				Type:        adapter.FieldTypePassword,
				Required:    true,
				Placeholder: "your-app-secret",
				Help:        "Bot application secret",
				Group:       "account",
			},
			{
				Key:         "token",
				Label:       "Bot Token",
				Type:        adapter.FieldTypePassword,
				Required:    false,
				Placeholder: "your-bot-token",
				Help:        "Legacy bot token (optional if using app_id/app_secret OAuth)",
				Group:       "account",
			},
			{
				Key:      "sandbox",
				Label:    "Sandbox Mode",
				Type:     adapter.FieldTypeCheckbox,
				Required: false,
				Help:     "Use the sandbox API environment",
			},
			{
				Key:         "intents",
				Label:       "Intents",
				Type:        adapter.FieldTypeText,
				Required:    false,
				Placeholder: "GUILDS,GUILD_MESSAGES,DIRECT_MESSAGE",
				Help:        "Comma-separated list of intent names",
			},
			{
				Key:      "removeAt",
				Label:    "Remove @ Mentions",
				Type:     adapter.FieldTypeCheckbox,
				Required: false,
				Help:     "Strip @bot mentions from message text",
			},
			{
				Key:         "defaultTo",
				Label:       "Default Target",
				Type:        adapter.FieldTypeText,
				Required:    false,
				Placeholder: "user:123 or group:456 or channel:789",
				Help:        "Default outbound target with type prefix",
			},
			{
				Key:         "inboundToken",
				Label:       "Webhook Token (Optional)",
				Type:        adapter.FieldTypePassword,
				Required:    false,
				Placeholder: "your-webhook-secret",
				Help:        "Token for authenticating inbound HTTP requests",
			},
		},
	}
}

// StartBackground starts the WebSocket connection(s) for all configured accounts.
func (a *QQChannelAdapter) StartBackground(parentCtx context.Context, channelID string, cfg adapter.ChannelConfig, dc *infra.DynamicHTTPClient, cb adapter.Callbacks) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.channelID = channelID
	a.cfg = cfg
	a.cb = cb
	a.started = true
	a.dc = dc

	ctx, cancel := context.WithCancel(parentCtx)
	a.cancel = cancel

	accounts := qqAccountsFromChannel(cfg)
	for _, acct := range accounts {
		go a.runWebSocket(ctx, acct)
	}

	return nil
}

// StopBackground stops all WebSocket connections.
func (a *QQChannelAdapter) StopBackground() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.started = false
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
}

// ServeHTTP handles inbound HTTP requests (forwarded webhook mode).
func (a *QQChannelAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, fmt.Sprintf("read qq request body: %v", err), http.StatusBadRequest)
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

// ---------- Outbound ----------

// SendText sends a text message to QQ (user, group, or guild channel).
func (a *QQChannelAdapter) SendText(ctx context.Context, message adapter.OutboundMessage, ch adapter.ChannelConfig) (adapter.DeliveryResult, error) {
	acct, err := a.resolveAccount(ch, message.AccountID)
	if err != nil {
		return adapter.DeliveryResult{}, err
	}

	to := strings.TrimSpace(message.To)
	if to == "" {
		return adapter.DeliveryResult{}, adapter.ErrTargetRequired
	}

	text := strings.TrimSpace(message.Payload.Text)
	if text == "" {
		return adapter.DeliveryResult{}, nil
	}

	targetType, targetID := parseTarget(to)
	msgID, err := a.sendText(ctx, acct, targetType, targetID, text, message.ThreadID)
	if err != nil {
		return adapter.DeliveryResult{}, err
	}

	return adapter.DeliveryResult{
		Channel:   a.ID(),
		MessageID: msgID,
		ChatID:    to,
	}, nil
}

// SendMedia sends a media message to QQ (falls back to text with URL).
func (a *QQChannelAdapter) SendMedia(ctx context.Context, message adapter.OutboundMessage, ch adapter.ChannelConfig) (adapter.DeliveryResult, error) {
	acct, err := a.resolveAccount(ch, message.AccountID)
	if err != nil {
		return adapter.DeliveryResult{}, err
	}

	to := strings.TrimSpace(message.To)
	if to == "" {
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

	targetType, targetID := parseTarget(to)
	msgID, err := a.sendText(ctx, acct, targetType, targetID, text, message.ThreadID)
	if err != nil {
		return adapter.DeliveryResult{}, err
	}

	return adapter.DeliveryResult{
		Channel:   a.ID(),
		MessageID: msgID,
		ChatID:    to,
	}, nil
}

func (a *QQChannelAdapter) sendText(ctx context.Context, acct qqResolvedAccount, targetType, targetID, text, refMsgID string) (string, error) {
	token, err := a.getAccessToken(ctx, acct)
	if err != nil {
		return "", fmt.Errorf("get qq access token: %w", err)
	}

	apiBase := qqAPIBase
	if acct.sandbox {
		apiBase = qqSandboxAPIBase
	}

	var apiURL string
	payload := map[string]any{
		"content": text,
	}
	if refMsgID != "" {
		payload["msg_id"] = refMsgID
	}

	switch targetType {
	case "user":
		apiURL = fmt.Sprintf("%s/v2/users/%s/messages", apiBase, targetID)
		payload["msg_type"] = 0
	case "group":
		apiURL = fmt.Sprintf("%s/v2/groups/%s/messages", apiBase, targetID)
		payload["msg_type"] = 0
	case "channel":
		apiURL = fmt.Sprintf("%s/channels/%s/messages", apiBase, targetID)
	default:
		// Default to guild channel format.
		apiURL = fmt.Sprintf("%s/channels/%s/messages", apiBase, targetID)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal qq message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create qq request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "QQBot "+token)

	resp, err := a.dc.Do(req)
	if err != nil {
		return "", fmt.Errorf("send qq message: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var result struct {
		ID      string `json:"id"`
		Message string `json:"message,omitempty"`
		Code    int    `json:"code,omitempty"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decode qq response: %w", err)
	}
	if result.Code != 0 && result.Message != "" {
		return "", fmt.Errorf("qq API error (%d): %s", result.Code, result.Message)
	}
	return result.ID, nil
}

// =========================================================================
// WebSocket lifecycle
// =========================================================================

func (a *QQChannelAdapter) runWebSocket(ctx context.Context, acct qqResolvedAccount) {
	a.audit(ctx, "info", "qq_websocket_starting",
		fmt.Sprintf("Starting QQ WebSocket for app %s", acct.appID), nil)

	for {
		if ctx.Err() != nil {
			return
		}

		err := a.connectAndListen(ctx, acct)
		if ctx.Err() != nil {
			return
		}

		a.audit(ctx, "warn", "qq_websocket_disconnected",
			fmt.Sprintf("QQ WebSocket disconnected for app %s: %v — reconnecting", acct.appID, err), nil)

		jitter := time.Duration(rand.Intn(5000)) * time.Millisecond
		select {
		case <-ctx.Done():
			return
		case <-time.After(5*time.Second + jitter):
		}
	}
}

func (a *QQChannelAdapter) connectAndListen(ctx context.Context, acct qqResolvedAccount) error {
	token, err := a.getAccessToken(ctx, acct)
	if err != nil {
		return fmt.Errorf("get access token: %w", err)
	}

	apiBase := qqAPIBase
	if acct.sandbox {
		apiBase = qqSandboxAPIBase
	}

	gatewayURL, err := a.fetchGateway(ctx, apiBase, token)
	if err != nil {
		return fmt.Errorf("fetch gateway: %w", err)
	}

	dialer := websocket.DefaultDialer
	if a.dc != nil {
		if cl := a.dc.Client(); cl != nil {
			if tr, ok := cl.Transport.(*http.Transport); ok && tr != nil && tr.Proxy != nil {
				dialer = &websocket.Dialer{
					Proxy: tr.Proxy,
				}
			}
		}
	}

	conn, _, err := dialer.DialContext(ctx, gatewayURL, nil)
	if err != nil {
		return fmt.Errorf("dial gateway: %w", err)
	}
	defer conn.Close()

	// Read Hello.
	_, helloMsg, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("read hello: %w", err)
	}

	var hello dto.WSPayload
	if err := json.Unmarshal(helloMsg, &hello); err != nil {
		return fmt.Errorf("parse hello: %w", err)
	}
	if hello.OPCode != dto.WSHello {
		return fmt.Errorf("expected Hello, got OP %d", hello.OPCode)
	}

	var helloData struct {
		HeartbeatInterval int `json:"heartbeat_interval"`
	}
	if hello.RawMessage != nil {
		_ = json.Unmarshal(hello.RawMessage, &helloData)
	}
	heartbeatInterval := time.Duration(helloData.HeartbeatInterval) * time.Millisecond
	if heartbeatInterval <= 0 {
		heartbeatInterval = 41250 * time.Millisecond
	}

	// Identify.
	intents := acct.computeIntents()
	identify := dto.WSPayload{
		WSPayloadBase: dto.WSPayloadBase{OPCode: dto.WSIdentity},
	}
	identifyData := map[string]any{
		"token":   fmt.Sprintf("QQBot %s", token),
		"intents": intents,
	}
	identifyBytes, _ := json.Marshal(identifyData)
	identify.RawMessage = identifyBytes

	if err := conn.WriteJSON(identify); err != nil {
		return fmt.Errorf("send identify: %w", err)
	}

	a.audit(ctx, "info", "qq_websocket_connected",
		fmt.Sprintf("QQ WebSocket connected for app %s", acct.appID), nil)

	// Heartbeat + dispatch loop.
	var lastSeq int64
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			hb := dto.WSPayload{
				WSPayloadBase: dto.WSPayloadBase{OPCode: dto.WSHeartbeat},
			}
			seqBytes, _ := json.Marshal(lastSeq)
			hb.RawMessage = seqBytes
			if err := conn.WriteJSON(hb); err != nil {
				return fmt.Errorf("send heartbeat: %w", err)
			}
		default:
			conn.SetReadDeadline(time.Now().Add(heartbeatInterval + 10*time.Second))
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return fmt.Errorf("read message: %w", err)
			}

			var payload dto.WSPayload
			if err := json.Unmarshal(msg, &payload); err != nil {
				continue
			}

			if payload.Seq > 0 {
				lastSeq = int64(payload.Seq)
			}

			switch payload.OPCode {
			case dto.WSHeartbeatAck:
				// OK.
			case dto.WSDispatchEvent:
				a.handleDispatch(ctx, acct, &payload)
			case dto.WSReconnect:
				return fmt.Errorf("server requested reconnect")
			case dto.WSInvalidSession:
				return fmt.Errorf("invalid session")
			}
		}
	}
}

func (a *QQChannelAdapter) handleDispatch(ctx context.Context, acct qqResolvedAccount, payload *dto.WSPayload) {
	eventType := string(payload.Type)

	switch eventType {
	case "C2C_MESSAGE_CREATE":
		a.handleC2CMessage(ctx, acct, payload.RawMessage)
	case "GROUP_AT_MESSAGE_CREATE":
		a.handleGroupAtMessage(ctx, acct, payload.RawMessage)
	case "DIRECT_MESSAGE_CREATE":
		a.handleDirectMessage(ctx, acct, payload.RawMessage)
	case "AT_MESSAGE_CREATE":
		a.handleAtMessage(ctx, acct, payload.RawMessage)
	}
}

func (a *QQChannelAdapter) handleC2CMessage(ctx context.Context, acct qqResolvedAccount, raw json.RawMessage) {
	var msg struct {
		ID     string `json:"id"`
		Author struct {
			ID string `json:"id"`
		} `json:"author"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	a.mu.Lock()
	cb := a.cb
	cfg := a.cfg
	a.mu.Unlock()

	text := msg.Content
	if boolFromMap(acct.config, "removeAt") {
		text = stripAtMentions(text)
	}

	inbound := adapter.InboundMessage{
		Channel:   a.inboundChannelID(),
		AccountID: acct.appID,
		From:      strings.TrimSpace(msg.Author.ID),
		To:        "user:" + strings.TrimSpace(msg.Author.ID),
		ChatType:  adapter.ChatTypeDirect,
		Text:      strings.TrimSpace(text),
		AgentID:   normalizeAgentID(cfg.Agent),
		MessageID: strings.TrimSpace(msg.ID),
	}

	if cb.OnMessage != nil {
		_ = cb.OnMessage(ctx, inbound)
	}
}

func (a *QQChannelAdapter) handleGroupAtMessage(ctx context.Context, acct qqResolvedAccount, raw json.RawMessage) {
	var msg struct {
		ID      string `json:"id"`
		GroupID string `json:"group_id"`
		Author  struct {
			ID string `json:"id"`
		} `json:"author"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	a.mu.Lock()
	cb := a.cb
	cfg := a.cfg
	a.mu.Unlock()

	text := msg.Content
	if boolFromMap(acct.config, "removeAt") {
		text = stripAtMentions(text)
	}

	inbound := adapter.InboundMessage{
		Channel:   a.inboundChannelID(),
		AccountID: acct.appID,
		From:      strings.TrimSpace(msg.Author.ID),
		To:        "group:" + strings.TrimSpace(msg.GroupID),
		ChatType:  adapter.ChatTypeGroup,
		Text:      strings.TrimSpace(text),
		AgentID:   normalizeAgentID(cfg.Agent),
		MessageID: strings.TrimSpace(msg.ID),
	}

	if cb.OnMessage != nil {
		_ = cb.OnMessage(ctx, inbound)
	}
}

func (a *QQChannelAdapter) handleDirectMessage(ctx context.Context, acct qqResolvedAccount, raw json.RawMessage) {
	var msg struct {
		ID        string `json:"id"`
		GuildID   string `json:"guild_id"`
		ChannelID string `json:"channel_id"`
		Author    struct {
			ID string `json:"id"`
		} `json:"author"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	a.mu.Lock()
	cb := a.cb
	cfg := a.cfg
	a.mu.Unlock()

	text := msg.Content
	if boolFromMap(acct.config, "removeAt") {
		text = stripAtMentions(text)
	}

	inbound := adapter.InboundMessage{
		Channel:   a.inboundChannelID(),
		AccountID: acct.appID,
		From:      strings.TrimSpace(msg.Author.ID),
		To:        "channel:" + strings.TrimSpace(msg.ChannelID),
		ChatType:  adapter.ChatTypeDirect,
		Text:      strings.TrimSpace(text),
		AgentID:   normalizeAgentID(cfg.Agent),
		MessageID: strings.TrimSpace(msg.ID),
	}

	if cb.OnMessage != nil {
		_ = cb.OnMessage(ctx, inbound)
	}
}

func (a *QQChannelAdapter) handleAtMessage(ctx context.Context, acct qqResolvedAccount, raw json.RawMessage) {
	var msg struct {
		ID        string `json:"id"`
		GuildID   string `json:"guild_id"`
		ChannelID string `json:"channel_id"`
		Author    struct {
			ID string `json:"id"`
		} `json:"author"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	a.mu.Lock()
	cb := a.cb
	cfg := a.cfg
	a.mu.Unlock()

	text := msg.Content
	if boolFromMap(acct.config, "removeAt") {
		text = stripAtMentions(text)
	}

	inbound := adapter.InboundMessage{
		Channel:   a.inboundChannelID(),
		AccountID: acct.appID,
		From:      strings.TrimSpace(msg.Author.ID),
		To:        "channel:" + strings.TrimSpace(msg.ChannelID),
		ChatType:  adapter.ChatTypeGroup,
		Text:      strings.TrimSpace(text),
		AgentID:   normalizeAgentID(cfg.Agent),
		MessageID: strings.TrimSpace(msg.ID),
	}

	if cb.OnMessage != nil {
		_ = cb.OnMessage(ctx, inbound)
	}
}

// =========================================================================
// OAuth token management
// =========================================================================

func (a *QQChannelAdapter) getAccessToken(ctx context.Context, acct qqResolvedAccount) (string, error) {
	// If there's a static token, just use that.
	if acct.token != "" {
		return acct.token, nil
	}
	if acct.appID == "" || acct.appSecret == "" {
		return "", adapter.ErrTokenRequired
	}

	cacheKey := acct.appID
	a.tokenMu.Lock()
	cached, ok := a.tokenCache[cacheKey]
	a.tokenMu.Unlock()

	if ok && time.Now().Before(cached.ExpiresAt.Add(-30*time.Second)) {
		return cached.AccessToken, nil
	}

	// Fetch new token.
	tokenURL := "https://bots.qq.com/app/getAppAccessToken"
	payload := map[string]string{
		"appId":        acct.appID,
		"clientSecret": acct.appSecret,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.dc.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch qq access token: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   string `json:"expires_in"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decode qq token response: %w", err)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("empty access token from QQ API")
	}

	expiresIn, _ := strconv.Atoi(result.ExpiresIn)
	if expiresIn <= 0 {
		expiresIn = 7200
	}

	a.tokenMu.Lock()
	a.tokenCache[cacheKey] = &qqCachedToken{
		AccessToken: result.AccessToken,
		ExpiresAt:   time.Now().Add(time.Duration(expiresIn) * time.Second),
	}
	a.tokenMu.Unlock()

	return result.AccessToken, nil
}

func (a *QQChannelAdapter) fetchGateway(ctx context.Context, apiBase, token string) (string, error) {
	apiURL := apiBase + "/gateway"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "QQBot "+token)

	resp, err := a.dc.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch qq gateway: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode gateway response: %w", err)
	}
	if result.URL == "" {
		return "", fmt.Errorf("empty gateway URL")
	}
	return result.URL, nil
}

func (a *QQChannelAdapter) resolveAccount(ch adapter.ChannelConfig, accountID string) (qqResolvedAccount, error) {
	accounts := qqAccountsFromChannel(ch)
	if len(accounts) == 0 {
		return qqResolvedAccount{}, adapter.ErrTokenRequired
	}

	if accountID != "" {
		for _, acct := range accounts {
			if acct.appID == accountID {
				return acct, nil
			}
		}
	}
	return accounts[0], nil
}

func (a *QQChannelAdapter) audit(ctx context.Context, level, eventType, message string, data map[string]any) {
	a.mu.Lock()
	cb := a.cb
	a.mu.Unlock()

	if cb.OnAudit != nil {
		cb.OnAudit(ctx, adapter.AuditEntry{
			Category: "channel",
			Type:     eventType,
			Level:    level,
			Channel:  a.ID(),
			Message:  message,
			Data:     data,
		})
	}
}

func (a *QQChannelAdapter) inboundChannelID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if id := normalizeID(a.channelID); id != "" {
		return id
	}
	return a.ID()
}

// =========================================================================
// Account resolution
// =========================================================================

type qqResolvedAccount struct {
	appID     string
	appSecret string
	token     string
	sandbox   bool
	config    map[string]any
}

func (acct qqResolvedAccount) computeIntents() int {
	intentStr := stringFromMap(acct.config, "intents")
	if intentStr == "" {
		// Default intents: GUILDS + GUILD_MESSAGES + DIRECT_MESSAGE + C2C + GROUP
		return int(dto.IntentGuilds) |
			int(dto.IntentGuildMessages) |
			int(dto.IntentDirectMessages) |
			int(dto.IntentGroupMessages)
	}

	var intent int
	for _, name := range splitCSVString(intentStr) {
		name = strings.TrimSpace(strings.ToUpper(name))
		i := dto.EventToIntent(dto.EventType(name))
		if i != 0 {
			intent |= int(i)
		}
		// Custom bit values for newer events not in the SDK.
		switch name {
		case "C2C_MESSAGE_CREATE", "GROUP_AT_MESSAGE_CREATE":
			intent |= int(dto.IntentGroupMessages)
		}
	}
	return intent
}

func qqAccountsFromChannel(cfg adapter.ChannelConfig) []qqResolvedAccount {
	var accounts []qqResolvedAccount

	// Check Accounts array.
	if cfg.Accounts != nil {
		for _, acct := range cfg.Accounts {
			if m, ok := acct.(map[string]any); ok {
				appID := stringFromMap(m, "app_id")
				if appID == "" {
					continue
				}
				accounts = append(accounts, qqResolvedAccount{
					appID:     appID,
					appSecret: stringFromMap(m, "app_secret"),
					token:     stringFromMap(m, "token"),
					sandbox:   boolFromMap(m, "sandbox"),
					config:    m,
				})
			}
		}
	}

	// Fall back to global Config.
	if len(accounts) == 0 && cfg.Config != nil {
		appID := stringFromMap(cfg.Config, "app_id")
		if appID != "" {
			accounts = append(accounts, qqResolvedAccount{
				appID:     appID,
				appSecret: stringFromMap(cfg.Config, "app_secret"),
				token:     stringFromMap(cfg.Config, "token"),
				sandbox:   boolFromMap(cfg.Config, "sandbox"),
				config:    cfg.Config,
			})
		}
	}

	return accounts
}

// parseTarget parses a "type:id" string into target type and ID.
func parseTarget(target string) (string, string) {
	if idx := strings.Index(target, ":"); idx > 0 {
		return target[:idx], target[idx+1:]
	}
	return "channel", target
}

// stripAtMentions removes <@!id> style mentions from text.
func stripAtMentions(text string) string {
	// Remove patterns like <@!12345> or <@12345>.
	result := text
	for {
		start := strings.Index(result, "<@")
		if start == -1 {
			break
		}
		end := strings.Index(result[start:], ">")
		if end == -1 {
			break
		}
		result = result[:start] + result[start+end+1:]
	}
	return strings.TrimSpace(result)
}

// =========================================================================
// Local helpers
// =========================================================================

func stringFromMap(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	switch s := v.(type) {
	case string:
		return strings.TrimSpace(s)
	case float64:
		return strconv.FormatFloat(s, 'f', -1, 64)
	case int:
		return strconv.Itoa(s)
	default:
		return fmt.Sprintf("%v", s)
	}
}

func boolFromMap(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	v, ok := m[key]
	if !ok {
		return false
	}
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return strings.EqualFold(b, "true") || b == "1"
	case float64:
		return b != 0
	default:
		return false
	}
}

func splitCSVString(s string) []string {
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
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
