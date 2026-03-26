package tool

import (
	"context"
	"strings"

	"github.com/kocort/kocort/internal/core"
	taskpkg "github.com/kocort/kocort/internal/task"
)

var unsupportedSessionsSpawnParamKeys = []string{
	"target",
	"transport",
	"channel",
	"to",
	"threadId",
	"thread_id",
	"replyTo",
	"reply_to",
}

type SessionsSpawnTool struct{}

func NewSessionsSpawnTool() *SessionsSpawnTool {
	return &SessionsSpawnTool{}
}

func (t *SessionsSpawnTool) Name() string {
	return "sessions_spawn"
}

func (t *SessionsSpawnTool) Description() string {
	return "Spawn an isolated sub-agent session."
}

func (t *SessionsSpawnTool) OpenAIFunctionTool() *core.OpenAIFunctionToolSchema {
	return &core.OpenAIFunctionToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task":                     map[string]any{"type": "string"},
				"label":                    map[string]any{"type": "string"},
				"agentId":                  map[string]any{"type": "string"},
				"thinking":                 map[string]any{"type": "string"},
				"runTimeoutSeconds":        map[string]any{"type": "number"},
				"timeoutSeconds":           map[string]any{"type": "number"},
				"thread":                   map[string]any{"type": "boolean"},
				"mode":                     map[string]any{"type": "string"},
				"cleanup":                  map[string]any{"type": "string"},
				"runtime":                  map[string]any{"type": "string"},
				"sandbox":                  map[string]any{"type": "string"},
				"streamTo":                 map[string]any{"type": "string"},
				"model":                    map[string]any{"type": "string"},
				"expectsCompletionMessage": map[string]any{"type": "boolean"},
				"attachments": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name":     map[string]any{"type": "string"},
							"content":  map[string]any{"type": "string"},
							"encoding": map[string]any{"type": "string"},
							"mimeType": map[string]any{"type": "string"},
						},
						"required":             []string{"name", "content"},
						"additionalProperties": false,
					},
				},
				"attachAs": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"mountPath": map[string]any{"type": "string"},
					},
					"additionalProperties": false,
				},
			},
			"required":             []string{"task"},
			"additionalProperties": false,
		},
	}
}

