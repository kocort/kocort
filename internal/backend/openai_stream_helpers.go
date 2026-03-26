// Pure OpenAI streaming helpers extracted from runtime/openai_compat_backend.go.
//
// Functions for tool call accumulation, usage map handling, URL resolution,
// response extraction, and the stream output watchdog.
// None of these reference *Runtime, AgentRunContext, or ToolContext.
package backend

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// ---------------------------------------------------------------------------
// Tool call accumulation
// ---------------------------------------------------------------------------

// AccumulateOpenAIToolCalls merges streamed tool-call delta chunks into an
// accumulator slice, growing it as needed.
func AccumulateOpenAIToolCalls(existing []*openai.ToolCall, chunks []openai.ToolCall) []*openai.ToolCall {
	ensureIndex := func(index int) {
		for len(existing) <= index {
			existing = append(existing, nil)
		}
		if existing[index] == nil {
			existing[index] = &openai.ToolCall{}
		}
	}
	for _, chunk := range chunks {
		index := 0
		if chunk.Index != nil && *chunk.Index >= 0 {
			index = *chunk.Index
		} else {
			index = len(existing)
		}
		ensureIndex(index)
		acc := existing[index]
		if id := strings.TrimSpace(chunk.ID); id != "" {
			acc.ID = id
		}
		if typ := strings.TrimSpace(string(chunk.Type)); typ != "" {
			acc.Type = openai.ToolType(typ)
		}
		if name := strings.TrimSpace(chunk.Function.Name); name != "" {
			acc.Function.Name = name
		}
		if args := chunk.Function.Arguments; args != "" {
			acc.Function.Arguments += args
		}
	}
	return existing
}

// CompactOpenAIToolCalls flattens a pointer-based accumulator slice into a
// value slice, dropping nil entries.
func CompactOpenAIToolCalls(accumulators []*openai.ToolCall) []openai.ToolCall {
	toolCalls := make([]openai.ToolCall, 0, len(accumulators))
	for _, acc := range accumulators {
		if acc == nil {
			continue
		}
		toolCalls = append(toolCalls, *acc)
	}
	return toolCalls
}

// ValidateOpenAICompatToolCalls validates that each tool call has a non-empty
// ID, function type, and function name.
func ValidateOpenAICompatToolCalls(calls []openai.ToolCall) ([]openai.ToolCall, error) {
	validated := make([]openai.ToolCall, 0, len(calls))
	for _, call := range calls {
		callID := strings.TrimSpace(call.ID)
		if callID == "" {
			return nil, fmt.Errorf("provider returned tool call with empty id")
		}
		if strings.TrimSpace(string(call.Type)) == "" {
			call.Type = openai.ToolTypeFunction
		}
		if strings.TrimSpace(string(call.Type)) != string(openai.ToolTypeFunction) {
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
// Usage helpers
// ---------------------------------------------------------------------------

// MergeUsageMaps copies all keys from src into dst.
func MergeUsageMaps(dst map[string]any, src map[string]any) {
	if len(src) == 0 {
		return
	}
	for key, value := range src {
		dst[key] = value
	}
}

// UsageToMap converts an openai.Usage to a generic map.
func UsageToMap(usage openai.Usage) map[string]any {
	out := map[string]any{
		"prompt_tokens":     usage.PromptTokens,
		"completion_tokens": usage.CompletionTokens,
		"total_tokens":      usage.TotalTokens,
	}
	if usage.CompletionTokensDetails != nil && usage.CompletionTokensDetails.ReasoningTokens > 0 {
		out["reasoning_tokens"] = usage.CompletionTokensDetails.ReasoningTokens
	}
	return out
}

// ---------------------------------------------------------------------------
// URL resolution
// ---------------------------------------------------------------------------

// ResolveOpenAICompatBaseURL normalises a provider base URL by stripping a
// trailing /chat/completions path component.
func ResolveOpenAICompatBaseURL(baseURL string) (string, error) {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return "", fmt.Errorf("provider baseUrl is required")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", err
	}
	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(path, "/chat/completions") {
		parsed.Path = strings.TrimSuffix(path, "/chat/completions")
		return parsed.String(), nil
	}
	return parsed.String(), nil
}

// ResolveAnthropicCompatBaseURL normalises a provider base URL by stripping
// trailing /v1/messages, /messages, or /v1 path components.
func ResolveAnthropicCompatBaseURL(baseURL string) (string, error) {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return "", fmt.Errorf("provider baseUrl is required")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", err
	}
	path := strings.TrimRight(parsed.Path, "/")
	switch {
	case strings.HasSuffix(path, "/v1/messages"):
		parsed.Path = strings.TrimSuffix(path, "/v1/messages")
	case strings.HasSuffix(path, "/messages"):
		parsed.Path = strings.TrimSuffix(path, "/messages")
	case strings.HasSuffix(path, "/v1"):
		parsed.Path = strings.TrimSuffix(path, "/v1")
	}
	return parsed.String(), nil
}

// ---------------------------------------------------------------------------
// Response extraction
// ---------------------------------------------------------------------------

// ExtractOpenAICompatResponseText extracts the first non-empty choice text
// from a chat completion response.
func ExtractOpenAICompatResponseText(response openai.ChatCompletionResponse) string {
	for _, choice := range response.Choices {
		if trimmed := strings.TrimSpace(choice.Message.Content); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// ExtractOpenAICompatContent extracts a string value from a generic content
// field, returning "" for non-string types.
func ExtractOpenAICompatContent(content any) string {
	switch value := content.(type) {
	case string:
		return value
	default:
		return ""
	}
}

// ---------------------------------------------------------------------------
// Stream output watchdog
// ---------------------------------------------------------------------------

// StreamOutputWatchdog cancels a context if no output is produced within a
// timeout window.  Call Touch() on each chunk received to reset the timer.
type StreamOutputWatchdog struct {
	timeout  time.Duration
	cancel   context.CancelFunc
	timer    *time.Timer
	timedOut bool
	mu       sync.Mutex
}

// NewStreamOutputWatchdog creates a watchdog that fires cancel after timeout
// elapses without a Touch() call.
func NewStreamOutputWatchdog(ctx context.Context, timeout time.Duration, cancel context.CancelFunc) *StreamOutputWatchdog {
	w := &StreamOutputWatchdog{
		timeout: timeout,
		cancel:  cancel,
	}
	if timeout > 0 {
		w.timer = time.AfterFunc(timeout, func() {
			w.mu.Lock()
			w.timedOut = true
			w.mu.Unlock()
			cancel()
		})
	}
	go func() {
		<-ctx.Done()
		w.Stop()
	}()
	return w
}

// Touch resets the watchdog timer, indicating output was received.
func (w *StreamOutputWatchdog) Touch() {
	if w == nil || w.timeout <= 0 || w.timer == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timedOut {
		return
	}
	if !w.timer.Stop() {
		select {
		default:
		}
	}
	w.timer.Reset(w.timeout)
}

// TimedOut returns true if the watchdog fired.
func (w *StreamOutputWatchdog) TimedOut() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.timedOut
}

// Stop disables the watchdog timer.
func (w *StreamOutputWatchdog) Stop() {
	if w == nil || w.timer == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.timer.Stop()
}
