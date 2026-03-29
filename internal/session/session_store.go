package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"

	"github.com/kocort/kocort/utils"
)

// SessionStore manages session entries and transcript persistence.
type SessionStore struct {
	storePath   string
	bindingPath string
	baseDir     string
	mu          sync.Mutex
	entries     map[string]core.SessionEntry
	bindings    map[string]SessionBindingRecord
	maintenance SessionStoreMaintenanceConfig
}

// SessionStoreMaintenanceConfig controls automatic pruning and rotation.
type SessionStoreMaintenanceConfig struct {
	Mode                  string
	PruneAfter            time.Duration
	MaxEntries            int
	RotateBytes           int64
	ResetArchiveRetention time.Duration
	DiskBudget            SessionDiskBudgetConfig
}

// SessionResolveOptions carries all parameters for session resolution.
type SessionResolveOptions struct {
	AgentID             string
	SessionKey          string
	SessionID           string
	To                  string
	Channel             string
	ThreadID            string
	ChatType            core.ChatType
	MainKey             string
	DMScope             string
	ParentForkMaxTokens int
	Now                 time.Time
	ResetPolicy         SessionFreshnessPolicy
	ForceNew            bool
	ForceNewReason      string
}

// SessionFreshnessPolicy describes when a session should be considered stale.
type SessionFreshnessPolicy struct {
	Mode        string
	AtHour      int
	IdleMinutes int
}

// SessionFreshnessResult is the outcome of a freshness evaluation.
type SessionFreshnessResult struct {
	Fresh  bool
	Reason string
}

// SessionListItem is a summary of a single session entry.
type SessionListItem struct {
	Key              string
	DisplayKey       string
	Kind             string
	Channel          string
	ParentSessionKey string
	ChildSessions    []string
	SessionID        string
	Label            string
	LastChannel      string
	LastTo           string
	LastAccountID    string
	LastThreadID     string
	UpdatedAt        time.Time
	ThinkingLevel    string
	FastMode         bool
	VerboseLevel     string
	ReasoningLevel   string
	ResponseUsage    string
	ElevatedLevel    string
	ProviderOverride string
	ModelOverride    string
	SpawnedBy        string
	SpawnMode        string
	SpawnDepth       int
}

// NewSessionStore creates a SessionStore rooted at baseDir.
func NewSessionStore(baseDir string) (*SessionStore, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, err
	}
	storePath := filepath.Join(baseDir, "sessions.json")
	s := &SessionStore{
		storePath:   storePath,
		bindingPath: filepath.Join(baseDir, "session_bindings.json"),
		baseDir:     baseDir,
		entries:     map[string]core.SessionEntry{},
		bindings:    map[string]SessionBindingRecord{},
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	if err := s.loadBindings(); err != nil {
		return nil, err
	}
	return s, nil
}

// SetMaintenanceConfig updates the maintenance configuration.
func (s *SessionStore) SetMaintenanceConfig(cfg SessionStoreMaintenanceConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maintenance = cfg
}

// DiskBudgetConfig returns the configured disk budget.
func (s *SessionStore) DiskBudgetConfig() SessionDiskBudgetConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maintenance.DiskBudget
}

// BaseDir returns the base directory of the session store.
func (s *SessionStore) BaseDir() string {
	return s.baseDir
}

// NewSessionStoreMaintenanceConfig builds a SessionStoreMaintenanceConfig from
// the config-layer SessionMaintenanceConfig. Exported from the original unexported
// newSessionStoreMaintenanceConfig.
func NewSessionStoreMaintenanceConfig(cfg *config.SessionMaintenanceConfig) SessionStoreMaintenanceConfig {
	out := SessionStoreMaintenanceConfig{
		Mode:                  "warn",
		PruneAfter:            30 * 24 * time.Hour,
		MaxEntries:            500,
		RotateBytes:           10 * 1024 * 1024,
		ResetArchiveRetention: 30 * 24 * time.Hour,
	}
	if cfg == nil {
		return out
	}
	if trimmed := strings.TrimSpace(strings.ToLower(cfg.Mode)); trimmed != "" {
		out.Mode = trimmed
	}
	if parsed, err := ParseSessionMaintenanceDuration(cfg.PruneAfter); err == nil && parsed > 0 {
		out.PruneAfter = parsed
	}
	if cfg.MaxEntries > 0 {
		out.MaxEntries = cfg.MaxEntries
	}
	if parsed, err := ParseSessionMaintenanceBytes(cfg.RotateBytes); err == nil && parsed > 0 {
		out.RotateBytes = parsed
	}
	if parsed, err := ParseSessionMaintenanceDuration(cfg.ResetArchiveRetention); err == nil && parsed > 0 {
		out.ResetArchiveRetention = parsed
	} else if strings.TrimSpace(cfg.ResetArchiveRetention) == "" {
		out.ResetArchiveRetention = out.PruneAfter
	}
	return out
}

