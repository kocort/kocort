package llamawrapper

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)
func init() {
	registerStopTokens("gemma4", []string{g4TurnClose})
}
// ── Gemma 4 renderer ─────────────────────────────────────────────────────────
// Uses <|turn>/<turn|> markers, <|channel>/<channel|> for thinking,
// <|tool>/<tool|> for declarations, <|tool_call>/<tool_call|> for calls,
// and <|"|> as a string delimiter.

const (
	g4TurnOpen  = "<|turn>"
	g4TurnClose = "<turn|>"

	g4ThinkTag = "<|think|>"

	g4ToolDeclOpen  = "<|tool>"
	g4ToolDeclClose = "<tool|>"

	g4ToolCallOpen  = "<|tool_call>"
	g4ToolCallClose = "<tool_call|>"

	g4ToolRespOpen  = "<|tool_response>"
	g4ToolRespClose = "<tool_response|>"

	g4StringDelim = `<|"|>`
)

// truncateAndRenderGemma4 performs context-aware truncation and renders
// the final prompt in Gemma 4 format.
func (e *Engine) truncateAndRenderGemma4(messages []renderMsg, tools []Tool, thinkingEnabled bool) ([]renderMsg, string, error) {
	lastIdx := len(messages) - 1
	currIdx := 0

	for i := 0; i <= lastIdx; i++ {
		var system []renderMsg
		for j := 0; j < i; j++ {
			if messages[j].Role == "system" {
				system = append(system, messages[j])
			}
		}
		candidate := append(append([]renderMsg{}, system...), messages[i:]...)
		prompt, err := renderGemma4(candidate, tools, thinkingEnabled)
		if err != nil {
			return nil, "", err
		}
		tokens, err := e.Tokenize(prompt)
		if err != nil {
			return nil, "", err
		}
		ctxLen := len(tokens)
		if e.image != nil {
			for _, msg := range candidate {
				ctxLen += 768 * len(msg.Images)
			}
		}
		if ctxLen <= e.cache.ctxLen {
			currIdx = i
			break
		}
		if i == lastIdx {
			currIdx = lastIdx
			break
		}
	}

	var system []renderMsg
	for i := 0; i < currIdx; i++ {
		if messages[i].Role == "system" {
			system = append(system, messages[i])
		}
	}
	rendered := append(append([]renderMsg{}, system...), messages[currIdx:]...)
	prompt, err := renderGemma4(rendered, tools, thinkingEnabled)
	if err != nil {
		return nil, "", err
	}
	return rendered, prompt, nil
}

// renderGemma4 renders messages in Gemma 4 native format.
func renderGemma4(messages []renderMsg, tools []Tool, thinkingEnabled bool) (string, error) {
	var sb strings.Builder

	// Extract system message.
	var systemContent string
	startIdx := 0
	if len(messages) > 0 && messages[0].Role == "system" {
		systemContent = strings.TrimSpace(renderMsgContent(messages[0], 0))
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
		imgOffset += len(messages[i].Images)
	}

	for i := startIdx; i < len(messages); i++ {
		msg := messages[i]
		content := renderMsgContent(msg, imgOffset)
		imgOffset += len(msg.Images)
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
			// Render tool calls before content (matching Gemma4 format).
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
			// Non-first system messages rendered inline.
			sb.WriteString(g4TurnOpen + "system\n")
			sb.WriteString(content)
			sb.WriteString(g4TurnClose + "\n")
		}

		// Last message: add model generation prefix.
		if lastMessage && !prefill {
			sb.WriteString(g4TurnOpen + "model\n")
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
