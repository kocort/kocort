package task

import (
	"sort"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	// DefaultOrphanRecoveryDelay is the initial delay before attempting
	// orphan recovery after process restart.
	DefaultOrphanRecoveryDelay = 5 * time.Second

	// MaxOrphanRecoveryRetries is the maximum number of recovery retries per
	// orphaned run before giving up and marking it as failed.
	MaxOrphanRecoveryRetries = 3

	// OrphanRecoveryRetryBackoffMultiplier doubles the delay between retries.
	OrphanRecoveryRetryBackoffMultiplier = 2

	// OrphanResumeMaxTaskLen is the maximum number of characters from the
	// original task to include in the synthetic resume message.
	OrphanResumeMaxTaskLen = 2000

	// OrphanRecoveryMaxAge is the maximum age of an orphaned run that can
	// be recovered. Runs older than this are marked as failed without
	// attempting recovery.
	OrphanRecoveryMaxAge = 30 * time.Minute
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// OrphanRecoveryCandidate describes a subagent run that was interrupted by a
// process restart and is eligible for recovery.
type OrphanRecoveryCandidate struct {
	Record       SubagentRunRecord
	SessionEntry *core.SessionEntry
	LastUserMsg  string
}

// OrphanRecoveryPlan describes the planned recovery actions for a set of
// orphaned subagent runs.
type OrphanRecoveryPlan struct {
	Recoverable []OrphanRecoveryCandidate
	TooOld      []SubagentRunRecord
	NoSession   []SubagentRunRecord
}

// OrphanRecoveryResult captures the outcome of orphan recovery.
type OrphanRecoveryResult struct {
	Recovered int
	Failed    int
	Skipped   int
}

// OrphanRecoveryRunFunc is the callback the runtime supplies to actually
// re-launch a recovered subagent run.  It receives the rebuilt
// AgentRunRequest and returns the result + error.
type OrphanRecoveryRunFunc func(req core.AgentRunRequest) (core.AgentRunResult, error)

// ---------------------------------------------------------------------------
// Plan construction  (pure, no side-effects)
// ---------------------------------------------------------------------------

// BuildOrphanRecoveryPlan scans the subagent registry for runs that were
// still active when the process last exited (EndedAt zero, no active
// goroutine) and classifies them into recoverable, too-old and no-session
// buckets.
//
// The function intentionally lives in internal/task (not runtime) so it can
// be unit-tested without a full Runtime.
func BuildOrphanRecoveryPlan(
	registry *SubagentRegistry,
	store *session.SessionStore,
	activeRuns *ActiveRunRegistry,
	now time.Time,
) OrphanRecoveryPlan {
	if registry == nil {
		return OrphanRecoveryPlan{}
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()

	plan := OrphanRecoveryPlan{}
	for _, record := range registry.runs {
		if record == nil {
			continue
		}
		// Only consider runs that never received a completion.
		if !record.EndedAt.IsZero() {
			continue
		}
		// Skip runs that are genuinely still active (should not happen on
		// fresh startup but guards against double-recovery).
		if activeRuns != nil && activeRuns.IsRunActive(record.ChildSessionKey, record.RunID) {
			continue
		}
		// Too old to recover – the context and model state are stale.
		if now.Sub(record.CreatedAt) > OrphanRecoveryMaxAge {
			plan.TooOld = append(plan.TooOld, *record)
			continue
		}
		// Need an existing session entry to rebuild the resume request.
		var entry *core.SessionEntry
		if store != nil {
			entry = store.Entry(record.ChildSessionKey)
		}
		if entry == nil {
			plan.NoSession = append(plan.NoSession, *record)
			continue
		}

		lastUserMsg := resolveLastUserMessage(store, record.ChildSessionKey, record.Task)

		plan.Recoverable = append(plan.Recoverable, OrphanRecoveryCandidate{
			Record:       *record,
			SessionEntry: entry,
			LastUserMsg:  lastUserMsg,
		})
	}
	// Sort recoverable by creation time so older runs are recovered first.
	sort.SliceStable(plan.Recoverable, func(i, j int) bool {
		return plan.Recoverable[i].Record.CreatedAt.Before(plan.Recoverable[j].Record.CreatedAt)
	})
	return plan
}

// ---------------------------------------------------------------------------
// Resume request construction  (pure)
// ---------------------------------------------------------------------------

// BuildOrphanResumeRequest constructs a synthetic AgentRunRequest that
// tells the child agent to continue where it left off.
func BuildOrphanResumeRequest(candidate OrphanRecoveryCandidate) core.AgentRunRequest {
	record := candidate.Record
	taskSnippet := truncate(strings.TrimSpace(record.Task), OrphanResumeMaxTaskLen)

	resumeMsg := buildResumeMessage(taskSnippet, candidate.LastUserMsg)

	req := core.AgentRunRequest{
		RunID:             record.RunID,
		Message:           resumeMsg,
		SessionKey:        record.ChildSessionKey,
		AgentID:           session.ResolveAgentIDFromSessionKey(record.ChildSessionKey),
		Lane:              core.LaneSubagent,
		SpawnedBy:         record.RequesterSessionKey,
		SpawnDepth:        record.SpawnDepth,
		WorkspaceOverride: record.WorkspaceDir,
		Deliver:           false,
	}
	if record.RunTimeoutSeconds > 0 {
		req.Timeout = time.Duration(record.RunTimeoutSeconds) * time.Second
	}
	if record.RouteChannel != "" || record.RouteThreadID != "" {
		req.Channel = record.RouteChannel
		req.ThreadID = record.RouteThreadID
	}
	if record.Model != "" {
		provider, model, _ := parseModelOverrideSimple(record.Model)
		req.SessionProviderOverride = provider
		req.SessionModelOverride = model
	}
	return req
}

// ---------------------------------------------------------------------------
// Execution  (has side-effects on registry)
// ---------------------------------------------------------------------------

// ExecuteOrphanRecovery processes a recovery plan: marks too-old/no-session
// runs as failed, and launches recoverable runs using the provided run
// function.
//
// The runFn callback is typically a closure that calls r.Run() inside a
// goroutine — the same pattern used by SpawnSubagent.
func ExecuteOrphanRecovery(
	registry *SubagentRegistry,
	plan OrphanRecoveryPlan,
	runFn OrphanRecoveryRunFunc,
) OrphanRecoveryResult {
	result := OrphanRecoveryResult{}

	// Mark too-old runs as failed.
	for _, record := range plan.TooOld {
		registry.MarkTerminated(record.RunID, "orphan-too-old")
		result.Skipped++
	}

	// Mark no-session runs as failed.
	for _, record := range plan.NoSession {
		registry.MarkTerminated(record.RunID, "orphan-no-session")
		result.Skipped++
	}

	// Attempt to recover each candidate.
	for _, candidate := range plan.Recoverable {
		req := BuildOrphanResumeRequest(candidate)

		// Generate a replacement run ID so the registry tracks the resumed
		// execution under a fresh record while keeping history.
		replacement, ok, err := registry.RegisterSteerRestartReplacement(candidate.Record.RunID)
		if err != nil || !ok || replacement == nil {
			// Cannot create replacement — mark original as failed.
			registry.MarkTerminated(candidate.Record.RunID, "orphan-recovery-failed")
			result.Failed++
			continue
		}

		// Update the request with the replacement run ID.
		req.RunID = replacement.RunID

		if runFn != nil {
			// Fire-and-forget: the completion handler will take over.
			go func(r core.AgentRunRequest, origRunID, replRunID string) {
				_, runErr := runFn(r)
				if runErr != nil {
					// If the recovery run itself fails, we don't retry here;
					// the normal completion handler + announcement retry logic
					// will take over.
					_ = runErr
				}
			}(req, candidate.Record.RunID, replacement.RunID)
			result.Recovered++
		} else {
			// No run function — rollback and mark failed.
			registry.RestoreAfterFailedSteerRestart(candidate.Record.RunID, replacement.RunID)
			registry.MarkTerminated(candidate.Record.RunID, "orphan-no-runner")
			result.Failed++
		}
	}

	return result
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// resolveLastUserMessage finds the last user message in the child session
// transcript.  Falls back to the original task text if no transcript is
// available or empty.
func resolveLastUserMessage(store *session.SessionStore, sessionKey string, fallbackTask string) string {
	if store == nil {
		return truncate(strings.TrimSpace(fallbackTask), OrphanResumeMaxTaskLen)
	}
	messages, err := store.LoadTranscript(sessionKey)
	if err != nil || len(messages) == 0 {
		return truncate(strings.TrimSpace(fallbackTask), OrphanResumeMaxTaskLen)
	}
	// Walk backwards to find the last user message.
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && strings.TrimSpace(messages[i].Text) != "" {
			return truncate(strings.TrimSpace(messages[i].Text), OrphanResumeMaxTaskLen)
		}
	}
	return truncate(strings.TrimSpace(fallbackTask), OrphanResumeMaxTaskLen)
}

func buildResumeMessage(taskSnippet, lastUserMsg string) string {
	var sb strings.Builder
	sb.WriteString("[System: Your previous run was interrupted by a process restart. ")
	sb.WriteString("Please continue where you left off.]\n\n")
	if taskSnippet != "" {
		sb.WriteString("Original task: ")
		sb.WriteString(taskSnippet)
		sb.WriteString("\n\n")
	}
	if lastUserMsg != "" && lastUserMsg != taskSnippet {
		sb.WriteString("Last message before interruption: ")
		sb.WriteString(lastUserMsg)
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

func parseModelOverrideSimple(raw string) (string, string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", false
	}
	if idx := strings.Index(trimmed, "/"); idx >= 0 {
		provider := strings.TrimSpace(trimmed[:idx])
		model := strings.TrimSpace(trimmed[idx+1:])
		if provider == "" || model == "" {
			return "", "", false
		}
		return strings.ToLower(provider), model, true
	}
	return "", trimmed, true
}
