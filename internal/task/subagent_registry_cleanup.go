package task

import (
	"time"

	"github.com/kocort/kocort/internal/session"
)

func (r *SubagentRegistry) SweepOrphans(store *session.SessionStore, active *ActiveRunRegistry) {
	r.mu.Lock()
	changed := false
	now := time.Now().UTC()
	for runID, record := range r.runs {
		if record == nil {
			delete(r.runs, runID)
			changed = true
			continue
		}
		if !record.EndedAt.IsZero() && store != nil && store.Entry(record.ChildSessionKey) == nil {
			delete(r.runs, runID)
			changed = true
			continue
		}
		if record.EndedAt.IsZero() && active != nil && !active.IsRunActive(record.ChildSessionKey, record.RunID) {
			// If the run is recent enough and its session still exists,
			// leave it for the orphan recovery process rather than
			// marking it as failed immediately.
			if now.Sub(record.CreatedAt) <= OrphanRecoveryMaxAge &&
				store != nil && store.Entry(record.ChildSessionKey) != nil {
				continue
			}
			record.EndedAt = now
			if record.Outcome == nil {
				record.Outcome = &SubagentRunOutcome{Status: SubagentOutcomeUnknown, Error: "orphaned run cleaned up"}
			}
			changed = true
		}
	}
	if changed {
		r.persistSnapshotLocked()
	}
	r.mu.Unlock()
}

func (r *SubagentRegistry) SweepExpired() {
	var archived []SubagentRunRecord
	r.mu.Lock()
	now := time.Now().UTC()
	for runID, record := range r.runs {
		if record == nil || record.ArchiveAt.IsZero() || record.ArchiveAt.After(now) {
			continue
		}
		archived = append(archived, *record)
		delete(r.runs, runID)
	}
	if len(r.runs) == 0 && r.stopSweeper != nil {
		close(r.stopSweeper)
		r.stopSweeper = nil
		r.sweeper = nil
	}
	handler := r.archiveHandler
	archivePath := r.archivePath
	cleanupEntries := append([]SubagentRunRecord{}, archived...)
	r.persistSnapshotLocked()
	r.mu.Unlock()
	for _, entry := range archived {
		_ = appendArchivedSubagentRun(archivePath, entry) // best-effort; failure is non-critical
		if handler != nil {
			handler(entry)
		}
	}
	for _, entry := range cleanupEntries {
		CleanupMaterializedSubagentAttachments(entry.AttachmentsDir, entry.AttachmentsRootDir)
	}
}

func (r *SubagentRegistry) startSweeperLocked() {
	if r.sweeper != nil {
		return
	}
	hasArchive := false
	for _, record := range r.runs {
		if record != nil && !record.ArchiveAt.IsZero() {
			hasArchive = true
			break
		}
	}
	if !hasArchive {
		return
	}
	r.sweeper = time.NewTicker(time.Minute)
	r.stopSweeper = make(chan struct{})
	ticker := r.sweeper
	stop := r.stopSweeper
	go func() {
		for {
			select {
			case <-ticker.C:
				r.SweepExpired()
			case <-stop:
				ticker.Stop()
				return
			}
		}
	}()
}
