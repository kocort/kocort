// pipeline_tools.go — Stage 5: Filter tools, resolve plugins, build RunContext.
//
// Corresponds to the original Run() lines ~282–383.
// Filters available tools by identity policy, resolves plugin-contributed
// tools, assembles the full AgentRunContext, and applies skill env overrides.
package runtime

import (
	"context"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/internal/skill"
	toolfn "github.com/kocort/kocort/internal/tool"
)

// buildRunContext filters tools, resolves plugin tools, and assembles
// the final AgentRunContext. The result is written to state.AgentRunCtx.
func (p *AgentPipeline) buildRunContext(_ context.Context, state *PipelineState) error {
	r := p.runtime
	req := state.Request
	sess := state.Session
	identity := state.Identity
	selection := state.Selection

	// ---- Delivery target ----
	target := core.DeliveryTarget{
		SessionKey:           sess.SessionKey,
		Channel:              req.Channel,
		To:                   req.To,
		AccountID:            req.AccountID,
		ThreadID:             req.ThreadID,
		RunID:                req.RunID,
		SkipTranscriptMirror: true,
	}
	state.Target = target
	state.Dispatcher = delivery.NewReplyDispatcher(r.Deliverer, target)

	// ---- Filter tools by identity policy ----
	var tools []rtypes.Tool
	if r.Tools != nil {
		policyCtx := rtypes.AgentRunContext{
			Request:  req,
			Session:  sess,
			Identity: identity,
		}
		for _, tool := range r.Tools.List() {
			if tool == nil {
				continue
			}
			if !toolfn.IsToolAllowedByIdentity(identity, policyCtx, r.Tools.Meta(tool.Name()), tool.Name()) {
				continue
			}
			tools = append(tools, tool)
		}
	}

	// ---- Resolve plugin-contributed tools ----
	if r.Plugins != nil {
		pluginTools, err := r.Plugins.ResolveTools(RuntimePluginToolContext{
			Runtime:           r,
			WorkspaceDir:      state.WorkspaceDir,
			Identity:          identity,
			Run:               rtypes.AgentRunContext{Request: req, Session: sess, Identity: identity},
			ExistingToolNames: toolfn.ExistingToolNames(tools),
			ToolAllowlist:     append([]string{}, identity.ToolAllowlist...),
		})
		if err != nil {
			return err
		}
		for _, registered := range pluginTools {
			if !toolfn.IsToolAllowedByIdentity(identity, rtypes.AgentRunContext{Request: req, Session: sess, Identity: identity}, registered.Meta, registered.Tool.Name()) {
				continue
			}
			if r.Tools != nil {
				r.Tools.RegisterWithMeta(registered.Tool, registered.Meta)
			}
			tools = append(tools, registered.Tool)
		}
	}

	// ---- Suppress image tool when local model has native vision ----
	// When the model can process images directly, remove the "image" tool
	// so the model analyzes images inline instead of delegating to a
	// sub-agent (which causes a confusing double-inference loop).
	if r.BrainLocal != nil && r.BrainLocal.HasVision() {
		filtered := make([]rtypes.Tool, 0, len(tools))
		for _, t := range tools {
			if t.Name() == "image" {
				continue
			}
			filtered = append(filtered, t)
		}
		tools = filtered
	}
	state.Tools = tools

	// ---- Assemble AgentRunContext ----
	runCtx := rtypes.AgentRunContext{
		Runtime:         r,
		Request:         req,
		Session:         sess,
		Identity:        identity,
		ModelSelection:  selection,
		Transcript:      state.Transcript,
		Memory:          state.MemoryHits,
		Skills:          state.Skills,
		AvailableTools:  tools,
		SystemPrompt:    infra.BuildSystemPrompt(buildPromptParams(state, req, state.Transcript)),
		WorkspaceDir:    state.WorkspaceDir,
		ReplyDispatcher: state.Dispatcher,
		RunState:        &core.AgentRunState{},
	}
	state.AgentRunCtx = runCtx

	// ---- Apply skill env overrides ----
	state.RestoreSkillEnv = skill.ApplySkillEnvOverridesFromEntries(&r.Config, state.Skills.Skills)

	return nil
}
