// compaction.go — session transcript compaction algorithm.
//
// This file contains the core compaction logic extracted from the runtime layer
// per RUNTIME_SLIM_PLAN P2-1.  All types and functions here use only packages
// that the session package is already allowed to import (core, config, os,
// stdlib), so no import cycles are introduced.
//
// The CompactionRunner interface is implemented by runtime.compactionRunnerAdapter,
// which bridges the gap to backend/infra without creating a cycle.
package session

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/utils"
)

// ---------------------------------------------------------------------------
// Result type
// ---------------------------------------------------------------------------

// CompactionResult holds the outcome of a session transcript compaction.
type CompactionResult struct {
	Summary         string
	CompactionCount int
	KeptCount       int
}

// ---------------------------------------------------------------------------
// Parameter types for runner callbacks
// ---------------------------------------------------------------------------

// CompactionTurnParams contains all inputs for a single compaction LLM turn.
// Every field uses only core package types to avoid import cycles with
// backend / infra / tool / rtypes.
type CompactionTurnParams struct {
	RunID        string
	AgentID      string
	Channel      string
	To           string
	AccountID    string
	ThreadID     string
	Timeout      time.Duration
	Session      core.SessionResolution
	Identity     core.AgentIdentity
	Selection    core.ModelSelection
	Summarizable []core.TranscriptMessage // used for fallback summary if LLM returns empty
	Prompt       string                   // pre-built compaction prompt (caller uses infra helpers)
	SystemPrompt string
	WorkspaceDir string
}

// MemoryFlushTurnParams contains all inputs for a pre-compaction memory flush turn.
// Every field uses only core package types.
type MemoryFlushTurnParams struct {
	RunID             string
	AgentID           string
	Channel           string
	To                string
	AccountID         string
	ThreadID          string
	Timeout           time.Duration
	Session           core.SessionResolution
	Identity          core.AgentIdentity
	Selection         core.ModelSelection
	History           []core.TranscriptMessage
	MemoryHits        []core.MemoryHit
	Skills            *core.SkillSnapshot
	InternalEvents    []core.TranscriptMessage
	BootstrapWarnings []string
	SystemPrompt      string // pre-built by caller using infra.BuildSystemPrompt
	Prompt            string // pre-built user prompt
	WorkspaceDir      string
}

// ---------------------------------------------------------------------------
// Dependency interfaces
// ---------------------------------------------------------------------------

// CompactionRunner abstracts LLM execution for compaction and memory-flush turns.
// The canonical implementation is runtime.compactionRunnerAdapter, which uses
// the Runtime's backend registry and infra helpers without creating an import cycle.
type CompactionRunner interface {
	// RunCompactionTurn runs an LLM turn to generate a session compaction summary.
	// The caller pre-builds the user prompt; this returns the raw summary text.
	RunCompactionTurn(ctx context.Context, params CompactionTurnParams) (string, error)

	// RunMemoryFlushTurn runs an LLM turn to persist memories before compaction.
	RunMemoryFlushTurn(ctx context.Context, params MemoryFlushTurnParams) error

	// EmitDebugEvent emits a debug event to webchat subscribers.
	EmitDebugEvent(sessionKey, runID, stream string, data map[string]any)
}

// CompactionSessionWriter is the minimal session-write surface needed by compaction.
// Satisfied by *SessionStore.
type CompactionSessionWriter interface {
	RewriteTranscript(key, sessionID string, msgs []core.TranscriptMessage) error
	Upsert(key string, entry core.SessionEntry) error
}

// ---------------------------------------------------------------------------
// Pure helper functions (exported for use by the runtime adapter/callers)
// ---------------------------------------------------------------------------

// SplitTranscriptForCompaction splits history into the summarizable older portion
// and the kept recent portion.
func SplitTranscriptForCompaction(history []core.TranscriptMessage, keepRecent int) (summarizable, kept []core.TranscriptMessage) {
	if len(history) == 0 {
		return nil, nil
	}
	if keepRecent <= 0 {
		return append([]core.TranscriptMessage{}, history...), nil
	}
	if len(history) <= keepRecent {
		return nil, append([]core.TranscriptMessage{}, history...)
	}
	cut := len(history) - keepRecent
	return append([]core.TranscriptMessage{}, history[:cut]...),
		append([]core.TranscriptMessage{}, history[cut:]...)
}

// CurrentCompactionCount returns the compaction count stored in the session entry.
func CurrentCompactionCount(sess core.SessionResolution) int {
	if sess.Entry == nil {
		return 0
	}
	return sess.Entry.CompactionCount
}

