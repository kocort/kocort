package runtime

import (
	"context"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
)

func (r *Runtime) AnalyzeImage(ctx context.Context, identity core.AgentIdentity, session core.SessionResolution, req core.AgentRunRequest) (core.AgentRunResult, error) {
	selection, err := r.ResolveModelSelection(ctx, identity, req, session)
	if err != nil {
		return core.AgentRunResult{}, err
	}
	memDeliverer := &delivery.MemoryDeliverer{}
	dispatcher := delivery.NewReplyDispatcher(memDeliverer, core.DeliveryTarget{SessionKey: session.SessionKey})
	runCtx := rtypes.AgentRunContext{
		Runtime:        r,
		Request:        req,
		Session:        session,
		Identity:       identity,
		ModelSelection: selection,
		Transcript:     nil,
		Skills:         nil,
		AvailableTools: nil,
		SystemPrompt: infra.BuildSystemPrompt(infra.PromptBuildParams{
			Identity:       identity,
			Request:        req,
			ModelSelection: selection,
		}),
		WorkspaceDir:    identity.WorkspaceDir,
		ReplyDispatcher: dispatcher,
		RunState:        &core.AgentRunState{},
	}
	result, err := r.Backend.Run(ctx, runCtx)
	dispatcher.MarkComplete()
	_ = dispatcher.WaitForIdle(ctx)
	if len(result.Payloads) == 0 {
		result.Payloads = delivery.VisibleAssistantPayloads(dispatcher)
	}
	return result, err
}
