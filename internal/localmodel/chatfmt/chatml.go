package chatfmt

import (
	"strings"
	"unicode"
)

// ── Shared constants for ChatML-family formats ───────────────────────────────

const (
	imStart = "<|im_start|>"
	imEnd   = "<|im_end|>"

	thinkOpen  = "<think>"
	thinkClose = "</think>"

	toolCallOpen  = "<tool_call>"
	toolCallClose = "</tool_call>"
)

// ── ChatML format ────────────────────────────────────────────────────────────

// ChatML implements the ChatML format (default/legacy).
type ChatML struct{}

var _ Format = (*ChatML)(nil)

func (c *ChatML) Name() string         { return "chatml" }
func (c *ChatML) StopTokens() []string { return []string{imEnd} }

func (c *ChatML) Render(messages []Message, tools []Tool, thinking ThinkingMode) (string, error) {
	var sb strings.Builder
	for _, msg := range messages {
		sb.WriteString(imStart)
		sb.WriteString(msg.Role)
		sb.WriteByte('\n')
		sb.WriteString(msg.Content)
		sb.WriteByte('\n')
		sb.WriteString(imEnd)
		sb.WriteByte('\n')
	}
	sb.WriteString(imStart)
	sb.WriteString("assistant\n")

	switch thinking {
	case ThinkingOn:
		sb.WriteString(thinkOpen + "\n")
	case ThinkingDisabled:
		sb.WriteString(thinkOpen + "\n" + thinkClose + "\n\n")
	}

	return sb.String(), nil
}

func (c *ChatML) NewParser(tools []Tool, lastMsg *Message, thinking ThinkingMode) StreamParser {
	return newLegacyParser(thinking == ThinkingOn)
}

// ── Legacy stream parser (ChatML / non-Qwen) ────────────────────────────────

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
			tw := trailingWSLen(beforePartial)
			ambStart := len(beforePartial) - tw
			unambiguous := acc[:ambStart]
			ambiguous := acc[ambStart:]
			p.acc.Reset()
			p.acc.WriteString(ambiguous)
			return unambiguous, "", false
		}
		tw := trailingWSLen(acc)
		ambStart := len(acc) - tw
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
		tw := trailingWSLen(acc)
		ambStart := len(acc) - tw
		unambiguous := acc[:ambStart]
		ambiguous := acc[ambStart:]
		p.acc.Reset()
		p.acc.WriteString(ambiguous)
		return "", unambiguous, false
	}
	return "", "", false
}
