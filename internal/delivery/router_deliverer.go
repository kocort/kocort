package delivery

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/session"
)

// ---------------------------------------------------------------------------
// Dependency interfaces — decouple from runtime-only types
// ---------------------------------------------------------------------------

// EventNotifier abstracts the EventHub for recording replies and emitting events.
type EventNotifier interface {
	Record(target core.DeliveryTarget, kind core.ReplyKind, payload core.ReplyPayload)
	EmitAgentEvent(sessionKey string, event core.AgentEvent)
}

// ChannelOutboundResolver abstracts channel registry outbound lookup.
// Outbound returns the channel outbound adapter as any; callers use type
// assertions against ChannelPayloadSender / ChannelTextSender / ChannelMediaSender.
type ChannelOutboundResolver interface {
	Outbound(channelID string) any
	ResolveOutboundMessage(ctx context.Context, target core.DeliveryTarget, payload core.ReplyPayload) (core.ChannelOutboundMessage, config.ChannelConfig, error)
}

// Channel sender interfaces — structurally identical to the runtime versions
// so that concrete channel adapters satisfy both.

// ChannelPayloadSender delivers a full payload to a channel.
type ChannelPayloadSender interface {
	SendPayload(ctx context.Context, message core.ChannelOutboundMessage, channel config.ChannelConfig) (core.ChannelDeliveryResult, error)
}

// ChannelTextSender delivers text messages to a channel.
type ChannelTextSender interface {
	SendText(ctx context.Context, message core.ChannelOutboundMessage, channel config.ChannelConfig) (core.ChannelDeliveryResult, error)
}

// ChannelMediaSender delivers media messages to a channel.
type ChannelMediaSender interface {
	SendMedia(ctx context.Context, message core.ChannelOutboundMessage, channel config.ChannelConfig) (core.ChannelDeliveryResult, error)
}

// ---------------------------------------------------------------------------
// RouterDeliverer
// ---------------------------------------------------------------------------

// RouterDeliverer routes replies to the correct channel (webchat, CLI fallback,
// or external channel) and manages the delivery queue.
type RouterDeliverer struct {
	Fallback         core.Deliverer
	Events           EventNotifier // renamed from Webchat
	Channels         ChannelOutboundResolver
	Sessions         *session.SessionStore
	Hooks            OutboundHookRunner
	Audit            *infra.AuditLog
	NormalizeChannel func(string) string
	ChunkText        func(text string, outbound any, cfg config.ChannelConfig, accountID string) []string
}

func (d *RouterDeliverer) normalizeChannel(ch string) string {
	if d.NormalizeChannel != nil {
		return d.NormalizeChannel(ch)
	}
	return strings.TrimSpace(strings.ToLower(ch))
}

func (d *RouterDeliverer) chunkText(text string, outbound any, cfg config.ChannelConfig, accountID string) []string {
	if d.ChunkText != nil {
		return d.ChunkText(text, outbound, cfg, accountID)
	}
	return []string{text}
}

