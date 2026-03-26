package acp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/core"
)

// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------

const (
	DefaultStreamFlushMs      = 2500
	DefaultNoOutputNoticeMs   = 60000
	DefaultNoOutputPollMs     = 15000
	DefaultMaxRelayLifetimeMs = 6 * 60 * 60 * 1000 // 6 hours
	StreamBufferMaxChars      = 4000
	StreamSnippetMaxChars     = 220
)

// ---------------------------------------------------------------------------
// LiveStreamRelay — real-time delta relay from ACP child to parent session
// ---------------------------------------------------------------------------

// LiveStreamRelay captures real-time progress from an ACP child run and
// relays summarised snippets to the parent session via the Deliverer.
// It operates as a long-lived goroutine and supports stall detection.
type LiveStreamRelay struct {
	mu sync.Mutex

	deliverer core.Deliverer
	target    core.DeliveryTarget
	logPath   string
	agentID   string
	childKey  string
	runID     string

	pendingText  strings.Builder
	lastOutputAt time.Time
	stallNotice  bool
	disposed     bool
	cancel       context.CancelFunc

	// Tuning parameters (milliseconds).
	streamFlushMs      int
	noOutputNoticeMs   int
	noOutputPollMs     int
	maxRelayLifetimeMs int
}

// LiveStreamRelayConfig holds optional tuning for the relay.
type LiveStreamRelayConfig struct {
	StreamFlushMs      int
	NoOutputNoticeMs   int
	NoOutputPollMs     int
	MaxRelayLifetimeMs int
	EmitStartNotice    bool
}

// StartLiveStreamRelay creates a relay that forwards real-time delta events
// from an ACP child to the parent session. Returns nil if the prerequisites
// are not satisfied (no deliverer, streamTo != "parent", or no route).
func StartLiveStreamRelay(
	deliverer core.Deliverer,
	req SessionSpawnRequest,
	result SessionSpawnResult,
	cfg LiveStreamRelayConfig,
) *LiveStreamRelay {
	if deliverer == nil || strings.TrimSpace(req.StreamTo) != "parent" {
		return nil
	}
	target := core.DeliveryTarget{
		SessionKey: req.RequesterSessionKey,
		Channel:    req.RouteChannel,
		To:         req.RouteTo,
		AccountID:  req.RouteAccountID,
		ThreadID:   req.RouteThreadID,
	}
	if strings.TrimSpace(target.Channel) == "" || strings.TrimSpace(target.To) == "" {
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	relay := &LiveStreamRelay{
		deliverer:          deliverer,
		target:             target,
		logPath:            ResolveParentStreamLogPath(result.ChildSessionKey),
		agentID:            strings.TrimSpace(result.AgentID),
		childKey:           strings.TrimSpace(result.ChildSessionKey),
		runID:              strings.TrimSpace(result.RunID),
		lastOutputAt:       time.Now().UTC(),
		cancel:             cancel,
		streamFlushMs:      defaultInt(cfg.StreamFlushMs, DefaultStreamFlushMs),
		noOutputNoticeMs:   defaultInt(cfg.NoOutputNoticeMs, DefaultNoOutputNoticeMs),
		noOutputPollMs:     defaultInt(cfg.NoOutputPollMs, DefaultNoOutputPollMs),
		maxRelayLifetimeMs: defaultInt(cfg.MaxRelayLifetimeMs, DefaultMaxRelayLifetimeMs),
	}

	relay.appendLog("started", fmt.Sprintf("started child=%s agent=%s runId=%s", relay.childKey, relay.agentID, relay.runID))

	if cfg.EmitStartNotice {
		_ = deliverer.Deliver(context.Background(), core.ReplyKindBlock, core.ReplyPayload{
			Text: fmt.Sprintf("[ACP stream started] %s → %s", relay.agentID, relay.childKey),
		}, target)
	}

	// Background goroutine: stall detection + lifetime limit.
	go relay.backgroundLoop(ctx)

	return relay
}

// OnEvent should be called for each ACP runtime event from the child session.
func (r *LiveStreamRelay) OnEvent(event core.AcpRuntimeEvent) {
	if r == nil || r.isDisposed() {
		return
	}
	switch event.Type {
	case "text_delta":
		r.handleTextDelta(event)
	case "done":
		r.flushPending()
		r.appendLog("done", strings.TrimSpace(event.StopReason))
	case "error":
		r.flushPending()
		r.appendLog("error", strings.TrimSpace(event.Text))
		_ = r.deliverer.Deliver(context.Background(), core.ReplyKindBlock, core.ReplyPayload{
			Text:    fmt.Sprintf("[ACP stream error] %s", strings.TrimSpace(event.Text)),
			IsError: true,
		}, r.target)
	}
}

// NotifyCompleted handles final completion (mirrors the existing ParentStreamRelay).
func (r *LiveStreamRelay) NotifyCompleted(result core.AgentRunResult, runErr error) {
	if r == nil {
		return
	}
	r.flushPending()
	if runErr != nil {
		r.appendLog("error", runErr.Error())
		_ = r.deliverer.Deliver(context.Background(), core.ReplyKindBlock, core.ReplyPayload{
			Text:    fmt.Sprintf("[ACP stream error] %s", runErr.Error()),
			IsError: true,
		}, r.target)
		r.Dispose()
		return
	}
	for _, payload := range result.Payloads {
		if strings.TrimSpace(payload.Text) == "" && strings.TrimSpace(payload.MediaURL) == "" && len(payload.MediaURLs) == 0 {
			continue
		}
		_ = r.deliverer.Deliver(context.Background(), core.ReplyKindBlock, payload, r.target)
	}
	r.appendLog("completed", summarizeRelayPayloads(result.Payloads))
	r.Dispose()
}

// Dispose tears down the relay and stops the background goroutine.
func (r *LiveStreamRelay) Dispose() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.disposed {
		r.mu.Unlock()
		return
	}
	r.disposed = true
	r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
	}
}

