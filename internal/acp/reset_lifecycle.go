package acp

import (
	"github.com/kocort/kocort/internal/core"
	sessionpkg "github.com/kocort/kocort/internal/session"
)

// ResetLifecycleStore adapts ACP-aware reset hooks to the session reset
// lifecycle interfaces without tying them to runtime.
type ResetLifecycleStore struct {
	LoadTranscriptFn       func(string) ([]core.TranscriptMessage, error)
	ResetFn                func(string, string) (string, error)
	ResetACPBoundSessionFn func(core.SessionResolution, string) (string, error)
}

func (s ResetLifecycleStore) LoadTranscript(sessionKey string) ([]core.TranscriptMessage, error) {
	return s.LoadTranscriptFn(sessionKey)
}

func (s ResetLifecycleStore) Reset(sessionKey string, reason string) (string, error) {
	return s.ResetFn(sessionKey, reason)
}

func (s ResetLifecycleStore) ResetACPBoundSession(sess core.SessionResolution, reason string) (string, error) {
	if s.ResetACPBoundSessionFn == nil {
		return s.Reset(sess.SessionKey, reason)
	}
	return s.ResetACPBoundSessionFn(sess, reason)
}

var _ sessionpkg.ResetLifecycleStore = ResetLifecycleStore{}
var _ sessionpkg.ACPResetLifecycleStore = ResetLifecycleStore{}
