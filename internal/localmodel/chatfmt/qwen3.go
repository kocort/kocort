package chatfmt

import (
	"fmt"
	"strings"
	"unicode"
)

// ── Qwen3 format ─────────────────────────────────────────────────────────────
// Uses ChatML tags with JSON-based tool calls inside <tool_call> tags.
// Thinking mode is controlled via /think or /no_think in the system message.

// Qwen3 implements the Qwen3 chat format.
type Qwen3 struct{}

var _ Format = (*Qwen3)(nil)

func (q *Qwen3) Name() string         { return "qwen3" }
func (q *Qwen3) StopTokens() []string { return []string{imEnd} }

func (q *Qwen3) Render(messages []Message, tools []Tool, thinking ThinkingMode) (string, error) {
	return renderQwen3(messages, tools, thinking)
}

func (q *Qwen3) NewParser(tools []Tool, lastMsg *Message, thinking ThinkingMode) StreamParser {
	return newQwen3StreamParser(tools, lastMsg, thinking == ThinkingOn)
}

// ── Qwen3 tool system prompt ─────────────────────────────────────────────────

const qwen3ToolSystemPrompt = `# Tools

You are provided with function signatures within <tools></tools> XML tags:
<tools>
%s</tools>

For each function call, return a json object with function name and arguments within <tool_call></tool_call> XML tags:
<tool_call>
{"name": <function-name>, "arguments": <args-json-object>}
</tool_call>`

// ── Qwen3 renderer ──────────────────────────────────────────────────────────

func renderQwen3(messages []Message, tools []Tool, thinking ThinkingMode) (string, error) {
	var sb strings.Builder

	startIdx := 0

	thinkDirective := "\n/no_think"
	if thinking == ThinkingOn {
		thinkDirective = "\n/think"
	}

	// System message with optional tool definitions.
	if len(tools) > 0 {
		sb.WriteString(imStart + "system\n")

		if len(messages) > 0 && messages[0].Role == "system" {
			content := strings.TrimSpace(messages[0].ContentWithImages(0))
			if content != "" {
				sb.WriteString(content + "\n\n")
			}
			startIdx = 1
		}

		var toolsBuf strings.Builder
		for _, tool := range tools {
			b, err := marshalSpaced(tool)
			if err != nil {
				return "", err
			}
			toolsBuf.WriteString("\n")
			toolsBuf.Write(b)
			toolsBuf.WriteString("\n")
		}
		sb.WriteString(fmt.Sprintf(qwen3ToolSystemPrompt, toolsBuf.String()))
		sb.WriteString(thinkDirective)
		sb.WriteString(imEnd + "\n")
	} else if len(messages) > 0 && messages[0].Role == "system" {
		content := strings.TrimSpace(messages[0].ContentWithImages(0))
		sb.WriteString(imStart + "system\n" + content + thinkDirective + imEnd + "\n")
		startIdx = 1
	} else {
		sb.WriteString(imStart + "system" + thinkDirective + imEnd + "\n")
	}

	imgOffset := 0
	for i := 0; i < startIdx; i++ {
		imgOffset += messages[i].ImageCount
	}

	for i := startIdx; i < len(messages); i++ {
		msg := messages[i]
		content := msg.ContentWithImages(imgOffset)
		imgOffset += msg.ImageCount
		content = strings.TrimSpace(content)
		lastMessage := i == len(messages)-1
		prefill := lastMessage && msg.Role == "assistant"

		switch {
		case msg.Role == "user":
			sb.WriteString(imStart + "user\n" + content + imEnd + "\n")

		case msg.Role == "assistant":
			sb.WriteString(imStart + "assistant\n")
			if msg.Reasoning != "" {
				sb.WriteString("<think>\n" + strings.TrimSpace(msg.Reasoning) + "\n</think>\n\n")
			}
			sb.WriteString(content)
			if len(msg.ToolCalls) > 0 {
				for j, tc := range msg.ToolCalls {
					if j == 0 && strings.TrimSpace(content) != "" {
						sb.WriteString("\n\n")
					} else if j > 0 {
						sb.WriteString("\n")
					}
					sb.WriteString(toolCallOpen + "\n")
					sb.WriteString(`{"name": "` + tc.Function.Name + `", "arguments": `)
					sb.WriteString(tc.Function.Arguments)
					sb.WriteString("}\n")
					sb.WriteString(toolCallClose)
				}
			}
			if !prefill {
				sb.WriteString(imEnd + "\n")
			}

		case msg.Role == "tool":
			if i == startIdx || messages[i-1].Role != "tool" {
				sb.WriteString(imStart + "user")
			}
			sb.WriteString("\n<tool_response>\n" + content + "\n</tool_response>")
			if i == len(messages)-1 || messages[i+1].Role != "tool" {
				sb.WriteString(imEnd + "\n")
			}

		case msg.Role == "system":
			sb.WriteString(imStart + "system\n" + content + imEnd + "\n")
		}

		if lastMessage && !prefill {
			sb.WriteString(imStart + "assistant\n")
		}
	}

	return sb.String(), nil
}

// ── Qwen3 stream parser ─────────────────────────────────────────────────────

type q3State int

const (
	q3SeekThinkOpen q3State = iota
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

func newQwen3StreamParser(tools []Tool, lastMsg *Message, thinkingEnabled bool) *qwen3StreamParser {
	p := &qwen3StreamParser{tools: tools}

	assistantPrefill := lastMsg != nil && lastMsg.Role == "assistant" && lastMsg.Content != ""
	if thinkingEnabled && !assistantPrefill {
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
			return evs, false
		}
		p.state = q3CollectContent
		return evs, true

	case q3CollectThinking:
		acc := p.buf.String()
		thinkCloseIdx := strings.Index(acc, thinkClose)
		toolOpenIdx := strings.Index(acc, toolCallOpen)

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

		maxOL := max(suffixPrefixOverlap(acc, thinkClose), suffixPrefixOverlap(acc, toolCallOpen))
		if maxOL > 0 {
			beforePartial := acc[:len(acc)-maxOL]
			tw := trailingWSLen(beforePartial)
			ambStart := len(beforePartial) - tw
			unambiguous := acc[:ambStart]
			ambiguous := acc[ambStart:]
			p.buf.Reset()
			p.buf.WriteString(ambiguous)
			if len(unambiguous) > 0 {
				evs = append(evs, parsedEvent{kind: "thinking", data: unambiguous})
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
			tw := trailingWSLen(beforePartial)
			ambStart := len(beforePartial) - tw
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
