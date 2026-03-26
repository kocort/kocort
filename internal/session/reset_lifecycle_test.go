package session

import (
	"errors"
	"testing"

	"github.com/kocort/kocort/internal/core"
)

type resetLifecycleStoreStub struct {
	history       []core.TranscriptMessage
	loadErr       error
	resetErr      error
	acpResetErr   error
	gotKey        string
	gotReason     string
	nextSessionID string
	acpCalled     bool
}

func (s *resetLifecycleStoreStub) LoadTranscript(sessionKey string) ([]core.TranscriptMessage, error) {
	s.gotKey = sessionKey
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return s.history, nil
}

func (s *resetLifecycleStoreStub) Reset(sessionKey string, reason string) (string, error) {
	s.gotKey = sessionKey
	s.gotReason = reason
	if s.resetErr != nil {
		return "", s.resetErr
	}
	return s.nextSessionID, nil
}

func (s *resetLifecycleStoreStub) ResetACPBoundSession(sess core.SessionResolution, reason string) (string, error) {
	s.gotKey = sess.SessionKey
	s.gotReason = reason
	s.acpCalled = true
	if s.acpResetErr != nil {
		return "", s.acpResetErr
	}
	return s.nextSessionID, nil
}

func TestExecuteSessionReset(t *testing.T) {
	store := &resetLifecycleStoreStub{
		history:       []core.TranscriptMessage{{Text: "hello"}},
		nextSessionID: "sess-next",
	}
	execution, err := ExecuteSessionReset(store, core.SessionResolution{SessionKey: "agent:main:main"}, SessionResetCommandMatch{
		Trigger: "/reset",
		Reason:  "reset",
	})
	if err != nil {
		t.Fatalf("ExecuteSessionReset: %v", err)
	}
	if store.gotKey != "agent:main:main" || store.gotReason != "reset" {
		t.Fatalf("unexpected store calls: key=%q reason=%q", store.gotKey, store.gotReason)
	}
	if execution.NextSessionID != "sess-next" || len(execution.History) != 1 {
		t.Fatalf("unexpected execution result: %+v", execution)
	}
}

func TestExecuteSessionResetPropagatesLoadError(t *testing.T) {
	store := &resetLifecycleStoreStub{loadErr: errors.New("boom")}
	_, err := ExecuteSessionReset(store, core.SessionResolution{SessionKey: "agent:main:main"}, SessionResetCommandMatch{
		Trigger: "/reset",
		Reason:  "reset",
	})
	if err == nil {
		t.Fatal("expected load error")
	}
}

func TestSessionResetExecutionImmediateResult(t *testing.T) {
	result := SessionResetExecution{
		Command: SessionResetCommandMatch{Trigger: "/new", Reason: "new"},
	}.ImmediateResult("run-1")
	if result.RunID != "run-1" || len(result.Payloads) != 1 || result.Payloads[0].Text != "Started a new session." {
		t.Fatalf("unexpected immediate result: %+v", result)
	}
}

func TestExecuteSessionResetUsesACPResetPathWhenAvailable(t *testing.T) {
	store := &resetLifecycleStoreStub{
		history:       []core.TranscriptMessage{{Text: "hello"}},
		nextSessionID: "sess-acp-next",
	}
	execution, err := ExecuteSessionReset(store, core.SessionResolution{
		SessionKey: "agent:main:acp:test",
		Entry:      &core.SessionEntry{ACP: &core.AcpSessionMeta{Backend: "acp-live"}},
	}, SessionResetCommandMatch{
		Trigger: "/reset",
		Reason:  "reset",
	})
	if err != nil {
		t.Fatalf("ExecuteSessionReset ACP path: %v", err)
	}
	if !store.acpCalled || execution.NextSessionID != "sess-acp-next" {
		t.Fatalf("expected ACP reset path, got store=%+v execution=%+v", store, execution)
	}
}

func TestResolveEffectiveACPResetSessionUsesBoundACPChild(t *testing.T) {
	store := newTestStore(t)
	if err := store.Upsert("agent:main:main", core.SessionEntry{SessionID: "sess-parent"}); err != nil {
		t.Fatalf("upsert parent: %v", err)
	}
	if err := store.Upsert("agent:worker:acp:acp-live:test", core.SessionEntry{
		SessionID: "sess-acp",
		ACP:       &core.AcpSessionMeta{Backend: "acp-live"},
	}); err != nil {
		t.Fatalf("upsert acp child: %v", err)
	}
	if err := store.UpsertSessionBinding(SessionBindingUpsert{
		TargetSessionKey: "agent:worker:acp:acp-live:test",
		TargetKind:       "session",
		Channel:          "discord",
		AccountID:        "acct-1",
		ThreadID:         "thread-1",
		ConversationID:   "thread-1",
	}); err != nil {
		t.Fatalf("upsert binding: %v", err)
	}
	resolved := ResolveEffectiveACPResetSession(store, core.SessionResolution{
		SessionKey: "agent:main:main",
		SessionID:  "sess-parent",
		Entry:      &core.SessionEntry{SessionID: "sess-parent"},
	}, BoundSessionLookupOptions{
		Channel:   "discord",
		AccountID: "acct-1",
		ThreadID:  "thread-1",
	})
	if resolved.SessionKey != "agent:worker:acp:acp-live:test" || resolved.Entry == nil || resolved.Entry.ACP == nil {
		t.Fatalf("expected ACP reset target resolution to use bound child, got %+v", resolved)
	}
}
