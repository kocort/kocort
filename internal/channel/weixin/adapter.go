// Package weixin implements the WeChat channel adapter using the Tencent iLink
// bot API. It receives inbound messages via long-polling and delivers outbound
// text/media via the iLink SendMessage + CDN upload flow.
//
// This adapter depends ONLY on the adapter package — no other project internals.
package weixin

import (
	"context"
	"crypto/md5" //nolint:gosec
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/channel/adapter"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/utils"
)

const (
	WeixinChannelID = "weixin"
	maxTextLen      = 4000

	defaultBaseURL    = "https://ilinkai.weixin.qq.com"
	defaultCDNBaseURL = "https://novac2c.cdn.weixin.qq.com/c2c"
	defaultBotType    = "3"
)

// ---------------------------------------------------------------------------
// Config Parsing
// ---------------------------------------------------------------------------

type adapterConfig struct {
	Token              string
	BaseURL            string
	CDNBaseURL         string
	PollTimeoutSeconds int
	EnableTyping       bool
}

func parseAdapterConfig(raw map[string]any) (adapterConfig, error) {
	cfg := adapterConfig{
		BaseURL:    defaultBaseURL,
		CDNBaseURL: defaultCDNBaseURL,
	}
	if raw == nil {
		return adapterConfig{}, errors.New("weixin: config is nil")
	}
	cfg.Token = strings.TrimSpace(readString(raw, "token"))
	if v := strings.TrimSpace(readString(raw, "baseUrl", "base_url")); v != "" {
		cfg.BaseURL = v
	}
	if v := strings.TrimSpace(readString(raw, "cdnBaseUrl", "cdn_base_url")); v != "" {
		cfg.CDNBaseURL = v
	}
	if v, ok := readInt(raw, "pollTimeoutSeconds", "poll_timeout_seconds"); ok {
		cfg.PollTimeoutSeconds = v
	}
	if v, ok := readBool(raw, "enableTyping", "enable_typing"); ok {
		cfg.EnableTyping = v
	}
	if cfg.Token == "" {
		return adapterConfig{}, errors.New("weixin: token is required")
	}
	return cfg, nil
}

// ---------------------------------------------------------------------------
// Context Token Cache
// ---------------------------------------------------------------------------

type contextTokenEntry struct {
	token     string
	expiresAt time.Time
}

type contextTokenCache struct {
	mu      sync.Mutex
	entries map[string]contextTokenEntry
	ttl     time.Duration
}

func newContextTokenCache(ttl time.Duration) *contextTokenCache {
	return &contextTokenCache{
		entries: make(map[string]contextTokenEntry),
		ttl:     ttl,
	}
}

func (c *contextTokenCache) Put(key, token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = contextTokenEntry{token: token, expiresAt: time.Now().Add(c.ttl)}
	now := time.Now()
	for k, v := range c.entries {
		if now.After(v.expiresAt) {
			delete(c.entries, k)
		}
	}
}

func (c *contextTokenCache) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expiresAt) {
		return "", false
	}
	return e.token, true
}

// ---------------------------------------------------------------------------
// Driver Registration
// ---------------------------------------------------------------------------

func init() {
	adapter.Register(WeixinChannelID, func() adapter.ChannelAdapter {
		return NewWeixinAdapter(WeixinChannelID)
	})
}

// ---------------------------------------------------------------------------
// Adapter
// ---------------------------------------------------------------------------

// WeixinAdapter implements adapter.ChannelAdapter for WeChat via long-polling
// the iLink bot API.
type WeixinAdapter struct {
	id           string
	client       *Client
	contextCache *contextTokenCache
	runtimeID    string

	mu      sync.Mutex
	started map[string]bool
	cancel  context.CancelFunc

	// Stored from StartBackground.
	callbacks  adapter.Callbacks
	channelCfg adapter.ChannelConfig
}

// NewWeixinAdapter returns a new adapter for the given channel ID.
func NewWeixinAdapter(id string) *WeixinAdapter {
	return &WeixinAdapter{
		id:           normalizeID(id),
		client:       NewClient(nil),
		contextCache: newContextTokenCache(24 * time.Hour),
		started:      make(map[string]bool),
	}
}

func (a *WeixinAdapter) ID() string { return a.id }

