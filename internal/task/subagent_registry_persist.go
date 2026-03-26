package task

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (r *SubagentRegistry) SetArchiveHandler(handler func(SubagentRunRecord)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.archiveHandler = handler
}

func (r *SubagentRegistry) SetArchivePath(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.archivePath = strings.TrimSpace(path)
}

func (r *SubagentRegistry) SetStatePath(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statePath = strings.TrimSpace(path)
}

func (r *SubagentRegistry) SetAttachmentsMetadata(runID string, absDir string, rootDir string, retainOnSessionKeep bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record := r.runs[runID]
	if record == nil {
		return
	}
	record.AttachmentsDir = strings.TrimSpace(absDir)
	record.AttachmentsRootDir = strings.TrimSpace(rootDir)
	record.RetainAttachmentsOnKeep = retainOnSessionKeep
	r.persistSnapshotLocked()
}

func (r *SubagentRegistry) RestoreFromState() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	path := strings.TrimSpace(r.statePath)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var runs []SubagentRunRecord
	if err := json.Unmarshal(data, &runs); err != nil {
		return err
	}
	r.runs = map[string]*SubagentRunRecord{}
	for _, record := range runs {
		copy := record
		if strings.TrimSpace(copy.RunID) == "" {
			continue
		}
		r.runs[copy.RunID] = &copy
	}
	r.startSweeperLocked()
	return nil
}

func (r *SubagentRegistry) persistSnapshotLocked() {
	path := strings.TrimSpace(r.statePath)
	if path == "" {
		return
	}
	records := make([]SubagentRunRecord, 0, len(r.runs))
	for _, record := range r.runs {
		if record == nil {
			continue
		}
		records = append(records, *record)
	}
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].CreatedAt.Before(records[j].CreatedAt)
	})
	_ = persistSubagentRunsSnapshot(path, records)
}

func appendArchivedSubagentRun(path string, entry SubagentRunRecord) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var runs []SubagentRunRecord
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &runs) // best-effort; empty slice fallback is acceptable
	}
	runs = append(runs, entry)
	encoded, err := json.MarshalIndent(runs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o644)
}

func persistSubagentRunsSnapshot(path string, runs []SubagentRunRecord) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(runs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o644)
}
