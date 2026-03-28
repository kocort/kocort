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

type wakeTimerKind string

const (
	defaultWakeCoalesce = 250 * time.Millisecond
	defaultWakeRetry    = 1 * time.Second

	wakeTimerNormal wakeTimerKind = "normal"
	wakeTimerRetry  wakeTimerKind = "retry"
)

type HeartbeatWakeBus struct {
	mu        sync.Mutex
	handler   HeartbeatWakeHandler
	pending   map[string]HeartbeatWakeRequest
	timer     *time.Timer
	timerDue  time.Time
	timerKind wakeTimerKind
	running   bool
	ctx       context.Context
	cancel    context.CancelFunc
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
		coalesce = defaultWakeCoalesce
	}
	req = normalizeWakeRequest(req)
	key := wakeRequestKey(req)
	b.mu.Lock()
	b.pending[key] = mergeWakeRequests(b.pending[key], req)
	b.scheduleLocked(coalesce, wakeTimerNormal)
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
	b.timerDue = time.Time{}
	b.timerKind = ""
	b.pending = map[string]HeartbeatWakeRequest{}
	b.mu.Unlock()
}

func (b *HeartbeatWakeBus) flush() {
	b.mu.Lock()
	if b.running {
		b.scheduleLocked(defaultWakeCoalesce, wakeTimerNormal)
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
	b.timerDue = time.Time{}
	b.timerKind = ""
	b.mu.Unlock()

	if handler != nil {
		for _, req := range pending {
			result := func() (out HeartbeatRunResult) {
				defer func() {
					if recover() != nil {
						out = HeartbeatRunResult{Status: "failed", Reason: "panic"}
					}
				}()
				return handler(b.ctx, req)
			}()
			if result.Status == "skipped" && strings.EqualFold(strings.TrimSpace(result.Reason), "requests-in-flight") {
				b.mu.Lock()
				b.pending[wakeRequestKey(req)] = mergeWakeRequests(b.pending[wakeRequestKey(req)], req)
				b.scheduleLocked(defaultWakeRetry, wakeTimerRetry)
				b.mu.Unlock()
			} else if result.Status == "failed" {
				b.mu.Lock()
				retryReq := req
				if strings.TrimSpace(retryReq.Reason) == "" {
					retryReq.Reason = "retry"
				}
				b.pending[wakeRequestKey(req)] = mergeWakeRequests(b.pending[wakeRequestKey(req)], retryReq)
				b.scheduleLocked(defaultWakeRetry, wakeTimerRetry)
				b.mu.Unlock()
			}
		}
	}

	b.mu.Lock()
	b.running = false
	if len(b.pending) > 0 && b.timer == nil {
		b.scheduleLocked(defaultWakeCoalesce, wakeTimerNormal)
	}
	b.mu.Unlock()
}

func (b *HeartbeatWakeBus) scheduleLocked(delay time.Duration, kind wakeTimerKind) {
	if delay <= 0 {
		delay = defaultWakeCoalesce
	}
	due := time.Now().Add(delay)
	if b.timer != nil {
		if b.timerKind == wakeTimerRetry {
			return
		}
		if !b.timerDue.IsZero() && !b.timerDue.After(due) {
			return
		}
		b.timer.Stop()
		b.timer = nil
		b.timerDue = time.Time{}
		b.timerKind = ""
	}
	b.timerDue = due
	b.timerKind = kind
	b.timer = time.AfterFunc(delay, b.flush)
}

func normalizeWakeRequest(req HeartbeatWakeRequest) HeartbeatWakeRequest {
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.SessionKey = strings.TrimSpace(req.SessionKey)
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Reason == "" {
		req.Reason = "requested"
	}
	return req
}

func wakeRequestKey(req HeartbeatWakeRequest) string {
	return req.AgentID + "::" + req.SessionKey
}

func mergeWakeRequests(current, next HeartbeatWakeRequest) HeartbeatWakeRequest {
	if current.Reason == "" {
		return next
	}
	currentPriority := wakeReasonPriority(current.Reason)
	nextPriority := wakeReasonPriority(next.Reason)
	if nextPriority > currentPriority {
		return next
	}
	if nextPriority == currentPriority {
		return next
	}
	return current
}

func wakeReasonPriority(reason string) int {
	reason = strings.ToLower(strings.TrimSpace(reason))
	switch {
	case reason == "retry":
		return 0
	case reason == "interval":
		return 1
	case strings.HasPrefix(reason, "cron:") || reason == "wake":
		return 3
	default:
		return 2
	}
}