func (a *WeixinAdapter) Schema() adapter.ChannelDriverSchema {
	return adapter.ChannelDriverSchema{
		ID:          WeixinChannelID,
		Name:        "WeChat (微信)",
		Description: "Connect to WeChat via iLink Bot API (personal WeChat long-poll)",
		Fields: []adapter.ChannelConfigField{
			{
				Key:         "token",
				Label:       "Bot Token",
				Type:        adapter.FieldTypePassword,
				Required:    true,
				Placeholder: "your-ilink-bot-token",
				Help:        "iLink bot token obtained via QR code login",
			},
			{
				Key:          "baseUrl",
				Label:        "API Base URL",
				Type:         adapter.FieldTypeText,
				Required:     false,
				DefaultValue: defaultBaseURL,
				Help:         "iLink API base URL (default: " + defaultBaseURL + ")",
			},
			{
				Key:      "pollTimeoutSeconds",
				Label:    "Poll Timeout (s)",
				Type:     adapter.FieldTypeNumber,
				Required: false,
				Help:     "Long-poll timeout in seconds (default: 35)",
			},
			{
				Key:      "enableTyping",
				Label:    "Enable Typing Indicator",
				Type:     adapter.FieldTypeCheckbox,
				Required: false,
				Help:     "Send typing indicator while processing messages",
			},
		},
	}
}

func (a *WeixinAdapter) StopBackground() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	a.started = make(map[string]bool)
	slog.Info("[weixin] background stopped", "adapter", a.id)
}

func (a *WeixinAdapter) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "weixin does not support HTTP ingress", http.StatusNotImplemented)
}

// TextChunkLimit returns the WeChat message size limit.
func (a *WeixinAdapter) TextChunkLimit() int { return maxTextLen }

// ---------------------------------------------------------------------------
// StartBackground — long-poll loop
// ---------------------------------------------------------------------------

func (a *WeixinAdapter) StartBackground(ctx context.Context, channelID string, cfg adapter.ChannelConfig, dc *infra.DynamicHTTPClient, cb adapter.Callbacks) error {
	adapterCfg, err := parseAdapterConfig(cfg.Config)
	if err != nil {
		return fmt.Errorf("weixin: invalid config: %w", err)
	}

	key := normalizeID(channelID)
	a.mu.Lock()
	if a.started[key] {
		a.mu.Unlock()
		return nil
	}
	a.started[key] = true
	a.runtimeID = key
	a.callbacks = cb
	a.channelCfg = cfg
	pollCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.mu.Unlock()

	go a.pollLoop(pollCtx, channelID, cfg, adapterCfg)

	a.emitAudit(ctx, adapter.AuditEntry{
		Category: "channel",
		Type:     "weixin_poll_started",
		Level:    "info",
		Channel:  WeixinChannelID,
		Message:  "weixin long-poll started",
		Data: map[string]any{
			"channel": channelID,
			"baseUrl": adapterCfg.BaseURL,
		},
	})
	return nil
}

func (a *WeixinAdapter) pollLoop(ctx context.Context, channelID string, ch adapter.ChannelConfig, cfg adapterConfig) {
	const (
		maxConsecutiveFailures = 3
		backoffDelay           = 30 * time.Second
		retryDelay             = 2 * time.Second
		sessionPauseDuration   = 1 * time.Hour
	)

	var getUpdatesBuf string
	var consecutiveFailures int

	slog.Info("[weixin] poll loop started",
		"channel", channelID,
		"baseUrl", cfg.BaseURL,
	)

	for {
		select {
		case <-ctx.Done():
			slog.Info("[weixin] poll loop stopped", "channel", channelID)
			return
		default:
		}

		resp, err := a.client.GetUpdates(ctx, cfg, getUpdatesBuf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			consecutiveFailures++
			slog.Error("[weixin] getupdates error",
				"channel", channelID,
				"error", err,
				"failures", consecutiveFailures,
			)
			if consecutiveFailures >= maxConsecutiveFailures {
				consecutiveFailures = 0
				sleepCtx(ctx, backoffDelay)
			} else {
				sleepCtx(ctx, retryDelay)
			}
			continue
		}

		isAPIError := (resp.Ret != 0) || (resp.ErrCode != 0)
		if isAPIError {
			if resp.ErrCode == sessionExpiredErrCode || resp.Ret == sessionExpiredErrCode {
				slog.Error("[weixin] session expired, pausing",
					"channel", channelID,
					"errcode", resp.ErrCode,
				)
				sleepCtx(ctx, sessionPauseDuration)
				consecutiveFailures = 0
				continue
			}
			consecutiveFailures++
			slog.Error("[weixin] getupdates api error",
				"channel", channelID,
				"ret", resp.Ret,
				"errcode", resp.ErrCode,
				"errmsg", resp.ErrMsg,
			)
			if consecutiveFailures >= maxConsecutiveFailures {
				consecutiveFailures = 0
				sleepCtx(ctx, backoffDelay)
			} else {
				sleepCtx(ctx, retryDelay)
			}
			continue
		}

		consecutiveFailures = 0
		if resp.GetUpdatesBuf != "" {
			getUpdatesBuf = resp.GetUpdatesBuf
		}

		for _, msg := range resp.Msgs {
			if msg.MessageType != MessageTypeUser {
				continue
			}

			inbound, ok := buildInboundMessage(channelID, msg, ch, cfg.CDNBaseURL)
			if !ok {
				continue
			}

			if strings.TrimSpace(msg.ContextToken) != "" {
				cacheKey := channelID + ":" + strings.TrimSpace(msg.FromUserID)
				a.contextCache.Put(cacheKey, msg.ContextToken)
			}

			a.mu.Lock()
			cb := a.callbacks
			a.mu.Unlock()

			go func(pushMsg adapter.InboundMessage) {
				pushErr := cb.OnMessage(context.Background(), pushMsg)
				if pushErr != nil {
					a.emitAudit(context.Background(), adapter.AuditEntry{
						Category: "channel",
						Type:     "weixin_inbound_push_failed",
						Level:    "error",
						Channel:  WeixinChannelID,
						Message:  "weixin inbound push failed",
						Data: map[string]any{
							"channel":   channelID,
							"from":      pushMsg.From,
							"messageId": pushMsg.MessageID,
							"error":     pushErr.Error(),
						},
					})
				}
			}(inbound)
		}
	}
}

