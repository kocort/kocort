package task

import (
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/session"

	"github.com/kocort/kocort/utils"
)

// SubagentLaunchPlan captures the prepared child run request plus the session
// entry fields that should be persisted before launch.
type SubagentLaunchPlan struct {
	ChildRequest            core.AgentRunRequest
	SessionEntry            core.SessionEntry
	AttachmentReceipt       *SubagentAttachmentReceipt
	AttachmentsDir          string
	AttachmentsRootDir      string
	RetainAttachmentsOnKeep bool
}

// PrepareSubagentLaunch builds the child run request and the session entry that
// should be written before starting the child run.
func PrepareSubagentLaunch(req SubagentSpawnRequest, spawnResult SubagentSpawnResult, targetIdentity *core.AgentIdentity, existingEntry *core.SessionEntry) (SubagentLaunchPlan, error) {
	childReq := core.AgentRunRequest{
		RunID:             spawnResult.RunID,
		Message:           req.Task,
		SessionKey:        spawnResult.ChildSessionKey,
		AgentID:           session.NormalizeAgentID(req.TargetAgentID),
		Thinking:          req.Thinking,
		Timeout:           time.Duration(req.RunTimeoutSeconds) * time.Second,
		Lane:              core.LaneSubagent,
		SpawnedBy:         req.RequesterSessionKey,
		SpawnDepth:        spawnResult.SpawnDepth,
		MaxSpawnDepth:     req.MaxSpawnDepth,
		WorkspaceOverride: spawnResult.WorkspaceDir,
		Deliver:           false,
	}
	if childReq.AgentID == "" {
		childReq.AgentID = session.NormalizeAgentID(req.RequesterAgentID)
	}
	childReq = session.ApplyRunRouteBinding(childReq, session.SessionRouteBinding{
		Channel:   req.RouteChannel,
		To:        req.RouteTo,
		AccountID: req.RouteAccountID,
		ThreadID:  req.RouteThreadID,
	})
	if targetIdentity != nil {
		childReq.WorkspaceOverride = infra.ResolveSpawnWorkspaceDir(infra.SpawnWorkspaceOptions{
			ExplicitWorkspace:  spawnResult.WorkspaceDir,
			RequesterWorkspace: req.WorkspaceDir,
			TargetWorkspace:    targetIdentity.WorkspaceDir,
		})
		if provider, model, ok := parseModelOverride(strings.TrimSpace(req.ModelOverride), targetIdentity.DefaultProvider); ok {
			childReq.SessionProviderOverride = provider
			childReq.SessionModelOverride = model
		} else if provider, model, ok := parseModelOverride(strings.TrimSpace(targetIdentity.SubagentModelPrimary), targetIdentity.DefaultProvider); ok {
			childReq.SessionProviderOverride = provider
			childReq.SessionModelOverride = model
		}
	} else {
		childReq.WorkspaceOverride = infra.ResolveSpawnWorkspaceDir(infra.SpawnWorkspaceOptions{
			ExplicitWorkspace:  spawnResult.WorkspaceDir,
			RequesterWorkspace: req.WorkspaceDir,
		})
	}

	entry := core.SessionEntry{}
	if existingEntry != nil {
		entry = *existingEntry
	}
	entry.SessionID = utils.NonEmpty(entry.SessionID, session.NewSessionID())
	entry.SpawnedBy = req.RequesterSessionKey
	entry.SpawnMode = strings.TrimSpace(req.SpawnMode)
	entry.SpawnDepth = spawnResult.SpawnDepth

	// Persist subagent role and control scope so that downstream operations
	// (kill / steer / send / spawn children) can gate on capabilities.
	caps := ResolveSubagentCapabilities(spawnResult.SpawnDepth, req.MaxSpawnDepth)
	entry.SubagentRole = string(caps.Role)
	entry.SubagentControlScope = string(caps.ControlScope)
	entry.ProviderOverride = utils.NonEmpty(strings.TrimSpace(childReq.SessionProviderOverride), entry.ProviderOverride)
	entry.ModelOverride = utils.NonEmpty(strings.TrimSpace(childReq.SessionModelOverride), entry.ModelOverride)
	entry = session.ApplySessionRouteBinding(entry, session.SessionRouteBinding{
		Channel:   req.RouteChannel,
		To:        req.RouteTo,
		AccountID: req.RouteAccountID,
		ThreadID:  req.RouteThreadID,
	})

	plan := SubagentLaunchPlan{
		ChildRequest: childReq,
		SessionEntry: entry,
	}
	materialized, err := MaterializeSubagentAttachmentsWithPolicy(childReq.WorkspaceOverride, req.Attachments, req.AttachMountPath, SubagentAttachmentPolicy{
		MaxFiles:            req.AttachmentMaxFiles,
		MaxFileBytes:        req.AttachmentMaxFileBytes,
		MaxTotalBytes:       req.AttachmentMaxTotalBytes,
		RetainOnSessionKeep: req.RetainAttachmentsOnKeep,
	})
	if err != nil {
		return SubagentLaunchPlan{}, err
	}
	if materialized != nil {
		plan.AttachmentReceipt = materialized.Receipt
		plan.AttachmentsDir = materialized.AbsDir
		plan.AttachmentsRootDir = materialized.RootDir
		plan.RetainAttachmentsOnKeep = materialized.RetainOnSessionKeep
		if suffix := strings.TrimSpace(materialized.SystemPromptSuffix); suffix != "" {
			plan.ChildRequest.ExtraSystemPrompt = strings.TrimSpace(strings.TrimSpace(plan.ChildRequest.ExtraSystemPrompt) + "\n\n" + suffix)
		}
	}
	return plan, nil
}

func parseModelOverride(raw string, defaultProvider string) (string, string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", false
	}
	if slash := strings.Index(trimmed, "/"); slash >= 0 {
		provider := strings.TrimSpace(trimmed[:slash])
		model := strings.TrimSpace(trimmed[slash+1:])
		if provider == "" || model == "" {
			return "", "", false
		}
		return strings.ToLower(provider), model, true
	}
	if strings.TrimSpace(defaultProvider) == "" {
		return "", "", false
	}
	return strings.ToLower(strings.TrimSpace(defaultProvider)), trimmed, true
}
