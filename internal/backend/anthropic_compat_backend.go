package backend

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/rtypes"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/infra"
	toolfn "github.com/kocort/kocort/internal/tool"

	"github.com/kocort/kocort/utils"
)

type anthropicStreamingRoundResult struct {
	FinalMessage anthropic.Message
	FinalText    string
	ToolUses     []anthropic.ToolUseBlock
	StopReason   string
	ResponseID   string
	Usage        map[string]any
}

type anthropicModelToolLoopState struct {
	request             anthropic.MessageNewParams
	messages            []anthropic.MessageParam
	client              *anthropic.Client
	lastRound           anthropicStreamingRoundResult
	pendingResultBlocks []anthropic.ContentBlockParamUnion
}

// AnthropicCompatBackend implements a backend using the Anthropic-compatible API.
type AnthropicCompatBackend struct {
	Config               config.AppConfig
	Env                  *infra.EnvironmentRuntime
	HTTPClient           *http.Client
	DynamicClient        *infra.DynamicHTTPClient
	NoOutputTimeout      time.Duration
	BlockSendTimeout     time.Duration
	BlockReplyCoalescing *delivery.BlockStreamingCoalescing
	AuthProfiles         *AuthProfileStore
}

// NewAnthropicCompatBackend creates a new Anthropic-compatible backend.
func NewAnthropicCompatBackend(cfg config.AppConfig, env *infra.EnvironmentRuntime, dc *infra.DynamicHTTPClient) *AnthropicCompatBackend {
	return &AnthropicCompatBackend{
		Config:           cfg,
		Env:              env,
		DynamicClient:    dc,
		BlockSendTimeout: 5 * time.Second,
		BlockReplyCoalescing: &delivery.BlockStreamingCoalescing{
			MinChars: 16,
			MaxChars: 160,
			Idle:     750 * time.Millisecond,
			Joiner:   "",
		},
	}
}

func (b *AnthropicCompatBackend) Run(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
	runCtx.Runtime = ensureRuntime(runCtx)
	requestCtx := ctx
	cancelRequest := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && runCtx.Request.Timeout > 0 {
		requestCtx, cancelRequest = context.WithTimeout(ctx, runCtx.Request.Timeout)
	} else {
		requestCtx, cancelRequest = context.WithCancel(ctx)
	}
	defer cancelRequest()

	providerCfg, err := b.resolveProvider(runCtx.ModelSelection.Provider)
	if err != nil {
		return core.AgentRunResult{}, err
	}
	if api := strings.TrimSpace(providerCfg.API); api != "anthropic-messages" {
		return core.AgentRunResult{}, fmt.Errorf("provider %q uses unsupported api %q", runCtx.ModelSelection.Provider, api)
	}
	modelCfg, err := config.ResolveConfiguredModel(b.Config, runCtx.ModelSelection.Provider, runCtx.ModelSelection.Model)
	if err != nil {
		return core.AgentRunResult{}, err
	}
	// Auth profile selection: if profiles are registered for this provider,
	// use SelectProfile to pick the best available API key.
	var selectedProfileID string
	effectiveProviderCfg := providerCfg
	if b.AuthProfiles != nil {
		if profile, isProbe := b.AuthProfiles.SelectProfile(runCtx.ModelSelection.Provider, true); profile != nil {
			effectiveProviderCfg.APIKey = profile.APIKey
			selectedProfileID = profile.ID
			if isProbe {
				b.AuthProfiles.RecordProbe(profile.ID)
			}
		}
	}
	client, err := b.newAnthropicClient(effectiveProviderCfg)
	if err != nil {
		return core.AgentRunResult{}, err
	}
	request := anthropic.MessageNewParams{
		MaxTokens: int64(modelCfg.MaxTokens),
		Model:     anthropic.Model(runCtx.ModelSelection.Model),
		Messages:  BuildAnthropicMessages(runCtx.Transcript, runCtx.Request, runCtx.Identity, runCtx.Session),
		Tools:     buildAnthropicToolDefinitions(runCtx.AvailableTools),
	}
	if providerCfg.HistoryTurnLimit > 0 {
		request.Messages = LimitAnthropicHistoryTurns(request.Messages, providerCfg.HistoryTurnLimit)
	}
	if trimmed := strings.TrimSpace(runCtx.SystemPrompt); trimmed != "" {
		request.System = []anthropic.TextBlockParam{{Text: trimmed}}
	}
	if toolChoice := resolveAnthropicToolChoice(runCtx.AvailableTools); toolChoice != nil {
		request.ToolChoice = *toolChoice
	}
	event.RecordModelEvent(requestCtx, runCtx.Runtime.GetAudit(), nil, runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID, "request_started", "info", "anthropic-compatible request started", map[string]any{
		"provider":     runCtx.ModelSelection.Provider,
		"model":        runCtx.ModelSelection.Model,
		"maxTokens":    modelCfg.MaxTokens,
		"messageCount": len(request.Messages),
		"toolCount":    len(request.Tools),
		"stream":       true,
	})
	result, runErr := b.runStreamingToolLoop(requestCtx, cancelRequest, &client, request, runCtx)
	// Record auth profile outcome for cooldown management.
	if selectedProfileID != "" && b.AuthProfiles != nil {
		if runErr != nil {
			reason := ErrorReason(runErr)
			b.AuthProfiles.RecordFailure(selectedProfileID, reason)
		} else {
			b.AuthProfiles.RecordSuccess(selectedProfileID)
		}
	}
	return result, runErr
}

