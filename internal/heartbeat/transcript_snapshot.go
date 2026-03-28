package heartbeat

import (
	"strings"

	"github.com/kocort/kocort/internal/core"
	sessionpkg "github.com/kocort/kocort/internal/session"
)

type TranscriptSnapshot struct {
	SessionKey string
	SessionID  string
	Messages   []core.TranscriptMessage
	Captured   bool
}

func CaptureTranscriptSnapshot(store *sessionpkg.SessionStore, sessionKey string) TranscriptSnapshot {
	sessionKey = strings.TrimSpace(sessionKey)
	if store == nil || sessionKey == "" {
		return TranscriptSnapshot{}
	}
	entry := store.Entry(sessionKey)
	if entry == nil || strings.TrimSpace(entry.SessionID) == "" {
		return TranscriptSnapshot{}
	}
	history, err := store.LoadTranscript(sessionKey)
	if err != nil {
		return TranscriptSnapshot{}
	}
	return TranscriptSnapshot{
		SessionKey: sessionKey,
		SessionID:  strings.TrimSpace(entry.SessionID),
		Messages:   append([]core.TranscriptMessage{}, history...),
		Captured:   true,
	}
}

func RestoreTranscriptSnapshot(store *sessionpkg.SessionStore, snapshot TranscriptSnapshot) {
	if store == nil || !snapshot.Captured || strings.TrimSpace(snapshot.SessionKey) == "" || strings.TrimSpace(snapshot.SessionID) == "" {
		return
	}
	_ = store.RewriteTranscript(snapshot.SessionKey, snapshot.SessionID, append([]core.TranscriptMessage{}, snapshot.Messages...))
}
