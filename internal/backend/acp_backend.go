package backend

import (
	"context"
	"fmt"

	"github.com/kocort/kocort/internal/acp"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	evtpkg "github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/internal/session"

	"strings"
	"time"

	"github.com/kocort/kocort/utils"
)

// ACPBackend implements a backend using the ACP session management protocol.
type ACPBackend struct {
	Config   config.AppConfig
	Env      *infra.EnvironmentRuntime
	Provider string
	Command  core.CommandBackendConfig

	Mgr     *acp.AcpSessionManager
	Runtime core.AcpRuntime
}

func (b *ACPBackend) Run(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
	runCtx.Runtime = ensureRuntime(runCtx)
	if !b.Config.ACP.Enabled {
		return core.AgentRunResult{}, fmt.Errorf("%w (`acp.enabled=false`)", core.ErrACPDisabledByPolicy)
	}
	manager := b.Mgr
	if manager == nil {
		manager = acp.NewAcpSessionManager()
		b.Mgr = manager
	}
	runtime := b.Runtime
	if runtime == nil {
		runtime = NewCLIAcpRuntime(b.Config, b.Env, b.Provider, b.Command)
		b.Runtime = runtime
	}
	mode := core.AcpSessionModePersistent
	if runCtx.Request.Lane == core.LaneNested {
		mode = core.AcpSessionModeOneShot
	}
	if configuredMode := strings.ToLower(strings.TrimSpace(runCtx.Identity.RuntimeMode)); configuredMode == string(core.AcpSessionModeOneShot) {
		mode = core.AcpSessionModeOneShot
	}
	acpAgent := utils.NonEmpty(strings.TrimSpace(runCtx.Identity.RuntimeAgent), runCtx.Identity.ID)
	acpCwd := utils.NonEmpty(strings.TrimSpace(runCtx.Identity.RuntimeCwd), runCtx.WorkspaceDir)
	timeoutSeconds := 0
	if runCtx.Request.Timeout > 0 {
		timeoutSeconds = int(runCtx.Request.Timeout / time.Second)
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = runCtx.Identity.TimeoutSeconds
	}
	if runCtx.Runtime.GetSessions() != nil {
		entry := runCtx.Runtime.GetSessions().Entry(runCtx.Session.SessionKey)
		if entry == nil {
			entry = &core.SessionEntry{SessionID: runCtx.Session.SessionID}
		}
		meta := entry.ACP
		if meta == nil {
			meta = &core.AcpSessionMeta{}
		}
		meta.Backend = NormalizeProviderID(b.Provider)
		meta.Agent = session.NormalizeAgentID(acpAgent)
		meta.IdentityName = utils.NonEmpty(strings.TrimSpace(runCtx.Identity.Name), runCtx.Identity.ID)
		meta.Cwd = acpCwd
		meta.Mode = mode
		meta.State = utils.NonEmpty(meta.State, "idle")
		meta.RuntimeOptions = &core.AcpSessionRuntimeOptions{
			Model:          utils.NonEmpty(strings.TrimSpace(runCtx.ModelSelection.Model), runCtx.Identity.DefaultModel),
			Cwd:            acpCwd,
			TimeoutSeconds: timeoutSeconds,
		}
		entry.ACP = meta
		if err := runCtx.Runtime.GetSessions().Upsert(runCtx.Session.SessionKey, *entry); err != nil {
			return core.AgentRunResult{}, fmt.Errorf("persist ACP session metadata: %w", err)
		}
	}
	meta := runCtx.Session.Entry
	if meta == nil || meta.ACP == nil {
		if _, _, err := manager.InitializeSession(
			ctx,
			runCtx.Runtime.GetSessions(),
			runtime,
			runCtx.Session.SessionKey,
			acpAgent,
			mode,
			acpCwd,
			b.Provider,
		); err != nil {
			return core.AgentRunResult{}, err
		}
	}
	var payloads []core.ReplyPayload
	var hadReasoning, reasoningDone bool
	result, err := manager.RunTurn(
		ctx,
		runCtx.Runtime.GetSessions(),
		runtime,
		runCtx.Session.SessionKey,
		runCtx.Request.Message,
		utils.NonEmpty(strings.TrimSpace(runCtx.Request.RunID), runCtx.Session.SessionID),
		core.AcpPromptModePrompt,
		func(event core.AcpRuntimeEvent) error {
			switch event.Type {
			case "text_delta":
				payload := core.ReplyPayload{Text: strings.TrimSpace(event.Text), IsReasoning: event.Stream == "thought"}
				if payload.Text == "" {
					return nil
				}
				if event.Stream == "thought" {
					hadReasoning = true
					evtpkg.EmitDebugEvent(runCtx.Runtime.GetEventBus(), runCtx.Session.SessionKey, runCtx.Request.RunID, "assistant", map[string]any{
						"type":   "reasoning_delta",
						"text":   payload.Text,
						"stream": event.Stream,
					})
				} else {
					// Emit reasoning_complete when transitioning from reasoning to text output.
					if hadReasoning && !reasoningDone {
						reasoningDone = true
						evtpkg.EmitDebugEvent(runCtx.Runtime.GetEventBus(), runCtx.Session.SessionKey, runCtx.Request.RunID, "assistant", map[string]any{
							"type": "reasoning_complete",
						})
					}
					evtpkg.EmitDebugEvent(runCtx.Runtime.GetEventBus(), runCtx.Session.SessionKey, runCtx.Request.RunID, "assistant", map[string]any{
						"type":   "text_delta",
						"text":   payload.Text,
						"stream": event.Stream,
					})
				}
				runCtx.ReplyDispatcher.SendBlockReply(payload)
				payloads = append(payloads, payload)
			case "tool_call":
				text := strings.TrimSpace(event.Text)
				if text == "" {
					return nil
				}
				// Emit reasoning_complete when transitioning from reasoning to tool calls.
				if hadReasoning && !reasoningDone {
					reasoningDone = true
					evtpkg.EmitDebugEvent(runCtx.Runtime.GetEventBus(), runCtx.Session.SessionKey, runCtx.Request.RunID, "assistant", map[string]any{
						"type": "reasoning_complete",
					})
				}
				evtpkg.EmitDebugEvent(runCtx.Runtime.GetEventBus(), runCtx.Session.SessionKey, runCtx.Request.RunID, "tool", map[string]any{
					"type":   "tool_call",
					"text":   text,
					"status": event.Status,
				})
				payload := core.ReplyPayload{Text: text}
				runCtx.ReplyDispatcher.SendToolResult(payload)
			case "done":
				// Emit reasoning_complete at end if reasoning never transitioned.
				if hadReasoning && !reasoningDone {
					reasoningDone = true
					evtpkg.EmitDebugEvent(runCtx.Runtime.GetEventBus(), runCtx.Session.SessionKey, runCtx.Request.RunID, "assistant", map[string]any{
						"type": "reasoning_complete",
					})
				}
				evtpkg.EmitDebugEvent(runCtx.Runtime.GetEventBus(), runCtx.Session.SessionKey, runCtx.Request.RunID, "lifecycle", map[string]any{
					"type":       "done",
					"stopReason": event.StopReason,
				})
			case "error":
				evtpkg.EmitDebugEvent(runCtx.Runtime.GetEventBus(), runCtx.Session.SessionKey, runCtx.Request.RunID, "lifecycle", map[string]any{
					"type":  "error",
					"error": event.Text,
					"code":  event.Code,
				})
			}
			return nil
		},
	)
	if err != nil {
		return core.AgentRunResult{}, err
	}
	if result.Meta == nil {
		result.Meta = map[string]any{}
	}
	result.Meta["backendKind"] = "acp"
	result.Meta["provider"] = NormalizeProviderID(b.Provider)
	if snapshot := manager.SnapshotSessions(runCtx.Runtime.GetSessions()); len(snapshot) > 0 {
		result.Meta["acpSnapshotSize"] = len(snapshot)
	}
	if sessionID, ok := result.Usage["sessionId"].(string); ok && sessionID != "" {
		result.Meta["acpSessionId"] = sessionID
	}
	if len(result.Payloads) == 0 && len(payloads) > 0 {
		result.Payloads = append([]core.ReplyPayload{}, payloads...)
	}
	return result, nil
}
