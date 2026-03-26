package acp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"

	"github.com/kocort/kocort/utils"
)

// ---------------------------------------------------------------------------
// ACP Lifecycle Controller — resume, control, and observability
// ---------------------------------------------------------------------------

// AcpSessionResumeInput carries the parameters for resuming a session.
type AcpSessionResumeInput struct {
	SessionKey string
	Agent      string
	Cwd        string
	Mode       core.AcpRuntimeSessionMode
	BackendID  string
	Reason     string
}

// AcpSessionResumeResult is the outcome of a resume operation.
type AcpSessionResumeResult struct {
	SessionKey string                     `json:"sessionKey"`
	Backend    string                     `json:"backend"`
	State      string                     `json:"state"`
	Mode       core.AcpRuntimeSessionMode `json:"mode"`
	Resumed    bool                       `json:"resumed"`
}

// AcpSessionControlInput carries a control action for a session.
type AcpSessionControlInput struct {
	SessionKey string
	BackendID  string
	Action     string // "pause", "resume-runtime", "set-mode", "set-config"
	Key        string // config key (for set-config)
	Value      string // value (for set-config / set-mode)
	Reason     string
}

// AcpSessionControlResult is the outcome of a control operation.
type AcpSessionControlResult struct {
	SessionKey string `json:"sessionKey"`
	Action     string `json:"action"`
	Success    bool   `json:"success"`
	State      string `json:"state,omitempty"`
	Error      string `json:"error,omitempty"`
}

// AcpSessionObservability is an extended observability snapshot.
type AcpSessionObservability struct {
	SessionKey       string                       `json:"sessionKey"`
	Backend          string                       `json:"backend"`
	Agent            string                       `json:"agent"`
	State            string                       `json:"state"`
	Mode             core.AcpRuntimeSessionMode   `json:"mode"`
	HasActiveTurn    bool                         `json:"hasActiveTurn"`
	CachedRuntime    bool                         `json:"cachedRuntime"`
	BackendSessionID string                       `json:"backendSessionId,omitempty"`
	AgentSessionID   string                       `json:"agentSessionId,omitempty"`
	RuntimeSession   string                       `json:"runtimeSession,omitempty"`
	Cwd              string                       `json:"cwd,omitempty"`
	LastActivityAt   int64                        `json:"lastActivityAt,omitempty"`
	LastError        string                       `json:"lastError,omitempty"`
	Capabilities     *core.AcpRuntimeCapabilities `json:"capabilities,omitempty"`
	RuntimeStatus    *core.AcpRuntimeStatus       `json:"runtimeStatus,omitempty"`
	ObservedAt       string                       `json:"observedAt"`
}

// ResumeSession re-initialises a previously closed or errored ACP session
// without losing the session entry. This is the "resume after crash" path.
func (m *AcpSessionManager) ResumeSession(
	ctx context.Context,
	store *session.SessionStore,
	runtime core.AcpRuntime,
	input AcpSessionResumeInput,
) (AcpSessionResumeResult, error) {
	sessionKey := strings.TrimSpace(input.SessionKey)
	if sessionKey == "" {
		return AcpSessionResumeResult{}, core.ErrACPSessionKeyRequired
	}
	if runtime == nil {
		return AcpSessionResumeResult{}, core.ErrACPNotConfigured
	}

	// Clear cached entry so we force a fresh EnsureSession.
	m.clearCached(sessionKey)

	agent := session.NormalizeAgentID(input.Agent)
	mode := input.Mode
	if mode == "" {
		mode = core.AcpSessionModePersistent
	}

	// If existing meta exists, inherit fields that weren't overridden.
	_, existingMeta, _ := m.ResolveSession(store, sessionKey)
	if existingMeta != nil {
		if agent == "" {
			agent = existingMeta.Agent
		}
		if input.Cwd == "" {
			input.Cwd = existingMeta.Cwd
		}
	}

	handle, meta, err := m.InitializeSession(ctx, store, runtime, sessionKey, agent, mode, input.Cwd, input.BackendID)
	if err != nil {
		return AcpSessionResumeResult{}, err
	}
	_ = handle // used internally by InitializeSession

	return AcpSessionResumeResult{
		SessionKey: sessionKey,
		Backend:    meta.Backend,
		State:      utils.NonEmpty(meta.State, "idle"),
		Mode:       mode,
		Resumed:    true,
	}, nil
}

