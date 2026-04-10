package chatfmt

import (
	"strings"
	"unicode"
)

// ── Qwen3.5 format ──────────────────────────────────────────────────────────
// Uses ChatML tags with XML-based tool calls (<function=name>) inside
// <tool_call> tags. Thinking uses <think>/<think> tags that are always present.

// Qwen35 implements the Qwen3.5 chat format.
type Qwen35 struct{}

var _ Format = (*Qwen35)(nil)

func (q *Qwen35) Name() string         { return "qwen3.5" }
func (q *Qwen35) StopTokens() []string { return []string{imEnd} }

func (q *Qwen35) Render(messages []Message, tools []Tool, thinking ThinkingMode) (string, error) {
	return renderQwen35(messages, tools, thinking)
}

func (q *Qwen35) NewParser(tools []Tool, lastMsg *Message, thinking ThinkingMode) StreamParser {
	return newQwen35StreamParser(tools, lastMsg, thinking == ThinkingOn)
}

// ── Qwen3.5 tool preamble/postamble ─────────────────────────────────────────

const qwen35ToolPostamble = `
</tools>

If you choose to call a function ONLY reply in the following format with NO suffix:

<tool_call>
<function=example_function_name>
<parameter=example_parameter_1>
value_1
</parameter>
<parameter=example_parameter_2>
This is the value for the second parameter
that can span
multiple lines
</parameter>
</function>
</tool_call>

<IMPORTANT>
Reminder:
- Function calls MUST follow the specified format: an inner <function=...></function> block must be nested within <tool_call></tool_call> XML tags
- Required parameters MUST be specified
- You may provide optional reasoning for your function call in natural language BEFORE the function call, but NOT after
- If there is no function call available, answer the question like normal with your current knowledge and do not tell the user about function calls
</IMPORTANT>`

// ── Qwen3.5 renderer ────────────────────────────────────────────────────────

func renderQwen35(messages []Message, tools []Tool, thinking ThinkingMode) (string, error) {
	var sb strings.Builder

	thinkingEnabled := thinking == ThinkingOn

	// System message with tools.
	if len(tools) > 0 {
		sb.WriteString(imStart + "system\n")
		sb.WriteString("# Tools\n\nYou have access to the following functions:\n\n<tools>")
		for _, tool := range tools {
			sb.WriteString("\n")
			b, err := marshalSpaced(tool)
			if err != nil {
				return "", err
			}
			sb.Write(b)
		}
		sb.WriteString(qwen35ToolPostamble)
		if len(messages) > 0 && messages[0].Role == "system" {
			content := messages[0].ContentWithImages(0)
			content = strings.TrimSpace(content)
			if content != "" {
				sb.WriteString("\n\n" + content)
			}
		}
		sb.WriteString(imEnd + "\n")
	} else if len(messages) > 0 && messages[0].Role == "system" {
		content := messages[0].ContentWithImages(0)
		sb.WriteString(imStart + "system\n" + strings.TrimSpace(content) + imEnd + "\n")
	}

	// Find last real user query index (for tool-step detection).
	lastQueryIdx := len(messages) - 1
	multiStepTool := true
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if multiStepTool && msg.Role == "user" {
			content := strings.TrimSpace(msg.ContentWithImages(0))
			if !(strings.HasPrefix(content, "<tool_response>") && strings.HasSuffix(content, "</tool_response>")) {
				multiStepTool = false
				lastQueryIdx = i
			}
		}
	}

	imgOffset := 0
	for i, msg := range messages {
		content := msg.ContentWithImages(imgOffset)
		imgOffset += msg.ImageCount
		content = strings.TrimSpace(content)
		lastMessage := i == len(messages)-1
		prefill := lastMessage && msg.Role == "assistant"

		switch {
		case msg.Role == "user" || (msg.Role == "system" && i != 0):
			sb.WriteString(imStart + msg.Role + "\n" + content + imEnd + "\n")

		case msg.Role == "assistant":
			reasoning, c := splitReasoning(content, msg.Reasoning, thinkingEnabled)
			if thinkingEnabled && i > lastQueryIdx {
				sb.WriteString(imStart + "assistant\n<think>\n" + reasoning + "\n</think>\n\n" + c)
			} else {
				sb.WriteString(imStart + "assistant\n" + c)
			}
			if len(msg.ToolCalls) > 0 {
				for j, tc := range msg.ToolCalls {
					if j == 0 && strings.TrimSpace(c) != "" {
						sb.WriteString("\n\n")
					} else if j > 0 {
						sb.WriteString("\n")
					}
					sb.WriteString("<tool_call>\n<function=" + tc.Function.Name + ">\n")
					args, err := parseOrderedArgs(tc.Function.Arguments)
					if err != nil {
						return "", err
					}
					for _, arg := range args {
						sb.WriteString("<parameter=" + arg.Name + ">\n")
						sb.WriteString(formatArgValue(arg.Value))
						sb.WriteString("\n</parameter>\n")
					}
					sb.WriteString("</function>\n</tool_call>")
				}
			}
			if !prefill {
				sb.WriteString(imEnd + "\n")
			}

		case msg.Role == "tool":
			if i == 0 || messages[i-1].Role != "tool" {
				sb.WriteString(imStart + "user")
			}
			sb.WriteString("\n<tool_response>\n" + content + "\n</tool_response>")
			if i == len(messages)-1 || messages[i+1].Role != "tool" {
				sb.WriteString(imEnd + "\n")
			}
		}

		if lastMessage && !prefill {
			sb.WriteString(imStart + "assistant\n")
			if thinkingEnabled {
				sb.WriteString("<think>\n")
			} else {
				sb.WriteString("<think>\n\n</think>\n\n")
			}
		}
	}

	return sb.String(), nil
}

