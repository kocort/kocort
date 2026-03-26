package session

import "github.com/kocort/kocort/internal/core"

const (
	// DefaultCompactionKeepRecentMessages is the retained tail length used by
	// current manual and overflow-driven compaction flows.
	DefaultCompactionKeepRecentMessages = 8

	// AutoCompactionInstructions is the default instruction string used when
	// compaction is triggered after context overflow.
	AutoCompactionInstructions = "Auto-compaction after context overflow."
)

// CompactionPlan captures the prepared inputs and reply semantics for a single
// compaction attempt.
type CompactionPlan struct {
	Instructions string
	Summarizable []core.TranscriptMessage
	Kept         []core.TranscriptMessage
}

// IsEmpty reports whether there is any transcript content that can be compacted.
func (p CompactionPlan) IsEmpty() bool {
	return len(p.Summarizable) == 0 && len(p.Kept) == 0
}

// EmptyResult returns the standard user-facing reply when there is nothing to compact.
func (p CompactionPlan) EmptyResult(runID string) core.AgentRunResult {
	return core.AgentRunResult{
		RunID:    runID,
		Payloads: []core.ReplyPayload{{Text: "Nothing to compact yet."}},
	}
}

// SuccessResult returns the standard user-facing manual compaction reply.
func (p CompactionPlan) SuccessResult(runID string, result CompactionResult) core.AgentRunResult {
	return core.AgentRunResult{
		RunID: runID,
		Payloads: []core.ReplyPayload{{
			Text: "Session compacted.",
		}},
		Meta: map[string]any{
			"compactionCount": result.CompactionCount,
			"keptCount":       result.KeptCount,
		},
	}
}

// PrepareCompactionPlan builds the summarizable/kept transcript slices and
// records the instruction string for a compaction attempt.
func PrepareCompactionPlan(history []core.TranscriptMessage, keepRecent int, instructions string) CompactionPlan {
	summarizable, kept := SplitTranscriptForCompaction(history, keepRecent)
	return CompactionPlan{
		Instructions: instructions,
		Summarizable: summarizable,
		Kept:         kept,
	}
}
