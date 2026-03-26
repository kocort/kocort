package session

import (
	"strings"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/utils"
)

const redactedTranscriptAttachmentContent = "__KOCORT_REDACTED__"

func SanitizeTranscriptMessageForWrite(msg core.TranscriptMessage) core.TranscriptMessage {
	toolName := strings.TrimSpace(strings.ToLower(msg.ToolName))
	if toolName != "sessions_spawn" {
		return msg
	}
	if len(msg.Args) == 0 {
		return msg
	}
	msg.Args = redactSessionsSpawnTranscriptArgs(msg.Args)
	return msg
}

func redactSessionsSpawnTranscriptArgs(args map[string]any) map[string]any {
	cloned := utils.CloneAnyMap(args)
	rawItems, ok := cloned["attachments"].([]any)
	if !ok || len(rawItems) == 0 {
		return cloned
	}
	nextItems := make([]any, 0, len(rawItems))
	for _, item := range rawItems {
		entry, ok := item.(map[string]any)
		if !ok {
			nextItems = append(nextItems, item)
			continue
		}
		next := utils.CloneAnyMap(entry)
		if _, exists := next["content"]; exists {
			next["content"] = redactedTranscriptAttachmentContent
		}
		nextItems = append(nextItems, next)
	}
	cloned["attachments"] = nextItems
	return cloned
}
