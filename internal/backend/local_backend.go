// local_backend.go — Backend implementation that routes agent runs through
// a locally-loaded GGUF model (via localmodel.Manager).
//
// All internal message / tool / tool-call wiring uses llamawrapper types so
// that the engine's model-specific prompt renderer (Qwen3, Qwen3.5, generic
// ChatML, …) and the matching stream parser are set up correctly.
//
// The only contact with third-party openai types is at the boundary where
// BuildOpenAICompatMessages / BuildOpenAICompatToolDefinitions produce them;
// they are immediately converted to llamawrapper equivalents here.
package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/event"
	"github.com/kocort/kocort/internal/localmodel"
	"github.com/kocort/kocort/internal/localmodel/llamawrapper"
	"github.com/kocort/kocort/internal/rtypes"
)

// maxLocalToolRounds is the circuit-breaker limit for the tool loop.
const maxLocalToolRounds = 30

// Finish-reason string constants (avoid importing openai just for these).
const (
	localFinishStop      = "stop"
	localFinishToolCalls = "tool_calls"
)

// localStreamingRoundResult holds the output of a single streaming round,
// using llamawrapper types throughout.
type localStreamingRoundResult struct {
	FinalText    string
	ToolCalls    []llamawrapper.ToolCall
	FinishReason string
	ResponseID   string
	Usage        map[string]any
}

type localModelToolLoopState struct {
	messages           []llamawrapper.ChatMessage
	tools              []llamawrapper.Tool
	rawToolCalls       []llamawrapper.ToolCall
	validatedToolCalls []llamawrapper.ToolCall
}

// LocalModelBackend implements rtypes.Backend by delegating to a
// localmodel.Manager instance loaded with a GGUF model.
type LocalModelBackend struct {
	Manager              *localmodel.Manager
	NoOutputTimeout      time.Duration
	BlockSendTimeout     time.Duration
	BlockReplyCoalescing *delivery.BlockStreamingCoalescing
}

// NewLocalModelBackend creates a backend that uses the given local model manager.
func NewLocalModelBackend(mgr *localmodel.Manager) *LocalModelBackend {
	return &LocalModelBackend{
		Manager:          mgr,
		BlockSendTimeout: 5 * time.Second,
		BlockReplyCoalescing: &delivery.BlockStreamingCoalescing{
			MinChars: 16,
			MaxChars: 160,
			Idle:     750 * time.Millisecond,
			Joiner:   "",
		},
	}
}

