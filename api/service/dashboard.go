package service

// Dashboard snapshot service.

import (
	"context"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/runtime"
)

// BuildDashboardSnapshot aggregates a point-in-time health/status snapshot.
func BuildDashboardSnapshot(ctx context.Context, rt *runtime.Runtime) (core.DashboardSnapshot, error) {
	if rt == nil {
		return core.DashboardSnapshot{OccurredAt: time.Now().UTC()}, nil
	}
	snapshot := core.DashboardSnapshot{
		OccurredAt: time.Now().UTC(),
		Runtime: core.RuntimeHealthSnapshot{
			GatewayEnabled: rt.Config.Gateway.Enabled,
			WebchatEnabled: rt.Config.Gateway.Webchat != nil && (rt.Config.Gateway.Webchat.Enabled == nil || *rt.Config.Gateway.Webchat.Enabled),
			Components:     map[string]bool{},
		},
	}
	if rt.Sessions != nil {
		items := rt.Sessions.ListSessions()
		snapshot.Runtime.SessionCount = len(items)
		for _, item := range items {
			if strings.TrimSpace(item.ParentSessionKey) == "" {
				snapshot.Runtime.SessionRootCount++
			} else {
				snapshot.Runtime.SpawnedSessionCount++
			}
		}
		snapshot.Runtime.StateDir = strings.TrimSpace(rt.Sessions.BaseDir())
	}
	if rt.Subagents != nil {
		snapshot.Runtime.SubagentCount = rt.Subagents.Count()
	}
	snapshot.Runtime.ConfiguredAgent = config.ResolveDefaultConfiguredAgentID(rt.Config)
	snapshot.Runtime.Components["environment"] = rt.Environment != nil
	snapshot.Runtime.Components["sessions"] = rt.Sessions != nil
	snapshot.Runtime.Components["memory"] = rt.Memory != nil
	snapshot.Runtime.Components["backend"] = rt.Backend != nil || rt.Backends != nil
	snapshot.Runtime.Components["deliverer"] = rt.Deliverer != nil
	snapshot.Runtime.Components["tasks"] = rt.Tasks != nil
	snapshot.Runtime.Components["audit"] = rt.Audit != nil
	snapshot.Runtime.Healthy = rt.Sessions != nil && rt.Memory != nil && (rt.Backend != nil || rt.Backends != nil) && rt.Deliverer != nil

	if rt.ActiveRuns != nil {
		snapshot.ActiveRuns = rt.ActiveRuns.Snapshot()
	}
	if rt.Sessions != nil && strings.TrimSpace(rt.Sessions.BaseDir()) != "" {
		items, err := delivery.LoadQueuedDeliveries(rt.Sessions.BaseDir(), true)
		if err == nil {
			for _, item := range items {
				switch item.Status {
				case delivery.DeliveryStatusFailed, delivery.DeliveryStatusPartial:
					snapshot.DeliveryQueue.Failed++
				default:
					snapshot.DeliveryQueue.Pending++
				}
			}
		}
	}
	if rt.Tasks != nil {
		snapshot.Tasks = rt.Tasks.Summary()
		if rt.Config.Tasks.Enabled != nil {
			snapshot.Tasks.Enabled = *rt.Config.Tasks.Enabled
		}
	}
	snapshot.Providers = SummarizeProviders(ctx, rt)

	// Brain mode + local model status for sidebar status bar
	brainMode := strings.TrimSpace(rt.Config.BrainMode)
	if brainMode == "" {
		brainMode = "cloud"
	}
	snapshot.BrainMode = brainMode
	if rt.BrainLocal != nil {
		snapshot.BrainLocalStatus = rt.BrainLocal.Status()
	}
	if rt.Cerebellum != nil {
		snapshot.CerebellumStatus = rt.Cerebellum.Status()
	}

	return snapshot, nil
}
