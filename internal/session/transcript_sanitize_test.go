package session

import (
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
)

func TestNormalizeTranscriptMessageForWriteRedactsSessionsSpawnAttachments(t *testing.T) {
	msg := NormalizeTranscriptMessageForWrite(core.TranscriptMessage{
		Type:      "tool_call",
		Role:      "assistant",
		ToolName:  " sessions_spawn ",
		Timestamp: time.Unix(100, 0).UTC(),
		Args: map[string]any{
			"task": "inspect",
			"attachments": []any{
				map[string]any{
					"name":    "a.txt",
					"content": "SECRET",
				},
			},
		},
	})
	items, _ := msg.Args["attachments"].([]any)
	attachment, _ := items[0].(map[string]any)
	if attachment["content"] != redactedTranscriptAttachmentContent {
		t.Fatalf("expected redacted attachment content, got %+v", attachment)
	}
}
