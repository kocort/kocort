package delivery

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/core"
)

// BlockStreamingCoalescing configures coalescing behaviour for block streaming.
type BlockStreamingCoalescing struct {
	MinChars       int
	MaxChars       int
	Idle           time.Duration
	Joiner         string
	FlushOnEnqueue bool
}

// BlockReplyBuffer allows buffering certain payloads (e.g. audio) until flush.
type BlockReplyBuffer interface {
	ShouldBuffer(payload core.ReplyPayload) bool
	OnEnqueue(payload core.ReplyPayload)
	Finalize(payload core.ReplyPayload) core.ReplyPayload
}

// AudioAsVoiceBuffer buffers audio payloads and propagates the AudioAsVoice flag.
type AudioAsVoiceBuffer struct {
	mu               sync.Mutex
	isAudioPayload   func(core.ReplyPayload) bool
	seenAudioAsVoice bool
}

// NewAudioAsVoiceBuffer creates an AudioAsVoiceBuffer with the given detector.
func NewAudioAsVoiceBuffer(isAudioPayload func(core.ReplyPayload) bool) *AudioAsVoiceBuffer {
	return &AudioAsVoiceBuffer{isAudioPayload: isAudioPayload}
}

func (b *AudioAsVoiceBuffer) ShouldBuffer(payload core.ReplyPayload) bool {
	return b.isAudioPayload(payload)
}

func (b *AudioAsVoiceBuffer) OnEnqueue(payload core.ReplyPayload) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if payload.AudioAsVoice {
		b.seenAudioAsVoice = true
	}
}

func (b *AudioAsVoiceBuffer) Finalize(payload core.ReplyPayload) core.ReplyPayload {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.seenAudioAsVoice {
		payload.AudioAsVoice = true
	}
	return payload
}

// BlockReplyPipeline manages block-level reply streaming with deduplication,
// coalescing, buffering, and ordered delivery.
type BlockReplyPipeline struct {
	onBlockReply func(context.Context, core.ReplyPayload) error
	timeout      time.Duration
	coalescer    *blockReplyCoalescer
	buffer       BlockReplyBuffer

	mu                 sync.Mutex
	sentKeys           map[string]struct{}
	pendingKeys        map[string]struct{}
	seenKeys           map[string]struct{}
	bufferedKeys       map[string]struct{}
	bufferedPayloads   []core.ReplyPayload
	bufferedPayloadKey map[string]struct{}
	sendChain          chan blockDispatchItem
	drained            chan struct{}
	stopped            chan struct{}
	aborted            bool
	didStream          bool
}

// NewBlockReplyPipeline creates a pipeline with optional coalescing and buffering.
func NewBlockReplyPipeline(
	onBlockReply func(context.Context, core.ReplyPayload) error,
	timeout time.Duration,
	coalescing *BlockStreamingCoalescing,
	buffer BlockReplyBuffer,
) *BlockReplyPipeline {
	p := &BlockReplyPipeline{
		onBlockReply:       onBlockReply,
		timeout:            timeout,
		buffer:             buffer,
		sentKeys:           map[string]struct{}{},
		pendingKeys:        map[string]struct{}{},
		seenKeys:           map[string]struct{}{},
		bufferedKeys:       map[string]struct{}{},
		bufferedPayloadKey: map[string]struct{}{},
		sendChain:          make(chan blockDispatchItem, 32),
		drained:            make(chan struct{}),
		stopped:            make(chan struct{}),
	}
	if coalescing != nil {
		p.coalescer = newBlockReplyCoalescer(*coalescing, p.isAborted, func(payload core.ReplyPayload) error {
			p.mu.Lock()
			p.bufferedKeys = map[string]struct{}{}
			p.mu.Unlock()
			return p.sendPayload(payload, true)
		})
	}
	go p.loop()
	return p
}

type blockDispatchItem struct {
	payload          core.ReplyPayload
	bypassSeenChecks bool
}

func blockReplyPayloadKey(payload core.ReplyPayload) string {
	text := strings.TrimSpace(payload.Text)
	mediaList := append([]string{}, payload.MediaURLs...)
	if payload.MediaURL != "" {
		mediaList = append([]string{payload.MediaURL}, mediaList...)
	}
	return fmt.Sprintf("%s|%s|%s", text, strings.Join(mediaList, ","), payload.ReplyToID)
}

func (p *BlockReplyPipeline) sendPayload(payload core.ReplyPayload, bypassSeenCheck bool) error {
	if p.isAborted() {
		return nil
	}
	key := blockReplyPayloadKey(payload)

	p.mu.Lock()
	if !bypassSeenCheck {
		if _, ok := p.seenKeys[key]; ok {
			p.mu.Unlock()
			return nil
		}
		p.seenKeys[key] = struct{}{}
	}
	if _, ok := p.sentKeys[key]; ok {
		p.mu.Unlock()
		return nil
	}
	if _, ok := p.pendingKeys[key]; ok {
		p.mu.Unlock()
		return nil
	}
	p.pendingKeys[key] = struct{}{}
	p.mu.Unlock()

	select {
	case <-p.stopped:
		return nil
	case p.sendChain <- blockDispatchItem{payload: payload, bypassSeenChecks: bypassSeenCheck}:
		return nil
	}
}

