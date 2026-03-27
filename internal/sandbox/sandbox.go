package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/internal/session"

	"github.com/kocort/kocort/utils"
)

// ResolveSandboxContext returns the sandbox metadata for a tool invocation.
//
// The resulting SandboxContext intentionally tracks concepts that are related
// but not identical:
//   - runCtx.WorkspaceDir: the tool's default working directory (default pwd)
//   - SandboxContext.WorkspaceDir: the sandbox-owned workspace path, if any
//   - SandboxContext.SandboxDirs: extra access-boundary directories layered on
//     top of the default working directory
//
// Moved from runtime/sandbox.go; the function already took a
// rtypes.RuntimeServices interface so no callers need updating beyond the
// import path.
func ResolveSandboxContext(_ context.Context, runtime rtypes.RuntimeServices, runCtx rtypes.AgentRunContext) (*rtypes.SandboxContext, error) {
	mode := strings.TrimSpace(strings.ToLower(runCtx.Identity.SandboxMode))

	// If user-level sandbox is enabled and dirs are configured, apply
	// directory restrictions regardless of the agent sandbox mode.
	userSandboxActive := runCtx.Identity.SandboxEnabled && len(runCtx.Identity.SandboxDirs) > 0
	effectiveSandboxDirs := []string(nil)
	effectiveWorkspaceDir := strings.TrimSpace(runCtx.WorkspaceDir)
	if userSandboxActive {
		effectiveSandboxDirs = append([]string{}, runCtx.Identity.SandboxDirs...)
		effectiveWorkspaceDir = effectiveSandboxDirs[0]
	}

	if mode == "" || mode == "off" {
		return &rtypes.SandboxContext{
			Enabled:         userSandboxActive,
			WorkspaceAccess: "rw",
			WorkspaceDir:    effectiveWorkspaceDir,
			SandboxDirs:     effectiveSandboxDirs,
			AgentWorkspace:  runCtx.WorkspaceDir,
		}, nil
	}
	if mode == "non-main" && runCtx.Identity.ID == session.DefaultAgentID {
		return &rtypes.SandboxContext{
			Enabled:         userSandboxActive,
			Mode:            mode,
			WorkspaceAccess: "rw",
			WorkspaceDir:    effectiveWorkspaceDir,
			SandboxDirs:     effectiveSandboxDirs,
			AgentWorkspace:  runCtx.WorkspaceDir,
		}, nil
	}
	workspaceAccess := strings.TrimSpace(strings.ToLower(runCtx.Identity.SandboxWorkspaceAccess))
	if workspaceAccess == "" {
		workspaceAccess = "ro"
	}
	scope := strings.TrimSpace(strings.ToLower(runCtx.Identity.SandboxScope))
	if scope == "" {
		scope = "agent"
	}
	root := strings.TrimSpace(runCtx.Identity.SandboxWorkspaceRoot)
	if root == "" {
		root = filepath.Join(runtime.GetSessions().BaseDir(), "sandboxes")
	}
	scopeKey := resolveSandboxScopeKey(runCtx, scope)
	sandboxWorkspace := filepath.Join(root, scopeKey)
	if err := os.MkdirAll(sandboxWorkspace, 0o755); err != nil {
		return nil, err
	}
	workspaceDir := sandboxWorkspace
	if workspaceAccess == "rw" {
		workspaceDir = runCtx.WorkspaceDir
	}
	return &rtypes.SandboxContext{
		Enabled:         true,
		Mode:            mode,
		WorkspaceAccess: workspaceAccess,
		Scope:           scope,
		ScopeKey:        scopeKey,
		WorkspaceRoot:   root,
		WorkspaceDir:    workspaceDir,
		SandboxDirs:     effectiveSandboxDirs,
		AgentWorkspace:  runCtx.WorkspaceDir,
	}, nil
}

func resolveSandboxScopeKey(runCtx rtypes.AgentRunContext, scope string) string {
	switch scope {
	case "session":
		if strings.TrimSpace(runCtx.Session.SessionKey) != "" {
			return sanitizeSandboxScopeKey(runCtx.Session.SessionKey)
		}
	case "shared":
		return "shared"
	}
	return sanitizeSandboxScopeKey(utils.NonEmpty(runCtx.Identity.ID, session.DefaultAgentID))
}

func sanitizeSandboxScopeKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, c := range value {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			b.WriteRune(c)
		} else {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
