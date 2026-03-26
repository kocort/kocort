package infra

import (
	"strings"
	"sync"
	"time"
)

type SystemEvent struct {
	Text       string
	Timestamp  time.Time
	ContextKey string
}

type SystemEventQueue struct {
	mu     sync.Mutex
	queues map[string][]SystemEvent
}

func NewSystemEventQueue() *SystemEventQueue {
	return &SystemEventQueue{queues: map[string][]SystemEvent{}}
}

func (q *SystemEventQueue) Enqueue(sessionKey, text, contextKey string) bool {
	if q == nil {
		return false
	}
	sessionKey = strings.TrimSpace(sessionKey)
	text = strings.TrimSpace(text)
	contextKey = strings.ToLower(strings.TrimSpace(contextKey))
	if sessionKey == "" || text == "" {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	list := q.queues[sessionKey]
	if len(list) > 0 {
		last := list[len(list)-1]
		if last.Text == text && last.ContextKey == contextKey {
			return false
		}
	}
	list = append(list, SystemEvent{Text: text, Timestamp: time.Now().UTC(), ContextKey: contextKey})
	if len(list) > 20 {
		list = list[len(list)-20:]
	}
	q.queues[sessionKey] = list
	return true
}

func (q *SystemEventQueue) Drain(sessionKey string) []SystemEvent {
	if q == nil {
		return nil
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	list := q.queues[sessionKey]
	if len(list) == 0 {
		return nil
	}
	out := append([]SystemEvent(nil), list...)
	delete(q.queues, sessionKey)
	return out
}

func (q *SystemEventQueue) Peek(sessionKey string) []SystemEvent {
	if q == nil {
		return nil
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	list := q.queues[sessionKey]
	if len(list) == 0 {
		return nil
	}
	return append([]SystemEvent(nil), list...)
}

func (q *SystemEventQueue) Has(sessionKey string) bool {
	return len(q.Peek(sessionKey)) > 0
}
