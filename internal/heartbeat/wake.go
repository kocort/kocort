package heartbeat

import (
	"context"
	"strings"
	"sync"
	"time"
)

type HeartbeatRunResult struct {
	Status   string
	Reason   string
	Duration time.Duration
}

type HeartbeatWakeRequest struct {
	Reason     string
	AgentID    string
	SessionKey string
}

type HeartbeatWakeHandler func(context.Context, HeartbeatWakeRequest) HeartbeatRunResult

type HeartbeatWakeBus struct {
	mu      sync.Mutex
	handler HeartbeatWakeHandler
	pending map[string]HeartbeatWakeRequest
	timer   *time.Timer
	running bool
	ctx     context.Context
	cancel  context.CancelFunc
}

func NewHeartbeatWakeBus() *HeartbeatWakeBus {
	ctx, cancel := context.WithCancel(context.Background())
	return &HeartbeatWakeBus{
		pending: map[string]HeartbeatWakeRequest{},
		ctx:     ctx,
		cancel:  cancel,
	}
}

func (b *HeartbeatWakeBus) SetHandler(handler HeartbeatWakeHandler) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handler = handler
}

func (b *HeartbeatWakeBus) Request(req HeartbeatWakeRequest, coalesce time.Duration) {
	if b == nil {
		return
	}
	if coalesce <= 0 {
		coalesce = 250 * time.Millisecond
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Reason == "" {
		req.Reason = "requested"
	}
	key := strings.TrimSpace(req.AgentID) + "::" + strings.TrimSpace(req.SessionKey)
	b.mu.Lock()
	b.pending[key] = req
	if b.timer == nil {
		b.timer = time.AfterFunc(coalesce, b.flush)
	}
	b.mu.Unlock()
}

func (b *HeartbeatWakeBus) Stop() {
	if b == nil {
		return
	}
	b.cancel()
	b.mu.Lock()
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	b.pending = map[string]HeartbeatWakeRequest{}
	b.mu.Unlock()
}

func (b *HeartbeatWakeBus) flush() {
	b.mu.Lock()
	if b.running {
		b.timer = time.AfterFunc(250*time.Millisecond, b.flush)
		b.mu.Unlock()
		return
	}
	b.running = true
	handler := b.handler
	pending := make([]HeartbeatWakeRequest, 0, len(b.pending))
	for _, req := range b.pending {
		pending = append(pending, req)
	}
	b.pending = map[string]HeartbeatWakeRequest{}
	b.timer = nil
	b.mu.Unlock()

	if handler != nil {
		for _, req := range pending {
			handler(b.ctx, req)
		}
	}

	b.mu.Lock()
	b.running = false
	if len(b.pending) > 0 && b.timer == nil {
		b.timer = time.AfterFunc(250*time.Millisecond, b.flush)
	}
	b.mu.Unlock()
}
