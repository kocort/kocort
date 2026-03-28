package heartbeat

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"
)

type HeartbeatRuntime interface {
	RunHeartbeatTurn(ctx context.Context, req HeartbeatWakeRequest) (HeartbeatRunResult, error)
	IdentitySnapshot() []core.AgentIdentity
	RunDiskBudgetSweep()
}

type heartbeatAgentState struct {
	Identity  core.AgentIdentity
	Interval  time.Duration
	LastRunAt time.Time
	NextDueAt time.Time
}

type HeartbeatRunner struct {
	runtime    HeartbeatRuntime
	wake       *HeartbeatWakeBus
	mu         sync.Mutex
	stop       chan struct{}
	stopped    bool
	timer      *time.Timer
	agents     map[string]heartbeatAgentState
	lastBudget time.Time
}

func NewHeartbeatRunner(rt HeartbeatRuntime) *HeartbeatRunner {
	r := &HeartbeatRunner{
		runtime: rt,
		wake:    NewHeartbeatWakeBus(),
		stop:    make(chan struct{}),
		agents:  map[string]heartbeatAgentState{},
	}
	r.wake.SetHandler(func(ctx context.Context, req HeartbeatWakeRequest) HeartbeatRunResult {
		return r.RunOnce(ctx, req)
	})
	return r
}

func (r *HeartbeatRunner) Start() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		r.stop = make(chan struct{})
		r.stopped = false
	}
	r.refreshLocked(time.Now().UTC())
	r.scheduleNextLocked()
}

func (r *HeartbeatRunner) Stop() {
	if r == nil {
		return
	}
	r.wake.Stop()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	if !r.stopped {
		close(r.stop)
		r.stopped = true
	}
}

func (r *HeartbeatRunner) RequestNow(req HeartbeatWakeRequest) {
	if r == nil {
		return
	}
	r.wake.Request(req, 250*time.Millisecond)
}

func (r *HeartbeatRunner) RunIntervals() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.refreshLocked(time.Now().UTC())
	r.scheduleNextLocked()
	r.mu.Unlock()
}

func (r *HeartbeatRunner) RunOnce(ctx context.Context, req HeartbeatWakeRequest) HeartbeatRunResult {
	if r == nil || r.runtime == nil {
		return HeartbeatRunResult{Status: "skipped", Reason: "runtime-missing"}
	}
	started := time.Now().UTC()
	result, err := r.runtime.RunHeartbeatTurn(ctx, req)
	duration := time.Since(started)
	if err != nil {
		return HeartbeatRunResult{Status: "failed", Reason: err.Error(), Duration: duration}
	}
	if result.Status == "ran" && strings.TrimSpace(req.AgentID) != "" {
		r.mu.Lock()
		agentID := session.NormalizeAgentID(req.AgentID)
		state, ok := r.agents[agentID]
		if ok {
			state.LastRunAt = started
			state.NextDueAt = started.Add(state.Interval)
			r.agents[agentID] = state
			r.scheduleNextLocked()
		}
		r.mu.Unlock()
	}
	result.Duration = duration
	return result
}

func (r *HeartbeatRunner) refreshLocked(now time.Time) {
	if r.runtime == nil {
		return
	}
	previous := r.agents
	next := make(map[string]heartbeatAgentState)
	for _, identity := range r.runtime.IdentitySnapshot() {
		agentID := session.NormalizeAgentID(identity.ID)
		if agentID == "" {
			continue
		}
		every := strings.TrimSpace(identity.HeartbeatEvery)
		if every == "" {
			continue
		}
		interval, err := time.ParseDuration(every)
		if err != nil || interval <= 0 {
			continue
		}
		state := heartbeatAgentState{
			Identity: identity,
			Interval: interval,
		}
		if prev, ok := previous[agentID]; ok {
			state.LastRunAt = prev.LastRunAt
			if prev.Interval == interval && prev.NextDueAt.After(now) {
				state.NextDueAt = prev.NextDueAt
			} else if !prev.LastRunAt.IsZero() {
				state.NextDueAt = prev.LastRunAt.Add(interval)
			}
		}
		if state.NextDueAt.IsZero() || !state.NextDueAt.After(now) {
			state.NextDueAt = now.Add(interval)
		}
		next[agentID] = state
	}
	r.agents = next

	const budgetInterval = 10 * time.Minute
	if r.lastBudget.IsZero() || now.Sub(r.lastBudget) >= budgetInterval {
		r.lastBudget = now
		go r.runtime.RunDiskBudgetSweep()
	}
}

func (r *HeartbeatRunner) scheduleNextLocked() {
	if r.stopped {
		return
	}
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	if len(r.agents) == 0 {
		return
	}
	var nextDue time.Time
	for _, state := range r.agents {
		if nextDue.IsZero() || state.NextDueAt.Before(nextDue) {
			nextDue = state.NextDueAt
		}
	}
	if nextDue.IsZero() {
		return
	}
	delay := time.Until(nextDue)
	if delay < 0 {
		delay = 0
	}
	r.timer = time.AfterFunc(delay, func() {
		r.tick()
	})
}

func (r *HeartbeatRunner) tick() {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	now := time.Now().UTC()
	r.refreshLocked(now)
	due := make([]heartbeatAgentState, 0, len(r.agents))
	for id, state := range r.agents {
		if !state.NextDueAt.After(now) {
			state.LastRunAt = now
			state.NextDueAt = now.Add(state.Interval)
			r.agents[id] = state
			due = append(due, state)
		}
	}
	r.scheduleNextLocked()
	r.mu.Unlock()

	for _, state := range due {
		r.RequestNow(HeartbeatWakeRequest{
			Reason:  "interval",
			AgentID: state.Identity.ID,
		})
	}
}