// Run satisfies the rtypes.Backend interface.
func (b *LocalModelBackend) Run(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
	if b == nil || b.Manager == nil {
		return core.AgentRunResult{}, fmt.Errorf("local model backend is not configured")
	}
	if b.Manager.Status() != localmodel.StatusRunning {
		slog.Warn("[local-backend] model is not running",
			"status", b.Manager.Status(),
			"modelID", b.Manager.ModelID(),
			"lastError", b.Manager.LastError())
		return core.AgentRunResult{}, fmt.Errorf("local model is not running (status: %s)", b.Manager.Status())
	}
	// Detect stub backend (built without -tags llamacpp).
	if b.Manager.IsStub() {
		slog.Warn("[local-backend] using StubInferencer — binary was built without '-tags llamacpp', local inference is not available")
		return core.AgentRunResult{}, fmt.Errorf("local model inference is not available: binary was built without llama.cpp support (rebuild with '-tags llamacpp')")
	}

	slog.Info("[local-backend] starting inference", "model", b.Manager.ModelID())

	runCtx.Runtime = ensureRuntime(runCtx)

	// Create cancellable context (same pattern as OpenAICompatBackend).
	requestCtx := ctx
	cancelRequest := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && runCtx.Request.Timeout > 0 {
		requestCtx, cancelRequest = context.WithTimeout(ctx, runCtx.Request.Timeout)
	} else {
		requestCtx, cancelRequest = context.WithCancel(ctx)
	}
	defer cancelRequest()

	// Build messages & tools via the shared OpenAI-compat helpers, then
	// immediately convert to llamawrapper types so everything downstream
	// is openai-free and the engine's model-specific renderer is used.
	messages := convertOpenAIMessagesToLlama(
		SanitizeOpenAICompatMessages(BuildOpenAICompatMessages(runCtx)),
	)
	tools := convertOpenAIToolsToLlama(
		BuildOpenAICompatToolDefinitions(runCtx.AvailableTools),
	)

	// Truncate conversation to fit within model context window.
	// Reserve ~25% of the context for generation.
	contextSize := b.Manager.ContextSize()
	if contextSize <= 0 {
		contextSize = 2048
	}
	promptBudget := contextSize * 3 / 4
	if promptBudget < 256 {
		promptBudget = 256
	}

	slog.Info("[local-backend] context budget", "contextSize", contextSize, "promptBudget", promptBudget)

	messages = truncateLlamaMessagesToFit(messages, tools, promptBudget)

	// If even after truncation the prompt exceeds budget, bail out.
	estAfter := estimateLlamaMessagesTokens(messages, tools)
	if estAfter > promptBudget {
		errMsg := fmt.Sprintf("prompt too large for context window: ~%d est tokens exceed %d budget (context=%d) — reduce system prompt or tool definitions",
			estAfter, promptBudget, contextSize)
		slog.Warn("[local-backend] prompt too large for context window",
			"estTokens", estAfter, "budget", promptBudget, "contextSize", contextSize)
		return core.AgentRunResult{}, fmt.Errorf("%s", errMsg)
	}

	event.RecordModelEvent(requestCtx, runCtx.Runtime.GetAudit(), nil,
		runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID,
		"request_started", "info", "local model request started", map[string]any{
			"provider":     "local",
			"model":        b.Manager.ModelID(),
			"messageCount": len(messages),
			"toolCount":    len(tools),
			"backendKind":  "local",
			"providerKind": "local",
			"stream":       true,
		})

	return b.runStreamingToolLoop(requestCtx, cancelRequest, messages, tools, runCtx)
}

// ---------------------------------------------------------------------------
// runStreamingToolLoop — tool-call / tool-result cycle using llama types
// ---------------------------------------------------------------------------

