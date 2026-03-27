package infra

import (
	"context"
	"strings"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"
)

type StaticIdentityResolver struct {
	identities map[string]core.AgentIdentity
}

func NewStaticIdentityResolver(identities map[string]core.AgentIdentity) *StaticIdentityResolver {
	if identities == nil {
		identities = map[string]core.AgentIdentity{}
	}
	return &StaticIdentityResolver{identities: identities}
}

func (r *StaticIdentityResolver) Resolve(_ context.Context, agentID string) (core.AgentIdentity, error) {
	agentID = session.NormalizeAgentID(agentID)
	identity := core.AgentIdentity{
		ID:              agentID,
		Name:            agentID,
		WorkspaceDir:    ResolveDefaultAgentWorkspaceDir(agentID),
		DefaultProvider: "openai",
		DefaultModel:    "gpt-4.1",
	}
	if configured, ok := r.identities[agentID]; ok {
		identity = configured
		if identity.ID == "" {
			identity.ID = agentID
		}
		if identity.Name == "" {
			identity.Name = agentID
		}
		if strings.TrimSpace(identity.WorkspaceDir) == "" {
			identity.WorkspaceDir = ResolveDefaultAgentWorkspaceDir(agentID)
		}
		identity = ApplyWorkspaceIdentityFile(identity)
		return identity, nil
	}
	identity = ApplyWorkspaceIdentityFile(identity)
	return identity, nil
}

func (r *StaticIdentityResolver) List() []core.AgentIdentity {
	if r == nil || len(r.identities) == 0 {
		return nil
	}
	out := make([]core.AgentIdentity, 0, len(r.identities))
	for _, identity := range r.identities {
		identity = ApplyWorkspaceIdentityFile(identity)
		out = append(out, identity)
	}
	return out
}

type NullMemoryProvider struct{}

func (NullMemoryProvider) Recall(context.Context, core.AgentIdentity, core.SessionResolution, string) ([]core.MemoryHit, error) {
	return nil, nil
}

func ApplyWorkspaceIdentityFile(identity core.AgentIdentity) core.AgentIdentity {
	workspaceDir, err := EnsureWorkspaceDir(identity.WorkspaceDir)
	if err != nil || workspaceDir == "" {
		return identity
	}
	identity.WorkspaceDir = workspaceDir
	content, err := LoadWorkspaceTextFile(workspaceDir, DefaultIdentityFilename)
	if err != nil || strings.TrimSpace(content) == "" {
		return identity
	}
	parsed := ParseIdentityMarkdown(content)
	if parsed.Name != "" && (identity.Name == "" || identity.Name == identity.ID) {
		identity.Name = parsed.Name
	}
	if identity.Emoji == "" && parsed.Emoji != "" {
		identity.Emoji = parsed.Emoji
	}
	if identity.Theme == "" && parsed.Theme != "" {
		identity.Theme = parsed.Theme
	}
	if identity.Avatar == "" && parsed.Avatar != "" {
		identity.Avatar = parsed.Avatar
	}
	if identity.PersonaPrompt == "" {
		var lines []string
		if parsed.Name != "" {
			lines = append(lines, "Identity name: "+parsed.Name)
		}
		if parsed.Emoji != "" {
			lines = append(lines, "Identity emoji: "+parsed.Emoji)
		}
		if parsed.Creature != "" {
			lines = append(lines, "Identity creature: "+parsed.Creature)
		}
		if parsed.Vibe != "" {
			lines = append(lines, "Identity vibe: "+parsed.Vibe)
		}
		if parsed.Theme != "" {
			lines = append(lines, "Identity theme: "+parsed.Theme)
		}
		if len(lines) > 0 {
			identity.PersonaPrompt = strings.Join(lines, "\n")
		}
	}
	return identity
}

type IdentityFile struct {
	Name     string
	Emoji    string
	Theme    string
	Creature string
	Vibe     string
	Avatar   string
}

func ParseIdentityMarkdown(content string) IdentityFile {
	var identity IdentityFile
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		cleaned := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		colonIndex := strings.Index(cleaned, ":")
		if colonIndex == -1 {
			continue
		}
		label := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(cleaned[:colonIndex], "*", "")))
		value := strings.TrimSpace(strings.Trim(cleaned[colonIndex+1:], "*_ "))
		if value == "" {
			continue
		}
		switch label {
		case "name":
			identity.Name = value
		case "emoji":
			identity.Emoji = value
		case "theme":
			identity.Theme = value
		case "creature":
			identity.Creature = value
		case "vibe":
			identity.Vibe = value
		case "avatar":
			identity.Avatar = value
		}
	}
	return identity
}
