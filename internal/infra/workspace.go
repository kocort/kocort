package infra

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/kocort/kocort/internal/config"
	memorypkg "github.com/kocort/kocort/internal/memory"
	"github.com/kocort/kocort/internal/session"
)

const (
	DefaultAgentsFilename   = "AGENTS.md"
	DefaultIdentityFilename = "IDENTITY.md"
	DefaultMemoryFilename   = memorypkg.DefaultMemoryFilename
	DefaultMemoryAltFile    = memorypkg.DefaultMemoryAltFile
)

func ResolveDefaultAgentDir(stateDir string, agentID string) string {
	base := strings.TrimSpace(stateDir)
	if base == "" {
		base = config.ResolveDefaultStateDir()
	}
	return filepath.Join(base, "agents", session.NormalizeAgentID(agentID), "agent")
}

func ResolveDefaultAgentWorkspaceDirForState(stateDir string, agentID string) string {
	base := strings.TrimSpace(stateDir)
	if base == "" {
		base = config.ResolveDefaultStateDir()
	}
	if strings.TrimSpace(base) == "" {
		if session.NormalizeAgentID(agentID) == session.DefaultAgentID {
			return filepath.Join(".kocort", "workspace")
		}
		return filepath.Join(".kocort", "workspace-"+session.NormalizeAgentID(agentID))
	}
	if session.NormalizeAgentID(agentID) == session.DefaultAgentID {
		return filepath.Join(base, "workspace")
	}
	return filepath.Join(base, "workspace-"+session.NormalizeAgentID(agentID))
}

func ResolveDefaultAgentWorkspaceDir(agentID string) string {
	return ResolveDefaultAgentWorkspaceDirForState("", agentID)
}

func EnsureWorkspaceDir(dir string) (string, error) {
	cleaned := strings.TrimSpace(dir)
	if cleaned == "" {
		return "", nil
	}
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", err
	}
	return abs, nil
}

func EnsureAgentDir(dir string) (string, error) {
	cleaned := strings.TrimSpace(dir)
	if cleaned == "" {
		return "", nil
	}
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return "", err
	}
	return abs, nil
}

func LoadWorkspaceTextFile(workspaceDir string, filename string) (string, error) {
	if strings.TrimSpace(workspaceDir) == "" || strings.TrimSpace(filename) == "" {
		return "", nil
	}
	content, err := os.ReadFile(filepath.Join(workspaceDir, filename))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(content), nil
}

func ListWorkspaceMemoryFiles(workspaceDir string) ([]string, error) {
	return memorypkg.ListWorkspaceMemoryFiles(workspaceDir)
}
