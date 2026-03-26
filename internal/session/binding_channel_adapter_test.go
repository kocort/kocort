package session

import (
	"testing"
	"time"
)

func TestChannelThreadBindingAdapter_BindAndResolve(t *testing.T) {
	adapter := NewChannelThreadBindingAdapter(ChannelThreadBindingAdapterConfig{
		Channel:         "discord",
		AccountID:       "acct-1",
		BindSupported:   true,
		UnbindSupported: true,
	})

	rec, err := adapter.Bind(BindThreadSessionInput{
		TargetSessionKey:    "agent:worker:subagent:abc",
		RequesterSessionKey: "agent:main:main",
		TargetKind:          "subagent",
		Placement:           ThreadBindingPlacementChild,
		Channel:             "discord",
		AccountID:           "acct-1",
		ConversationID:      "thread-123",
		ThreadID:            "thread-123",
	})
	if err != nil {
		t.Fatalf("bind failed: %v", err)
	}
	if rec.BindingID == "" {
		t.Fatal("expected non-empty binding ID")
	}

	// Resolve by conversation.
	found, ok := adapter.ResolveByConversation(ThreadBindingConversationRef{
		Channel:        "discord",
		AccountID:      "acct-1",
		ConversationID: "thread-123",
	})
	if !ok {
		t.Fatal("expected to find binding by conversation")
	}
	if found.TargetSessionKey != "agent:worker:subagent:abc" {
		t.Fatalf("unexpected target: %s", found.TargetSessionKey)
	}

	// List by session.
	list := adapter.ListBySession("agent:worker:subagent:abc")
	if len(list) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(list))
	}
}

func TestChannelThreadBindingAdapter_FocusUnfocus(t *testing.T) {
	adapter := NewChannelThreadBindingAdapter(ChannelThreadBindingAdapterConfig{
		Channel:         "slack",
		AccountID:       "acct-2",
		BindSupported:   true,
		UnbindSupported: true,
	})

	_, err := adapter.Focus(BindThreadSessionInput{
		TargetSessionKey: "agent:main:subagent:child-1",
		ConversationID:   "thread-456",
		ThreadID:         "thread-456",
		Channel:          "slack",
		AccountID:        "acct-2",
	})
	if err != nil {
		t.Fatalf("focus failed: %v", err)
	}

	// Should resolve.
	found, ok := adapter.ResolveByConversation(ThreadBindingConversationRef{
		ConversationID: "thread-456",
	})
	if !ok || found.TargetSessionKey != "agent:main:subagent:child-1" {
		t.Fatalf("expected child-1, got %v ok=%v", found.TargetSessionKey, ok)
	}

	// Unfocus.
	removed, ok := adapter.Unfocus("thread-456")
	if !ok {
		t.Fatal("expected unfocus to succeed")
	}
	if removed.TargetSessionKey != "agent:main:subagent:child-1" {
		t.Fatalf("unexpected removed target: %s", removed.TargetSessionKey)
	}

	// Should no longer resolve.
	_, ok = adapter.ResolveByConversation(ThreadBindingConversationRef{
		ConversationID: "thread-456",
	})
	if ok {
		t.Fatal("expected no binding after unfocus")
	}
}

func TestChannelThreadBindingAdapter_IdleExpiry(t *testing.T) {
	adapter := NewChannelThreadBindingAdapter(ChannelThreadBindingAdapterConfig{
		Channel:         "telegram",
		AccountID:       "acct-3",
		BindSupported:   true,
		UnbindSupported: true,
	})

	_, err := adapter.Bind(BindThreadSessionInput{
		TargetSessionKey: "agent:main:subagent:expire-test",
		ConversationID:   "thread-789",
		ThreadID:         "thread-789",
		Channel:          "telegram",
		AccountID:        "acct-3",
		IdleTimeoutMs:    1, // 1ms idle timeout → will expire immediately
	})
	if err != nil {
		t.Fatalf("bind failed: %v", err)
	}

	// Wait a bit for expiry.
	time.Sleep(5 * time.Millisecond)

	// Should not resolve anymore (idle expired).
	_, ok := adapter.ResolveByConversation(ThreadBindingConversationRef{
		ConversationID: "thread-789",
	})
	if ok {
		t.Fatal("expected binding to be expired")
	}
}

func TestChannelThreadBindingAdapter_Rebind(t *testing.T) {
	adapter := NewChannelThreadBindingAdapter(ChannelThreadBindingAdapterConfig{
		Channel:         "discord",
		AccountID:       "acct-4",
		BindSupported:   true,
		UnbindSupported: true,
	})

	// Bind to child-1.
	_, _ = adapter.Bind(BindThreadSessionInput{
		TargetSessionKey: "agent:main:subagent:child-1",
		ConversationID:   "thread-abc",
		ThreadID:         "thread-abc",
		Channel:          "discord",
		AccountID:        "acct-4",
	})

	// Rebind to child-2.
	rec, err := adapter.Rebind(BindThreadSessionInput{
		TargetSessionKey: "agent:main:subagent:child-2",
		ConversationID:   "thread-abc",
		ThreadID:         "thread-abc",
		Channel:          "discord",
		AccountID:        "acct-4",
	}, "test-rebind")
	if err != nil {
		t.Fatalf("rebind failed: %v", err)
	}
	if rec.TargetSessionKey != "agent:main:subagent:child-2" {
		t.Fatalf("expected child-2, got %s", rec.TargetSessionKey)
	}

	// Should resolve to child-2.
	found, ok := adapter.ResolveByConversation(ThreadBindingConversationRef{
		ConversationID: "thread-abc",
	})
	if !ok || found.TargetSessionKey != "agent:main:subagent:child-2" {
		t.Fatalf("expected child-2 after rebind, got %v ok=%v", found.TargetSessionKey, ok)
	}
}
