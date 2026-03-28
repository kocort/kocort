package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/rtypes"

	openai "github.com/sashabaranov/go-openai"

	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/infra"
	toolfn "github.com/kocort/kocort/internal/tool"
)

type agentEventBuilder struct {
	runID      string
	sessionKey string
	seq        int
	events     []core.AgentEvent
	emit       func(core.AgentEvent)
}

func newAgentEventBuilder(runCtx rtypes.AgentRunContext) *agentEventBuilder {
	bus := runCtx.Runtime.GetEventBus()
	return &agentEventBuilder{
		runID:      strings.TrimSpace(runCtx.Request.RunID),
		sessionKey: strings.TrimSpace(runCtx.Session.SessionKey),
		emit:       func(ev core.AgentEvent) { event.EmitAgentEvent(bus, ev) },
	}
}

func (b *agentEventBuilder) Add(stream string, data map[string]any) {
	if b == nil {
		return
	}
	b.seq++
	event := core.AgentEvent{
		RunID:      b.runID,
		Seq:        b.seq,
		Stream:     strings.TrimSpace(stream),
		OccurredAt: time.Now().UTC(),
		SessionKey: b.sessionKey,
		Data:       CloneAnyMap(data),
	}
	b.events = append(b.events, event)
	if b.emit != nil {
		b.emit(event)
	}
}

func (b *agentEventBuilder) Events() []core.AgentEvent {
	if b == nil || len(b.events) == 0 {
		return nil
	}
	return append([]core.AgentEvent{}, b.events...)
}

type openAIChatMessage = openai.ChatCompletionMessage
type openAIChatMessagePart = openai.ChatMessagePart
type openAIToolCallWire = openai.ToolCall
type openAIToolDefinition = openai.Tool
type openAIChatResponse = openai.ChatCompletionResponse

type openAIStreamingRoundResult struct {
	FinalText    string
	ToolCalls    []openAIToolCallWire
	FinishReason string
	ResponseID   string
	Usage        map[string]any
}

type openAIModelToolLoopState struct {
	request            openai.ChatCompletionRequest
	messages           []openAIChatMessage
	policy             TranscriptPolicy
	allowedNames       map[string]bool
	adapters           StreamChunkAdapter
	client             *openai.Client
	rawToolCalls       []openAIToolCallWire
	validatedToolCalls []openAIToolCallWire
}

// OpenAICompatBackend implements a backend using the OpenAI-compatible API.
type OpenAICompatBackend struct {
	Config               config.AppConfig
	Env                  *infra.EnvironmentRuntime
	HTTPClient           *http.Client
	DynamicClient        *infra.DynamicHTTPClient
	NoOutputTimeout      time.Duration
	BlockSendTimeout     time.Duration
	BlockReplyCoalescing *delivery.BlockStreamingCoalescing
	AuthProfiles         *AuthProfileStore
}

// Original Kocort does not rely on a tiny fixed tool-round cap in the main
// embedded/client-tool path; the practical guard is loop detection plus the
// run-level timeout/watchdog. Keep a high circuit-breaker-style fallback here
// instead of an aggressive low cap.
const defaultStreamingToolLoopMaxRounds = 30

// NewOpenAICompatBackend creates a new OpenAI-compatible backend.
func NewOpenAICompatBackend(cfg config.AppConfig, env *infra.EnvironmentRuntime, dc *infra.DynamicHTTPClient) *OpenAICompatBackend {
	return &OpenAICompatBackend{
		Config:           cfg,
		Env:              env,
		BlockSendTimeout: 5 * time.Second,
		BlockReplyCoalescing: &delivery.BlockStreamingCoalescing{
			MinChars: 16,
			MaxChars: 160,
			Idle:     750 * time.Millisecond,
			Joiner:   "",
		},
		DynamicClient: dc,
	}
}