func (b *AnthropicCompatBackend) runStreamingToolLoop(
	ctx context.Context,
	cancel context.CancelFunc,
	client *anthropic.Client,
	request anthropic.MessageNewParams,
	runCtx rtypes.AgentRunContext,
) (core.AgentRunResult, error) {
	return runStandardModelToolLoop(ctx, cancel, runCtx, StandardModelToolLoopConfig[anthropicModelToolLoopState]{
		InitialState: anthropicModelToolLoopState{
			request:  request,
			messages: append([]anthropic.MessageParam{}, request.Messages...),
			client:   client,
		},
		MaxRounds:    defaultStreamingToolLoopMaxRounds,
		BackendKind:  "embedded",
		ProviderKind: "anthropic-messages",
		ExecuteRound: func(ctx context.Context, cancel context.CancelFunc, state *anthropicModelToolLoopState, runCtx rtypes.AgentRunContext, events *agentEventBuilder) (StandardModelRoundResult, error) {
			current := state.request
			current.Messages = SanitizeAnthropicMessages(append([]anthropic.MessageParam{}, state.messages...))
			roundResult, err := b.runStreamingRound(ctx, cancel, state.client, current, runCtx, events)
			if err != nil {
				return StandardModelRoundResult{}, err
			}
			state.lastRound = roundResult
			return StandardModelRoundResult{
				FinalText:  roundResult.FinalText,
				StopReason: roundResult.StopReason,
				ResponseID: roundResult.ResponseID,
				Usage:      roundResult.Usage,
			}, nil
		},
		NormalizeToolCalls: func(state *anthropicModelToolLoopState, _ StandardModelRoundResult) ([]StandardModelToolCall, error) {
			calls := make([]StandardModelToolCall, 0, len(state.lastRound.ToolUses))
			for _, toolUse := range state.lastRound.ToolUses {
				calls = append(calls, StandardModelToolCall{
					ID:        toolUse.ID,
					Name:      toolUse.Name,
					Arguments: string(toolUse.Input),
				})
			}
			return calls, nil
		},
		AppendAssistantToolCalls: func(state *anthropicModelToolLoopState, _ StandardModelRoundResult) error {
			state.messages = append(state.messages, state.lastRound.FinalMessage.ToParam())
			return nil
		},
		BeforeToolResults: func(state *anthropicModelToolLoopState) error {
			state.pendingResultBlocks = state.pendingResultBlocks[:0]
			return nil
		},
		AppendToolResult: func(state *anthropicModelToolLoopState, call StandardModelToolCall, historyText string, isError bool) error {
			state.pendingResultBlocks = append(state.pendingResultBlocks, anthropic.NewToolResultBlock(call.ID, historyText, isError))
			return nil
		},
		AfterToolResults: func(state *anthropicModelToolLoopState) error {
			state.messages = append(state.messages, anthropic.NewUserMessage(state.pendingResultBlocks...))
			return nil
		},
		IsToolCallStopReason: func(reason string) bool {
			return reason == string(anthropic.StopReasonToolUse)
		},
		MissingToolCallsError: func(_ string) error {
			return fmt.Errorf("provider returned stop_reason=tool_use with no tool uses")
		},
		LoopExceededError: func(maxRounds int) error {
			return fmt.Errorf("tool loop exceeded max rounds (%d)", maxRounds)
		},
		RecordRoundError: func(ctx context.Context, runCtx rtypes.AgentRunContext, err error) {
			event.RecordModelEvent(ctx, runCtx.Runtime.GetAudit(), nil, runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID, "request_failed", "error", "anthropic-compatible request failed", map[string]any{
				"provider": runCtx.ModelSelection.Provider,
				"model":    runCtx.ModelSelection.Model,
				"error":    err.Error(),
			})
		},
		RecordToolRoundComplete: func(ctx context.Context, runCtx rtypes.AgentRunContext, round int, pendingCalls []string, stopReason string) {
			event.RecordModelEvent(ctx, runCtx.Runtime.GetAudit(), nil, runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID, "tool_round_complete", "info", "anthropic-compatible tool round completed", map[string]any{
				"provider":         runCtx.ModelSelection.Provider,
				"model":            runCtx.ModelSelection.Model,
				"round":            round,
				"pendingToolCalls": pendingCalls,
				"stopReason":       stopReason,
			})
		},
	})
}