// ---------------------------------------------------------------------------
// Inbound Message Builder
// ---------------------------------------------------------------------------

func buildInboundMessage(channelID string, msg WeixinMessage, ch adapter.ChannelConfig, cdnBaseURL string) (adapter.InboundMessage, bool) {
	text, attachments := extractContent(msg, cdnBaseURL)
	if strings.TrimSpace(text) == "" && len(attachments) == 0 {
		return adapter.InboundMessage{}, false
	}

	fromUserID := strings.TrimSpace(msg.FromUserID)
	if fromUserID == "" {
		return adapter.InboundMessage{}, false
	}

	msgID := strconv.FormatInt(msg.MessageID, 10)
	if msg.Seq > 0 {
		msgID = strconv.FormatInt(msg.MessageID, 10) + ":" + strconv.Itoa(msg.Seq)
	}

	return adapter.InboundMessage{
		Channel:     normalizeID(channelID),
		From:        fromUserID,
		To:          strings.TrimSpace(msg.ToUserID),
		ChatType:    adapter.ChatTypeDirect,
		Text:        text,
		Attachments: attachments,
		AgentID:     strings.TrimSpace(ch.Agent),
		MessageID:   msgID,
		Raw: map[string]any{
			"session_id":    strings.TrimSpace(msg.SessionID),
			"seq":           msg.Seq,
			"context_token": msg.ContextToken,
		},
	}, true
}

func extractContent(msg WeixinMessage, cdnBaseURL string) (string, []adapter.Attachment) {
	if len(msg.ItemList) == 0 {
		return "", nil
	}

	var textParts []string
	var attachments []adapter.Attachment

	for _, item := range msg.ItemList {
		switch item.Type {
		case ItemTypeText:
			t := extractTextFromItem(item)
			if t != "" {
				textParts = append(textParts, t)
			}
		case ItemTypeImage:
			if att, ok := buildImageAttachment(item, cdnBaseURL); ok {
				attachments = append(attachments, att)
			}
		case ItemTypeVoice:
			if item.VoiceItem != nil && strings.TrimSpace(item.VoiceItem.Text) != "" && !hasMediaRef(item) {
				textParts = append(textParts, item.VoiceItem.Text)
			} else if att, ok := buildVoiceAttachment(item, cdnBaseURL); ok {
				attachments = append(attachments, att)
			}
		case ItemTypeFile:
			if att, ok := buildFileAttachment(item, cdnBaseURL); ok {
				attachments = append(attachments, att)
			}
		case ItemTypeVideo:
			if att, ok := buildVideoAttachment(item, cdnBaseURL); ok {
				attachments = append(attachments, att)
			}
		}
	}

	return strings.Join(textParts, "\n"), attachments
}

