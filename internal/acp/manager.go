// Package acp — AcpSessionManager manages ACP session lifecycle, caching, and turn execution.
package acp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"

	"github.com/kocort/kocort/utils"
)

// ---------------------------------------------------------------------------
// Internal cached-runtime & active-turn bookkeeping
// ---------------------------------------------------------------------------

type cachedRuntime struct {
	runtime  core.AcpRuntime
	handle   core.AcpRuntimeHandle
	backend  string
	agent    string
	mode     core.AcpRuntimeSessionMode
	cwd      string
	lastUsed time.Time
}

type activeTurn struct {
	runtime core.AcpRuntime
	handle  core.AcpRuntimeHandle
	cancel  context.CancelFunc
}

// ---------------------------------------------------------------------------
// AcpSessionStatus
// ---------------------------------------------------------------------------

// AcpSessionStatus is a snapshot of an ACP session's current state.
type AcpSessionStatus struct {
	SessionKey     string                        `json:"sessionKey"`
	Backend        string                        `json:"backend"`
	Agent          string                        `json:"agent"`
	State          string                        `json:"state"`
	Mode           core.AcpRuntimeSessionMode    `json:"mode"`
	RuntimeOptions core.AcpSessionRuntimeOptions `json:"runtimeOptions,omitempty"`
	Capabilities   core.AcpRuntimeCapabilities   `json:"capabilities,omitempty"`
	RuntimeStatus  *core.AcpRuntimeStatus        `json:"runtimeStatus,omitempty"`
	LastActivityAt int64                         `json:"lastActivityAt,omitempty"`
	LastError      string                        `json:"lastError,omitempty"`
}

// ---------------------------------------------------------------------------
// AcpSessionManager
// ---------------------------------------------------------------------------

// AcpSessionManager manages ACP session lifecycle including caching,
// initialisation, turn execution, and cleanup.
type AcpSessionManager struct {
	mu          sync.Mutex
	cache       map[string]cachedRuntime
	activeTurns map[string]*activeTurn
	idleTTL     time.Duration
}

// NewAcpSessionManager creates a manager with a default 10-minute idle TTL.
func NewAcpSessionManager() *AcpSessionManager {
	return &AcpSessionManager{
		cache:       map[string]cachedRuntime{},
		activeTurns: map[string]*activeTurn{},
		idleTTL:     10 * time.Minute,
	}
}

// NewAcpSessionManagerWithTTL creates a manager with a custom idle TTL.
func NewAcpSessionManagerWithTTL(ttl time.Duration) *AcpSessionManager {
	manager := NewAcpSessionManager()
	if ttl > 0 {
		manager.idleTTL = ttl
	}
	return manager
}

// IdleTTL returns the configured idle eviction TTL.
func (m *AcpSessionManager) IdleTTL() time.Duration {
	return m.idleTTL
}

// ---------------------------------------------------------------------------
// Public methods
// ---------------------------------------------------------------------------

// ResolveSession looks up the ACP metadata for a session key.
func (m *AcpSessionManager) ResolveSession(store *session.SessionStore, sessionKey string) (string, *core.AcpSessionMeta, error) {
	if store == nil {
		return "", nil, fmt.Errorf("session store is not configured")
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return "", nil, core.ErrACPSessionKeyRequired
	}
	entry := store.Entry(sessionKey)
	if entry == nil || entry.ACP == nil {
		if strings.Contains(sessionKey, ":acp:") {
			return sessionKey, nil, fmt.Errorf("ACP metadata is missing for session %q", sessionKey)
		}
		return sessionKey, nil, nil
	}
	copy := *entry.ACP
	return sessionKey, &copy, nil
}

