package runtime

import (
	"fmt"
	"strings"

	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
	toolfn "github.com/kocort/kocort/internal/tool"
)

// toPromptTools converts a slice of rtypes.Tool to []infra.PromptTool.
// rtypes.Tool is a superset of infra.PromptTool (Name + Description), so each
// element is assignable without any data loss.
func toPromptTools(tools []rtypes.Tool) []infra.PromptTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]infra.PromptTool, len(tools))
	for i, t := range tools {
		out[i] = t
	}
	return out
}

// existingToolNames returns a set of normalized tool names from the given tools.
func existingToolNames(tools []rtypes.Tool) map[string]struct{} {
	if len(tools) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		out[toolfn.NormalizeToolPolicyName(tool.Name())] = struct{}{}
	}
	return out
}

// resolveBackend resolves the appropriate backend for the given run context.
func resolveBackend(backends BackendResolver, fallback rtypes.Backend, runCtx rtypes.AgentRunContext) (rtypes.Backend, string, error) {
	backendProvider := strings.TrimSpace(runCtx.ModelSelection.Provider)
	switch strings.ToLower(strings.TrimSpace(runCtx.Identity.RuntimeType)) {
	case "acp":
		if trimmed := strings.TrimSpace(runCtx.Identity.RuntimeBackend); trimmed != "" {
			backendProvider = trimmed
		}
	case "embedded":
		if trimmed := strings.TrimSpace(runCtx.Identity.RuntimeBackend); trimmed != "" {
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

// Note: applyBackendSessionState has been moved to backend.ApplySessionState
// in internal/backend/session_state.go (P0-1 of RUNTIME_SLIM_PLAN).
