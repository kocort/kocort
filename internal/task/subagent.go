package task

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"
)

// SubagentOutcomeStatus represents the final status of a subagent run.
type SubagentOutcomeStatus string

const (
	SubagentOutcomeOK      SubagentOutcomeStatus = "ok"
	SubagentOutcomeError   SubagentOutcomeStatus = "error"
	SubagentOutcomeTimeout SubagentOutcomeStatus = "timeout"
	SubagentOutcomeUnknown SubagentOutcomeStatus = "unknown"
)

// SubagentRunOutcome captures an outcome after a subagent run finishes.
type SubagentRunOutcome struct {
	Status SubagentOutcomeStatus
	Error  string
}

// SubagentSpawnRequest describes a request to spawn a subagent.
type SubagentSpawnRequest struct {
	RequesterSessionKey         string
	RequesterAgentID            string
	RequesterDisplayKey         string
	TargetAgentID               string
	Task                        string
	Label                       string
	ModelOverride               string
	ExpectsCompletionMessage    bool
	ExpectsCompletionMessageSet bool
	Thinking                    string
	RunTimeoutSeconds           int
	MaxSpawnDepth               int
	MaxChildren                 int
	CurrentDepth                int
	WorkspaceDir                string
	Cleanup                     string
	SpawnMode                   string
	ThreadRequested             bool
	SandboxMode                 string
	RouteChannel                string
	RouteTo                     string
	RouteAccountID              string
	RouteThreadID               string
	Attachments                 []SubagentInlineAttachment
	AttachMountPath             string
	AttachmentMaxFiles          int
	AttachmentMaxFileBytes      int
	AttachmentMaxTotalBytes     int
	RetainAttachmentsOnKeep     bool
	ArchiveAfterMinutes         int
}

// SubagentSpawnResult is the result of spawning a subagent.
type SubagentSpawnResult struct {
	Status          string
	ChildSessionKey string
	RunID           string
	Lane            core.Lane
	SpawnedBy       string
	SpawnDepth      int
	WorkspaceDir    string
	Note            string
	Attachments     *SubagentAttachmentReceipt
}

// SubagentRunRecord tracks the lifecycle of a single subagent run.
type SubagentRunRecord struct {
	RunID                          string
	ChildKind                      string
	ChildSessionKey                string
	RequesterSessionKey            string
	RequesterDisplayKey            string
	RequesterOrigin                *core.DeliveryContext
	Task                           string
	Label                          string
	Model                          string
	Cleanup                        string
	SpawnMode                      string
	RouteChannel                   string
	RouteThreadID                  string
	RuntimeBackend                 string
	RuntimeState                   string
	RuntimeMode                    string
	RuntimeSessionName             string
	RuntimeStatusSummary           string
	RuntimeBackendSessionID        string
	RuntimeAgentSessionID          string
	SteeredFromRunID               string
	ReplacementRunID               string
	SpawnDepth                     int
	WorkspaceDir                   string
	AttachmentsDir                 string
	AttachmentsRootDir             string
	RetainAttachmentsOnKeep        bool
	RunTimeoutSeconds              int
	ArchiveAt                      time.Time
	CreatedAt                      time.Time
	StartedAt                      time.Time
	EndedAt                        time.Time
	Outcome                        *SubagentRunOutcome
	EndedReason                    string
	FrozenResultText               string
	FrozenResultCapturedAt         time.Time
	FallbackFrozenResultText       string
	FallbackFrozenResultCapturedAt time.Time
	SuppressAnnounceReason         string
	CompletionDeferredAt           time.Time
	CompletionMessageSentAt        time.Time
	CleanupCompletedAt             time.Time
	CleanupHandled                 bool
	EndedHookEmittedAt             time.Time
	ExpectsCompletionMessage       bool
	WakeOnDescendantSettle         bool
	CompletionAttempts             int
	AnnounceRetryCount             int
	LastAnnounceRetryAt            time.Time
	NextAnnounceAttemptAt          time.Time
	LastAnnounceError              string
	AnnounceDeliveryPath           string
}

