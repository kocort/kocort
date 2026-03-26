package session

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
)

// ---------------------------------------------------------------------------
// Multi-stage Compaction — Split → Parallel Summarize → Merge
//
// When the transcript is too large for a single LLM summarization call,
// the multi-stage strategy:
//   1. Splits messages into chunks by estimated token count
//   2. Summarizes each chunk in parallel via the CompactionRunner
//   3. Merges the chunk summaries into a final composite summary
//

// ---------------------------------------------------------------------------

// IdentifierPreservation controls how identifiers are preserved across compaction.
type IdentifierPreservation string

const (
	// IdentifierPreservationStrict preserves all code identifiers, variable names,
	// function names, etc. verbatim in summaries.
	IdentifierPreservationStrict IdentifierPreservation = "strict"

	// IdentifierPreservationCustom preserves user-specified identifiers only.
	IdentifierPreservationCustom IdentifierPreservation = "custom"

	// IdentifierPreservationOff allows the LLM to paraphrase freely.
	IdentifierPreservationOff IdentifierPreservation = "off"
)

// StagedCompactionConfig controls multi-stage compaction behavior.
type StagedCompactionConfig struct {
	// MaxChunkTokens is the maximum estimated tokens per chunk.
	// Default: 8000
	MaxChunkTokens int

	// SafetyFactor is the multiplier applied to MaxChunkTokens to avoid
	// exceeding LLM context limits. Default: 1.2
	SafetyFactor float64

	// MaxRetries is the number of retry attempts per chunk. Default: 3
	MaxRetries int

	// IdentifierMode controls identifier preservation in summaries.
	IdentifierMode IdentifierPreservation

	// CustomIdentifiers lists specific identifiers to preserve when
	// IdentifierMode == IdentifierPreservationCustom.
	CustomIdentifiers []string
}

func (c StagedCompactionConfig) maxChunkTokens() int {
	if c.MaxChunkTokens > 0 {
		return c.MaxChunkTokens
	}
	return 8000
}

func (c StagedCompactionConfig) safetyFactor() float64 {
	if c.SafetyFactor > 0 {
		return c.SafetyFactor
	}
	return 1.2
}

func (c StagedCompactionConfig) maxRetries() int {
	if c.MaxRetries > 0 {
		return c.MaxRetries
	}
	return 3
}

// ---------------------------------------------------------------------------
// Token estimation
// ---------------------------------------------------------------------------

const charsPerToken = 4

// estimateTokensForText returns a rough token estimate using chars/4 heuristic.
func estimateTokensForText(text string) int {
	n := len(text)
	if n == 0 {
		return 0
	}
	return (n + charsPerToken - 1) / charsPerToken
}

// estimateTokensForMessages returns combined estimated tokens for messages.
func estimateTokensForMessages(msgs []core.TranscriptMessage) int {
	total := 0
	for _, m := range msgs {
		total += estimateTokensForText(m.Text)
		total += estimateTokensForText(m.Summary)
	}
	return total
}

// ---------------------------------------------------------------------------
// Splitting functions
// ---------------------------------------------------------------------------

// SplitMessagesByTokenShare splits messages into approximately equal-sized
// parts by token count. Each part will have roughly totalTokens/parts tokens.
func SplitMessagesByTokenShare(msgs []core.TranscriptMessage, parts int) [][]core.TranscriptMessage {
	if parts <= 0 {
		parts = 1
	}
	if len(msgs) == 0 {
		return nil
	}
	if parts == 1 || len(msgs) == 1 {
		return [][]core.TranscriptMessage{append([]core.TranscriptMessage{}, msgs...)}
	}

	totalTokens := estimateTokensForMessages(msgs)
	if totalTokens == 0 {
		// All messages empty; split evenly by count.
		return splitByCount(msgs, parts)
	}

	targetPerPart := totalTokens / parts
	if targetPerPart == 0 {
		targetPerPart = 1
	}

	result := make([][]core.TranscriptMessage, 0, parts)
	var current []core.TranscriptMessage
	currentTokens := 0

	for _, msg := range msgs {
		msgTokens := estimateTokensForText(msg.Text) + estimateTokensForText(msg.Summary)
		current = append(current, msg)
		currentTokens += msgTokens

		if currentTokens >= targetPerPart && len(result) < parts-1 {
			result = append(result, current)
			current = nil
			currentTokens = 0
		}
	}
	if len(current) > 0 {
		result = append(result, current)
	}
	return result
}