// ── Qwen3.5 stream parser ───────────────────────────────────────────────────

type q35State int

const (
	q35CollectThinking q35State = iota
	q35ThinkDoneEatWS
	q35CollectContent
)

type qwen35StreamParser struct {
	toolParser toolStreamParser
	state      q35State
	buf        strings.Builder

	allowLeadingThinkOpen bool
}

func newQwen35StreamParser(tools []Tool, lastMsg *Message, thinkingEnabled bool) *qwen35StreamParser {
	p := &qwen35StreamParser{}
	p.toolParser = toolStreamParser{tools: tools}

	assistantPrefill := lastMsg != nil && lastMsg.Role == "assistant" && lastMsg.Content != ""
	if thinkingEnabled && !assistantPrefill {
		p.state = q35CollectThinking
		p.allowLeadingThinkOpen = true
	} else {
		p.state = q35CollectContent
	}
	return p
}

func (p *qwen35StreamParser) Add(s string) (string, string, []ToolCall) {
	p.buf.WriteString(s)
	events := p.parse()

	var contentSb, thinkSb strings.Builder
	var calls []ToolCall
	for _, ev := range events {
		switch ev.kind {
		case "content":
			c, tc := p.toolParser.Add(ev.data)
			contentSb.WriteString(c)
			calls = append(calls, tc...)
		case "thinking":
			thinkSb.WriteString(ev.data)
		}
	}
	return thinkSb.String(), contentSb.String(), calls
}

func (p *qwen35StreamParser) parse() []parsedEvent {
	var all []parsedEvent
	cont := true
	for cont {
		var evs []parsedEvent
		evs, cont = p.eat()
		all = append(all, evs...)
	}
	return all
}

func (p *qwen35StreamParser) splitTag(tag string, trimAfter bool) (string, string) {
	return splitAtTag(&p.buf, tag, trimAfter)
}

func (p *qwen35StreamParser) eatWSAndTransition(next q35State) ([]parsedEvent, bool) {
	trimmed := strings.TrimLeftFunc(p.buf.String(), unicode.IsSpace)
	p.buf.Reset()
	if trimmed == "" {
		return nil, false
	}
	p.state = next
	p.buf.WriteString(trimmed)
	return nil, true
}

func (p *qwen35StreamParser) maybeConsumeLeadingThinkOpen(acc string) (handled, cont bool) {
	if !p.allowLeadingThinkOpen {
		return false, false
	}
	trimmed := strings.TrimLeftFunc(acc, unicode.IsSpace)
	if strings.HasPrefix(trimmed, thinkOpen) {
		after := strings.TrimPrefix(trimmed, thinkOpen)
		after = strings.TrimLeftFunc(after, unicode.IsSpace)
		p.buf.Reset()
		p.buf.WriteString(after)
		if after == "" {
			return true, false
		}
		p.allowLeadingThinkOpen = false
		return true, true
	}
	if strings.HasPrefix(thinkOpen, trimmed) {
		return true, false
	}
	p.allowLeadingThinkOpen = false
	return false, false
}

