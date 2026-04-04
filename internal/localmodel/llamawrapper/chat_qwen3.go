package llamawrapper

import (
	"fmt"
	"strings"
)

func init() {
	registerStopTokens("qwen3", []string{imEnd})
}

// qwen3ToolSystemPrompt is the system-level tool instruction for Qwen3.
// Qwen3 uses JSON-based tool calls inside <tool_call> tags, unlike Qwen3.5's XML format.
const qwen3ToolSystemPrompt = `# Tools

You are provided with function signatures within <tools></tools> XML tags:
<tools>
%s</tools>

For each function call, return a json object with function name and arguments within <tool_call></tool_call> XML tags:
<tool_call>
{"name": <function-name>, "arguments": <args-json-object>}
</tool_call>`

// ── Qwen3 renderer ──────────────────────────────────────────────────────────

func (e *Engine) truncateAndRenderQwen3(messages []renderMsg, tools []Tool, thinkingEnabled bool) ([]renderMsg, string, error) {
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
		prompt, err := renderQwen3(candidate, tools, thinkingEnabled)
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
	prompt, err := renderQwen3(rendered, tools, thinkingEnabled)
	if err != nil {
		return nil, "", err
	}
	return rendered, prompt, nil
}

// renderQwen3 renders messages in Qwen3 ChatML format with JSON-based tool calls.
// Key differences from Qwen3.5:
//   - Tool calls use JSON format: <tool_call>{"name":"fn","arguments":{...}}</tool_call>
//   - Thinking mode controlled via /think or /no_think appended to the system message
//   - When thinking is enabled, the model outputs <think>...</think> on its own
func renderQwen3(messages []renderMsg, tools []Tool, thinkingEnabled bool) (string, error) {
	var sb strings.Builder

	startIdx := 0

	// thinkDirective is appended at the end of the system message, before <|im_end|>.
	thinkDirective := "\n/no_think"
	if thinkingEnabled {
		thinkDirective = "\n/think"
	}

	// System message with optional tool definitions.
	if len(tools) > 0 {
		sb.WriteString(imStart + "system\n")

		// Append user's system message content first (if present).
		if len(messages) > 0 && messages[0].Role == "system" {
			content := strings.TrimSpace(renderMsgContent(messages[0], 0))
			if content != "" {
				sb.WriteString(content + "\n\n")
			}
			startIdx = 1
		}

		// Build tools JSON block.
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
		content := strings.TrimSpace(renderMsgContent(messages[0], 0))
		sb.WriteString(imStart + "system\n" + content + thinkDirective + imEnd + "\n")
		startIdx = 1
	} else {
		// No system message — inject one purely for the thinking directive.
		sb.WriteString(imStart + "system" + thinkDirective + imEnd + "\n")
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
			sb.WriteString(imStart + "user\n" + content + imEnd + "\n")

		case msg.Role == "assistant":
			sb.WriteString(imStart + "assistant\n")
			// Include reasoning as <think> block if present in history.
			if msg.Reasoning != "" {
				sb.WriteString("<think>\n" + strings.TrimSpace(msg.Reasoning) + "\n</think>\n\n")
			}
			sb.WriteString(content)
			// Render tool calls in JSON format.
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
			// Qwen3 wraps tool responses in user turn with <tool_response> tags.
			if i == startIdx || messages[i-1].Role != "tool" {
				sb.WriteString(imStart + "user")
			}
			sb.WriteString("\n<tool_response>\n" + content + "\n</tool_response>")
			if i == len(messages)-1 || messages[i+1].Role != "tool" {
				sb.WriteString(imEnd + "\n")
			}

		case msg.Role == "system":
			// Non-first system messages rendered inline.
			sb.WriteString(imStart + "system\n" + content + imEnd + "\n")
		}

		// Last message: add assistant generation prefix.
		// Qwen3 relies on /think in the system prompt — no need for <think> tags here.
		if lastMessage && !prefill {
			sb.WriteString(imStart + "assistant\n")
		}
	}

	return sb.String(), nil
}
