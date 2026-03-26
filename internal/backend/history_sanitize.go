package backend

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// ---------------------------------------------------------------------------
// SanitizeHistoryPipeline applies the full history sanitization pipeline
// based on the TranscriptPolicy. Each layer is independently testable.

// SanitizeOpenAICompatMessages.
// ---------------------------------------------------------------------------

func SanitizeHistoryPipeline(
	messages []openai.ChatCompletionMessage,
	policy TranscriptPolicy,
	allowedToolNames map[string]bool,
) []openai.ChatCompletionMessage {
	if len(messages) == 0 {
		return nil
	}

	// Layer 1: Drop thinking blocks from assistant history
	if policy.DropThinkingBlocks {
		messages = DropThinkingBlocksFromHistory(messages)
	}

	// Layer 2: Remove tool calls for tools not in the allowed set
	if len(allowedToolNames) > 0 {
		messages = SanitizeToolCallInputs(messages, allowedToolNames)
	}

	// Layer 3: Repair tool_use / tool_result pairing
	if policy.RepairToolUseResultPairing {
		messages = RepairToolUseResultPairing(messages)
	}

	// Layer 4: Truncate overly long tool results
	messages = StripToolResultDetails(messages, defaultMaxToolResultLen)

	// Layer 5: Validate Anthropic turn structure
	if policy.ValidateAnthropicTurns {
		messages = ValidateAnthropicTurns(messages)
	}

	// Layer 6: Validate Gemini turn structure
	if policy.ValidateGeminiTurns {
		messages = ValidateGeminiTurns(messages)
	}

	// Layer 7: Limit history by user turn count
	if policy.HistoryTurnLimit > 0 {
		messages = LimitHistoryTurns(messages, policy.HistoryTurnLimit)
	}

	// Layer 8: Sanitize tool call IDs
	if policy.SanitizeToolCallIDs && policy.ToolCallIDMode != "" {
		messages = SanitizeToolCallIDs(messages, policy.ToolCallIDMode)
	}

	return messages
}

// ---------------------------------------------------------------------------
// Layer 1: DropThinkingBlocksFromHistory
// ---------------------------------------------------------------------------

var thinkBlockRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

// DropThinkingBlocksFromHistory removes <think>...</think> blocks from
// assistant message content in history. This prevents providers from
// rejecting replayed thinking content.
func DropThinkingBlocksFromHistory(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	out := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, msg := range messages {
		if msg.Role != "assistant" {
			out = append(out, msg)
			continue
		}
		content := extractOpenAICompatContent(msg.Content)
		cleaned := thinkBlockRe.ReplaceAllString(content, "")
		cleaned = strings.TrimSpace(cleaned)
		if cleaned == content {
			out = append(out, msg)
			continue
		}
		if cleaned == "" && len(msg.ToolCalls) == 0 {
			// Preserve the turn with fallback text
			cleaned = "[thinking content omitted]"
		}
		clone := msg
		clone.Content = cleaned
		out = append(out, clone)
	}
	return out
}

// ---------------------------------------------------------------------------
// Layer 2: SanitizeToolCallInputs
// ---------------------------------------------------------------------------