// SubagentRegistry tracks active and recently-completed subagent runs.
type SubagentRegistry struct {
	mu             sync.Mutex
	runs           map[string]*SubagentRunRecord
	sweeper        *time.Ticker
	stopSweeper    chan struct{}
	announceTimers map[string]*time.Timer
	archiveHandler func(SubagentRunRecord)
	archivePath    string
	statePath      string
}

// NewSubagentRegistry creates a new empty SubagentRegistry.
func NewSubagentRegistry() *SubagentRegistry {
	return &SubagentRegistry{
		runs:           map[string]*SubagentRunRecord{},
		announceTimers: map[string]*time.Timer{},
	}
}

func (r *SubagentRegistry) Register(record SubagentRunRecord) {
	r.mu.Lock()
	recordCopy := record
	r.runs[record.RunID] = &recordCopy
	r.startSweeperLocked()
	r.persistSnapshotLocked()
	r.mu.Unlock()
}

func (r *SubagentRegistry) Get(runID string) *SubagentRunRecord {
	r.SweepExpired()
	r.mu.Lock()
	defer r.mu.Unlock()
	record := r.runs[runID]
	if record == nil {
		return nil
	}
	copy := *record
	return &copy
}

func (r *SubagentRegistry) Count() int {
	r.SweepExpired()
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.runs)
}

func (r *SubagentRegistry) Complete(runID string, result core.AgentRunResult, err error) (*SubagentRunRecord, bool) {
	r.mu.Lock()
	record := r.runs[runID]
	if record == nil {
		r.mu.Unlock()
		return nil, false
	}
	if !record.EndedAt.IsZero() {
		copy := *record
		r.mu.Unlock()
		return &copy, false
	}
	record.EndedAt = time.Now().UTC()
	record.Outcome = &SubagentRunOutcome{Status: SubagentOutcomeOK}
	if err != nil {
		record.Outcome = &SubagentRunOutcome{
			Status: SubagentOutcomeError,
			Error:  err.Error(),
		}
	}
	record.FrozenResultText = freezeSubagentResultText(result, err)
	record.FrozenResultCapturedAt = record.EndedAt
	record.FallbackFrozenResultText = record.FrozenResultText
	record.FallbackFrozenResultCapturedAt = record.FrozenResultCapturedAt
	record.EndedReason = resolveSubagentEndedReason(result, err)
	if !record.ArchiveAt.IsZero() {
		r.startSweeperLocked()
	}
	copy := *record
	r.persistSnapshotLocked()
	r.mu.Unlock()
	return &copy, true
}

func (r *SubagentRegistry) MarkCompletionMessageSent(runID string) {
	r.mu.Lock()
	record := r.runs[runID]
	if record == nil {
		r.mu.Unlock()
		return
	}
	record.CompletionMessageSentAt = time.Now().UTC()
	record.CleanupCompletedAt = record.CompletionMessageSentAt
	record.CleanupHandled = true
	attachmentsDir := record.AttachmentsDir
	attachmentsRootDir := record.AttachmentsRootDir
	retainAttachmentsOnKeep := record.RetainAttachmentsOnKeep
	if record.Cleanup == "delete" && record.SpawnMode != "session" {
		delete(r.runs, runID)
	}
	r.persistSnapshotLocked()
	r.mu.Unlock()
	if record.Cleanup == "delete" && record.SpawnMode != "session" {
		CleanupMaterializedSubagentAttachments(attachmentsDir, attachmentsRootDir)
	} else if !retainAttachmentsOnKeep {
		CleanupMaterializedSubagentAttachments(attachmentsDir, attachmentsRootDir)
	}
}

func (r *SubagentRegistry) MarkAnnouncementDeliveryPath(runID string, path string) {
	r.mu.Lock()
	record := r.runs[runID]
	if record == nil {
		r.mu.Unlock()
		return
	}
	record.AnnounceDeliveryPath = strings.TrimSpace(path)
	r.persistSnapshotLocked()
	r.mu.Unlock()
}

