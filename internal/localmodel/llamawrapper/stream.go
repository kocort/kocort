package llamawrapper

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ── Stream parser ────────────────────────────────────────────────────────────
// Handles streaming output, separating thinking / content / tool_call blocks.

type streamParser struct {
	qwen35 *qwen35StreamParser
	qwen3  *qwen3StreamParser
	gemma4 *gemma4StreamParser
	legacy *legacyStreamParser
}

func newStreamParser(renderer string, tools []Tool, messages []renderMsg, thinkingEnabled bool) *streamParser {
	switch renderer {
	case "qwen3.5":
		var lastMsg *renderMsg
		if len(messages) > 0 {
			lastMsg = &messages[len(messages)-1]
		}
		return &streamParser{
			qwen35: newQwen35StreamParser(tools, lastMsg, thinkingEnabled),
		}
	case "qwen3":
		var lastMsg *renderMsg
		if len(messages) > 0 {
			lastMsg = &messages[len(messages)-1]
		}
		return &streamParser{
			qwen3: newQwen3StreamParser(tools, lastMsg, thinkingEnabled),
		}
	case "gemma4":
		var lastMsg *renderMsg
		if len(messages) > 0 {
			lastMsg = &messages[len(messages)-1]
		}
		return &streamParser{
			gemma4: newGemma4StreamParser(tools, lastMsg, thinkingEnabled),
		}
	default:
		return &streamParser{
			legacy: newLegacyParser(thinkingEnabled),
		}
	}
}

func (p *streamParser) Add(content string) (thinking, remaining string, toolCalls []ToolCall) {
	if p == nil {
		return "", content, nil
	}
	if p.qwen35 != nil {
		return p.qwen35.Add(content)
	}
	if p.qwen3 != nil {
		return p.qwen3.Add(content)
	}
	if p.gemma4 != nil {
		return p.gemma4.Add(content)
	}
	return p.legacy.Add(content)
}

// ── Shared types ─────────────────────────────────────────────────────────────

type parsedEvent struct {
	kind string // "content", "thinking", or "tool"
	data string
}

// ── Shared helpers ───────────────────────────────────────────────────────────

func splitAtTag(sb *strings.Builder, tag string, trimAfter bool) (string, string) {
	split := strings.SplitN(sb.String(), tag, 2)
	if len(split) == 1 {
		sb.Reset()
		return split[0], ""
	}
	before := strings.TrimRightFunc(split[0], unicode.IsSpace)
	after := split[1]
	if trimAfter {
		after = strings.TrimLeftFunc(after, unicode.IsSpace)
	}
	sb.Reset()
	sb.WriteString(after)
	return before, after
}

// trailingWSLenStr returns the byte length of trailing whitespace in s.
func trailingWSLenStr(s string) int {
	remaining := s
	total := 0
	for len(remaining) > 0 {
		r, size := utf8.DecodeLastRuneInString(remaining)
		if r == utf8.RuneError && size == 1 {
			break
		}
		if !unicode.IsSpace(r) {
			break
		}
		total += size
		remaining = remaining[:len(remaining)-size]
	}
	return total
}