func (p *BlockReplyPipeline) loop() {
	defer close(p.drained)
	for {
		select {
		case <-p.stopped:
			return
		case item := <-p.sendChain:
			key := blockReplyPayloadKey(item.payload)
			ctx := context.Background()
			if p.timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, p.timeout)
				err := p.onBlockReply(ctx, item.payload)
				cancel()
				p.afterSend(key, err)
				if err != nil {
					return
				}
				continue
			}
			err := p.onBlockReply(ctx, item.payload)
			p.afterSend(key, err)
			if err != nil {
				return
			}
		}
	}
}

func (p *BlockReplyPipeline) afterSend(key string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.pendingKeys, key)
	if err != nil {
		p.aborted = true
		select {
		case <-p.stopped:
		default:
			close(p.stopped)
		}
		return
	}
	p.sentKeys[key] = struct{}{}
	p.didStream = true
}

// Enqueue adds a payload to the pipeline for delivery.
func (p *BlockReplyPipeline) Enqueue(payload core.ReplyPayload) {
	if p.isAborted() {
		return
	}
	if p.buffer != nil {
		p.buffer.OnEnqueue(payload)
		if p.buffer.ShouldBuffer(payload) {
			key := blockReplyPayloadKey(payload)
			p.mu.Lock()
			if _, ok := p.seenKeys[key]; ok {
				p.mu.Unlock()
				return
			}
			if _, ok := p.sentKeys[key]; ok {
				p.mu.Unlock()
				return
			}
			if _, ok := p.pendingKeys[key]; ok {
				p.mu.Unlock()
				return
			}
			if _, ok := p.bufferedPayloadKey[key]; ok {
				p.mu.Unlock()
				return
			}
			p.seenKeys[key] = struct{}{}
			p.bufferedPayloadKey[key] = struct{}{}
			p.bufferedPayloads = append(p.bufferedPayloads, payload)
			p.mu.Unlock()
			return
		}
	}

	hasMedia := payload.MediaURL != "" || len(payload.MediaURLs) > 0
	if hasMedia {
		if p.coalescer != nil {
			_ = p.coalescer.Flush(true) // best-effort; failure is non-critical
		}
		_ = p.sendPayload(payload, false) // best-effort; failure is non-critical
		return
	}
	if p.coalescer != nil {
		key := blockReplyPayloadKey(payload)
		p.mu.Lock()
		if _, ok := p.seenKeys[key]; ok {
			p.mu.Unlock()
			return
		}
		if _, ok := p.pendingKeys[key]; ok {
			p.mu.Unlock()
			return
		}
		if _, ok := p.bufferedKeys[key]; ok {
			p.mu.Unlock()
			return
		}
		p.seenKeys[key] = struct{}{}
		p.bufferedKeys[key] = struct{}{}
		p.mu.Unlock()
		p.coalescer.Enqueue(payload)
		return
	}
	_ = p.sendPayload(payload, false) // best-effort; failure is non-critical
}

// Flush flushes any buffered payloads and waits for pending sends.
func (p *BlockReplyPipeline) Flush(force bool) error {
	if p.coalescer != nil {
		if err := p.coalescer.Flush(force); err != nil {
			return err
		}
	}

	p.mu.Lock()
	buffered := append([]core.ReplyPayload{}, p.bufferedPayloads...)
	p.bufferedPayloads = nil
	p.bufferedPayloadKey = map[string]struct{}{}
	p.mu.Unlock()

	for _, payload := range buffered {
		finalPayload := payload
		if p.buffer != nil {
			finalPayload = p.buffer.Finalize(payload)
		}
		if err := p.sendPayload(finalPayload, true); err != nil {
			return err
		}
	}

	for {
		p.mu.Lock()
		pending := len(p.pendingKeys)
		p.mu.Unlock()
		if pending == 0 {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Stop aborts the pipeline.
func (p *BlockReplyPipeline) Stop() {
	if p.coalescer != nil {
		p.coalescer.Stop()
	}
	p.mu.Lock()
	if !p.aborted {
		p.aborted = true
		select {
		case <-p.stopped:
		default:
			close(p.stopped)
		}
	}
	p.mu.Unlock()
}

// HasBuffered reports whether payloads are waiting to be sent.
func (p *BlockReplyPipeline) HasBuffered() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.bufferedPayloads) > 0 || (p.coalescer != nil && p.coalescer.HasBuffered())
}

// DidStream reports whether at least one payload was streamed.
func (p *BlockReplyPipeline) DidStream() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.didStream
}

func (p *BlockReplyPipeline) isAborted() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.aborted
}

// IsAborted reports whether the pipeline was aborted.
func (p *BlockReplyPipeline) IsAborted() bool {
	return p.isAborted()
}