func (b *AnthropicCompatBackend) runStreamingRound(
	ctx context.Context,
	cancel context.CancelFunc,
	client *anthropic.Client,
	request anthropic.MessageNewParams,
	runCtx rtypes.AgentRunContext,
	events *agentEventBuilder,
) (anthropicStreamingRoundResult, error) {
	stream := client.Messages.NewStreaming(ctx, request)
	defer stream.Close()
	event.RecordModelEvent(ctx, runCtx.Runtime.GetAudit(), nil, runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID, "stream_opened", "info", "anthropic-compatible stream opened", map[string]any{
		"provider": runCtx.ModelSelection.Provider,
		"model":    runCtx.ModelSelection.Model,
	})

	watchdogCtx, stopWatchdog := context.WithCancel(ctx)
	defer stopWatchdog()
	watchdog := NewStreamOutputWatchdog(watchdogCtx, b.resolveNoOutputTimeout(ctx), cancel)
	defer watchdog.Stop()

	pipeline := delivery.NewBlockReplyPipeline(func(_ context.Context, payload core.ReplyPayload) error {
		runCtx.ReplyDispatcher.SendBlockReply(payload)
		return nil
	}, b.resolveBlockSendTimeout(), b.BlockReplyCoalescing, nil)
	defer pipeline.Stop()

	var (
		fullText      strings.Builder
		finalMsg      anthropic.Message
		stopReason    string
		responseID    string
		usage         map[string]any
		hadReasoning  bool // tracks whether we received any reasoning deltas
		reasoningDone bool // true once reasoning_complete has been emitted
	)

	for stream.Next() {
		if ctx.Err() != nil {
			break
		}
		if watchdog.TimedOut() {
			return anthropicStreamingRoundResult{}, &BackendError{
				Reason:  BackendFailureTransientHTTP,
				Message: fmt.Sprintf("provider stream produced no output for %s", b.resolveNoOutputTimeout(ctx).Round(time.Second)),
			}
		}
		watchdog.Touch()
		event := stream.Current()
		if err := (&finalMsg).Accumulate(event); err != nil {
			return anthropicStreamingRoundResult{}, fmt.Errorf("provider stream accumulation failed: %w", err)
		}
		if id := strings.TrimSpace(finalMsg.ID); id != "" {
			responseID = id
		}
		if stop := strings.TrimSpace(string(finalMsg.StopReason)); stop != "" {
			stopReason = stop
		}
		switch variant := event.AsAny().(type) {
		case anthropic.ContentBlockStartEvent:
			switch block := variant.ContentBlock.AsAny().(type) {
			case anthropic.ToolUseBlock:
				// Emit reasoning_complete when transitioning from reasoning to tool calls.
				if hadReasoning && !reasoningDone {
					reasoningDone = true
					events.Add("assistant", map[string]any{
						"type": "reasoning_complete",
					})
				}
				_, _ = fmt.Fprintf(os.Stderr, "\n[tool_call_start] id=%s name=%s\n", strings.TrimSpace(block.ID), strings.TrimSpace(block.Name)) // best-effort debug trace
				events.Add("tool", map[string]any{
					"type":       "tool_call_started",
					"toolCallId": strings.TrimSpace(block.ID),
					"toolName":   strings.TrimSpace(block.Name),
				})
			}
		case anthropic.ContentBlockDeltaEvent:
			switch delta := variant.Delta.AsAny().(type) {
			case anthropic.TextDelta:
				text := delta.Text
				if text != "" {
					// Emit reasoning_complete when transitioning from reasoning to text output.
					if hadReasoning && !reasoningDone {
						reasoningDone = true
						events.Add("assistant", map[string]any{
							"type": "reasoning_complete",
						})
					}
					_, _ = fmt.Fprint(os.Stderr, text) // best-effort debug trace
					fullText.WriteString(text)
					pipeline.Enqueue(core.ReplyPayload{Text: text})
					events.Add("assistant", map[string]any{
						"type": "text_delta",
						"text": text,
					})
				}
			case anthropic.ThinkingDelta:
				if strings.TrimSpace(delta.Thinking) != "" {
					hadReasoning = true
					_, _ = fmt.Fprintf(os.Stderr, "\n[reasoning]%s", delta.Thinking) // best-effort debug trace
					events.Add("assistant", map[string]any{
						"type": "reasoning_delta",
						"text": delta.Thinking,
					})
				}
			case anthropic.InputJSONDelta:
				index := int(variant.Index)
				var toolCallID, toolName, arguments string
				if index >= 0 && index < len(finalMsg.Content) {
					if toolUse, ok := finalMsg.Content[index].AsAny().(anthropic.ToolUseBlock); ok {
						toolCallID = strings.TrimSpace(toolUse.ID)
						toolName = strings.TrimSpace(toolUse.Name)
						arguments = strings.TrimSpace(string(toolUse.Input))
					}
				}
				_, _ = fmt.Fprintf(os.Stderr, "\n[tool_call_delta] index=%d id=%s name=%s args=%s", index, toolCallID, toolName, delta.PartialJSON) // best-effort debug trace
				events.Add("tool", map[string]any{
					"type":       "tool_call_delta",
					"index":      index,
					"toolCallId": toolCallID,
					"toolName":   toolName,
					"arguments":  arguments,
					"partial":    delta.PartialJSON,
				})
			}
		case anthropic.MessageDeltaEvent:
			if reason := strings.TrimSpace(string(variant.Delta.StopReason)); reason != "" {
				stopReason = reason
				_, _ = fmt.Fprintf(os.Stderr, "\n[finish_reason]%s\n", reason) // best-effort debug trace
			}
		}
	}
	if err := stream.Err(); err != nil {
		if watchdog.TimedOut() {
			return anthropicStreamingRoundResult{}, &BackendError{
				Reason:  BackendFailureTransientHTTP,
				Message: fmt.Sprintf("provider stream produced no output for %s", b.resolveNoOutputTimeout(ctx).Round(time.Second)),
			}
		}
		return anthropicStreamingRoundResult{}, fmt.Errorf("provider request failed: %w", err)
	}
	// Context may have been cancelled mid-stream (e.g. ChatCancel); surface
	// that immediately so the caller does not treat partial data as success.
	if ctx.Err() != nil {
		return anthropicStreamingRoundResult{}, fmt.Errorf("provider request cancelled: %w", ctx.Err())
	}
	// Emit reasoning_complete at stream end if reasoning phase never transitioned.
	if hadReasoning && !reasoningDone {
		events.Add("assistant", map[string]any{
			"type": "reasoning_complete",
		})
	}
	if err := pipeline.Flush(true); err != nil {
		return anthropicStreamingRoundResult{}, err
	}
	usage = AnthropicUsageToMap(finalMsg.Usage)
	event.RecordModelEvent(ctx, runCtx.Runtime.GetAudit(), nil, runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID, "response_completed", "info", "anthropic-compatible response completed", map[string]any{
		"responseId":   responseID,
		"stopReason":   stopReason,
		"usage":        usage,
		"toolUseCount": len(ExtractAnthropicToolUses(finalMsg)),
	})
	return anthropicStreamingRoundResult{
		FinalMessage: finalMsg,
		FinalText:    strings.TrimSpace(utils.NonEmpty(strings.TrimSpace(fullText.String()), ExtractAnthropicResponseText(finalMsg))),
		ToolUses:     ExtractAnthropicToolUses(finalMsg),
		StopReason:   stopReason,
		ResponseID:   responseID,
		Usage:        usage,
	}, nil
}