// Deliver routes the reply to the appropriate channel.
func (d *RouterDeliverer) Deliver(ctx context.Context, kind core.ReplyKind, payload core.ReplyPayload, target core.DeliveryTarget) error {
	target = d.resolvePersistedDeliveryTarget(target)
	channel := d.normalizeChannel(target.Channel)
	switch channel {
	case "", "cli":
		if d.Fallback == nil {
			return nil
		}
		return d.Fallback.Deliver(ctx, kind, payload, target)
	case "webchat":
		if d.Events != nil {
			d.Events.Record(target, kind, payload)
			d.emitMessageSent(ctx, kind, target, payload, QueuedDelivery{}, nil, nil)
			if kind == core.ReplyKindFinal {
				d.mirrorTranscript(target, payload)
			}
			return nil
		}
		if d.Fallback != nil {
			return d.Fallback.Deliver(ctx, kind, payload, target)
		}
		return nil
	default:
		if d.Channels == nil {
			if d.Fallback != nil {
				return d.Fallback.Deliver(ctx, kind, payload, target)
			}
			return core.ErrChannelRegistryNotConfigured
		}
		outbound := d.Channels.Outbound(channel)
		if outbound == nil {
			if d.Fallback != nil {
				return d.Fallback.Deliver(ctx, kind, payload, target)
			}
			return fmt.Errorf("channel outbound %q is not registered", channel)
		}
		if kind != core.ReplyKindFinal {
			return nil
		}
		normalized, ok := NormalizeReplyPayloadForDelivery(payload)
		if !ok {
			return nil
		}
		var queueEntry QueuedDelivery
		if d.Sessions != nil && strings.TrimSpace(d.Sessions.BaseDir()) != "" {
			if queued, queueErr := EnqueueDelivery(d.Sessions.BaseDir(), kind, normalized, target); queueErr == nil {
				queueEntry = queued
				if d.Audit != nil {
					_ = d.Audit.Record(ctx, core.AuditEvent{ // best-effort; failure is non-critical
						Category:   core.AuditCategoryDelivery,
						Type:       "queued",
						Level:      "info",
						SessionKey: target.SessionKey,
						Channel:    channel,
						Message:    "delivery queued",
						Data: map[string]any{
							"queueId": queueEntry.ID,
							"kind":    kind,
							"to":      target.To,
						},
					})
				}
				if d.Events != nil {
					d.Events.EmitAgentEvent(target.SessionKey, core.AgentEvent{
						Stream:     "delivery",
						OccurredAt: time.Now().UTC(),
						SessionKey: target.SessionKey,
						Data: map[string]any{
							"type":    "queued",
							"queueId": queueEntry.ID,
							"kind":    kind,
							"channel": channel,
						},
					})
				}
			}
		}
		if IsPlainTextSurface(channel) {
			normalized.Text = SanitizeForPlainText(normalized.Text)
		}
		if d.Hooks != nil {
			hookResult, hookErr := d.Hooks.OnMessageSending(ctx, OutboundMessageSendingEvent{
				Kind:        kind,
				SessionKey:  target.SessionKey,
				Channel:     channel,
				To:          target.To,
				AccountID:   target.AccountID,
				ThreadID:    target.ThreadID,
				QueueID:     queueEntry.ID,
				Attempt:     queueEntry.AttemptCount + 1,
				Mode:        resolveOutboundMode(target),
				Content:     normalized.Text,
				MediaURLs:   normalizedMediaURLs(normalized),
				ChannelData: cloneChannelData(normalized.ChannelData),
			})
			if hookErr == nil {
				if hookResult.Cancel {
					if queueEntry.ID != "" && d.Sessions != nil {
						_ = CancelDelivery(d.Sessions.BaseDir(), queueEntry) // best-effort; failure is non-critical
					}
					return nil
				}
				if strings.TrimSpace(hookResult.Content) != "" {
					normalized.Text = strings.TrimSpace(hookResult.Content)
				}
			}
		}
		d.recordDebugDeliveryEvent(ctx, "resolve_start", kind, target, normalized, queueEntry, nil, nil, map[string]any{
			"resolvedChannel": channel,
		})
		message, cfg, err := d.Channels.ResolveOutboundMessage(ctx, target, normalized)
		if err != nil {
			d.recordDebugDeliveryEvent(ctx, "resolve_failed", kind, target, normalized, queueEntry, nil, err, nil)
			d.emitMessageSent(ctx, kind, target, normalized, queueEntry, nil, err)
			if queueEntry.ID != "" && d.Sessions != nil {
				_ = FailDelivery(d.Sessions.BaseDir(), queueEntry, err.Error(), false) // best-effort; failure is non-critical
			}
			if d.Fallback != nil {
				return d.Fallback.Deliver(ctx, kind, normalized, target)
			}
			return err
		}
		d.recordDebugDeliveryEvent(ctx, "resolve_success", kind, target, normalized, queueEntry, nil, nil, map[string]any{
			"outboundChannel": message.Channel,
			"outboundTo":      message.To,
			"outboundMode":    message.Mode,
			"outboundAccount": message.AccountID,
			"outboundThread":  message.ThreadID,
			"replyToId":       message.ReplyToID,
		})
		queueEntry.Target = target
		queueEntry.Payload = normalized
		if queueEntry.ID != "" && d.Sessions != nil {
			_ = MarkDeliverySending(d.Sessions.BaseDir(), queueEntry) // best-effort; failure is non-critical
			queueEntry.AttemptCount++
			queueEntry.Status = DeliveryStatusSending
			queueEntry.LastAttemptAt = time.Now().UTC()
		}
		if d.Events != nil {
			d.Events.EmitAgentEvent(target.SessionKey, core.AgentEvent{
				Stream:     "delivery",
				OccurredAt: time.Now().UTC(),
				SessionKey: target.SessionKey,
				Data: map[string]any{
					"type":      "sending",
					"queueId":   queueEntry.ID,
					"attempt":   queueEntry.AttemptCount,
					"kind":      kind,
					"channel":   channel,
					"to":        target.To,
					"accountId": target.AccountID,
					"threadId":  target.ThreadID,
				},
			})
		}
		results := make([]core.ChannelDeliveryResult, 0)
		if sender, ok := outbound.(ChannelPayloadSender); ok {
			var result core.ChannelDeliveryResult
			d.recordDebugDeliveryEvent(ctx, "payload_send_start", kind, target, normalized, queueEntry, nil, nil, map[string]any{
				"outboundTo":    message.To,
				"outboundMode":  message.Mode,
				"outboundReply": message.ReplyToID,
				"accountId":     message.AccountID,
				"threadId":      message.ThreadID,
			})
			result, err = sender.SendPayload(ctx, message, cfg)
			if err == nil {
				results = append(results, result)
				d.recordDebugDeliveryEvent(ctx, "payload_send_success", kind, target, normalized, queueEntry, results, nil, nil)
			} else {
				d.recordDebugDeliveryEvent(ctx, "payload_send_failed", kind, target, normalized, queueEntry, results, err, nil)
			}
			return d.finishExternalDelivery(ctx, kind, target, normalized, queueEntry, results, err)
		}
		if hasMediaPayload(normalized) {
			if sender, ok := outbound.(ChannelMediaSender); ok {
				mediaURLs := normalizedMediaURLs(normalized)
				for idx, mediaURL := range mediaURLs {
					next := message
					next.Payload = normalized
					next.Payload.MediaURL = mediaURL
					next.Payload.MediaURLs = nil
					if idx > 0 {
						next.Payload.Text = ""
					}
					d.recordDebugDeliveryEvent(ctx, "media_send_start", kind, target, next.Payload, queueEntry, results, nil, map[string]any{
						"index":         idx,
						"mediaUrl":      mediaURL,
						"outboundTo":    next.To,
						"outboundMode":  next.Mode,
						"outboundReply": next.ReplyToID,
						"accountId":     next.AccountID,
						"threadId":      next.ThreadID,
					})
					result, sendErr := sender.SendMedia(ctx, next, cfg)
					err = sendErr
					if err != nil {
						d.recordDebugDeliveryEvent(ctx, "media_send_failed", kind, target, next.Payload, queueEntry, results, err, map[string]any{
							"index": idx,
						})
						return d.finishExternalDelivery(ctx, kind, target, normalized, queueEntry, results, err)
					}
					results = append(results, result)
					d.recordDebugDeliveryEvent(ctx, "media_send_success", kind, target, next.Payload, queueEntry, results, nil, map[string]any{
						"index": idx,
					})
				}
				return d.finishExternalDelivery(ctx, kind, target, normalized, queueEntry, results, nil)
			}
		}
		if sender, ok := outbound.(ChannelTextSender); ok {
			text := strings.TrimSpace(normalized.Text)
			if hasMediaPayload(normalized) && text == "" {
				return d.finishExternalDelivery(ctx, kind, target, normalized, queueEntry, results, nil)
			}
			for _, chunk := range d.chunkText(text, outbound, cfg, message.AccountID) {
				next := message
				next.Payload = normalized
				next.Payload.Text = chunk
				d.recordDebugDeliveryEvent(ctx, "text_send_start", kind, target, next.Payload, queueEntry, results, nil, map[string]any{
					"chunkChars":    len([]rune(strings.TrimSpace(chunk))),
					"outboundTo":    next.To,
					"outboundMode":  next.Mode,
					"outboundReply": next.ReplyToID,
					"accountId":     next.AccountID,
					"threadId":      next.ThreadID,
				})
				result, sendErr := sender.SendText(ctx, next, cfg)
				err = sendErr
				if err != nil {
					d.recordDebugDeliveryEvent(ctx, "text_send_failed", kind, target, next.Payload, queueEntry, results, err, nil)
					return d.finishExternalDelivery(ctx, kind, target, normalized, queueEntry, results, err)
				}
				results = append(results, result)
				d.recordDebugDeliveryEvent(ctx, "text_send_success", kind, target, next.Payload, queueEntry, results, nil, nil)
			}
			return d.finishExternalDelivery(ctx, kind, target, normalized, queueEntry, results, nil)
		}
		err = fmt.Errorf("channel outbound %q does not support payload/text delivery", channel)
		d.emitMessageSent(ctx, kind, target, normalized, queueEntry, nil, err)
		if queueEntry.ID != "" && d.Sessions != nil {
			_ = FailDelivery(d.Sessions.BaseDir(), queueEntry, err.Error(), false) // best-effort; failure is non-critical
		}
		return err
	}
}

