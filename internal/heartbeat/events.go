package heartbeat

import (
	"sync"
	"time"
)

type IndicatorType string

const (
	IndicatorOK    IndicatorType = "ok"
	IndicatorAlert IndicatorType = "alert"
	IndicatorError IndicatorType = "error"
)

type Event struct {
	Timestamp     time.Time
	Status        string
	Reason        string
	To            string
	Preview       string
	Duration      time.Duration
	HasMedia      bool
	Channel       string
	AccountID     string
	Silent        bool
	IndicatorType IndicatorType
}

var (
	heartbeatEventsMu   sync.Mutex
	lastHeartbeatEvent  *Event
	heartbeatListeners  = map[int]func(Event){}
	nextHeartbeatListen int
)

func EmitEvent(evt Event) {
	heartbeatEventsMu.Lock()
	defer heartbeatEventsMu.Unlock()
	copyEvt := evt
	if copyEvt.Timestamp.IsZero() {
		copyEvt.Timestamp = time.Now().UTC()
	}
	lastHeartbeatEvent = &copyEvt
	for _, listener := range heartbeatListeners {
		listener(copyEvt)
	}
}

func LastEvent() *Event {
	heartbeatEventsMu.Lock()
	defer heartbeatEventsMu.Unlock()
	if lastHeartbeatEvent == nil {
		return nil
	}
	copyEvt := *lastHeartbeatEvent
	return &copyEvt
}

func OnEvent(listener func(Event)) func() {
	heartbeatEventsMu.Lock()
	defer heartbeatEventsMu.Unlock()
	id := nextHeartbeatListen
	nextHeartbeatListen++
	heartbeatListeners[id] = listener
	return func() {
		heartbeatEventsMu.Lock()
		defer heartbeatEventsMu.Unlock()
		delete(heartbeatListeners, id)
	}
}