// InitializeSession ensures a runtime session exists and persists the ACP metadata.
func (m *AcpSessionManager) InitializeSession(
	ctx context.Context,
	store *session.SessionStore,
	runtime core.AcpRuntime,
	sessionKey string,
	agent string,
	mode core.AcpRuntimeSessionMode,
	cwd string,
	backendID string,
) (core.AcpRuntimeHandle, *core.AcpSessionMeta, error) {
	if runtime == nil {
		return core.AcpRuntimeHandle{}, nil, core.ErrACPNotConfigured
	}
	if strings.TrimSpace(sessionKey) == "" {
		return core.AcpRuntimeHandle{}, nil, core.ErrACPSessionKeyRequired
	}
	if mode == "" {
		mode = core.AcpSessionModePersistent
	}
	handle, err := runtime.EnsureSession(ctx, core.AcpEnsureSessionInput{
		SessionKey: sessionKey,
		Agent:      session.NormalizeAgentID(agent),
		Mode:       mode,
		Cwd:        strings.TrimSpace(cwd),
	})
	if err != nil {
		return core.AcpRuntimeHandle{}, nil, err
	}
	now := time.Now().UnixMilli()
	meta := &core.AcpSessionMeta{
		Backend:            utils.NonEmpty(strings.TrimSpace(handle.Backend), strings.TrimSpace(backendID)),
		Agent:              session.NormalizeAgentID(agent),
		RuntimeSessionName: strings.TrimSpace(handle.RuntimeSessionName),
		BackendSessionID:   strings.TrimSpace(handle.BackendSessionID),
		AgentSessionID:     strings.TrimSpace(handle.AgentSessionID),
		Cwd:                utils.NonEmpty(strings.TrimSpace(handle.Cwd), strings.TrimSpace(cwd)),
		State:              "idle",
		Mode:               mode,
		LastActivityAt:     now,
	}
	if err := m.writeSessionMeta(store, sessionKey, meta); err != nil {
		_ = runtime.Close(context.Background(), core.AcpCloseInput{Handle: handle, Reason: "init-meta-failed"}) // best-effort; failure is non-critical
		return core.AcpRuntimeHandle{}, nil, err
	}
	m.setCached(sessionKey, cachedRuntime{
		runtime:  runtime,
		handle:   handle,
		backend:  meta.Backend,
		agent:    meta.Agent,
		mode:     mode,
		cwd:      meta.Cwd,
		lastUsed: time.Now().UTC(),
	})
	return handle, meta, nil
}

// GetSessionStatus returns a full status snapshot for an initialised session.
func (m *AcpSessionManager) GetSessionStatus(
	ctx context.Context,
	store *session.SessionStore,
	runtime core.AcpRuntime,
	sessionKey string,
) (AcpSessionStatus, error) {
	resolvedKey, meta, err := m.ResolveSession(store, sessionKey)
	if err != nil {
		return AcpSessionStatus{}, err
	}
	if meta == nil {
		return AcpSessionStatus{}, fmt.Errorf("ACP session %q is not initialized", resolvedKey)
	}
	handle, nextMeta, err := m.ensureRuntimeHandle(ctx, store, runtime, resolvedKey, meta)
	if err != nil {
		return AcpSessionStatus{}, err
	}
	capabilities, err := runtime.GetCapabilities(ctx, &handle)
	if err != nil {
		return AcpSessionStatus{}, err
	}
	status, err := runtime.GetStatus(ctx, handle)
	if err != nil {
		return AcpSessionStatus{}, err
	}
	updated := *nextMeta
	updated.Capabilities = &capabilities
	updated.RuntimeStatus = &status
	updated.Observability = m.buildObservabilitySnapshot(resolvedKey, handle, &updated)
	if err := m.writeSessionMeta(store, resolvedKey, &updated); err != nil {
		return AcpSessionStatus{}, err
	}
	return AcpSessionStatus{
		SessionKey:     resolvedKey,
		Backend:        utils.NonEmpty(strings.TrimSpace(handle.Backend), updated.Backend),
		Agent:          updated.Agent,
		State:          utils.NonEmpty(updated.State, "idle"),
		Mode:           updated.Mode,
		RuntimeOptions: derefAcpRuntimeOptions(updated.RuntimeOptions),
		Capabilities:   capabilities,
		RuntimeStatus:  &status,
		LastActivityAt: updated.LastActivityAt,
		LastError:      updated.LastError,
	}, nil
}

