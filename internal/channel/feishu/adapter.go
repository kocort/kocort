// Package feishu implements the Feishu/Lark channel adapter.
// It depends ONLY on the adapter package — no other project-internal packages.
package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

	"github.com/kocort/kocort/internal/channel/adapter"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/utils"
)

func init() {
	adapter.Register("feishu", func() adapter.ChannelAdapter {
		return NewFeishuAdapter()
	})
}

const (
	FeishuChannelID     = "feishu"
	feishuMediaMaxBytes = 30 << 20

	// feishuMsgIDDedupeWindow is the time window during which duplicate
	// inbound message IDs are suppressed.
	feishuMsgIDDedupeWindow = 10 * time.Minute
)

// FeishuAdapter implements adapter.ChannelAdapter for Feishu/Lark.
type FeishuAdapter struct {
	dc         *infra.DynamicHTTPClient
	now        func() time.Time
	mu         sync.Mutex
	channelID  string
	started    map[string]bool
	cancel     context.CancelFunc
	seenMsgIDs map[string]time.Time

	// Stored from StartBackground for use in event handlers.
	callbacks  adapter.Callbacks
	channelCfg adapter.ChannelConfig
}

type feishuResolvedAccount struct {
	ID        string
	AppID     string
	AppSecret string
	Domain    string
	Enabled   bool
}

type feishuMediaSource struct {
	Content  []byte
	FileName string
	MIMEType string
	IsImage  bool
	Path     string
}

// NewFeishuAdapter returns a new FeishuAdapter.
func NewFeishuAdapter() *FeishuAdapter {
	return &FeishuAdapter{
		now:        time.Now,
		started:    map[string]bool{},
		seenMsgIDs: map[string]time.Time{},
	}
}

// ---------------------------------------------------------------------------
// adapter.ChannelAdapter interface
// ---------------------------------------------------------------------------

func (a *FeishuAdapter) ID() string { return FeishuChannelID }

func (a *FeishuAdapter) Schema() adapter.ChannelDriverSchema {
	return adapter.ChannelDriverSchema{
		ID:          FeishuChannelID,
		Name:        "Feishu/Lark",
		Description: "Connect to Feishu (飞书) or Lark via App Credentials",
		Fields: []adapter.ChannelConfigField{
			{
				Key:          "domain",
				Label:        "Domain",
				Type:         adapter.FieldTypeSelect,
				Required:     true,
				DefaultValue: "feishu",
				Help:         "Feishu (China) or Lark (International)",
				Options: []adapter.SelectOption{
					{Value: "feishu", Label: "Feishu (飞书)"},
					{Value: "lark", Label: "Lark (International)"},
				},
			},
			{
				Key:         "appId",
				Label:       "App ID",
				Type:        adapter.FieldTypeText,
				Required:    true,
				Placeholder: "cli_a1234567890abcde",
				Help:        "App ID from Feishu Developer Console",
				Group:       "account",
			},
			{
				Key:         "appSecret",
				Label:       "App Secret",
				Type:        adapter.FieldTypePassword,
				Required:    true,
				Placeholder: "App Secret",
				Help:        "App Secret from Feishu Developer Console",
				Group:       "account",
			},
			{
				Key:          "defaultAccount",
				Label:        "Default Account",
				Type:         adapter.FieldTypeText,
				Required:     false,
				DefaultValue: "main",
				Placeholder:  "main",
				Help:         "Default account name for multi-account setup",
			},
		},
	}
}

func (a *FeishuAdapter) StartBackground(ctx context.Context, channelID string, cfg adapter.ChannelConfig, dc *infra.DynamicHTTPClient, cb adapter.Callbacks) error {
	a.mu.Lock()
	a.callbacks = cb
	a.channelCfg = cfg
	a.dc = dc
	a.channelID = normalizeID(channelID)
	if a.cancel == nil {
		var loopCtx context.Context
		loopCtx, a.cancel = context.WithCancel(ctx)
		ctx = loopCtx
	}
	a.mu.Unlock()

	normalizedChannelID := normalizeID(channelID)
	for accountID, account := range feishuAccountsFromChannel(cfg) {
		if !account.Enabled {
			continue
		}
		key := normalizedChannelID + ":" + strings.TrimSpace(accountID)
		a.mu.Lock()
		if a.started[key] {
			a.mu.Unlock()
			continue
		}
		a.started[key] = true
		a.mu.Unlock()

		handler := a.newFeishuEventDispatcher(strings.TrimSpace(accountID))
		wsClient := larkws.NewClient(
			account.AppID,
			account.AppSecret,
			larkws.WithEventHandler(handler),
			larkws.WithDomain(account.Domain),
		)
		go func(acctID string, cli *larkws.Client) {
			if err := cli.Start(ctx); err != nil {
				a.emitAudit(ctx, adapter.AuditEntry{
					Category: "channel",
					Type:     "feishu_websocket_stopped",
					Level:    "error",
					Channel:  FeishuChannelID,
					Message:  "feishu websocket stopped",
					Data: map[string]any{
						"channel":   normalizedChannelID,
						"accountId": acctID,
						"error":     err.Error(),
					},
				})
			}
		}(accountID, wsClient)

		a.emitAudit(ctx, adapter.AuditEntry{
			Category: "channel",
			Type:     "feishu_websocket_started",
			Level:    "info",
			Channel:  FeishuChannelID,
			Message:  "feishu websocket started",
			Data: map[string]any{
				"channel":   normalizedChannelID,
				"accountId": accountID,
			},
		})
	}
	return nil
}

