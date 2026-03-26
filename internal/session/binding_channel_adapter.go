package session

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// ChannelThreadBindingAdapter — generic adapter that channels can use to
// provide real adapter-backed thread binding. This implements the
// ThreadBindingAdapter interface and can be registered per channel+accountID.
// ---------------------------------------------------------------------------

// ChannelThreadBindingAdapter provides an in-memory, channel-aware binding
// adapter that channels (Discord, Slack, Telegram, etc.) can instantiate
// to back the ThreadBindingService with proper conversationId-level routing.
type ChannelThreadBindingAdapter struct {
	mu        sync.Mutex
	channel   string
	accountID string
	caps      ThreadBindingAdapterCapabilities
	bindings  map[string]*adapterBindingRecord // bindingID → record
	byConvo   map[string]string                // conversationID → bindingID
	bySession map[string][]string              // targetSessionKey → []bindingID
	nextID    int
}

type adapterBindingRecord struct {
	binding        SessionBindingRecord
	conversationID string
	parentConvoID  string
	boundAt        time.Time
	lastActivityAt time.Time
	idleTimeoutMs  int64
	maxAgeMs       int64
}

// ChannelThreadBindingAdapterConfig configures a new channel adapter.
type ChannelThreadBindingAdapterConfig struct {
	Channel         string
	AccountID       string
	BindSupported   bool
	UnbindSupported bool
	Placements      []ThreadBindingPlacement
}

// NewChannelThreadBindingAdapter creates a new adapter for a specific channel+account.
func NewChannelThreadBindingAdapter(cfg ChannelThreadBindingAdapterConfig) *ChannelThreadBindingAdapter {
	placements := cfg.Placements
	if len(placements) == 0 {
		placements = []ThreadBindingPlacement{ThreadBindingPlacementCurrent, ThreadBindingPlacementChild}
	}
	return &ChannelThreadBindingAdapter{
		channel:   strings.TrimSpace(cfg.Channel),
		accountID: strings.TrimSpace(cfg.AccountID),
		caps: ThreadBindingAdapterCapabilities{
			Placements:      placements,
			BindSupported:   cfg.BindSupported,
			UnbindSupported: cfg.UnbindSupported,
		},
		bindings:  make(map[string]*adapterBindingRecord),
		byConvo:   make(map[string]string),
		bySession: make(map[string][]string),
	}
}

func (a *ChannelThreadBindingAdapter) Channel() string   { return a.channel }
func (a *ChannelThreadBindingAdapter) AccountID() string { return a.accountID }
func (a *ChannelThreadBindingAdapter) Capabilities() ThreadBindingAdapterCapabilities {
	return a.caps
}

func (a *ChannelThreadBindingAdapter) Bind(input BindThreadSessionInput) (SessionBindingRecord, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// If already bound to same target, just touch.
	convoID := firstNonEmpty(input.ConversationID, input.ThreadID)
	if convoID != "" {
		if existingID, ok := a.byConvo[convoID]; ok {
			if existing := a.bindings[existingID]; existing != nil {
				if existing.binding.TargetSessionKey == input.TargetSessionKey {
					existing.lastActivityAt = time.Now().UTC()
					return existing.binding, nil
				}
				// Different target → unbind old, bind new.
				a.unbindRecordLocked(existingID)
			}
		}
	}

	a.nextID++
	bindingID := fmt.Sprintf("bind_%s_%s_%d", a.channel, a.accountID, a.nextID)
	now := time.Now().UTC()

	record := &adapterBindingRecord{
		binding: SessionBindingRecord{
			BindingID:            bindingID,
			TargetSessionKey:     input.TargetSessionKey,
			RequesterSessionKey:  input.RequesterSessionKey,
			TargetKind:           input.TargetKind,
			Status:               "active",
			BoundBy:              firstNonEmpty(string(input.Placement), "system"),
			Placement:            string(input.Placement),
			Channel:              input.Channel,
			To:                   input.To,
			AccountID:            input.AccountID,
			ThreadID:             input.ThreadID,
			ConversationID:       convoID,
			ParentConversationID: input.ParentConversationID,
			IdleTimeoutMs:        input.IdleTimeoutMs,
			MaxAgeMs:             input.MaxAgeMs,
			BoundAt:              now,
			LastActivityAt:       now,
		},
		conversationID: convoID,
		parentConvoID:  input.ParentConversationID,
		boundAt:        now,
		lastActivityAt: now,
		idleTimeoutMs:  input.IdleTimeoutMs,
		maxAgeMs:       input.MaxAgeMs,
	}

	a.bindings[bindingID] = record
	if convoID != "" {
		a.byConvo[convoID] = bindingID
	}
	a.bySession[input.TargetSessionKey] = append(a.bySession[input.TargetSessionKey], bindingID)

	return record.binding, nil
}

