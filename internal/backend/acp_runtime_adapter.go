package backend

import (
	"context"
	"fmt"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"

	"github.com/kocort/kocort/utils"
	"strings"
	"sync"
	"time"
)

type acpEventDeliverer struct {
	onEvent func(core.AcpRuntimeEvent) error
}

func (d *acpEventDeliverer) Deliver(_ context.Context, kind core.ReplyKind, payload core.ReplyPayload, _ core.DeliveryTarget) error {
	if d == nil || d.onEvent == nil {
		return nil
	}
	event := core.AcpRuntimeEvent{Type: "text_delta", Text: payload.Text}
	if payload.IsReasoning {
		event.Stream = "thought"
	} else {
		event.Stream = "output"
	}
	if kind == core.ReplyKindTool {
		event.Type = "tool_call"
		event.Status = "completed"
	}
	return d.onEvent(event)
}

// CLIAcpRuntime implements core.AcpRuntime using a CLI backend.
type CLIAcpRuntime struct {
	config   config.AppConfig
	env      *infra.EnvironmentRuntime
	provider string
	command  core.CommandBackendConfig

	mu      sync.Mutex
	handles map[string]core.AcpRuntimeHandle
}

// NewCLIAcpRuntime creates a new CLI-based ACP runtime.
func NewCLIAcpRuntime(cfg config.AppConfig, env *infra.EnvironmentRuntime, provider string, command core.CommandBackendConfig) *CLIAcpRuntime {
	return &CLIAcpRuntime{
		config:   cfg,
		env:      env,
		provider: NormalizeProviderID(provider),
		command:  command,
		handles:  map[string]core.AcpRuntimeHandle{},
	}
}

func (r *CLIAcpRuntime) EnsureSession(ctx context.Context, input core.AcpEnsureSessionInput) (core.AcpRuntimeHandle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if handle, ok := r.handles[input.SessionKey]; ok {
		if input.Cwd != "" && handle.Cwd == "" {
			handle.Cwd = input.Cwd
			r.handles[input.SessionKey] = handle
		}
		return handle, nil
	}
	handle := core.AcpRuntimeHandle{
		SessionKey:         input.SessionKey,
		Backend:            r.provider,
		RuntimeSessionName: input.SessionKey,
		Cwd:                strings.TrimSpace(input.Cwd),
	}
	r.handles[input.SessionKey] = handle
	return handle, nil
}

func (r *CLIAcpRuntime) RunTurn(ctx context.Context, input core.AcpRunTurnInput) error {
	deliverer := &acpEventDeliverer{onEvent: input.OnEvent}
	dispatcher := delivery.NewReplyDispatcher(deliverer, core.DeliveryTarget{SessionKey: input.Handle.SessionKey})
	runCtx := rtypes.AgentRunContext{
		Request: core.AgentRunRequest{
			Message:    input.Text,
			SessionKey: input.Handle.SessionKey,
			SessionID:  utils.NonEmpty(input.Handle.AgentSessionID, input.Handle.BackendSessionID),
			Timeout:    timeSecondFallback(ctx),
		},
		Session: core.SessionResolution{
			SessionKey: input.Handle.SessionKey,
			SessionID:  utils.NonEmpty(input.Handle.AgentSessionID, input.Handle.BackendSessionID),
			Entry: &core.SessionEntry{
				SessionID: input.Handle.AgentSessionID,
				ACP: &core.AcpSessionMeta{
					Backend:          r.provider,
					BackendSessionID: input.Handle.BackendSessionID,
					AgentSessionID:   input.Handle.AgentSessionID,
					Cwd:              input.Handle.Cwd,
				},
			},
		},
		ModelSelection: core.ModelSelection{
			Provider: r.provider,
		},
		WorkspaceDir:    input.Handle.Cwd,
		ReplyDispatcher: dispatcher,
	}
	cliBackend := &CLIBackend{
		Config:   r.config,
		Env:      r.env,
		Provider: r.provider,
		Command:  r.command,
	}
	result, err := cliBackend.Run(ctx, runCtx)
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(context.Background()) // best-effort; failure is non-critical
	if err != nil {
		if input.OnEvent != nil {
			_ = input.OnEvent(core.AcpRuntimeEvent{Type: "error", Text: err.Error(), Code: string(ErrorReason(err))}) // best-effort; failure is non-critical
		}
		return err
	}
	if sessionID, _ := result.Usage["sessionId"].(string); strings.TrimSpace(sessionID) != "" { // zero value fallback is intentional
		input.Handle.BackendSessionID = strings.TrimSpace(sessionID)
		input.Handle.AgentSessionID = strings.TrimSpace(sessionID)
		r.mu.Lock()
		r.handles[input.Handle.SessionKey] = input.Handle
		r.mu.Unlock()
	}
	if input.OnEvent != nil {
		if used, ok := result.Usage["used"].(int); ok {
			_ = input.OnEvent(core.AcpRuntimeEvent{Type: "status", Used: used}) // best-effort; failure is non-critical
		}
		_ = input.OnEvent(core.AcpRuntimeEvent{Type: "done", StopReason: result.StopReason}) // best-effort; failure is non-critical
	}
	return nil
}

func (r *CLIAcpRuntime) GetCapabilities(_ context.Context, _ *core.AcpRuntimeHandle) (core.AcpRuntimeCapabilities, error) {
	return core.AcpRuntimeCapabilities{
		Controls: []core.AcpRuntimeControl{
			core.AcpControlSetMode,
			core.AcpControlSetConfigOption,
			core.AcpControlStatus,
		},
		ConfigOptionKeys: []string{"model", "approval_policy", "timeout"},
	}, nil
}

func (r *CLIAcpRuntime) GetStatus(_ context.Context, handle core.AcpRuntimeHandle) (core.AcpRuntimeStatus, error) {
	status := core.AcpRuntimeStatus{
		Summary:          "ready",
		BackendSessionID: handle.BackendSessionID,
		AgentSessionID:   handle.AgentSessionID,
		Details: map[string]any{
			"cwd":        handle.Cwd,
			"provider":   r.provider,
			"command":    strings.TrimSpace(r.command.Command),
			"sessionKey": handle.SessionKey,
		},
	}
	return status, nil
}

func (r *CLIAcpRuntime) SetMode(_ context.Context, input core.AcpSetModeInput) error {
	if strings.TrimSpace(input.Mode) == "" {
		return fmt.Errorf("ACP runtime mode is required")
	}
	return nil
}

func (r *CLIAcpRuntime) SetConfigOption(_ context.Context, input core.AcpSetConfigOptionInput) error {
	if strings.TrimSpace(input.Key) == "" {
		return fmt.Errorf("ACP config key is required")
	}
	return nil
}

func (r *CLIAcpRuntime) Cancel(_ context.Context, _ core.AcpCancelInput) error {
	return nil
}

func (r *CLIAcpRuntime) Close(_ context.Context, input core.AcpCloseInput) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.handles, input.Handle.SessionKey)
	return nil
}

func timeSecondFallback(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		if until := time.Until(deadline); until > 0 {
			return until
		}
	}
	return 60 * time.Second
}