func (d *RouterDeliverer) recordDebugDeliveryEvent(ctx context.Context, typ string, kind core.ReplyKind, target core.DeliveryTarget, payload core.ReplyPayload, queueEntry QueuedDelivery, results []core.ChannelDeliveryResult, err error, extra map[string]any) {
	if d == nil || d.Audit == nil {
		return
	}
	data := map[string]any{
		"kind":            kind,
		"queueId":         queueEntry.ID,
		"attempt":         queueEntry.AttemptCount,
		"targetChannel":   target.Channel,
		"targetTo":        target.To,
		"targetAccountId": target.AccountID,
		"targetThreadId":  target.ThreadID,
		"replyToId":       strings.TrimSpace(payload.ReplyToID),
		"textChars":       len([]rune(strings.TrimSpace(payload.Text))),
		"mediaCount":      len(normalizedMediaURLs(payload)),
		"contentPreview":  previewAuditText(payload.Text, 120),
	}
	if len(results) > 0 {
		last := results[len(results)-1]
		data["resultMessageId"] = last.MessageID
		data["resultChatId"] = last.ChatID
		data["resultMeta"] = cloneChannelData(last.Meta)
	}
	if err != nil {
		data["error"] = err.Error()
	}
	for key, value := range extra {
		data[key] = value
	}
	_ = d.Audit.Record(ctx, core.AuditEvent{ // best-effort; failure is non-critical
		Category:   core.AuditCategoryDelivery,
		Type:       strings.TrimSpace(typ),
		Level:      "debug",
		SessionKey: target.SessionKey,
		Channel:    d.normalizeChannel(target.Channel),
		Message:    "delivery debug trace",
		Data:       data,
	})
}

