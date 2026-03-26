package session

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type ThreadBindingPlacement string

const (
	ThreadBindingPlacementCurrent ThreadBindingPlacement = "current"
	ThreadBindingPlacementChild   ThreadBindingPlacement = "child"
)

type ThreadBindingCapabilities struct {
	AdapterAvailable bool                     `json:"adapterAvailable"`
	SupportsBinding  bool                     `json:"supportsBinding"`
	SupportsRebind   bool                     `json:"supportsRebind"`
	SupportsUnbind   bool                     `json:"supportsUnbind"`
	SupportsIntro    bool                     `json:"supportsIntro"`
	SupportsFarewell bool                     `json:"supportsFarewell"`
	Placements       []ThreadBindingPlacement `json:"placements,omitempty"`
}

type ThreadBindingErrorCode string

const (
	ThreadBindingAdapterUnavailable    ThreadBindingErrorCode = "BINDING_ADAPTER_UNAVAILABLE"
	ThreadBindingCapabilityUnsupported ThreadBindingErrorCode = "BINDING_CAPABILITY_UNSUPPORTED"
	ThreadBindingCreateFailed          ThreadBindingErrorCode = "BINDING_CREATE_FAILED"
)

type ThreadBindingError struct {
	Code      ThreadBindingErrorCode
	Message   string
	Channel   string
	AccountID string
	Placement ThreadBindingPlacement
}