func (b *AnthropicCompatBackend) resolveProvider(provider string) (config.ProviderConfig, error) {
	entry, _, err := ResolveConfiguredProviderWithEnvironment(b.Config, b.Env, provider)
	return entry, err
}

func (b *AnthropicCompatBackend) newAnthropicClient(providerCfg config.ProviderConfig) (anthropic.Client, error) {
	baseURL, err := ResolveAnthropicCompatBaseURL(providerCfg.BaseURL)
	if err != nil {
		return anthropic.Client{}, err
	}
	opts := []option.RequestOption{
		option.WithBaseURL(baseURL),
		option.WithAPIKey(strings.TrimSpace(providerCfg.APIKey)),
	}
	if b.DynamicClient != nil {
		opts = append(opts, option.WithHTTPClient(b.DynamicClient.Client()))
	} else if b.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(b.HTTPClient))
	}
	return anthropic.NewClient(opts...), nil
}

func buildAnthropicToolDefinitions(tools []rtypes.Tool) []anthropic.ToolUnionParam {
	definitions := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		provider, ok := tool.(core.OpenAIFunctionToolProvider)
		if !ok {
			continue
		}
		schema := provider.OpenAIFunctionTool()
		if schema == nil || strings.TrimSpace(schema.Name) == "" {
			continue
		}
		// Normalize schema for Anthropic (flattens anyOf/oneOf unions).
		normalized := toolfn.NormalizeToolParameters(CloneAnyMap(schema.Parameters), toolfn.SchemaProviderAnthropic)
		inputSchema := anthropic.ToolInputSchemaParam{Type: "object"}
		if properties, ok := normalized["properties"].(map[string]any); ok {
			inputSchema.Properties = CloneAnyMap(properties)
		}
		if required, ok := normalized["required"].([]string); ok {
			inputSchema.Required = append([]string{}, required...)
		} else if requiredAny, ok := normalized["required"].([]any); ok {
			req := make([]string, 0, len(requiredAny))
			for _, item := range requiredAny {
				if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
					req = append(req, strings.TrimSpace(text))
				}
			}
			inputSchema.Required = req
		}
		var toolParam anthropic.ToolParam
		toolParam.Name = strings.TrimSpace(schema.Name)
		toolParam.Description = param.NewOpt(strings.TrimSpace(schema.Description))
		toolParam.InputSchema = inputSchema
		toolParam.Type = anthropic.ToolTypeCustom
		definitions = append(definitions, anthropic.ToolUnionParam{OfTool: &toolParam})
	}
	return definitions
}

