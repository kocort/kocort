package delivery

import (
	"context"

	"github.com/kocort/kocort/internal/core"
)

// OutboundMessageSendingEvent is emitted before a message is sent to a channel.
type OutboundMessageSendingEvent struct {
	Kind        core.ReplyKind
	SessionKey  string
	Channel     string
	To          string
	AccountID   string
	ThreadID    string
	QueueID     string
	Attempt     int
	Mode        string
	Content     string
	MediaURLs   []string
	ChannelData map[string]any
}

// OutboundMessageSendingResult is returned by the sending hook.
type OutboundMessageSendingResult struct {
	Cancel  bool
	Content string
}

// OutboundMessageSentEvent is emitted after a message delivery attempt.
type OutboundMessageSentEvent struct {
	Kind        core.ReplyKind
	SessionKey  string
	Channel     string
	To          string
	AccountID   string
	ThreadID    string
	QueueID     string
	Attempt     int
	Status      string
	Mode        string
	Content     string
	MediaURLs   []string
	ChannelData map[string]any
	Success     bool
	Error       string
	MessageID   string
}

// OutboundHookRunner processes outbound message hooks.
type OutboundHookRunner interface {
	OnMessageSending(ctx context.Context, event OutboundMessageSendingEvent) (OutboundMessageSendingResult, error)
	OnMessageSent(ctx context.Context, event OutboundMessageSentEvent) error
}
