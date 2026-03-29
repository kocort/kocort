package acp

import (
	"context"
	"sort"
	"strings"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"
)

// PersistentSessionResumer resumes a single persisted ACP session.
type PersistentSessionResumer func(context.Context, AcpSessionResumeInput) (AcpSessionResumeResult, error)

// ResumePersistentSessions scans the session store for resumable persistent ACP
// sessions and invokes the supplied resumer for each one.
func ResumePersistentSessions(ctx context.Context, store *session.SessionStore, resume PersistentSessionResumer) []AcpSessionResumeResult {
	if store == nil || resume == nil {
		return nil
	}
	entries := store.AllEntries()
	results := make([]AcpSessionResumeResult, 0)
	for sessionKey, entry := range entries {
		if entry.ACP == nil || entry.ACP.Mode != core.AcpSessionModePersistent {
			continue
		}
		state := strings.ToLower(strings.TrimSpace(entry.ACP.State))
		if state != "" && state != "error" && state != "closed" {
			continue
		}
		result, err := resume(ctx, AcpSessionResumeInput{
			SessionKey: sessionKey,
			Agent:      entry.ACP.Agent,
			Cwd:        entry.ACP.Cwd,
			Mode:       entry.ACP.Mode,
			BackendID:  entry.ACP.Backend,
			Reason:     "process-restart",
		})
		if err == nil {
			results = append(results, result)
		}
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].SessionKey < results[j].SessionKey
	})
	return results
}