func (a *FeishuAdapter) StopBackground() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	a.started = map[string]bool{}
}

func (a *FeishuAdapter) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "feishu does not support HTTP ingress", http.StatusNotImplemented)
}

func (a *FeishuAdapter) SendText(ctx context.Context, message adapter.OutboundMessage, cfg adapter.ChannelConfig) (adapter.DeliveryResult, error) {
	account, err := resolveFeishuAccount(cfg, strings.TrimSpace(message.AccountID))
	if err != nil {
		return adapter.DeliveryResult{}, err
	}
	targetType, target := NormalizeFeishuTarget(message.To)
	if target == "" {
		return adapter.DeliveryResult{}, fmt.Errorf("feishu outbound target is required")
	}
	text := strings.TrimSpace(message.Payload.Text)
	if text == "" {
		return adapter.DeliveryResult{}, nil
	}
	return a.sendFeishuText(ctx, account, targetType, target, text, strings.TrimSpace(message.ReplyToID))
}

func (a *FeishuAdapter) SendMedia(ctx context.Context, message adapter.OutboundMessage, cfg adapter.ChannelConfig) (adapter.DeliveryResult, error) {
	account, err := resolveFeishuAccount(cfg, strings.TrimSpace(message.AccountID))
	if err != nil {
		return adapter.DeliveryResult{}, err
	}
	targetType, target := NormalizeFeishuTarget(message.To)
	if target == "" {
		return adapter.DeliveryResult{}, fmt.Errorf("feishu outbound target is required")
	}

	media := strings.TrimSpace(message.Payload.MediaURL)
	if media == "" && len(message.Payload.MediaURLs) > 0 {
		media = strings.TrimSpace(message.Payload.MediaURLs[0])
	}

	var mediaResult adapter.DeliveryResult
	if media != "" {
		source, sourceErr := a.resolveMediaSource(ctx, media)
		if sourceErr != nil {
			return adapter.DeliveryResult{}, sourceErr
		}
		mediaResult, err = a.sendFeishuMedia(ctx, account, targetType, target, strings.TrimSpace(message.ReplyToID), source)
		if err != nil {
			return adapter.DeliveryResult{}, err
		}
	}

	if text := strings.TrimSpace(message.Payload.Text); text != "" {
		textResult, textErr := a.sendFeishuText(ctx, account, targetType, target, text, strings.TrimSpace(message.ReplyToID))
		if textErr != nil {
			return adapter.DeliveryResult{}, textErr
		}
		if media == "" {
			return textResult, nil
		}
	}

	return mediaResult, nil
}

// ---------------------------------------------------------------------------
// Audit helper
// ---------------------------------------------------------------------------

func (a *FeishuAdapter) emitAudit(ctx context.Context, entry adapter.AuditEntry) {
	a.mu.Lock()
	cb := a.callbacks
	a.mu.Unlock()
	if cb.OnAudit != nil {
		cb.OnAudit(ctx, entry)
	}
}

// ---------------------------------------------------------------------------
// Inbound event dispatcher
// ---------------------------------------------------------------------------

