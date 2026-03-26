package infra

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"
)

const (
	// DefaultAuditMaxSizeMB is the default max size for audit log files (100 MB).
	DefaultAuditMaxSizeMB = 100
)

// AuditLog records audit events to JSONL files with date+size rotation.
// Files are named: events-2024-01-15.jsonl, events-2024-01-15-001.jsonl, etc.
type AuditLog struct {
	mu          sync.Mutex
	baseDir     string
	maxFileSize int64 // max bytes per file (0 = unlimited)
	currentDate string
	currentIdx  int
	currentFile *os.File
	currentSize int64
	currentPath string
}

// NewAuditLog creates a new AuditLog with default max file size (100 MB).
func NewAuditLog(stateDir string) (*AuditLog, error) {
	return NewAuditLogWithSize(stateDir, DefaultAuditMaxSizeMB)
}

// NewAuditLogWithSize creates a new AuditLog with a custom max file size in MB.
// If maxFileSizeMB <= 0, uses the default (100 MB).
func NewAuditLogWithSize(stateDir string, maxFileSizeMB int) (*AuditLog, error) {
	base := filepath.Join(strings.TrimSpace(stateDir), "audit")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, err
	}
	maxBytes := int64(DefaultAuditMaxSizeMB) * 1024 * 1024
	if maxFileSizeMB > 0 {
		maxBytes = int64(maxFileSizeMB) * 1024 * 1024
	}
	return &AuditLog{
		baseDir:     base,
		maxFileSize: maxBytes,
	}, nil
}