func extractTextFromItem(item MessageItem) string {
	if item.TextItem == nil || strings.TrimSpace(item.TextItem.Text) == "" {
		return ""
	}
	text := item.TextItem.Text
	ref := item.RefMsg
	if ref == nil {
		return text
	}
	if ref.MessageItem != nil && isMediaItemType(ref.MessageItem.Type) {
		return text
	}
	var parts []string
	if strings.TrimSpace(ref.Title) != "" {
		parts = append(parts, ref.Title)
	}
	if ref.MessageItem != nil && ref.MessageItem.TextItem != nil &&
		strings.TrimSpace(ref.MessageItem.TextItem.Text) != "" {
		parts = append(parts, ref.MessageItem.TextItem.Text)
	}
	if len(parts) == 0 {
		return text
	}
	return fmt.Sprintf("[引用: %s]\n%s", strings.Join(parts, " | "), text)
}

func isMediaItemType(t int) bool {
	return t == ItemTypeImage || t == ItemTypeVideo || t == ItemTypeFile || t == ItemTypeVoice
}

// downloadCDNMedia downloads and optionally decrypts media from the WeChat CDN.
// The fallbackAESKeyHex parameter provides an alternate hex-encoded AES key
// (e.g. from ImageItem.AESKey) when CDNMedia.AESKey is empty.
func downloadCDNMedia(cdnBaseURL string, media *CDNMedia, fallbackAESKeyHex string) ([]byte, error) {
	if cdnBaseURL == "" {
		return nil, errors.New("weixin: CDN base URL is empty")
	}
	if media == nil || strings.TrimSpace(media.EncryptQueryParam) == "" {
		return nil, errors.New("weixin: no CDN media reference")
	}

	encParam := strings.TrimSpace(media.EncryptQueryParam)

	// Determine AES key: prefer CDNMedia.AESKey (base64), fall back to hex key.
	aesKeyB64 := strings.TrimSpace(media.AESKey)
	if aesKeyB64 == "" && strings.TrimSpace(fallbackAESKeyHex) != "" {
		// Wrap hex key in base64 so parseAESKey can handle it.
		aesKeyB64 = base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(fallbackAESKeyHex)))
	}

	if media.EncryptType == 0 || aesKeyB64 == "" {
		return downloadPlain(cdnBaseURL, encParam)
	}
	return downloadAndDecrypt(cdnBaseURL, encParam, aesKeyB64)
}

func hasMediaRef(item MessageItem) bool {
	return item.VoiceItem != nil && item.VoiceItem.Media != nil &&
		strings.TrimSpace(item.VoiceItem.Media.EncryptQueryParam) != ""
}

func buildImageAttachment(item MessageItem, cdnBaseURL string) (adapter.Attachment, bool) {
	img := item.ImageItem
	if img == nil || img.Media == nil || strings.TrimSpace(img.Media.EncryptQueryParam) == "" {
		return adapter.Attachment{}, false
	}
	att := adapter.Attachment{
		Type:     "image",
		MIMEType: "image/jpeg",
		Name:     "image.jpg",
	}
	if content, err := downloadCDNMedia(cdnBaseURL, img.Media, img.AESKey); err != nil {
		slog.Warn("[weixin] failed to download image from CDN",
			"error", err,
			"encryptQueryParam", img.Media.EncryptQueryParam,
		)
	} else {
		att.Content = content
		if detected := strings.TrimSpace(strings.Split(http.DetectContentType(content), ";")[0]); detected != "" {
			att.MIMEType = detected
		}
	}
	return att, true
}

func buildVoiceAttachment(item MessageItem, cdnBaseURL string) (adapter.Attachment, bool) {
	v := item.VoiceItem
	if v == nil || v.Media == nil || strings.TrimSpace(v.Media.EncryptQueryParam) == "" {
		return adapter.Attachment{}, false
	}
	att := adapter.Attachment{
		Type:     "audio",
		MIMEType: "audio/silk",
		Name:     "voice.silk",
	}
	if content, err := downloadCDNMedia(cdnBaseURL, v.Media, ""); err != nil {
		slog.Warn("[weixin] failed to download voice from CDN",
			"error", err,
			"encryptQueryParam", v.Media.EncryptQueryParam,
		)
	} else {
		att.Content = content
	}
	return att, true
}

