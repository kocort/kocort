// pipeline_context.go — Stage 4: Load context (workspace, transcript,
// skills, memory, model selection).
//
// Corresponds to the original Run() lines ~196–280.
// Resolves workspace/agent directories, loads the transcript history,
// builds the skill snapshot, recalls memory, and resolves the model
// selection (including stored-override recovery).
package runtime

import (
	"context"
	"strings"

	"github.com/kocort/kocort/internal/backend"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/infra"
	memorypkg "github.com/kocort/kocort/internal/memory"
	sessionpkg "github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/internal/skill"
)

// loadContext resolves workspace directories, loads chat history, skill
// snapshots, memory hits, and model selection. Results are written to
// the PipelineState for use by subsequent stages.
func (p *AgentPipeline) loadContext(ctx context.Context, state *PipelineState) error {
	r := p.runtime
	req := &state.Request
	identity := &state.Identity
	sess := state.Session

	// ---- Resolve workspace directory ----
	// WorkspaceDir is now the "agent directory" where context files live
	// (SYSTEM.md, IDENTITY.md, etc.). It is distinct from the sandbox
	// directory which constrains tool file operations.
	workspaceDir := identity.WorkspaceDir
	defaultWorkspaceDir := infra.ResolveDefaultAgentWorkspaceDir(identity.ID)
	if strings.TrimSpace(workspaceDir) == "" || workspaceDir == defaultWorkspaceDir {
		workspaceDir = infra.ResolveDefaultAgentWorkspaceDirForState(r.Sessions.BaseDir(), identity.ID)
	}
	if req.WorkspaceOverride != "" && req.SpawnedBy != "" {
		workspaceDir = req.WorkspaceOverride
	} else if workspaceDir == "" && req.SpawnedBy != "" {
		requesterIdentity, requesterErr := r.Identities.Resolve(ctx, sessionpkg.ResolveAgentIDFromSessionKey(req.SpawnedBy))
		if requesterErr == nil && strings.TrimSpace(requesterIdentity.WorkspaceDir) != "" {
			workspaceDir = requesterIdentity.WorkspaceDir
			requesterDefaultWorkspaceDir := infra.ResolveDefaultAgentWorkspaceDir(requesterIdentity.ID)
			if workspaceDir == requesterDefaultWorkspaceDir {
				workspaceDir = infra.ResolveDefaultAgentWorkspaceDirForState(r.Sessions.BaseDir(), requesterIdentity.ID)
			}
		}
	}
	var err error
	workspaceDir, err = infra.EnsureWorkspaceDir(workspaceDir)
	if err != nil {
		return err
	}
	identity.WorkspaceDir = workspaceDir
	state.WorkspaceDir = workspaceDir

	// ---- Resolve sandbox directories ----
	// The sandbox directories constrain where file/exec tools can operate.
	// If sandbox is not enabled or no dirs are configured, tools are unrestricted.
	sandboxDirs := identity.SandboxDirs
	for i, dir := range sandboxDirs {
		dir = strings.TrimSpace(dir)
		if dir != "" {
			resolved, ensureErr := infra.EnsureWorkspaceDir(dir)
			if ensureErr != nil {
				return ensureErr
			}
			sandboxDirs[i] = resolved
		}
	}
	identity.SandboxDirs = sandboxDirs

	// ---- Resolve agent directory ----
	agentDir := strings.TrimSpace(identity.AgentDir)
	if agentDir == "" {
		agentDir = infra.ResolveDefaultAgentDir(r.Sessions.BaseDir(), identity.ID)
	}
	agentDir, err = infra.EnsureAgentDir(agentDir)
	if err != nil {
		return err
	}
	identity.AgentDir = agentDir
	state.AgentDir = agentDir

	// ---- Load transcript history ----
	history, err := r.Sessions.LoadTranscript(sess.SessionKey)
	if err != nil {
		return err
	}
	history = backend.SanitizeTranscriptForOpenAI(history)
	state.Transcript = history

	// ---- Collect internal events ----
	internalEvents := append([]core.TranscriptMessage{}, infra.SelectInternalPromptEvents(history)...)
	if len(req.InternalEvents) > 0 {
		internalEvents = append(internalEvents, req.InternalEvents...)
	}
	state.InternalEvents = internalEvents

	// ---- Build skill snapshot ----
	skillsSnapshot, err := skill.BuildWorkspaceSkillSnapshot(workspaceDir, identity.SkillFilter, &r.Config)
	if err != nil {
		return err
	}
	state.Skills = skillsSnapshot

	// ---- Load context files ----
	contextFiles, bootstrapWarnings := memorypkg.LoadPromptContextFiles(workspaceDir, req.ChatType, req.IsHeartbeat)
	state.ContextFiles = contextFiles
	state.BootstrapWarnings = bootstrapWarnings

	if preparer, ok := r.Memory.(memorypkg.Preparer); ok {
		if err := preparer.EnsurePrepared(ctx, *identity, sess); err != nil {
			return err
		}
	}

	// ---- Recall memory ----
	memoryHits, err := r.Memory.Recall(ctx, *identity, sess, req.Message)
	if err != nil {
		return err
	}
	state.MemoryHits = memoryHits

	// ---- Resolve model selection ----
	// In pure-local brain mode the agent runs on the local GGUF model.
	// Skip cloud model resolution and set a placeholder selection so the
	// pipeline routes to the LocalModelBackend.
	if r.Config.BrainLocalEnabled() {
		state.Selection = core.ModelSelection{
			Provider: "local",
			Model:    "local",
		}
		return nil
	}

	selection, err := backend.ResolveModelSelection(ctx, *identity, *req, sess)
	if err != nil {
		return err
	}
	if strings.TrimSpace(selection.Provider) == "" || strings.TrimSpace(selection.Model) == "" {
		return core.ErrNoDefaultModelConfigured
	}

	// Validate the selected model is configured.
	if _, modelErr := config.ResolveConfiguredModel(r.Config, selection.Provider, selection.Model); modelErr != nil {
		_, providerConfigured := r.Config.Models.Providers[backend.NormalizeProviderID(selection.Provider)]
		// Allow unconfigured providers when a default backend is set.
		if !(r.Backend != nil && r.Backends == nil && !providerConfigured) {
			hasExplicitSessionOverride := strings.TrimSpace(req.SessionModelOverride) != ""
			hasStoredSessionOverride := sess.Entry != nil && strings.TrimSpace(sess.Entry.ModelOverride) != ""
			if !hasExplicitSessionOverride && selection.StoredOverride && hasStoredSessionOverride {
				// Clear stale stored override and retry.
				cleared := *sess.Entry
				cleared.ProviderOverride = ""
				cleared.ModelOverride = ""
				if upsertErr := r.Sessions.Upsert(sess.SessionKey, cleared); upsertErr != nil {
					return upsertErr
				}
				sess.Entry = &cleared
				state.Session = sess
				selection, err = backend.ResolveModelSelection(ctx, *identity, *req, sess)
				if err != nil {
					return err
				}
			} else {
				return modelErr
			}
		}
	}
	// Enrich selection with model-level token limits so downstream budget
	// calculations can adapt to the selected model's context window.
	if mcfg, resolveErr := config.ResolveConfiguredModel(r.Config, selection.Provider, selection.Model); resolveErr == nil {
		selection.ContextWindow = mcfg.ContextWindow
		selection.MaxOutputTokens = mcfg.MaxTokens
	}

	state.Selection = selection

	return nil
}