// ---------------------------------------------------------------------------
// Internal
// ---------------------------------------------------------------------------

func (r *LiveStreamRelay) isDisposed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.disposed
}

func (r *LiveStreamRelay) handleTextDelta(event core.AcpRuntimeEvent) {
	text := event.Text
	if text == "" {
		return
	}

	r.mu.Lock()
	r.lastOutputAt = time.Now().UTC()
	if r.stallNotice {
		r.stallNotice = false
		r.mu.Unlock()
		_ = r.deliverer.Deliver(context.Background(), core.ReplyKindBlock, core.ReplyPayload{
			Text: fmt.Sprintf("[ACP stream resumed] %s", r.childKey),
		}, r.target)
		r.mu.Lock()
	}
	r.pendingText.WriteString(text)
	shouldFlush := r.pendingText.Len() >= StreamSnippetMaxChars || strings.Contains(text, "\n\n")
	r.mu.Unlock()

	if shouldFlush {
		r.flushPending()
	}
}

func (r *LiveStreamRelay) flushPending() {
	r.mu.Lock()
	text := r.pendingText.String()
	r.pendingText.Reset()
	r.mu.Unlock()

	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	// Truncate if too long.
	if len(text) > StreamBufferMaxChars {
		text = text[:StreamBufferMaxChars] + "…"
	}
	snippet := text
	if len(snippet) > StreamSnippetMaxChars {
		snippet = snippet[:StreamSnippetMaxChars] + "…"
	}

	r.appendLog("delta", snippet)
	_ = r.deliverer.Deliver(context.Background(), core.ReplyKindBlock, core.ReplyPayload{
		Text: fmt.Sprintf("[ACP progress] %s\n%s", r.childKey, snippet),
	}, r.target)
}

func (r *LiveStreamRelay) backgroundLoop(ctx context.Context) {
	pollInterval := time.Duration(r.noOutputPollMs) * time.Millisecond
	maxLifetime := time.Duration(r.maxRelayLifetimeMs) * time.Millisecond
	startedAt := time.Now().UTC()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			// Lifetime limit.
			if now.UTC().Sub(startedAt) > maxLifetime {
				r.appendLog("timeout", "max relay lifetime exceeded")
				_ = r.deliverer.Deliver(context.Background(), core.ReplyKindBlock, core.ReplyPayload{
					Text: fmt.Sprintf("[ACP stream timeout] %s — relay exceeded maximum lifetime", r.childKey),
				}, r.target)
				r.Dispose()
				return
			}
			// Stall detection.
			r.mu.Lock()
			idleMs := now.UTC().Sub(r.lastOutputAt).Milliseconds()
			alreadyNotified := r.stallNotice
			r.mu.Unlock()
			if idleMs > int64(r.noOutputNoticeMs) && !alreadyNotified {
				r.mu.Lock()
				r.stallNotice = true
				r.mu.Unlock()
				r.appendLog("stall", fmt.Sprintf("no output for %dms", idleMs))
				_ = r.deliverer.Deliver(context.Background(), core.ReplyKindBlock, core.ReplyPayload{
					Text: fmt.Sprintf("[ACP stream stall] %s — no output for %ds", r.childKey, idleMs/1000),
				}, r.target)
			}
		}
	}
}

func (r *LiveStreamRelay) appendLog(kind, text string) {
	if r == nil || strings.TrimSpace(r.logPath) == "" {
		return
	}
	dir := filepath.Dir(r.logPath)
	_ = os.MkdirAll(dir, 0o755)
	line := fmt.Sprintf("%s\t%s\t%s\n", time.Now().UTC().Format(time.RFC3339), strings.TrimSpace(kind), strings.TrimSpace(text))
	f, err := os.OpenFile(r.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

func defaultInt(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}