// Record writes an audit event to the current log file.
// It automatically rotates to a new file when:
// 1. The date changes (new day)
// 2. The current file exceeds maxFileSize
func (l *AuditLog) Record(_ context.Context, event core.AuditEvent) error {
	if l == nil {
		return nil
	}
	if strings.TrimSpace(event.ID) == "" {
		id, err := session.RandomToken(12)
		if err != nil {
			return err
		}
		event.ID = id
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	// slog output
	slog.Debug("Audit event recorded", "event", event)
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()

	// Check if we need to rotate the file
	if err := l.maybeRotateLocked(); err != nil {
		return err
	}

	// Open file if not already open
	if l.currentFile == nil {
		if err := l.openCurrentFileLocked(); err != nil {
			return err
		}
	}

	// Write the event
	n, err := l.currentFile.Write(raw)
	if err != nil {
		return err
	}
	l.currentSize += int64(n)
	return nil
}

// maybeRotateLocked checks if rotation is needed and performs it.
// Must be called with l.mu held.
func (l *AuditLog) maybeRotateLocked() error {
	today := time.Now().UTC().Format("2006-01-02")

	// Date changed: close current file and reset
	if l.currentDate != today {
		if l.currentFile != nil {
			_ = l.currentFile.Close()
			l.currentFile = nil
		}
		l.currentDate = today
		l.currentIdx = 0
		l.currentSize = 0
		l.currentPath = ""
		return nil
	}

	// Check size limit (only if maxFileSize > 0)
	if l.maxFileSize > 0 && l.currentSize > 0 && l.currentSize >= l.maxFileSize {
		if l.currentFile != nil {
			_ = l.currentFile.Close()
			l.currentFile = nil
		}
		l.currentIdx++
		l.currentSize = 0
		l.currentPath = ""
	}

	return nil
}

// openCurrentFileLocked opens the current log file for appending.
// Must be called with l.mu held.
func (l *AuditLog) openCurrentFileLocked() error {
	path := l.buildFilePath(l.currentDate, l.currentIdx)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	// Get current file size
	stat, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	l.currentFile = f
	l.currentPath = path
	l.currentSize = stat.Size()
	return nil
}

// buildFilePath constructs the log file path for a given date and index.
func (l *AuditLog) buildFilePath(date string, idx int) string {
	if idx == 0 {
		return filepath.Join(l.baseDir, fmt.Sprintf("events-%s.jsonl", date))
	}
	return filepath.Join(l.baseDir, fmt.Sprintf("events-%s-%03d.jsonl", date, idx))
}

// List retrieves audit events matching the query.
// It scans all relevant date files and applies filters.
func (l *AuditLog) List(_ context.Context, query core.AuditQuery) ([]core.AuditEvent, error) {
	if l == nil {
		return nil, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	// Determine date range to scan
	startDate, endDate := l.resolveDateRange(query)

	// Collect all matching files
	files, err := l.collectFilesInRange(startDate, endDate)
	if err != nil {
		return nil, err
	}

	// Read events from all files (most recent first)
	var out []core.AuditEvent
	for i := len(files) - 1; i >= 0; i-- {
		events, err := l.readEventsFromFile(files[i], query)
		if err != nil {
			return nil, err
		}
		out = append(out, events...)
	}

	// Apply limit (take from the end, which are most recent)
	if query.Limit > 0 && len(out) > query.Limit {
		out = out[len(out)-query.Limit:]
	}

	return out, nil
}

// resolveDateRange determines the date range to scan based on query.
// Returns start and end dates (inclusive) in "2006-01-02" format.
func (l *AuditLog) resolveDateRange(query core.AuditQuery) (string, string) {
	now := time.Now().UTC()

	// Default: scan last 7 days if no time range specified
	defaultDays := 7
	start := now.AddDate(0, 0, -defaultDays)
	end := now

	if !query.StartTime.IsZero() {
		start = query.StartTime.UTC()
	}
	if !query.EndTime.IsZero() {
		end = query.EndTime.UTC()
	}

	return start.Format("2006-01-02"), end.Format("2006-01-02")
}

// collectFilesInRange returns all log files within the date range, sorted by date.
func (l *AuditLog) collectFilesInRange(startDate, endDate string) ([]string, error) {
	entries, err := os.ReadDir(l.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Parse date from filename: events-2024-01-15.jsonl or events-2024-01-15-001.jsonl
		date := l.parseDateFromFilename(name)
		if date == "" {
			continue
		}
		// Check if date is within range
		if date < startDate || date > endDate {
			continue
		}
		files = append(files, filepath.Join(l.baseDir, name))
	}

	// Sort by filename (which sorts by date and index)
	sort.Strings(files)
	return files, nil
}

// parseDateFromFilename extracts the date from a log filename.
// Returns empty string if not a valid log filename.
func (l *AuditLog) parseDateFromFilename(name string) string {
	// Pattern: events-YYYY-MM-DD.jsonl or events-YYYY-MM-DD-NNN.jsonl
	if !strings.HasPrefix(name, "events-") || !strings.HasSuffix(name, ".jsonl") {
		return ""
	}
	// Extract date part: events-2024-01-15... -> 2024-01-15
	core := strings.TrimPrefix(name, "events-")
	core = strings.TrimSuffix(core, ".jsonl")
	// Handle index suffix
	parts := strings.SplitN(core, "-", 5)
	if len(parts) < 3 {
		return ""
	}
	date := strings.Join(parts[0:3], "-")
	// Validate date format
	if len(date) != 10 {
		return ""
	}
	return date
}

// readEventsFromFile reads all events from a file that match the query filters.
func (l *AuditLog) readEventsFromFile(path string, query core.AuditQuery) ([]core.AuditEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []core.AuditEvent
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event core.AuditEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, err
		}
		if !l.eventMatchesQuery(event, query) {
			continue
		}
		out = append(out, event)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return out, nil
}

// eventMatchesQuery checks if an event matches the query filters.
func (l *AuditLog) eventMatchesQuery(event core.AuditEvent, query core.AuditQuery) bool {
	// Time range filter
	if !query.StartTime.IsZero() && event.OccurredAt.Before(query.StartTime) {
		return false
	}
	if !query.EndTime.IsZero() && event.OccurredAt.After(query.EndTime) {
		return false
	}
	if query.Category != "" && event.Category != query.Category {
		return false
	}
	if strings.TrimSpace(query.Type) != "" && event.Type != strings.TrimSpace(query.Type) {
		return false
	}
	if strings.TrimSpace(query.Level) != "" && event.Level != strings.TrimSpace(query.Level) {
		return false
	}
	if strings.TrimSpace(query.SessionKey) != "" && event.SessionKey != strings.TrimSpace(query.SessionKey) {
		return false
	}
	if strings.TrimSpace(query.RunID) != "" && event.RunID != strings.TrimSpace(query.RunID) {
		return false
	}
	if strings.TrimSpace(query.TaskID) != "" && event.TaskID != strings.TrimSpace(query.TaskID) {
		return false
	}
	if needle := strings.ToLower(strings.TrimSpace(query.Text)); needle != "" {
		haystacks := []string{
			strings.ToLower(strings.TrimSpace(event.Message)),
			strings.ToLower(strings.TrimSpace(event.ToolName)),
			strings.ToLower(strings.TrimSpace(event.RunID)),
			strings.ToLower(strings.TrimSpace(event.SessionKey)),
			strings.ToLower(strings.TrimSpace(event.TaskID)),
			strings.ToLower(strings.TrimSpace(event.Channel)),
			strings.ToLower(strings.TrimSpace(event.Type)),
			strings.ToLower(string(event.Category)),
		}
		if len(event.Data) > 0 {
			if raw, err := json.Marshal(event.Data); err == nil {
				haystacks = append(haystacks, strings.ToLower(string(raw)))
			}
		}
		matched := false
		for _, haystack := range haystacks {
			if strings.Contains(haystack, needle) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}
