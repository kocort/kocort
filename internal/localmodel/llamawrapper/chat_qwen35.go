package llamawrapper

import (
	"strings"
)

func init() {
	registerStopTokens("qwen3.5", []string{imEnd})
}

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

func (e *Engine) truncateAndRenderQwen35(messages []renderMsg, tools []Tool, thinkingEnabled bool) ([]renderMsg, string, error) {
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
		prompt, err := renderQwen35(candidate, tools, thinkingEnabled)
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
	prompt, err := renderQwen35(rendered, tools, thinkingEnabled)
	if err != nil {
		return nil, "", err
	}
	return rendered, prompt, nil
}

func renderQwen35(messages []renderMsg, tools []Tool, thinkingEnabled bool) (string, error) {
	var sb strings.Builder

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
			content := renderMsgContent(messages[0], 0)
			content = strings.TrimSpace(content)
			if content != "" {
				sb.WriteString("\n\n" + content)
			}
		}
		sb.WriteString(imEnd + "\n")
	} else if len(messages) > 0 && messages[0].Role == "system" {
		content := renderMsgContent(messages[0], 0)
		sb.WriteString(imStart + "system\n" + strings.TrimSpace(content) + imEnd + "\n")
	}

	// Find last real user query index (for tool-step detection).
	lastQueryIdx := len(messages) - 1
	multiStepTool := true
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if multiStepTool && msg.Role == "user" {
			content := strings.TrimSpace(renderMsgContent(msg, 0))
			if !(strings.HasPrefix(content, "<tool_response>") && strings.HasSuffix(content, "</tool_response>")) {
				multiStepTool = false
				lastQueryIdx = i
			}
		}
	}

	imgOffset := 0
	for i, msg := range messages {
		content := renderMsgContent(msg, imgOffset)
		imgOffset += len(msg.Images)
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
						sb.WriteString("<parameter=" + arg.name + ">\n")
						sb.WriteString(formatArgValue(arg.value))
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