// SanitizeToolCallInputs removes tool calls from assistant messages when the
// tool name is not in the allowed set. Matching tool results are also removed.
func SanitizeToolCallInputs(messages []openai.ChatCompletionMessage, allowed map[string]bool) []openai.ChatCompletionMessage {
	if len(allowed) == 0 {
		return messages
	}
	// First pass: collect tool call IDs to remove
	removedIDs := map[string]bool{}
	for _, msg := range messages {
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		for _, tc := range msg.ToolCalls {
			if !allowed[strings.TrimSpace(tc.Function.Name)] {
				removedIDs[tc.ID] = true
			}
		}
	}
	if len(removedIDs) == 0 {
		return messages
	}

	out := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case "assistant":
			if len(msg.ToolCalls) == 0 {
				out = append(out, msg)
				continue
			}
			filtered := make([]openai.ToolCall, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				if !removedIDs[tc.ID] {
					filtered = append(filtered, tc)
				}
			}
			clone := msg
			clone.ToolCalls = filtered
			if len(filtered) == 0 {
				text := strings.TrimSpace(extractOpenAICompatContent(msg.Content))
				if text == "" {
					continue // drop empty assistant message
				}
			}
			out = append(out, clone)
		case "tool":
			if removedIDs[strings.TrimSpace(msg.ToolCallID)] {
				continue // drop orphaned tool result
			}
			out = append(out, msg)
		default:
			out = append(out, msg)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Layer 3: RepairToolUseResultPairing
// ---------------------------------------------------------------------------

// RepairToolUseResultPairing ensures every assistant tool call has a matching
// tool result and vice versa. Missing results get synthetic error entries;
// orphan results are dropped; duplicates are removed.
func RepairToolUseResultPairing(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	if len(messages) == 0 {
		return messages
	}

	out := make([]openai.ChatCompletionMessage, 0, len(messages))
	// Track which tool call IDs have been seen
	pendingCallIDs := map[string]string{} // id -> tool name
	answeredIDs := map[string]bool{}

	for _, msg := range messages {
		switch msg.Role {
		case "assistant":
			out = append(out, msg)
			for _, tc := range msg.ToolCalls {
				id := strings.TrimSpace(tc.ID)
				if id != "" {
					pendingCallIDs[id] = strings.TrimSpace(tc.Function.Name)
				}
			}
		case "tool":
			toolCallID := strings.TrimSpace(msg.ToolCallID)
			if toolCallID == "" {
				continue // drop: no ID
			}
			if _, pending := pendingCallIDs[toolCallID]; !pending {
				continue // drop: orphan result with no matching call
			}
			if answeredIDs[toolCallID] {
				continue // drop: duplicate result
			}
			answeredIDs[toolCallID] = true
			out = append(out, msg)
		default:
			// Before adding non-tool message, flush any unanswered tool calls
			// by inserting synthetic error results.
			for id, name := range pendingCallIDs {
				if !answeredIDs[id] {
					out = append(out, openai.ChatCompletionMessage{
						Role:       "tool",
						ToolCallID: id,
						Name:       name,
						Content:    "[tool result unavailable — execution was interrupted or timed out]",
					})
					answeredIDs[id] = true
				}
			}
			out = append(out, msg)
		}
	}

	// Flush any remaining unanswered calls at end of messages
	for id, name := range pendingCallIDs {
		if !answeredIDs[id] {
			out = append(out, openai.ChatCompletionMessage{
				Role:       "tool",
				ToolCallID: id,
				Name:       name,
				Content:    "[tool result unavailable — execution was interrupted or timed out]",
			})
		}
	}

	return out
}

// ---------------------------------------------------------------------------
// Layer 4: StripToolResultDetails
// ---------------------------------------------------------------------------

const defaultMaxToolResultLen = 32_000 // ~8K tokens

// StripToolResultDetails truncates tool result content that exceeds maxLen
// characters.
func StripToolResultDetails(messages []openai.ChatCompletionMessage, maxLen int) []openai.ChatCompletionMessage {
	if maxLen <= 0 {
		return messages
	}
	touched := false
	out := make([]openai.ChatCompletionMessage, len(messages))
	for i, msg := range messages {
		if msg.Role == "tool" {
			content := extractOpenAICompatContent(msg.Content)
			if len(content) > maxLen {
				clone := msg
				clone.Content = content[:maxLen] + "\n[... truncated]"
				out[i] = clone
				touched = true
				continue
			}
		}
		out[i] = msg
	}
	if !touched {
		return messages
	}
	return out
}

// ---------------------------------------------------------------------------
// Layer 5: ValidateAnthropicTurns
// ---------------------------------------------------------------------------

// ValidateAnthropicTurns strips dangling tool_use blocks from assistant
// messages when the following user/tool turn doesn't contain matching
// tool_results, and merges consecutive user turns.
func ValidateAnthropicTurns(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	if len(messages) == 0 {
		return messages
	}

	// Step 1: Strip dangling tool_use blocks
	messages = stripDanglingToolCalls(messages)

	// Step 2: Merge consecutive user turns
	messages = mergeConsecutiveTurns(messages, "user")

	return messages
}

// stripDanglingToolCalls removes tool calls from assistant messages
// where the immediately following messages don't contain matching tool results.
func stripDanglingToolCalls(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	out := make([]openai.ChatCompletionMessage, 0, len(messages))

	for i, msg := range messages {
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			out = append(out, msg)
			continue
		}

		// Collect tool result IDs from the next contiguous tool messages
		respondedIDs := map[string]bool{}
		for j := i + 1; j < len(messages); j++ {
			if messages[j].Role != "tool" {
				break
			}
			if id := strings.TrimSpace(messages[j].ToolCallID); id != "" {
				respondedIDs[id] = true
			}
		}

		// Filter out tool calls that don't have matching results
		filtered := make([]openai.ToolCall, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			if respondedIDs[strings.TrimSpace(tc.ID)] {
				filtered = append(filtered, tc)
			}
		}

		clone := msg
		clone.ToolCalls = filtered
		if len(filtered) == 0 {
			text := strings.TrimSpace(extractOpenAICompatContent(msg.Content))
			if text == "" {
				clone.Content = "[tool calls omitted]"
			}
		}
		out = append(out, clone)
	}

	return out
}