func previewAuditText(text string, limit int) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || limit <= 0 {
		return ""
	}
	runes := []rune(trimmed)
	if len(runes) <= limit {
		return trimmed
	}
	return string(runes[:limit]) + "\u2026"
}

func (d *RouterDeliverer) resolvePersistedDeliveryTarget(target core.DeliveryTarget) core.DeliveryTarget {
	if d == nil || d.Sessions == nil || strings.TrimSpace(target.SessionKey) == "" {
		return target
	}
	entry := d.Sessions.Entry(target.SessionKey)
	if entry == nil {
		return target
	}
	if strings.TrimSpace(target.Channel) == "" {
		if entry.DeliveryContext != nil && strings.TrimSpace(entry.DeliveryContext.Channel) != "" {
			target.Channel = entry.DeliveryContext.Channel
		} else if strings.TrimSpace(entry.LastChannel) != "" {
			target.Channel = entry.LastChannel
		}
	}
	if strings.TrimSpace(target.To) == "" {
		if entry.DeliveryContext != nil && strings.TrimSpace(entry.DeliveryContext.To) != "" {
			target.To = entry.DeliveryContext.To
		} else if strings.TrimSpace(entry.LastTo) != "" {
			target.To = entry.LastTo
		}
	}
	if strings.TrimSpace(target.AccountID) == "" {
		if entry.DeliveryContext != nil && strings.TrimSpace(entry.DeliveryContext.AccountID) != "" {
			target.AccountID = entry.DeliveryContext.AccountID
		} else if strings.TrimSpace(entry.LastAccountID) != "" {
			target.AccountID = entry.LastAccountID
		}
	}
	if strings.TrimSpace(target.ThreadID) == "" {
		if entry.DeliveryContext != nil && strings.TrimSpace(entry.DeliveryContext.ThreadID) != "" {
			target.ThreadID = entry.DeliveryContext.ThreadID
		} else if strings.TrimSpace(entry.LastThreadID) != "" {
			target.ThreadID = entry.LastThreadID
		}
	}
	return target
}