func resolveAnthropicToolChoice(tools []rtypes.Tool) *anthropic.ToolChoiceUnionParam {
	if len(buildAnthropicToolDefinitions(tools)) == 0 {
		return nil
	}
	auto := anthropic.ToolChoiceAutoParam{Type: "auto"}
	return &anthropic.ToolChoiceUnionParam{OfAuto: &auto}
}

func (b *AnthropicCompatBackend) resolveNoOutputTimeout(ctx context.Context) time.Duration {
	if b.NoOutputTimeout > 0 {
		return b.NoOutputTimeout
	}
	const (
		minTimeout = 180 * time.Second
		maxTimeout = 600 * time.Second
		ratio      = 0.8
	)
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return minTimeout
		}
		capTimeout := remaining - time.Second
		if capTimeout <= 0 {
			return remaining
		}
		computed := time.Duration(float64(remaining) * ratio)
		if computed < minTimeout {
			computed = minTimeout
		}
		if computed > maxTimeout {
			computed = maxTimeout
		}
		if computed > capTimeout {
			computed = capTimeout
		}
		return computed
	}
	return 180 * time.Second
}

func (b *AnthropicCompatBackend) resolveBlockSendTimeout() time.Duration {
	if b.BlockSendTimeout > 0 {
		return b.BlockSendTimeout
	}
	return 5 * time.Second
}
