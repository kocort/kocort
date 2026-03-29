package runtime

import (
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
	reasoningHint := infra.ResolveReasoningHint(state.Selection.ThinkLevel)

	// Context budget: derive from the selected model's context window and
	// output token limits. Falls back to sensible defaults when unset.
	contextBudget := infra.NewContextBudget(
		state.Selection.ContextWindow,
		state.Selection.MaxOutputTokens,
	)

	return infra.PromptBuildParams{
		InternalEvents:                state.InternalEvents,
		ContextFiles:                  state.ContextFiles,
		BootstrapWarnings:             state.BootstrapWarnings,
		Identity:                      state.Identity,
		Request:                       req,
		ModelSelection:                state.Selection,
		History:                       history,
		MemoryHits:                    state.MemoryHits,
		Tools:                         infra.ToPromptTools(state.Tools),
		Skills:                        state.Skills,
		DocsPath:                      infra.ResolvePromptDocsPath(),
		Sandbox:                       infra.BuildPromptSandboxInfo(state.Identity, state.WorkspaceDir),
		Mode:                          mode,
		HeartbeatEnabled:              heartbeatEnabled,
		ModelAliases:                  state.Identity.ModelAliases,
		OwnerLine:                     state.Identity.OwnerLine,
		ReasoningHint:                 reasoningHint,
		OmitUserMessageInSystemPrompt: true,
		ContextBudget:                 contextBudget,
	}
}
