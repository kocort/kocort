package backend

import (
	"encoding/json"
	"html"
	"regexp"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// ---------------------------------------------------------------------------
// StreamChunkAdapter applies per-provider fixes to streaming chunks.

// to a simpler post-processing adapter pattern instead of function wrapping.
// ---------------------------------------------------------------------------

// StreamChunkAdapter can transform streaming chunks and tool call deltas.
type StreamChunkAdapter interface {
	// ProcessChoice transforms a single streaming choice delta in-place.
	ProcessChoice(choice *openai.ChatCompletionStreamChoice)
	// FinalizeToolCalls post-processes accumulated tool calls before execution.
	FinalizeToolCalls(calls []openai.ToolCall) []openai.ToolCall
}

// ChainAdapters combines multiple adapters into one.
func ChainAdapters(adapters ...StreamChunkAdapter) StreamChunkAdapter {
	if len(adapters) == 0 {
		return &noopAdapter{}
	}
	if len(adapters) == 1 {
		return adapters[0]
	}
	return &chainedAdapter{adapters: adapters}
}

type noopAdapter struct{}

func (a *noopAdapter) ProcessChoice(_ *openai.ChatCompletionStreamChoice) {}
func (a *noopAdapter) FinalizeToolCalls(calls []openai.ToolCall) []openai.ToolCall {
	return calls
}

type chainedAdapter struct {
	adapters []StreamChunkAdapter
}

func (c *chainedAdapter) ProcessChoice(choice *openai.ChatCompletionStreamChoice) {
	for _, a := range c.adapters {
		a.ProcessChoice(choice)
	}
}

func (c *chainedAdapter) FinalizeToolCalls(calls []openai.ToolCall) []openai.ToolCall {
	for _, a := range c.adapters {
		calls = a.FinalizeToolCalls(calls)
	}
	return calls
}

// ResolveStreamAdapters builds the appropriate adapter chain for the given policy.
func ResolveStreamAdapters(policy TranscriptPolicy) StreamChunkAdapter {
	var adapters []StreamChunkAdapter

	if policy.TrimToolCallNames {
		adapters = append(adapters, &toolCallNameTrimmer{})
	}
	if policy.RepairMalformedToolCallArgs {
		adapters = append(adapters, &malformedArgsRepairer{})
	}
	if policy.DecodeHTMLEntityToolCallArgs {
		adapters = append(adapters, &htmlEntityArgsDecoder{})
	}

	return ChainAdapters(adapters...)
}

// ---------------------------------------------------------------------------
// Adapter 1: ToolCallNameTrimmer
// Trims whitespace from tool call function names in streaming deltas.

// ---------------------------------------------------------------------------

type toolCallNameTrimmer struct{}

func (a *toolCallNameTrimmer) ProcessChoice(choice *openai.ChatCompletionStreamChoice) {
	for i := range choice.Delta.ToolCalls {
		choice.Delta.ToolCalls[i].Function.Name = strings.TrimSpace(choice.Delta.ToolCalls[i].Function.Name)
	}
}

func (a *toolCallNameTrimmer) FinalizeToolCalls(calls []openai.ToolCall) []openai.ToolCall {
	for i := range calls {
		calls[i].Function.Name = strings.TrimSpace(calls[i].Function.Name)
	}
	return calls
}

// ---------------------------------------------------------------------------
// Adapter 2: MalformedArgsRepairer
// Attempts to extract valid JSON from malformed tool call arguments.

// ---------------------------------------------------------------------------

type malformedArgsRepairer struct{}

func (a *malformedArgsRepairer) ProcessChoice(_ *openai.ChatCompletionStreamChoice) {
	// No-op during streaming; repair happens at finalization
}

func (a *malformedArgsRepairer) FinalizeToolCalls(calls []openai.ToolCall) []openai.ToolCall {
	for i := range calls {
		args := calls[i].Function.Arguments
		if args == "" {
			continue
		}
		// Check if args is valid JSON already
		if json.Valid([]byte(args)) {
			continue
		}
		// Attempt to extract balanced JSON prefix
		if repaired := extractBalancedJSONPrefix(args); repaired != "" {
			calls[i].Function.Arguments = repaired
		}
	}
	return calls
}

// extractBalancedJSONPrefix attempts to find a valid JSON object prefix in
// a potentially malformed string. Returns empty string if no valid prefix found.
func extractBalancedJSONPrefix(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "{") {
		return ""
	}
	depth := 0
	inString := false
	escaped := false
	for i, ch := range s {
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch ch {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				candidate := s[:i+1]
				if json.Valid([]byte(candidate)) {
					return candidate
				}
			}
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Adapter 3: HTMLEntityArgsDecoder
// Decodes HTML entities in tool call arguments.

// ---------------------------------------------------------------------------

type htmlEntityArgsDecoder struct{}

func (a *htmlEntityArgsDecoder) ProcessChoice(choice *openai.ChatCompletionStreamChoice) {
	for i := range choice.Delta.ToolCalls {
		args := choice.Delta.ToolCalls[i].Function.Arguments
		if args != "" && containsHTMLEntity(args) {
			choice.Delta.ToolCalls[i].Function.Arguments = html.UnescapeString(args)
		}
	}
}

func (a *htmlEntityArgsDecoder) FinalizeToolCalls(calls []openai.ToolCall) []openai.ToolCall {
	for i := range calls {
		args := calls[i].Function.Arguments
		if args != "" && containsHTMLEntity(args) {
			calls[i].Function.Arguments = html.UnescapeString(args)
		}
	}
	return calls
}

var htmlEntityRe = regexp.MustCompile(`&(?:amp|lt|gt|quot|#\d+|#x[0-9a-fA-F]+);`)

func containsHTMLEntity(s string) bool {
	return htmlEntityRe.MatchString(s)
}
