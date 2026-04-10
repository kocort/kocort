package chatfmt

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// ── Gemma 4 format ──────────────────────────────────────────────────────────
// Uses <|turn>/<turn|> markers, <|channel>/<channel|> for thinking,
// <|tool>/<tool|> for declarations, <|tool_call>/<tool_call|> for calls,
// and <|"|> as a string delimiter.

// Gemma4 implements the Gemma 4 chat format.
type Gemma4 struct{}

var _ Format = (*Gemma4)(nil)

func (g *Gemma4) Name() string         { return "gemma4" }
func (g *Gemma4) StopTokens() []string { return []string{g4TurnClose} }

func (g *Gemma4) Render(messages []Message, tools []Tool, thinking ThinkingMode) (string, error) {
	return renderGemma4(messages, tools, thinking == ThinkingOn)
}

func (g *Gemma4) NewParser(tools []Tool, lastMsg *Message, thinking ThinkingMode) StreamParser {
	return newGemma4StreamParser(tools, lastMsg, thinking == ThinkingOn)
}

// ── Gemma 4 constants ────────────────────────────────────────────────────────

const (
	g4TurnOpen  = "<|turn>"
	g4TurnClose = "<turn|>"

	g4ThinkTag = "<|think|>"

	g4ThinkOpen  = "<|channel>"
	g4ThinkClose = "<channel|>"

	g4ToolDeclOpen  = "<|tool>"
	g4ToolDeclClose = "<tool|>"

	g4ToolCallOpen  = "<|tool_call>"
	g4ToolCallClose = "<tool_call|>"

	g4ToolRespOpen  = "<|tool_response>"
	g4ToolRespClose = "<tool_response|>"

	g4StringDelim = `<|"|>`
)

var (
	g4QuotedStringRe = regexp.MustCompile(`(?s)<\|"\|>(.*?)<\|"\|>`)
	g4BareKeyRe      = regexp.MustCompile(`([,{])(\w+):`)
)

// ── Gemma 4 renderer ─────────────────────────────────────────────────────────

func renderGemma4(messages []Message, tools []Tool, thinkingEnabled bool) (string, error) {
	var sb strings.Builder

	// Extract system message.
	var systemContent string
	startIdx := 0
	if len(messages) > 0 && messages[0].Role == "system" {
		systemContent = strings.TrimSpace(messages[0].ContentWithImages(0))
		startIdx = 1
	}

	// Emit system turn if there is system content, tools, or thinking.
	if systemContent != "" || len(tools) > 0 || thinkingEnabled {
		sb.WriteString(g4TurnOpen + "system\n")
		if thinkingEnabled {
			sb.WriteString(g4ThinkTag)
		}
		if systemContent != "" {
			sb.WriteString(systemContent)
		}
		for _, tool := range tools {
			sb.WriteString(renderGemma4ToolDecl(tool))
		}
		sb.WriteString(g4TurnClose + "\n")
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
			sb.WriteString(g4TurnOpen + "user\n")
			sb.WriteString(content)
			sb.WriteString(g4TurnClose + "\n")

		case msg.Role == "assistant":
			sb.WriteString(g4TurnOpen + "model\n")
			for _, tc := range msg.ToolCalls {
				sb.WriteString(formatGemma4ToolCall(tc))
			}
			if content != "" {
				sb.WriteString(content)
			}
			if !prefill {
				sb.WriteString(g4TurnClose + "\n")
			}

		case msg.Role == "tool":
			sb.WriteString(g4TurnOpen + "tool\n")
			sb.WriteString(content)
			sb.WriteString(g4TurnClose + "\n")

		case msg.Role == "system":
			sb.WriteString(g4TurnOpen + "system\n")
			sb.WriteString(content)
			sb.WriteString(g4TurnClose + "\n")
		}

		if lastMessage && !prefill {
			sb.WriteString(g4TurnOpen + "model\n")
			if thinkingEnabled {
				sb.WriteString(g4ThinkOpen + "thought\n")
			}
		}
	}

	return sb.String(), nil
}

// ── Gemma 4 tool declaration ─────────────────────────────────────────────────

func renderGemma4ToolDecl(tool Tool) string {
	var sb strings.Builder
	fn := tool.Function

	sb.WriteString(g4ToolDeclOpen + "declaration:" + fn.Name + "{")
	sb.WriteString("description:" + g4StringDelim + fn.Description + g4StringDelim)

	if len(fn.Parameters) > 0 {
		var params struct {
			Type       string                            `json:"type"`
			Properties map[string]map[string]interface{} `json:"properties"`
			Required   []string                          `json:"required"`
		}
		if err := json.Unmarshal(fn.Parameters, &params); err == nil {
			sb.WriteString(",parameters:{")
			needsComma := false

			if len(params.Properties) > 0 {
				sb.WriteString("properties:{")
				writeGemma4Properties(&sb, params.Properties)
				sb.WriteString("}")
				needsComma = true
			}

			if len(params.Required) > 0 {
				if needsComma {
					sb.WriteString(",")
				}
				sb.WriteString("required:[")
				for j, req := range params.Required {
					if j > 0 {
						sb.WriteString(",")
					}
					sb.WriteString(g4StringDelim + req + g4StringDelim)
				}
				sb.WriteString("]")
				needsComma = true
			}

			if params.Type != "" {
				if needsComma {
					sb.WriteString(",")
				}
				sb.WriteString("type:" + g4StringDelim + strings.ToUpper(params.Type) + g4StringDelim)
			}

			sb.WriteString("}")
		}
	}

	sb.WriteString("}" + g4ToolDeclClose)
	return sb.String()
}