// Resolve is a convenience wrapper around ResolveForRequest with sensible defaults.
func (s *SessionStore) Resolve(ctx context.Context, agentID, sessionKey, sessionID, to, channel string) (core.SessionResolution, error) {
	return s.ResolveForRequest(ctx, SessionResolveOptions{
		AgentID:    agentID,
		SessionKey: sessionKey,
		SessionID:  sessionID,
		To:         to,
		Channel:    channel,
		ChatType:   core.ChatTypeDirect,
		MainKey:    DefaultMainKey,
		Now:        time.Now().UTC(),
	})
}

// ResolveForRequest resolves or creates a session for the given options.
func (s *SessionStore) ResolveForRequest(ctx context.Context, opts SessionResolveOptions) (core.SessionResolution, error) {
	select {
	case <-ctx.Done():
		return core.SessionResolution{}, ctx.Err()
	default:
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}

	sessionKey := ResolveSessionKeyFromOptions(opts)

	existing, ok := s.entries[sessionKey]
	if !ok {
		if opts.SessionID == "" {
			opts.SessionID = NewSessionID()
		}
		var forkedEntry *core.SessionEntry
		if parentKey := ResolveParentSessionKeyFromOptions(opts, sessionKey); parentKey != "" && parentKey != sessionKey {
			forkedEntry = s.forkSessionFromParentLocked(sessionKey, opts.SessionID, parentKey, opts.Now, opts.ParentForkMaxTokens)
			if forkedEntry != nil {
				if err := s.flush(); err != nil {
					return core.SessionResolution{}, err
				}
			}
		}
		return core.SessionResolution{
			SessionID:  opts.SessionID,
			SessionKey: sessionKey,
			Entry:      forkedEntry,
			IsNew:      true,
			Fresh:      true,
		}, nil
	}

	if opts.ForceNew {
		nextSessionID, err := s.resetLocked(sessionKey, utils.NonEmpty(strings.TrimSpace(opts.ForceNewReason), "force-new"))
		if err != nil {
			return core.SessionResolution{}, err
		}
		freshEntry := s.entries[sessionKey]
		return core.SessionResolution{
			SessionID:        nextSessionID,
			SessionKey:       sessionKey,
			Entry:            PtrSessionEntry(freshEntry),
			IsNew:            true,
			PersistedThink:   "",
			PersistedVerbose: "",
			Fresh:            false,
		}, nil
	}

	freshness := EvaluateSessionFreshness(existing.UpdatedAt, opts.Now, opts.ResetPolicy)
	if !freshness.Fresh {
		nextSessionID, err := s.resetLocked(sessionKey, freshness.Reason)
		if err != nil {
			return core.SessionResolution{}, err
		}
		freshEntry := s.entries[sessionKey]
		return core.SessionResolution{
			SessionID:        nextSessionID,
			SessionKey:       sessionKey,
			Entry:            PtrSessionEntry(freshEntry),
			IsNew:            true,
			PersistedThink:   "",
			PersistedVerbose: "",
			Fresh:            false,
		}, nil
	}
	if opts.SessionID == "" {
		opts.SessionID = existing.SessionID
	}
	return core.SessionResolution{
		SessionID:        opts.SessionID,
		SessionKey:       sessionKey,
		Entry:            PtrSessionEntry(existing),
		IsNew:            false,
		PersistedThink:   existing.ThinkingLevel,
		PersistedVerbose: existing.VerboseLevel,
		Fresh:            true,
	}, nil
}

