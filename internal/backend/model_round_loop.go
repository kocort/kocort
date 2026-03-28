package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/rtypes"
	"github.com/kocort/kocort/utils"
)

type StandardModelToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type StandardModelRoundResult struct {
	FinalText  string
	StopReason string
	ResponseID string
	Usage      map[string]any
}

type StandardModelToolLoopConfig[S any] struct {
	InitialState                   S
	MaxRounds                      int
	BackendKind                    string
	ProviderKind                   string
	IncludeAccumulatedMediaOnYield bool
	ExecuteRound                   func(ctx context.Context, cancel context.CancelFunc, state *S, runCtx rtypes.AgentRunContext, events *agentEventBuilder) (StandardModelRoundResult, error)
	NormalizeToolCalls             func(state *S, round StandardModelRoundResult) ([]StandardModelToolCall, error)
	AppendAssistantToolCalls       func(state *S, round StandardModelRoundResult) error
	BeforeToolResults              func(state *S) error
	AppendToolResult               func(state *S, call StandardModelToolCall, historyText string, isError bool) error
	AfterToolResults               func(state *S) error
	IsToolCallStopReason           func(reason string) bool
	MissingToolCallsError          func(stopReason string) error
	LoopExceededError              func(maxRounds int) error
	RecordRoundError               func(ctx context.Context, runCtx rtypes.AgentRunContext, err error)
	RecordToolRoundComplete        func(ctx context.Context, runCtx rtypes.AgentRunContext, round int, pendingCalls []string, stopReason string)
}