func buildFileAttachment(item MessageItem, cdnBaseURL string) (adapter.Attachment, bool) {
	f := item.FileItem
	if f == nil || f.Media == nil || strings.TrimSpace(f.Media.EncryptQueryParam) == "" {
		return adapter.Attachment{}, false
	}
	name := strings.TrimSpace(f.FileName)
	if name == "" {
		name = "file"
	}
	att := adapter.Attachment{
		Type:     "file",
		MIMEType: "application/octet-stream",
		Name:     name,
	}
	if content, err := downloadCDNMedia(cdnBaseURL, f.Media, ""); err != nil {
		slog.Warn("[weixin] failed to download file from CDN",
			"error", err,
			"encryptQueryParam", f.Media.EncryptQueryParam,
		)
	} else {
		att.Content = content
	}
	return att, true
}

func buildVideoAttachment(item MessageItem, cdnBaseURL string) (adapter.Attachment, bool) {
	v := item.VideoItem
	if v == nil || v.Media == nil || strings.TrimSpace(v.Media.EncryptQueryParam) == "" {
		return adapter.Attachment{}, false
	}
	att := adapter.Attachment{
		Type:     "video",
		MIMEType: "video/mp4",
		Name:     "video.mp4",
	}
	if content, err := downloadCDNMedia(cdnBaseURL, v.Media, ""); err != nil {
		slog.Warn("[weixin] failed to download video from CDN",
			"error", err,
			"encryptQueryParam", v.Media.EncryptQueryParam,
		)
	} else {
		att.Content = content
	}
	return att, true
}

// ---------------------------------------------------------------------------
// SendText
// ---------------------------------------------------------------------------

func (a *WeixinAdapter) SendText(ctx context.Context, message adapter.OutboundMessage, cfg adapter.ChannelConfig) (adapter.DeliveryResult, error) {
	adapterCfg, err := parseAdapterConfig(cfg.Config)
	if err != nil {
		return adapter.DeliveryResult{}, err
	}

	target := strings.TrimSpace(message.To)
	if target == "" {
		return adapter.DeliveryResult{}, errors.New("weixin: outbound target (user ID) is required")
	}

	text := strings.TrimSpace(message.Payload.Text)
	if text == "" {
		return adapter.DeliveryResult{}, nil
	}

	channelID := a.id
	a.mu.Lock()
	if strings.TrimSpace(a.runtimeID) != "" {
		channelID = a.runtimeID
	}
	a.mu.Unlock()
	cacheKey := channelID + ":" + target
	contextToken, ok := a.contextCache.Get(cacheKey)
	if !ok {
		return adapter.DeliveryResult{}, fmt.Errorf("weixin: no context_token cached for target %s (reply-only channel — message can only be sent after receiving an inbound message)", target)
	}

	chunks := chunkText(text, maxTextLen)
	for _, chunk := range chunks {
		if err := sendText(ctx, a.client, adapterCfg, target, chunk, contextToken); err != nil {
			return adapter.DeliveryResult{}, fmt.Errorf("weixin: send text: %w", err)
		}
	}

	return adapter.DeliveryResult{
		Channel: a.id,
		ChatID:  target,
	}, nil
}

// ---------------------------------------------------------------------------
// SendMedia
// ---------------------------------------------------------------------------

