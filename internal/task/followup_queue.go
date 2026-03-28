package task

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/core"
)

const (
	DefaultQueueDebounce = time.Second
	DefaultQueueCap      = 20
)

// QueueSettings controls follow-up queue behaviour per queue key.
type QueueSettings struct {
	Mode       core.QueueMode
	Debounce   time.Duration
	Cap        int
	DropPolicy core.QueueDropPolicy
}

// FollowupRun represents a single queued follow-up run.
type FollowupRun struct {
	QueueKey             string
	Request              core.AgentRunRequest
	Prompt               string
	MessageID            string
	SummaryLine          string
	EnqueuedAt           time.Time
	OriginatingChannel   string
	OriginatingTo        string
	OriginatingAccountID string
	OriginatingThreadID  string
}

// FollowupQueueState is the internal state for one queue key.
type FollowupQueueState struct {
	Items          []FollowupRun
	Draining       bool
	LastEnqueuedAt time.Time
	Mode           core.QueueMode
	Debounce       time.Duration
	Cap            int
	DropPolicy     core.QueueDropPolicy
	DroppedCount   int
	SummaryLines   []string
	LastRun        *core.AgentRunRequest
}

// FollowupQueue manages debounced follow-up message queues.
type FollowupQueue struct {
	mu              sync.Mutex
	items           map[string]*FollowupQueueState
	callbacks       map[string]func(FollowupRun) error
	recentMessageID map[string]time.Time
	now             func() time.Time
	sleep           func(context.Context, time.Duration) error
}

// SetSleep overrides the sleep function (for testing).
func (q *FollowupQueue) SetSleep(fn func(context.Context, time.Duration) error) {
	q.sleep = fn
}

// NewFollowupQueue creates a new empty FollowupQueue.
func NewFollowupQueue() *FollowupQueue {
	return &FollowupQueue{
		items:           map[string]*FollowupQueueState{},
		callbacks:       map[string]func(FollowupRun) error{},
		recentMessageID: map[string]time.Time{},
		now:             func() time.Time { return time.Now().UTC() },
		sleep: func(ctx context.Context, d time.Duration) error {
			if d <= 0 {
				return nil
			}
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
	}
}

// ResolveActiveRunQueueAction determines what to do when a run arrives while
// another is active.
func ResolveActiveRunQueueAction(isActive, isHeartbeat, shouldFollowup bool, queueMode core.QueueMode) core.ActiveRunQueueAction {
	if !isActive {
		return core.ActiveRunRunNow
	}
	if isHeartbeat {
		return core.ActiveRunDrop
	}
	if shouldFollowup || queueMode == core.QueueModeSteer {
		return core.ActiveRunEnqueueFollowup
	}
	return core.ActiveRunRunNow
}

func normalizeQueueSettings(settings QueueSettings) QueueSettings {
	normalized := settings
	if normalized.Mode == "" {
		normalized.Mode = core.QueueModeFollowup
	}
	if normalized.Debounce < 0 {
		normalized.Debounce = 0
	}
	if normalized.Debounce == 0 {
		normalized.Debounce = DefaultQueueDebounce
	}
	if normalized.Cap <= 0 {
		normalized.Cap = DefaultQueueCap
	}
	if normalized.DropPolicy == "" {
		normalized.DropPolicy = core.QueueDropSummarize
	}
	return normalized
}

func (q *FollowupQueue) getOrCreateState(queueKey string, settings QueueSettings) *FollowupQueueState {
	queueKey = strings.TrimSpace(queueKey)
	normalized := normalizeQueueSettings(settings)
	if existing, ok := q.items[queueKey]; ok {
		existing.Mode = normalized.Mode
		existing.Debounce = normalized.Debounce
		existing.Cap = normalized.Cap
		existing.DropPolicy = normalized.DropPolicy
		return existing
	}
	created := &FollowupQueueState{
		Items:        []FollowupRun{},
		Mode:         normalized.Mode,
		Debounce:     normalized.Debounce,
		Cap:          normalized.Cap,
		DropPolicy:   normalized.DropPolicy,
		SummaryLines: []string{},
	}
	q.items[queueKey] = created
	return created
}

func (q *FollowupQueue) buildRecentMessageIDKey(queueKey string, run FollowupRun) string {
	if strings.TrimSpace(run.MessageID) == "" {
		return ""
	}
	return fmt.Sprintf(
		"queue|%s|%s|%s|%s|%s|%s",
		queueKey,
		run.OriginatingChannel,
		run.OriginatingTo,
		run.OriginatingAccountID,
		run.OriginatingThreadID,
		strings.TrimSpace(run.MessageID),
	)
}

func sameRouting(a, b FollowupRun) bool {
	return a.OriginatingChannel == b.OriginatingChannel &&
		a.OriginatingTo == b.OriginatingTo &&
		a.OriginatingAccountID == b.OriginatingAccountID &&
		a.OriginatingThreadID == b.OriginatingThreadID
}

func isRunAlreadyQueued(run FollowupRun, items []FollowupRun, allowPromptFallback bool) bool {
	messageID := strings.TrimSpace(run.MessageID)
	for _, item := range items {
		if !sameRouting(item, run) {
			continue
		}
		if messageID != "" && strings.TrimSpace(item.MessageID) == messageID {
			return true
		}
	}
	if !allowPromptFallback {
		return false
	}
	for _, item := range items {
		if sameRouting(item, run) && item.Prompt == run.Prompt {
			return true
		}
	}
	return false
}

func buildQueueSummaryLine(text string, limit int) string {
	cleaned := strings.Join(strings.Fields(text), " ")
	if len(cleaned) <= limit {
		return cleaned
	}
	if limit <= 1 {
		return cleaned[:1]
	}
	return strings.TrimSpace(cleaned[:limit-1]) + "..."
}

func (q *FollowupQueue) applyDropPolicy(state *FollowupQueueState, run FollowupRun) bool {
	if state.Cap <= 0 || len(state.Items) < state.Cap {
		return true
	}
	if state.DropPolicy == core.QueueDropNew {
		return false
	}
	dropCount := len(state.Items) - state.Cap + 1
	dropped := append([]FollowupRun(nil), state.Items[:dropCount]...)
	state.Items = append([]FollowupRun(nil), state.Items[dropCount:]...)
	if state.DropPolicy == core.QueueDropSummarize {
		for _, item := range dropped {
			state.DroppedCount++
			summary := strings.TrimSpace(item.SummaryLine)
			if summary == "" {
				summary = strings.TrimSpace(item.Prompt)
			}
			state.SummaryLines = append(state.SummaryLines, buildQueueSummaryLine(summary, 160))
		}
		for len(state.SummaryLines) > state.Cap {
			state.SummaryLines = state.SummaryLines[1:]
		}
	}
	_ = run // unused; run processing complete
	return true
}

func (q *FollowupQueue) Enqueue(run FollowupRun, settings QueueSettings, dedupeMode core.QueueDedupeMode) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	queueKey := strings.TrimSpace(run.QueueKey)
	if queueKey == "" {
		return false
	}
	state := q.getOrCreateState(queueKey, settings)
	if dedupeMode != core.QueueDedupeNone {
		recentKey := q.buildRecentMessageIDKey(queueKey, run)
		if recentKey != "" {
			if seenAt, ok := q.recentMessageID[recentKey]; ok && q.now().Sub(seenAt) < 5*time.Minute {
				return false
			}
		}
		if isRunAlreadyQueued(run, state.Items, dedupeMode == core.QueueDedupePrompt) {
			return false
		}
	}

	state.LastEnqueuedAt = q.now()
	reqCopy := run.Request
	state.LastRun = &reqCopy
	if !q.applyDropPolicy(state, run) {
		return false
	}
	state.Items = append(state.Items, run)
	if dedupeMode != core.QueueDedupeNone {
		if recentKey := q.buildRecentMessageIDKey(queueKey, run); recentKey != "" {
			q.recentMessageID[recentKey] = q.now()
		}
	}
	return true
}

