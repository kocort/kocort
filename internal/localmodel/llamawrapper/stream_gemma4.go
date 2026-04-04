package llamawrapper

import (
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"unicode"
)

// ── Gemma 4 stream parser ────────────────────────────────────────────────────
// Handles <|channel>thought...<channel|> for thinking and
// <|tool_call>call:NAME{...}<tool_call|> for tool calls.

type g4State int

const (
	g4CollectContent  g4State = iota // normal content
	g4CollectThinking                // inside <|channel>...<channel|>
	g4CollectToolCall                // inside <|tool_call>...<tool_call|>
)

const (
	g4ThinkOpen  = "<|channel>"
	g4ThinkClose = "<channel|>"
)

var (
	g4QuotedStringRe = regexp.MustCompile(`(?s)<\|"\|>(.*?)<\|"\|>`)
	g4BareKeyRe      = regexp.MustCompile(`([,{])(\w+):`)
)

type gemma4StreamParser struct {
	state                 g4State
	buf                   strings.Builder
	thinkingEnabled       bool
	needsChannelNameStrip bool // strip "thought\n" after entering channel
	callIndex             int
}

func newGemma4StreamParser(tools []Tool, lastMsg *renderMsg, thinkingEnabled bool) *gemma4StreamParser {
	p := &gemma4StreamParser{
		thinkingEnabled: thinkingEnabled,
		state:           g4CollectContent,
	}
	// When thinking is enabled, the prompt already ends with
	// "<|channel>thought\n", so the model's first output tokens are
	// thinking content — start directly in thinking state.
	if thinkingEnabled {
		p.state = g4CollectThinking
	}
	return p
}

func (p *gemma4StreamParser) Add(s string) (thinking, content string, toolCalls []ToolCall) {
	p.buf.WriteString(s)

	var thinkSb, contentSb strings.Builder
	var calls []ToolCall

	cont := true
	for cont {
		var evs []g4Event
		evs, cont = p.eat()
		for _, ev := range evs {
			switch ev.kind {
			case "thinking":
				if p.thinkingEnabled {
					thinkSb.WriteString(ev.data)
				}
			case "content":
				contentSb.WriteString(ev.data)
			case "tool":
				tc, err := parseGemma4ToolCallContent(ev.data)
				if err != nil {
					slog.Warn("gemma4 tool call parse failed", "error", err, "content", ev.data)
					contentSb.WriteString(g4ToolCallOpen + ev.data + g4ToolCallClose)
					continue
				}
				tc.Index = p.callIndex
				p.callIndex++
				calls = append(calls, tc)
			}
		}
	}

	return thinkSb.String(), contentSb.String(), calls
}

type g4Event struct {
	kind string // "content", "thinking", or "tool"
	data string
}