// RunTurn executes a single ACP turn within the given session.
func (m *AcpSessionManager) RunTurn(
	ctx context.Context,
	store *session.SessionStore,
	runtime core.AcpRuntime,
	sessionKey string,
	text string,
	requestID string,
	mode core.AcpRuntimePromptMode,
	onEvent func(core.AcpRuntimeEvent) error,
) (core.AgentRunResult, error) {
	resolvedKey, meta, err := m.ResolveSession(store, sessionKey)
	if err != nil {
		return core.AgentRunResult{}, err
	}
	if meta == nil {
		return core.AgentRunResult{}, fmt.Errorf("ACP session %q is not initialized", resolvedKey)
	}
	handle, nextMeta, err := m.ensureRuntimeHandle(ctx, store, runtime, resolvedKey, meta)
	if err != nil {
		return core.AgentRunResult{}, err
	}
	if err := m.applyRuntimeOptions(ctx, runtime, handle, nextMeta); err != nil {
		return core.AgentRunResult{}, err
	}
	if err := m.setSessionState(store, resolvedKey, "running", ""); err != nil {
		return core.AgentRunResult{}, err
	}

	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.registerActiveTurn(resolvedKey, &activeTurn{runtime: runtime, handle: handle, cancel: cancel})
	defer m.clearActiveTurn(resolvedKey)

	var (
		payloads   []core.ReplyPayload
		usage      = map[string]any{}
		stopReason string
	)
	err = runtime.RunTurn(turnCtx, core.AcpRunTurnInput{
		Handle:    handle,
		Text:      text,
		Mode:      mode,
		RequestID: requestID,
		Signal:    turnCtx,
		OnEvent: func(event core.AcpRuntimeEvent) error {
			switch event.Type {
			case "text_delta":
				text := strings.TrimSpace(event.Text)
				if text != "" {
					payloads = append(payloads, core.ReplyPayload{Text: text, IsReasoning: event.Stream == "thought"})
				}
			case "status":
				if event.Used > 0 {
					usage["used"] = event.Used
				}
				if event.Size > 0 {
					usage["size"] = event.Size
				}
			case "done":
				stopReason = strings.TrimSpace(event.StopReason)
			case "error":
				if strings.TrimSpace(event.Text) != "" {
					return fmt.Errorf(strings.TrimSpace(event.Text))
				}
				return fmt.Errorf("ACP turn failed")
			}
			if onEvent != nil {
				return onEvent(event)
			}
			return nil
		},
	})
	if err != nil {
		_ = m.setSessionState(store, resolvedKey, "error", err.Error()) // best-effort; failure is non-critical
		return core.AgentRunResult{}, err
	}
	if err := m.setSessionState(store, resolvedKey, "idle", ""); err != nil {
		return core.AgentRunResult{}, err
	}
	_ = m.refreshHandleStatus(ctx, store, runtime, resolvedKey, handle, nextMeta) // best-effort; failure is non-critical
	return core.AgentRunResult{
		Payloads:   payloads,
		Usage:      usage,
		StopReason: stopReason,
		Meta: map[string]any{
			"backendKind": "acp",
		},
	}, nil
}

// CancelSession cancels an active turn or sends a cancel to the runtime.
func (m *AcpSessionManager) CancelSession(
	ctx context.Context,
	store *session.SessionStore,
	runtime core.AcpRuntime,
	sessionKey string,
	reason string,
) error {
	if active := m.getActiveTurn(sessionKey); active != nil {
		active.cancel()
		if err := active.runtime.Cancel(ctx, core.AcpCancelInput{Handle: active.handle, Reason: reason}); err != nil {
			return err
		}
		return m.setSessionState(store, sessionKey, "idle", "")
	}
	_, meta, err := m.ResolveSession(store, sessionKey)
	if err != nil {
		return err
	}
	if meta == nil {
		return fmt.Errorf("ACP session %q is not initialized", sessionKey)
	}
	handle, _, err := m.ensureRuntimeHandle(ctx, store, runtime, sessionKey, meta)
	if err != nil {
		return err
	}
	if err := runtime.Cancel(ctx, core.AcpCancelInput{Handle: handle, Reason: reason}); err != nil {
		_ = m.setSessionState(store, sessionKey, "error", err.Error()) // best-effort; failure is non-critical
		return err
	}
	return m.setSessionState(store, sessionKey, "idle", "")
}