func (a *FeishuAdapter) newFeishuEventDispatcher(accountID string) *dispatcher.EventDispatcher {
	return dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			receivedAt := time.Now()

			if event == nil || event.Event == nil || event.Event.Message == nil {
				a.emitAudit(ctx, adapter.AuditEntry{
					Category: "channel",
					Type:     "feishu_inbound_nil_event",
					Level:    "warn",
					Channel:  FeishuChannelID,
					Message:  "feishu received nil or incomplete event payload",
					Data:     map[string]any{"accountId": accountID},
				})
				return nil
			}

			msgID := strings.TrimSpace(LarkString(event.Event.Message.MessageId))
			msgType := strings.TrimSpace(LarkString(event.Event.Message.MessageType))
			chatType := strings.TrimSpace(LarkString(event.Event.Message.ChatType))
			chatID := strings.TrimSpace(LarkString(event.Event.Message.ChatId))
			senderID := ""
			if event.Event.Sender != nil && event.Event.Sender.SenderId != nil {
				senderID = strings.TrimSpace(LarkString(event.Event.Sender.SenderId.OpenId))
			}

			a.emitAudit(ctx, adapter.AuditEntry{
				Category: "channel",
				Type:     "feishu_inbound_raw",
				Level:    "info",
				Channel:  FeishuChannelID,
				Message:  "feishu inbound event received from SDK",
				Data: map[string]any{
					"accountId":        accountID,
					"messageId":        msgID,
					"messageType":      msgType,
					"chatType":         chatType,
					"chatId":           chatID,
					"senderId":         senderID,
					"receivedAtUnixMs": receivedAt.UnixMilli(),
					"receivedAt":       receivedAt.UTC().Format(time.RFC3339Nano),
				},
			})

			// Deduplicate by Feishu message_id.
			if msgID != "" {
				now := time.Now()
				a.mu.Lock()
				if firstSeen, seen := a.seenMsgIDs[msgID]; seen && now.Sub(firstSeen) < feishuMsgIDDedupeWindow {
					elapsedMs := now.Sub(firstSeen).Milliseconds()
					a.mu.Unlock()
					a.emitAudit(ctx, adapter.AuditEntry{
						Category: "channel",
						Type:     "feishu_inbound_duplicate",
						Level:    "warn",
						Channel:  FeishuChannelID,
						Message:  "feishu duplicate event suppressed by dedup cache",
						Data: map[string]any{
							"accountId":           accountID,
							"messageId":           msgID,
							"messageType":         msgType,
							"chatType":            chatType,
							"chatId":              chatID,
							"senderId":            senderID,
							"firstSeenAt":         firstSeen.UTC().Format(time.RFC3339Nano),
							"firstSeenUnixMs":     firstSeen.UnixMilli(),
							"receivedAt":          receivedAt.UTC().Format(time.RFC3339Nano),
							"receivedUnixMs":      receivedAt.UnixMilli(),
							"elapsedSinceFirstMs": elapsedMs,
						},
					})
					return nil
				}
				a.seenMsgIDs[msgID] = now
				for id, t := range a.seenMsgIDs {
					if now.Sub(t) >= feishuMsgIDDedupeWindow {
						delete(a.seenMsgIDs, id)
					}
				}
				a.mu.Unlock()
			}

			a.mu.Lock()
			cfg := a.channelCfg
			cb := a.callbacks
			a.mu.Unlock()

			account, err := resolveFeishuAccount(cfg, accountID)
			if err != nil {
				a.emitAudit(ctx, adapter.AuditEntry{
					Category: "channel",
					Type:     "feishu_inbound_account_error",
					Level:    "error",
					Channel:  FeishuChannelID,
					Message:  "feishu failed to resolve account",
					Data: map[string]any{
						"accountId": accountID,
						"messageId": msgID,
						"error":     err.Error(),
					},
				})
				return err
			}

			msg, err := a.feishuInboundMessageFromSDKEvent(ctx, account, accountID, event, cfg)
			if err != nil {
				a.emitAudit(ctx, adapter.AuditEntry{
					Category: "channel",
					Type:     "feishu_inbound_parse_error",
					Level:    "error",
					Channel:  FeishuChannelID,
					Message:  "feishu failed to parse inbound event",
					Data: map[string]any{
						"accountId": accountID,
						"messageId": msgID,
						"error":     err.Error(),
					},
				})
				return err
			}

			parsedMs := time.Since(receivedAt).Milliseconds()

			slog.Debug("[feishu] event parsed into inbound message", "msg", fmt.Sprintf("%+v", msg))
			if reactErr := a.sendFeishuAckReaction(ctx, account, LarkString(event.Event.Message.MessageId), cfg); reactErr != nil {
				a.emitAudit(ctx, adapter.AuditEntry{
					Category: "channel",
					Type:     "feishu_ack_reaction_failed",
					Level:    "warn",
					Channel:  FeishuChannelID,
					Message:  "feishu ack reaction failed",
					Data: map[string]any{
						"accountId": accountID,
						"messageId": msg.MessageID,
						"error":     reactErr.Error(),
					},
				})
			}

			a.emitAudit(ctx, adapter.AuditEntry{
				Category: "channel",
				Type:     "feishu_inbound_dispatched",
				Level:    "info",
				Channel:  FeishuChannelID,
				Message:  "feishu inbound message dispatched to agent pipeline",
				Data: map[string]any{
					"accountId":       accountID,
					"messageId":       msg.MessageID,
					"from":            msg.From,
					"to":              msg.To,
					"threadId":        msg.ThreadID,
					"chatType":        msg.ChatType,
					"textChars":       len([]rune(strings.TrimSpace(msg.Text))),
					"attachmentCount": len(msg.Attachments),
					"receivedAt":      receivedAt.UTC().Format(time.RFC3339Nano),
					"parsedMs":        parsedMs,
				},
			})

			go func(pushMsg adapter.InboundMessage, dispatchedAt time.Time) {
				pipelineStartAt := time.Now()
				queuedMs := pipelineStartAt.Sub(dispatchedAt).Milliseconds()

				a.emitAudit(context.Background(), adapter.AuditEntry{
					Category: "channel",
					Type:     "feishu_inbound_pipeline_start",
					Level:    "info",
					Channel:  FeishuChannelID,
					Message:  "feishu inbound pipeline started",
					Data: map[string]any{
						"accountId":  accountID,
						"messageId":  pushMsg.MessageID,
						"receivedAt": receivedAt.UTC().Format(time.RFC3339Nano),
						"queuedMs":   queuedMs,
					},
				})

				pushErr := cb.OnMessage(context.Background(), pushMsg)
				elapsedMs := time.Since(pipelineStartAt).Milliseconds()
				if pushErr != nil {
					a.emitAudit(context.Background(), adapter.AuditEntry{
						Category: "channel",
						Type:     "feishu_inbound_push_failed",
						Level:    "error",
						Channel:  FeishuChannelID,
						Message:  "feishu inbound push failed",
						Data: map[string]any{
							"accountId": accountID,
							"messageId": pushMsg.MessageID,
							"elapsedMs": elapsedMs,
							"error":     pushErr.Error(),
						},
					})
				} else {
					a.emitAudit(context.Background(), adapter.AuditEntry{
						Category: "channel",
						Type:     "feishu_inbound_push_success",
						Level:    "info",
						Channel:  FeishuChannelID,
						Message:  "feishu inbound push succeeded",
						Data: map[string]any{
							"accountId": accountID,
							"messageId": pushMsg.MessageID,
							"elapsedMs": elapsedMs,
						},
					})
				}
			}(msg, time.Now())
			return nil
		})
}

