package llamawrapper

import (
	"strings"
	"unicode"
)

// ── Qwen3 stream parser ─────────────────────────────────────────────────────
// Handles <think>/<think> for reasoning and <tool_call>/<tool_call> with JSON
// tool calls. Qwen3 differs from Qwen3.5 because:
//   - Thinking is controlled via /think or /no_think in the system prompt
//   - When thinking is enabled, the model outputs <think>...</think> itself
//     (not pre-filled in the prompt). So the parser first seeks <think> opening.
//   - Tool calls are JSON: <tool_call>{"name":"fn","arguments":{...}}</tool_call>
//   - Tool calls can appear inside thinking (ends thinking) or in normal content

type q3State int

const (
	q3SeekThinkOpen q3State = iota // looking for <think> at start of output
	q3CollectThinking
	q3ThinkDoneEatWS
	q3CollectContent
	q3ToolEatWS
	q3CollectTool
)

type qwen3StreamParser struct {
	state     q3State
	buf       strings.Builder
	tools     []Tool
	callIndex int
}

func newQwen3StreamParser(tools []Tool, lastMsg *renderMsg, thinkingEnabled bool) *qwen3StreamParser {
	p := &qwen3StreamParser{tools: tools}

	assistantPrefill := lastMsg != nil && lastMsg.Role == "assistant" && lastMsg.Content != ""
	if thinkingEnabled && !assistantPrefill {
		// Model will output <think> on its own, so first seek the opening tag.
		p.state = q3SeekThinkOpen
	} else {
		p.state = q3CollectContent
	}
	return p
}

func (p *qwen3StreamParser) Add(s string) (string, string, []ToolCall) {
	p.buf.WriteString(s)
	events := p.parseAll()

	var contentSb, thinkSb strings.Builder
	var calls []ToolCall
	for _, ev := range events {
		switch ev.kind {
		case "content":
			contentSb.WriteString(ev.data)
		case "thinking":
			thinkSb.WriteString(ev.data)
		case "tool":
			call, err := parseToolCallJSON(ev.data, p.tools)
			if err != nil {
				// Failed to parse — emit as plain content.
				contentSb.WriteString(toolCallOpen)
				contentSb.WriteString(ev.data)
				contentSb.WriteString(toolCallClose)
				continue
			}
			call.Index = p.callIndex
			p.callIndex++
			calls = append(calls, call)
		}
	}
	return thinkSb.String(), contentSb.String(), calls
}

func (p *qwen3StreamParser) parseAll() []parsedEvent {
	var all []parsedEvent
	cont := true
	for cont {
		var evs []parsedEvent
		evs, cont = p.eat()
		all = append(all, evs...)
	}
	return all
}

