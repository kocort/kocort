package task

import (
	"strings"

	"github.com/kocort/kocort/internal/acp"
)

// SpawnRuntime identifies which runtime family should handle a sessions_spawn
// request.
type SpawnRuntime string

const (
	SpawnRuntimeSubagent SpawnRuntime = "subagent"
	SpawnRuntimeACP      SpawnRuntime = "acp"
)

// SessionsSpawnToolInput is the normalized, tool-facing input shape before
// runtime-specific dispatch.
type SessionsSpawnToolInput struct {
	Task                        string
	Label                       string
	Runtime                     string
	AgentID                     string
	ModelOverride               string
	ExpectsCompletionMessage    bool
	ExpectsCompletionMessageSet bool
	Thinking                    string
	RunTimeoutSeconds           int
	ThreadRequested             bool
	Mode                        string
	Cleanup                     string
	SandboxMode                 string
	StreamTo                    string
	Attachments                 []SubagentInlineAttachment
	AttachMountPath             string
}

// SessionsSpawnPlan is the domain-level result of parsing a sessions_spawn
// tool call. It centralizes runtime selection and subagent request defaults so
// the tool layer can remain a thin façade.
type SessionsSpawnPlan struct {
	Runtime       SpawnRuntime
	SubagentSpawn SubagentSpawnRequest
	ACPSpawn      acp.SessionSpawnRequest
}

// BuildSessionsSpawnPlan resolves the runtime selection and, for the current
// subagent MVP path, builds the normalized SubagentSpawnRequest.
func BuildSessionsSpawnPlan(input SessionsSpawnToolInput, defaults SubagentSpawnDefaults) SessionsSpawnPlan {
	runtimeName := normalizeSpawnRuntime(input.Runtime)
	acpDefaults := acp.SessionSpawnDefaults{
		RequesterSessionKey: defaults.RequesterSessionKey,
		RequesterAgentID:    defaults.RequesterAgentID,
		WorkspaceDir:        defaults.WorkspaceDir,
		RouteChannel:        defaults.RouteChannel,
		RouteTo:             defaults.RouteTo,
		RouteAccountID:      defaults.RouteAccountID,
		RouteThreadID:       defaults.RouteThreadID,
		DefaultTimeoutSec:   defaults.DefaultTimeoutSec,
	}
	return SessionsSpawnPlan{
		Runtime: runtimeName,
		SubagentSpawn: NormalizeSubagentSpawnRequest(SubagentSpawnRequest{
			RequesterSessionKey:         defaults.RequesterSessionKey,
			RequesterAgentID:            defaults.RequesterAgentID,
			RequesterDisplayKey:         defaults.RequesterSessionKey,
			TargetAgentID:               input.AgentID,
			Task:                        input.Task,
			Label:                       input.Label,
			ModelOverride:               input.ModelOverride,
			ExpectsCompletionMessage:    input.ExpectsCompletionMessage,
			ExpectsCompletionMessageSet: input.ExpectsCompletionMessageSet,
			Thinking:                    input.Thinking,
			RunTimeoutSeconds:           input.RunTimeoutSeconds,
			WorkspaceDir:                defaults.WorkspaceDir,
			Cleanup:                     input.Cleanup,
			SpawnMode:                   input.Mode,
			ThreadRequested:             input.ThreadRequested,
			SandboxMode:                 input.SandboxMode,
			RouteChannel:                defaults.RouteChannel,
			RouteTo:                     defaults.RouteTo,
			RouteAccountID:              defaults.RouteAccountID,
			RouteThreadID:               defaults.RouteThreadID,
			Attachments:                 append([]SubagentInlineAttachment{}, input.Attachments...),
			AttachMountPath:             input.AttachMountPath,
			AttachmentMaxFiles:          defaults.AttachmentMaxFiles,
			AttachmentMaxFileBytes:      defaults.AttachmentMaxFileBytes,
			AttachmentMaxTotalBytes:     defaults.AttachmentMaxTotalBytes,
			RetainAttachmentsOnKeep:     defaults.RetainAttachmentsOnKeep,
		}, defaults),
		ACPSpawn: acp.NormalizeSessionSpawnRequest(acp.SessionSpawnRequest{
			RequesterSessionKey: defaults.RequesterSessionKey,
			RequesterAgentID:    defaults.RequesterAgentID,
			TargetAgentID:       input.AgentID,
			Task:                input.Task,
			Label:               input.Label,
			ModelOverride:       input.ModelOverride,
			RunTimeoutSeconds:   input.RunTimeoutSeconds,
			WorkspaceDir:        defaults.WorkspaceDir,
			SpawnMode:           input.Mode,
			ThreadRequested:     input.ThreadRequested,
			SandboxMode:         input.SandboxMode,
			StreamTo:            input.StreamTo,
			RouteChannel:        defaults.RouteChannel,
			RouteTo:             defaults.RouteTo,
			RouteAccountID:      defaults.RouteAccountID,
			RouteThreadID:       defaults.RouteThreadID,
		}, acpDefaults),
	}
}

func normalizeSpawnRuntime(value string) SpawnRuntime {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "acp":
		return SpawnRuntimeACP
	default:
		return SpawnRuntimeSubagent
	}
}