// ---------------------------------------------------------------------------
// Outbound: text
// ---------------------------------------------------------------------------

func (a *FeishuAdapter) sendFeishuText(ctx context.Context, account feishuResolvedAccount, targetType, target, text, replyToID string) (adapter.DeliveryResult, error) {
	client := a.newFeishuClient(account)
	if LooksLikeFeishuMarkdown(text) {
		content, err := BuildFeishuMarkdownPost(text)
		if err != nil {
			return adapter.DeliveryResult{}, err
		}
		return a.sendFeishuMessage(ctx, client, targetType, target, replyToID, "post", content, account.ID)
	}
	contentBytes, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return adapter.DeliveryResult{}, err
	}
	return a.sendFeishuMessage(ctx, client, targetType, target, replyToID, "text", string(contentBytes), account.ID)
}

// ---------------------------------------------------------------------------
// Outbound: media
// ---------------------------------------------------------------------------

func (a *FeishuAdapter) sendFeishuMedia(ctx context.Context, account feishuResolvedAccount, targetType, target, replyToID string, source feishuMediaSource) (adapter.DeliveryResult, error) {
	client := a.newFeishuClient(account)
	if source.IsImage {
		uploadResp, err := client.Im.Image.Create(ctx, larkim.NewCreateImageReqBuilder().
			Body(larkim.NewCreateImageReqBodyBuilder().
				ImageType(larkim.ImageTypeMessage).
				Image(bytes.NewReader(source.Content)).
				Build()).
			Build())
		if err != nil {
			return adapter.DeliveryResult{}, err
		}
		if uploadResp == nil || !uploadResp.Success() || uploadResp.Data == nil || uploadResp.Data.ImageKey == nil {
			return adapter.DeliveryResult{}, fmt.Errorf("feishu image upload failed: code=%d msg=%s", safeCodeError(uploadResp), safeCodeMessage(uploadResp))
		}
		content, err := json.Marshal(map[string]string{"image_key": strings.TrimSpace(*uploadResp.Data.ImageKey)})
		if err != nil {
			return adapter.DeliveryResult{}, err
		}
		return a.sendFeishuMessage(ctx, client, targetType, target, replyToID, "image", string(content), account.ID)
	}
	uploadResp, err := client.Im.File.Create(ctx, larkim.NewCreateFileReqBuilder().
		Body(larkim.NewCreateFileReqBodyBuilder().
			FileType(InferFeishuFileType(source.FileName, source.MIMEType)).
			FileName(source.FileName).
			File(bytes.NewReader(source.Content)).
			Build()).
		Build())
	if err != nil {
		return adapter.DeliveryResult{}, err
	}
	if uploadResp == nil || !uploadResp.Success() || uploadResp.Data == nil || uploadResp.Data.FileKey == nil {
		return adapter.DeliveryResult{}, fmt.Errorf("feishu file upload failed: code=%d msg=%s", safeCodeError(uploadResp), safeCodeMessage(uploadResp))
	}
	content, err := json.Marshal(map[string]string{"file_key": strings.TrimSpace(*uploadResp.Data.FileKey)})
	if err != nil {
		return adapter.DeliveryResult{}, err
	}
	return a.sendFeishuMessage(ctx, client, targetType, target, replyToID, "file", string(content), account.ID)
}