// Upsert inserts or updates a session entry.
func (s *SessionStore) Upsert(sessionKey string, entry core.SessionEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.entries[sessionKey]; ok {
		entry = MergeSessionEntry(existing, entry)
	}
	if entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = time.Now().UTC()
	}
	s.entries[sessionKey] = entry
	return s.flush()
}

// Mutate applies fn to the session entry under the lock, then flushes.
func (s *SessionStore) Mutate(sessionKey string, fn func(*core.SessionEntry) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.entries[sessionKey]
	if err := fn(&entry); err != nil {
		return err
	}
	entry.UpdatedAt = time.Now().UTC()
	s.entries[sessionKey] = entry
	return s.flush()
}

// AppendTranscript appends messages to the session's transcript file.
func (s *SessionStore) AppendTranscript(sessionKey, sessionID string, messages ...core.TranscriptMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[sessionKey]
	if !ok {
		entry = core.SessionEntry{SessionID: sessionID, UpdatedAt: time.Now().UTC()}
	}
	if entry.SessionID == "" {
		entry.SessionID = sessionID
	}
	if entry.SessionID == "" {
		return errors.New("missing session ID")
	}

	sessionFile := entry.SessionFile
	if sessionFile == "" {
		sessionFile = filepath.Join(s.baseDir, "transcripts", entry.SessionID+".jsonl")
		entry.SessionFile = sessionFile
	}
	if err := os.MkdirAll(filepath.Dir(sessionFile), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(sessionFile); errors.Is(err, os.ErrNotExist) {
		header := map[string]any{
			"type":      "session",
			"id":        entry.SessionID,
			"version":   1,
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		}
		if err := AppendJSONL(sessionFile, header); err != nil {
			return err
		}
	}

	for _, msg := range messages {
		msg = NormalizeTranscriptMessageForWrite(msg)
		if err := AppendJSONL(sessionFile, msg); err != nil {
			return err
		}
	}

	entry.UpdatedAt = time.Now().UTC()
	s.entries[sessionKey] = entry
	return s.flush()
}

// RewriteTranscript atomically replaces the session transcript.
func (s *SessionStore) RewriteTranscript(sessionKey, sessionID string, messages []core.TranscriptMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[sessionKey]
	if !ok {
		entry = core.SessionEntry{SessionID: sessionID, UpdatedAt: time.Now().UTC()}
	}
	if strings.TrimSpace(entry.SessionID) == "" {
		entry.SessionID = sessionID
	}
	if strings.TrimSpace(entry.SessionID) == "" {
		return errors.New("missing session ID")
	}

	sessionFile := entry.SessionFile
	if sessionFile == "" {
		sessionFile = filepath.Join(s.baseDir, "transcripts", entry.SessionID+".jsonl")
		entry.SessionFile = sessionFile
	}
	if err := os.MkdirAll(filepath.Dir(sessionFile), 0o755); err != nil {
		return err
	}
	tmpFile := sessionFile + ".tmp"
	if err := WriteTranscriptFile(tmpFile, entry.SessionID, messages); err != nil {
		return err
	}
	if err := os.Rename(tmpFile, sessionFile); err != nil {
		_ = os.Remove(tmpFile) // best-effort cleanup
		return err
	}
	entry.UpdatedAt = time.Now().UTC()
	s.entries[sessionKey] = entry
	return s.flush()
}

// LoadTranscript reads the transcript for the given session key.
func (s *SessionStore) LoadTranscript(sessionKey string) ([]core.TranscriptMessage, error) {
	s.mu.Lock()
	entry, ok := s.entries[sessionKey]
	s.mu.Unlock()
	if !ok || entry.SessionFile == "" {
		return nil, nil
	}

	file, err := os.Open(entry.SessionFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	var out []core.TranscriptMessage
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(line, &raw); err != nil {
			return nil, err
		}
		if rawType, ok := raw["type"]; ok {
			var typeValue string
			if err := json.Unmarshal(rawType, &typeValue); err == nil && strings.TrimSpace(strings.ToLower(typeValue)) == "session" {
				continue
			}
		}
		var msg core.TranscriptMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, err
		}
		msg = NormalizeTranscriptMessageForWrite(msg)
		out = append(out, msg)
	}
	return out, scanner.Err()
}

