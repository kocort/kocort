package session

import (
	"fmt"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
)

type fakeThreadBindingAdapter struct {
	channel string
	account string
	records map[string]SessionBindingRecord
	touched []string
	unbound []string
}

func (f *fakeThreadBindingAdapter) Channel() string   { return f.channel }
func (f *fakeThreadBindingAdapter) AccountID() string { return f.account }
func (f *fakeThreadBindingAdapter) Capabilities() ThreadBindingAdapterCapabilities {
	return ThreadBindingAdapterCapabilities{
		Placements:      []ThreadBindingPlacement{ThreadBindingPlacementChild},
		BindSupported:   true,
		UnbindSupported: true,
	}
}
func (f *fakeThreadBindingAdapter) Bind(input BindThreadSessionInput) (SessionBindingRecord, error) {
	if f.records == nil {
		f.records = map[string]SessionBindingRecord{}
	}
	record := SessionBindingRecord{
		BindingID:           fmt.Sprintf("%s:%s", input.Channel, input.ThreadID),
		TargetSessionKey:    input.TargetSessionKey,
		RequesterSessionKey: input.RequesterSessionKey,
		TargetKind:          input.TargetKind,
		Status:              "active",
		BoundBy:             "adapter",
		Placement:           string(input.Placement),
		Channel:             input.Channel,
		AccountID:           input.AccountID,
		ThreadID:            input.ThreadID,
		ConversationID:      firstNonEmpty(input.ConversationID, input.ThreadID),
		BoundAt:             time.Now().UTC(),
		LastActivityAt:      time.Now().UTC(),
	}
	f.records[record.BindingID] = record
	return record, nil
}
func (f *fakeThreadBindingAdapter) ListBySession(targetSessionKey string) []SessionBindingRecord {
	out := make([]SessionBindingRecord, 0)
	for _, record := range f.records {
		if record.TargetSessionKey == targetSessionKey {
			out = append(out, record)
		}
	}
	return out
}
func (f *fakeThreadBindingAdapter) ResolveByConversation(ref ThreadBindingConversationRef) (SessionBindingRecord, bool) {
	for _, record := range f.records {
		if record.Channel == ref.Channel && record.AccountID == ref.AccountID && record.ConversationID == ref.ConversationID {
			return record, true
		}
	}
	return SessionBindingRecord{}, false
}
func (f *fakeThreadBindingAdapter) Touch(bindingID string) bool {
	if _, ok := f.records[bindingID]; !ok {
		return false
	}
	f.touched = append(f.touched, bindingID)
	return true
}
func (f *fakeThreadBindingAdapter) Unbind(targetSessionKey string, _ string) ([]SessionBindingRecord, error) {
	removed := make([]SessionBindingRecord, 0)
	for id, record := range f.records {
		if record.TargetSessionKey != targetSessionKey {
			continue
		}
		removed = append(removed, record)
		f.unbound = append(f.unbound, id)
		delete(f.records, id)
	}
	return removed, nil
}

func TestThreadBindingServiceBindResolveTouchAndUnbind(t *testing.T) {
	store := &SessionStore{
		entries:   map[string]core.SessionEntry{},
		bindings:  map[string]SessionBindingRecord{},
		storePath: "",
	}
	svc := NewThreadBindingService(store)
	if !svc.Capabilities().SupportsBinding {
		t.Fatal("expected binding capability")
	}
	if err := svc.BindThreadSession(BindThreadSessionInput{
		TargetSessionKey:    "agent:worker:subagent:test",
		RequesterSessionKey: "agent:main:main",
		TargetKind:          "subagent",
		Placement:           ThreadBindingPlacementChild,
		Channel:             "discord",
		To:                  "room-1",
		ThreadID:            "thread-1",
	}); err != nil {
		t.Fatalf("bind thread session: %v", err)
	}
	key, ok := svc.ResolveThreadSession(BoundSessionLookupOptions{
		Channel:  "discord",
		To:       "room-1",
		ThreadID: "thread-1",
	})
	if !ok || key != "agent:worker:subagent:test" {
		t.Fatalf("unexpected bound resolution: %q %v", key, ok)
	}
	if !svc.TouchThreadBinding(BoundSessionLookupOptions{
		Channel:  "discord",
		To:       "room-1",
		ThreadID: "thread-1",
	}) {
		t.Fatal("expected touch to succeed")
	}
	if removed := svc.UnbindTargetSession("agent:worker:subagent:test", "killed"); removed != 1 {
		t.Fatalf("expected unbind to remove 1 binding, got %d", removed)
	}
}

