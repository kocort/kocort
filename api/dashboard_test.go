package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kocort/kocort/api/service"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/task"
	"github.com/kocort/kocort/runtime"
	"github.com/kocort/kocort/utils"
)

func TestDashboardSnapshotSummarizesState(t *testing.T) {
	stateDir := t.TempDir()
	cfg := config.AppConfig{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"ready": {
					BaseURL: "https://example.invalid/v1",
					APIKey:  "ready-key",
					API:     "openai-completions",
					Models:  []config.ProviderModelConfig{{ID: "demo", Name: "Demo"}},
				},
				"broken": {
					API:    "openai-completions",
					Models: []config.ProviderModelConfig{{ID: "bad", Name: "Bad"}},
				},
			},
		},
		Gateway: config.GatewayConfig{
			Enabled: true,
			Webchat: &config.GatewayWebchatConfig{Enabled: utils.BoolPtr(true)},
		},
		Tasks: config.TasksConfig{Enabled: utils.BoolPtr(true), MaxConcurrent: 3},
	}
	rt, err := runtime.NewRuntimeFromConfig(cfg, config.RuntimeConfigParams{
		StateDir:  stateDir,
		AgentID:   "main",
		Provider:  "ready",
		Model:     "demo",
		Deliverer: &delivery.MemoryDeliverer{},
	})
	if err != nil {
		t.Fatalf("NewRuntimeFromConfig: %v", err)
	}
	srv := NewServer(rt, config.GatewayConfig{})

	sessionKey := session.BuildMainSessionKey("main")
	if err := rt.Sessions.Upsert(sessionKey, core.SessionEntry{SessionID: "sess-dashboard", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	stop := rt.ActiveRuns.StartRun(sessionKey, "run-dashboard", func() {})
	defer stop()

	scheduled, err := rt.ScheduleTask(context.Background(), task.TaskScheduleRequest{AgentID: "main", Title: "dash", Message: "hello"})
	if err != nil {
		t.Fatalf("ScheduleTask: %v", err)
	}
	if err := rt.Tasks.MarkQueued(scheduled.ID); err != nil {
		t.Fatalf("MarkQueued: %v", err)
	}

	entry, err := delivery.EnqueueDelivery(stateDir, core.ReplyKindFinal, core.ReplyPayload{Text: "hello"}, core.DeliveryTarget{SessionKey: sessionKey, Channel: "mock", To: "user"})
	if err != nil {
		t.Fatalf("enqueueDelivery: %v", err)
	}
	if err := delivery.FailDelivery(stateDir, entry, "boom", false); err != nil {
		t.Fatalf("failDelivery: %v", err)
	}

	snapshot, err := service.BuildDashboardSnapshot(context.Background(), srv.Runtime)
	if err != nil {
		t.Fatalf("buildDashboardSnapshot: %v", err)
	}
	if !snapshot.Runtime.Healthy {
		t.Fatalf("expected runtime healthy, got %+v", snapshot.Runtime)
	}
	if snapshot.Runtime.SessionCount != 1 || snapshot.Runtime.SessionRootCount != 1 || snapshot.Runtime.SpawnedSessionCount != 0 {
		t.Fatalf("unexpected session counters: %+v", snapshot.Runtime)
	}
	if snapshot.ActiveRuns.Total < 1 || snapshot.ActiveRuns.BySession[sessionKey] != 1 {
		t.Fatalf("unexpected active runs: %+v", snapshot.ActiveRuns)
	}
	if snapshot.DeliveryQueue.Failed != 1 {
		t.Fatalf("expected 1 failed delivery, got %+v", snapshot.DeliveryQueue)
	}
	if snapshot.Tasks.Total == 0 || snapshot.Tasks.ByStatus[string(core.TaskStatusQueued)] == 0 {
		t.Fatalf("unexpected task summary: %+v", snapshot.Tasks)
	}
	if len(snapshot.Providers) != 2 {
		t.Fatalf("expected 2 provider summaries, got %+v", snapshot.Providers)
	}
	var ready, broken *core.ProviderHealthSummary
	for i := range snapshot.Providers {
		switch snapshot.Providers[i].Provider {
		case "ready":
			ready = &snapshot.Providers[i]
		case "broken":
			broken = &snapshot.Providers[i]
		}
	}
	if ready == nil || !ready.Ready {
		t.Fatalf("expected ready provider to be ready, got %+v", snapshot.Providers)
	}
	if broken == nil || broken.Ready || !strings.Contains(broken.LastError, "not fully configured") {
		t.Fatalf("expected broken provider summary, got %+v", snapshot.Providers)
	}
}
