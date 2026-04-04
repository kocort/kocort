package llamawrapper

import (
	"strings"
	"unicode"
)

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

func newQwen35StreamParser(tools []Tool, lastMsg *renderMsg, thinkingEnabled bool) *qwen35StreamParser {
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
	events := p.parse()

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

func (p *toolStreamParser) parse() []parsedEvent {
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
			trailingWS := trailingWSLen(beforePartial)
			ambStart := len(beforePartial) - trailingWS
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