// HasSentPayload reports whether a specific payload has been sent.
func (p *BlockReplyPipeline) HasSentPayload(payload core.ReplyPayload) bool {
	key := blockReplyPayloadKey(payload)
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.sentKeys[key]
	return ok
}

// ---------------------------------------------------------------------------
// blockReplyCoalescer — internal coalescing helper
// ---------------------------------------------------------------------------

type blockReplyCoalescer struct {
	config      BlockStreamingCoalescing
	shouldAbort func() bool
	onFlush     func(core.ReplyPayload) error

	mu               sync.Mutex
	bufferText       string
	bufferReplyToID  string
	bufferAudioVoice bool
	idleTimer        *time.Timer
}

func newBlockReplyCoalescer(
	config BlockStreamingCoalescing,
	shouldAbort func() bool,
	onFlush func(core.ReplyPayload) error,
) *blockReplyCoalescer {
	if config.MinChars <= 0 {
		config.MinChars = 1
	}
	if config.MaxChars < config.MinChars {
		config.MaxChars = config.MinChars
	}
	return &blockReplyCoalescer{
		config:      config,
		shouldAbort: shouldAbort,
		onFlush:     onFlush,
	}
}

func (c *blockReplyCoalescer) clearIdleTimer() {
	if c.idleTimer == nil {
		return
	}
	c.idleTimer.Stop()
	c.idleTimer = nil
}

func (c *blockReplyCoalescer) resetBuffer() {
	c.bufferText = ""
	c.bufferReplyToID = ""
	c.bufferAudioVoice = false
}

func (c *blockReplyCoalescer) scheduleIdleFlush() {
	if c.config.Idle <= 0 {
		return
	}
	c.clearIdleTimer()
	c.idleTimer = time.AfterFunc(c.config.Idle, func() {
		_ = c.Flush(false) // best-effort; failure is non-critical
	})
}

func (c *blockReplyCoalescer) Enqueue(payload core.ReplyPayload) {
	if c.shouldAbort() {
		return
	}
	hasMedia := payload.MediaURL != "" || len(payload.MediaURLs) > 0
	text := payload.Text
	hasText := strings.TrimSpace(text) != ""
	if hasMedia {
		_ = c.Flush(true)      // best-effort; failure is non-critical
		_ = c.onFlush(payload) // best-effort; failure is non-critical
		return
	}
	if !hasText {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.config.FlushOnEnqueue {
		if c.bufferText != "" {
			_ = c.flushLocked(true) // best-effort; failure is non-critical
		}
		c.bufferReplyToID = payload.ReplyToID
		c.bufferAudioVoice = payload.AudioAsVoice
		c.bufferText = text
		_ = c.flushLocked(true) // best-effort; failure is non-critical
		return
	}

	replyToConflict := c.bufferText != "" && payload.ReplyToID != "" && (c.bufferReplyToID == "" || c.bufferReplyToID != payload.ReplyToID)
	if c.bufferText != "" && (replyToConflict || c.bufferAudioVoice != payload.AudioAsVoice) {
		_ = c.flushLocked(true) // best-effort; failure is non-critical
	}
	if c.bufferText == "" {
		c.bufferReplyToID = payload.ReplyToID
		c.bufferAudioVoice = payload.AudioAsVoice
	}

	nextText := text
	if c.bufferText != "" {
		nextText = c.bufferText + c.config.Joiner + text
	}
	if len(nextText) > c.config.MaxChars {
		if c.bufferText != "" {
			_ = c.flushLocked(true) // best-effort; failure is non-critical
			c.bufferReplyToID = payload.ReplyToID
			c.bufferAudioVoice = payload.AudioAsVoice
			if len(text) >= c.config.MaxChars {
				_ = c.onFlush(payload) // best-effort; failure is non-critical
				return
			}
			c.bufferText = text
			c.scheduleIdleFlush()
			return
		}
		_ = c.onFlush(payload) // best-effort; failure is non-critical
		return
	}

	c.bufferText = nextText
	if len(c.bufferText) >= c.config.MaxChars {
		_ = c.flushLocked(true) // best-effort; failure is non-critical
		return
	}
	c.scheduleIdleFlush()
}

func (c *blockReplyCoalescer) flushLocked(force bool) error {
	c.clearIdleTimer()
	if c.shouldAbort() {
		c.resetBuffer()
		return nil
	}
	if c.bufferText == "" {
		return nil
	}
	if !force && !c.config.FlushOnEnqueue && len(c.bufferText) < c.config.MinChars {
		c.scheduleIdleFlush()
		return nil
	}
	payload := core.ReplyPayload{
		Text:         c.bufferText,
		ReplyToID:    c.bufferReplyToID,
		AudioAsVoice: c.bufferAudioVoice,
	}
	c.resetBuffer()
	return c.onFlush(payload)
}

func (c *blockReplyCoalescer) Flush(force bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.flushLocked(force)
}

func (c *blockReplyCoalescer) HasBuffered() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bufferText != ""
}

func (c *blockReplyCoalescer) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clearIdleTimer()
}