// ShouldRunPreCompactionMemoryFlush returns true if a memory flush should run
// before the next compaction.
func ShouldRunPreCompactionMemoryFlush(identity core.AgentIdentity, sess core.SessionResolution) bool {
	if !identity.MemoryEnabled || !identity.MemoryFlushEnabled {
		return false
	}
	if sess.Entry == nil {
		return true
	}
	if sess.Entry.MemoryFlushAt.IsZero() {
		return true
	}
	return sess.Entry.MemoryFlushCompactionCount != CurrentCompactionCount(sess)
}

// ---------------------------------------------------------------------------
// Main compaction function
// ---------------------------------------------------------------------------

// compactionSystemPrompt is the constant system prompt used for all compaction turns.
const compactionSystemPrompt = `You are compacting an existing session transcript.
Write a concise persistent summary of the older conversation so future turns can continue in the same session.
Preserve: user goals, standing preferences, unfinished tasks, promised follow-ups, key facts, important tool outcomes, and any constraints.
Do not include filler or conversational niceties.`

// CompactTranscript compacts the pre-split transcript and writes the result back to sessions.
//
// The caller is responsible for:
//  1. Splitting the transcript with SplitTranscriptForCompaction.
//  2. Building the compaction user prompt (typically using infra.BuildTranscriptPromptSection).
//  3. Providing a CompactionRunner that wraps the runtime backend.
func CompactTranscript(
	sessions CompactionSessionWriter,
	runner CompactionRunner,
	ctx context.Context,
	req core.AgentRunRequest,
	sess core.SessionResolution,
	identity core.AgentIdentity,
	selection core.ModelSelection,
	summarizable []core.TranscriptMessage,
	kept []core.TranscriptMessage,
	instructions string,
	prompt string, // pre-built by caller
) (CompactionResult, error) {
	if len(summarizable) == 0 {
		return CompactionResult{
			CompactionCount: CurrentCompactionCount(sess) + 1,
			KeptCount:       len(kept),
		}, nil
	}

	params := CompactionTurnParams{
		RunID:        req.RunID + ":compact",
		AgentID:      req.AgentID,
		Channel:      req.Channel,
		To:           req.To,
		AccountID:    req.AccountID,
		ThreadID:     req.ThreadID,
		Timeout:      req.Timeout,
		Session:      sess,
		Identity:     identity,
		Selection:    selection,
		Summarizable: summarizable,
		Prompt:       prompt,
		SystemPrompt: strings.TrimSpace(compactionSystemPrompt),
		WorkspaceDir: identity.WorkspaceDir,
	}

	// Use multi-stage compaction when the summarizable content exceeds a
	// single-chunk token budget.  SummarizeInStages splits into chunks,
	// summarizes each, then merges — falling through to a single-call
	// path when only one chunk is produced.
	stagedCfg := StagedCompactionConfig{MaxChunkTokens: 8000, SafetyFactor: 1.2, MaxRetries: 3}
	estimatedTokens := estimateTranscriptTokens(summarizable)
	var summary string
	var err error
	if estimatedTokens > stagedCfg.maxChunkTokens() {
		summary, err = SummarizeInStages(ctx, runner, params, summarizable, stagedCfg)
	} else {
		summary, err = runner.RunCompactionTurn(ctx, params)
	}
	if err != nil {
		return CompactionResult{}, err
	}

	if strings.TrimSpace(summary) == "" {
		summary = simpleFallbackSummary(summarizable)
	}

	now := time.Now().UTC()
	compactionEntry := core.TranscriptMessage{
		Type:             "compaction",
		Role:             "system",
		Text:             strings.TrimSpace(summary),
		Summary:          strings.TrimSpace(summary),
		Timestamp:        now,
		FirstKeptEntryID: firstKeptEntryID(kept),
		TokensBefore:     estimateTranscriptWords(summarizable),
		Instructions:     strings.TrimSpace(instructions),
	}
	rewritten := make([]core.TranscriptMessage, 0, 1+len(kept))
	rewritten = append(rewritten, compactionEntry)
	rewritten = append(rewritten, kept...)

	if err := sessions.RewriteTranscript(sess.SessionKey, sess.SessionID, rewritten); err != nil {
		return CompactionResult{}, err
	}

	entry := core.SessionEntry{SessionID: sess.SessionID}
	if sess.Entry != nil {
		entry = *sess.Entry
	}
	entry.SessionID = sess.SessionID
	entry.CompactionCount = CurrentCompactionCount(sess) + 1
	entry.LastActivityReason = "compact"
	if err := sessions.Upsert(sess.SessionKey, entry); err != nil {
		return CompactionResult{}, err
	}

	return CompactionResult{
		Summary:         strings.TrimSpace(summary),
		CompactionCount: entry.CompactionCount,
		KeptCount:       len(kept),
	}, nil
}