// ListSessions returns a sorted list of all session summaries.
func (s *SessionStore) ListSessions() []SessionListItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]SessionListItem, 0, len(s.entries))
	childrenByParent := make(map[string][]SessionListItem, len(s.entries))
	for key, entry := range s.entries {
		item := SessionListItem{
			Key:              key,
			ParentSessionKey: strings.TrimSpace(entry.SpawnedBy),
			SessionID:        entry.SessionID,
			Label:            utils.NonEmpty(entry.Label, key),
			LastChannel:      entry.LastChannel,
			LastTo:           entry.LastTo,
			LastAccountID:    entry.LastAccountID,
			LastThreadID:     entry.LastThreadID,
			UpdatedAt:        entry.UpdatedAt,
			ThinkingLevel:    entry.ThinkingLevel,
			FastMode:         entry.FastMode,
			VerboseLevel:     entry.VerboseLevel,
			ReasoningLevel:   entry.ReasoningLevel,
			ResponseUsage:    entry.ResponseUsage,
			ElevatedLevel:    entry.ElevatedLevel,
			ProviderOverride: entry.ProviderOverride,
			ModelOverride:    entry.ModelOverride,
			SpawnedBy:        entry.SpawnedBy,
			SpawnMode:        entry.SpawnMode,
			SpawnDepth:       entry.SpawnDepth,
		}
		items = append(items, item)
		if item.ParentSessionKey != "" {
			childrenByParent[item.ParentSessionKey] = append(childrenByParent[item.ParentSessionKey], item)
		}
	}
	for idx := range items {
		children := childrenByParent[items[idx].Key]
		if len(children) == 0 {
			continue
		}
		sort.SliceStable(children, func(i, j int) bool {
			if children[i].UpdatedAt.Equal(children[j].UpdatedAt) {
				return children[i].Key < children[j].Key
			}
			return children[i].UpdatedAt.After(children[j].UpdatedAt)
		})
		items[idx].ChildSessions = make([]string, 0, len(children))
		for _, child := range children {
			items[idx].ChildSessions = append(items[idx].ChildSessions, child.Key)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return items
}

// Entry returns a copy of the session entry for the given key, or nil.
func (s *SessionStore) Entry(sessionKey string) *core.SessionEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[sessionKey]
	if !ok {
		return nil
	}
	copy := entry
	return &copy
}

// AllEntries returns a snapshot of all session entries.
func (s *SessionStore) AllEntries() map[string]core.SessionEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]core.SessionEntry, len(s.entries))
	for key, entry := range s.entries {
		out[key] = entry
	}
	return out
}

// ResolveSessionKeyReference resolves a reference (key, session ID, or label) to
// a session key.
func (s *SessionStore) ResolveSessionKeyReference(reference string) (string, bool) {
	key, _, ok := s.ResolveSessionKeyReferenceDetailed(reference)
	return key, ok
}

// ResolveSessionKeyReferenceDetailed resolves a reference (key, session ID, or
// globally unique label) to a session key and reports which kind matched.
func (s *SessionStore) ResolveSessionKeyReferenceDetailed(reference string) (string, SessionReferenceKind, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ref := strings.TrimSpace(reference)
	if ref == "" {
		return "", SessionReferenceUnknown, false
	}
	if _, ok := s.entries[ref]; ok {
		return ref, SessionReferenceKey, true
	}
	for key, entry := range s.entries {
		if entry.SessionID == ref {
			return key, SessionReferenceSessionID, true
		}
		if label := strings.TrimSpace(entry.Label); label != "" && label == ref {
			return key, SessionReferenceLabel, true
		}
	}
	return "", SessionReferenceUnknown, false
}

// ResolveSessionLabel finds a session by agent, label, and optional spawnedBy.
func (s *SessionStore) ResolveSessionLabel(agentID string, label string, spawnedBy string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	label = strings.TrimSpace(label)
	if label == "" {
		return "", false
	}
	normalizedAgentID := NormalizeAgentID(agentID)
	for key, entry := range s.entries {
		if strings.TrimSpace(entry.Label) != label {
			continue
		}
		if normalizedAgentID != "" && ResolveAgentIDFromSessionKey(key) != normalizedAgentID {
			continue
		}
		if strings.TrimSpace(spawnedBy) != "" && strings.TrimSpace(entry.SpawnedBy) != strings.TrimSpace(spawnedBy) {
			continue
		}
		return key, true
	}
	return "", false
}

