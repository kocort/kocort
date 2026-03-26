package task

import (
	"context"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
)

func TestFollowupQueueEnqueue(t *testing.T) {
	q := NewFollowupQueue()
	ok := q.Enqueue(FollowupRun{
		QueueKey: "key1",
		Prompt:   "hello",
	}, QueueSettings{}, core.QueueDedupeNone)
	if !ok {
		t.Error("expected enqueue to succeed")
	}
	if q.Depth("key1") != 1 {
		t.Errorf("expected depth=1, got %d", q.Depth("key1"))
	}
}

func TestFollowupQueueEnqueueEmptyKey(t *testing.T) {
	q := NewFollowupQueue()
	ok := q.Enqueue(FollowupRun{QueueKey: "", Prompt: "hi"}, QueueSettings{}, core.QueueDedupeNone)
	if ok {
		t.Error("expected enqueue to fail for empty key")
	}
}

func TestFollowupQueueDedupePrompt(t *testing.T) {
	q := NewFollowupQueue()
	run := FollowupRun{QueueKey: "key1", Prompt: "hello"}
	q.Enqueue(run, QueueSettings{}, core.QueueDedupePrompt)
	ok := q.Enqueue(run, QueueSettings{}, core.QueueDedupePrompt)
	if ok {
		t.Error("expected dedup to reject duplicate prompt")
	}
	if q.Depth("key1") != 1 {
		t.Errorf("expected depth=1, got %d", q.Depth("key1"))
	}
}

func TestFollowupQueueDedupeMessage(t *testing.T) {
	q := NewFollowupQueue()
	run := FollowupRun{
		QueueKey:  "key1",
		Prompt:    "hello",
		MessageID: "msg_1",
	}
	q.Enqueue(run, QueueSettings{}, core.QueueDedupeMessageID)
	ok := q.Enqueue(run, QueueSettings{}, core.QueueDedupeMessageID)
	if ok {
		t.Error("expected dedup to reject duplicate messageID")
	}
}

func TestFollowupQueueClear(t *testing.T) {
	q := NewFollowupQueue()
	q.Enqueue(FollowupRun{QueueKey: "key1", Prompt: "a"}, QueueSettings{}, core.QueueDedupeNone)
	q.Enqueue(FollowupRun{QueueKey: "key1", Prompt: "b"}, QueueSettings{}, core.QueueDedupeNone)
	count := q.Clear("key1")
	if count != 2 {
		t.Errorf("expected 2 cleared, got %d", count)
	}
	if q.Depth("key1") != 0 {
		t.Errorf("expected depth=0, got %d", q.Depth("key1"))
	}
}

func TestFollowupQueueClearNonexistent(t *testing.T) {
	q := NewFollowupQueue()
	count := q.Clear("nonexistent")
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}
}

func TestFollowupQueueCapDrop(t *testing.T) {
	q := NewFollowupQueue()
	settings := QueueSettings{Cap: 2, DropPolicy: core.QueueDropOld}
	for i := 0; i < 5; i++ {
		q.Enqueue(FollowupRun{
			QueueKey: "key1",
			Prompt:   string(rune('a' + i)),
		}, settings, core.QueueDedupeNone)
	}
	// With cap=2 and drop-old policy, depth should never exceed 2.
	if q.Depth("key1") > 2 {
		t.Errorf("expected depth<=2, got %d", q.Depth("key1"))
	}
}

func TestFollowupQueueDropNew(t *testing.T) {
	q := NewFollowupQueue()
	settings := QueueSettings{Cap: 1, DropPolicy: core.QueueDropNew}
	q.Enqueue(FollowupRun{QueueKey: "key1", Prompt: "first"}, settings, core.QueueDedupeNone)
	ok := q.Enqueue(FollowupRun{QueueKey: "key1", Prompt: "second"}, settings, core.QueueDedupeNone)
	if ok {
		t.Error("expected enqueue to fail with drop-new policy at cap")
	}
	if q.Depth("key1") != 1 {
		t.Errorf("expected depth=1, got %d", q.Depth("key1"))
	}
}

func TestResolveActiveRunQueueAction(t *testing.T) {
	tests := []struct {
		name     string
		isActive bool
		isHB     bool
		followup bool
		mode     core.QueueMode
		want     core.ActiveRunQueueAction
	}{
		{"not_active", false, false, false, "", core.ActiveRunRunNow},
		{"heartbeat", true, true, false, "", core.ActiveRunDrop},
		{"followup", true, false, true, "", core.ActiveRunEnqueueFollowup},
		{"steer_mode", true, false, false, core.QueueModeSteer, core.ActiveRunEnqueueFollowup},
		{"active_no_followup", true, false, false, "", core.ActiveRunRunNow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveActiveRunQueueAction(tt.isActive, tt.isHB, tt.followup, tt.mode)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFollowupQueueScheduleDrain(t *testing.T) {
	q := NewFollowupQueue()
	// Override sleep to be instant for testing.
	q.SetSleep(func(_ context.Context, _ time.Duration) error {
		return nil
	})

	var drained []string
	q.Enqueue(FollowupRun{QueueKey: "key1", Prompt: "msg1"}, QueueSettings{Debounce: 0}, core.QueueDedupeNone)

	done := make(chan struct{})
	q.ScheduleDrain(context.Background(), "key1", func(run FollowupRun) error {
		drained = append(drained, run.Prompt)
		close(done)
		return nil
	})
	<-done
	if len(drained) != 1 || drained[0] != "msg1" {
		t.Errorf("expected [msg1], got %v", drained)
	}
}