func (r *SubagentRegistry) MarkCompletionAbandoned(runID string, cause string) {
	r.mu.Lock()
	record := r.runs[runID]
	if record == nil {
		r.mu.Unlock()
		return
	}
	now := time.Now().UTC()
	record.CompletionMessageSentAt = now
	record.CleanupCompletedAt = now
	record.CleanupHandled = true
	record.NextAnnounceAttemptAt = time.Time{}
	if trimmed := strings.TrimSpace(cause); trimmed != "" {
		record.LastAnnounceError = trimmed
	}
	r.persistSnapshotLocked()
	r.mu.Unlock()
}

func (r *SubagentRegistry) SuppressCompletionAnnouncement(runID string, reason string) {
	r.mu.Lock()
	record := r.runs[runID]
	if record == nil {
		r.mu.Unlock()
		return
	}
	record.SuppressAnnounceReason = strings.TrimSpace(reason)
	record.NextAnnounceAttemptAt = time.Time{}
	record.CompletionDeferredAt = time.Time{}
	if !record.EndedAt.IsZero() {
		now := time.Now().UTC()
		record.CompletionMessageSentAt = now
		record.CleanupCompletedAt = now
		record.CleanupHandled = true
	}
	r.persistSnapshotLocked()
	r.mu.Unlock()
}

func (r *SubagentRegistry) MarkWakeOnDescendantSettle(runID string, enabled bool) {
	r.mu.Lock()
	record := r.runs[runID]
	if record == nil {
		r.mu.Unlock()
		return
	}
	record.WakeOnDescendantSettle = enabled
	r.persistSnapshotLocked()
	r.mu.Unlock()
}

func (r *SubagentRegistry) MarkEndedHookEmitted(runID string) {
	r.mu.Lock()
	record := r.runs[runID]
	if record == nil {
		r.mu.Unlock()
		return
	}
	record.EndedHookEmittedAt = time.Now().UTC()
	r.persistSnapshotLocked()
	r.mu.Unlock()
}

// MarkTerminated forcefully terminates a running subagent (e.g. killed by
// parent). If the run is already ended, this is a no-op.
func (r *SubagentRegistry) MarkTerminated(runID string, reason string) {
	r.mu.Lock()
	record := r.runs[runID]
	if record == nil || !record.EndedAt.IsZero() {
		r.mu.Unlock()
		return
	}
	now := time.Now().UTC()
	record.EndedAt = now
	record.EndedReason = reason
	record.Outcome = &SubagentRunOutcome{Status: SubagentOutcomeError, Error: reason}
	record.FrozenResultText = reason
	record.FrozenResultCapturedAt = now
	record.FallbackFrozenResultText = record.FrozenResultText
	record.FallbackFrozenResultCapturedAt = now
	r.persistSnapshotLocked()
	r.mu.Unlock()
}

// MarkSteeredFrom marks a run as having been steered (interrupted and
// replaced). The SteeredFromRunID field on the replacement record should
// point back to this run's ID.
func (r *SubagentRegistry) MarkSteeredFrom(runID string) {
	r.mu.Lock()
	record := r.runs[runID]
	if record == nil {
		r.mu.Unlock()
		return
	}
	record.EndedAt = time.Now().UTC()
	record.EndedReason = "steered"
	if record.Outcome == nil {
		record.Outcome = &SubagentRunOutcome{Status: SubagentOutcomeOK}
	}
	record.SuppressAnnounceReason = "steered-restart"
	r.persistSnapshotLocked()
	r.mu.Unlock()
}

func (r *SubagentRegistry) markCompletionDeferred(runID string) {
	r.MarkCompletionDeferredUntil(runID, time.Now().UTC())
}

func (r *SubagentRegistry) MarkCompletionDeferredUntil(runID string, when time.Time) {
	r.mu.Lock()
	record := r.runs[runID]
	if record == nil {
		r.mu.Unlock()
		return
	}
	if when.IsZero() {
		when = time.Now().UTC()
	}
	record.CompletionDeferredAt = when.UTC()
	r.persistSnapshotLocked()
	r.mu.Unlock()
}