func (p *qwen3StreamParser) eat() ([]parsedEvent, bool) {
	var evs []parsedEvent

	switch p.state {
	case q3SeekThinkOpen:
		// The model should output <think> as its first token(s) when thinking
		// is enabled via /think. We wait for it, strip it, and transition to
		// collecting thinking content.
		trimmed := strings.TrimLeftFunc(p.buf.String(), unicode.IsSpace)
		if strings.HasPrefix(trimmed, thinkOpen) {
			after := strings.TrimPrefix(trimmed, thinkOpen)
			after = strings.TrimLeftFunc(after, unicode.IsSpace)
			p.buf.Reset()
			p.buf.WriteString(after)
			if after == "" {
				p.state = q3CollectThinking
				return evs, false
			}
			p.state = q3CollectThinking
			return evs, true
		}
		if strings.HasPrefix(thinkOpen, trimmed) || trimmed == "" {
			// Partial match or only whitespace — buffer more.
			return evs, false
		}
		// No <think> found; model didn't think despite /think. Treat as content.
		p.state = q3CollectContent
		return evs, true

	case q3CollectThinking:
		acc := p.buf.String()
		thinkCloseIdx := strings.Index(acc, thinkClose)
		toolOpenIdx := strings.Index(acc, toolCallOpen)

		// If <tool_call> appears before </think>, treat as end of thinking.
		if toolOpenIdx != -1 && (thinkCloseIdx == -1 || toolOpenIdx < thinkCloseIdx) {
			before, after := splitAtTag(&p.buf, toolCallOpen, true)
			if len(before) > 0 {
				evs = append(evs, parsedEvent{kind: "thinking", data: before})
			}
			if after == "" {
				p.state = q3ToolEatWS
			} else {
				p.state = q3CollectTool
			}
			return evs, true
		}

		if thinkCloseIdx != -1 {
			thinking, remaining := splitAtTag(&p.buf, thinkClose, true)
			if len(thinking) > 0 {
				evs = append(evs, parsedEvent{kind: "thinking", data: thinking})
			}
			if remaining == "" {
				p.state = q3ThinkDoneEatWS
			} else {
				p.state = q3CollectContent
			}
			return evs, true
		}

		// Check for partial tag overlap.
		maxOL := max(suffixPrefixOverlap(acc, thinkClose), suffixPrefixOverlap(acc, toolCallOpen))
		if maxOL > 0 {
			beforePartial := acc[:len(acc)-maxOL]
			trailingWS := trailingWSLen(beforePartial)
			ambStart := len(beforePartial) - trailingWS
			unambiguous := acc[:ambStart]
			ambiguous := acc[ambStart:]
			p.buf.Reset()
			p.buf.WriteString(ambiguous)
			if len(unambiguous) > 0 {
				evs = append(evs, parsedEvent{kind: "thinking", data: unambiguous})
			}
			return evs, false
		}

		// Emit unambiguous thinking content, keep trailing whitespace buffered.
		wsLen := trailingWSLen(acc)
		ambStart := len(acc) - wsLen
		unambiguous := acc[:ambStart]
		ambiguous := acc[ambStart:]
		p.buf.Reset()
		p.buf.WriteString(ambiguous)
		if len(unambiguous) > 0 {
			evs = append(evs, parsedEvent{kind: "thinking", data: unambiguous})
		}
		return evs, false

	case q3ThinkDoneEatWS:
		trimmed := strings.TrimLeftFunc(p.buf.String(), unicode.IsSpace)
		p.buf.Reset()
		if trimmed == "" {
			return evs, false
		}
		p.state = q3CollectContent
		p.buf.WriteString(trimmed)
		return evs, true

	case q3CollectContent:
		acc := p.buf.String()

		// Look for <tool_call>.
		if strings.Contains(acc, toolCallOpen) {
			before, after := splitAtTag(&p.buf, toolCallOpen, true)
			if len(before) > 0 {
				evs = append(evs, parsedEvent{kind: "content", data: before})
			}
			if after == "" {
				p.state = q3ToolEatWS
			} else {
				p.state = q3CollectTool
			}
			return evs, true
		}

		if ol := suffixPrefixOverlap(acc, toolCallOpen); ol > 0 {
			beforePartial := acc[:len(acc)-ol]
			trailingWS := trailingWSLen(beforePartial)
			ambStart := len(beforePartial) - trailingWS
			unambiguous := acc[:ambStart]
			ambiguous := acc[ambStart:]
			p.buf.Reset()
			p.buf.WriteString(ambiguous)
			if len(unambiguous) > 0 {
				evs = append(evs, parsedEvent{kind: "content", data: unambiguous})
			}
			return evs, false
		}

		wsLen := trailingWSLen(acc)
		ambStart := len(acc) - wsLen
		unambiguous := acc[:ambStart]
		ambiguous := acc[ambStart:]
		p.buf.Reset()
		p.buf.WriteString(ambiguous)
		if len(unambiguous) > 0 {
			evs = append(evs, parsedEvent{kind: "content", data: unambiguous})
		}
		return evs, false

	case q3ToolEatWS:
		trimmed := strings.TrimLeftFunc(p.buf.String(), unicode.IsSpace)
		p.buf.Reset()
		if trimmed == "" {
			return evs, false
		}
		p.state = q3CollectTool
		p.buf.WriteString(trimmed)
		return evs, true

	case q3CollectTool:
		acc := p.buf.String()
		if strings.Contains(acc, toolCallClose) {
			toolContent, _ := splitAtTag(&p.buf, toolCallClose, true)
			if len(toolContent) > 0 {
				evs = append(evs, parsedEvent{kind: "tool", data: toolContent})
			}
			p.state = q3CollectContent
			return evs, true
		}
		return evs, false
	}
	return evs, false
}
