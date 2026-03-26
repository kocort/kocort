package service

// Context file management service.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kocort/kocort/api/types"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/runtime"
)

var managedContextFileNames = []string{
	"AGENTS.md",
	"IDENTITY.md",
	"MEMORY.md",
	"README.md",
	"CONTEXT.md",
	"SYSTEM.md",
}

// BuildDataState builds the data state response.
func BuildDataState(ctx context.Context, rt *runtime.Runtime) types.DataState {
	agentID := defaultAgentID(rt)
	workspace := resolveAgentWorkspace(rt.Config, agentID)
	if workspace == "" {
		if identity, err := resolveDefaultIdentity(ctx, rt); err == nil {
			workspace = strings.TrimSpace(identity.WorkspaceDir)
		}
	}
	return types.DataState{
		DefaultAgent: agentID,
		Workspace:    workspace,
		SystemPrompt: ResolveDefaultSystemPrompt(rt.Config),
		Files:        loadContextFiles(workspace),
	}
}

// SaveDataState saves data state changes.
func SaveDataState(cfg *config.AppConfig, req types.DataSaveRequest) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	if req.SystemPrompt != nil {
		SetDefaultSystemPrompt(cfg, *req.SystemPrompt)
	}
	if len(req.Files) > 0 {
		workspace := resolveAgentWorkspace(*cfg, config.ResolveDefaultConfiguredAgentID(*cfg))
		if strings.TrimSpace(workspace) == "" {
			return fmt.Errorf("default agent workspace is not configured")
		}
		if err := os.MkdirAll(workspace, 0o755); err != nil {
			return err
		}
		for _, file := range req.Files {
			name := normalizeContextFileName(file.Name)
			if name == "" {
				return fmt.Errorf("unsupported context file %q", file.Name)
			}
			path := filepath.Join(workspace, name)
			content := normalizeContextFileContent(file.Content)
			if content == "" {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					return err
				}
				continue
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

func loadContextFiles(workspace string) []types.ContextFileState {
	out := make([]types.ContextFileState, 0, len(managedContextFileNames))
	for _, name := range managedContextFileNames {
		path := filepath.Join(workspace, name)
		data, err := os.ReadFile(path)
		if err != nil {
			out = append(out, types.ContextFileState{
				Name:   name,
				Path:   path,
				Exists: false,
			})
			continue
		}
		out = append(out, types.ContextFileState{
			Name:    name,
			Path:    path,
			Exists:  true,
			Content: string(data),
		})
	}
	return out
}

func normalizeContextFileName(raw string) string {
	name := strings.TrimSpace(strings.ToUpper(raw))
	for _, candidate := range managedContextFileNames {
		if name == strings.ToUpper(candidate) {
			return candidate
		}
	}
	return ""
}

func normalizeContextFileContent(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	return content + "\n"
}

func defaultAgentID(rt *runtime.Runtime) string {
	if rt == nil {
		return "main"
	}
	return config.ResolveDefaultConfiguredAgentID(rt.Config)
}

// SetDefaultSystemPrompt sets the persona prompt for the default agent.
func SetDefaultSystemPrompt(cfg *config.AppConfig, prompt string) {
	if cfg == nil {
		return
	}
	prompt = strings.TrimSpace(prompt)
	agentID := config.ResolveDefaultConfiguredAgentID(*cfg)
	for i := range cfg.Agents.List {
		if session.NormalizeAgentID(cfg.Agents.List[i].ID) != agentID {
			continue
		}
		if cfg.Agents.List[i].Identity == nil {
			cfg.Agents.List[i].Identity = &config.AgentIdentityConfig{}
		}
		cfg.Agents.List[i].Identity.PersonaPrompt = prompt
		return
	}
	if cfg.Agents.Defaults == nil {
		cfg.Agents.Defaults = &config.AgentDefaultsConfig{}
	}
	if cfg.Agents.Defaults.Identity == nil {
		cfg.Agents.Defaults.Identity = &config.AgentIdentityConfig{}
	}
	cfg.Agents.Defaults.Identity.PersonaPrompt = prompt
}
