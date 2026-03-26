package task

import (
	"strings"

	spawnpolicypkg "github.com/kocort/kocort/internal/spawnpolicy"
)

// SubagentSpawnDefaults carries the identity/runtime defaults that should be
// applied when a sessions_spawn tool call omits optional fields.
type SubagentSpawnDefaults struct {
	RequesterSessionKey     string
	RequesterAgentID        string
	WorkspaceDir            string
	RouteChannel            string
	RouteTo                 string
	RouteAccountID          string
	RouteThreadID           string
	DefaultThinking         string
	DefaultTimeoutSec       int
	MaxSpawnDepth           int
	MaxChildren             int
	CurrentDepth            int
	ArchiveAfterMinutes     int
	AttachmentMaxFiles      int
	AttachmentMaxFileBytes  int
	AttachmentMaxTotalBytes int
	RetainAttachmentsOnKeep bool
}

// NormalizeSubagentSpawnRequest fills defaults for the MVP subagent spawn path
// without introducing runtime-specific orchestration logic into the tool layer.
func NormalizeSubagentSpawnRequest(req SubagentSpawnRequest, defaults SubagentSpawnDefaults) SubagentSpawnRequest {
	req.RequesterSessionKey = strings.TrimSpace(nonEmpty(req.RequesterSessionKey, defaults.RequesterSessionKey))
	req.RequesterAgentID = strings.TrimSpace(nonEmpty(req.RequesterAgentID, defaults.RequesterAgentID))
	req.RequesterDisplayKey = strings.TrimSpace(nonEmpty(req.RequesterDisplayKey, req.RequesterSessionKey))
	req.WorkspaceDir = strings.TrimSpace(nonEmpty(req.WorkspaceDir, defaults.WorkspaceDir))
	req.RouteChannel = strings.TrimSpace(nonEmpty(req.RouteChannel, defaults.RouteChannel))
	req.RouteTo = strings.TrimSpace(nonEmpty(req.RouteTo, defaults.RouteTo))
	req.RouteAccountID = strings.TrimSpace(nonEmpty(req.RouteAccountID, defaults.RouteAccountID))
	req.RouteThreadID = strings.TrimSpace(nonEmpty(req.RouteThreadID, defaults.RouteThreadID))
	req.Thinking = strings.TrimSpace(nonEmpty(req.Thinking, defaults.DefaultThinking))
	if req.RunTimeoutSeconds <= 0 {
		req.RunTimeoutSeconds = defaults.DefaultTimeoutSec
	}
	if req.MaxSpawnDepth <= 0 {
		req.MaxSpawnDepth = defaults.MaxSpawnDepth
	}
	if req.MaxChildren <= 0 {
		req.MaxChildren = defaults.MaxChildren
	}
	if req.CurrentDepth <= 0 {
		req.CurrentDepth = defaults.CurrentDepth
	}
	if strings.TrimSpace(req.Cleanup) == "" {
		req.Cleanup = "keep"
	}
	if !req.ExpectsCompletionMessageSet {
		req.ExpectsCompletionMessage = true
	}
	req.SandboxMode = spawnpolicypkg.NormalizeSpawnSandboxMode(req.SandboxMode)
	if strings.TrimSpace(req.SpawnMode) == "" {
		if req.ThreadRequested {
			req.SpawnMode = "session"
		} else {
			req.SpawnMode = "run"
		}
	}
	if req.ArchiveAfterMinutes <= 0 {
		req.ArchiveAfterMinutes = defaults.ArchiveAfterMinutes
	}
	if req.AttachmentMaxFiles <= 0 {
		req.AttachmentMaxFiles = defaults.AttachmentMaxFiles
	}
	if req.AttachmentMaxFileBytes <= 0 {
		req.AttachmentMaxFileBytes = defaults.AttachmentMaxFileBytes
	}
	if req.AttachmentMaxTotalBytes <= 0 {
		req.AttachmentMaxTotalBytes = defaults.AttachmentMaxTotalBytes
	}
	if !req.RetainAttachmentsOnKeep {
		req.RetainAttachmentsOnKeep = defaults.RetainAttachmentsOnKeep
	}
	return req
}

// ValidateSubagentSpawnRequest enforces the current MVP semantics for
// sessions_spawn. Parsing support can land before full runtime support, but the
// rules should live in internal/task rather than in the tool layer.
func ValidateSubagentSpawnRequest(req SubagentSpawnRequest) error {
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