// ---------------------------------------------------------------------------
// Outbound: low-level Feishu API
// ---------------------------------------------------------------------------

func (a *FeishuAdapter) sendFeishuMessage(ctx context.Context, client *lark.Client, targetType, target, replyToID, msgType, content, accountID string) (adapter.DeliveryResult, error) {
	if strings.TrimSpace(replyToID) != "" {
		resp, err := client.Im.Message.Reply(ctx, larkim.NewReplyMessageReqBuilder().
			MessageId(strings.TrimSpace(replyToID)).
			Body(larkim.NewReplyMessageReqBodyBuilder().
				MsgType(msgType).
				Content(content).
				Build()).
			Build())
		if err != nil {
			return adapter.DeliveryResult{}, fmt.Errorf("feishu reply send failed: account=%s targetType=%s target=%s replyTo=%s msgType=%s: %w", accountID, targetType, target, strings.TrimSpace(replyToID), msgType, err)
		}
		if resp == nil || !resp.Success() {
			return adapter.DeliveryResult{}, fmt.Errorf("feishu reply send failed: account=%s targetType=%s target=%s replyTo=%s msgType=%s code=%d msg=%s", accountID, targetType, target, strings.TrimSpace(replyToID), msgType, safeCodeError(resp), safeCodeMessage(resp))
		}
		var messageID string
		if resp.Data != nil {
			messageID = LarkString(resp.Data.MessageId)
		}
		return adapter.DeliveryResult{
			Channel:   FeishuChannelID,
			MessageID: messageID,
			ChatID:    target,
			Meta: map[string]any{
				"accountId":  accountID,
				"targetType": targetType,
				"sendMode":   "reply",
				"replyToId":  strings.TrimSpace(replyToID),
			},
		}, nil
	}
	resp, err := client.Im.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(targetType).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(target).
			MsgType(msgType).
			Content(content).
			Build()).
		Build())
	if err != nil {
		return adapter.DeliveryResult{}, fmt.Errorf("feishu create send failed: account=%s targetType=%s target=%s msgType=%s: %w", accountID, targetType, target, msgType, err)
	}
	if resp == nil || !resp.Success() {
		return adapter.DeliveryResult{}, fmt.Errorf("feishu create send failed: account=%s targetType=%s target=%s msgType=%s code=%d msg=%s", accountID, targetType, target, msgType, safeCodeError(resp), safeCodeMessage(resp))
	}
	var messageID, chatIDResp string
	if resp.Data != nil {
		messageID = LarkString(resp.Data.MessageId)
		chatIDResp = LarkString(resp.Data.ChatId)
	}
	return adapter.DeliveryResult{
		Channel:   FeishuChannelID,
		MessageID: messageID,
		ChatID:    nonEmpty(chatIDResp, target),
		Meta: map[string]any{
			"accountId":  accountID,
			"targetType": targetType,
			"sendMode":   "create",
		},
	}, nil
}

