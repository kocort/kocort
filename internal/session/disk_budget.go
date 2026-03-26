package session

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------

// SessionDiskBudgetConfig controls when and how disk-budget enforcement runs.
type SessionDiskBudgetConfig struct {
	MaxDiskBytes   int64   // hard ceiling; 0 = disabled
	HighWaterRatio float64 // 0.0–1.0; default 0.8
}

// HighWaterBytes returns the high-water mark (cleanup target).
func (c SessionDiskBudgetConfig) HighWaterBytes() int64 {
	ratio := c.HighWaterRatio
	if ratio <= 0 || ratio >= 1.0 {
		ratio = 0.8
	}
	return int64(float64(c.MaxDiskBytes) * ratio)
}

// SessionDiskBudgetSweepResult captures what the budget enforcement did.
type SessionDiskBudgetSweepResult struct {
	TotalBytesBefore int64
	TotalBytesAfter  int64
	RemovedFiles     int
	RemovedEntries   int
	FreedBytes       int64
	MaxBytes         int64
	HighWaterBytes   int64
	OverBudget       bool
}

// EnforceSessionDiskBudget performs a two-phase cleanup to keep the sessions
// directory within the configured disk budget.
//
// Phase 1: remove orphan transcript files (archives, unreferenced files)
// Phase 2: evict store entries (oldest first, skip activeSessionKey)
//
// The cleanup target is highWaterBytes (not maxDiskBytes) to avoid thrashing.
func EnforceSessionDiskBudget(
	store *SessionStore,
	activeSessionKey string,
	budget SessionDiskBudgetConfig,
	warnOnly bool,
) (*SessionDiskBudgetSweepResult, error) {
	if store == nil || budget.MaxDiskBytes <= 0 {
		return nil, nil
	}

	baseDir := store.BaseDir()
	if strings.TrimSpace(baseDir) == "" {
		return nil, nil
	}

	totalBytes, fileInfos, err := walkSessionDirectory(baseDir)
	if err != nil {
		return nil, fmt.Errorf("disk budget walk: %w", err)
	}

	highWater := budget.HighWaterBytes()
	result := &SessionDiskBudgetSweepResult{
		TotalBytesBefore: totalBytes,
		TotalBytesAfter:  totalBytes,
		MaxBytes:         budget.MaxDiskBytes,
		HighWaterBytes:   highWater,
		OverBudget:       totalBytes > budget.MaxDiskBytes,
	}

	if totalBytes <= budget.MaxDiskBytes {
		return result, nil
	}

	if warnOnly {
		return result, nil
	}

	// Build set of transcript paths referenced by current store entries.
	referenced := buildReferencedPathSet(store)

	// Phase 1: remove orphan files — archives first, then unreferenced transcripts.
	// Sort by mtime ascending (oldest first).
	sort.Slice(fileInfos, func(i, j int) bool {
		return fileInfos[i].modTime.Before(fileInfos[j].modTime)
	})

	for _, fi := range fileInfos {
		if totalBytes <= highWater {
			break
		}
		if isTranscriptArchive(fi.path) || !referenced[fi.path] {
			if removeErr := os.Remove(fi.path); removeErr == nil {
				totalBytes -= fi.size
				result.RemovedFiles++
			}
		}
	}

	// Phase 2: evict store entries if still over budget.
	if totalBytes > highWater {
		evicted := evictEntriesByAge(store, activeSessionKey, totalBytes, highWater)
		result.RemovedEntries = evicted.count
		totalBytes -= evicted.freed
	}

	result.TotalBytesAfter = totalBytes
	result.FreedBytes = result.TotalBytesBefore - totalBytes
	result.OverBudget = totalBytes > budget.MaxDiskBytes
	return result, nil
}

// ---------------------------------------------------------------------------
// Internal types
// ---------------------------------------------------------------------------

type fileEntry struct {
	path    string
	size    int64
	modTime time.Time
}

type evictionResult struct {
	count int
	freed int64
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func walkSessionDirectory(baseDir string) (int64, []fileEntry, error) {
	var totalBytes int64
	var files []fileEntry

	err := filepath.WalkDir(baseDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil // skip unreadable files
		}
		size := info.Size()
		totalBytes += size
		files = append(files, fileEntry{
			path:    path,
			size:    size,
			modTime: info.ModTime(),
		})
		return nil
	})
	return totalBytes, files, err
}

func buildReferencedPathSet(store *SessionStore) map[string]bool {
	if store == nil {
		return nil
	}
	refs := make(map[string]bool)
	for _, entry := range store.AllEntries() {
		sf := strings.TrimSpace(entry.SessionFile)
		if sf != "" {
			abs, err := filepath.Abs(sf)
			if err == nil {
				refs[abs] = true
			}
			refs[sf] = true
		}
	}
	return refs
}

func isTranscriptArchive(path string) bool {
	name := filepath.Base(path)
	return strings.Contains(name, ".archive") ||
		strings.Contains(name, ".bak") ||
		strings.Contains(name, ".reset.") ||
		strings.Contains(name, ".archived.")
}

func evictEntriesByAge(store *SessionStore, activeSessionKey string, currentBytes, highWater int64) evictionResult {
	if store == nil {
		return evictionResult{}
	}

	type candidate struct {
		key       string
		entry     sessionEntryForEviction
		updatedAt time.Time
	}

	allEntries := store.AllEntries()
	candidates := make([]candidate, 0, len(allEntries))
	for key, entry := range allEntries {
		if key == activeSessionKey {
			continue // never evict the active session
		}
		candidates = append(candidates, candidate{
			key:       key,
			entry:     sessionEntryForEviction{sessionFile: entry.SessionFile},
			updatedAt: entry.UpdatedAt,
		})
	}

	// Sort by updatedAt ascending (oldest first).
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].updatedAt.IsZero() {
			return true
		}
		if candidates[j].updatedAt.IsZero() {
			return false
		}
		return candidates[i].updatedAt.Before(candidates[j].updatedAt)
	})

	var result evictionResult
	for _, c := range candidates {
		if currentBytes <= highWater {
			break
		}
		var freed int64
		if sf := strings.TrimSpace(c.entry.sessionFile); sf != "" {
			if info, err := os.Stat(sf); err == nil {
				freed = info.Size()
			}
			_ = os.Remove(sf)
		}
		_ = store.Delete(c.key)
		currentBytes -= freed
		// Estimate store entry size (~512 bytes average).
		currentBytes -= 512
		result.count++
		result.freed += freed + 512
	}
	return result
}

type sessionEntryForEviction struct {
	sessionFile string
}