// ControlSession sends a control action to an active ACP session.
func (m *AcpSessionManager) ControlSession(
	ctx context.Context,
	store *session.SessionStore,
	runtime core.AcpRuntime,
	input AcpSessionControlInput,
) (AcpSessionControlResult, error) {
	sessionKey := strings.TrimSpace(input.SessionKey)
	if sessionKey == "" {
		return AcpSessionControlResult{}, core.ErrACPSessionKeyRequired
	}
	if runtime == nil {
		return AcpSessionControlResult{}, core.ErrACPNotConfigured
	}

	resolvedKey, meta, err := m.ResolveSession(store, sessionKey)
	if err != nil {
		return AcpSessionControlResult{}, err
	}
	if meta == nil {
		return AcpSessionControlResult{}, fmt.Errorf("ACP session %q is not initialized", resolvedKey)
	}

	handle, nextMeta, err := m.ensureRuntimeHandle(ctx, store, runtime, resolvedKey, meta)
	if err != nil {
		return AcpSessionControlResult{}, err
	}

	var controlErr error
	action := strings.TrimSpace(strings.ToLower(input.Action))

	switch action {
	case "pause":
		controlErr = runtime.Cancel(ctx, core.AcpCancelInput{Handle: handle, Reason: utils.NonEmpty(input.Reason, "pause")})
		if controlErr == nil {
			_ = m.setSessionState(store, resolvedKey, "paused", "")
		}

	case "resume-runtime":
		// Re-ensure session to wake it up.
		m.clearCached(resolvedKey)
		_, nextMeta, controlErr = m.ensureRuntimeHandle(ctx, store, runtime, resolvedKey, meta)
		if controlErr == nil {
			_ = m.setSessionState(store, resolvedKey, "idle", "")
		}

	case "set-mode":
		caps, capErr := runtime.GetCapabilities(ctx, &handle)
		if capErr != nil {
			controlErr = capErr
		} else if containsControl(caps.Controls, core.AcpControlSetMode) {
			controlErr = runtime.SetMode(ctx, core.AcpSetModeInput{Handle: handle, Mode: strings.TrimSpace(input.Value)})
		} else {
			controlErr = fmt.Errorf("set-mode is not supported by runtime")
		}

	case "set-config":
		caps, capErr := runtime.GetCapabilities(ctx, &handle)
		if capErr != nil {
			controlErr = capErr
		} else if containsControl(caps.Controls, core.AcpControlSetConfigOption) {
			controlErr = runtime.SetConfigOption(ctx, core.AcpSetConfigOptionInput{
				Handle: handle,
				Key:    strings.TrimSpace(input.Key),
				Value:  strings.TrimSpace(input.Value),
			})
		} else {
			controlErr = fmt.Errorf("set-config is not supported by runtime")
		}

	default:
		return AcpSessionControlResult{Action: action, Error: "unknown action"}, fmt.Errorf("unknown ACP control action: %q", action)
	}

	state := utils.NonEmpty(nextMeta.State, "idle")
	if controlErr != nil {
		_ = m.setSessionState(store, resolvedKey, "error", controlErr.Error())
		return AcpSessionControlResult{
			SessionKey: resolvedKey,
			Action:     action,
			Success:    false,
			State:      "error",
			Error:      controlErr.Error(),
		}, controlErr
	}

	return AcpSessionControlResult{
		SessionKey: resolvedKey,
		Action:     action,
		Success:    true,
		State:      state,
	}, nil
}

// GetFullObservability returns an extended observability snapshot including
// capabilities and runtime status.
func (m *AcpSessionManager) GetFullObservability(
	ctx context.Context,
	store *session.SessionStore,
	runtime core.AcpRuntime,
	sessionKey string,
) (AcpSessionObservability, error) {
	resolvedKey, meta, err := m.ResolveSession(store, sessionKey)
	if err != nil {
		return AcpSessionObservability{}, err
	}
	if meta == nil {
		return AcpSessionObservability{}, fmt.Errorf("ACP session %q is not initialized", resolvedKey)
	}

	obs := AcpSessionObservability{
		SessionKey:       resolvedKey,
		Backend:          meta.Backend,
		Agent:            meta.Agent,
		State:            utils.NonEmpty(meta.State, "unknown"),
		Mode:             meta.Mode,
		HasActiveTurn:    m.getActiveTurn(resolvedKey) != nil,
		BackendSessionID: meta.BackendSessionID,
		AgentSessionID:   meta.AgentSessionID,
		RuntimeSession:   meta.RuntimeSessionName,
		Cwd:              meta.Cwd,
		LastActivityAt:   meta.LastActivityAt,
		LastError:        meta.LastError,
		ObservedAt:       time.Now().UTC().Format(time.RFC3339),
	}

	// Check cache.
	if _, ok := m.getCached(resolvedKey); ok {
		obs.CachedRuntime = true
	}

	if runtime != nil {
		handle, _, handleErr := m.ensureRuntimeHandle(ctx, store, runtime, resolvedKey, meta)
		if handleErr == nil {
			caps, capErr := runtime.GetCapabilities(ctx, &handle)
			if capErr == nil {
				obs.Capabilities = &caps
			}
			status, statusErr := runtime.GetStatus(ctx, handle)
			if statusErr == nil {
				obs.RuntimeStatus = &status
			}
		}
	}

	return obs, nil
}

// ResumeAllPersistent re-initialises all persistent ACP sessions that are in
// an error or closed state. This is called on process restart.
func (m *AcpSessionManager) ResumeAllPersistent(
	ctx context.Context,
	store *session.SessionStore,
	runtime core.AcpRuntime,
) []AcpSessionResumeResult {
	if store == nil || runtime == nil {
		return nil
	}
	entries := store.AllEntries()
	results := make([]AcpSessionResumeResult, 0)
	for key, entry := range entries {
		if entry.ACP == nil {
			continue
		}
		if entry.ACP.Mode != core.AcpSessionModePersistent {
			continue
		}
		state := strings.ToLower(strings.TrimSpace(entry.ACP.State))
		if state != "error" && state != "closed" && state != "" {
			continue
		}
		result, err := m.ResumeSession(ctx, store, runtime, AcpSessionResumeInput{
			SessionKey: key,
			Agent:      entry.ACP.Agent,
			Cwd:        entry.ACP.Cwd,
			Mode:       entry.ACP.Mode,
			BackendID:  entry.ACP.Backend,
			Reason:     "process-restart",
		})
		if err == nil {
			results = append(results, result)
		}
	}
	return results
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func containsControl(controls []core.AcpRuntimeControl, want core.AcpRuntimeControl) bool {
	for _, c := range controls {
		if c == want {
			return true
		}
	}
	return false
}