func (a *ChannelThreadBindingAdapter) ListBySession(targetSessionKey string) []SessionBindingRecord {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.evictExpiredLocked()

	ids := a.bySession[targetSessionKey]
	out := make([]SessionBindingRecord, 0, len(ids))
	for _, id := range ids {
		if rec := a.bindings[id]; rec != nil {
			out = append(out, rec.binding)
		}
	}
	return out
}

func (a *ChannelThreadBindingAdapter) ResolveByConversation(ref ThreadBindingConversationRef) (SessionBindingRecord, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.evictExpiredLocked()

	convoID := strings.TrimSpace(ref.ConversationID)
	if convoID == "" {
		return SessionBindingRecord{}, false
	}
	bindingID, ok := a.byConvo[convoID]
	if !ok {
		return SessionBindingRecord{}, false
	}
	rec := a.bindings[bindingID]
	if rec == nil {
		return SessionBindingRecord{}, false
	}
	return rec.binding, true
}

func (a *ChannelThreadBindingAdapter) Touch(bindingID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	rec := a.bindings[bindingID]
	if rec == nil {
		return false
	}
	rec.lastActivityAt = time.Now().UTC()
	rec.binding.LastActivityAt = rec.lastActivityAt
	return true
}

func (a *ChannelThreadBindingAdapter) Unbind(targetSessionKey string, reason string) ([]SessionBindingRecord, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	ids := a.bySession[targetSessionKey]
	removed := make([]SessionBindingRecord, 0, len(ids))
	for _, id := range ids {
		if rec := a.bindings[id]; rec != nil {
			removed = append(removed, rec.binding)
			a.unbindRecordLocked(id)
		}
	}
	delete(a.bySession, targetSessionKey)
	return removed, nil
}

// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------

// Focus binds the given conversationID to the target session, replacing
// any existing binding for that conversation.
func (a *ChannelThreadBindingAdapter) Focus(input BindThreadSessionInput) (SessionBindingRecord, error) {
	return a.Bind(input)
}

// Unfocus removes the binding for the given conversationID.
func (a *ChannelThreadBindingAdapter) Unfocus(conversationID string) (SessionBindingRecord, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	convoID := strings.TrimSpace(conversationID)
	if convoID == "" {
		return SessionBindingRecord{}, false
	}
	bindingID, ok := a.byConvo[convoID]
	if !ok {
		return SessionBindingRecord{}, false
	}
	rec := a.bindings[bindingID]
	if rec == nil {
		return SessionBindingRecord{}, false
	}
	removed := rec.binding
	a.unbindRecordLocked(bindingID)
	return removed, true
}

// Rebind removes any existing binding for the conversation, then creates
// a new one targeting a different session.
func (a *ChannelThreadBindingAdapter) Rebind(input BindThreadSessionInput, reason string) (SessionBindingRecord, error) {
	convoID := firstNonEmpty(input.ConversationID, input.ThreadID)
	if convoID != "" {
		a.mu.Lock()
		if existingID, ok := a.byConvo[convoID]; ok {
			a.unbindRecordLocked(existingID)
		}
		a.mu.Unlock()
	}
	return a.Bind(input)
}

// ---------------------------------------------------------------------------
// Internal
// ---------------------------------------------------------------------------

func (a *ChannelThreadBindingAdapter) unbindRecordLocked(bindingID string) {
	rec := a.bindings[bindingID]
	if rec == nil {
		return
	}
	if rec.conversationID != "" {
		if a.byConvo[rec.conversationID] == bindingID {
			delete(a.byConvo, rec.conversationID)
		}
	}
	targetKey := rec.binding.TargetSessionKey
	if ids, ok := a.bySession[targetKey]; ok {
		filtered := ids[:0]
		for _, id := range ids {
			if id != bindingID {
				filtered = append(filtered, id)
			}
		}
		if len(filtered) == 0 {
			delete(a.bySession, targetKey)
		} else {
			a.bySession[targetKey] = filtered
		}
	}
	delete(a.bindings, bindingID)
}

func (a *ChannelThreadBindingAdapter) evictExpiredLocked() {
	now := time.Now().UTC()
	var expired []string
	for id, rec := range a.bindings {
		if rec.idleTimeoutMs > 0 {
			idleDeadline := rec.lastActivityAt.Add(time.Duration(rec.idleTimeoutMs) * time.Millisecond)
			if now.After(idleDeadline) {
				expired = append(expired, id)
				continue
			}
		}
		if rec.maxAgeMs > 0 {
			maxDeadline := rec.boundAt.Add(time.Duration(rec.maxAgeMs) * time.Millisecond)
			if now.After(maxDeadline) {
				expired = append(expired, id)
				continue
			}
		}
	}
	for _, id := range expired {
		a.unbindRecordLocked(id)
	}
}