func TestThreadBindingServiceUsesRegisteredAdapter(t *testing.T) {
	store := &SessionStore{
		entries:   map[string]core.SessionEntry{},
		bindings:  map[string]SessionBindingRecord{},
		storePath: "",
	}
	adapter := &fakeThreadBindingAdapter{channel: "discord", account: "acct-1", records: map[string]SessionBindingRecord{}}
	RegisterThreadBindingAdapter(adapter)
	defer UnregisterThreadBindingAdapter("discord", "acct-1")
	svc := NewThreadBindingService(store)

	caps := svc.CapabilitiesFor("discord", "acct-1")
	if !caps.AdapterAvailable || !caps.SupportsBinding || len(caps.Placements) != 1 || caps.Placements[0] != ThreadBindingPlacementChild {
		t.Fatalf("unexpected adapter capabilities: %+v", caps)
	}
	if err := svc.BindThreadSession(BindThreadSessionInput{
		TargetSessionKey:    "agent:worker:acp:test",
		RequesterSessionKey: "agent:main:main",
		TargetKind:          string(ThreadBindingTargetKindSession),
		Placement:           ThreadBindingPlacementChild,
		Channel:             "discord",
		AccountID:           "acct-1",
		ThreadID:            "thread-42",
		ConversationID:      "thread-42",
	}); err != nil {
		t.Fatalf("bind via adapter: %v", err)
	}
	key, ok := svc.ResolveThreadSession(BoundSessionLookupOptions{
		Channel:   "discord",
		AccountID: "acct-1",
		ThreadID:  "thread-42",
	})
	if !ok || key != "agent:worker:acp:test" {
		t.Fatalf("unexpected adapter resolution: %q %v", key, ok)
	}
	if !svc.TouchThreadBinding(BoundSessionLookupOptions{
		Channel:   "discord",
		AccountID: "acct-1",
		ThreadID:  "thread-42",
	}) {
		t.Fatal("expected adapter touch to succeed")
	}
	if len(adapter.touched) != 1 {
		t.Fatalf("expected adapter touch call, got %d", len(adapter.touched))
	}
	bindings := svc.ListBindingsForTargetSession("agent:worker:acp:test")
	if len(bindings) == 0 {
		t.Fatal("expected adapter-backed binding in listing")
	}
	if removed := svc.UnbindTargetSession("agent:worker:acp:test", "ended"); removed < 1 {
		t.Fatalf("expected adapter unbind to remove at least one binding, got %d", removed)
	}
}

func TestThreadBindingServiceRebindsExistingConversation(t *testing.T) {
	store := &SessionStore{
		entries:   map[string]core.SessionEntry{},
		bindings:  map[string]SessionBindingRecord{},
		storePath: "",
	}
	svc := NewThreadBindingService(store)
	if err := svc.BindThreadSession(BindThreadSessionInput{
		TargetSessionKey:    "agent:worker:subagent:one",
		RequesterSessionKey: "agent:main:main",
		TargetKind:          "subagent",
		Placement:           ThreadBindingPlacementCurrent,
		Channel:             "discord",
		To:                  "room-1",
		ThreadID:            "thread-1",
	}); err != nil {
		t.Fatalf("initial bind: %v", err)
	}
	if err := svc.RebindThreadSession(BindThreadSessionInput{
		TargetSessionKey:    "agent:worker:subagent:two",
		RequesterSessionKey: "agent:main:main",
		TargetKind:          "subagent",
		Placement:           ThreadBindingPlacementCurrent,
		Channel:             "discord",
		To:                  "room-1",
		ThreadID:            "thread-1",
	}, "focus"); err != nil {
		t.Fatalf("rebind thread session: %v", err)
	}
	key, ok := svc.ResolveThreadSession(BoundSessionLookupOptions{
		Channel:  "discord",
		To:       "room-1",
		ThreadID: "thread-1",
	})
	if !ok || key != "agent:worker:subagent:two" {
		t.Fatalf("unexpected rebound resolution: %q %v", key, ok)
	}
	bindings := svc.ListBindingsForTargetSession("agent:worker:subagent:one")
	if len(bindings) != 0 {
		t.Fatalf("expected old target bindings removed after rebind, got %+v", bindings)
	}
}