func (b *LocalModelBackend) runStreamingToolLoop(
	ctx context.Context,
	cancel context.CancelFunc,
	messages []llamawrapper.ChatMessage,
	tools []llamawrapper.Tool,
	runCtx rtypes.AgentRunContext,
) (core.AgentRunResult, error) {
	return runStandardModelToolLoop(ctx, cancel, runCtx, StandardModelToolLoopConfig[localModelToolLoopState]{
		InitialState: localModelToolLoopState{
			messages: append([]llamawrapper.ChatMessage{}, messages...),
			tools:    tools,
		},
		MaxRounds:                      maxLocalToolRounds,
		BackendKind:                    "local",
		ProviderKind:                   "local",
		IncludeAccumulatedMediaOnYield: true,
		ExecuteRound: func(ctx context.Context, cancel context.CancelFunc, state *localModelToolLoopState, runCtx rtypes.AgentRunContext, events *agentEventBuilder) (StandardModelRoundResult, error) {
			sanitized := sanitizeLlamaMessages(state.messages)
			roundResult, err := b.runStreamingRound(ctx, cancel, sanitized, state.tools, runCtx, events)
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
		NormalizeToolCalls: func(state *localModelToolLoopState, _ StandardModelRoundResult) ([]StandardModelToolCall, error) {
			validatedCalls, err := validateLlamaToolCalls(state.rawToolCalls)
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
		AppendAssistantToolCalls: func(state *localModelToolLoopState, round StandardModelRoundResult) error {
			state.messages = append(state.messages, llamawrapper.ChatMessage{
				Role:      "assistant",
				Content:   strings.TrimSpace(round.FinalText),
				ToolCalls: state.validatedToolCalls,
			})
			return nil
		},
		AppendToolResult: func(state *localModelToolLoopState, call StandardModelToolCall, historyText string, _ bool) error {
			state.messages = append(state.messages, llamawrapper.ChatMessage{
				Role:       "tool",
				ToolCallID: call.ID,
				Name:       call.Name,
				Content:    historyText,
			})
			return nil
		},
		IsToolCallStopReason: func(reason string) bool {
			return reason == localFinishToolCalls
		},
		MissingToolCallsError: func(_ string) error {
			return fmt.Errorf("provider returned finish_reason=tool_calls with no tool calls")
		},
		LoopExceededError: func(maxRounds int) error {
			return fmt.Errorf("local model tool loop exceeded max rounds (%d)", maxRounds)
		},
		RecordRoundError: func(ctx context.Context, runCtx rtypes.AgentRunContext, err error) {
			event.RecordModelEvent(ctx, runCtx.Runtime.GetAudit(), nil,
				runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID,
				"request_failed", "error", "local model request failed", map[string]any{
					"provider": "local",
					"model":    b.Manager.ModelID(),
					"error":    err.Error(),
				})
		},
		RecordToolRoundComplete: func(ctx context.Context, runCtx rtypes.AgentRunContext, round int, pendingCalls []string, stopReason string) {
			event.RecordModelEvent(ctx, runCtx.Runtime.GetAudit(), nil,
				runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID,
				"tool_round_complete", "info", "local model tool round completed", map[string]any{
					"provider":         "local",
					"model":            b.Manager.ModelID(),
					"round":            round,
					"pendingToolCalls": pendingCalls,
					"stopReason":       stopReason,
				})
		},
	})
}

// ---------------------------------------------------------------------------
// runStreamingRound — streaming inference round using llama types
// ---------------------------------------------------------------------------

func (b *LocalModelBackend) runStreamingRound(
	ctx context.Context,
	cancel context.CancelFunc,
	messages []llamawrapper.ChatMessage,
	tools []llamawrapper.Tool,
	runCtx rtypes.AgentRunContext,
	events *agentEventBuilder,
) (localStreamingRoundResult, error) {
	enableThinking := b.Manager.EnableThinking()
	numPredict := -1 // unlimited by default

	completionReq := llamawrapper.ChatCompletionRequest{
		Model:          b.Manager.ModelID(),
		Messages:       messages,
		Tools:          tools,
		MaxTokens:      &numPredict,
		Stream:         true,
		EnableThinking: llamawrapper.BoolPtr(enableThinking),
	}

	ch, err := b.Manager.CreateChatCompletionStream(ctx, completionReq)
	if err != nil {
		event.RecordModelEvent(ctx, runCtx.Runtime.GetAudit(), nil,
			runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID,
			"stream_open_failed", "error", "local model stream open failed", map[string]any{
				"provider": "local",
				"model":    b.Manager.ModelID(),
				"error":    err.Error(),
			})
		return localStreamingRoundResult{}, fmt.Errorf("local model request failed: %w", err)
	}

	event.RecordModelEvent(ctx, runCtx.Runtime.GetAudit(), nil,
		runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID,
		"stream_opened", "info", "local model stream opened", map[string]any{
			"provider": "local",
			"model":    b.Manager.ModelID(),
		})

	// Watchdog: cancel the request if no output is produced for too long.
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
		streamTC      []llamawrapper.ToolCall // tool calls extracted by the engine's stream parser
		hadReasoning  bool
		reasoningDone bool
	)

recvLoop:
	for {
		select {
		case <-ctx.Done():
			return localStreamingRoundResult{}, fmt.Errorf("local model request cancelled: %w", ctx.Err())
		case chunk, ok := <-ch:
			if !ok {
				break recvLoop
			}
			if watchdog.TimedOut() {
				return localStreamingRoundResult{}, &BackendError{
					Reason:  BackendFailureTransientHTTP,
					Message: fmt.Sprintf("local model stream produced no output for %s", b.resolveNoOutputTimeout(ctx).Round(time.Second)),
				}
			}
			watchdog.Touch()

			if id := strings.TrimSpace(chunk.ID); id != "" {
				responseID = id
			}
			if chunk.Usage != nil {
				usage = map[string]any{
					"prompt_tokens":     chunk.Usage.PromptTokens,
					"completion_tokens": chunk.Usage.CompletionTokens,
					"total_tokens":      chunk.Usage.TotalTokens,
				}
			}

			for _, choice := range chunk.Choices {
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

				if reasoning := choice.Delta.Reasoning; reasoning != "" {
					hadReasoning = true
					_, _ = fmt.Fprintf(os.Stderr, "\n[reasoning]%s", reasoning) // best-effort debug trace
					events.Add("assistant", map[string]any{
						"type": "reasoning_delta",
						"text": reasoning,
					})
				}

				// Accumulate tool calls extracted by the engine's stream parser.
				if len(choice.Delta.ToolCalls) > 0 {
					// Emit reasoning_complete when transitioning from reasoning to tool calls.
					if hadReasoning && !reasoningDone {
						reasoningDone = true
						events.Add("assistant", map[string]any{
							"type": "reasoning_complete",
						})
					}
					streamTC = append(streamTC, choice.Delta.ToolCalls...)
				}

				if choice.FinishReason != nil {
					if reason := strings.TrimSpace(*choice.FinishReason); reason != "" {
						_, _ = fmt.Fprintf(os.Stderr, "\n[finish_reason]%s\n", reason) // best-effort debug trace
						finish = reason
					}
				}
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
		return localStreamingRoundResult{}, err
	}

	// Determine final text and extract tool calls.
	// Prefer tool calls extracted by the engine's stream parser (which
	// understands Qwen3 <tool_call> JSON, Qwen3.5 XML, etc.).
	// Fall back to regex extraction from raw text for legacy/unknown models.
	finalText := strings.TrimSpace(fullText.String())
	var toolCalls []llamawrapper.ToolCall

	if len(streamTC) > 0 {
		toolCalls = streamTC
		finish = localFinishToolCalls
	} else {
		extracted, cleanText := extractLocalToolCalls(finalText)
		if len(extracted) > 0 {
			toolCalls = extracted
			finalText = cleanText
			finish = localFinishToolCalls
		}
	}

	if finish == "" {
		finish = localFinishStop
	}

	event.RecordModelEvent(ctx, runCtx.Runtime.GetAudit(), nil,
		runCtx.Identity.ID, runCtx.Session.SessionKey, runCtx.Request.RunID,
		"response_completed", "info", "local model response completed", map[string]any{
			"responseId":    responseID,
			"finishReason":  finish,
			"usage":         usage,
			"toolCallCount": len(toolCalls),
		})

	return localStreamingRoundResult{
		FinalText:    finalText,
		ToolCalls:    toolCalls,
		FinishReason: finish,
		ResponseID:   responseID,
		Usage:        usage,
	}, nil
}

// resolveNoOutputTimeout returns the no-output timeout — identical logic to
// OpenAICompatBackend.resolveNoOutputTimeout.
func (b *LocalModelBackend) resolveNoOutputTimeout(ctx context.Context) time.Duration {
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

// resolveBlockSendTimeout returns the configured block send timeout or a default.
func (b *LocalModelBackend) resolveBlockSendTimeout() time.Duration {
	if b.BlockSendTimeout > 0 {
		return b.BlockSendTimeout
	}
	return 5 * time.Second
}

// ---------------------------------------------------------------------------
// Prompt truncation helpers (operate on llamawrapper types)
// ---------------------------------------------------------------------------

// estimateLlamaMessagesTokens gives a rough token count for a set of
// llamawrapper messages + tool definitions. The heuristic is ~4 chars/token.
func estimateLlamaMessagesTokens(messages []llamawrapper.ChatMessage, tools []llamawrapper.Tool) int {
	chars := 0
	for _, m := range messages {
		content := ""
		if s, ok := m.Content.(string); ok {
			content = s
		}
		chars += len(m.Role) + len(content) + 8
		for _, tc := range m.ToolCalls {
			chars += len(tc.Function.Name) + len(tc.Function.Arguments) + 16
		}
	}
	for _, t := range tools {
		chars += len(t.Function.Name) + len(t.Function.Description) + 32
		if len(t.Function.Parameters) > 0 {
			chars += len(t.Function.Parameters)
		}
	}
	return chars / 4
}

// truncateLlamaMessagesToFit removes the oldest conversation messages
// (keeping the system prompt at [0] and the user message at [len-1]) until
// the estimated prompt token count fits within budget.
func truncateLlamaMessagesToFit(
	messages []llamawrapper.ChatMessage,
	tools []llamawrapper.Tool,
	budget int,
) []llamawrapper.ChatMessage {
	if budget <= 0 || len(messages) <= 2 {
		return messages
	}

	est := estimateLlamaMessagesTokens(messages, tools)
	if est <= budget {
		return messages
	}

	slog.Info("[local-backend] prompt too large, truncating conversation history",
		"estTokens", est, "budget", budget)

	for est > budget && len(messages) > 2 {
		messages = append(messages[:1], messages[2:]...)
		est = estimateLlamaMessagesTokens(messages, tools)
	}

	if est > budget {
		slog.Warn("[local-backend] prompt still too large after truncation",
			"estTokens", est, "budget", budget)
	} else {
		slog.Info("[local-backend] truncated conversation history",
			"messageCount", len(messages), "estTokens", est, "budget", budget)
	}

	return messages
}

// ---------------------------------------------------------------------------
// OpenAI → llamawrapper type conversion (used at the boundary in Run())
// ---------------------------------------------------------------------------

// convertOpenAIMessagesToLlama converts OpenAI chat messages to llamawrapper
// ChatMessage format so that the engine's model-specific renderer handles
// prompt construction.
func convertOpenAIMessagesToLlama(msgs []openAIChatMessage) []llamawrapper.ChatMessage {
	out := make([]llamawrapper.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		cm := llamawrapper.ChatMessage{
			Role:       m.Role,
			Content:    ExtractOpenAICompatContent(m.Content),
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			idx := 0
			if tc.Index != nil {
				idx = *tc.Index
			}
			cm.ToolCalls = append(cm.ToolCalls, llamawrapper.ToolCall{
				ID:    tc.ID,
				Index: idx,
				Type:  string(tc.Type),
				Function: llamawrapper.ToolFunction{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
		out = append(out, cm)
	}
	return out
}

// convertOpenAIToolsToLlama converts OpenAI tool definitions to llamawrapper
// Tool format for the engine's model-specific tool formatting.
func convertOpenAIToolsToLlama(tools []openAIToolDefinition) []llamawrapper.Tool {
	out := make([]llamawrapper.Tool, 0, len(tools))
	for _, t := range tools {
		if t.Function == nil {
			continue
		}
		params, _ := json.Marshal(t.Function.Parameters)
		out = append(out, llamawrapper.Tool{
			Type: string(t.Type),
			Function: llamawrapper.ToolDefFunc{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  json.RawMessage(params),
			},
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// llamawrapper-based message sanitization and tool-call validation
// ---------------------------------------------------------------------------

// sanitizeLlamaMessages cleans and validates a slice of llamawrapper messages,
// dropping empty messages, merging adjacent system messages, and ensuring
// assistant tool-call messages have matching tool-result messages.
func sanitizeLlamaMessages(messages []llamawrapper.ChatMessage) []llamawrapper.ChatMessage {
	if len(messages) == 0 {
		return nil
	}
	var sanitized []llamawrapper.ChatMessage
	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		role := strings.TrimSpace(strings.ToLower(msg.Role))
		contentStr, _ := msg.Content.(string)
		text := strings.TrimSpace(contentStr)

		switch role {
		case "system", "user":
			if text == "" {
				continue
			}
			if role == "system" && len(sanitized) > 0 && sanitized[len(sanitized)-1].Role == role {
				prev, _ := sanitized[len(sanitized)-1].Content.(string)
				sanitized[len(sanitized)-1].Content = strings.TrimSpace(prev + "\n\n" + text)
				continue
			}
			sanitized = append(sanitized, llamawrapper.ChatMessage{Role: role, Content: text})

		case "assistant":
			validCalls, _ := validateLlamaToolCalls(msg.ToolCalls)
			if len(validCalls) == 0 {
				if text == "" {
					continue
				}
				sanitized = append(sanitized, llamawrapper.ChatMessage{Role: "assistant", Content: text})
				continue
			}
			responded := map[string]llamawrapper.ChatMessage{}
			j := i + 1
			for ; j < len(messages); j++ {
				next := messages[j]
				if strings.TrimSpace(strings.ToLower(next.Role)) != "tool" {
					break
				}
				toolCallID := strings.TrimSpace(next.ToolCallID)
				if toolCallID == "" {
					continue
				}
				for _, call := range validCalls {
					if call.ID == toolCallID {
						nextContent, _ := next.Content.(string)
						responded[toolCallID] = llamawrapper.ChatMessage{
							Role:       "tool",
							ToolCallID: toolCallID,
							Name:       strings.TrimSpace(next.Name),
							Content:    strings.TrimSpace(nextContent),
						}
						break
					}
				}
			}
			filteredCalls := make([]llamawrapper.ToolCall, 0, len(validCalls))
			for _, call := range validCalls {
				if _, ok := responded[call.ID]; ok {
					filteredCalls = append(filteredCalls, call)
				}
			}
			if len(filteredCalls) == 0 {
				if text == "" {
					i = j - 1
					continue
				}
				sanitized = append(sanitized, llamawrapper.ChatMessage{Role: "assistant", Content: text})
				i = j - 1
				continue
			}
			sanitized = append(sanitized, llamawrapper.ChatMessage{
				Role:      "assistant",
				Content:   text,
				ToolCalls: filteredCalls,
			})
			for _, call := range filteredCalls {
				sanitized = append(sanitized, responded[call.ID])
			}
			i = j - 1

		case "tool":
			continue // handled above with assistant
		}
	}
	return sanitized
}

// validateLlamaToolCalls checks that each tool call has a non-empty ID and
// function name, defaulting Type to "function" if empty.
func validateLlamaToolCalls(calls []llamawrapper.ToolCall) ([]llamawrapper.ToolCall, error) {
	validated := make([]llamawrapper.ToolCall, 0, len(calls))
	for _, call := range calls {
		callID := strings.TrimSpace(call.ID)
		if callID == "" {
			return nil, fmt.Errorf("provider returned tool call with empty id")
		}
		if strings.TrimSpace(call.Type) == "" {
			call.Type = "function"
		}
		if strings.TrimSpace(call.Type) != "function" {
			return nil, fmt.Errorf("provider returned unsupported tool call type %q", call.Type)
		}
		call.Function.Name = strings.TrimSpace(call.Function.Name)
		if call.Function.Name == "" {
			return nil, fmt.Errorf("provider returned tool call %q with empty function name", callID)
		}
		call.ID = callID
		validated = append(validated, call)
	}
	return validated, nil
}

// ---------------------------------------------------------------------------
// Legacy tool-call extraction from raw text (fallback for unknown models)
// ---------------------------------------------------------------------------

// extractLocalToolCalls scans model output text for ```tool_call``` blocks
// and returns structured llamawrapper.ToolCall values plus the remaining
// clean text. If no tool calls are found, returns nil and the original text.
func extractLocalToolCalls(text string) ([]llamawrapper.ToolCall, string) {
	const startMarker = "```tool_call"
	const endMarker = "```"

	var toolCalls []llamawrapper.ToolCall
	remaining := text
	callIdx := 0

	for {
		idx := strings.Index(remaining, startMarker)
		if idx < 0 {
			break
		}

		beforeBlock := strings.TrimSpace(remaining[:idx])

		afterStart := remaining[idx+len(startMarker):]
		afterStart = strings.TrimLeft(afterStart, "\r\n")
		endIdx := strings.Index(afterStart, endMarker)
		if endIdx < 0 {
			break
		}

		jsonBlock := strings.TrimSpace(afterStart[:endIdx])

		var parsed struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(jsonBlock), &parsed); err != nil {
			break
		}
		if strings.TrimSpace(parsed.Name) == "" {
			break
		}

		rawArgs := "{}"
		if len(parsed.Arguments) > 0 {
			rawArgs = string(parsed.Arguments)
		}

		callID := fmt.Sprintf("local_%d", time.Now().UnixNano())
		toolCalls = append(toolCalls, llamawrapper.ToolCall{
			Index: callIdx,
			ID:    callID,
			Type:  "function",
			Function: llamawrapper.ToolFunction{
				Name:      strings.TrimSpace(parsed.Name),
				Arguments: rawArgs,
			},
		})
		callIdx++

		remaining = beforeBlock + " " + strings.TrimSpace(afterStart[endIdx+len(endMarker):])
		remaining = strings.TrimSpace(remaining)
	}

	if len(toolCalls) == 0 {
		return nil, text
	}

	return toolCalls, strings.TrimSpace(remaining)
}