func writeGemma4Properties(sb *strings.Builder, props map[string]map[string]interface{}) {
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	first := true
	for _, name := range keys {
		prop := props[name]
		if !first {
			sb.WriteString(",")
		}
		first = false

		sb.WriteString(name + ":{")
		hasContent := false

		if desc, ok := prop["description"].(string); ok && desc != "" {
			sb.WriteString("description:" + g4StringDelim + desc + g4StringDelim)
			hasContent = true
		}

		if typeVal, ok := prop["type"].(string); ok && typeVal != "" {
			typeName := strings.ToUpper(typeVal)

			if typeName == "STRING" {
				if enumVal, ok := prop["enum"].([]interface{}); ok && len(enumVal) > 0 {
					if hasContent {
						sb.WriteString(",")
					}
					sb.WriteString("enum:[")
					for j, e := range enumVal {
						if j > 0 {
							sb.WriteString(",")
						}
						sb.WriteString(g4StringDelim + fmt.Sprintf("%v", e) + g4StringDelim)
					}
					sb.WriteString("]")
					hasContent = true
				}
			}

			if hasContent {
				sb.WriteString(",")
			}
			sb.WriteString("type:" + g4StringDelim + typeName + g4StringDelim)
		}

		sb.WriteString("}")
	}
}

// ── Gemma 4 tool call formatting ─────────────────────────────────────────────

func formatGemma4ToolCall(tc ToolCall) string {
	var sb strings.Builder
	sb.WriteString(g4ToolCallOpen + "call:" + tc.Function.Name + "{")

	var args map[string]interface{}
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err == nil {
		keys := make([]string, 0, len(args))
		for k := range args {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		first := true
		for _, key := range keys {
			if !first {
				sb.WriteString(",")
			}
			first = false
			sb.WriteString(key + ":" + formatGemma4Value(args[key]))
		}
	}

	sb.WriteString("}" + g4ToolCallClose)
	return sb.String()
}

func formatGemma4Value(value interface{}) string {
	switch v := value.(type) {
	case string:
		return g4StringDelim + v + g4StringDelim
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%v", v)
	case map[string]interface{}:
		var sb strings.Builder
		sb.WriteString("{")
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		first := true
		for _, k := range keys {
			if !first {
				sb.WriteString(",")
			}
			first = false
			sb.WriteString(k + ":" + formatGemma4Value(v[k]))
		}
		sb.WriteString("}")
		return sb.String()
	case []interface{}:
		var sb strings.Builder
		sb.WriteString("[")
		for i, item := range v {
			if i > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(formatGemma4Value(item))
		}
		sb.WriteString("]")
		return sb.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

// ── Gemma 4 stream parser ────────────────────────────────────────────────────

type g4State int

const (
	g4CollectContent g4State = iota
	g4CollectThinking
	g4CollectToolCall
)

type gemma4StreamParser struct {
	state                 g4State
	buf                   strings.Builder
	thinkingEnabled       bool
	needsChannelNameStrip bool
	callIndex             int
}

func newGemma4StreamParser(tools []Tool, lastMsg *Message, thinkingEnabled bool) *gemma4StreamParser {
	p := &gemma4StreamParser{
		thinkingEnabled: thinkingEnabled,
		state:           g4CollectContent,
	}
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
		var evs []parsedEvent
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

func (p *gemma4StreamParser) eat() ([]parsedEvent, bool) {
	var evs []parsedEvent
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
				evs = append(evs, parsedEvent{kind: "content", data: before})
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
				evs = append(evs, parsedEvent{kind: "content", data: before})
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
				evs = append(evs, parsedEvent{kind: "content", data: unambiguous})
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
			evs = append(evs, parsedEvent{kind: "content", data: unambiguous})
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
				return evs, false
			} else {
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
				evs = append(evs, parsedEvent{kind: "thinking", data: thinking})
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
				evs = append(evs, parsedEvent{kind: "thinking", data: unambiguous})
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
			evs = append(evs, parsedEvent{kind: "thinking", data: unambiguous})
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
				evs = append(evs, parsedEvent{kind: "tool", data: toolContent})
			}
			return evs, true
		}
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
	var quotedStrings []string
	text := g4QuotedStringRe.ReplaceAllStringFunc(s, func(match string) string {
		submatches := g4QuotedStringRe.FindStringSubmatch(match)
		quotedStrings = append(quotedStrings, submatches[1])
		return "\x00" + string(rune(len(quotedStrings)-1)) + "\x00"
	})

	text = g4BareKeyRe.ReplaceAllString(text, `$1"$2":`)

	for i, value := range quotedStrings {
		escaped, _ := json.Marshal(value)
		text = strings.ReplaceAll(text, "\x00"+string(rune(i))+"\x00", string(escaped))
	}

	return text
}
