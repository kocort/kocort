package session

import (
	"strings"

	"github.com/kocort/kocort/internal/core"
)

// RecoveryResetPlan describes how to recover from a backend/session failure by
// resetting the current session.
type RecoveryResetPlan struct {
	Reason    string
	ReplyText string
}

// ResetOnlyStore is the minimal session store surface needed for recovery
// resets.
type ResetOnlyStore interface {
	Reset(sessionKey string, reason string) (string, error)
}

// ResolveRecoveryResetPlan maps a backend/session failure reason to the
// session reset reason and user-facing reply text.
func ResolveRecoveryResetPlan(reason string) (RecoveryResetPlan, bool) {
	switch strings.TrimSpace(strings.ToLower(reason)) {
	case "context_overflow":
		return RecoveryResetPlan{
			Reason:    "overflow",
			ReplyText: "⚠️ Context limit exceeded. I've reset our conversation to start fresh - please try again.",
		}, true
	case "role_ordering":
		return RecoveryResetPlan{
			Reason:    "reset",
			ReplyText: "⚠️ Message ordering conflict. I've reset the conversation - please try again.",
		}, true
	case "session_corruption":
		return RecoveryResetPlan{
			Reason:    "reset",
			ReplyText: "⚠️ Session history was corrupted. I've reset the conversation - please try again.",
		}, true
	default:
		return RecoveryResetPlan{}, false
	}
}

// ExecuteRecoveryReset applies a recovery reset plan to the session store and
// returns the user-facing result payload.
func ExecuteRecoveryReset(store ResetOnlyStore, sessionKey string, runID string, plan RecoveryResetPlan) (core.AgentRunResult, bool, error) {
	if _, err := store.Reset(sessionKey, plan.Reason); err != nil {
		return core.AgentRunResult{}, false, err
	}
	return core.AgentRunResult{
		RunID:    runID,
		Payloads: []core.ReplyPayload{{Text: plan.ReplyText}},
	}, true, nil
}
