package acp

import (
	"strings"

	spawnpolicypkg "github.com/kocort/kocort/internal/spawnpolicy"
)

// SessionSpawnRequest describes a sessions_spawn request targeting the ACP
// runtime family.
type SessionSpawnRequest struct {
	RequesterSessionKey string
	RequesterAgentID    string
	TargetAgentID       string
	Task                string
	Label               string
	ModelOverride       string
	RunTimeoutSeconds   int
	WorkspaceDir        string
	SpawnMode           string
	ThreadRequested     bool
	SandboxMode         string
	StreamTo            string
	RouteChannel        string
	RouteTo             string
	RouteAccountID      string
	RouteThreadID       string
}

// SessionSpawnDefaults carries the defaults used to normalize an ACP spawn
// request from the sessions_spawn tool surface.
type SessionSpawnDefaults struct {
	RequesterSessionKey string
	RequesterAgentID    string
	WorkspaceDir        string
	RouteChannel        string
	RouteTo             string
	RouteAccountID      string
	RouteThreadID       string
	DefaultTimeoutSec   int
}

func NormalizeSessionSpawnRequest(req SessionSpawnRequest, defaults SessionSpawnDefaults) SessionSpawnRequest {
	req.RequesterSessionKey = strings.TrimSpace(nonEmpty(req.RequesterSessionKey, defaults.RequesterSessionKey))
	req.RequesterAgentID = strings.TrimSpace(nonEmpty(req.RequesterAgentID, defaults.RequesterAgentID))
	req.WorkspaceDir = strings.TrimSpace(nonEmpty(req.WorkspaceDir, defaults.WorkspaceDir))
	req.RouteChannel = strings.TrimSpace(nonEmpty(req.RouteChannel, defaults.RouteChannel))
	req.RouteTo = strings.TrimSpace(nonEmpty(req.RouteTo, defaults.RouteTo))
	req.RouteAccountID = strings.TrimSpace(nonEmpty(req.RouteAccountID, defaults.RouteAccountID))
	req.RouteThreadID = strings.TrimSpace(nonEmpty(req.RouteThreadID, defaults.RouteThreadID))
	if req.RunTimeoutSeconds <= 0 {
		req.RunTimeoutSeconds = defaults.DefaultTimeoutSec
	}
	req.SandboxMode = spawnpolicypkg.NormalizeSpawnSandboxMode(req.SandboxMode)
	req.StreamTo = strings.TrimSpace(strings.ToLower(req.StreamTo))
	if strings.TrimSpace(req.SpawnMode) == "" {
		if req.ThreadRequested {
			req.SpawnMode = "session"
		} else {
			req.SpawnMode = "run"
		}
	}
	return req
}

func ValidateSessionSpawnRequest(req SessionSpawnRequest) error {
	mode := strings.TrimSpace(strings.ToLower(req.SpawnMode))
	if mode == "session" && !req.ThreadRequested {
		return ToolFacingSpawnError(`mode="session" requires thread=true`)
	}
	if req.ThreadRequested && strings.TrimSpace(req.RouteThreadID) == "" {
		return ToolFacingSpawnError(`thread=true requires a current thread context`)
	}
	if req.ThreadRequested && mode != "session" {
		return ToolFacingSpawnError(`thread=true currently requires mode="session"`)
	}
	return nil
}

type ToolFacingSpawnError string

func (e ToolFacingSpawnError) Error() string { return string(e) }

func nonEmpty(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