// ---------------------------------------------------------------------------
// Layer 6: ValidateGeminiTurns
// ---------------------------------------------------------------------------

// ValidateGeminiTurns merges consecutive assistant turns, as required by the
// Google Gemini API.
func ValidateGeminiTurns(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	return mergeConsecutiveTurns(messages, "assistant")
}

// mergeConsecutiveTurns merges adjacent messages with the same role.
func mergeConsecutiveTurns(messages []openai.ChatCompletionMessage, targetRole string) []openai.ChatCompletionMessage {
	if len(messages) == 0 {
		return messages
	}
	out := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, msg := range messages {
		if msg.Role != targetRole || len(out) == 0 || out[len(out)-1].Role != targetRole {
			out = append(out, msg)
			continue
		}
		// Merge into previous message of same role
		prev := &out[len(out)-1]
		prevText := strings.TrimSpace(extractOpenAICompatContent(prev.Content))
		msgText := strings.TrimSpace(extractOpenAICompatContent(msg.Content))
		if msgText != "" {
			if prevText != "" {
				prev.Content = prevText + "\n\n" + msgText
			} else {
				prev.Content = msgText
			}
		}
		// Merge tool calls if both are assistant messages
		if targetRole == "assistant" && len(msg.ToolCalls) > 0 {
			prev.ToolCalls = append(prev.ToolCalls, msg.ToolCalls...)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Layer 7: LimitHistoryTurns
// ---------------------------------------------------------------------------

// LimitHistoryTurns truncates history to retain at most `limit` user turns,
// counting from the end. The system message (if first) is always preserved.
func LimitHistoryTurns(messages []openai.ChatCompletionMessage, limit int) []openai.ChatCompletionMessage {
	if limit <= 0 || len(messages) == 0 {
		return messages
	}

	// Preserve system message at index 0
	var systemMsg *openai.ChatCompletionMessage
	startIdx := 0
	if len(messages) > 0 && messages[0].Role == "system" {
		systemMsg = &messages[0]
		startIdx = 1
	}

	history := messages[startIdx:]
	userCount := 0
	cutIdx := 0

	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "user" {
			userCount++
			if userCount > limit {
				cutIdx = i + 1
				break
			}
		}
	}

	truncated := history[cutIdx:]
	if systemMsg != nil {
		result := make([]openai.ChatCompletionMessage, 0, len(truncated)+1)
		result = append(result, *systemMsg)
		result = append(result, truncated...)
		return result
	}
	return truncated
}

// ---------------------------------------------------------------------------
// Layer 8: SanitizeToolCallIDs
// ---------------------------------------------------------------------------

// SanitizeToolCallIDs rewrites tool call IDs to match provider format
// requirements.
//   - "strict": alphanumeric only, preserves length
//   - "strict9": alphanumeric, exactly 9 characters (Mistral)
func SanitizeToolCallIDs(messages []openai.ChatCompletionMessage, mode string) []openai.ChatCompletionMessage {
	if mode == "" {
		return messages
	}

	// Build a mapping of old IDs to new IDs
	idMap := map[string]string{}
	for _, msg := range messages {
		if msg.Role == "assistant" {
			for _, tc := range msg.ToolCalls {
				oldID := strings.TrimSpace(tc.ID)
				if oldID == "" {
					continue
				}
				if _, exists := idMap[oldID]; !exists {
					idMap[oldID] = sanitizeToolCallID(oldID, mode)
				}
			}
		}
	}

	if len(idMap) == 0 {
		return messages
	}

	// Check if any IDs actually changed
	anyChanged := false
	for old, new_ := range idMap {
		if old != new_ {
			anyChanged = true
			break
		}
	}
	if !anyChanged {
		return messages
	}

	// Rewrite IDs
	out := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case "assistant":
			if len(msg.ToolCalls) == 0 {
				out = append(out, msg)
				continue
			}
			clone := msg
			clone.ToolCalls = make([]openai.ToolCall, len(msg.ToolCalls))
			for i, tc := range msg.ToolCalls {
				clone.ToolCalls[i] = tc
				if newID, ok := idMap[strings.TrimSpace(tc.ID)]; ok {
					clone.ToolCalls[i].ID = newID
				}
			}
			out = append(out, clone)
		case "tool":
			if newID, ok := idMap[strings.TrimSpace(msg.ToolCallID)]; ok {
				clone := msg
				clone.ToolCallID = newID
				out = append(out, clone)
			} else {
				out = append(out, msg)
			}
		default:
			out = append(out, msg)
		}
	}
	return out
}

