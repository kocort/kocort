package infra

import (
	"testing"
)

func TestSystemEventQueue_Enqueue(t *testing.T) {
	q := NewSystemEventQueue()

	t.Run("basic_enqueue", func(t *testing.T) {
		ok := q.Enqueue("session-1", "event text", "ctx-key")
		if !ok {
			t.Error("expected true for first enqueue")
		}
	})

	t.Run("empty_session", func(t *testing.T) {
		ok := q.Enqueue("", "text", "")
		if ok {
			t.Error("empty session should return false")
		}
	})

	t.Run("empty_text", func(t *testing.T) {
		ok := q.Enqueue("session-1", "", "")
		if ok {
			t.Error("empty text should return false")
		}
	})

	t.Run("dedupes_consecutive", func(t *testing.T) {
		q2 := NewSystemEventQueue()
		q2.Enqueue("s1", "same text", "key")
		ok := q2.Enqueue("s1", "same text", "key")
		if ok {
			t.Error("duplicate should return false")
		}
	})

	t.Run("different_text_ok", func(t *testing.T) {
		q2 := NewSystemEventQueue()
		q2.Enqueue("s1", "text1", "key")
		ok := q2.Enqueue("s1", "text2", "key")
		if !ok {
			t.Error("different text should be accepted")
		}
	})

	t.Run("same_text_different_context", func(t *testing.T) {
		q2 := NewSystemEventQueue()
		q2.Enqueue("s1", "same", "ctx1")
		ok := q2.Enqueue("s1", "same", "ctx2")
		if !ok {
			t.Error("different context key should be accepted")
		}
	})

	t.Run("nil_safe", func(t *testing.T) {
		var q *SystemEventQueue
		ok := q.Enqueue("s", "t", "")
		if ok {
			t.Error("nil queue should return false")
		}
	})

	t.Run("cap_at_20", func(t *testing.T) {
		q2 := NewSystemEventQueue()
		for i := 0; i < 25; i++ {
			q2.Enqueue("s1", "event "+string(rune('A'+i)), "")
		}
		events := q2.Peek("s1")
		if len(events) != 20 {
			t.Errorf("expected cap at 20, got %d", len(events))
		}
	})
}

func TestSystemEventQueue_Drain(t *testing.T) {
	q := NewSystemEventQueue()
	q.Enqueue("s1", "event1", "")
	q.Enqueue("s1", "event2", "")

	events := q.Drain("s1")
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Text != "event1" {
		t.Errorf("first event = %q", events[0].Text)
	}

	// Drain again should be empty
	events2 := q.Drain("s1")
	if len(events2) != 0 {
		t.Errorf("second drain should be empty, got %d", len(events2))
	}

	t.Run("empty_session", func(t *testing.T) {
		if q.Drain("") != nil {
			t.Error("empty session should return nil")
		}
	})

	t.Run("nil_safe", func(t *testing.T) {
		var q *SystemEventQueue
		if q.Drain("s1") != nil {
			t.Error("nil queue should return nil")
		}
	})
}

func TestSystemEventQueue_Peek(t *testing.T) {
	q := NewSystemEventQueue()
	q.Enqueue("s1", "peek-event", "")

	events := q.Peek("s1")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	// Peek again should still have events
	events2 := q.Peek("s1")
	if len(events2) != 1 {
		t.Error("peek should not consume events")
	}

	t.Run("nil_safe", func(t *testing.T) {
		var q *SystemEventQueue
		if q.Peek("s1") != nil {
			t.Error("nil queue should return nil")
		}
	})

	t.Run("empty_key", func(t *testing.T) {
		if q.Peek("") != nil {
			t.Error("empty key should return nil")
		}
	})
}

func TestSystemEventQueue_Has(t *testing.T) {
	q := NewSystemEventQueue()

	if q.Has("s1") {
		t.Error("empty queue should not have events")
	}

	q.Enqueue("s1", "event", "")
	if !q.Has("s1") {
		t.Error("should have events after enqueue")
	}

	q.Drain("s1")
	if q.Has("s1") {
		t.Error("should not have events after drain")
	}
}

func TestSystemEventQueue_Timestamp(t *testing.T) {
	q := NewSystemEventQueue()
	q.Enqueue("s1", "timed event", "")
	events := q.Peek("s1")
	if len(events) == 0 {
		t.Fatal("expected events")
	}
	if events[0].Timestamp.IsZero() {
		t.Error("timestamp should be set")
	}
}
