package heartbeat

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"
)

// HeartbeatRuntime abstracts the runtime methods needed by HeartbeatRunner,
// allowing the runner to live outside the runtime package.
type HeartbeatRuntime interface {
	RunHeartbeatTurn(ctx context.Context, req HeartbeatWakeRequest) (HeartbeatRunResult, error)
	IdentitySnapshot() []core.AgentIdentity
	// RunDiskBudgetSweep enforces the session disk budget.  Implementations
	// that do not support disk budget enforcement may no-op.
	RunDiskBudgetSweep()
}

type HeartbeatRunner struct {
	runtime       HeartbeatRuntime
	wake          *HeartbeatWakeBus
	mu            sync.Mutex
	ticker        *time.Ticker
	stop          chan struct{}
	lastRun       map[string]time.Time
	lastBudget    time.Time // last disk-budget sweep time
}

func NewHeartbeatRunner(rt HeartbeatRuntime) *HeartbeatRunner {
	r := &HeartbeatRunner{
		runtime: rt,
		wake:    NewHeartbeatWakeBus(),
		stop:    make(chan struct{}),
		lastRun: map[string]time.Time{},
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
	if r.ticker != nil {
		r.mu.Unlock()
		return
	}
	r.ticker = time.NewTicker(time.Minute)
	r.mu.Unlock()
	go func() {
		for {
			select {
			case <-r.stop:
				return
			case <-r.ticker.C:
				r.RunIntervals()
			}
		}
	}()
}

func (r *HeartbeatRunner) Stop() {
	if r == nil {
		return
	}
	r.wake.Stop()
	r.mu.Lock()
	if r.ticker != nil {
		r.ticker.Stop()
		r.ticker = nil
	}
	select {
	case <-r.stop:
	default:
		close(r.stop)
	}
	r.mu.Unlock()
}

func (r *HeartbeatRunner) RequestNow(req HeartbeatWakeRequest) {
	if r == nil {
		return
	}
	r.wake.Request(req, 250*time.Millisecond)
}

func (r *HeartbeatRunner) RunIntervals() {
	if r == nil || r.runtime == nil {
		return
	}
	now := time.Now().UTC()
	for _, identity := range r.runtime.IdentitySnapshot() {
		every := strings.TrimSpace(identity.HeartbeatEvery)
		if every == "" {
			continue
		}
		d, err := time.ParseDuration(every)
		if err != nil || d <= 0 {
			continue
		}
		r.mu.Lock()
		last := r.lastRun[identity.ID]
		if !last.IsZero() && now.Sub(last) < d {
			r.mu.Unlock()
			continue
		}
		r.mu.Unlock()
		r.RequestNow(HeartbeatWakeRequest{Reason: "interval", AgentID: identity.ID})
	}

	// Disk-budget sweep — throttled to once every 10 minutes.
	const budgetInterval = 10 * time.Minute
	r.mu.Lock()
	runBudget := r.lastBudget.IsZero() || now.Sub(r.lastBudget) >= budgetInterval
	if runBudget {
		r.lastBudget = now
	}
	r.mu.Unlock()
	if runBudget {
		r.runtime.RunDiskBudgetSweep()
	}
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
		r.lastRun[session.NormalizeAgentID(req.AgentID)] = time.Now().UTC()
		r.mu.Unlock()
	}
	result.Duration = duration
	return result
}