func (q *FollowupQueue) Depth(queueKey string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	state, ok := q.items[queueKey]
	if !ok {
		return 0
	}
	return len(state.Items)
}

func (q *FollowupQueue) TotalDepth() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	total := 0
	for _, state := range q.items {
		total += len(state.Items)
	}
	return total
}

func (q *FollowupQueue) Clear(queueKey string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	state, ok := q.items[queueKey]
	if !ok {
		return 0
	}
	count := len(state.Items)
	delete(q.items, queueKey)
	delete(q.callbacks, queueKey)
	return count
}

func (q *FollowupQueue) previewSummaryPromptLocked(state *FollowupQueueState, noun string, title string) string {
	if state.DropPolicy != core.QueueDropSummarize || state.DroppedCount <= 0 {
		return ""
	}
	if title == "" {
		if state.DroppedCount == 1 {
			title = fmt.Sprintf("[Queue overflow] Dropped 1 %s due to cap.", noun)
		} else {
			title = fmt.Sprintf("[Queue overflow] Dropped %d %ss due to cap.", state.DroppedCount, noun)
		}
	}
	lines := []string{title}
	if len(state.SummaryLines) > 0 {
		lines = append(lines, "Summary:")
		for _, line := range state.SummaryLines {
			lines = append(lines, "- "+line)
		}
	}
	state.DroppedCount = 0
	state.SummaryLines = nil
	return strings.Join(lines, "\n")
}

func buildCollectPrompt(summary string, items []FollowupRun) string {
	blocks := []string{"[Queued messages while agent was busy]"}
	if summary != "" {
		blocks = append(blocks, summary)
	}
	for idx, item := range items {
		blocks = append(blocks, fmt.Sprintf("---\nQueued #%d\n%s", idx+1, item.Prompt))
	}
	return strings.Join(blocks, "\n\n")
}