// ReleaseDeferredAnnouncementsForRequester clears CompletionDeferredAt on all
// pending entries that belong to the given requester, allowing them to be
// included in the next flush. This enables batching: when the last sibling
// completes, all deferred siblings are released so they can be announced
// together in a single message.
func (r *SubagentRegistry) ReleaseDeferredAnnouncementsForRequester(requesterSessionKey string) {
	r.mu.Lock()
	changed := false
	for _, record := range r.runs {
		if record == nil || record.RequesterSessionKey != requesterSessionKey {
			continue
		}
		if record.EndedAt.IsZero() || record.CleanupHandled {
			continue
		}
		if !record.CompletionDeferredAt.IsZero() {
			record.CompletionDeferredAt = time.Time{}
			changed = true
		}
	}
	if changed {
		r.persistSnapshotLocked()
	}
	r.mu.Unlock()
}

func (r *SubagentRegistry) MarkCompletionAnnounceAttempt(runID string, err error) {
	r.mu.Lock()
	record := r.runs[runID]
	if record == nil {
		r.mu.Unlock()
		return
	}
	record.CompletionAttempts++
	if err != nil {
		record.CleanupHandled = false
		record.CleanupCompletedAt = time.Time{}
		record.AnnounceRetryCount++
		record.LastAnnounceRetryAt = time.Now().UTC()
		record.LastAnnounceError = strings.TrimSpace(err.Error())
		record.NextAnnounceAttemptAt = time.Now().UTC().Add(resolveSubagentAnnounceRetryDelay(record.CompletionAttempts))
		r.persistSnapshotLocked()
		r.mu.Unlock()
		return
	}
	record.CleanupHandled = false
	record.LastAnnounceError = ""
	record.NextAnnounceAttemptAt = time.Time{}
	record.AnnounceRetryCount = 0
	record.LastAnnounceRetryAt = time.Time{}
	r.persistSnapshotLocked()
	r.mu.Unlock()
}

func freezeSubagentResultText(result core.AgentRunResult, runErr error) string {
	if runErr != nil {
		return strings.TrimSpace(runErr.Error())
	}
	for i := len(result.Payloads) - 1; i >= 0; i-- {
		text := strings.TrimSpace(result.Payloads[i].Text)
		if text != "" {
			return text
		}
	}
	return ""
}