func (a *WeixinAdapter) SendMedia(ctx context.Context, message adapter.OutboundMessage, cfg adapter.ChannelConfig) (adapter.DeliveryResult, error) {
	adapterCfg, err := parseAdapterConfig(cfg.Config)
	if err != nil {
		return adapter.DeliveryResult{}, err
	}

	target := strings.TrimSpace(message.To)
	if target == "" {
		return adapter.DeliveryResult{}, errors.New("weixin: outbound target (user ID) is required")
	}

	channelID := a.id
	a.mu.Lock()
	if strings.TrimSpace(a.runtimeID) != "" {
		channelID = a.runtimeID
	}
	a.mu.Unlock()
	cacheKey := channelID + ":" + target
	contextToken, ok := a.contextCache.Get(cacheKey)
	if !ok {
		return adapter.DeliveryResult{}, fmt.Errorf("weixin: no context_token cached for target %s", target)
	}

	text := strings.TrimSpace(message.Payload.Text)
	mediaURL := strings.TrimSpace(message.Payload.MediaURL)
	if mediaURL == "" && len(message.Payload.MediaURLs) > 0 {
		mediaURL = strings.TrimSpace(message.Payload.MediaURLs[0])
	}

	if mediaURL == "" {
		if text == "" {
			return adapter.DeliveryResult{}, nil
		}
		for _, chunk := range chunkText(text, maxTextLen) {
			if err := sendText(ctx, a.client, adapterCfg, target, chunk, contextToken); err != nil {
				return adapter.DeliveryResult{}, fmt.Errorf("weixin: send text: %w", err)
			}
		}
		return adapter.DeliveryResult{Channel: a.id, ChatID: target}, nil
	}

	data, mimeType, fileName, resolveErr := resolveWeixinMediaSource(ctx, a.client, mediaURL)
	if resolveErr != nil {
		slog.Warn("weixin: failed to resolve media, falling back to text",
			"mediaUrl", mediaURL, "error", resolveErr)
		fallbackContent := text
		if fallbackContent != "" {
			fallbackContent += "\n"
		}
		fallbackContent += mediaURL
		for _, chunk := range chunkText(fallbackContent, maxTextLen) {
			if err := sendText(ctx, a.client, adapterCfg, target, chunk, contextToken); err != nil {
				return adapter.DeliveryResult{}, fmt.Errorf("weixin: send media text fallback: %w", err)
			}
		}
		return adapter.DeliveryResult{Channel: a.id, ChatID: target}, nil
	}

	uploadType, itemType := classifyWeixinMedia(mimeType)
	if itemType == ItemTypeFile {
		if err := sendFileBytes(ctx, a.client, adapterCfg, target, contextToken, text, fileName, data); err != nil {
			return adapter.DeliveryResult{}, fmt.Errorf("weixin: send file bytes: %w", err)
		}
	} else {
		if err := sendMediaBytes(ctx, a.client, adapterCfg, target, contextToken, text, data, uploadType, itemType); err != nil {
			return adapter.DeliveryResult{}, fmt.Errorf("weixin: send media bytes: %w", err)
		}
	}

	return adapter.DeliveryResult{
		Channel: a.id,
		ChatID:  target,
	}, nil
}

// ---------------------------------------------------------------------------
// Audit helper
// ---------------------------------------------------------------------------

func (a *WeixinAdapter) emitAudit(ctx context.Context, entry adapter.AuditEntry) {
	a.mu.Lock()
	cb := a.callbacks
	a.mu.Unlock()
	if cb.OnAudit != nil {
		cb.OnAudit(ctx, entry)
	}
}

// ---------------------------------------------------------------------------
// Media Source Resolution
// ---------------------------------------------------------------------------

const weixinMediaMaxBytes = 20 * 1024 * 1024

func resolveWeixinMediaSource(ctx context.Context, _ *Client, raw string) ([]byte, string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, "", "", errors.New("weixin: media URL is empty")
	}

	if strings.HasPrefix(strings.ToLower(raw), "file://") {
		path := utils.FileURIToPath(raw)
		info, err := os.Stat(path)
		if err != nil {
			return nil, "", "", fmt.Errorf("weixin: media file not found: %w", err)
		}
		if info.IsDir() {
			return nil, "", "", errors.New("weixin: media path is a directory")
		}
		if info.Size() > weixinMediaMaxBytes {
			return nil, "", "", fmt.Errorf("weixin: media exceeds size limit (%d bytes)", info.Size())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, "", "", fmt.Errorf("weixin: read media file: %w", err)
		}
		mimeType := detectWeixinMimeType(path, data)
		return data, mimeType, filepath.Base(path), nil
	}

	if strings.HasPrefix(strings.ToLower(raw), "http://") || strings.HasPrefix(strings.ToLower(raw), "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
		if err != nil {
			return nil, "", "", err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, "", "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, "", "", fmt.Errorf("weixin: media download returned status %d", resp.StatusCode)
		}
		limited := make([]byte, 0, 512*1024)
		buf := make([]byte, 32*1024)
		total := 0
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				total += n
				if total > weixinMediaMaxBytes {
					return nil, "", "", fmt.Errorf("weixin: media exceeds size limit")
				}
				limited = append(limited, buf[:n]...)
			}
			if readErr != nil {
				break
			}
		}
		mimeType := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
		fileName := filepath.Base(strings.TrimSpace(resp.Request.URL.Path))
		if fileName == "" || fileName == "." || fileName == "/" {
			fileName = "attachment"
			if ext := extensionForWeixinMime(mimeType); ext != "" {
				fileName += ext
			}
		}
		return limited, mimeType, fileName, nil
	}

	return nil, "", "", fmt.Errorf("weixin: unsupported media URL scheme: %s", raw)
}