// ChunkMessagesByMaxTokens splits messages into chunks where each chunk is
// estimated to be at most maxTokens * safetyFactor tokens.
func ChunkMessagesByMaxTokens(msgs []core.TranscriptMessage, maxTokens int, safetyFactor float64) [][]core.TranscriptMessage {
	if maxTokens <= 0 {
		maxTokens = 8000
	}
	if safetyFactor <= 0 {
		safetyFactor = 1.2
	}
	effectiveMax := int(float64(maxTokens) / safetyFactor)
	if effectiveMax <= 0 {
		effectiveMax = 1
	}

	if len(msgs) == 0 {
		return nil
	}

	result := make([][]core.TranscriptMessage, 0)
	var current []core.TranscriptMessage
	currentTokens := 0

	for _, msg := range msgs {
		msgTokens := estimateTokensForText(msg.Text) + estimateTokensForText(msg.Summary)

		// If adding this message would exceed the limit (and we have at least one message),
		// start a new chunk.
		if currentTokens+msgTokens > effectiveMax && len(current) > 0 {
			result = append(result, current)
			current = nil
			currentTokens = 0
		}

		current = append(current, msg)
		currentTokens += msgTokens
	}
	if len(current) > 0 {
		result = append(result, current)
	}
	return result
}

// ComputeAdaptiveChunkRatio determines how many chunks to split messages into
// based on their average token count and a target budget.
// Returns the number of parts (minimum 1).
func ComputeAdaptiveChunkRatio(msgs []core.TranscriptMessage, targetBudget int) int {
	if targetBudget <= 0 {
		targetBudget = 8000
	}
	total := estimateTokensForMessages(msgs)
	if total <= targetBudget {
		return 1
	}
	parts := int(math.Ceil(float64(total) / float64(targetBudget)))
	if parts < 1 {
		parts = 1
	}
	return parts
}

// ---------------------------------------------------------------------------
// Identifier preservation instructions
// ---------------------------------------------------------------------------

// buildIdentifierInstruction builds the identifier preservation instruction
// to prepend to compaction prompts.
func buildIdentifierInstruction(mode IdentifierPreservation, custom []string) string {
	switch mode {
	case IdentifierPreservationStrict:
		return "IMPORTANT: Preserve all code identifiers, variable names, function names, class names, file paths, URLs, and technical terms EXACTLY as written. Do not paraphrase or abbreviate them."
	case IdentifierPreservationCustom:
		if len(custom) == 0 {
			return ""
		}
		return fmt.Sprintf("IMPORTANT: Preserve these identifiers exactly as written: %s", strings.Join(custom, ", "))
	case IdentifierPreservationOff:
		return ""
	default:
		return ""
	}
}

// mergeSummaryInstruction is appended to the merge prompt to guide the LLM.
const mergeSummaryInstruction = `Merge the following section summaries into a single coherent summary.
Preserve: active tasks, batch progress, the user's last request, TODOs, key decisions, and all technical identifiers.
Deduplicate repeated information across sections.
Keep the result concise and well-organized.`

// ---------------------------------------------------------------------------
// Multi-stage summarization
// ---------------------------------------------------------------------------

// SummarizeInStages runs a multi-stage compaction:
//  1. Split messages into chunks via ChunkMessagesByMaxTokens
//  2. Summarize each chunk individually (with retry + fallback)
//  3. Merge chunk summaries into a final summary
//
// The runner.RunCompactionTurn is called for each chunk and for the merge.
func SummarizeInStages(
	ctx context.Context,
	runner CompactionRunner,
	baseParams CompactionTurnParams,
	summarizable []core.TranscriptMessage,
	config StagedCompactionConfig,
) (string, error) {
	chunks := ChunkMessagesByMaxTokens(summarizable, config.maxChunkTokens(), config.safetyFactor())

	if len(chunks) <= 1 {
		// Single chunk: no need for multi-stage.
		return SummarizeWithFallback(ctx, runner, baseParams, summarizable, config)
	}

	identInstr := buildIdentifierInstruction(config.IdentifierMode, config.CustomIdentifiers)

	// Stage 1: Summarize each chunk.
	chunkSummaries := make([]string, len(chunks))
	var firstErr error

	for i, chunk := range chunks {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		params := baseParams
		params.RunID = fmt.Sprintf("%s:compact-chunk-%d", baseParams.RunID, i)
		params.Summarizable = chunk

		// Build chunk prompt.
		promptParts := []string{}
		if identInstr != "" {
			promptParts = append(promptParts, identInstr)
		}
		promptParts = append(promptParts, fmt.Sprintf("Summarize the following section (%d of %d) of the conversation:", i+1, len(chunks)))
		promptParts = append(promptParts, formatMessagesForPrompt(chunk))
		params.Prompt = strings.Join(promptParts, "\n\n")

		summary, err := summarizeWithRetry(ctx, runner, params, chunk, config.maxRetries())
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			// Use fallback summary for this chunk.
			chunkSummaries[i] = simpleFallbackSummary(chunk)
			continue
		}
		chunkSummaries[i] = summary
	}

	// Stage 2: Merge chunk summaries.
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	mergeParams := baseParams
	mergeParams.RunID = baseParams.RunID + ":compact-merge"
	mergeParts := []string{mergeSummaryInstruction}
	if identInstr != "" {
		mergeParts = append(mergeParts, identInstr)
	}
	for i, s := range chunkSummaries {
		mergeParts = append(mergeParts, fmt.Sprintf("--- Section %d of %d ---\n%s", i+1, len(chunkSummaries), s))
	}
	mergeParams.Prompt = strings.Join(mergeParts, "\n\n")

	merged, err := summarizeWithRetry(ctx, runner, mergeParams, nil, config.maxRetries())
	if err != nil {
		// Fallback: concatenate chunk summaries.
		return strings.Join(chunkSummaries, "\n\n"), nil
	}
	return merged, nil
}

