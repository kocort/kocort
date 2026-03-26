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
	return p.legacy.Add(content)
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

type parsedEvent struct {
	kind string // "content" or "thinking"
	data string
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

// ── Legacy stream parser (non-Qwen3.5) ──────────────────────────────────────

type legacyState int

const (
	legacySeekThinkOpen legacyState = iota
	legacyThinkOpenEatWS
	legacyCollecting
	legacyThinkCloseEatWS
	legacyContent
)

type legacyStreamParser struct {
	state legacyState
	acc   strings.Builder
}

func newLegacyParser(thinkingEnabled bool) *legacyStreamParser {
	p := &legacyStreamParser{}
	if thinkingEnabled {
		p.state = legacySeekThinkOpen
	} else {
		p.state = legacyContent
	}
	return p
}

func (p *legacyStreamParser) Add(content string) (thinking, remaining string, toolCalls []ToolCall) {
	p.acc.WriteString(content)
	var thinkBuf, contentBuf strings.Builder
	cont := true
	for cont {
		var t, c string
		t, c, cont = p.eat()
		thinkBuf.WriteString(t)
		contentBuf.WriteString(c)
	}
	return thinkBuf.String(), contentBuf.String(), nil
}

func (p *legacyStreamParser) eat() (thinking, content string, cont bool) {
	switch p.state {
	case legacySeekThinkOpen:
		trimmed := strings.TrimLeftFunc(p.acc.String(), unicode.IsSpace)
		if strings.HasPrefix(trimmed, thinkOpen) {
			after := strings.TrimLeftFunc(trimmed[len(thinkOpen):], unicode.IsSpace)
			p.acc.Reset()
			p.acc.WriteString(after)
			if after == "" {
				p.state = legacyThinkOpenEatWS
			} else {
				p.state = legacyCollecting
			}
			return "", "", true
		}
		if strings.HasPrefix(thinkOpen, trimmed) || trimmed == "" {
			return "", "", false
		}
		p.state = legacyContent
		return "", "", true

	case legacyThinkOpenEatWS:
		trimmed := strings.TrimLeftFunc(p.acc.String(), unicode.IsSpace)
		p.acc.Reset()
		if trimmed == "" {
			return "", "", false
		}
		p.state = legacyCollecting
		p.acc.WriteString(trimmed)
		return "", "", true

	case legacyCollecting:
		acc := p.acc.String()
		if strings.Contains(acc, thinkClose) {
			before, after := splitAtTag(&p.acc, thinkClose, true)
			if after == "" {
				p.state = legacyThinkCloseEatWS
			} else {
				p.state = legacyContent
			}
			return before, "", true
		}
		if ol := suffixPrefixOverlap(acc, thinkClose); ol > 0 {
			beforePartial := acc[:len(acc)-ol]
			trailingWS := trailingWSLen(beforePartial)
			ambStart := len(beforePartial) - trailingWS
			unambiguous := acc[:ambStart]
			ambiguous := acc[ambStart:]
			p.acc.Reset()
			p.acc.WriteString(ambiguous)
			return unambiguous, "", false
		}
		trailingWS := trailingWSLen(acc)
		ambStart := len(acc) - trailingWS
		unambiguous := acc[:ambStart]
		ambiguous := acc[ambStart:]
		p.acc.Reset()
		p.acc.WriteString(ambiguous)
		return unambiguous, "", false

	case legacyThinkCloseEatWS:
		trimmed := strings.TrimLeftFunc(p.acc.String(), unicode.IsSpace)
		p.acc.Reset()
		if trimmed == "" {
			return "", "", false
		}
		p.state = legacyContent
		p.acc.WriteString(trimmed)
		return "", "", true

	case legacyContent:
		acc := p.acc.String()
		trailingWS := trailingWSLen(acc)
		ambStart := len(acc) - trailingWS
		unambiguous := acc[:ambStart]
		ambiguous := acc[ambStart:]
		p.acc.Reset()
		p.acc.WriteString(ambiguous)
		return "", unambiguous, false
	}
	return "", "", false
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

// trailingWSLen2 is an alias using the shared implementation.
// (We already have trailingWSLen in thinking.go.)
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