func (a *FeishuAdapter) sendFeishuAckReaction(ctx context.Context, account feishuResolvedAccount, messageID string, cfg adapter.ChannelConfig) error {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return nil
	}
	emojiType := ResolveFeishuAckEmojiType(cfg.Config)
	if emojiType == "" {
		return nil
	}
	client := a.newFeishuClient(account)
	resp, err := client.Im.MessageReaction.Create(ctx, larkim.NewCreateMessageReactionReqBuilder().
		MessageId(messageID).
		Body(larkim.NewCreateMessageReactionReqBodyBuilder().
			ReactionType(larkim.NewEmojiBuilder().EmojiType(emojiType).Build()).
			Build()).
		Build())
	if err != nil {
		return err
	}
	if resp == nil || !resp.Success() {
		return fmt.Errorf("feishu ack reaction failed: messageId=%s emoji=%s code=%d msg=%s", messageID, emojiType, resp.Code, strings.TrimSpace(resp.Msg))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Media download/resolution
// ---------------------------------------------------------------------------

func (a *FeishuAdapter) resolveMediaSource(ctx context.Context, input string) (feishuMediaSource, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return feishuMediaSource{}, fmt.Errorf("feishu media input is empty")
	}
	if content, mimeType, fileName, err := DecodeFeishuDataURL(raw); err == nil {
		if len(content) > feishuMediaMaxBytes {
			return feishuMediaSource{}, fmt.Errorf("feishu media exceeds size limit")
		}
		return feishuMediaSource{
			Content:  content,
			FileName: fileName,
			MIMEType: mimeType,
			IsImage:  strings.HasPrefix(mimeType, "image/"),
		}, nil
	}
	if strings.HasPrefix(strings.ToLower(raw), "http://") || strings.HasPrefix(strings.ToLower(raw), "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
		if err != nil {
			return feishuMediaSource{}, err
		}
		res, err := a.dc.Do(req)
		if err != nil {
			return feishuMediaSource{}, err
		}
		defer res.Body.Close()
		content, err := io.ReadAll(io.LimitReader(res.Body, int64(feishuMediaMaxBytes)+1))
		if err != nil {
			return feishuMediaSource{}, err
		}
		if len(content) > feishuMediaMaxBytes {
			return feishuMediaSource{}, fmt.Errorf("feishu media exceeds size limit")
		}
		mimeType := strings.TrimSpace(strings.Split(res.Header.Get("Content-Type"), ";")[0])
		fileName := filepath.Base(strings.TrimSpace(res.Request.URL.Path))
		if fileName == "" || fileName == "." || fileName == "/" {
			fileName = "attachment" + ExtensionForMimeType(mimeType)
		}
		return feishuMediaSource{
			Content:  content,
			FileName: fileName,
			MIMEType: mimeType,
			IsImage:  strings.HasPrefix(mimeType, "image/"),
		}, nil
	}
	var path string
	if strings.HasPrefix(strings.ToLower(raw), "file://") {
		path = utils.FileURIToPath(raw)
	} else {
		path = raw
	}
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		content, err := os.ReadFile(path)
		if err != nil {
			return feishuMediaSource{}, err
		}
		if len(content) > feishuMediaMaxBytes {
			return feishuMediaSource{}, fmt.Errorf("feishu media exceeds size limit")
		}
		mimeType := DetectMediaMimeType(path, content)
		return feishuMediaSource{
			Content:  content,
			FileName: filepath.Base(path),
			MIMEType: mimeType,
			IsImage:  strings.HasPrefix(mimeType, "image/"),
		}, nil
	}
	return feishuMediaSource{}, fmt.Errorf("feishu media path/url not found: %s", raw)
}

// ---------------------------------------------------------------------------
// Inbound parsing
// ---------------------------------------------------------------------------

func (a *FeishuAdapter) feishuInboundMessageFromSDKEvent(ctx context.Context, account feishuResolvedAccount, accountID string, event *larkim.P2MessageReceiveV1, cfg adapter.ChannelConfig) (adapter.InboundMessage, error) {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return adapter.InboundMessage{}, fmt.Errorf("feishu event is missing message payload")
	}
	message := event.Event.Message
	messageType := strings.TrimSpace(strings.ToLower(LarkString(message.MessageType)))
	text := strings.TrimSpace(ResolveFeishuInboundText(messageType, LarkString(message.Content)))
	if text == "" {
		text = FeishuPlaceholderText(messageType)
	}
	from := resolveFeishuSenderOpenID(event.Event.Sender)
	chatID := LarkString(message.ChatId)
	replyTarget, chatType := ResolveFeishuReplyTarget(strings.TrimSpace(strings.ToLower(LarkString(message.ChatType))), from, chatID)
	threadID := FirstNonEmptyTrimmed(
		LarkString(message.ThreadId),
		LarkString(message.RootId),
		LarkString(message.ParentId),
	)
	raw := map[string]any{}
	if payload, err := json.Marshal(event); err == nil {
		_ = json.Unmarshal(payload, &raw)
	}
	attachments, err := a.resolveFeishuInboundAttachments(ctx, account, cfg, feishuInboundMediaRequest{
		MessageID:   LarkString(message.MessageId),
		MessageType: messageType,
		Content:     LarkString(message.Content),
	})
	if err != nil {
		return adapter.InboundMessage{}, err
	}
	return adapter.InboundMessage{
		Channel:     a.inboundChannelID(),
		AccountID:   strings.TrimSpace(accountID),
		From:        from,
		To:          replyTarget,
		ThreadID:    threadID,
		ChatType:    chatType,
		Text:        text,
		Attachments: attachments,
		AgentID:     strings.TrimSpace(cfg.Agent),
		MessageID:   LarkString(message.MessageId),
		Raw:         raw,
	}, nil
}