func (t *SessionsSpawnTool) Execute(ctx context.Context, toolCtx ToolContext, args map[string]any) (core.ToolResult, error) {
	for _, key := range unsupportedSessionsSpawnParamKeys {
		if _, ok := args[key]; ok {
			return core.ToolResult{}, ToolInputError{
				Message: `sessions_spawn does not support "` + key + `". Use message delivery tools for channel delivery.`,
			}
		}
	}

	task, err := ReadStringParam(args, "task", true)
	if err != nil {
		return core.ToolResult{}, err
	}
	label, err := ReadStringParam(args, "label", false)
	if err != nil {
		return core.ToolResult{}, err
	}
	runtimeName, err := ReadStringParam(args, "runtime", false)
	if err != nil {
		return core.ToolResult{}, err
	}
	agentID, err := ReadStringParam(args, "agentId", false)
	if err != nil {
		return core.ToolResult{}, err
	}
	modelOverride, err := ReadStringParam(args, "model", false)
	if err != nil {
		return core.ToolResult{}, err
	}
	expectsCompletionMessage, err := ReadBoolParam(args, "expectsCompletionMessage")
	if err != nil {
		return core.ToolResult{}, err
	}
	_, expectsCompletionMessageSet := args["expectsCompletionMessage"]
	thinking, err := ReadStringParam(args, "thinking", false)
	if err != nil {
		return core.ToolResult{}, err
	}
	mode, err := ReadStringParam(args, "mode", false)
	if err != nil {
		return core.ToolResult{}, err
	}
	cleanup, err := ReadStringParam(args, "cleanup", false)
	if err != nil {
		return core.ToolResult{}, err
	}
	sandboxMode, err := ReadStringParam(args, "sandbox", false)
	if err != nil {
		return core.ToolResult{}, err
	}
	streamTo, err := ReadStringParam(args, "streamTo", false)
	if err != nil {
		return core.ToolResult{}, err
	}
	threadRequested := false
	if raw, ok := args["thread"]; ok {
		if value, ok := raw.(bool); ok {
			threadRequested = value
		}
	}

	runTimeoutSeconds := 0
	if raw, ok := args["runTimeoutSeconds"]; ok {
		if n, ok := raw.(float64); ok {
			runTimeoutSeconds = int(n)
		}
	}
	if runTimeoutSeconds == 0 {
		if raw, ok := args["timeoutSeconds"]; ok {
			if n, ok := raw.(float64); ok {
				runTimeoutSeconds = int(n)
			}
		}
	}
	if runTimeoutSeconds == 0 && toolCtx.Run.Identity.SubagentTimeoutSeconds > 0 {
		runTimeoutSeconds = toolCtx.Run.Identity.SubagentTimeoutSeconds
	}
	attachments := readSubagentInlineAttachments(args)
	attachMountPath := ""
	if rawAttachAs, ok := args["attachAs"].(map[string]any); ok {
		attachMountPath, _ = ReadStringParam(rawAttachAs, "mountPath", false)
	}
	maxSpawnDepth := toolCtx.Run.Identity.SubagentMaxSpawnDepth
	if maxSpawnDepth <= 0 {
		maxSpawnDepth = 5
	}
	maxChildren := toolCtx.Run.Identity.SubagentMaxChildren
	if maxChildren <= 0 {
		maxChildren = 5
	}
	plan := taskpkg.BuildSessionsSpawnPlan(taskpkg.SessionsSpawnToolInput{
		Task:                        task,
		Label:                       label,
		Runtime:                     runtimeName,
		AgentID:                     agentID,
		ModelOverride:               modelOverride,
		ExpectsCompletionMessage:    expectsCompletionMessage,
		ExpectsCompletionMessageSet: expectsCompletionMessageSet,
		Thinking:                    thinking,
		RunTimeoutSeconds:           runTimeoutSeconds,
		ThreadRequested:             threadRequested,
		Mode:                        mode,
		Cleanup:                     cleanup,
		SandboxMode:                 sandboxMode,
		StreamTo:                    streamTo,
		Attachments:                 attachments,
		AttachMountPath:             attachMountPath,
	}, taskpkg.SubagentSpawnDefaults{
		RequesterSessionKey:     toolCtx.Run.Session.SessionKey,
		RequesterAgentID:        toolCtx.Run.Identity.ID,
		WorkspaceDir:            toolCtx.Run.WorkspaceDir,
		RouteChannel:            toolCtx.Run.Request.Channel,
		RouteTo:                 toolCtx.Run.Request.To,
		RouteAccountID:          toolCtx.Run.Request.AccountID,
		RouteThreadID:           toolCtx.Run.Request.ThreadID,
		DefaultThinking:         strings.TrimSpace(toolCtx.Run.Identity.SubagentThinking),
		DefaultTimeoutSec:       toolCtx.Run.Identity.SubagentTimeoutSeconds,
		MaxSpawnDepth:           maxSpawnDepth,
		MaxChildren:             maxChildren,
		CurrentDepth:            toolCtx.Run.Request.SpawnDepth,
		ArchiveAfterMinutes:     toolCtx.Run.Identity.SubagentArchiveAfterMinutes,
		AttachmentMaxFiles:      toolCtx.Run.Identity.SubagentAttachmentMaxFiles,
		AttachmentMaxFileBytes:  toolCtx.Run.Identity.SubagentAttachmentMaxFileBytes,
		AttachmentMaxTotalBytes: toolCtx.Run.Identity.SubagentAttachmentMaxTotalBytes,
		RetainAttachmentsOnKeep: toolCtx.Run.Identity.SubagentRetainAttachmentsOnKeep,
	})
	if plan.Runtime == taskpkg.SpawnRuntimeACP {
		if len(plan.SubagentSpawn.Attachments) > 0 {
			return JSONResult(map[string]any{
				"status": "error",
				"error":  "attachments are currently unsupported for runtime=acp; use runtime=subagent or remove attachments",
			})
		}
		if err := taskpkg.ValidateACPSpawnRequest(plan.ACPSpawn); err != nil {
			return JSONResult(map[string]any{
				"status": "error",
				"error":  err.Error(),
			})
		}
		result, err := toolCtx.Runtime.SpawnACPSession(ctx, plan.ACPSpawn)
		if err != nil {
			return core.ToolResult{}, err
		}
		return JSONResult(result)
	}
	if len(plan.SubagentSpawn.Attachments) > 0 && !toolCtx.Run.Identity.SubagentAttachmentsEnabled {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  "attachments are disabled for this agent's subagent policy; remove attachments or enable subagents.attachmentsEnabled",
		})
	}
	if plan.Runtime != taskpkg.SpawnRuntimeSubagent {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  `runtime="` + string(plan.Runtime) + `" is not supported yet in kocort MVP`,
		})
	}
	if err := taskpkg.ValidateSubagentSpawnRequest(plan.SubagentSpawn); err != nil {
		return JSONResult(map[string]any{
			"status": "error",
			"error":  err.Error(),
		})
	}
	result, err := toolCtx.Runtime.SpawnSubagent(ctx, plan.SubagentSpawn)
	if err != nil {
		return core.ToolResult{}, err
	}
	return JSONResult(result)
}

func readSubagentInlineAttachments(args map[string]any) []taskpkg.SubagentInlineAttachment {
	raw, ok := args["attachments"].([]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	items := make([]taskpkg.SubagentInlineAttachment, 0, len(raw))
	for _, entry := range raw {
		obj, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		name, _ := ReadStringParam(obj, "name", false)
		content, _ := ReadStringParam(obj, "content", false)
		encoding, _ := ReadStringParam(obj, "encoding", false)
		mimeType, _ := ReadStringParam(obj, "mimeType", false)
		items = append(items, taskpkg.SubagentInlineAttachment{
			Name:     name,
			Content:  content,
			Encoding: encoding,
			MIMEType: mimeType,
		})
	}
	return items
}
