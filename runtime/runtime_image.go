package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/localmodel/llamawrapper"
	"github.com/kocort/kocort/internal/rtypes"
)

func (r *Runtime) AnalyzeImage(ctx context.Context, identity core.AgentIdentity, session core.SessionResolution, req core.AgentRunRequest) (core.AgentRunResult, error) {
	selection, err := r.ResolveModelSelection(ctx, identity, req, session)
	if err != nil {
		return core.AgentRunResult{}, err
	}

	// When the resolved provider is "local" and the local model has vision
	// support, call it directly with a minimal prompt and no tools.
	// This avoids the heavy system prompt and tool definitions that confuse
	// small vision models.
	if selection.Provider == "local" && r.BrainLocal != nil && r.BrainLocal.HasVision() && r.BrainLocal.Status() == "running" {
		return r.analyzeImageLocal(ctx, req)
	}

	// Route through the normal backend (cloud or non-vision local).
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
			Mode:           infra.PromptModeNone,
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

// analyzeImageLocal calls BrainLocal.CreateChatCompletionStream directly,
// bypassing the full agent pipeline (system prompt, tools, tool loop).
// This produces a clean vision-only inference with no tool interference.
func (r *Runtime) analyzeImageLocal(ctx context.Context, req core.AgentRunRequest) (core.AgentRunResult, error) {
	slog.Info("[runtime] analyzeImageLocal: routing image analysis to local vision model",
		"model", r.BrainLocal.ModelID(),
		"attachments", len(req.Attachments))

	// Build multipart user message with image + text prompt.
	parts := make([]any, 0, 2)
	for i, att := range req.Attachments {
		slog.Info("[runtime] analyzeImageLocal: attachment",
			"index", i, "name", att.Name, "mime", att.MIMEType,
			"contentLen", len(att.Content), "isImage", infra.AttachmentIsImage(att))
		if !infra.AttachmentIsImage(att) {
			continue
		}
		dataURL := infra.AttachmentDataURL(att)
		if dataURL == "" {
			slog.Warn("[runtime] analyzeImageLocal: empty data URL for attachment", "name", att.Name)
			continue
		}
		slog.Info("[runtime] analyzeImageLocal: built data URL",
			"urlLen", len(dataURL), "prefix", dataURL[:min(50, len(dataURL))])
		parts = append(parts, map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": dataURL},
		})
	}
	if len(parts) == 0 {
		return core.AgentRunResult{}, fmt.Errorf("no image attachments found in request")
	}

	prompt := strings.TrimSpace(req.Message)
	if prompt == "" {
		prompt = "Describe the image."
	}
	parts = append(parts, map[string]any{
		"type": "text",
		"text": prompt,
	})

	messages := []llamawrapper.ChatMessage{
		{Role: "user", Content: parts},
	}

	maxTokens := 512
	stream := true
	llamaReq := llamawrapper.ChatCompletionRequest{
		Model:     r.BrainLocal.ModelID(),
		Messages:  messages,
		Stream:    stream,
		MaxTokens: &maxTokens,
		// No Tools — critical for small vision models.
	}

	ch, err := r.BrainLocal.CreateChatCompletionStream(ctx, llamaReq)
	if err != nil {
		return core.AgentRunResult{}, fmt.Errorf("local vision inference failed: %w", err)
	}

	// Drain the streaming response.
	var sb strings.Builder
	for chunk := range ch {
		for _, choice := range chunk.Choices {
			sb.WriteString(choice.Delta.Content)
		}
	}

	reply := strings.TrimSpace(sb.String())
	slog.Info("[runtime] analyzeImageLocal: inference complete",
		"replyLen", len(reply))

	return core.AgentRunResult{
		RunID: req.RunID,
		Payloads: []core.ReplyPayload{
			{Text: reply},
		},
	}, nil
}
