package session

import "testing"

type resetOnlyStoreStub struct {
	gotKey    string
	gotReason string
	err       error
}

func (s *resetOnlyStoreStub) Reset(sessionKey string, reason string) (string, error) {
	s.gotKey = sessionKey
	s.gotReason = reason
	return "next", s.err
}

func TestResolveRecoveryResetPlan(t *testing.T) {
	tests := []struct {
		reason string
		want   string
	}{
		{reason: "context_overflow", want: "overflow"},
		{reason: "role_ordering", want: "reset"},
		{reason: "session_corruption", want: "reset"},
	}
	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			plan, ok := ResolveRecoveryResetPlan(tt.reason)
			if !ok || plan.Reason != tt.want || plan.ReplyText == "" {
				t.Fatalf("unexpected plan: %+v %v", plan, ok)
			}
		})
	}
}

func TestExecuteRecoveryReset(t *testing.T) {
	store := &resetOnlyStoreStub{}
	result, handled, err := ExecuteRecoveryReset(store, "agent:main:main", "run-1", RecoveryResetPlan{
		Reason:    "overflow",
		ReplyText: "reset",
	})
	if err != nil || !handled {
		t.Fatalf("unexpected execution result: handled=%v err=%v", handled, err)
	}
	if store.gotKey != "agent:main:main" || store.gotReason != "overflow" {
		t.Fatalf("unexpected reset call: key=%q reason=%q", store.gotKey, store.gotReason)
	}
	if result.RunID != "run-1" || len(result.Payloads) != 1 || result.Payloads[0].Text != "reset" {
		t.Fatalf("unexpected tool result: %+v", result)
	}
}