// IsSpawnedSessionVisible checks whether the requester can see the target session
// by walking the spawn chain.
func (s *SessionStore) IsSpawnedSessionVisible(requesterSessionKey string, targetSessionKey string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if requesterSessionKey == targetSessionKey {
		return true
	}
	current := strings.TrimSpace(targetSessionKey)
	for current != "" {
		entry, ok := s.entries[current]
		if !ok {
			return false
		}
		parent := strings.TrimSpace(entry.SpawnedBy)
		if parent == "" {
			return false
		}
		if parent == strings.TrimSpace(requesterSessionKey) {
			return true
		}
		current = parent
	}
	return false
}

// Delete removes a session entry and its transcript file.
func (s *SessionStore) Delete(sessionKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[sessionKey]
	if ok && strings.TrimSpace(entry.SessionFile) != "" {
		_ = os.Remove(entry.SessionFile) // best-effort cleanup
	}
	delete(s.entries, sessionKey)
	s.deleteBindingsLocked(sessionKey)
	return s.flush()
}

// Reset resets a session, archiving the old transcript and generating a new session ID.
func (s *SessionStore) Reset(sessionKey string, reason string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resetLocked(sessionKey, reason)
}

func (s *SessionStore) resetLocked(sessionKey string, reason string) (string, error) {
	entry, ok := s.entries[sessionKey]
	if !ok {
		nextSessionID := NewSessionID()
		s.entries[sessionKey] = core.SessionEntry{
			SessionID:          nextSessionID,
			UpdatedAt:          time.Now().UTC(),
			ResetReason:        strings.TrimSpace(reason),
			LastActivityReason: "reset",
		}
		return nextSessionID, s.flush()
	}
	if strings.TrimSpace(entry.SessionFile) != "" {
		archived, err := ArchiveTranscriptFile(entry.SessionFile, utils.NonEmpty(strings.TrimSpace(reason), "reset"))
		if err != nil {
			return "", err
		}
		_ = archived // unused; archive path is not needed here
	}
	nextSessionID := NewSessionID()
	entry.SessionID = nextSessionID
	entry.SessionFile = ""
	entry.ResetReason = strings.TrimSpace(reason)
	entry.LastActivityReason = "reset"
	entry.UpdatedAt = time.Now().UTC()
	s.entries[sessionKey] = entry
	s.deleteBindingsLocked(sessionKey)
	return nextSessionID, s.flush()
}

func (s *SessionStore) load() error {
	data, err := os.ReadFile(s.storePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, &s.entries)
}

func (s *SessionStore) flush() error {
	if err := s.applyMaintenanceLocked(time.Now().UTC()); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return err
	}
	if s.maintenance.RotateBytes > 0 && int64(len(data)) > s.maintenance.RotateBytes {
		if err := s.rotateStoreFileLocked(time.Now().UTC()); err != nil {
			return err
		}
	}
	if err := os.WriteFile(s.storePath, data, 0o644); err != nil {
		return err
	}
	return s.flushBindingsLocked()
}

func (s *SessionStore) applyMaintenanceLocked(now time.Time) error {
	if !strings.EqualFold(strings.TrimSpace(s.maintenance.Mode), "enforce") {
		return nil
	}
	if err := s.pruneStaleEntriesLocked(now); err != nil {
		return err
	}
	if err := s.enforceMaxEntriesLocked(now); err != nil {
		return err
	}
	if err := s.purgeTranscriptArchivesLocked(now); err != nil {
		return err
	}
	return nil
}

func (s *SessionStore) pruneStaleEntriesLocked(now time.Time) error {
	if s.maintenance.PruneAfter <= 0 {
		return nil
	}
	cutoff := now.Add(-s.maintenance.PruneAfter)
	for key, entry := range s.entries {
		if entry.UpdatedAt.IsZero() || !entry.UpdatedAt.Before(cutoff) {
			continue
		}
		if err := s.removeSessionEntryLocked(key, entry, "deleted"); err != nil {
			return err
		}
	}
	return nil
}

