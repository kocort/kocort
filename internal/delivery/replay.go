// replay.go — standalone delivery-replay functions extracted from runtime.
//
// These functions were formerly methods on *runtime.Runtime. They operate
// on the delivery subsystem only and need no runtime fields.
package delivery

import (
	"context"
	"time"

	"github.com/kocort/kocort/internal/core"
)

// ReplayResult summarises a single replay-queue pass.
type ReplayResult struct {
	Replayed int
	Failed   int
	Acked    int
}

// ReplayQueue replays due queued deliveries up to the given limit.
// It requires only a base directory (from SessionStore.BaseDir()) and
// a Deliverer that can be type-asserted to *RouterDeliverer.
func ReplayQueue(ctx context.Context, baseDir string, deliverer core.Deliverer, limit int) (ReplayResult, error) {
	if baseDir == "" || deliverer == nil {
		return ReplayResult{}, nil
	}
	router, ok := deliverer.(*RouterDeliverer)
	if !ok {
		return ReplayResult{}, nil
	}
	items, err := DueQueuedDeliveries(baseDir, time.Now().UTC(), limit)
	if err != nil {
		return ReplayResult{}, err
	}
	var result ReplayResult
	for _, item := range items {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		err := router.ReplayQueued(ctx, item)
		if err != nil {
			result.Failed++
			continue
		}
		result.Replayed++
		result.Acked++
	}
	return result, nil
}

// StartReplayWorker runs a background goroutine that periodically calls
// ReplayQueue. It blocks until ctx is cancelled.
func StartReplayWorker(ctx context.Context, baseDir string, deliverer core.Deliverer, interval time.Duration, limit int) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = ReplayQueue(ctx, baseDir, deliverer, limit) // best-effort; failure is non-critical
			}
		}
	}()
}