func (e *ThreadBindingError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func IsThreadBindingError(err error) bool {
	var target *ThreadBindingError
	return errors.As(err, &target)
}

type ThreadBindingService struct {
	store *SessionStore
}

func NewThreadBindingService(store *SessionStore) *ThreadBindingService {
	return &ThreadBindingService{store: store}
}

func (s *ThreadBindingService) Capabilities() ThreadBindingCapabilities {
	return ThreadBindingCapabilities{
		AdapterAvailable: s != nil && s.store != nil,
		SupportsBinding:  s != nil && s.store != nil,
		SupportsRebind:   s != nil && s.store != nil,
		SupportsUnbind:   s != nil && s.store != nil,
		SupportsIntro:    true,
		SupportsFarewell: true,
		Placements:       []ThreadBindingPlacement{ThreadBindingPlacementCurrent, ThreadBindingPlacementChild},
	}
}

func (s *ThreadBindingService) CapabilitiesFor(channel string, accountID string) ThreadBindingCapabilities {
	if adapter, ok := resolveThreadBindingAdapter(channel, accountID); ok {
		adapterCaps := adapter.Capabilities()
		placements := normalizeThreadBindingPlacements(adapterCaps.Placements)
		return ThreadBindingCapabilities{
			AdapterAvailable: true,
			SupportsBinding:  adapterCaps.BindSupported,
			SupportsRebind:   adapterCaps.BindSupported,
			SupportsUnbind:   adapterCaps.UnbindSupported,
			SupportsIntro:    adapterCaps.BindSupported,
			SupportsFarewell: adapterCaps.UnbindSupported,
			Placements:       placements,
		}
	}
	return s.Capabilities()
}

type BindThreadSessionInput struct {
	TargetSessionKey     string
	RequesterSessionKey  string
	TargetKind           string
	Placement            ThreadBindingPlacement
	Channel              string
	To                   string
	AccountID            string
	ThreadID             string
	ConversationID       string
	ParentConversationID string
	IdleTimeoutMs        int64
	MaxAgeMs             int64
	Label                string
	AgentID              string
}

func (s *ThreadBindingService) BindThreadSession(input BindThreadSessionInput) error {
	if s == nil || s.store == nil {
		return nil
	}
	if adapter, ok := resolveThreadBindingAdapter(input.Channel, input.AccountID); ok {
		caps := s.CapabilitiesFor(input.Channel, input.AccountID)
		if !caps.SupportsBinding {
			return &ThreadBindingError{
				Code:      ThreadBindingCapabilityUnsupported,
				Message:   fmt.Sprintf("Thread bindings do not support binding for %s:%s.", strings.TrimSpace(input.Channel), strings.TrimSpace(input.AccountID)),
				Channel:   strings.TrimSpace(input.Channel),
				AccountID: strings.TrimSpace(input.AccountID),
				Placement: input.Placement,
			}
		}
		if !containsThreadBindingPlacement(caps.Placements, input.Placement) {
			return &ThreadBindingError{
				Code:      ThreadBindingCapabilityUnsupported,
				Message:   fmt.Sprintf("Thread binding placement %q is not supported for %s:%s.", string(input.Placement), strings.TrimSpace(input.Channel), strings.TrimSpace(input.AccountID)),
				Channel:   strings.TrimSpace(input.Channel),
				AccountID: strings.TrimSpace(input.AccountID),
				Placement: input.Placement,
			}
		}
		record, err := adapter.Bind(input)
		if err != nil {
			return err
		}
		if strings.TrimSpace(record.BindingID) == "" {
			return &ThreadBindingError{
				Code:      ThreadBindingCreateFailed,
				Message:   "Thread binding adapter failed to create a binding record.",
				Channel:   strings.TrimSpace(input.Channel),
				AccountID: strings.TrimSpace(input.AccountID),
				Placement: input.Placement,
			}
		}
		return s.store.UpsertSessionBinding(SessionBindingUpsert{
			TargetSessionKey:     record.TargetSessionKey,
			RequesterSessionKey:  record.RequesterSessionKey,
			TargetKind:           record.TargetKind,
			Status:               record.Status,
			BoundBy:              record.BoundBy,
			Placement:            record.Placement,
			Channel:              record.Channel,
			ConversationID:       firstNonEmpty(record.ConversationID, input.ConversationID, input.ThreadID),
			ParentConversationID: firstNonEmpty(record.ParentConversationID, input.ParentConversationID),
			To:                   record.To,
			AccountID:            record.AccountID,
			ThreadID:             firstNonEmpty(record.ThreadID, input.ThreadID),
			IdleTimeoutMs:        record.IdleTimeoutMs,
			MaxAgeMs:             record.MaxAgeMs,
			Label:                input.Label,
			AgentID:              input.AgentID,
			BoundAt:              record.BoundAt,
			LastActivityAt:       record.LastActivityAt,
		})
	}
	return s.store.UpsertSessionBinding(SessionBindingUpsert{
		TargetSessionKey:     input.TargetSessionKey,
		RequesterSessionKey:  input.RequesterSessionKey,
		TargetKind:           input.TargetKind,
		Status:               "active",
		BoundBy:              "system",
		Placement:            string(input.Placement),
		Channel:              input.Channel,
		ConversationID:       firstNonEmpty(input.ConversationID, input.ThreadID),
		ParentConversationID: input.ParentConversationID,
		To:                   input.To,
		AccountID:            input.AccountID,
		ThreadID:             input.ThreadID,
		IdleTimeoutMs:        input.IdleTimeoutMs,
		MaxAgeMs:             input.MaxAgeMs,
		Label:                input.Label,
		AgentID:              input.AgentID,
	})
}

func (s *ThreadBindingService) RebindThreadSession(input BindThreadSessionInput, reason string) error {
	if s == nil || s.store == nil {
		return nil
	}
	currentTarget, found := s.ResolveThreadSession(BoundSessionLookupOptions{
		Channel:   input.Channel,
		To:        input.To,
		AccountID: input.AccountID,
		ThreadID:  input.ThreadID,
	})
	if found && strings.TrimSpace(currentTarget) != "" && currentTarget != input.TargetSessionKey {
		s.UnbindTargetSession(currentTarget, firstNonEmpty(reason, "rebind"))
	}
	return s.BindThreadSession(input)
}

func (s *ThreadBindingService) ResolveThreadSession(opts BoundSessionLookupOptions) (string, bool) {
	if s == nil || s.store == nil {
		return "", false
	}
	if adapter, ok := resolveThreadBindingAdapter(opts.Channel, opts.AccountID); ok {
		record, found := adapter.ResolveByConversation(ThreadBindingConversationRef{
			Channel:        strings.TrimSpace(opts.Channel),
			AccountID:      strings.TrimSpace(opts.AccountID),
			ConversationID: strings.TrimSpace(opts.ThreadID),
		})
		if found && strings.TrimSpace(record.TargetSessionKey) != "" {
			return record.TargetSessionKey, true
		}
	}
	return s.store.ResolveBoundSessionKey(opts)
}

func (s *ThreadBindingService) TouchThreadBinding(opts BoundSessionLookupOptions) bool {
	if s == nil || s.store == nil {
		return false
	}
	if adapter, ok := resolveThreadBindingAdapter(opts.Channel, opts.AccountID); ok {
		record, found := adapter.ResolveByConversation(ThreadBindingConversationRef{
			Channel:        strings.TrimSpace(opts.Channel),
			AccountID:      strings.TrimSpace(opts.AccountID),
			ConversationID: strings.TrimSpace(opts.ThreadID),
		})
		if found && strings.TrimSpace(record.BindingID) != "" && adapter.Touch(record.BindingID) {
			return true
		}
	}
	return s.store.TouchSessionBindingActivity(SessionBindingLookup{
		Channel:   opts.Channel,
		To:        opts.To,
		AccountID: opts.AccountID,
		ThreadID:  opts.ThreadID,
	}, time.Now().UTC())
}

func (s *ThreadBindingService) UnbindTargetSession(targetSessionKey string, reason string) int {
	if s == nil || s.store == nil {
		return 0
	}
	removed := 0
	for _, record := range s.store.ListSessionBindingsForTargetSession(targetSessionKey) {
		if adapter, ok := resolveThreadBindingAdapter(record.Channel, record.AccountID); ok {
			rows, err := adapter.Unbind(targetSessionKey, reason)
			if err == nil {
				removed += len(rows)
			}
		}
	}
	removed += s.store.UnbindSessionBindingsByTargetSession(targetSessionKey, reason)
	return removed
}

func (s *ThreadBindingService) ListBindingsForTargetSession(targetSessionKey string) []SessionBindingRecord {
	if s == nil || s.store == nil || strings.TrimSpace(targetSessionKey) == "" {
		return nil
	}
	records := s.store.ListSessionBindingsForTargetSession(targetSessionKey)
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		if strings.TrimSpace(record.BindingID) != "" {
			seen[strings.TrimSpace(record.BindingID)] = struct{}{}
		}
	}
	threadBindingAdapters.Range(func(_, value any) bool {
		adapter, ok := value.(ThreadBindingAdapter)
		if !ok || adapter == nil {
			return true
		}
		for _, record := range adapter.ListBySession(targetSessionKey) {
			bindingID := strings.TrimSpace(record.BindingID)
			if bindingID != "" {
				if _, exists := seen[bindingID]; exists {
					continue
				}
				seen[bindingID] = struct{}{}
			}
			records = append(records, record)
		}
		return true
	})
	return records
}

func normalizeThreadBindingPlacements(values []ThreadBindingPlacement) []ThreadBindingPlacement {
	if len(values) == 0 {
		return []ThreadBindingPlacement{ThreadBindingPlacementCurrent, ThreadBindingPlacementChild}
	}
	out := make([]ThreadBindingPlacement, 0, len(values))
	seen := map[ThreadBindingPlacement]struct{}{}
	for _, value := range values {
		switch value {
		case ThreadBindingPlacementCurrent, ThreadBindingPlacementChild:
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return []ThreadBindingPlacement{ThreadBindingPlacementCurrent, ThreadBindingPlacementChild}
	}
	return out
}

func containsThreadBindingPlacement(values []ThreadBindingPlacement, want ThreadBindingPlacement) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}
