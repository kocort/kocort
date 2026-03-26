package session

import (
	"testing"

	"github.com/kocort/kocort/internal/core"
)

func TestPrepareCompactionPlan(t *testing.T) {
	history := []core.TranscriptMessage{
		{Text: "1"},
		{Text: "2"},
		{Text: "3"},
	}
	plan := PrepareCompactionPlan(history, 1, "manual")
	if plan.Instructions != "manual" {
		t.Fatalf("unexpected instructions: %q", plan.Instructions)
	}
	if len(plan.Summarizable) != 2 || len(plan.Kept) != 1 {
		t.Fatalf("unexpected split: summarizable=%d kept=%d", len(plan.Summarizable), len(plan.Kept))
	}
}

func TestCompactionPlanResults(t *testing.T) {
	plan := CompactionPlan{}
	empty := plan.EmptyResult("run-empty")
	if empty.RunID != "run-empty" || len(empty.Payloads) != 1 || empty.Payloads[0].Text != "Nothing to compact yet." {
		t.Fatalf("unexpected empty result: %+v", empty)
	}
	success := plan.SuccessResult("run-ok", CompactionResult{CompactionCount: 3, KeptCount: 8})
	if success.RunID != "run-ok" || len(success.Payloads) != 1 || success.Payloads[0].Text != "Session compacted." {
		t.Fatalf("unexpected success result: %+v", success)
	}
	if success.Meta["compactionCount"] != 3 || success.Meta["keptCount"] != 8 {
		t.Fatalf("unexpected success meta: %+v", success.Meta)
	}
}