func detectWeixinMimeType(path string, content []byte) string {
	if ext := strings.TrimSpace(strings.ToLower(filepath.Ext(path))); ext != "" {
		if guessed := strings.TrimSpace(strings.Split(mime.TypeByExtension(ext), ";")[0]); guessed != "" {
			return guessed
		}
	}
	return strings.TrimSpace(strings.Split(http.DetectContentType(content), ";")[0])
}

func extensionForWeixinMime(mimeType string) string {
	if mimeType == "" {
		return ""
	}
	if exts, _ := mime.ExtensionsByType(mimeType); len(exts) > 0 {
		return exts[0]
	}
	return ""
}

func classifyWeixinMedia(mimeType string) (uploadType, itemType int) {
	lower := strings.ToLower(mimeType)
	switch {
	case strings.HasPrefix(lower, "image/"):
		return UploadMediaImage, ItemTypeImage
	case strings.HasPrefix(lower, "video/"):
		return UploadMediaVideo, ItemTypeVideo
	default:
		return UploadMediaFile, ItemTypeFile
	}
}

// ---------------------------------------------------------------------------
// Outbound Helpers
// ---------------------------------------------------------------------------

func sendText(ctx context.Context, client *Client, cfg adapterConfig, target, text, contextToken string) error {
	if strings.TrimSpace(contextToken) == "" {
		return errors.New("weixin: context_token is required to send messages")
	}
	clientID := generateClientID()
	req := SendMessageRequest{
		Msg: WeixinMessage{
			ToUserID:     target,
			ClientID:     clientID,
			MessageType:  MessageTypeBot,
			MessageState: MessageStateFinish,
			ItemList: []MessageItem{
				{Type: ItemTypeText, TextItem: &TextItem{Text: text}},
			},
			ContextToken: contextToken,
		},
	}
	return client.SendMessage(ctx, cfg, req)
}

func sendMediaBytes(ctx context.Context, client *Client, cfg adapterConfig, target, contextToken, text string, data []byte, uploadType, itemType int) error {
	if strings.TrimSpace(contextToken) == "" {
		return errors.New("weixin: context_token is required for media send")
	}

	aesKey := make([]byte, 16)
	if _, err := rand.Read(aesKey); err != nil {
		return fmt.Errorf("weixin: gen aes key: %w", err)
	}
	filekey := make([]byte, 16)
	if _, err := rand.Read(filekey); err != nil {
		return fmt.Errorf("weixin: gen filekey: %w", err)
	}
	filekeyHex := hex.EncodeToString(filekey)
	rawMD5 := md5.Sum(data) //nolint:gosec
	rawMD5Hex := hex.EncodeToString(rawMD5[:])
	fileSize := aesECBPaddedSize(len(data))
	mediaAESKey := encodeCDNMediaAESKey(aesKey)

	uploadResp, err := client.GetUploadURL(ctx, cfg, GetUploadURLRequest{
		FileKey:     filekeyHex,
		MediaType:   uploadType,
		ToUserID:    target,
		RawSize:     len(data),
		RawFileMD5:  rawMD5Hex,
		FileSize:    fileSize,
		NoNeedThumb: true,
		AESKey:      hex.EncodeToString(aesKey),
	})
	if err != nil {
		return fmt.Errorf("weixin: get upload url: %w", err)
	}
	if strings.TrimSpace(uploadResp.UploadParam) == "" {
		return errors.New("weixin: empty upload_param")
	}

	downloadParam, err := uploadToCDN(cfg.CDNBaseURL, uploadResp.UploadParam, filekeyHex, data, aesKey)
	if err != nil {
		return fmt.Errorf("weixin: cdn upload: %w", err)
	}

	var mediaItem MessageItem
	switch itemType {
	case ItemTypeImage:
		mediaItem = MessageItem{
			Type: ItemTypeImage,
			ImageItem: &ImageItem{
				Media: &CDNMedia{
					EncryptQueryParam: downloadParam,
					AESKey:            mediaAESKey,
					EncryptType:       1,
				},
				MidSize: fileSize,
			},
		}
	case ItemTypeVideo:
		mediaItem = MessageItem{
			Type: ItemTypeVideo,
			VideoItem: &VideoItem{
				Media: &CDNMedia{
					EncryptQueryParam: downloadParam,
					AESKey:            mediaAESKey,
					EncryptType:       1,
				},
				VideoSize: fileSize,
			},
		}
	default:
		return fmt.Errorf("weixin: unsupported media item type %d", itemType)
	}

	items := make([]MessageItem, 0, 2)
	if strings.TrimSpace(text) != "" {
		items = append(items, MessageItem{Type: ItemTypeText, TextItem: &TextItem{Text: text}})
	}
	items = append(items, mediaItem)

	for _, it := range items {
		req := SendMessageRequest{
			Msg: WeixinMessage{
				ToUserID:     target,
				ClientID:     generateClientID(),
				MessageType:  MessageTypeBot,
				MessageState: MessageStateFinish,
				ItemList:     []MessageItem{it},
				ContextToken: contextToken,
			},
		}
		if err := client.SendMessage(ctx, cfg, req); err != nil {
			return fmt.Errorf("weixin: send media item: %w", err)
		}
	}
	return nil
}