// CloseSession closes the runtime session and optionally clears the metadata.
func (m *AcpSessionManager) CloseSession(
	ctx context.Context,
	store *session.SessionStore,
	runtime core.AcpRuntime,
	sessionKey string,
	reason string,
	clearMeta bool,
) error {
	_, meta, err := m.ResolveSession(store, sessionKey)
	if err != nil {
		return err
	}
	if meta == nil {
		return nil
	}
	handle, _, err := m.ensureRuntimeHandle(ctx, store, runtime, sessionKey, meta)
	if err != nil {
		return err
	}
	if err := runtime.Close(ctx, core.AcpCloseInput{Handle: handle, Reason: reason}); err != nil {
		return err
	}
	m.clearCached(sessionKey)
	if !clearMeta {
		return m.setSessionState(store, sessionKey, "closed", "")
	}
	entry := store.Entry(sessionKey)
	if entry == nil {
		return nil
	}
	entry.ACP = nil
	return store.Upsert(sessionKey, *entry)
}

// SnapshotSessions returns a status snapshot for every ACP-enabled session.
func (m *AcpSessionManager) SnapshotSessions(store *session.SessionStore) []AcpSessionStatus {
	if store == nil {
		return nil
	}
	entries := store.AllEntries()
	out := make([]AcpSessionStatus, 0, len(entries))
	for key, entry := range entries {
		if entry.ACP == nil {
			continue
		}
		meta := entry.ACP
		out = append(out, AcpSessionStatus{
			SessionKey:     key,
			Backend:        meta.Backend,
			Agent:          meta.Agent,
			State:          meta.State,
			Mode:           meta.Mode,
			RuntimeOptions: derefAcpRuntimeOptions(meta.RuntimeOptions),
			Capabilities:   derefAcpRuntimeCapabilities(meta.Capabilities),
			RuntimeStatus:  meta.RuntimeStatus,
			LastActivityAt: meta.LastActivityAt,
			LastError:      meta.LastError,
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// Private helpers
// ---------------------------------------------------------------------------

func (m *AcpSessionManager) ensureRuntimeHandle(
	ctx context.Context,
	store *session.SessionStore,
	runtime core.AcpRuntime,
	sessionKey string,
	meta *core.AcpSessionMeta,
) (core.AcpRuntimeHandle, *core.AcpSessionMeta, error) {
	m.evictIdle()
	agent := session.NormalizeAgentID(meta.Agent)
	mode := meta.Mode
	if mode == "" {
		mode = core.AcpSessionModePersistent
	}
	meta.IdentityName = utils.NonEmpty(strings.TrimSpace(meta.IdentityName), agent)
	cwd := strings.TrimSpace(meta.Cwd)
	if cached, ok := m.getCached(sessionKey); ok {
		if cached.agent == agent && cached.mode == mode && cached.cwd == cwd {
			return cached.handle, meta, nil
		}
		m.clearCached(sessionKey)
	}
	handle, err := runtime.EnsureSession(ctx, core.AcpEnsureSessionInput{
		SessionKey: sessionKey,
		Agent:      agent,
		Mode:       mode,
		Cwd:        cwd,
	})
	if err != nil {
		return core.AcpRuntimeHandle{}, nil, err
	}
	nextMeta := *meta
	nextMeta.Backend = utils.NonEmpty(strings.TrimSpace(handle.Backend), nextMeta.Backend)
	nextMeta.RuntimeSessionName = utils.NonEmpty(strings.TrimSpace(handle.RuntimeSessionName), nextMeta.RuntimeSessionName)
	nextMeta.BackendSessionID = utils.NonEmpty(strings.TrimSpace(handle.BackendSessionID), nextMeta.BackendSessionID)
	nextMeta.AgentSessionID = utils.NonEmpty(strings.TrimSpace(handle.AgentSessionID), nextMeta.AgentSessionID)
	nextMeta.Cwd = utils.NonEmpty(strings.TrimSpace(handle.Cwd), nextMeta.Cwd)
	nextMeta.LastActivityAt = time.Now().UnixMilli()
	if err := m.writeSessionMeta(store, sessionKey, &nextMeta); err != nil {
		return core.AcpRuntimeHandle{}, nil, err
	}
	m.setCached(sessionKey, cachedRuntime{
		runtime:  runtime,
		handle:   handle,
		backend:  nextMeta.Backend,
		agent:    nextMeta.Agent,
		mode:     nextMeta.Mode,
		cwd:      nextMeta.Cwd,
		lastUsed: time.Now().UTC(),
	})
	return handle, &nextMeta, nil
}

func (m *AcpSessionManager) applyRuntimeOptions(
	ctx context.Context,
	runtime core.AcpRuntime,
	handle core.AcpRuntimeHandle,
	meta *core.AcpSessionMeta,
) error {
	options := derefAcpRuntimeOptions(meta.RuntimeOptions)
	caps, err := runtime.GetCapabilities(ctx, &handle)
	if err != nil {
		return err
	}
	controls := make(map[core.AcpRuntimeControl]struct{}, len(caps.Controls))
	for _, control := range caps.Controls {
		controls[control] = struct{}{}
	}
	if options.RuntimeMode != "" {
		if _, ok := controls[core.AcpControlSetMode]; ok {
			if err := runtime.SetMode(ctx, core.AcpSetModeInput{Handle: handle, Mode: options.RuntimeMode}); err != nil {
				return err
			}
		}
	}
	if _, ok := controls[core.AcpControlSetConfigOption]; !ok {
		meta.Capabilities = &caps
		meta.UnsupportedOptions = nil
		return nil
	}
	allowedKeys := map[string]struct{}{}
	allowAllConfig := len(caps.ConfigOptionKeys) == 0
	for _, key := range caps.ConfigOptionKeys {
		if trimmed := strings.TrimSpace(key); trimmed != "" {
			allowedKeys[trimmed] = struct{}{}
		}
	}
	setConfigIfAllowed := func(key string, value string) error {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil
		}
		if !allowAllConfig {
			if _, ok := allowedKeys[key]; !ok {
				meta.UnsupportedOptions = AppendUniqueString(meta.UnsupportedOptions, key)
				return nil
			}
		}
		return runtime.SetConfigOption(ctx, core.AcpSetConfigOptionInput{Handle: handle, Key: key, Value: value})
	}
	meta.Capabilities = &caps
	meta.UnsupportedOptions = nil
	if options.Model != "" {
		if err := setConfigIfAllowed("model", options.Model); err != nil {
			return err
		}
	}
	if options.PermissionProfile != "" {
		if err := setConfigIfAllowed("approval_policy", options.PermissionProfile); err != nil {
			return err
		}
	}
	if options.TimeoutSeconds > 0 {
		if err := setConfigIfAllowed("timeout", fmt.Sprintf("%d", options.TimeoutSeconds)); err != nil {
			return err
		}
	}
	for key, value := range options.BackendExtras {
		if err := setConfigIfAllowed(key, value); err != nil {
			return err
		}
	}
	return nil
}

func (m *AcpSessionManager) refreshHandleStatus(
	ctx context.Context,
	store *session.SessionStore,
	runtime core.AcpRuntime,
	sessionKey string,
	handle core.AcpRuntimeHandle,
	meta *core.AcpSessionMeta,
) error {
	caps, err := runtime.GetCapabilities(ctx, &handle)
	if err != nil {
		return err
	}
	status, err := runtime.GetStatus(ctx, handle)
	if err != nil {
		return err
	}
	next := *meta
	next.Capabilities = &caps
	next.RuntimeStatus = &status
	next.BackendSessionID = utils.NonEmpty(status.BackendSessionID, next.BackendSessionID)
	next.AgentSessionID = utils.NonEmpty(status.AgentSessionID, next.AgentSessionID)
	next.LastActivityAt = time.Now().UnixMilli()
	next.Observability = m.buildObservabilitySnapshot(sessionKey, handle, &next)
	return m.writeSessionMeta(store, sessionKey, &next)
}

func (m *AcpSessionManager) writeSessionMeta(store *session.SessionStore, sessionKey string, meta *core.AcpSessionMeta) error {
	entry := store.Entry(sessionKey)
	if entry == nil {
		entry = &core.SessionEntry{SessionID: session.NewSessionID()}
	}
	entry.ACP = meta
	return store.Upsert(sessionKey, *entry)
}

func (m *AcpSessionManager) setSessionState(store *session.SessionStore, sessionKey string, state string, lastErr string) error {
	entry := store.Entry(sessionKey)
	if entry == nil || entry.ACP == nil {
		return nil
	}
	meta := *entry.ACP
	meta.State = strings.TrimSpace(state)
	meta.LastActivityAt = time.Now().UnixMilli()
	meta.LastError = strings.TrimSpace(lastErr)
	entry.ACP = &meta
	return store.Upsert(sessionKey, *entry)
}

func (m *AcpSessionManager) getCached(sessionKey string) (cachedRuntime, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cached, ok := m.cache[sessionKey]
	if ok {
		cached.lastUsed = time.Now().UTC()
		m.cache[sessionKey] = cached
	}
	return cached, ok
}

func (m *AcpSessionManager) setCached(sessionKey string, cached cachedRuntime) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cache[sessionKey] = cached
}

func (m *AcpSessionManager) clearCached(sessionKey string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cache, sessionKey)
}

func (m *AcpSessionManager) evictIdle() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for key, cached := range m.cache {
		if now.Sub(cached.lastUsed) > m.idleTTL {
			delete(m.cache, key)
		}
	}
}