// SummarizeWithFallback summarizes messages with retry and fallback.
// For oversized single-chunk messages, falls back to a simple text summary.
func SummarizeWithFallback(
	ctx context.Context,
	runner CompactionRunner,
	params CompactionTurnParams,
	msgs []core.TranscriptMessage,
	config StagedCompactionConfig,
) (string, error) {
	identInstr := buildIdentifierInstruction(config.IdentifierMode, config.CustomIdentifiers)

	promptParts := []string{}
	if identInstr != "" {
		promptParts = append(promptParts, identInstr)
	}
	promptParts = append(promptParts, "Summarize the following conversation:")
	promptParts = append(promptParts, formatMessagesForPrompt(msgs))

	p := params
	p.Prompt = strings.Join(promptParts, "\n\n")

	summary, err := summarizeWithRetry(ctx, runner, p, msgs, config.maxRetries())
	if err != nil {
		return simpleFallbackSummary(msgs), nil
	}
	return summary, nil
}

// ---------------------------------------------------------------------------
// Retry with exponential backoff + jitter
// ---------------------------------------------------------------------------

// summarizeWithRetry calls RunCompactionTurn with retries on failure.
// Uses exponential backoff: base=1s, multiplier=2x, jitter=0-500ms.
func summarizeWithRetry(
	ctx context.Context,
	runner CompactionRunner,
	params CompactionTurnParams,
	fallbackMsgs []core.TranscriptMessage,
	maxRetries int,
) (string, error) {
	if maxRetries <= 0 {
		maxRetries = 3
	}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		if attempt > 0 {
			backoff := time.Duration(float64(time.Second) * math.Pow(2, float64(attempt-1)))
			jitter := time.Duration(rand.Int63n(int64(500 * time.Millisecond)))
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(backoff + jitter):
			}
		}

		summary, err := runner.RunCompactionTurn(ctx, params)
		if err != nil {
			lastErr = err
			continue
		}
		summary = strings.TrimSpace(summary)
		if summary == "" {
			if fallbackMsgs != nil {
				return simpleFallbackSummary(fallbackMsgs), nil
			}
			lastErr = fmt.Errorf("compaction returned empty summary")
			continue
		}
		return summary, nil
	}
	return "", lastErr
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// formatMessagesForPrompt formats transcript messages for use in a compaction prompt.
func formatMessagesForPrompt(msgs []core.TranscriptMessage) string {
	var lines []string
	for _, msg := range msgs {
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "message"
		}
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			text = strings.TrimSpace(msg.Summary)
		}
		if text == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("[%s]: %s", role, text))
	}
	return strings.Join(lines, "\n")
}

// splitByCount splits messages evenly by count when they're all zero tokens.
func splitByCount(msgs []core.TranscriptMessage, parts int) [][]core.TranscriptMessage {
	if parts <= 0 || len(msgs) == 0 {
		return nil
	}
	perPart := (len(msgs) + parts - 1) / parts
	result := make([][]core.TranscriptMessage, 0, parts)
	for i := 0; i < len(msgs); i += perPart {
		end := i + perPart
		if end > len(msgs) {
			end = len(msgs)
		}
		result = append(result, msgs[i:end])
	}
	return result
}
