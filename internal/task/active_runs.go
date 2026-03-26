package task

import (
	"strings"
	"sync"

	"github.com/kocort/kocort/internal/core"
)

// ActiveRunRegistry tracks in-flight agent runs by session key, with optional
// per-run cancellation support.
type ActiveRunRegistry struct {
	mu      sync.Mutex
	counts  map[string]int
	runMeta map[string]map[string]func()
}

// NewActiveRunRegistry creates a new empty ActiveRunRegistry.
func NewActiveRunRegistry() *ActiveRunRegistry {
	return &ActiveRunRegistry{
		counts:  map[string]int{},
		runMeta: map[string]map[string]func(){},
	}
}

func (r *ActiveRunRegistry) IsActive(sessionKey string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[sessionKey] > 0
}

func (r *ActiveRunRegistry) Count(sessionKey string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[sessionKey]
}

func (r *ActiveRunRegistry) TotalCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	total := 0
	for _, count := range r.counts {
		total += count
	}
	return total
}

func (r *ActiveRunRegistry) Start(sessionKey string) func() {
	return r.StartRun(sessionKey, "", nil)
}

func (r *ActiveRunRegistry) StartRun(sessionKey string, runID string, cancel func()) func() {
	r.mu.Lock()
	r.counts[sessionKey]++
	if strings.TrimSpace(runID) != "" {
		if r.runMeta[sessionKey] == nil {
			r.runMeta[sessionKey] = map[string]func(){}
		}
		r.runMeta[sessionKey][runID] = cancel
	}
	r.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if strings.TrimSpace(runID) != "" {
				if runs := r.runMeta[sessionKey]; runs != nil {
					delete(runs, runID)
					if len(runs) == 0 {
						delete(r.runMeta, sessionKey)
					}
				}
			}
			current := r.counts[sessionKey]
			if current <= 1 {
				delete(r.counts, sessionKey)
				return
			}
			r.counts[sessionKey] = current - 1
		})
	}
}

func (r *ActiveRunRegistry) CancelRun(sessionKey string, runID string) bool {
	r.mu.Lock()
	cancel := func() {}
	found := false
	if runs := r.runMeta[sessionKey]; runs != nil {
		if candidate, ok := runs[runID]; ok {
			if candidate != nil {
				cancel = candidate
			}
			found = true
		}
	}
	r.mu.Unlock()
	if found {
		cancel()
	}
	return found
}

func (r *ActiveRunRegistry) CancelSession(sessionKey string) []string {
	r.mu.Lock()
	cancels := map[string]func(){}
	if runs := r.runMeta[sessionKey]; runs != nil {
		for runID, cancel := range runs {
			cancels[runID] = cancel
		}
	}
	r.mu.Unlock()
	if len(cancels) == 0 {
		return nil
	}
	runIDs := make([]string, 0, len(cancels))
	for runID, cancel := range cancels {
		runIDs = append(runIDs, runID)
		if cancel != nil {
			cancel()
		}
	}
	return runIDs
}

func (r *ActiveRunRegistry) IsRunActive(sessionKey string, runID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if runs := r.runMeta[sessionKey]; runs != nil {
		_, ok := runs[runID]
		return ok
	}
	return false
}

func (r *ActiveRunRegistry) Snapshot() core.ActiveRunSummary {
	r.mu.Lock()
	defer r.mu.Unlock()
	summary := core.ActiveRunSummary{
		BySession: map[string]int{},
	}
	for sessionKey, count := range r.counts {
		summary.BySession[sessionKey] = count
		summary.Total += count
	}
	for _, runs := range r.runMeta {
		summary.CancelableRuns += len(runs)
	}
	return summary
}