func (a *FeishuAdapter) inboundChannelID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.channelID != "" {
		return a.channelID
	}
	return FeishuChannelID
}

type feishuInboundMediaRequest struct {
	MessageID   string
	MessageType string
	Content     string
}

func (a *FeishuAdapter) resolveFeishuInboundAttachments(ctx context.Context, account feishuResolvedAccount, cfg adapter.ChannelConfig, req feishuInboundMediaRequest) ([]adapter.Attachment, error) {
	messageID := strings.TrimSpace(req.MessageID)
	messageType := strings.TrimSpace(strings.ToLower(req.MessageType))
	if messageID == "" || messageType == "" {
		return nil, nil
	}
	maxBytes := FeishuMediaMaxBytesForChannel(cfg.Config, feishuMediaMaxBytes)
	client := a.newFeishuClient(account)
	if messageType == "post" {
		mediaKeys := ParseFeishuPostMedia(req.Content)
		attachments := make([]adapter.Attachment, 0, len(mediaKeys))
		for _, media := range mediaKeys {
			attachment, err := a.downloadFeishuMessageResource(ctx, client, messageID, media.FileKey, media.ResourceType, media.FileName, maxBytes)
			if err != nil {
				continue
			}
			attachments = append(attachments, attachment)
		}
		return attachments, nil
	}
	parsed := ParseFeishuMessageMedia(req.Content, messageType)
	resourceKey := strings.TrimSpace(parsed.FileKey)
	resourceType := "file"
	if resourceKey == "" {
		resourceKey = strings.TrimSpace(parsed.ImageKey)
		resourceType = "image"
	}
	switch messageType {
	case "image":
		resourceType = "image"
	case "file", "audio", "video", "media", "sticker":
		resourceType = "file"
	}
	if resourceKey == "" {
		return nil, nil
	}
	attachment, err := a.downloadFeishuMessageResource(ctx, client, messageID, resourceKey, resourceType, parsed.FileName, maxBytes)
	if err != nil {
		return nil, nil
	}
	return []adapter.Attachment{attachment}, nil
}

func (a *FeishuAdapter) downloadFeishuMessageResource(ctx context.Context, client *lark.Client, messageID, fileKey, resourceType, suggestedName string, maxBytes int) (adapter.Attachment, error) {
	resp, err := client.Im.MessageResource.Get(ctx, larkim.NewGetMessageResourceReqBuilder().
		MessageId(messageID).
		FileKey(fileKey).
		Type(resourceType).
		Build())
	if err != nil {
		return adapter.Attachment{}, err
	}
	if resp == nil {
		return adapter.Attachment{}, fmt.Errorf("empty feishu message resource response")
	}
	if resp.ApiResp != nil && resp.ApiResp.StatusCode != http.StatusOK {
		return adapter.Attachment{}, fmt.Errorf("feishu message resource download failed: code=%d msg=%s", resp.Code, strings.TrimSpace(resp.Msg))
	}
	content, err := io.ReadAll(io.LimitReader(resp.File, int64(maxBytes)+1))
	if err != nil {
		return adapter.Attachment{}, err
	}
	if len(content) > maxBytes {
		return adapter.Attachment{}, fmt.Errorf("feishu message resource exceeds size limit")
	}
	mimeType := ""
	if resp.ApiResp != nil {
		mimeType = strings.TrimSpace(strings.Split(resp.ApiResp.Header.Get("Content-Type"), ";")[0])
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(content)
	}
	fileName := strings.TrimSpace(resp.FileName)
	if fileName == "" {
		fileName = strings.TrimSpace(suggestedName)
	}
	if fileName == "" {
		fileName = resourceType + "-" + fileKey + ExtensionForMimeType(mimeType)
	}
	return adapter.Attachment{
		Name:     fileName,
		MIMEType: strings.TrimSpace(strings.Split(mimeType, ";")[0]),
		Content:  content,
	}, nil
}

// ---------------------------------------------------------------------------
// Lark client factory
// ---------------------------------------------------------------------------

func (a *FeishuAdapter) newFeishuClient(account feishuResolvedAccount) *lark.Client {
	return lark.NewClient(
		account.AppID,
		account.AppSecret,
		lark.WithHttpClient(a.dc.Client()),
		lark.WithOpenBaseUrl(account.Domain),
	)
}

// ---------------------------------------------------------------------------
// Account resolution
// ---------------------------------------------------------------------------

func resolveFeishuSenderOpenID(sender *larkim.EventSender) string {
	if sender == nil || sender.SenderId == nil {
		return ""
	}
	return FirstNonEmptyTrimmed(
		LarkString(sender.SenderId.OpenId),
		LarkString(sender.SenderId.UserId),
		LarkString(sender.SenderId.UnionId),
	)
}