func (b *OpenAICompatBackend) Run(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
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
	if api := strings.TrimSpace(providerCfg.API); api != "" && api != "openai-completions" {
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
	client, err := b.newOpenAICompatClient(effectiveProviderCfg)
	if err != nil {
		return core.AgentRunResult{}, err
	}
	policy := ResolveTranscriptPolicy(
		runCtx.ModelSelection.Provider,
		strings.TrimSpace(providerCfg.API),
		runCtx.ModelSelection.Model,
	)
	if providerCfg.HistoryTurnLimit > 0 {
		policy.HistoryTurnLimit = providerCfg.HistoryTurnLimit
	}
	allowedNames := collectAllowedToolNamesFromRtypes(runCtx.AvailableTools)
	initialMessages := SanitizeHistoryPipeline(
		BuildOpenAICompatMessages(runCtx),
		policy,
		allowedNames,
	)
	request := openai.ChatCompletionRequest{
		Model:         runCtx.ModelSelection.Model,
		Messages:      initialMessages,
		MaxTokens:     modelCfg.MaxTokens,
		Tools:         BuildOpenAICompatToolDefinitions(runCtx.AvailableTools, ResolveSchemaProvider(runCtx.ModelSelection.Provider, strings.TrimSpace(providerCfg.API))),
		ToolChoice:    resolveOpenAICompatToolChoice(runCtx.AvailableTools),
		StreamOptions: &openai.StreamOptions{IncludeUsage: true},
	}
	event.RecordModelEvent(requestCtx, runCtx.Runtime.GetAudit(), nil, runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID, "request_started", "info", "openai-compatible request started", map[string]any{
		"provider":     runCtx.ModelSelection.Provider,
		"model":        runCtx.ModelSelection.Model,
		"maxTokens":    modelCfg.MaxTokens,
		"messageCount": len(request.Messages),
		"toolCount":    len(request.Tools),
		"toolChoice":   request.ToolChoice,
		"stream":       true,
	})
	result, runErr := b.runStreamingToolLoop(requestCtx, cancelRequest, client, request, runCtx, policy)
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

func (b *OpenAICompatBackend) runStreamingToolLoop(
	ctx context.Context,
	cancel context.CancelFunc,
	client *openai.Client,
	request openai.ChatCompletionRequest,
	runCtx rtypes.AgentRunContext,
	policy TranscriptPolicy,
) (core.AgentRunResult, error) {
	return runStandardModelToolLoop(ctx, cancel, runCtx, StandardModelToolLoopConfig[openAIModelToolLoopState]{
		InitialState: openAIModelToolLoopState{
			request:      request,
			messages:     append([]openAIChatMessage{}, request.Messages...),
			policy:       policy,
			allowedNames: collectAllowedToolNamesFromRtypes(runCtx.AvailableTools),
			adapters:     ResolveStreamAdapters(policy),
			client:       client,
		},
		MaxRounds:    defaultStreamingToolLoopMaxRounds,
		BackendKind:  "embedded",
		ProviderKind: "openai-completions",
		ExecuteRound: func(ctx context.Context, cancel context.CancelFunc, state *openAIModelToolLoopState, runCtx rtypes.AgentRunContext, events *agentEventBuilder) (StandardModelRoundResult, error) {
			current := state.request
			current.Messages = SanitizeHistoryPipeline(append([]openAIChatMessage{}, state.messages...), state.policy, state.allowedNames)
			roundResult, err := b.runStreamingRound(ctx, cancel, state.client, current, runCtx, events, state.adapters)
			if err != nil {
				return StandardModelRoundResult{}, err
			}
			state.rawToolCalls = roundResult.ToolCalls
			state.validatedToolCalls = nil
			return StandardModelRoundResult{
				FinalText:  roundResult.FinalText,
				StopReason: roundResult.FinishReason,
				ResponseID: roundResult.ResponseID,
				Usage:      roundResult.Usage,
			}, nil
		},
		NormalizeToolCalls: func(state *openAIModelToolLoopState, _ StandardModelRoundResult) ([]StandardModelToolCall, error) {
			validatedCalls, err := ValidateOpenAICompatToolCalls(state.rawToolCalls)
			if err != nil {
				return nil, err
			}
			state.validatedToolCalls = validatedCalls
			calls := make([]StandardModelToolCall, 0, len(validatedCalls))
			for _, call := range validatedCalls {
				calls = append(calls, StandardModelToolCall{
					ID:        call.ID,
					Name:      call.Function.Name,
					Arguments: strings.TrimSpace(call.Function.Arguments),
				})
			}
			return calls, nil
		},
		AppendAssistantToolCalls: func(state *openAIModelToolLoopState, round StandardModelRoundResult) error {
			state.messages = append(state.messages, openAIChatMessage{
				Role:      openai.ChatMessageRoleAssistant,
				Content:   strings.TrimSpace(round.FinalText),
				ToolCalls: state.validatedToolCalls,
			})
			return nil
		},
		AppendToolResult: func(state *openAIModelToolLoopState, call StandardModelToolCall, historyText string, _ bool) error {
			state.messages = append(state.messages, openAIChatMessage{
				Role:       openai.ChatMessageRoleTool,
				ToolCallID: call.ID,
				Name:       call.Name,
				Content:    historyText,
			})
			return nil
		},
		IsToolCallStopReason: func(reason string) bool {
			return reason == string(openai.FinishReasonToolCalls)
		},
		MissingToolCallsError: func(_ string) error {
			return fmt.Errorf("provider returned finish_reason=tool_calls with no tool calls")
		},
		LoopExceededError: func(maxRounds int) error {
			return fmt.Errorf("tool loop exceeded max rounds (%d)", maxRounds)
		},
		RecordRoundError: func(ctx context.Context, runCtx rtypes.AgentRunContext, err error) {
			event.RecordModelEvent(ctx, runCtx.Runtime.GetAudit(), nil, runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID, "request_failed", "error", "openai-compatible request failed", map[string]any{
				"provider": runCtx.ModelSelection.Provider,
				"model":    runCtx.ModelSelection.Model,
				"error":    err.Error(),
			})
		},
		RecordToolRoundComplete: func(ctx context.Context, runCtx rtypes.AgentRunContext, round int, pendingCalls []string, stopReason string) {
			event.RecordModelEvent(ctx, runCtx.Runtime.GetAudit(), nil, runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID, "tool_round_complete", "info", "openai-compatible tool round completed", map[string]any{
				"provider":         runCtx.ModelSelection.Provider,
				"model":            runCtx.ModelSelection.Model,
				"round":            round,
				"pendingToolCalls": pendingCalls,
				"stopReason":       stopReason,
			})
		},
	})
}

func (b *OpenAICompatBackend) runStreamingRound(
	ctx context.Context,
	cancel context.CancelFunc,
	client *openai.Client,
	request openai.ChatCompletionRequest,
	runCtx rtypes.AgentRunContext,
	events *agentEventBuilder,
	adapters StreamChunkAdapter,
) (openAIStreamingRoundResult, error) {
	stream, err := client.CreateChatCompletionStream(ctx, request)
	if err != nil {
		event.RecordModelEvent(ctx, runCtx.Runtime.GetAudit(), nil, runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID, "stream_open_failed", "error", "openai-compatible stream open failed", map[string]any{
			"provider": runCtx.ModelSelection.Provider,
			"model":    runCtx.ModelSelection.Model,
			"error":    err.Error(),
		})
		return openAIStreamingRoundResult{}, fmt.Errorf("provider request failed: %w", err)
	}
	defer stream.Close()
	event.RecordModelEvent(ctx, runCtx.Runtime.GetAudit(), nil, runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID, "stream_opened", "info", "openai-compatible stream opened", map[string]any{
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
		responseID    string
		finish        string
		usage         map[string]any
		accumulators  []*openAIToolCallWire
		hadReasoning  bool // tracks whether we received any reasoning deltas
		reasoningDone bool // true once reasoning_complete has been emitted
	)

	for {
		if ctx.Err() != nil {
			return openAIStreamingRoundResult{}, fmt.Errorf("provider request cancelled: %w", ctx.Err())
		}
		if watchdog.TimedOut() {
			return openAIStreamingRoundResult{}, &BackendError{
				Reason:  BackendFailureTransientHTTP,
				Message: fmt.Sprintf("provider stream produced no output for %s", b.resolveNoOutputTimeout(ctx).Round(time.Second)),
			}
		}
		chunk, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			if watchdog.TimedOut() {
				return openAIStreamingRoundResult{}, &BackendError{
					Reason:  BackendFailureTransientHTTP,
					Message: fmt.Sprintf("provider stream produced no output for %s", b.resolveNoOutputTimeout(ctx).Round(time.Second)),
				}
			}
			return openAIStreamingRoundResult{}, fmt.Errorf("provider request failed: %w", err)
		}
		watchdog.Touch()
		if id := strings.TrimSpace(chunk.ID); id != "" {
			responseID = id
		}
		if chunk.Usage != nil {
			usage = UsageToMap(*chunk.Usage)
		}
		for ci := range chunk.Choices {
			choice := &chunk.Choices[ci]
			if adapters != nil {
				adapters.ProcessChoice(choice)
			}
			if text := choice.Delta.Content; text != "" {
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
			if reasoning := choice.Delta.ReasoningContent; reasoning != "" {
				hadReasoning = true
				_, _ = fmt.Fprintf(os.Stderr, "\n[reasoning]%s", reasoning) // best-effort debug trace
				events.Add("assistant", map[string]any{
					"type": "reasoning_delta",
					"text": reasoning,
				})
			}
			if len(choice.Delta.ToolCalls) > 0 {
				// Emit reasoning_complete when transitioning from reasoning to tool calls.
				if hadReasoning && !reasoningDone {
					reasoningDone = true
					events.Add("assistant", map[string]any{
						"type": "reasoning_complete",
					})
				}
				for _, toolCall := range choice.Delta.ToolCalls {
					_, _ = fmt.Fprintf( // best-effort debug trace
						os.Stderr,
						"\n[tool_call_delta] index=%v id=%s name=%s args=%s",
						toolCall.Index,
						strings.TrimSpace(toolCall.ID),
						strings.TrimSpace(toolCall.Function.Name),
						toolCall.Function.Arguments,
					)
				}
				accumulators = AccumulateOpenAIToolCalls(accumulators, choice.Delta.ToolCalls)
			}
			if reason := strings.TrimSpace(string(choice.FinishReason)); reason != "" {
				_, _ = fmt.Fprintf(os.Stderr, "\n[finish_reason]%s\n", reason) // best-effort debug trace
				finish = reason
			}
		}
	}
	// Emit reasoning_complete at stream end if reasoning phase never transitioned.
	if hadReasoning && !reasoningDone {
		events.Add("assistant", map[string]any{
			"type": "reasoning_complete",
		})
	}
	if err := pipeline.Flush(true); err != nil {
		return openAIStreamingRoundResult{}, err
	}
	event.RecordModelEvent(ctx, runCtx.Runtime.GetAudit(), nil, runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID, "response_completed", "info", "openai-compatible response completed", map[string]any{
		"responseId":    responseID,
		"finishReason":  finish,
		"usage":         usage,
		"toolCallCount": len(CompactOpenAIToolCalls(accumulators)),
	})
	compacted := CompactOpenAIToolCalls(accumulators)
	if adapters != nil {
		compacted = adapters.FinalizeToolCalls(compacted)
	}
	return openAIStreamingRoundResult{
		FinalText:    strings.TrimSpace(fullText.String()),
		ToolCalls:    compacted,
		FinishReason: finish,
		ResponseID:   responseID,
		Usage:        usage,
	}, nil
}

func (b *OpenAICompatBackend) resolveProvider(provider string) (config.ProviderConfig, error) {
	entry, _, err := ResolveConfiguredProviderWithEnvironment(b.Config, b.Env, provider)
	return entry, err
}

func (b *OpenAICompatBackend) newOpenAICompatClient(providerCfg config.ProviderConfig) (*openai.Client, error) {
	baseURL, err := ResolveOpenAICompatBaseURL(providerCfg.BaseURL)
	if err != nil {
		return nil, err
	}
	cfg := openai.DefaultConfig(strings.TrimSpace(providerCfg.APIKey))
	cfg.BaseURL = baseURL
	if b.DynamicClient != nil {
		cfg.HTTPClient = b.DynamicClient.Client()
	} else if b.HTTPClient != nil {
		cfg.HTTPClient = b.HTTPClient
	}
	return openai.NewClientWithConfig(cfg), nil
}

func BuildOpenAICompatMessages(runCtx rtypes.AgentRunContext) []openAIChatMessage {
	messages := make([]openAIChatMessage, 0, len(runCtx.Transcript)+2)
	if trimmed := strings.TrimSpace(runCtx.SystemPrompt); trimmed != "" {
		messages = append(messages, openAIChatMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: trimmed,
		})
	}
	for _, message := range runCtx.Transcript {
		switch strings.TrimSpace(strings.ToLower(message.Type)) {
		case "tool_call":
			callName := strings.TrimSpace(message.ToolName)
			callID := strings.TrimSpace(message.ToolCallID)
			if callName == "" || callID == "" {
				continue
			}
			argsJSON := "{}"
			if len(message.Args) > 0 {
				if encoded, err := json.Marshal(message.Args); err == nil {
					argsJSON = string(encoded)
				}
			}
			messages = append(messages, openAIChatMessage{
				Role:    openai.ChatMessageRoleAssistant,
				Content: strings.TrimSpace(message.Text),
				ToolCalls: []openAIToolCallWire{{
					ID:   callID,
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      callName,
						Arguments: argsJSON,
					},
				}},
			})
			continue
		case "tool_result":
			if strings.TrimSpace(message.ToolCallID) == "" {
				continue
			}
			text := strings.TrimSpace(message.Text)
			messages = append(messages, openAIChatMessage{
				Role:       openai.ChatMessageRoleTool,
				ToolCallID: strings.TrimSpace(message.ToolCallID),
				Name:       strings.TrimSpace(message.ToolName),
				Content:    text,
			})
			continue
		case "assistant_partial":
			continue
		}
		role := NormalizeTranscriptRole(message.Role, message.Type)
		if role == "tool" {
			continue
		}
		text := strings.TrimSpace(message.Text)
		if text == "" {
			continue
		}
		messages = append(messages, openAIChatMessage{
			Role:    role,
			Content: text,
		})
	}
	if trimmed := strings.TrimSpace(runCtx.Request.Message); trimmed != "" {
		message := openAIChatMessage{Role: openai.ChatMessageRoleUser, Content: trimmed}
		if parts := buildOpenAICompatAttachmentParts(runCtx.Request); len(parts) > 0 {
			message.Content = ""
			message.MultiContent = parts
		}
		// Deduplicate: if the last message is a text-only user message with the
		// same content, replace it. This happens because the current user message
		// is appended to the transcript in Stage 2 (AppendIncomingUserTranscript)
		// before the transcript is loaded in Stage 4 (LoadTranscript), resulting
		// in two consecutive user messages — the first text-only and the second
		// potentially multimodal (with image attachments). Consecutive duplicate
		// user messages confuse many provider APIs.
		if len(messages) > 0 {
			last := &messages[len(messages)-1]
			if last.Role == openai.ChatMessageRoleUser && len(last.MultiContent) == 0 {
				lastText := strings.TrimSpace(extractOpenAICompatContent(last.Content))
				if lastText == trimmed {
					messages[len(messages)-1] = message
					return SanitizeOpenAICompatMessages(messages)
				}
			}
		}
		messages = append(messages, message)
	} else if parts := buildOpenAICompatAttachmentParts(runCtx.Request); len(parts) > 0 {
		messages = append(messages, openAIChatMessage{Role: openai.ChatMessageRoleUser, MultiContent: parts})
	}
	return SanitizeOpenAICompatMessages(messages)
}

func buildOpenAICompatAttachmentParts(req core.AgentRunRequest) []openai.ChatMessagePart {
	var imageParts []openai.ChatMessagePart
	for _, attachment := range req.Attachments {
		if !infra.AttachmentIsImage(attachment) {
			continue
		}
		dataURL := infra.AttachmentDataURL(attachment)
		if dataURL == "" {
			continue
		}
		imageParts = append(imageParts, openai.ChatMessagePart{
			Type:     openai.ChatMessagePartTypeImageURL,
			ImageURL: &openai.ChatMessageImageURL{URL: dataURL, Detail: openai.ImageURLDetailAuto},
		})
	}
	if len(imageParts) == 0 {
		return nil
	}
	parts := make([]openai.ChatMessagePart, 0, len(imageParts)+1)
	if text := strings.TrimSpace(req.Message); text != "" {
		parts = append(parts, openai.ChatMessagePart{Type: openai.ChatMessagePartTypeText, Text: text})
	}
	parts = append(parts, imageParts...)
	return parts
}

func BuildOpenAICompatToolDefinitions(tools []rtypes.Tool, schemaProviderHints ...toolfn.SchemaProvider) []openAIToolDefinition {
	var schemaProv toolfn.SchemaProvider
	if len(schemaProviderHints) > 0 {
		schemaProv = schemaProviderHints[0]
	}
	var definitions []openAIToolDefinition
	for _, tool := range tools {
		provider, ok := tool.(core.OpenAIFunctionToolProvider)
		if !ok {
			continue
		}
		schema := provider.OpenAIFunctionTool()
		if schema == nil || strings.TrimSpace(schema.Name) == "" {
			continue
		}
		params := schema.Parameters
		if schemaProv != "" && params != nil {
			params = toolfn.NormalizeToolParameters(CloneAnyMap(params), schemaProv)
		}
		definition := openAIToolDefinition{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        strings.TrimSpace(schema.Name),
				Description: strings.TrimSpace(schema.Description),
				Parameters:  params,
			},
		}
		definitions = append(definitions, definition)
	}
	return definitions
}

func resolveOpenAICompatToolChoice(tools []rtypes.Tool) any {
	if len(BuildOpenAICompatToolDefinitions(tools)) == 0 {
		return nil
	}
	return "auto"
}

func (b *OpenAICompatBackend) resolveNoOutputTimeout(ctx context.Context) time.Duration {
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
	return minTimeout
}

func (b *OpenAICompatBackend) resolveBlockSendTimeout() time.Duration {
	if b.BlockSendTimeout > 0 {
		return b.BlockSendTimeout
	}
	return 5 * time.Second
}

// collectAllowedToolNamesFromRtypes converts []toolfn.Tool to a set of allowed
// tool names that can be passed to SanitizeHistoryPipeline.
func collectAllowedToolNamesFromRtypes(tools []toolfn.Tool) map[string]bool {
	if len(tools) == 0 {
		return nil
	}
	names := make(map[string]bool, len(tools))
	for _, t := range tools {
		if name := strings.TrimSpace(t.Name()); name != "" {
			names[name] = true
		}
	}
	return names
}