func (p *qwen35StreamParser) eat() ([]parsedEvent, bool) {
	var evs []parsedEvent

	switch p.state {
	case q35CollectThinking:
		acc := p.buf.String()
		if handled, cont := p.maybeConsumeLeadingThinkOpen(acc); handled {
			return evs, cont
		}

		if strings.Contains(acc, thinkClose) {
			thinking, remaining := p.splitTag(thinkClose, true)
			if len(thinking) > 0 {
				evs = append(evs, parsedEvent{kind: "thinking", data: thinking})
			}
			if remaining == "" {
				p.state = q35ThinkDoneEatWS
			} else {
				p.state = q35CollectContent
			}
			return evs, true
		}

		if ol := suffixPrefixOverlap(acc, thinkClose); ol > 0 {
			beforePartial := acc[:len(acc)-ol]
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

	case q35ThinkDoneEatWS:
		return p.eatWSAndTransition(q35CollectContent)

	case q35CollectContent:
		if p.buf.Len() == 0 {
			return evs, false
		}
		content := p.buf.String()
		p.buf.Reset()
		evs = append(evs, parsedEvent{kind: "content", data: content})
		return evs, false
	}
	return evs, false
}

// ── Tool-call stream parser (Qwen <tool_call> blocks) ────────────────────────

type toolParserState int

const (
	toolScanForOpen toolParserState = iota
	toolCollecting
)

type toolStreamParser struct {
	state     toolParserState
	acc       strings.Builder
	tools     []Tool
	callIndex int
}

func (p *toolStreamParser) Add(s string) (string, []ToolCall) {
	p.acc.WriteString(s)
	events := p.parseTool()

	var sb strings.Builder
	var calls []ToolCall
	for _, ev := range events {
		switch ev.kind {
		case "content":
			sb.WriteString(ev.data)
		case "tool":
			call, err := parseToolCallXML(ev.data, p.tools)
			if err != nil {
				sb.WriteString(toolCallOpen)
				sb.WriteString(ev.data)
				sb.WriteString(toolCallClose)
				continue
			}
			call.Index = p.callIndex
			p.callIndex++
			calls = append(calls, call)
		}
	}
	return sb.String(), calls
}

func (p *toolStreamParser) parseTool() []parsedEvent {
	var all []parsedEvent
	cont := true
	for cont {
		var evs []parsedEvent
		evs, cont = p.eatTool()
		all = append(all, evs...)
	}
	return all
}

func (p *toolStreamParser) eatTool() ([]parsedEvent, bool) {
	var evs []parsedEvent
	switch p.state {
	case toolScanForOpen:
		if strings.Contains(p.acc.String(), toolCallOpen) {
			before, after := splitAtTag(&p.acc, toolCallOpen, false)
			if len(before) > 0 {
				evs = append(evs, parsedEvent{kind: "content", data: before})
			}
			p.acc.Reset()
			p.acc.WriteString(after)
			p.state = toolCollecting
			return evs, true
		}

		if ol := suffixPrefixOverlap(p.acc.String(), toolCallOpen); ol > 0 {
			beforePartial := p.acc.String()[:len(p.acc.String())-ol]
			tw := trailingWSLen(beforePartial)
			ambStart := len(beforePartial) - tw
			unambiguous := p.acc.String()[:ambStart]
			ambiguous := p.acc.String()[ambStart:]
			p.acc.Reset()
			p.acc.WriteString(ambiguous)
			if len(unambiguous) > 0 {
				evs = append(evs, parsedEvent{kind: "content", data: unambiguous})
			}
			return evs, false
		}

		wsLen := trailingWSLen(p.acc.String())
		ambStart := len(p.acc.String()) - wsLen
		unambiguous := p.acc.String()[:ambStart]
		ambiguous := p.acc.String()[ambStart:]
		p.acc.Reset()
		p.acc.WriteString(ambiguous)
		if len(unambiguous) > 0 {
			evs = append(evs, parsedEvent{kind: "content", data: unambiguous})
		}
		return evs, false

	case toolCollecting:
		if strings.Contains(p.acc.String(), toolCallClose) {
			before, after := splitAtTag(&p.acc, toolCallClose, true)
			p.acc.Reset()
			p.acc.WriteString(after)
			evs = append(evs, parsedEvent{kind: "tool", data: before})
			p.state = toolScanForOpen
			return evs, true
		}
		return evs, false
	}
	return evs, false
}