func (d *RouterDeliverer) finishExternalDelivery(ctx context.Context, kind core.ReplyKind, target core.DeliveryTarget, payload core.ReplyPayload, queueEntry QueuedDelivery, results []core.ChannelDeliveryResult, err error) error {
	if err != nil {
		if errorsIsContextCancellation(err) {
			if queueEntry.ID != "" && d.Sessions != nil {
				_ = CancelDelivery(d.Sessions.BaseDir(), queueEntry) // best-effort; failure is non-critical
			}
			return err
		}
		if queueEntry.ID != "" && d.Sessions != nil {
			_ = FailDelivery(d.Sessions.BaseDir(), queueEntry, err.Error(), len(results) > 0) // best-effort; failure is non-critical
		}
		d.emitMessageSent(ctx, kind, target, payload, queueEntry, results, err)
		return err
	}
	if queueEntry.ID != "" && d.Sessions != nil {
		_ = SentDelivery(d.Sessions.BaseDir(), queueEntry, results) // best-effort; failure is non-critical
	}
	d.emitMessageSent(ctx, kind, target, payload, queueEntry, results, nil)
	d.mirrorTranscript(target, payload)
	return nil
}

func (d *RouterDeliverer) emitMessageSent(ctx context.Context, kind core.ReplyKind, target core.DeliveryTarget, payload core.ReplyPayload, queueEntry QueuedDelivery, results []core.ChannelDeliveryResult, err error) {
	status := DeliveryStatusSent
	if err != nil {
		if errorsIsContextCancellation(err) {
			status = DeliveryStatusCanceled
		} else if len(results) > 0 {
			status = DeliveryStatusPartial
		} else {
			status = DeliveryStatusFailed
		}
	}
	if d != nil && d.Events != nil && strings.TrimSpace(target.SessionKey) != "" {
		lastMessageID := ""
		if len(results) > 0 {
			lastMessageID = results[len(results)-1].MessageID
		}
		d.Events.EmitAgentEvent(target.SessionKey, core.AgentEvent{
			Stream:     "delivery",
			OccurredAt: time.Now().UTC(),
			SessionKey: target.SessionKey,
			Data: map[string]any{
				"type":      "sent",
				"kind":      kind,
				"queueId":   queueEntry.ID,
				"attempt":   queueEntry.AttemptCount,
				"status":    status,
				"channel":   target.Channel,
				"to":        target.To,
				"accountId": target.AccountID,
				"threadId":  target.ThreadID,
				"messageId": lastMessageID,
				"error": func() string {
					if err != nil {
						return err.Error()
					}
					return ""
				}(),
			},
		})
	}
	if d == nil || d.Hooks == nil {
		if d != nil && d.Audit != nil {
			d.recordDeliveryAudit(ctx, kind, target, payload, queueEntry, results, err, status)
		}
		return
	}
	lastMessageID := ""
	if len(results) > 0 {
		lastMessageID = results[len(results)-1].MessageID
	}
	_ = d.Hooks.OnMessageSent(ctx, OutboundMessageSentEvent{ // best-effort; failure is non-critical
		Kind:        kind,
		SessionKey:  target.SessionKey,
		Channel:     target.Channel,
		To:          target.To,
		AccountID:   target.AccountID,
		ThreadID:    target.ThreadID,
		QueueID:     queueEntry.ID,
		Attempt:     queueEntry.AttemptCount,
		Status:      status,
		Mode:        resolveOutboundMode(target),
		Content:     payload.Text,
		MediaURLs:   normalizedMediaURLs(payload),
		ChannelData: cloneChannelData(payload.ChannelData),
		Success:     err == nil,
		Error: func() string {
			if err != nil {
				return err.Error()
			}
			return ""
		}(),
		MessageID: lastMessageID,
	})
	if d.Audit != nil {
		d.recordDeliveryAudit(ctx, kind, target, payload, queueEntry, results, err, status)
	}
}