func (s *SessionStore) enforceMaxEntriesLocked(now time.Time) error {
	if s.maintenance.MaxEntries <= 0 || len(s.entries) <= s.maintenance.MaxEntries {
		return nil
	}
	type candidate struct {
		key   string
		entry core.SessionEntry
	}
	candidates := make([]candidate, 0, len(s.entries))
	for key, entry := range s.entries {
		candidates = append(candidates, candidate{key: key, entry: entry})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i].entry.UpdatedAt
		right := candidates[j].entry.UpdatedAt
		if left.Equal(right) {
			return candidates[i].key < candidates[j].key
		}
		if left.IsZero() {
			return true
		}
		if right.IsZero() {
			return false
		}
		return left.Before(right)
	})
	overflow := len(candidates) - s.maintenance.MaxEntries
	for _, item := range candidates[:overflow] {
		if err := s.removeSessionEntryLocked(item.key, item.entry, "deleted"); err != nil {
			return err
		}
	}
	return nil
}

func (s *SessionStore) removeSessionEntryLocked(sessionKey string, entry core.SessionEntry, reason string) error {
	if strings.TrimSpace(entry.SessionFile) != "" {
		if strings.TrimSpace(reason) == "" {
			reason = "deleted"
		}
		if _, err := ArchiveTranscriptFile(entry.SessionFile, strings.TrimSpace(reason)); err != nil {
			return err
		}
	}
	delete(s.entries, sessionKey)
	return nil
}

func (s *SessionStore) purgeTranscriptArchivesLocked(now time.Time) error {
	if s.maintenance.ResetArchiveRetention <= 0 {
		return nil
	}
	transcriptsDir := filepath.Join(s.baseDir, "transcripts")
	entries, err := os.ReadDir(transcriptsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	cutoff := now.Add(-s.maintenance.ResetArchiveRetention)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !IsTranscriptArchiveName(name) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(transcriptsDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *SessionStore) rotateStoreFileLocked(now time.Time) error {
	if _, err := os.Stat(s.storePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	rotated := fmt.Sprintf("%s.%s", s.storePath, now.UTC().Format("2006-01-02T15-04-05.000Z"))
	if err := os.Rename(s.storePath, rotated); err != nil {
		return err
	}
	return nil
}

// AppendJSONL appends a single JSON value as a line to the given file.
// Exported from the original unexported appendJSONL.
func AppendJSONL(path string, value any) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	return enc.Encode(value)
}

// WriteTranscriptFile writes a complete transcript file with header.
// Exported from the original unexported writeTranscriptFile.
func WriteTranscriptFile(path string, sessionID string, messages []core.TranscriptMessage) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	header := map[string]any{
		"type":      "session",
		"id":        sessionID,
		"version":   1,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := enc.Encode(header); err != nil {
		return err
	}
	for _, msg := range messages {
		msg = NormalizeTranscriptMessageForWrite(msg)
		if err := enc.Encode(msg); err != nil {
			return err
		}
	}
	return nil
}

// ArchiveTranscriptFile renames a transcript file to an archived name.
// Exported from the original unexported archiveTranscriptFile.
func ArchiveTranscriptFile(path string, reason string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", nil
	}
	if _, err := os.Stat(trimmed); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	reason = strings.TrimSpace(strings.ToLower(reason))
	if reason == "" {
		reason = "reset"
	}
	timestamp := time.Now().UTC().Format("2006-01-02T15-04-05.000Z")
	archived := fmt.Sprintf("%s.%s.%s", trimmed, reason, timestamp)
	if err := os.Rename(trimmed, archived); err != nil {
		return "", err
	}
	return archived, nil
}

// ParseSessionMaintenanceDuration parses a duration string that may include "d" suffix.
// Exported from the original unexported parseSessionMaintenanceDuration.
func ParseSessionMaintenanceDuration(raw string) (time.Duration, error) {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" {
		return 0, nil
	}
	if strings.HasSuffix(trimmed, "d") {
		daysText := strings.TrimSpace(strings.TrimSuffix(trimmed, "d"))
		if daysText == "" {
			return 0, fmt.Errorf("invalid day duration %q", raw)
		}
		var days int
		if _, err := fmt.Sscanf(daysText, "%d", &days); err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(trimmed)
}

// ParseSessionMaintenanceBytes parses a byte-size string with optional unit suffix.
// Exported from the original unexported parseSessionMaintenanceBytes.
func ParseSessionMaintenanceBytes(raw string) (int64, error) {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" {
		return 0, nil
	}
	// Order matters: check longest suffixes first to avoid "b" matching "kb".
	type suffixMultiplier struct {
		suffix     string
		multiplier int64
	}
	multipliers := []suffixMultiplier{
		{"kb", 1024},
		{"mb", 1024 * 1024},
		{"gb", 1024 * 1024 * 1024},
		{"b", 1},
	}
	for _, sm := range multipliers {
		suffix, multiplier := sm.suffix, sm.multiplier
		if !strings.HasSuffix(trimmed, suffix) {
			continue
		}
		valueText := strings.TrimSpace(strings.TrimSuffix(trimmed, suffix))
		var value int64
		if _, err := fmt.Sscanf(valueText, "%d", &value); err != nil {
			return 0, err
		}
		return value * multiplier, nil
	}
	var value int64
	if _, err := fmt.Sscanf(trimmed, "%d", &value); err != nil {
		return 0, err
	}
	return value, nil
}

