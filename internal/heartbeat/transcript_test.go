package heartbeat

import (
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
)

func TestPruneRunTranscript(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	messages := []core.TranscriptMessage{
		{RunID: "run-1", Text: "heartbeat prompt", Timestamp: now},
		{RunID: "run-2", Text: "keep", Timestamp: now},
		{RunID: "run-1", Text: "heartbeat reply", Timestamp: now},
	}
	pruned := PruneRunTranscript(messages, "run-1")
	if len(pruned) != 1 {
		t.Fatalf("expected one message after pruning, got %d", len(pruned))
	}
	if pruned[0].RunID != "run-2" {
		t.Fatalf("expected run-2 to remain, got %+v", pruned[0])
	}
}
