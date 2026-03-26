package runtime

import (
	"context"
	"sort"
	"strings"

	"github.com/kocort/kocort/internal/acp"
	backendpkg "github.com/kocort/kocort/internal/backend"
	"github.com/kocort/kocort/internal/core"
)

// ListACPSessions returns the persisted ACP session snapshot.
func (r *Runtime) ListACPSessions() []acp.AcpSessionStatus {
	if r == nil || r.Sessions == nil {
		return nil
	}
	sessions := acp.NewAcpSessionManager().SnapshotSessions(r.Sessions)
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].SessionKey < sessions[j].SessionKey
	})
	return sessions
}

// GetACPSessionStatus returns a fresh ACP session status snapshot.
func (r *Runtime) GetACPSessionStatus(ctx context.Context, sessionKey string) (acp.AcpSessionStatus, error) {
	backend, err := r.resolveACPBackend("", sessionKey)
	if err != nil {
		return acp.AcpSessionStatus{}, err
	}
	manager, runtime, err := ensureACPServices(backend)
	if err != nil {
		return acp.AcpSessionStatus{}, err
	}
	return manager.GetSessionStatus(ctx, r.Sessions, runtime, sessionKey)
}

// GetACPObservability returns an extended ACP observability snapshot.
func (r *Runtime) GetACPObservability(ctx context.Context, sessionKey string) (acp.AcpSessionObservability, error) {
	backend, err := r.resolveACPBackend("", sessionKey)
	if err != nil {
		return acp.AcpSessionObservability{}, err
	}
	manager, runtime, err := ensureACPServices(backend)
	if err != nil {
		return acp.AcpSessionObservability{}, err
	}
	return manager.GetFullObservability(ctx, r.Sessions, runtime, sessionKey)
}

// ControlACPSession applies a control operation to an ACP session.
func (r *Runtime) ControlACPSession(ctx context.Context, input acp.AcpSessionControlInput) (acp.AcpSessionControlResult, error) {
	backend, err := r.resolveACPBackend(input.BackendID, input.SessionKey)
	if err != nil {
		return acp.AcpSessionControlResult{}, err
	}
	manager, runtime, err := ensureACPServices(backend)
	if err != nil {
		return acp.AcpSessionControlResult{}, err
	}
	return manager.ControlSession(ctx, r.Sessions, runtime, input)
}

// ResumeACPSession resumes a single ACP session.
func (r *Runtime) ResumeACPSession(ctx context.Context, input acp.AcpSessionResumeInput) (acp.AcpSessionResumeResult, error) {
	backend, err := r.resolveACPBackend(input.BackendID, input.SessionKey)
	if err != nil {
		return acp.AcpSessionResumeResult{}, err
	}
	manager, runtime, err := ensureACPServices(backend)
	if err != nil {
		return acp.AcpSessionResumeResult{}, err
	}
	return manager.ResumeSession(ctx, r.Sessions, runtime, input)
}

// ResumeAllPersistentACPSessions resumes persistent ACP sessions after restart.
func (r *Runtime) ResumeAllPersistentACPSessions(ctx context.Context) []acp.AcpSessionResumeResult {
	if r == nil || r.Sessions == nil {
		return nil
	}
	entries := r.Sessions.AllEntries()
	results := make([]acp.AcpSessionResumeResult, 0)
	for sessionKey, entry := range entries {
		if entry.ACP == nil || entry.ACP.Mode != core.AcpSessionModePersistent {
			continue
		}
		state := strings.ToLower(strings.TrimSpace(entry.ACP.State))
		if state != "" && state != "error" && state != "closed" {
			continue
		}
		result, err := r.ResumeACPSession(ctx, acp.AcpSessionResumeInput{
			SessionKey: sessionKey,
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
	sort.Slice(results, func(i, j int) bool {
		return results[i].SessionKey < results[j].SessionKey
	})
	return results
}

func (r *Runtime) resolveACPBackend(explicitBackendID string, sessionKey string) (*backendpkg.ACPBackend, error) {
	for _, candidate := range r.acpBackendCandidates(explicitBackendID, sessionKey) {
		if backend := r.resolveACPBackendCandidate(candidate); backend != nil {
			return backend, nil
		}
	}
	return nil, core.ErrACPNotConfigured
}

func (r *Runtime) acpBackendCandidates(explicitBackendID string, sessionKey string) []string {
	candidates := make([]string, 0, 4)
	seen := map[string]struct{}{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		candidates = append(candidates, value)
	}
	add(explicitBackendID)
	if r != nil && r.Sessions != nil && strings.TrimSpace(sessionKey) != "" {
		if entry := r.Sessions.Entry(sessionKey); entry != nil && entry.ACP != nil {
			add(entry.ACP.Backend)
		}
	}
	if r != nil {
		add(r.Config.ACP.Backend)
		for providerID, providerCfg := range r.Config.Models.Providers {
			if strings.EqualFold(strings.TrimSpace(providerCfg.API), "acp") {
				add(providerID)
			}
		}
		if backend, ok := r.Backend.(*backendpkg.ACPBackend); ok {
			add(backend.Provider)
		}
	}
	return candidates
}

func (r *Runtime) resolveACPBackendCandidate(candidate string) *backendpkg.ACPBackend {
	if r == nil {
		return nil
	}
	candidate = strings.TrimSpace(candidate)
	if r.Backends != nil && candidate != "" {
		resolved, kind, err := r.Backends.Resolve(candidate)
		if err == nil && strings.EqualFold(strings.TrimSpace(kind), "acp") {
			if acpBackend, ok := resolved.(*backendpkg.ACPBackend); ok {
				return acpBackend
			}
		}
	}
	if acpBackend, ok := r.Backend.(*backendpkg.ACPBackend); ok {
		if candidate == "" || strings.EqualFold(strings.TrimSpace(acpBackend.Provider), candidate) {
			return acpBackend
		}
	}
	return nil
}

func ensureACPServices(acpBackend *backendpkg.ACPBackend) (*acp.AcpSessionManager, core.AcpRuntime, error) {
	if acpBackend == nil {
		return nil, nil, core.ErrACPNotConfigured
	}
	manager := acpBackend.Mgr
	if manager == nil {
		manager = acp.NewAcpSessionManager()
		acpBackend.Mgr = manager
	}
	runtime := acpBackend.Runtime
	if runtime == nil {
		if acpBackend.Env == nil {
			return nil, nil, core.ErrACPNotConfigured
		}
		runtime = backendpkg.NewCLIAcpRuntime(acpBackend.Config, acpBackend.Env, acpBackend.Provider, acpBackend.Command)
		acpBackend.Runtime = runtime
	}
	return manager, runtime, nil
}