// IsTranscriptArchiveName checks if a filename looks like an archived transcript.
// Exported from the original unexported isTranscriptArchiveName.
func IsTranscriptArchiveName(name string) bool {
	if !strings.Contains(name, ".jsonl.") {
		return false
	}
	if strings.Contains(name, ".tmp") {
		return false
	}
	return true
}

// MergeSessionEntry merges an incoming entry over an existing one, preserving
// non-empty fields from existing when the incoming field is empty/zero.
// Exported from the original unexported mergeSessionEntry.
func MergeSessionEntry(existing core.SessionEntry, next core.SessionEntry) core.SessionEntry {
	if strings.TrimSpace(next.SessionID) == "" {
		next.SessionID = existing.SessionID
	}
	if strings.TrimSpace(next.Label) == "" {
		next.Label = existing.Label
	}
	if next.UpdatedAt.IsZero() {
		next.UpdatedAt = existing.UpdatedAt
	}
	if strings.TrimSpace(next.LastChannel) == "" {
		next.LastChannel = existing.LastChannel
	}
	if strings.TrimSpace(next.LastTo) == "" {
		next.LastTo = existing.LastTo
	}
	if strings.TrimSpace(next.LastAccountID) == "" {
		next.LastAccountID = existing.LastAccountID
	}
	if strings.TrimSpace(next.LastThreadID) == "" {
		next.LastThreadID = existing.LastThreadID
	}
	if next.DeliveryContext == nil {
		next.DeliveryContext = existing.DeliveryContext
	}
	if strings.TrimSpace(next.ThinkingLevel) == "" {
		next.ThinkingLevel = existing.ThinkingLevel
	}
	if !next.FastMode {
		next.FastMode = existing.FastMode
	}
	if strings.TrimSpace(next.VerboseLevel) == "" {
		next.VerboseLevel = existing.VerboseLevel
	}
	if strings.TrimSpace(next.ReasoningLevel) == "" {
		next.ReasoningLevel = existing.ReasoningLevel
	}
	if strings.TrimSpace(next.ResponseUsage) == "" {
		next.ResponseUsage = existing.ResponseUsage
	}
	if strings.TrimSpace(next.ElevatedLevel) == "" {
		next.ElevatedLevel = existing.ElevatedLevel
	}
	if strings.TrimSpace(next.ProviderOverride) == "" {
		next.ProviderOverride = existing.ProviderOverride
	}
	if strings.TrimSpace(next.ModelOverride) == "" {
		next.ModelOverride = existing.ModelOverride
	}
	if strings.TrimSpace(next.AuthProfileOverride) == "" {
		next.AuthProfileOverride = existing.AuthProfileOverride
	}
	if strings.TrimSpace(next.SessionFile) == "" {
		next.SessionFile = existing.SessionFile
	}
	if strings.TrimSpace(next.CLIType) == "" {
		next.CLIType = existing.CLIType
	}
	if next.CLISessionIDs == nil {
		next.CLISessionIDs = existing.CLISessionIDs
	}
	if strings.TrimSpace(next.ClaudeCLISessionID) == "" {
		next.ClaudeCLISessionID = existing.ClaudeCLISessionID
	}
	if strings.TrimSpace(next.OpenAIPreviousID) == "" {
		next.OpenAIPreviousID = existing.OpenAIPreviousID
	}
	if next.ACP == nil {
		next.ACP = existing.ACP
	}
	if strings.TrimSpace(next.SpawnedBy) == "" {
		next.SpawnedBy = existing.SpawnedBy
	}
	if strings.TrimSpace(next.SpawnMode) == "" {
		next.SpawnMode = existing.SpawnMode
	}
	if next.SpawnDepth == 0 {
		next.SpawnDepth = existing.SpawnDepth
	}
	if next.SkillsSnapshot == nil {
		next.SkillsSnapshot = existing.SkillsSnapshot
	}
	if next.Usage == nil {
		next.Usage = existing.Usage
	}
	if next.ContextTokens == 0 {
		next.ContextTokens = existing.ContextTokens
	}
	if strings.TrimSpace(next.ActiveProvider) == "" {
		next.ActiveProvider = existing.ActiveProvider
	}
	if strings.TrimSpace(next.ActiveModel) == "" {
		next.ActiveModel = existing.ActiveModel
	}
	if next.CompactionCount == 0 {
		next.CompactionCount = existing.CompactionCount
	}
	if next.LastModelCallAt.IsZero() {
		next.LastModelCallAt = existing.LastModelCallAt
	}
	if next.MemoryFlushAt.IsZero() {
		next.MemoryFlushAt = existing.MemoryFlushAt
	}
	if next.MemoryFlushCompactionCount == 0 {
		next.MemoryFlushCompactionCount = existing.MemoryFlushCompactionCount
	}
	if strings.TrimSpace(next.ResetReason) == "" {
		next.ResetReason = existing.ResetReason
	}
	if strings.TrimSpace(next.LastActivityReason) == "" {
		next.LastActivityReason = existing.LastActivityReason
	}
	if strings.TrimSpace(next.LastHeartbeatText) == "" {
		next.LastHeartbeatText = existing.LastHeartbeatText
	}
	if next.LastHeartbeatSentAt.IsZero() {
		next.LastHeartbeatSentAt = existing.LastHeartbeatSentAt
	}
	if next.LastChatType == "" {
		next.LastChatType = existing.LastChatType
	}
	if !next.ForkedFromParent {
		next.ForkedFromParent = existing.ForkedFromParent
	}
	return next
}