func errorsIsContextCancellation(err error) bool {
	return err == context.Canceled || err == context.DeadlineExceeded
}

func cloneChannelData(data map[string]any) map[string]any {
	if len(data) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(data))
	for key, value := range data {
		cloned[key] = value
	}
	return cloned
}

// ReplayQueued attempts to re-deliver a previously queued delivery entry.
func (d *RouterDeliverer) ReplayQueued(ctx context.Context, entry QueuedDelivery) error {
	if d == nil {
		return core.ErrDelivererNotConfigured
	}
	target := d.resolvePersistedDeliveryTarget(entry.Target)
	channel := d.normalizeChannel(target.Channel)
	if channel == "" || channel == "cli" || channel == "webchat" {
		return nil
	}
	if d.Channels == nil {
		return core.ErrChannelRegistryNotConfigured
	}
	outbound := d.Channels.Outbound(channel)
	if outbound == nil {
		return fmt.Errorf("channel outbound %q is not registered", channel)
	}
	payload, ok := NormalizeReplyPayloadForDelivery(entry.Payload)
	if !ok {
		if d.Sessions != nil {
			_ = AckDelivery(d.Sessions.BaseDir(), entry.ID) // best-effort; failure is non-critical
		}
		return nil
	}
	if IsPlainTextSurface(channel) {
		payload.Text = SanitizeForPlainText(payload.Text)
	}
	entry.Target = target
	entry.Payload = payload
	if d.Sessions != nil {
		_ = MarkDeliverySending(d.Sessions.BaseDir(), entry) // best-effort; failure is non-critical
		entry.AttemptCount++
		entry.Status = DeliveryStatusSending
		entry.LastAttemptAt = time.Now().UTC()
	}
	if d.Hooks != nil {
		hookResult, hookErr := d.Hooks.OnMessageSending(ctx, OutboundMessageSendingEvent{
			Kind:        entry.Kind,
			SessionKey:  target.SessionKey,
			Channel:     channel,
			To:          target.To,
			AccountID:   target.AccountID,
			ThreadID:    target.ThreadID,
			QueueID:     entry.ID,
			Attempt:     entry.AttemptCount,
			Mode:        resolveOutboundMode(target),
			Content:     payload.Text,
			MediaURLs:   normalizedMediaURLs(payload),
			ChannelData: cloneChannelData(payload.ChannelData),
		})
		if hookErr == nil {
			if hookResult.Cancel {
				if d.Sessions != nil {
					_ = CancelDelivery(d.Sessions.BaseDir(), entry) // best-effort; failure is non-critical
				}
				return nil
			}
			if strings.TrimSpace(hookResult.Content) != "" {
				payload.Text = strings.TrimSpace(hookResult.Content)
			}
		}
	}
	message, cfg, err := d.Channels.ResolveOutboundMessage(ctx, target, payload)
	if err != nil {
		if d.Sessions != nil {
			_ = FailDelivery(d.Sessions.BaseDir(), entry, err.Error(), false) // best-effort; failure is non-critical
		}
		d.emitMessageSent(ctx, entry.Kind, target, payload, entry, nil, err)
		return err
	}
	results := make([]core.ChannelDeliveryResult, 0)
	if sender, ok := outbound.(ChannelPayloadSender); ok {
		result, err := sender.SendPayload(ctx, message, cfg)
		if err == nil {
			results = append(results, result)
		}
		return d.finishExternalDelivery(ctx, entry.Kind, target, payload, entry, results, err)
	}
	if hasMediaPayload(payload) {
		if sender, ok := outbound.(ChannelMediaSender); ok {
			for idx, mediaURL := range normalizedMediaURLs(payload) {
				next := message
				next.Payload = payload
				next.Payload.MediaURL = mediaURL
				next.Payload.MediaURLs = nil
				if idx > 0 {
					next.Payload.Text = ""
				}
				result, err := sender.SendMedia(ctx, next, cfg)
				if err != nil {
					return d.finishExternalDelivery(ctx, entry.Kind, target, payload, entry, results, err)
				}
				results = append(results, result)
			}
			return d.finishExternalDelivery(ctx, entry.Kind, target, payload, entry, results, nil)
		}
	}
	if sender, ok := outbound.(ChannelTextSender); ok {
		for _, chunk := range d.chunkText(strings.TrimSpace(payload.Text), outbound, cfg, message.AccountID) {
			next := message
			next.Payload = payload
			next.Payload.Text = chunk
			result, err := sender.SendText(ctx, next, cfg)
			if err != nil {
				return d.finishExternalDelivery(ctx, entry.Kind, target, payload, entry, results, err)
			}
			results = append(results, result)
		}
		return d.finishExternalDelivery(ctx, entry.Kind, target, payload, entry, results, nil)
	}
	err = fmt.Errorf("channel outbound %q does not support payload/text delivery", channel)
	if d.Sessions != nil {
		_ = FailDelivery(d.Sessions.BaseDir(), entry, err.Error(), false) // best-effort; failure is non-critical
	}
	d.emitMessageSent(ctx, entry.Kind, target, payload, entry, nil, err)
	return err
}

