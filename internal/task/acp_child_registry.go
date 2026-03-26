package task

import "github.com/kocort/kocort/internal/core"

func (r *SubagentRegistry) UpdateACPChildRuntime(runID string, entry *core.SessionEntry) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	record := r.runs[runID]
	if record == nil || record.ChildKind != "acp" {
		return false
	}
	SyncACPChildRuntimeMetadata(record, entry)
	r.persistSnapshotLocked()
	return true
}
