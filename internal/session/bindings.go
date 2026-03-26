package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type SessionBindingRecord struct {
	BindingID           string    `json:"bindingId"`
	TargetSessionKey    string    `json:"targetSessionKey"`
	RequesterSessionKey string    `json:"requesterSessionKey,omitempty"`
	TargetKind          string    `json:"targetKind,omitempty"`
	Status              string    `json:"status,omitempty"`
	BoundBy             string    `json:"boundBy,omitempty"`
	Placement           string    `json:"placement,omitempty"`
	Channel             string    `json:"channel"`
	ConversationID      string    `json:"conversationId,omitempty"`
	ParentConversationID string   `json:"parentConversationId,omitempty"`
	To                  string    `json:"to,omitempty"`
	AccountID           string    `json:"accountId,omitempty"`
	ThreadID            string    `json:"threadId"`
	IdleTimeoutMs       int64     `json:"idleTimeoutMs,omitempty"`
	MaxAgeMs            int64     `json:"maxAgeMs,omitempty"`
	IntroText           string    `json:"introText,omitempty"`
	FarewellText        string    `json:"farewellText,omitempty"`
	BoundAt             time.Time `json:"boundAt"`
	LastActivityAt      time.Time `json:"lastActivityAt,omitempty"`
	ExpiresAt           time.Time `json:"expiresAt,omitempty"`
}

type SessionBindingUpsert struct {
	TargetSessionKey    string
	RequesterSessionKey string
	TargetKind          string
	Status              string
	BoundBy             string
	Placement           string
	Channel             string
	ConversationID      string
	ParentConversationID string
	To                  string
	AccountID           string
	ThreadID            string
	IdleTimeoutMs       int64
	MaxAgeMs            int64
	Label               string
	AgentID             string
	BoundAt             time.Time
	LastActivityAt      time.Time
}

type SessionBindingLookup struct {
	Channel   string
	To        string
	AccountID string
	ThreadID  string
}