func (p *gemma4StreamParser) eat() ([]g4Event, bool) {
	var evs []g4Event
	bufStr := p.buf.String()
	if bufStr == "" {
		return evs, false
	}

	switch p.state {
	case g4CollectContent:
		// Check for thinking open tag <|channel>.
		if idx := strings.Index(bufStr, g4ThinkOpen); idx != -1 {
			before := bufStr[:idx]
			remaining := bufStr[idx+len(g4ThinkOpen):]
			p.buf.Reset()
			p.buf.WriteString(remaining)
			p.state = g4CollectThinking
			p.needsChannelNameStrip = true
			if before = strings.TrimRightFunc(before, unicode.IsSpace); len(before) > 0 {
				evs = append(evs, g4Event{kind: "content", data: before})
			}
			return evs, true
		}

		// Check for tool call open tag <|tool_call>.
		if idx := strings.Index(bufStr, g4ToolCallOpen); idx != -1 {
			before := bufStr[:idx]
			remaining := bufStr[idx+len(g4ToolCallOpen):]
			p.buf.Reset()
			p.buf.WriteString(remaining)
			p.state = g4CollectToolCall
			if before = strings.TrimRightFunc(before, unicode.IsSpace); len(before) > 0 {
				evs = append(evs, g4Event{kind: "content", data: before})
			}
			return evs, true
		}

		// Check for partial tag overlap.
		maxOL := max(suffixPrefixOverlap(bufStr, g4ThinkOpen), suffixPrefixOverlap(bufStr, g4ToolCallOpen))
		if maxOL > 0 {
			beforePartial := bufStr[:len(bufStr)-maxOL]
			wsLen := trailingWSLen(beforePartial)
			ambStart := len(beforePartial) - wsLen
			unambiguous := bufStr[:ambStart]
			ambiguous := bufStr[ambStart:]
			p.buf.Reset()
			p.buf.WriteString(ambiguous)
			if len(unambiguous) > 0 {
				evs = append(evs, g4Event{kind: "content", data: unambiguous})
			}
			return evs, false
		}

		// No tags; emit all content but hold trailing whitespace.
		wsLen := trailingWSLen(bufStr)
		ambStart := len(bufStr) - wsLen
		unambiguous := bufStr[:ambStart]
		ambiguous := bufStr[ambStart:]
		p.buf.Reset()
		p.buf.WriteString(ambiguous)
		if len(unambiguous) > 0 {
			evs = append(evs, g4Event{kind: "content", data: unambiguous})
		}
		return evs, false

	case g4CollectThinking:
		// Strip channel name "thought\n" after <|channel>.
		if p.needsChannelNameStrip {
			if strings.HasPrefix(bufStr, "thought\n") {
				bufStr = bufStr[len("thought\n"):]
				p.buf.Reset()
				p.buf.WriteString(bufStr)
				p.needsChannelNameStrip = false
				if bufStr == "" {
					return evs, false
				}
			} else if strings.HasPrefix("thought\n", bufStr) {
				// Partial match — wait for more data.
				return evs, false
			} else {
				// No match — don't strip.
				p.needsChannelNameStrip = false
			}
		}

		// Look for <channel|> close.
		if idx := strings.Index(bufStr, g4ThinkClose); idx != -1 {
			thinking := strings.TrimRightFunc(bufStr[:idx], unicode.IsSpace)
			remaining := strings.TrimLeftFunc(bufStr[idx+len(g4ThinkClose):], unicode.IsSpace)
			p.buf.Reset()
			p.buf.WriteString(remaining)
			p.state = g4CollectContent
			if len(thinking) > 0 {
				evs = append(evs, g4Event{kind: "thinking", data: thinking})
			}
			return evs, true
		}

		// Check for partial close tag overlap.
		if ol := suffixPrefixOverlap(bufStr, g4ThinkClose); ol > 0 {
			beforePartial := bufStr[:len(bufStr)-ol]
			wsLen := trailingWSLen(beforePartial)
			ambStart := len(beforePartial) - wsLen
			unambiguous := bufStr[:ambStart]
			ambiguous := bufStr[ambStart:]
			p.buf.Reset()
			p.buf.WriteString(ambiguous)
			if len(unambiguous) > 0 {
				evs = append(evs, g4Event{kind: "thinking", data: unambiguous})
			}
			return evs, false
		}

		// Emit unambiguous thinking, keep trailing whitespace.
		wsLen := trailingWSLen(bufStr)
		ambStart := len(bufStr) - wsLen
		unambiguous := bufStr[:ambStart]
		ambiguous := bufStr[ambStart:]
		p.buf.Reset()
		p.buf.WriteString(ambiguous)
		if len(unambiguous) > 0 {
			evs = append(evs, g4Event{kind: "thinking", data: unambiguous})
		}
		return evs, false

	case g4CollectToolCall:
		// Look for <tool_call|> close.
		if idx := strings.Index(bufStr, g4ToolCallClose); idx != -1 {
			toolContent := bufStr[:idx]
			remaining := strings.TrimLeftFunc(bufStr[idx+len(g4ToolCallClose):], unicode.IsSpace)
			p.buf.Reset()
			p.buf.WriteString(remaining)
			p.state = g4CollectContent
			if len(toolContent) > 0 {
				evs = append(evs, g4Event{kind: "tool", data: toolContent})
			}
			return evs, true
		}
		// Wait for closing tag.
		return evs, false
	}

	return evs, false
}

// ── Gemma 4 tool call parsing ────────────────────────────────────────────────

// parseGemma4ToolCallContent parses "call:NAME{key:value,...}" into a ToolCall.
func parseGemma4ToolCallContent(content string) (ToolCall, error) {
	if !strings.HasPrefix(content, "call:") {
		return ToolCall{}, errors.New("expected 'call:' prefix")
	}
	content = content[len("call:"):]

	braceIdx := strings.Index(content, "{")
	if braceIdx == -1 {
		return ToolCall{}, errors.New("expected '{' in tool call")
	}

	toolName := strings.TrimSpace(content[:braceIdx])
	argsStr := content[braceIdx:]

	jsonStr := gemma4ArgsToJSON(argsStr)

	var args map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &args); err != nil {
		return ToolCall{}, err
	}

	argsBytes, err := json.Marshal(args)
	if err != nil {
		return ToolCall{}, err
	}

	return ToolCall{
		ID:   genToolCallID(),
		Type: "function",
		Function: ToolFunction{
			Name:      toolName,
			Arguments: string(argsBytes),
		},
	}, nil
}

// gemma4ArgsToJSON converts Gemma 4's custom argument format to valid JSON.
// Gemma 4 uses <|"|> as string delimiters and bare keys without quotes.
func gemma4ArgsToJSON(s string) string {
	// First, extract all <|"|>...<|"|> quoted strings and replace with placeholders.
	var quotedStrings []string
	text := g4QuotedStringRe.ReplaceAllStringFunc(s, func(match string) string {
		submatches := g4QuotedStringRe.FindStringSubmatch(match)
		quotedStrings = append(quotedStrings, submatches[1])
		return "\x00" + string(rune(len(quotedStrings)-1)) + "\x00"
	})

	// Quote bare keys: {key: or ,key: → {"key": or ,"key":
	text = g4BareKeyRe.ReplaceAllString(text, `$1"$2":`)

	// Restore quoted strings as properly-escaped JSON strings.
	for i, value := range quotedStrings {
		escaped, _ := json.Marshal(value)
		text = strings.ReplaceAll(text, "\x00"+string(rune(i))+"\x00", string(escaped))
	}

	return text
}
