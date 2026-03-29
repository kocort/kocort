package backend

import (
	"fmt"
	"strings"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/rtypes"
)

// BackendResolver resolves a model backend by provider identifier.
// The canonical implementation is *BackendRegistry.
type BackendResolver interface {
	Resolve(provider string) (rtypes.Backend, string, error)
}

// ResolveBackendForRun resolves the appropriate backend for the given run
// context. It checks the agent's runtime type (acp/embedded) for backend
// overrides, falls back to the provider from model selection, and finally
// uses the fallback backend.
func ResolveBackendForRun(backends BackendResolver, fallback rtypes.Backend, identity core.AgentIdentity, selection core.ModelSelection) (rtypes.Backend, string, error) {
	backendProvider := strings.TrimSpace(selection.Provider)
	switch strings.ToLower(strings.TrimSpace(identity.RuntimeType)) {
	case "acp":
		if trimmed := strings.TrimSpace(identity.RuntimeBackend); trimmed != "" {
			backendProvider = trimmed
		}
	case "embedded":
		if trimmed := strings.TrimSpace(identity.RuntimeBackend); trimmed != "" {
			backendProvider = trimmed
		}
	}
	if backends != nil && backendProvider != "" {
		backend, kind, err := backends.Resolve(backendProvider)
		if err == nil && backend != nil {
			return backend, kind, nil
		}
	}
	if fallback != nil {
		return fallback, "", nil
	}
	return nil, "", fmt.Errorf("runtime backend is not configured")
}
