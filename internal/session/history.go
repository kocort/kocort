// history.go — chat history pagination and transcript collapsing.
//
// Moved from runtime/runtime_chat_api.go so that callers outside the runtime
// package (e.g. api/) can use these utilities without importing the full
// runtime layer.
package session

import (
	"strings"

	"github.com/kocort/kocort/internal/core"
)

const (
	defaultChatHistoryLimit = 200
	hardMaxChatHistoryLimit = 1000
)

// TranscriptLoader loads a session transcript by session key.
// Satisfied by *SessionStore and test doubles.
type TranscriptLoader interface {
	LoadTranscript(sessionKey string) ([]core.TranscriptMessage, error)
}

// LoadChatHistoryPage loads a paginated slice of collapsed chat history.
// It requires only a TranscriptLoader (satisfied by *SessionStore) rather
// than the full Runtime.
func LoadChatHistoryPage(loader TranscriptLoader, sessionKey string, limit int, before int) ([]core.TranscriptMessage, int, bool, int, error) {
	history, err := loader.LoadTranscript(sessionKey)
	if err != nil {
		return nil, 0, false, 0, err
	}
	history = CollapseChatHistoryMessages(history)
	history = filterInternalMessages(history)
	resolvedLimit := limit
	if resolvedLimit <= 0 {
		resolvedLimit = defaultChatHistoryLimit
	}
	if resolvedLimit > hardMaxChatHistoryLimit {
		resolvedLimit = hardMaxChatHistoryLimit
	}
	total := len(history)
	if total == 0 {
		return nil, 0, false, 0, nil
	}
	end := total
	if before > 0 && before < end {
		end = before
	}
	start := end - resolvedLimit
	if start < 0 {
		start = 0
	}
	raw := history[start:end]
	page := make([]core.TranscriptMessage, len(raw))
	for i, msg := range raw {
		if strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
			msg.Text = stripTimestampPrefix(msg.Text)
		}
		page[i] = msg
	}
	hasMore := start > 0
	nextBefore := 0
	if hasMore {
		nextBefore = start
	}
	return page, total, hasMore, nextBefore, nil
}

// CollapseChatHistoryMessages merges assistant_partial / assistant_final
// chains so that the final message replaces any preceding partial ones.
func CollapseChatHistoryMessages(history []core.TranscriptMessage) []core.TranscriptMessage {
	if len(history) == 0 {
		return nil
	}
	collapsed := make([]core.TranscriptMessage, 0, len(history))
	for i := 0; i < len(history); i++ {
		current := history[i]
		if strings.EqualFold(strings.TrimSpace(current.Role), "assistant") &&
			strings.EqualFold(strings.TrimSpace(current.Type), "assistant_partial") {
			j := i + 1
			var finalIndex = -1
			for j < len(history) {
				next := history[j]
				if !strings.EqualFold(strings.TrimSpace(next.Role), "assistant") {
					break
				}
				nextType := strings.TrimSpace(strings.ToLower(next.Type))
				if nextType == "assistant_final" {
					finalIndex = j
				}
				if nextType != "assistant_partial" && nextType != "assistant_final" {
					break
				}
				j++
			}
			if finalIndex >= 0 {
				i = finalIndex - 1
				continue
			}
		}
		collapsed = append(collapsed, current)
	}
	return collapsed
}

// filterInternalMessages removes messages that are internal to the
// orchestrator (e.g. subagent completion announcements injected as user
// role messages) and should not be surfaced in the chat UI.
func filterInternalMessages(history []core.TranscriptMessage) []core.TranscriptMessage {
	if len(history) == 0 {
		return nil
	}
	filtered := make([]core.TranscriptMessage, 0, len(history))
	for _, msg := range history {
		if isInternalMessage(msg) {
			continue
		}
		filtered = append(filtered, msg)
	}
	return filtered
}

// isInternalMessage reports whether a transcript message is an internal
// orchestrator message that should be hidden from the end user.
func isInternalMessage(msg core.TranscriptMessage) bool {
	msgType := strings.TrimSpace(strings.ToLower(msg.Type))
	switch msgType {
	case "subagent_completion":
		return true
	case "tool_call", "tool_result", "compaction":
		return true
	}
	return false
}

// stripTimestampPrefix removes the "[...] " prefix injected by
// infra.InjectTimestamp. Kept local to avoid an import cycle with infra.
func stripTimestampPrefix(message string) string {
	trimmed := strings.TrimSpace(message)
	if strings.HasPrefix(trimmed, "[") {
		if end := strings.Index(trimmed, "] "); end > 0 {
			return strings.TrimSpace(trimmed[end+2:])
		}
	}
	return trimmed
}
