package runtime

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/infra"
)

func buildPromptParams(state *PipelineState, req core.AgentRunRequest, history []core.TranscriptMessage) infra.PromptBuildParams {
	mode := infra.PromptModeFull
	if req.Lane == core.LaneSubagent || req.IsHeartbeat {
		mode = infra.PromptModeMinimal
	}
	heartbeatEnabled := strings.TrimSpace(state.Identity.HeartbeatEvery) != ""

	// Reasoning hint: when thinking is requested but the model doesn't
	// natively handle it through the API, we inject tag instructions.
	reasoningHint := resolveReasoningHint(state.Selection.ThinkLevel)

	// Context budget: derive from the selected model's context window and
	// output token limits. Falls back to sensible defaults when unset.
	contextBudget := infra.NewContextBudget(
		state.Selection.ContextWindow,
		state.Selection.MaxOutputTokens,
	)

	return infra.PromptBuildParams{
		InternalEvents:              state.InternalEvents,
		ContextFiles:                state.ContextFiles,
		BootstrapWarnings:           state.BootstrapWarnings,
		Identity:                    state.Identity,
		Request:                     req,
		ModelSelection:              state.Selection,
		History:                     history,
		MemoryHits:                  state.MemoryHits,
		Tools:                       toPromptTools(state.Tools),
		Skills:                      state.Skills,
		DocsPath:                    resolvePromptDocsPath(),
		Sandbox:                     buildPromptSandboxInfo(state.Identity, state.WorkspaceDir),
		Mode:                        mode,
		HeartbeatEnabled:            heartbeatEnabled,
		ModelAliases:                state.Identity.ModelAliases,
		OwnerLine:                   state.Identity.OwnerLine,
		ReasoningHint:               reasoningHint,
		OmitUserMessageInSystemPrompt: true,
		ContextBudget:               contextBudget,
	}
}

// resolveReasoningHint returns reasoning-format instructions when the model
// needs explicit <think> tag guidance. Returns "" when reasoning is off or
// the model handles reasoning natively through its API.
func resolveReasoningHint(thinkLevel string) string {
	level := strings.TrimSpace(strings.ToLower(thinkLevel))
	if level == "" || level == "off" {
		return ""
	}
	return "ALL internal reasoning MUST be inside <think>...</think> tags.\n" +
		"Content outside these tags is visible to the user."
}

func buildPromptSandboxInfo(identity core.AgentIdentity, workspaceDir string) infra.PromptSandboxInfo {
	mode := strings.TrimSpace(strings.ToLower(identity.SandboxMode))
	if mode == "" || mode == "off" {
		return infra.PromptSandboxInfo{}
	}
	return infra.PromptSandboxInfo{
		Enabled:         true,
		Mode:            mode,
		WorkspaceAccess: strings.TrimSpace(identity.SandboxWorkspaceAccess),
		Scope:           strings.TrimSpace(identity.SandboxScope),
		WorkspaceRoot:   strings.TrimSpace(identity.SandboxWorkspaceRoot),
		WorkspaceDir:    strings.TrimSpace(workspaceDir),
		AgentWorkspace:  strings.TrimSpace(identity.WorkspaceDir),
	}
}

func resolvePromptDocsPath() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	candidate := filepath.Join(cwd, "docs")
	info, statErr := os.Stat(candidate)
	if statErr != nil || !info.IsDir() {
		return ""
	}
	return "docs"
}