func (d *RouterDeliverer) mirrorTranscript(target core.DeliveryTarget, payload core.ReplyPayload) {
	if d == nil || d.Sessions == nil || strings.TrimSpace(target.SessionKey) == "" || target.SkipTranscriptMirror {
		return
	}
	mirror := resolveMirroredTranscript(payload)
	if strings.TrimSpace(mirror.Text) == "" && len(mirror.MediaURLs) == 0 {
		return
	}
	history, err := d.Sessions.LoadTranscript(target.SessionKey)
	if err != nil {
		return
	}
	if len(history) > 0 {
		last := history[len(history)-1]
		if last.Role == "assistant" && strings.TrimSpace(last.Text) == strings.TrimSpace(mirror.Text) {
			if time.Since(last.Timestamp) < 2*time.Minute {
				return
			}
		}
	}
	entry := d.Sessions.Entry(target.SessionKey)
	if entry == nil {
		return
	}
	_ = d.Sessions.AppendTranscript(target.SessionKey, entry.SessionID, core.TranscriptMessage{ // best-effort; failure is non-critical
		Type:      "assistant_final",
		Role:      "assistant",
		Text:      mirror.Text,
		Timestamp: time.Now().UTC(),
		Final:     true,
		MediaURLs: mirror.MediaURLs,
	})
}

func resolveMirroredTranscript(payload core.ReplyPayload) core.MirroredTranscript {
	media := normalizedMediaURLs(payload)
	return core.MirroredTranscript{
		Text:      strings.TrimSpace(payload.Text),
		MediaURLs: media,
	}
}