func runStandardModelToolLoop[S any](
	ctx context.Context,
	cancel context.CancelFunc,
	runCtx rtypes.AgentRunContext,
	config StandardModelToolLoopConfig[S],
) (core.AgentRunResult, error) {
	state := config.InitialState
	usage := map[string]any{}
	events := newAgentEventBuilder(runCtx)
	var accumulatedMediaURLs []string
	var lastVisibleToolPayload core.ReplyPayload

	for round := 0; round < config.MaxRounds; round++ {
		if ctx.Err() != nil {
			return core.AgentRunResult{}, ctx.Err()
		}

		roundResult, err := config.ExecuteRound(ctx, cancel, &state, runCtx, events)
		if err != nil {
			if config.RecordRoundError != nil {
				config.RecordRoundError(ctx, runCtx, err)
			}
			return core.AgentRunResult{}, err
		}

		MergeUsageMaps(usage, roundResult.Usage)
		if strings.TrimSpace(roundResult.ResponseID) != "" {
			usage["previousResponseId"] = strings.TrimSpace(roundResult.ResponseID)
		}

		if !config.IsToolCallStopReason(roundResult.StopReason) {
			payload, ok := resolveToolLoopFinalPayload(roundResult.FinalText, accumulatedMediaURLs, lastVisibleToolPayload)
			if !ok {
				return core.AgentRunResult{}, core.ErrProviderEmptyResponse
			}
			runCtx.ReplyDispatcher.SendFinalReply(payload)
			events.Add("assistant", map[string]any{
				"type":         "final",
				"text":         payload.Text,
				"mediaUrl":     payload.MediaURL,
				"mediaUrls":    payload.MediaURLs,
				"stopReason":   roundResult.StopReason,
				"toolRounds":   round,
				"responseId":   roundResult.ResponseID,
				"backendKind":  config.BackendKind,
				"providerKind": config.ProviderKind,
			})
			if len(roundResult.Usage) > 0 {
				events.Add("lifecycle", map[string]any{
					"type":  "usage",
					"usage": CloneAnyMap(roundResult.Usage),
				})
			}
			return core.AgentRunResult{
				Payloads:   []core.ReplyPayload{payload},
				Events:     events.Events(),
				Usage:      usage,
				StopReason: roundResult.StopReason,
				Meta: map[string]any{
					"backendKind": config.BackendKind,
					"toolRounds":  round,
				},
			}, nil
		}

		toolCalls, err := config.NormalizeToolCalls(&state, roundResult)
		if err != nil {
			return core.AgentRunResult{}, err
		}
		if len(toolCalls) == 0 {
			if config.MissingToolCallsError != nil {
				return core.AgentRunResult{}, config.MissingToolCallsError(roundResult.StopReason)
			}
			return core.AgentRunResult{}, fmt.Errorf("provider returned tool-call stop reason with no tool calls")
		}

		if config.AppendAssistantToolCalls != nil {
			if err := config.AppendAssistantToolCalls(&state, roundResult); err != nil {
				return core.AgentRunResult{}, err
			}
		}
		if config.BeforeToolResults != nil {
			if err := config.BeforeToolResults(&state); err != nil {
				return core.AgentRunResult{}, err
			}
		}

		pendingCalls := make([]string, 0, len(toolCalls))
		for _, call := range toolCalls {
			pendingCalls = append(pendingCalls, call.Name)
			events.Add("tool", map[string]any{
				"type":       "tool_call",
				"toolCallId": call.ID,
				"toolName":   call.Name,
				"arguments":  strings.TrimSpace(call.Arguments),
				"round":      round + 1,
			})

			args := map[string]any{}
			if strings.TrimSpace(call.Arguments) != "" {
				if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
					return core.AgentRunResult{}, fmt.Errorf("tool %q returned invalid arguments JSON: %w", call.Name, err)
				}
			}
			args["__toolCallId"] = call.ID

			result, err := runCtx.Runtime.ExecuteTool(ctx, runCtx, call.Name, args)
			if err != nil {
				var toolErr *core.ToolExecutionFailure
				if !errorAsToolExecutionFailure(err, &toolErr) {
					return core.AgentRunResult{}, err
				}
				events.Add("tool", map[string]any{
					"type":       "tool_result",
					"toolCallId": call.ID,
					"toolName":   call.Name,
					"text":       strings.TrimSpace(toolErr.HistoryText),
					"error":      strings.TrimSpace(toolErr.Message),
					"round":      round + 1,
				})
				if config.AppendToolResult != nil {
					if err := config.AppendToolResult(&state, call, utils.NonEmpty(strings.TrimSpace(toolErr.HistoryText), "ERROR"), true); err != nil {
						return core.AgentRunResult{}, err
					}
				}
				continue
			}

			visibleText := resolveToolResultText(result)
			historyText := resolveToolResultHistoryContent(result)
			if result.MediaURL != "" {
				accumulatedMediaURLs = append(accumulatedMediaURLs, result.MediaURL)
			}
			if len(result.MediaURLs) > 0 {
				accumulatedMediaURLs = append(accumulatedMediaURLs, result.MediaURLs...)
			}
			if visibleText != "" || result.MediaURL != "" || len(result.MediaURLs) > 0 {
				toolPayload := core.ReplyPayload{
					Text:      visibleText,
					MediaURL:  result.MediaURL,
					MediaURLs: result.MediaURLs,
				}
				runCtx.ReplyDispatcher.SendToolResult(toolPayload)
				lastVisibleToolPayload = toolPayload
			}
			events.Add("tool", map[string]any{
				"type":       "tool_result",
				"toolCallId": call.ID,
				"toolName":   call.Name,
				"text":       visibleText,
				"round":      round + 1,
			})
			if config.AppendToolResult != nil {
				if err := config.AppendToolResult(&state, call, historyText, false); err != nil {
					return core.AgentRunResult{}, err
				}
			}
		}

		if config.AfterToolResults != nil {
			if err := config.AfterToolResults(&state); err != nil {
				return core.AgentRunResult{}, err
			}
		}

		events.Add("lifecycle", map[string]any{
			"type":             "tool_round_complete",
			"round":            round + 1,
			"pendingToolCalls": pendingCalls,
			"stopReason":       roundResult.StopReason,
		})
		if config.RecordToolRoundComplete != nil {
			config.RecordToolRoundComplete(ctx, runCtx, round+1, pendingCalls, roundResult.StopReason)
		}

		if runCtx.RunState != nil && runCtx.RunState.Yielded {
			yieldMsg := runCtx.RunState.YieldMessage
			if yieldMsg == "" {
				yieldMsg = "Turn yielded."
			}
			payload := core.ReplyPayload{Text: yieldMsg}
			if config.IncludeAccumulatedMediaOnYield {
				payload.MediaURLs = accumulatedMediaURLs
				if len(accumulatedMediaURLs) == 1 {
					payload.MediaURL = accumulatedMediaURLs[0]
					payload.MediaURLs = nil
				}
			}
			runCtx.ReplyDispatcher.SendFinalReply(payload)
			events.Add("assistant", map[string]any{
				"type":         "yield",
				"text":         yieldMsg,
				"stopReason":   "end_turn",
				"toolRounds":   round + 1,
				"backendKind":  config.BackendKind,
				"providerKind": config.ProviderKind,
			})
			return core.AgentRunResult{
				Payloads:   []core.ReplyPayload{payload},
				Events:     events.Events(),
				Usage:      usage,
				StopReason: "end_turn",
				Meta: map[string]any{
					"backendKind": config.BackendKind,
					"toolRounds":  round + 1,
					"yielded":     true,
				},
			}, nil
		}
	}

	if config.LoopExceededError != nil {
		return core.AgentRunResult{}, config.LoopExceededError(config.MaxRounds)
	}
	return core.AgentRunResult{}, fmt.Errorf("tool loop exceeded max rounds (%d)", config.MaxRounds)
}

