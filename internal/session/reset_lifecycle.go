package session

import "github.com/kocort/kocort/internal/core"

// ResetLifecycleStore is the minimal session-store surface required to execute
// a reset/new session rollover.
type ResetLifecycleStore interface {
	LoadTranscript(sessionKey string) ([]core.TranscriptMessage, error)
	Reset(sessionKey string, reason string) (string, error)
}

// ACPResetLifecycleStore optionally supports an ACP-specialized reset path for
// ACP-bound sessions that should be reset in-place before the store rolls the
// session state forward.
type ACPResetLifecycleStore interface {
	ResetACPBoundSession(sess core.SessionResolution, reason string) (string, error)
}

// SessionResetExecution describes the result of executing a parsed reset/new
// command against the session store.
type SessionResetExecution struct {
	Command       SessionResetCommandMatch
	History       []core.TranscriptMessage
	NextSessionID string
}

// HasFollowup reports whether the reset command should be followed by a fresh
// run using the stripped remainder.
func (e SessionResetExecution) HasFollowup() bool {
	return e.Command.Remainder != ""
}

// FollowupMessage returns the stripped post-reset message that should be sent
// into the next run when present.
func (e SessionResetExecution) FollowupMessage() string {
	return e.Command.Remainder
}

// ImmediateResult builds the default direct reply for a reset/new command when
// there is no remainder to forward.
func (e SessionResetExecution) ImmediateResult(runID string) core.AgentRunResult {
	return core.AgentRunResult{
		RunID:    runID,
		Payloads: []core.ReplyPayload{{Text: e.Command.ReplyText()}},
	}
}

// ExecuteSessionReset performs the basic session rollover against the store and
// returns the loaded history plus the next session id. Side effects beyond the
// store itself (such as memory archive or rerunning the request) stay with the
// caller.
func ExecuteSessionReset(store ResetLifecycleStore, sess core.SessionResolution, command SessionResetCommandMatch) (SessionResetExecution, error) {
	history, err := store.LoadTranscript(sess.SessionKey)
	if err != nil {
		return SessionResetExecution{}, err
	}
	nextSessionID, err := executeSessionResetStoreCall(store, sess, command.Reason)
	if err != nil {
		return SessionResetExecution{}, err
	}
	return SessionResetExecution{
		Command:       command,
		History:       history,
		NextSessionID: nextSessionID,
	}, nil
}

func executeSessionResetStoreCall(store ResetLifecycleStore, sess core.SessionResolution, reason string) (string, error) {
	if sess.Entry != nil && sess.Entry.ACP != nil {
		if specialized, ok := store.(ACPResetLifecycleStore); ok {
			return specialized.ResetACPBoundSession(sess, reason)
		}
	}
	return store.Reset(sess.SessionKey, reason)
}