var nonAlphanumericRe = regexp.MustCompile(`[^a-zA-Z0-9]`)

func sanitizeToolCallID(id string, mode string) string {
	cleaned := nonAlphanumericRe.ReplaceAllString(id, "")
	switch mode {
	case "strict9":
		if len(cleaned) > 9 {
			cleaned = cleaned[:9]
		}
		for len(cleaned) < 9 {
			cleaned += randomAlphanumChar()
		}
		return cleaned
	case "strict":
		if cleaned == "" {
			cleaned = randomAlphanumString(12)
		}
		return cleaned
	default:
		return id
	}
}

func randomAlphanumChar() string {
	b := make([]byte, 1)
	_, _ = rand.Read(b)
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	return string(chars[int(b[0])%len(chars)])
}

func randomAlphanumString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// collectAllowedToolNames builds a set of allowed tool names from available tools.
func collectAllowedToolNames(tools []interface{ Name() string }) map[string]bool {
	if len(tools) == 0 {
		return nil
	}
	names := make(map[string]bool, len(tools))
	for _, t := range tools {
		if name := strings.TrimSpace(t.Name()); name != "" {
			names[name] = true
		}
	}
	return names
}

// collectAllowedToolNamesFromStrings builds a set from string slice.
func collectAllowedToolNamesFromStrings(names []string) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	m := make(map[string]bool, len(names))
	for _, name := range names {
		if n := strings.TrimSpace(name); n != "" {
			m[n] = true
		}
	}
	return m
}

// formatSyntheticToolCallID generates a deterministic synthetic ID for
// repair operations using a prefix and a counter.
func formatSyntheticToolCallID(prefix string, counter int) string {
	return fmt.Sprintf("%s_%04d", prefix, counter)
}