func (m *AcpSessionManager) registerActiveTurn(sessionKey string, turn *activeTurn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeTurns[sessionKey] = turn
}

func (m *AcpSessionManager) clearActiveTurn(sessionKey string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.activeTurns, sessionKey)
}

func (m *AcpSessionManager) getActiveTurn(sessionKey string) *activeTurn {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeTurns[sessionKey]
}

func (m *AcpSessionManager) buildObservabilitySnapshot(sessionKey string, handle core.AcpRuntimeHandle, meta *core.AcpSessionMeta) map[string]any {
	active := m.getActiveTurn(sessionKey) != nil
	return map[string]any{
		"sessionKey":       sessionKey,
		"backend":          utils.NonEmpty(strings.TrimSpace(handle.Backend), strings.TrimSpace(meta.Backend)),
		"runtimeSession":   strings.TrimSpace(handle.RuntimeSessionName),
		"backendSessionId": utils.NonEmpty(strings.TrimSpace(handle.BackendSessionID), strings.TrimSpace(meta.BackendSessionID)),
		"agentSessionId":   utils.NonEmpty(strings.TrimSpace(handle.AgentSessionID), strings.TrimSpace(meta.AgentSessionID)),
		"cwd":              utils.NonEmpty(strings.TrimSpace(handle.Cwd), strings.TrimSpace(meta.Cwd)),
		"state":            strings.TrimSpace(meta.State),
		"hasActiveTurn":    active,
		"observedAt":       time.Now().UTC().Format(time.RFC3339),
	}
}

// ---------------------------------------------------------------------------
// Utility functions
// ---------------------------------------------------------------------------

func derefAcpRuntimeOptions(input *core.AcpSessionRuntimeOptions) core.AcpSessionRuntimeOptions {
	if input == nil {
		return core.AcpSessionRuntimeOptions{}
	}
	return *input
}

func derefAcpRuntimeCapabilities(input *core.AcpRuntimeCapabilities) core.AcpRuntimeCapabilities {
	if input == nil {
		return core.AcpRuntimeCapabilities{}
	}
	return *input
}

// AppendUniqueString appends value to items if it isn't already present (case-insensitive).
func AppendUniqueString(items []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return items
	}
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), value) {
			return items
		}
	}
	return append(items, value)
}