func (s *SessionStore) UpsertSessionBinding(input SessionBindingUpsert) error {
	if s == nil {
		return nil
	}
	record, ok := buildSessionBindingRecord(input)
	if !ok {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bindings[record.BindingID] = record
	return s.flushBindingsLocked()
}

func (s *SessionStore) ResolveSessionBinding(input SessionBindingLookup) (SessionBindingRecord, bool) {
	if s == nil {
		return SessionBindingRecord{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok, changed := s.resolveSessionBindingLocked(input, time.Now().UTC())
	if changed {
		_ = s.flushBindingsLocked()
	}
	return record, ok
}

func (s *SessionStore) ListSessionBindingsForTargetSession(targetSessionKey string) []SessionBindingRecord {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	out := make([]SessionBindingRecord, 0)
	changed := false
	target := strings.TrimSpace(targetSessionKey)
	for bindingID, record := range s.bindings {
		if strings.TrimSpace(record.TargetSessionKey) != target {
			continue
		}
		if isSessionBindingExpired(record, now) {
			delete(s.bindings, bindingID)
			changed = true
			continue
		}
		out = append(out, record)
	}
	if changed {
		_ = s.flushBindingsLocked()
	}
	return out
}

func (s *SessionStore) UnbindSessionBindingsByTargetSession(targetSessionKey string, reason string) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	target := strings.TrimSpace(targetSessionKey)
	if target == "" {
		return 0
	}
	removed := 0
	for bindingID, record := range s.bindings {
		if strings.TrimSpace(record.TargetSessionKey) != target {
			continue
		}
		record.Status = "ended"
		record.FarewellText = ResolveThreadBindingFarewellText(ThreadBindingFarewellParams{
			Reason:        strings.TrimSpace(reason),
			IdleTimeoutMs: record.IdleTimeoutMs,
			MaxAgeMs:      record.MaxAgeMs,
		})
		delete(s.bindings, bindingID)
		removed++
	}
	if removed > 0 {
		_ = s.flushBindingsLocked()
	}
	return removed
}

func (s *SessionStore) TouchSessionBindingActivity(input SessionBindingLookup, now time.Time) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok, changed := s.resolveSessionBindingLocked(input, now)
	if !ok {
		if changed {
			_ = s.flushBindingsLocked()
		}
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	record.LastActivityAt = now.UTC()
	s.bindings[record.BindingID] = record
	_ = s.flushBindingsLocked()
	return true
}

func (s *SessionStore) loadBindings() error {
	if strings.TrimSpace(s.bindingPath) == "" {
		return nil
	}
	data, err := os.ReadFile(s.bindingPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, &s.bindings)
}

func (s *SessionStore) flushBindingsLocked() error {
	if strings.TrimSpace(s.bindingPath) == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.bindings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.bindingPath, data, 0o644)
}

func (s *SessionStore) deleteBindingsLocked(targetSessionKey string) {
	target := strings.TrimSpace(targetSessionKey)
	if target == "" {
		return
	}
	for bindingID, record := range s.bindings {
		if strings.TrimSpace(record.TargetSessionKey) == target {
			delete(s.bindings, bindingID)
		}
	}
}

func (s *SessionStore) resolveSessionBindingLocked(input SessionBindingLookup, now time.Time) (SessionBindingRecord, bool, bool) {
	channel := strings.TrimSpace(input.Channel)
	threadID := strings.TrimSpace(input.ThreadID)
	if channel == "" || threadID == "" {
		return SessionBindingRecord{}, false, false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	to := strings.TrimSpace(input.To)
	accountID := strings.TrimSpace(input.AccountID)
	best := SessionBindingRecord{}
	found := false
	changed := false
	for _, record := range s.bindings {
		if strings.TrimSpace(record.Channel) != channel || strings.TrimSpace(record.ThreadID) != threadID {
			continue
		}
		if to != "" && strings.TrimSpace(record.To) != "" && strings.TrimSpace(record.To) != to {
			continue
		}
		if accountID != "" && strings.TrimSpace(record.AccountID) != "" && strings.TrimSpace(record.AccountID) != accountID {
			continue
		}
		if expiredReason, expired := resolveSessionBindingExpiryReason(record, now); expired || strings.TrimSpace(record.TargetSessionKey) == "" {
			if expired {
				record.Status = "ended"
				record.FarewellText = ResolveThreadBindingFarewellText(ThreadBindingFarewellParams{
					Reason:        expiredReason,
					IdleTimeoutMs: record.IdleTimeoutMs,
					MaxAgeMs:      record.MaxAgeMs,
				})
			}
			delete(s.bindings, record.BindingID)
			changed = true
			continue
		}
		if !found || record.LastActivityAt.After(best.LastActivityAt) || (best.LastActivityAt.IsZero() && record.BoundAt.After(best.BoundAt)) {
			best = record
			found = true
		}
	}
	return best, found, changed
}

func buildSessionBindingRecord(input SessionBindingUpsert) (SessionBindingRecord, bool) {
	channel := strings.TrimSpace(input.Channel)
	threadID := strings.TrimSpace(input.ThreadID)
	targetSessionKey := strings.TrimSpace(input.TargetSessionKey)
	if channel == "" || threadID == "" || targetSessionKey == "" {
		return SessionBindingRecord{}, false
	}
	boundAt := input.BoundAt
	if boundAt.IsZero() {
		boundAt = time.Now().UTC()
	}
	lastActivityAt := input.LastActivityAt
	if lastActivityAt.IsZero() {
		lastActivityAt = boundAt
	}
	idleTimeoutMs := normalizeSessionBindingDurationMs(input.IdleTimeoutMs)
	maxAgeMs := normalizeSessionBindingDurationMs(input.MaxAgeMs)
	accountID := strings.TrimSpace(input.AccountID)
	if accountID == "" {
		accountID = "default"
	}
	expiresAt := time.Time{}
	if maxAgeMs > 0 {
		expiresAt = boundAt.UTC().Add(time.Duration(maxAgeMs) * time.Millisecond)
	}
	boundBy := NormalizeSessionBindingBoundBy(input.BoundBy)
	placement := NormalizeSessionBindingPlacement(input.Placement)
	return SessionBindingRecord{
		BindingID:           fmt.Sprintf("%s:%s:%s", channel, accountID, threadID),
		TargetSessionKey:    targetSessionKey,
		RequesterSessionKey: strings.TrimSpace(input.RequesterSessionKey),
		TargetKind:          normalizeSessionBindingTargetKind(input.TargetKind),
		Status:              normalizeSessionBindingStatus(input.Status),
		BoundBy:             boundBy,
		Placement:           placement,
		Channel:             channel,
		ConversationID:      strings.TrimSpace(input.ConversationID),
		ParentConversationID: strings.TrimSpace(input.ParentConversationID),
		To:                  strings.TrimSpace(input.To),
		AccountID:           accountID,
		ThreadID:            threadID,
		IdleTimeoutMs:       idleTimeoutMs,
		MaxAgeMs:            maxAgeMs,
		IntroText: ResolveThreadBindingIntroText(ThreadBindingIntroParams{
			AgentID:       strings.TrimSpace(input.AgentID),
			Label:         strings.TrimSpace(input.Label),
			IdleTimeoutMs: idleTimeoutMs,
			MaxAgeMs:      maxAgeMs,
		}),
		BoundAt:        boundAt.UTC(),
		LastActivityAt: lastActivityAt.UTC(),
		ExpiresAt:      expiresAt,
	}, true
}
