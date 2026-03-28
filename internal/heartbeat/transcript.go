package heartbeat

import (
	"strings"

	"github.com/kocort/kocort/internal/core"
)

func PruneRunTranscript(messages []core.TranscriptMessage, runID string) []core.TranscriptMessage {
	runID = strings.TrimSpace(runID)
	if runID == "" || len(messages) == 0 {
		return messages
	}
	pruned := make([]core.TranscriptMessage, 0, len(messages))
	for _, msg := range messages {
		if strings.TrimSpace(msg.RunID) == runID {
			continue
		}
		pruned = append(pruned, msg)
	}
	return pruned
}
