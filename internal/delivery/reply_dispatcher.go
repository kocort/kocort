package delivery

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/core"
)

// MemoryDeliverer records deliveries in memory for testing.
type MemoryDeliverer struct {
	mu      sync.Mutex
	Records []core.DeliveryRecord
}

// Deliver records the delivery in memory.
func (d *MemoryDeliverer) Deliver(_ context.Context, kind core.ReplyKind, payload core.ReplyPayload, target core.DeliveryTarget) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Records = append(d.Records, core.DeliveryRecord{
		Kind:    kind,
		Payload: payload,
		Target:  target,
	})
	return nil
}

// ReplyDispatcher serialises delivery calls, tracks transcript, and supports
// idle-waiting so callers can block until all queued replies are sent.
type ReplyDispatcher struct {
	deliverer core.Deliverer
	target    core.DeliveryTarget

	mu           sync.Mutex
	pending      int
	complete     bool
	queue        []dispatchItem
	transcript   []core.TranscriptMessage
	idle         chan struct{}
	closed       bool
	lastToolText string
}

type dispatchItem struct {
	kind    core.ReplyKind
	payload core.ReplyPayload
}

// NewReplyDispatcher creates and starts a new dispatcher goroutine.
func NewReplyDispatcher(deliverer core.Deliverer, target core.DeliveryTarget) *ReplyDispatcher {
	d := &ReplyDispatcher{
		deliverer: deliverer,
		target:    target,
		pending:   1,
		idle:      make(chan struct{}),
	}
	go d.loop()
	return d
}

// SendToolResult enqueues a tool result payload for delivery.
func (d *ReplyDispatcher) SendToolResult(payload core.ReplyPayload) bool {
	return d.enqueue(core.ReplyKindTool, payload)
}

// SendBlockReply enqueues a block reply payload for delivery.
func (d *ReplyDispatcher) SendBlockReply(payload core.ReplyPayload) bool {
	return d.enqueue(core.ReplyKindBlock, payload)
}

// SendFinalReply enqueues a final reply payload for delivery.
func (d *ReplyDispatcher) SendFinalReply(payload core.ReplyPayload) bool {
	return d.enqueue(core.ReplyKindFinal, payload)
}

// MarkComplete signals that no further items will be enqueued.
func (d *ReplyDispatcher) MarkComplete() {
	d.mu.Lock()
	d.complete = true
	if d.pending == 1 {
		d.pending = 0
		if !d.closed {
			close(d.idle)
			d.closed = true
		}
	}
	d.mu.Unlock()
}

// WaitForIdle blocks until all queued items are delivered or the context is cancelled.
func (d *ReplyDispatcher) WaitForIdle(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-d.idle:
		return nil
	}
}

// TranscriptMessages returns a snapshot of recorded transcript messages.
func (d *ReplyDispatcher) TranscriptMessages() []core.TranscriptMessage {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]core.TranscriptMessage, len(d.transcript))
	copy(out, d.transcript)
	return out
}

func (d *ReplyDispatcher) enqueue(kind core.ReplyKind, payload core.ReplyPayload) bool {
	if payload.Text == "" && payload.MediaURL == "" && len(payload.MediaURLs) == 0 {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return false
	}
	d.pending++
	d.queue = append(d.queue, dispatchItem{kind: kind, payload: payload})
	return true
}

func (d *ReplyDispatcher) loop() {
	for {
		d.mu.Lock()
		if len(d.queue) == 0 {
			if d.complete && d.pending == 0 && !d.closed {
				close(d.idle)
				d.closed = true
			}
			d.mu.Unlock()
			if d.closed {
				return
			}
			continue
		}
		item := d.queue[0]
		d.queue = d.queue[1:]
		d.mu.Unlock()

		_ = d.deliverer.Deliver(context.Background(), item.kind, item.payload, d.target) // best-effort; failure is non-critical

		d.mu.Lock()
		d.recordTranscript(item)
		d.pending--
		if d.pending == 1 && d.complete {
			d.pending = 0
		}
		if d.complete && d.pending == 0 && !d.closed {
			close(d.idle)
			d.closed = true
		}
		d.mu.Unlock()
		if d.closed {
			return
		}
	}
}

func (d *ReplyDispatcher) recordTranscript(item dispatchItem) {
	trimmed := item.payload.Text
	if item.kind == core.ReplyKindTool {
		trimmed = strings.TrimSpace(trimmed)
		if trimmed == "" || trimmed == d.lastToolText {
			return
		}
		d.lastToolText = trimmed
		return
	}
	if strings.TrimSpace(trimmed) == "" && item.payload.MediaURL == "" && len(item.payload.MediaURLs) == 0 {
		return
	}
	msg := core.TranscriptMessage{
		Role:      "assistant",
		Text:      strings.TrimSpace(trimmed),
		Timestamp: time.Now().UTC(),
		MediaURL:  item.payload.MediaURL,
		MediaURLs: item.payload.MediaURLs,
	}
	if item.kind == core.ReplyKindBlock {
		msg.Type = "assistant_partial"
		msg.Partial = true
	} else {
		msg.Type = "assistant_final"
		msg.Final = true
	}
	d.transcript = append(d.transcript, msg)
}
