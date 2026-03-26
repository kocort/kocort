package runtime

import (
	"context"
	"strings"

	backendpkg "github.com/kocort/kocort/internal/backend"
	"github.com/kocort/kocort/internal/core"
	sessionpkg "github.com/kocort/kocort/internal/session"
)

type resetLifecycleAdapter struct {
	runtime *Runtime
}

func (a resetLifecycleAdapter) LoadTranscript(sessionKey string) ([]core.TranscriptMessage, error) {
	return a.runtime.Sessions.LoadTranscript(sessionKey)
}

func (a resetLifecycleAdapter) Reset(sessionKey string, reason string) (string, error) {
	return a.runtime.Sessions.Reset(sessionKey, reason)
}

func (a resetLifecycleAdapter) ResetACPBoundSession(sess core.SessionResolution, reason string) (string, error) {
	if a.runtime == nil || a.runtime.Sessions == nil || sess.Entry == nil || sess.Entry.ACP == nil {
		return a.Reset(sess.SessionKey, reason)
	}
	meta := sess.Entry.ACP
	provider := strings.TrimSpace(meta.Backend)
	if provider == "" {
		return a.Reset(sess.SessionKey, reason)
	}
	resolved, _, err := a.runtime.Backends.Resolve(provider)
	if err != nil {
		return a.Reset(sess.SessionKey, reason)
	}
	acpBackend, ok := resolved.(*backendpkg.ACPBackend)
	if !ok || acpBackend.Mgr == nil || acpBackend.Runtime == nil {
		return a.Reset(sess.SessionKey, reason)
	}
	_ = acpBackend.Mgr.CloseSession(context.Background(), a.runtime.Sessions, acpBackend.Runtime, sess.SessionKey, "session-reset", false)
	return a.runtime.Sessions.Reset(sess.SessionKey, reason)
}

var _ sessionpkg.ResetLifecycleStore = resetLifecycleAdapter{}
var _ sessionpkg.ACPResetLifecycleStore = resetLifecycleAdapter{}
