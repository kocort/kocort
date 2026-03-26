package task

import (
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"
)

// RegisterSteerRestartReplacement suppresses completion announce for the
// existing run and registers a fresh replacement run record for the same child
// session. This keeps steer lifecycle state in internal/task instead of the
// tool/runtime layers.
func (r *SubagentRegistry) RegisterSteerRestartReplacement(runID string) (*SubagentRunRecord, bool, error) {
	if r == nil {
		return nil, false, nil
	}
	newRunID, err := session.RandomToken(8)
	if err != nil {
		return nil, false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	existing := r.runs[runID]
	if existing == nil {
		return nil, false, nil
	}
	now := time.Now().UTC()
	existing.SuppressAnnounceReason = "steer-restart"
	existing.AnnounceDeliveryPath = "steered"
	existing.NextAnnounceAttemptAt = time.Time{}
	existing.CompletionDeferredAt = time.Time{}
	replacement := &SubagentRunRecord{
		RunID:                    newRunID,
		ChildSessionKey:          existing.ChildSessionKey,
		RequesterSessionKey:      existing.RequesterSessionKey,
		RequesterDisplayKey:      existing.RequesterDisplayKey,
		RequesterOrigin:          cloneDeliveryContext(existing.RequesterOrigin),
		SteeredFromRunID:         existing.RunID,
		Task:                     existing.Task,
		Label:                    existing.Label,
		Model:                    existing.Model,
		Cleanup:                  existing.Cleanup,
		SpawnMode:                existing.SpawnMode,
		RouteChannel:             existing.RouteChannel,
		RouteThreadID:            existing.RouteThreadID,
		SpawnDepth:               existing.SpawnDepth,
		WorkspaceDir:             existing.WorkspaceDir,
		RunTimeoutSeconds:        existing.RunTimeoutSeconds,
		AttachmentsDir:           strings.TrimSpace(existing.AttachmentsDir),
		AttachmentsRootDir:       strings.TrimSpace(existing.AttachmentsRootDir),
		RetainAttachmentsOnKeep:  existing.RetainAttachmentsOnKeep,
		ExpectsCompletionMessage: existing.ExpectsCompletionMessage,
		CreatedAt:                now,
		StartedAt:                now,
	}
	existing.ReplacementRunID = newRunID
	r.runs[newRunID] = replacement
	r.persistSnapshotLocked()
	copy := *replacement
	return &copy, true, nil
}

// RestoreAfterFailedSteerRestart removes a replacement run and restores the
// original run's announcement state when steer dispatch fails.
func (r *SubagentRegistry) RestoreAfterFailedSteerRestart(originalRunID string, replacementRunID string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	changed := false
	if replacement := r.runs[replacementRunID]; replacement != nil {
		delete(r.runs, replacementRunID)
		changed = true
	}
	if original := r.runs[originalRunID]; original != nil && strings.TrimSpace(original.SuppressAnnounceReason) == "steer-restart" {
		original.SuppressAnnounceReason = ""
		original.AnnounceDeliveryPath = ""
		original.ReplacementRunID = ""
		changed = true
	}
	if changed {
		r.persistSnapshotLocked()
	}
	return changed
}

func cloneDeliveryContext(value *core.DeliveryContext) *core.DeliveryContext {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