func sendFileBytes(ctx context.Context, client *Client, cfg adapterConfig, target, contextToken, text, fileName string, data []byte) error {
	if strings.TrimSpace(contextToken) == "" {
		return errors.New("weixin: context_token is required for file send")
	}

	aesKey := make([]byte, 16)
	if _, err := rand.Read(aesKey); err != nil {
		return fmt.Errorf("weixin: gen aes key: %w", err)
	}
	filekey := make([]byte, 16)
	if _, err := rand.Read(filekey); err != nil {
		return fmt.Errorf("weixin: gen filekey: %w", err)
	}
	filekeyHex := hex.EncodeToString(filekey)
	rawMD5 := md5.Sum(data) //nolint:gosec
	rawMD5Hex := hex.EncodeToString(rawMD5[:])
	fileSize := aesECBPaddedSize(len(data))
	mediaAESKey := encodeCDNMediaAESKey(aesKey)

	uploadResp, err := client.GetUploadURL(ctx, cfg, GetUploadURLRequest{
		FileKey:     filekeyHex,
		MediaType:   UploadMediaFile,
		ToUserID:    target,
		RawSize:     len(data),
		RawFileMD5:  rawMD5Hex,
		FileSize:    fileSize,
		NoNeedThumb: true,
		AESKey:      hex.EncodeToString(aesKey),
	})
	if err != nil {
		return fmt.Errorf("weixin: get upload url: %w", err)
	}
	if strings.TrimSpace(uploadResp.UploadParam) == "" {
		return errors.New("weixin: empty upload_param")
	}

	downloadParam, err := uploadToCDN(cfg.CDNBaseURL, uploadResp.UploadParam, filekeyHex, data, aesKey)
	if err != nil {
		return fmt.Errorf("weixin: cdn upload: %w", err)
	}

	fileItem := MessageItem{
		Type: ItemTypeFile,
		FileItem: &FileItem{
			Media: &CDNMedia{
				EncryptQueryParam: downloadParam,
				AESKey:            mediaAESKey,
				EncryptType:       1,
			},
			FileName: fileName,
			Len:      strconv.Itoa(len(data)),
		},
	}

	items := make([]MessageItem, 0, 2)
	if strings.TrimSpace(text) != "" {
		items = append(items, MessageItem{Type: ItemTypeText, TextItem: &TextItem{Text: text}})
	}
	items = append(items, fileItem)

	for _, it := range items {
		req := SendMessageRequest{
			Msg: WeixinMessage{
				ToUserID:     target,
				ClientID:     generateClientID(),
				MessageType:  MessageTypeBot,
				MessageState: MessageStateFinish,
				ItemList:     []MessageItem{it},
				ContextToken: contextToken,
			},
		}
		if err := client.SendMessage(ctx, cfg, req); err != nil {
			return fmt.Errorf("weixin: send file item: %w", err)
		}
	}
	return nil
}

func generateClientID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "kocort-weixin-" + hex.EncodeToString(b)
}

// ---------------------------------------------------------------------------
// Text Utilities
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Config Helpers
// ---------------------------------------------------------------------------

func readString(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := raw[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}

func readInt(raw map[string]any, keys ...string) (int, bool) {
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case int:
			return v, true
		case int32:
			return int(v), true
		case int64:
			return int(v), true
		case float64:
			return int(v), true
		case float32:
			return int(v), true
		case string:
			trimmed := strings.TrimSpace(v)
			if trimmed == "" {
				continue
			}
			parsed, err := strconv.Atoi(trimmed)
			if err != nil {
				continue
			}
			return parsed, true
		}
	}
	return 0, false
}

func readBool(raw map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case bool:
			return v, true
		case string:
			b, err := strconv.ParseBool(strings.TrimSpace(v))
			if err == nil {
				return b, true
			}
		}
	}
	return false, false
}

func normalizeID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