func (d *RouterDeliverer) recordDeliveryAudit(ctx context.Context, kind core.ReplyKind, target core.DeliveryTarget, payload core.ReplyPayload, queueEntry QueuedDelivery, results []core.ChannelDeliveryResult, err error, status string) {
	if d == nil || d.Audit == nil {
		return
	}
	lastMessageID := ""
	if len(results) > 0 {
		lastMessageID = results[len(results)-1].MessageID
	}
	_ = d.Audit.Record(ctx, core.AuditEvent{ // best-effort; failure is non-critical
		Category:   core.AuditCategoryDelivery,
		Type:       strings.TrimSpace(status),
		Level:      "info",
		SessionKey: target.SessionKey,
		Channel:    target.Channel,
		Message:    "delivery completed",
		Data: map[string]any{
			"kind":        kind,
			"queueId":     queueEntry.ID,
			"attempt":     queueEntry.AttemptCount,
			"to":          target.To,
			"accountId":   target.AccountID,
			"threadId":    target.ThreadID,
			"messageId":   lastMessageID,
			"content":     payload.Text,
			"mediaUrls":   normalizedMediaURLs(payload),
			"channelData": cloneChannelData(payload.ChannelData),
			"error": func() string {
				if err != nil {
					return err.Error()
				}
				return ""
			}(),
		},
	})
}

func hasMediaPayload(payload core.ReplyPayload) bool {
	return strings.TrimSpace(payload.MediaURL) != "" || len(payload.MediaURLs) > 0
}

func normalizedMediaURLs(payload core.ReplyPayload) []string {
	out := make([]string, 0, len(payload.MediaURLs)+1)
	if trimmed := strings.TrimSpace(payload.MediaURL); trimmed != "" {
		out = append(out, trimmed)
	}
	for _, mediaURL := range payload.MediaURLs {
		if trimmed := strings.TrimSpace(mediaURL); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func resolveOutboundMode(target core.DeliveryTarget) string {
	if strings.TrimSpace(target.To) != "" {
		return "explicit"
	}
	return "implicit"
}

// // impl converts the runtime struct into a delivery.RouterDeliverer.
// func (d *RouterDeliverer) impl() *RouterDeliverer {
// 	rd := &RouterDeliverer{
// 		Fallback:         d.Fallback,
// 		Sessions:         d.Sessions,
// 		Hooks:            d.Hooks,
// 		Audit:            d.Audit,
// 		NormalizeChannel: backend.NormalizeProviderID,
// 		ChunkText: func(text string, outbound any, cfg config.ChannelConfig, accountID string) []string {
// 			if ob, ok := outbound.(ChannelOutbound); ok {
// 				return ChunkOutboundTextForAdapter(text, ob, cfg, accountID)
// 			}
// 			return []string{text}
// 		},
// 	}
// 	if d.Events != nil {
// 		rd.Events = d.Events
// 	}
// 	if d.Channels != nil {
// 		rd.Channels = &channelResolverAdapter{reg: d.Channels}
// 	}
// 	return rd
// }

// func (d *RouterDeliverer) Deliver(ctx context.Context, kind core.ReplyKind, payload core.ReplyPayload, target core.DeliveryTarget) error {
// 	return d.impl().Deliver(ctx, kind, payload, target)
// }

// func (d *RouterDeliverer) ReplayQueued(ctx context.Context, entry delivery.QueuedDelivery) error {
// 	return d.impl().ReplayQueued(ctx, entry)
// }

// ---------------------------------------------------------------------------
// channelResolverAdapter wraps *ChannelRegistry as delivery.ChannelOutboundResolver
// ---------------------------------------------------------------------------

// type channelResolverAdapter struct {
// 	reg *channel.ChannelRegistry
// }

// func (a *channelResolverAdapter) Outbound(channelID string) any {
// 	return a.reg.Outbound(channelID)
// }

// func (a *channelResolverAdapter) ResolveOutboundMessage(ctx context.Context, target core.DeliveryTarget, payload core.ReplyPayload) (core.ChannelOutboundMessage, config.ChannelConfig, error) {
// 	return a.reg.ResolveOutboundMessage(ctx, target, payload)
// }
