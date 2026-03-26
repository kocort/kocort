package acp

import (
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/session"

	"github.com/kocort/kocort/utils"
)

// SessionSpawnResult is the accepted-result payload returned by the ACP spawn
// path. It intentionally mirrors the subagent accepted payload shape where it
// makes sense, while surfacing ACP-specific metadata.
type SessionSpawnResult struct {
	Status          string
	ChildSessionKey string
	SessionID       string
	RunID           string
	Lane            core.Lane
	SpawnedBy       string
	WorkspaceDir    string
	Backend         string
	AgentID         string
	Mode            string
	Note            string
	StreamLogPath   string
}

// SessionLaunchPlan captures the prepared ACP child run plus the session entry
// that should be written before launch.
type SessionLaunchPlan struct {
	ChildRequest core.AgentRunRequest
	SessionEntry core.SessionEntry
	Result       SessionSpawnResult
}

func PrepareSessionLaunch(req SessionSpawnRequest, targetIdentity *core.AgentIdentity, existingEntry *core.SessionEntry) (SessionLaunchPlan, error) {
	targetAgentID := session.NormalizeAgentID(req.TargetAgentID)
	if targetAgentID == "" {
		targetAgentID = session.NormalizeAgentID(req.RequesterAgentID)
	}
	backendID := "acp"
	if targetIdentity != nil && strings.TrimSpace(targetIdentity.RuntimeBackend) != "" {
		backendID = strings.TrimSpace(targetIdentity.RuntimeBackend)
	}
	sessionRef, err := session.RandomToken(8)
	if err != nil {
		return SessionLaunchPlan{}, err
	}
	childSessionKey := session.BuildAcpSessionKey(targetAgentID, backendID, sessionRef)
	runID := session.NewRunID()
	targetWorkspace := ""
	if targetIdentity != nil {
		targetWorkspace = utils.NonEmpty(strings.TrimSpace(targetIdentity.RuntimeCwd), strings.TrimSpace(targetIdentity.WorkspaceDir))
	}
	workspaceDir := infra.ResolveSpawnWorkspaceDir(infra.SpawnWorkspaceOptions{
		ExplicitWorkspace:  req.WorkspaceDir,
		RequesterWorkspace: req.WorkspaceDir,
		TargetWorkspace:    targetWorkspace,
	})

	childReq := core.AgentRunRequest{
		RunID:             runID,
		Message:           req.Task,
		SessionKey:        childSessionKey,
		AgentID:           targetAgentID,
		Timeout:           time.Duration(req.RunTimeoutSeconds) * time.Second,
		Lane:              resolveAcpSpawnLane(req.SpawnMode),
		SpawnedBy:         req.RequesterSessionKey,
		WorkspaceOverride: workspaceDir,
		Deliver:           false,
	}
	if strings.TrimSpace(req.StreamTo) == "parent" {
		childReq.Deliver = true
	}
	childReq = session.ApplyRunRouteBinding(childReq, session.SessionRouteBinding{
		Channel:   req.RouteChannel,
		To:        req.RouteTo,
		AccountID: req.RouteAccountID,
		ThreadID:  req.RouteThreadID,
	})
	if targetIdentity != nil {
		defaultProvider := utils.NonEmpty(strings.TrimSpace(targetIdentity.DefaultProvider), strings.TrimSpace(targetIdentity.RuntimeBackend))
		if provider, model, ok := parseModelOverride(strings.TrimSpace(req.ModelOverride), defaultProvider); ok {
			childReq.SessionProviderOverride = provider
			childReq.SessionModelOverride = model
		}
	}

	entry := core.SessionEntry{}
	if existingEntry != nil {
		entry = *existingEntry
	}
	entry.SessionID = utils.NonEmpty(entry.SessionID, session.NewSessionID())
	entry.Label = utils.NonEmpty(strings.TrimSpace(req.Label), entry.Label)
	entry.SpawnedBy = req.RequesterSessionKey
	entry.SpawnMode = strings.TrimSpace(req.SpawnMode)
	entry.ProviderOverride = utils.NonEmpty(strings.TrimSpace(childReq.SessionProviderOverride), entry.ProviderOverride)
	entry.ModelOverride = utils.NonEmpty(strings.TrimSpace(childReq.SessionModelOverride), entry.ModelOverride)
	entry = session.ApplySessionRouteBinding(entry, session.SessionRouteBinding{
		Channel:   req.RouteChannel,
		To:        req.RouteTo,
		AccountID: req.RouteAccountID,
		ThreadID:  req.RouteThreadID,
	})

	meta := &core.AcpSessionMeta{
		Backend:        strings.TrimSpace(backendID),
		Agent:          targetAgentID,
		Cwd:            workspaceDir,
		State:          "idle",
		Mode:           resolveAcpSpawnSessionMode(req.SpawnMode),
		LastActivityAt: time.Now().UTC().UnixMilli(),
		RuntimeOptions: &core.AcpSessionRuntimeOptions{
			Model:          strings.TrimSpace(childReq.SessionModelOverride),
			Cwd:            workspaceDir,
			TimeoutSeconds: req.RunTimeoutSeconds,
		},
	}
	if targetIdentity != nil {
		meta.IdentityName = utils.NonEmpty(strings.TrimSpace(targetIdentity.Name), targetIdentity.ID)
		if meta.RuntimeOptions.Model == "" {
			meta.RuntimeOptions.Model = strings.TrimSpace(targetIdentity.DefaultModel)
		}
	}
	if existingEntry != nil && existingEntry.ACP != nil {
		copyMeta := *existingEntry.ACP
		if strings.TrimSpace(copyMeta.Backend) == "" {
			copyMeta.Backend = meta.Backend
		}
		if strings.TrimSpace(copyMeta.Agent) == "" {
			copyMeta.Agent = meta.Agent
		}
		if strings.TrimSpace(copyMeta.IdentityName) == "" {
			copyMeta.IdentityName = meta.IdentityName
		}
		if strings.TrimSpace(copyMeta.Cwd) == "" {
			copyMeta.Cwd = meta.Cwd
		}
		if copyMeta.Mode == "" {
			copyMeta.Mode = meta.Mode
		}
		if copyMeta.LastActivityAt == 0 {
			copyMeta.LastActivityAt = meta.LastActivityAt
		}
		if copyMeta.RuntimeOptions == nil {
			copyMeta.RuntimeOptions = meta.RuntimeOptions
		}
		meta = &copyMeta
	}
	entry.ACP = meta

	return SessionLaunchPlan{
		ChildRequest: childReq,
		SessionEntry: entry,
		Result: SessionSpawnResult{
			Status:          "accepted",
			ChildSessionKey: childSessionKey,
			SessionID:       entry.SessionID,
			RunID:           runID,
			Lane:            core.LaneNested,
			SpawnedBy:       req.RequesterSessionKey,
			WorkspaceDir:    workspaceDir,
			Backend:         backendID,
			AgentID:         targetAgentID,
			Mode:            normalizeSpawnMode(req.SpawnMode),
			Note:            resolveAcpAcceptedNote(req.SpawnMode),
			StreamLogPath:   resolveParentStreamLogPathForResult(req, childSessionKey),
		},
	}, nil
}

func resolveParentStreamLogPathForResult(req SessionSpawnRequest, childSessionKey string) string {
	if strings.TrimSpace(req.StreamTo) != "parent" {
		return ""
	}
	return ResolveParentStreamLogPath(childSessionKey)
}

func resolveAcpSpawnLane(spawnMode string) core.Lane {
	if normalizeSpawnMode(spawnMode) == "session" {
		return core.LaneDefault
	}
	return core.LaneNested
}

func resolveAcpSpawnSessionMode(spawnMode string) core.AcpRuntimeSessionMode {
	if normalizeSpawnMode(spawnMode) == "session" {
		return core.AcpSessionModePersistent
	}
	return core.AcpSessionModeOneShot
}

func normalizeSpawnMode(value string) string {
	if strings.TrimSpace(strings.ToLower(value)) == "session" {
		return "session"
	}
	return "run"
}

func resolveAcpAcceptedNote(spawnMode string) string {
	if normalizeSpawnMode(spawnMode) == "session" {
		return "Persistent ACP child session created; later follow-up on the same bound thread or by session key should reuse this ACP session."
	}
	return "ACP child session started in one-shot mode; use sessions_history or sessions_send with the returned session key for follow-up inspection."
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