func hasCrossChannelItems(items []FollowupRun) bool {
	keys := map[string]struct{}{}
	hasUnkeyed := false
	for _, item := range items {
		if item.OriginatingChannel == "" && item.OriginatingTo == "" && item.OriginatingAccountID == "" && item.OriginatingThreadID == "" {
			hasUnkeyed = true
			continue
		}
		if item.OriginatingChannel == "" || item.OriginatingTo == "" {
			return true
		}
		key := fmt.Sprintf("%s|%s|%s|%s", item.OriginatingChannel, item.OriginatingTo, item.OriginatingAccountID, item.OriginatingThreadID)
		keys[key] = struct{}{}
	}
	if len(keys) == 0 {
		return false
	}
	if hasUnkeyed {
		return true
	}
	return len(keys) > 1
}

// CopyRunWithPrompt creates a FollowupRun from a request with a new prompt.
func CopyRunWithPrompt(run core.AgentRunRequest, prompt string) FollowupRun {
	run.Message = prompt
	return FollowupRun{
		Request: run,
		Prompt:  prompt,
	}
}

func (q *FollowupQueue) waitForDebounce(ctx context.Context, state *FollowupQueueState) error {
	if state.Debounce <= 0 {
		return nil
	}
	for {
		wait := state.Debounce - q.now().Sub(state.LastEnqueuedAt)
		if wait <= 0 {
			return nil
		}
		if err := q.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

func (q *FollowupQueue) ScheduleDrain(ctx context.Context, queueKey string, runFollowup func(FollowupRun) error) {
	q.mu.Lock()
	state, ok := q.items[queueKey]
	if !ok || state.Draining {
		q.mu.Unlock()
		return
	}
	state.Draining = true
	q.callbacks[queueKey] = runFollowup
	q.mu.Unlock()

	go q.drainLoop(ctx, queueKey, runFollowup)
}

func (q *FollowupQueue) KickDrainIfIdle(ctx context.Context, queueKey string) {
	q.mu.Lock()
	cb := q.callbacks[queueKey]
	q.mu.Unlock()
	if cb == nil {
		return
	}
	q.ScheduleDrain(ctx, queueKey, cb)
}

func (q *FollowupQueue) drainLoop(ctx context.Context, queueKey string, runFollowup func(FollowupRun) error) {
	for {
		q.mu.Lock()
		state, ok := q.items[queueKey]
		if !ok {
			delete(q.callbacks, queueKey)
			q.mu.Unlock()
			return
		}
		q.mu.Unlock()

		if err := q.waitForDebounce(ctx, state); err != nil {
			q.finishDrain(queueKey)
			return
		}

		q.mu.Lock()
		state, ok = q.items[queueKey]
		if !ok {
			delete(q.callbacks, queueKey)
			q.mu.Unlock()
			return
		}
		if len(state.Items) == 0 && state.DroppedCount == 0 {
			delete(q.items, queueKey)
			delete(q.callbacks, queueKey)
			q.mu.Unlock()
			return
		}

		if state.Mode == core.QueueModeCollect {
			items := append([]FollowupRun(nil), state.Items...)
			summary := q.previewSummaryPromptLocked(state, "message", "")
			lastRun := state.LastRun
			crossChannel := hasCrossChannelItems(items)
			if crossChannel {
				next := state.Items[0]
				state.Items = state.Items[1:]
				q.mu.Unlock()
				if err := runFollowup(next); err != nil {
					q.finishDrain(queueKey)
					return
				}
				continue
			}
			if lastRun == nil || len(items) == 0 {
				delete(q.items, queueKey)
				delete(q.callbacks, queueKey)
				q.mu.Unlock()
				return
			}
			state.Items = nil
			composite := items[len(items)-1]
			composite.Request = *lastRun
			composite.Prompt = buildCollectPrompt(summary, items)
			composite.Request.Message = composite.Prompt
			q.mu.Unlock()
			if err := runFollowup(composite); err != nil {
				q.finishDrain(queueKey)
				return
			}
			continue
		}

		if summary := q.previewSummaryPromptLocked(state, "message", ""); summary != "" {
			lastRun := state.LastRun
			if lastRun == nil || len(state.Items) == 0 {
				delete(q.items, queueKey)
				delete(q.callbacks, queueKey)
				q.mu.Unlock()
				return
			}
			next := state.Items[0]
			next.Request = *lastRun
			next.Prompt = summary
			next.Request.Message = summary
			state.Items = state.Items[1:]
			q.mu.Unlock()
			if err := runFollowup(next); err != nil {
				q.finishDrain(queueKey)
				return
			}
			continue
		}

		if len(state.Items) == 0 {
			delete(q.items, queueKey)
			delete(q.callbacks, queueKey)
			q.mu.Unlock()
			return
		}
		next := state.Items[0]
		state.Items = state.Items[1:]
		q.mu.Unlock()
		if err := runFollowup(next); err != nil {
			q.finishDrain(queueKey)
			return
		}
	}
}

func (q *FollowupQueue) finishDrain(queueKey string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	state, ok := q.items[queueKey]
	if !ok {
		delete(q.callbacks, queueKey)
		return
	}
	state.Draining = false
	if len(state.Items) == 0 && state.DroppedCount == 0 {
		delete(q.items, queueKey)
		delete(q.callbacks, queueKey)
	}
}