func resolveToolLoopFinalPayload(finalText string, accumulatedMediaURLs []string, lastVisibleToolPayload core.ReplyPayload) (core.ReplyPayload, bool) {
	payload := core.ReplyPayload{
		Text:      strings.TrimSpace(finalText),
		MediaURLs: accumulatedMediaURLs,
	}
	if len(accumulatedMediaURLs) == 1 {
		payload.MediaURL = accumulatedMediaURLs[0]
		payload.MediaURLs = nil
	}
	if payload.Text == "" && payload.MediaURL == "" && len(payload.MediaURLs) == 0 {
		if lastVisibleToolPayload.Text == "" && lastVisibleToolPayload.MediaURL == "" && len(lastVisibleToolPayload.MediaURLs) == 0 {
			return core.ReplyPayload{}, false
		}
		payload = lastVisibleToolPayload
	}
	return payload, true
}

func errorAsToolExecutionFailure(err error, target **core.ToolExecutionFailure) bool {
	if err == nil {
		return false
	}
	return errors.As(err, target)
}

func resolveToolResultText(result core.ToolResult) string {
	text := strings.TrimSpace(result.Text)
	if text == "" && len(result.JSON) > 0 {
		text = strings.TrimSpace(string(result.JSON))
	}
	return text
}

func resolveToolResultHistoryContent(result core.ToolResult) string {
	text := resolveToolResultText(result)
	if result.MediaURL != "" || len(result.MediaURLs) > 0 {
		mediaInfo := map[string]any{}
		if result.MediaURL != "" {
			mediaInfo["mediaUrl"] = result.MediaURL
		}
		if len(result.MediaURLs) > 0 {
			mediaInfo["mediaUrls"] = result.MediaURLs
		}
		if text == "" {
			if data, err := json.Marshal(mediaInfo); err == nil {
				return string(data)
			}
			return "{}"
		}
		var existing map[string]any
		if json.Unmarshal([]byte(text), &existing) == nil {
			for key, value := range mediaInfo {
				existing[key] = value
			}
			if data, err := json.Marshal(existing); err == nil {
				return string(data)
			}
		}
	}
	if text == "" {
		return "{}"
	}
	return text
}