func safeCodeError(resp any) int {
	switch typed := resp.(type) {
	case *larkim.CreateImageResp:
		if typed == nil {
			return -1
		}
		return typed.Code
	case *larkim.CreateFileResp:
		if typed == nil {
			return -1
		}
		return typed.Code
	case *larkim.CreateMessageResp:
		if typed == nil {
			return -1
		}
		return typed.Code
	case *larkim.ReplyMessageResp:
		if typed == nil {
			return -1
		}
		return typed.Code
	default:
		return -1
	}
}

func safeCodeMessage(resp any) string {
	switch typed := resp.(type) {
	case *larkim.CreateImageResp:
		if typed == nil {
			return "empty response"
		}
		return strings.TrimSpace(typed.Msg)
	case *larkim.CreateFileResp:
		if typed == nil {
			return "empty response"
		}
		return strings.TrimSpace(typed.Msg)
	case *larkim.CreateMessageResp:
		if typed == nil {
			return "empty response"
		}
		return strings.TrimSpace(typed.Msg)
	case *larkim.ReplyMessageResp:
		if typed == nil {
			return "empty response"
		}
		return strings.TrimSpace(typed.Msg)
	default:
		return "unknown"
	}
}

func resolveFeishuAccount(cfg adapter.ChannelConfig, requestedAccountID string) (feishuResolvedAccount, error) {
	account, _, err := resolveFeishuAccountWithID(cfg, requestedAccountID)
	return account, err
}

func resolveFeishuAccountWithID(cfg adapter.ChannelConfig, requestedAccountID string) (feishuResolvedAccount, string, error) {
	accounts := feishuAccountsFromChannel(cfg)
	accountID := strings.TrimSpace(requestedAccountID)
	if accountID == "" {
		accountID = strings.TrimSpace(cfg.DefaultAccount)
	}
	if accountID != "" {
		account, ok := accounts[accountID]
		if !ok {
			return feishuResolvedAccount{}, "", fmt.Errorf("feishu account %q is not configured", accountID)
		}
		if !account.Enabled {
			return feishuResolvedAccount{}, "", fmt.Errorf("feishu account %q is disabled", accountID)
		}
		return account, accountID, nil
	}
	if len(accounts) == 1 {
		for id, account := range accounts {
			if account.Enabled {
				return account, id, nil
			}
		}
	}
	if account, ok := accounts["default"]; ok && account.Enabled {
		return account, "default", nil
	}
	for id, account := range accounts {
		if account.Enabled {
			return account, id, nil
		}
	}
	return feishuResolvedAccount{}, "", fmt.Errorf("feishu credentials are not configured")
}

func feishuAccountsFromChannel(cfg adapter.ChannelConfig) map[string]feishuResolvedAccount {
	accounts := map[string]feishuResolvedAccount{}
	base := feishuAccountFromMap("default", cfg.Config)
	if base.Enabled && strings.TrimSpace(base.AppID) != "" && strings.TrimSpace(base.AppSecret) != "" {
		accounts[base.ID] = base
	}
	for id, raw := range cfg.Accounts {
		entryMap, _ := raw.(map[string]any)
		account := mergeFeishuAccount(base, id, entryMap)
		if account.Enabled && strings.TrimSpace(account.AppID) != "" && strings.TrimSpace(account.AppSecret) != "" {
			accounts[id] = account
		}
	}
	return accounts
}

func feishuAccountFromMap(id string, raw map[string]any) feishuResolvedAccount {
	account := feishuResolvedAccount{
		ID:        id,
		AppID:     strings.TrimSpace(StringFromMap(raw, "appId")),
		AppSecret: strings.TrimSpace(StringFromMap(raw, "appSecret")),
		Domain:    NormalizeFeishuDomain(StringFromMap(raw, "domain")),
		Enabled:   true,
	}
	if enabled, ok := BoolFromMap(raw, "enabled"); ok {
		account.Enabled = enabled
	}
	if account.Domain == "" {
		account.Domain = FeishuDefaultDomain
	}
	return account
}

func mergeFeishuAccount(base feishuResolvedAccount, id string, raw map[string]any) feishuResolvedAccount {
	account := base
	account.ID = id
	if value := strings.TrimSpace(StringFromMap(raw, "appId")); value != "" {
		account.AppID = value
	}
	if value := strings.TrimSpace(StringFromMap(raw, "appSecret")); value != "" {
		account.AppSecret = value
	}
	if value := NormalizeFeishuDomain(StringFromMap(raw, "domain")); value != "" {
		account.Domain = value
	}
	if enabled, ok := BoolFromMap(raw, "enabled"); ok {
		account.Enabled = enabled
	}
	return account
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

func normalizeID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

func nonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
