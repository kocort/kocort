package heartbeat

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestHeartbeatWakeBusPrefersHigherPriorityReason(t *testing.T) {
	t.Parallel()

	bus := NewHeartbeatWakeBus()
	defer bus.Stop()

	var (
		mu    sync.Mutex
		calls []HeartbeatWakeRequest
		done  = make(chan struct{}, 1)
	)
	bus.SetHandler(func(_ context.Context, req HeartbeatWakeRequest) HeartbeatRunResult {
		mu.Lock()
		calls = append(calls, req)
		mu.Unlock()
		select {
		case done <- struct{}{}:
		default:
		}
		return HeartbeatRunResult{Status: "ran"}
	})

	bus.Request(HeartbeatWakeRequest{AgentID: "main", SessionKey: "s1", Reason: "interval"}, 10*time.Millisecond)
	bus.Request(HeartbeatWakeRequest{AgentID: "main", SessionKey: "s1", Reason: "cron:test"}, 10*time.Millisecond)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for wake handler")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("expected one call, got %d", len(calls))
	}
	if calls[0].Reason != "cron:test" {
		t.Fatalf("expected higher-priority cron reason, got %q", calls[0].Reason)
	}
}

func TestHeartbeatWakeBusRetriesRequestsInFlight(t *testing.T) {
	t.Parallel()

	bus := NewHeartbeatWakeBus()
	defer bus.Stop()

	var (
		mu        sync.Mutex
		callCount int
		reasons   []string
		done      = make(chan struct{}, 1)
	)
	bus.SetHandler(func(_ context.Context, req HeartbeatWakeRequest) HeartbeatRunResult {
		mu.Lock()
		callCount++
		reasons = append(reasons, req.Reason)
		current := callCount
		mu.Unlock()
		if current == 1 {
			return HeartbeatRunResult{Status: "skipped", Reason: "requests-in-flight"}
		}
		select {
		case done <- struct{}{}:
		default:
		}
		return HeartbeatRunResult{Status: "ran"}
	})

	bus.Request(HeartbeatWakeRequest{AgentID: "main", SessionKey: "s1", Reason: "interval"}, 10*time.Millisecond)

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for retry wake handler")
	}

	mu.Lock()
	defer mu.Unlock()
	if callCount < 2 {
		t.Fatalf("expected retry to trigger a second call, got %d", callCount)
	}
	if reasons[0] != "interval" || reasons[1] != "interval" {
		t.Fatalf("expected retry to preserve wake reason, got %+v", reasons)
	}
}