// ---------------------------------------------------------------------------
// Pre-compaction memory flush
// ---------------------------------------------------------------------------

// RunPreCompactionMemoryFlush runs a memory flush if conditions are met.
// The caller is responsible for pre-building params.SystemPrompt and params.Prompt
// using infra helpers (which session cannot import).
func RunPreCompactionMemoryFlush(
	sessions CompactionSessionWriter,
	runner CompactionRunner,
	ctx context.Context,
	params MemoryFlushTurnParams,
) error {
	identity := params.Identity
	sess := params.Session

	if !ShouldRunPreCompactionMemoryFlush(identity, sess) {
		return nil
	}
	workspaceDir := strings.TrimSpace(identity.WorkspaceDir)
	if workspaceDir == "" || !workspaceWritableForMemoryFlush(workspaceDir, identity) {
		runner.EmitDebugEvent(sess.SessionKey, params.RunID, "memory_flush", map[string]any{
			"type":   "memory_flush_skipped",
			"reason": "workspace_not_writable",
		})
		return nil
	}

	runner.EmitDebugEvent(sess.SessionKey, params.RunID, "memory_flush", map[string]any{
		"type":               "memory_flush_started",
		"compactionCount":    CurrentCompactionCount(sess),
		"softThreshold":      identity.MemoryFlushSoftThresholdTokens,
		"reserveTokensFloor": identity.CompactionReserveTokensFloor,
	})

	if err := runner.RunMemoryFlushTurn(ctx, params); err != nil {
		return err
	}

	entry := core.SessionEntry{SessionID: sess.SessionID}
	if sess.Entry != nil {
		entry = *sess.Entry
	}
	entry.SessionID = sess.SessionID
	entry.MemoryFlushAt = time.Now().UTC()
	entry.MemoryFlushCompactionCount = CurrentCompactionCount(sess)
	entry.LastActivityReason = utils.NonEmpty(entry.LastActivityReason, "turn")
	if err := sessions.Upsert(sess.SessionKey, entry); err != nil {
		return err
	}

	runner.EmitDebugEvent(sess.SessionKey, params.RunID, "memory_flush", map[string]any{
		"type":            "memory_flush_completed",
		"compactionCount": CurrentCompactionCount(sess),
	})
	return nil
}

// ---------------------------------------------------------------------------
// Private helpers
// ---------------------------------------------------------------------------

// workspaceWritableForMemoryFlush verifies write access to the workspace directory.
func workspaceWritableForMemoryFlush(workspaceDir string, identity core.AgentIdentity) bool {
	switch strings.ToLower(strings.TrimSpace(identity.SandboxWorkspaceAccess)) {
	case "ro", "none":
		return false
	}
	probe, err := os.CreateTemp(workspaceDir, ".kocort-memory-flush-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	_ = probe.Close()   // best-effort cleanup
	_ = os.Remove(name) // best-effort cleanup
	return true
}

// firstKeptEntryID returns the ID of the first message in the kept portion.
func firstKeptEntryID(msgs []core.TranscriptMessage) string {
	for _, msg := range msgs {
		if strings.TrimSpace(msg.ID) != "" {
			return strings.TrimSpace(msg.ID)
		}
	}
	return ""
}

// estimateTranscriptTokens estimates total tokens using chars/4 heuristic,
// consistent with the estimator in compaction_staged.go.
func estimateTranscriptTokens(history []core.TranscriptMessage) int {
	total := 0
	for _, msg := range history {
		total += (len(msg.Text) + 3) / 4
		total += (len(msg.Summary) + 3) / 4
	}
	if total < 1 {
		total = 1
	}
	return total
}

// estimateTranscriptWords estimates the token count of a transcript as a word count.
// This is a simple approximation that avoids importing infra.
func estimateTranscriptWords(history []core.TranscriptMessage) int {
	total := 0
	for _, msg := range history {
		total += len(strings.Fields(msg.Text))
		total += len(strings.Fields(msg.Summary))
	}
	return total
}

// simpleFallbackSummary generates a minimal summary when the LLM returns empty.
// This is intentionally simpler than infra.FormatTranscriptPromptLine to avoid
// creating an import dependency on infra.
func simpleFallbackSummary(history []core.TranscriptMessage) string {
	lines := []string{"Summary of earlier conversation:"}
	for _, msg := range history {
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "message"
		}
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", role, text))
	}
	return strings.Join(lines, "\n")
}