func resolveSubagentEndedReason(result core.AgentRunResult, runErr error) string {
	if errors.Is(runErr, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(runErr, context.Canceled) {
		return "killed"
	}
	if runErr != nil {
		return "error"
	}
	return "complete"
}

// IsRequesterDescendant returns true if requesterSessionKey descends from rootSessionKey.
func IsRequesterDescendant(requesterSessionKey string, rootSessionKey string) bool {
	requesterSessionKey = strings.TrimSpace(requesterSessionKey)
	rootSessionKey = strings.TrimSpace(rootSessionKey)
	if requesterSessionKey == "" || rootSessionKey == "" {
		return false
	}
	if requesterSessionKey == rootSessionKey {
		return true
	}
	return strings.Contains(requesterSessionKey, ":subagent:") && strings.HasPrefix(requesterSessionKey, rootSessionKey)
}

// SpawnSubagent validates a spawn request and registers a new subagent run.
func SpawnSubagent(ctx context.Context, registry *SubagentRegistry, req SubagentSpawnRequest) (SubagentSpawnResult, error) {
	select {
	case <-ctx.Done():
		return SubagentSpawnResult{}, ctx.Err()
	default:
	}

	if strings.TrimSpace(req.Task) == "" {
		return SubagentSpawnResult{}, errors.New("missing task")
	}
	if req.MaxSpawnDepth <= 0 {
		req.MaxSpawnDepth = 5
	}
	if req.MaxChildren <= 0 {
		req.MaxChildren = 5
	}
	if req.CurrentDepth >= req.MaxSpawnDepth {
		return SubagentSpawnResult{Status: "forbidden"}, fmt.Errorf("sessions_spawn depth limit reached (%d/%d)", req.CurrentDepth, req.MaxSpawnDepth)
	}
	if len(registry.ListByRequester(req.RequesterSessionKey)) >= req.MaxChildren {
		return SubagentSpawnResult{Status: "forbidden"}, fmt.Errorf("sessions_spawn max active children reached (%d/%d)", len(registry.ListByRequester(req.RequesterSessionKey)), req.MaxChildren)
	}

	targetAgentID := session.NormalizeAgentID(req.TargetAgentID)
	if targetAgentID == "" {
		targetAgentID = session.NormalizeAgentID(req.RequesterAgentID)
	}
	childSessionKey, err := session.BuildSubagentSessionKey(targetAgentID)
	if err != nil {
		return SubagentSpawnResult{}, err
	}
	runID, err := session.RandomToken(8)
	if err != nil {
		return SubagentSpawnResult{}, err
	}
	if req.Cleanup == "" {
		req.Cleanup = "delete"
	}
	if req.SpawnMode == "" {
		req.SpawnMode = "run"
	}
	if req.SpawnMode == "session" {
		req.Cleanup = "keep"
	}
	if !req.ExpectsCompletionMessageSet {
		req.ExpectsCompletionMessage = true
	}
	record := SubagentRunRecord{
		RunID:               runID,
		ChildKind:           "subagent",
		ChildSessionKey:     childSessionKey,
		RequesterSessionKey: req.RequesterSessionKey,
		RequesterDisplayKey: strings.TrimSpace(nonEmpty(req.RequesterDisplayKey, req.RequesterSessionKey)),
		RequesterOrigin: &core.DeliveryContext{
			Channel:   req.RouteChannel,
			To:        req.RouteTo,
			AccountID: req.RouteAccountID,
			ThreadID:  req.RouteThreadID,
		},
		Task:                     req.Task,
		Label:                    req.Label,
		Model:                    strings.TrimSpace(req.ModelOverride),
		Cleanup:                  req.Cleanup,
		SpawnMode:                req.SpawnMode,
		RouteChannel:             req.RouteChannel,
		RouteThreadID:            req.RouteThreadID,
		SpawnDepth:               req.CurrentDepth + 1,
		WorkspaceDir:             req.WorkspaceDir,
		RunTimeoutSeconds:        req.RunTimeoutSeconds,
		ExpectsCompletionMessage: req.ExpectsCompletionMessage,
		CreatedAt:                time.Now().UTC(),
		StartedAt:                time.Now().UTC(),
	}
	if req.SpawnMode != "session" && req.ArchiveAfterMinutes > 0 {
		record.ArchiveAt = record.CreatedAt.Add(time.Duration(req.ArchiveAfterMinutes) * time.Minute)
	}
	registry.Register(record)

	return SubagentSpawnResult{
		Status:          "accepted",
		ChildSessionKey: childSessionKey,
		RunID:           runID,
		Lane:            core.LaneSubagent,
		SpawnedBy:       req.RequesterSessionKey,
		SpawnDepth:      req.CurrentDepth + 1,
		WorkspaceDir:    req.WorkspaceDir,
		Note:            resolveSpawnAcceptedNote(req.SpawnMode),
	}, nil
}

func resolveSpawnAcceptedNote(spawnMode string) string {
	if strings.TrimSpace(strings.ToLower(spawnMode)) == "session" {
		return "Persistent child session created; later follow-up on the same bound thread or by session key should reuse this child session."
	}
	return "Auto-announce is push-based; parent should wait for completion events instead of polling."
}

// CascadeKillChildren recursively cancels all descendant subagent runs
// spawned by the given parent session key. Uses a visited set to prevent
// infinite loops in case of circular references.
func CascadeKillChildren(registry *SubagentRegistry, activeRuns *ActiveRunRegistry, parentChildSessionKey string, seen map[string]bool) {
	if seen == nil {
		seen = map[string]bool{}
	}
	if seen[parentChildSessionKey] {
		return
	}
	seen[parentChildSessionKey] = true

	children := registry.ListByRequester(parentChildSessionKey)
	for _, child := range children {
		if child.EndedAt.IsZero() {
			activeRuns.CancelSession(child.ChildSessionKey)
			registry.MarkTerminated(child.RunID, "cascade-killed")
		}
		CascadeKillChildren(registry, activeRuns, child.ChildSessionKey, seen)
	}
}
