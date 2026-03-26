package task

import (
	"strings"
	"time"

	"github.com/kocort/kocort/internal/acp"
	"github.com/kocort/kocort/internal/core"
)

// BuildACPChildRunRecord converts an ACP spawn launch into the unified
// spawned-run registry record used by subagents tooling and lifecycle
// recovery.
func BuildACPChildRunRecord(req acp.SessionSpawnRequest, launch acp.SessionLaunchPlan) SubagentRunRecord {
	model := strings.TrimSpace(launch.ChildRequest.SessionModelOverride)
	if model == "" && launch.SessionEntry.ACP != nil && launch.SessionEntry.ACP.RuntimeOptions != nil {
		model = strings.TrimSpace(launch.SessionEntry.ACP.RuntimeOptions.Model)
	}
	now := time.Now().UTC()
	record := SubagentRunRecord{
		RunID:                    launch.Result.RunID,
		ChildKind:                "acp",
		ChildSessionKey:          launch.Result.ChildSessionKey,
		RequesterSessionKey:      req.RequesterSessionKey,
		RequesterDisplayKey:      strings.TrimSpace(req.RequesterSessionKey),
		RequesterOrigin:          cloneACPDeliveryContext(&core.DeliveryContext{Channel: req.RouteChannel, To: req.RouteTo, AccountID: req.RouteAccountID, ThreadID: req.RouteThreadID}),
		Task:                     req.Task,
		Label:                    strings.TrimSpace(req.Label),
		Model:                    model,
		RuntimeBackend:           resolveACPChildRuntimeBackend(launch),
		RuntimeState:             resolveACPChildRuntimeState(launch),
		RuntimeMode:              resolveACPChildRuntimeMode(launch),
		RuntimeSessionName:       resolveACPChildRuntimeSessionName(launch),
		RuntimeStatusSummary:     resolveACPChildRuntimeStatusSummary(launch),
		RuntimeBackendSessionID:  resolveACPChildBackendSessionID(launch),
		RuntimeAgentSessionID:    resolveACPChildAgentSessionID(launch),
		Cleanup:                  "keep",
		SpawnMode:                strings.TrimSpace(req.SpawnMode),
		RouteChannel:             req.RouteChannel,
		RouteThreadID:            req.RouteThreadID,
		WorkspaceDir:             launch.Result.WorkspaceDir,
		RunTimeoutSeconds:        req.RunTimeoutSeconds,
		ExpectsCompletionMessage: strings.TrimSpace(req.StreamTo) == "parent",
		CreatedAt:                now,
		StartedAt:                now,
	}
	if record.SpawnMode != "session" {
		record.ArchiveAt = now.Add(60 * time.Minute)
	}
	return record
}

func resolveACPChildRuntimeBackend(launch acp.SessionLaunchPlan) string {
	if launch.SessionEntry.ACP == nil {
		return ""
	}
	return strings.TrimSpace(launch.SessionEntry.ACP.Backend)
}

func resolveACPChildRuntimeState(launch acp.SessionLaunchPlan) string {
	if launch.SessionEntry.ACP == nil {
		return ""
	}
	return strings.TrimSpace(launch.SessionEntry.ACP.State)
}

func resolveACPChildRuntimeMode(launch acp.SessionLaunchPlan) string {
	if launch.SessionEntry.ACP == nil {
		return ""
	}
	return strings.TrimSpace(string(launch.SessionEntry.ACP.Mode))
}

func resolveACPChildRuntimeSessionName(launch acp.SessionLaunchPlan) string {
	if launch.SessionEntry.ACP == nil {
		return ""
	}
	return strings.TrimSpace(launch.SessionEntry.ACP.RuntimeSessionName)
}

func resolveACPChildRuntimeStatusSummary(launch acp.SessionLaunchPlan) string {
	if launch.SessionEntry.ACP == nil || launch.SessionEntry.ACP.RuntimeStatus == nil {
		return ""
	}
	return strings.TrimSpace(launch.SessionEntry.ACP.RuntimeStatus.Summary)
}

func resolveACPChildBackendSessionID(launch acp.SessionLaunchPlan) string {
	if launch.SessionEntry.ACP == nil {
		return ""
	}
	return strings.TrimSpace(launch.SessionEntry.ACP.BackendSessionID)
}

func resolveACPChildAgentSessionID(launch acp.SessionLaunchPlan) string {
	if launch.SessionEntry.ACP == nil {
		return ""
	}
	return strings.TrimSpace(launch.SessionEntry.ACP.AgentSessionID)
}

func cloneACPDeliveryContext(value *core.DeliveryContext) *core.DeliveryContext {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
