// Package chatfmt provides unified chat template rendering and stream parsing
// for different LLM model families. Each Format implementation combines prompt
// rendering and streaming output parsing into a single cohesive unit.
package chatfmt

import "strings"

// ── Thinking mode ────────────────────────────────────────────────────────────

// ThinkingMode controls how thinking/reasoning is handled during rendering.
type ThinkingMode int

const (
	// ThinkingOff means no thinking tags are inserted (engine default off,
	// no explicit request override).
	ThinkingOff ThinkingMode = iota

	// ThinkingOn enables thinking (e.g., <think>, /think, <|channel>thought).
	ThinkingOn

	// ThinkingDisabled explicitly disables thinking. Some formats insert
	// empty think blocks (e.g., <think></think>) to suppress reasoning.
	ThinkingDisabled
)

// ── Interfaces ───────────────────────────────────────────────────────────────

// Format defines a chat template format that can render prompts and parse
// streaming output. Each supported model family (ChatML, Qwen3, Qwen3.5,
// Gemma4) provides an implementation.
type Format interface {
	// Name returns the format identifier (e.g., "chatml", "qwen3", "qwen3.5", "gemma4").
	Name() string

	// StopTokens returns the stop sequences for this format.
	StopTokens() []string

	// Render converts messages into a prompt string for the model.
	Render(messages []Message, tools []Tool, thinking ThinkingMode) (string, error)

	// NewParser creates a stream parser for separating thinking/content/tool_calls
	// from the model's streaming output.
	NewParser(tools []Tool, lastMsg *Message, thinking ThinkingMode) StreamParser
}

// StreamParser processes streaming token output, separating thinking,
// content, and tool calls.
type StreamParser interface {
	// Add processes a token piece and returns separated components.
	Add(content string) (thinking, contentOut string, toolCalls []ToolCall)
}

// Tokenizer is satisfied by any type that can tokenize text into token IDs.
type Tokenizer interface {
	Tokenize(text string) ([]int, error)
}

// ── Format detection ─────────────────────────────────────────────────────────

// Detect selects the appropriate Format based on the model's embedded Jinja2
// chat template string and/or the GGUF general.architecture value.
// The chat template is preferred because it is a stronger signal than the
// architecture name.
func Detect(chatTemplate, modelArch string) Format {
	if chatTemplate != "" {
		tpl := strings.ToLower(chatTemplate)
		switch {
		case strings.Contains(tpl, "<|channel>") && strings.Contains(tpl, "<turn|>"):
			return &Gemma4{}
		case strings.Contains(tpl, "<function="):
			return &Qwen35{}
		case strings.Contains(tpl, "/think") && strings.Contains(tpl, "<tool_call>"):
			return &Qwen3{}
		}
	}

	// Fallback to GGUF architecture metadata.
	return byArch(modelArch)
}

// byArch returns a Format based on the GGUF general.architecture value.
func byArch(arch string) Format {
	switch arch {
	case "qwen35", "qwen35moe":
		return &Qwen35{}
	case "qwen3", "qwen3moe":
		return &Qwen3{}
	case "gemma4":
		return &Gemma4{}
	default:
		return &ChatML{}
	}
}

// ── Context-aware truncation ─────────────────────────────────────────────────

// TruncateAndRender performs context-aware message truncation, then renders
// the final prompt. It tries progressively shorter message windows (always
// preserving system messages) until the tokenized prompt fits within ctxLen.
func TruncateAndRender(
	f Format,
	messages []Message,
	tools []Tool,
	thinking ThinkingMode,
	tok Tokenizer,
	ctxLen int,
	hasVision bool,
) (rendered []Message, prompt string, err error) {
	lastIdx := len(messages) - 1
	currIdx := 0

	for i := 0; i <= lastIdx; i++ {
		// Preserve system messages that precede the truncation point.
		var system []Message
		for j := 0; j < i; j++ {
			if messages[j].Role == "system" {
				system = append(system, messages[j])
			}
		}
		candidate := append(append([]Message{}, system...), messages[i:]...)
		p, err := f.Render(candidate, tools, thinking)
		if err != nil {
			return nil, "", err
		}
		tokens, err := tok.Tokenize(p)
		if err != nil {
			return nil, "", err
		}
		tokenLen := len(tokens)
		if hasVision {
			for _, msg := range candidate {
				tokenLen += 768 * msg.ImageCount
			}
		}
		if tokenLen <= ctxLen {
			currIdx = i
			break
		}
		if i == lastIdx {
			currIdx = lastIdx
			break
		}
	}

	var system []Message
	for i := 0; i < currIdx; i++ {
		if messages[i].Role == "system" {
			system = append(system, messages[i])
		}
	}
	rendered = append(append([]Message{}, system...), messages[currIdx:]...)
	prompt, err = f.Render(rendered, tools, thinking)
	if err != nil {
		return nil, "", err
	}
	return rendered, prompt, nil
}