// NewSessionID generates a new session ID string.
// Exported from the original unexported newSessionID.
func NewSessionID() string {
	id, err := RandomToken(12)
	if err != nil {
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return "sess_" + id
}

// PtrSessionEntry returns a pointer to a copy of a SessionEntry.
// Exported from the original unexported ptrSessionEntry.
func PtrSessionEntry(value core.SessionEntry) *core.SessionEntry {
	v := value
	return &v
}

// NormalizeTranscriptMessageForWrite ensures a transcript message has an ID and
// timestamp before being written to disk.
// Exported from the original unexported normalizeTranscriptMessageForWrite.
func NormalizeTranscriptMessageForWrite(msg core.TranscriptMessage) core.TranscriptMessage {
	if strings.TrimSpace(msg.ID) == "" {
		msg.ID = NewTranscriptEntryID()
	}
	if strings.TrimSpace(msg.Text) == "" && strings.TrimSpace(msg.Summary) != "" {
		msg.Text = strings.TrimSpace(msg.Summary)
	}
	if strings.TrimSpace(msg.Summary) == "" && strings.EqualFold(strings.TrimSpace(msg.Type), "compaction") && strings.TrimSpace(msg.Text) != "" {
		msg.Summary = strings.TrimSpace(msg.Text)
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now().UTC()
	}
	return SanitizeTranscriptMessageForWrite(msg)
}

// NewTranscriptEntryID generates a unique transcript entry ID.
// Exported from the original unexported newTranscriptEntryID.
func NewTranscriptEntryID() string {
	id, err := RandomToken(10)
	if err != nil {
		return fmt.Sprintf("entry_%d", time.Now().UTC().UnixNano())
	}
	return "entry_" + id
}
