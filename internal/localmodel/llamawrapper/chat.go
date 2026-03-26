package llamawrapper

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ── Constants ────────────────────────────────────────────────────────────────

const (
	imStart = "<|im_start|>"
	imEnd   = "<|im_end|>"

	thinkOpen  = "<think>"
	thinkClose = "</think>"

	qwen35ToolPostamble = `
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

	// qwen3ToolSystemPrompt is the system-level tool instruction for Qwen3.
	// Qwen3 uses JSON-based tool calls inside <tool_call> tags, unlike Qwen3.5's XML format.
	qwen3ToolSystemPrompt = `# Tools

You are provided with function signatures within <tools></tools> XML tags:
<tools>
%s</tools>

For each function call, return a json object with function name and arguments within <tool_call></tool_call> XML tags:
<tool_call>
{"name": <function-name>, "arguments": <args-json-object>}
</tool_call>`
)

// ── Internal message representation ──────────────────────────────────────────

// renderMsg is a normalized internal message for prompt rendering.
type renderMsg struct {
	Role       string
	Content    string
	Reasoning  string
	ToolCalls  []ToolCall
	Name       string
	ToolCallID string
	Images     []ImageData
}

// ── Renderer selection ───────────────────────────────────────────────────────

// chatRenderer returns the renderer name based on model architecture.
func (e *Engine) chatRenderer() string {
	switch e.modelArch {
	case "qwen35", "qwen35moe":
		return "qwen3.5"
	case "qwen3", "qwen3moe":
		return "qwen3"
	default:
		return "chatml"
	}
}

// ── Public prompt building ───────────────────────────────────────────────────

// buildPrompt constructs the full prompt string and extracts images.
func (e *Engine) buildPrompt(req *ChatCompletionRequest) (string, []ImageData, []renderMsg, error) {
	msgs, images, err := normalizeMessages(req.Messages)
	if err != nil {
		return "", nil, nil, err
	}

	thinkingEnabled := e.isThinkingActive(req)
	renderer := e.chatRenderer()

	switch renderer {
	case "qwen3.5":
		rendered, prompt, err := e.truncateAndRenderQwen35(msgs, req.Tools, thinkingEnabled)
		if err != nil {
			return "", nil, nil, err
		}
		return prompt, images, rendered, nil
	case "qwen3":
		rendered, prompt, err := e.truncateAndRenderQwen3(msgs, req.Tools, thinkingEnabled)
		if err != nil {
			return "", nil, nil, err
		}
		return prompt, images, rendered, nil
	default:
		prompt := renderChatML(req.Messages)
		prompt = e.applyThinkingTags(prompt, req)
		return prompt, images, msgs, nil
	}
}

// ── Thinking logic ───────────────────────────────────────────────────────────

// hasJSONGrammar returns true when the request specifies json_object or json_schema.
func hasJSONGrammar(req *ChatCompletionRequest) bool {
	if req.ResponseFormat == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(req.ResponseFormat.Type)) {
	case "json_object", "json_schema":
		return true
	}
	return false
}

// isThinkingDisabled returns true when the request explicitly disables thinking.
func isThinkingDisabled(req *ChatCompletionRequest) bool {
	if req.EnableThinking != nil && !*req.EnableThinking {
		return true
	}
	if req.Reasoning != nil {
		return req.Reasoning.Effort == "none"
	}
	if req.ReasoningEffort != nil {
		return *req.ReasoningEffort == "none"
	}
	return false
}

// isThinkingActive determines if thinking should be active for a request.
func (e *Engine) isThinkingActive(req *ChatCompletionRequest) bool {
	if hasJSONGrammar(req) {
		return false
	}
	if req.EnableThinking != nil {
		return *req.EnableThinking
	}
	if req.Reasoning != nil {
		return req.Reasoning.Effort != "none"
	}
	if req.ReasoningEffort != nil {
		return *req.ReasoningEffort != "none"
	}
	return e.enableThinking
}

// applyThinkingTags appends thinking control tags to the prompt.
func (e *Engine) applyThinkingTags(prompt string, req *ChatCompletionRequest) string {
	if hasJSONGrammar(req) {
		return prompt
	}
	if e.isThinkingActive(req) {
		return prompt + thinkOpen + "\n"
	}
	if isThinkingDisabled(req) {
		return prompt + thinkOpen + "\n" + thinkClose + "\n\n"
	}
	return prompt
}

// ── ChatML renderer ──────────────────────────────────────────────────────────

// renderChatML converts messages to ChatML format.
func renderChatML(messages []ChatMessage) string {
	var sb strings.Builder
	for _, msg := range messages {
		sb.WriteString(imStart)
		sb.WriteString(msg.Role)
		sb.WriteByte('\n')
		if content, ok := msg.Content.(string); ok {
			sb.WriteString(content)
		}
		sb.WriteByte('\n')
		sb.WriteString(imEnd)
		sb.WriteByte('\n')
	}
	sb.WriteString(imStart)
	sb.WriteString("assistant\n")
	return sb.String()
}

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

func renderMsgContent(msg renderMsg, imgOffset int) string {
	var sb strings.Builder
	for range msg.Images {
		sb.WriteString(fmt.Sprintf("[img-%d]", imgOffset))
		imgOffset++
	}
	sb.WriteString(msg.Content)
	return sb.String()
}

func splitReasoning(content, reasoning string, thinkingEnabled bool) (string, string) {
	if thinkingEnabled && reasoning != "" {
		return strings.TrimSpace(reasoning), content
	}
	if idx := strings.Index(content, thinkClose); idx != -1 {
		before := content[:idx]
		if open := strings.LastIndex(before, thinkOpen); open != -1 {
			reasoning = before[open+len(thinkOpen):]
		} else {
			reasoning = before
		}
		content = strings.TrimLeft(content[idx+len(thinkClose):], "\n")
	}
	return strings.TrimSpace(reasoning), content
}

// ── Message normalization ────────────────────────────────────────────────────

func normalizeMessages(messages []ChatMessage) ([]renderMsg, []ImageData, error) {
	var out []renderMsg
	var images []ImageData

	for _, msg := range messages {
		toolName := ""
		if strings.EqualFold(msg.Role, "tool") {
			toolName = msg.Name
			if toolName == "" && msg.ToolCallID != "" {
				toolName = toolNameByCallID(messages, msg.ToolCallID)
			}
		}

		switch content := msg.Content.(type) {
		case nil:
			out = append(out, renderMsg{
				Role: msg.Role, Reasoning: msg.Reasoning, ToolCalls: msg.ToolCalls,
				Name: toolName, ToolCallID: msg.ToolCallID,
			})
		case string:
			out = append(out, renderMsg{
				Role: msg.Role, Content: content, Reasoning: msg.Reasoning,
				ToolCalls: msg.ToolCalls, Name: toolName, ToolCallID: msg.ToolCallID,
			})
		case []any:
			start := len(out)
			for _, part := range content {
				data, ok := part.(map[string]any)
				if !ok {
					return nil, nil, errors.New("invalid message content part")
				}
				switch data["type"] {
				case "text":
					text, _ := data["text"].(string)
					out = append(out, renderMsg{Role: msg.Role, Content: text})
				case "image_url":
					url, err := extractImageURL(data["image_url"])
					if err != nil {
						return nil, nil, err
					}
					img, err := decodeBase64Image(url)
					if err != nil {
						return nil, nil, err
					}
					img.ID = len(images)
					images = append(images, img)
					out = append(out, renderMsg{Role: msg.Role, Images: []ImageData{img}})
				default:
					return nil, nil, errors.New("unsupported content part type")
				}
			}
			if len(out) == start {
				out = append(out, renderMsg{Role: msg.Role})
			}
			last := len(out) - 1
			out[last].Reasoning = msg.Reasoning
			out[last].ToolCalls = msg.ToolCalls
			out[last].Name = toolName
			out[last].ToolCallID = msg.ToolCallID
		default:
			return nil, nil, errors.New("invalid message content type")
		}
	}

	return out, images, nil
}

func toolNameByCallID(messages []ChatMessage, callID string) string {
	for i := len(messages) - 1; i >= 0; i-- {
		for _, tc := range messages[i].ToolCalls {
			if tc.ID == callID {
				return tc.Function.Name
			}
		}
	}
	return ""
}

func extractImageURL(v any) (string, error) {
	if m, ok := v.(map[string]any); ok {
		url, _ := m["url"].(string)
		if url == "" {
			return "", errors.New("missing image url")
		}
		return url, nil
	}
	if url, ok := v.(string); ok {
		return url, nil
	}
	return "", errors.New("invalid image_url format")
}

func decodeBase64Image(url string) (ImageData, error) {
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return ImageData{}, errors.New("http image URLs not supported; use base64 data")
	}

	// Strip data URI prefix.
	types := []string{"jpeg", "jpg", "png", "webp"}
	if strings.HasPrefix(url, "data:;base64,") {
		url = strings.TrimPrefix(url, "data:;base64,")
	} else {
		found := false
		for _, t := range types {
			prefix := "data:image/" + t + ";base64,"
			if strings.HasPrefix(url, prefix) {
				url = strings.TrimPrefix(url, prefix)
				found = true
				break
			}
		}
		if !found {
			return ImageData{}, errors.New("invalid image data format")
		}
	}

	data, err := base64.StdEncoding.DecodeString(url)
	if err != nil {
		return ImageData{}, errors.New("invalid base64 image data")
	}
	return ImageData{Data: data}, nil
}

// ── JSON helpers ─────────────────────────────────────────────────────────────

// marshalSpaced marshals JSON with spaces after : and ,
func marshalSpaced(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(b)+len(b)/8)
	inStr := false
	esc := false
	for _, c := range b {
		if inStr {
			out = append(out, c)
			if esc {
				esc = false
				continue
			}
			if c == '\\' {
				esc = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
			out = append(out, c)
		case ':':
			out = append(out, ':', ' ')
		case ',':
			out = append(out, ',', ' ')
		default:
			out = append(out, c)
		}
	}
	return out, nil
}

// parseStopSequences extracts stop sequences from the OpenAI "stop" field.
func parseStopSequences(stop any) []string {
	if stop == nil {
		return nil
	}
	switch v := stop.(type) {
	case string:
		return []string{v}
	case []any:
		var result []string
		for _, s := range v {
			if str, ok := s.(string); ok {
				result = append(result, str)
			}
		}
		return result
	}
	return nil
}

type orderedArg struct {
	name  string
	value any
}

func parseOrderedArgs(raw string) ([]orderedArg, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := tok.(json.Delim)
	if !ok || d != '{' {
		return nil, errors.New("expected JSON object")
	}
	var args []orderedArg
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := keyTok.(string)
		var val any
		if err := dec.Decode(&val); err != nil {
			return nil, err
		}
		args = append(args, orderedArg{name: key, value: val})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return args, nil
}

func formatArgValue(v any) string {
	if v == nil {
		return "null"
	}
	switch val := v.(type) {
	case string:
		return val
	case []byte:
		return string(val)
	}
	switch v.(type) {
	case map[string]any, []any:
		b, err := json.Marshal(v)
		if err == nil {
			return string(b)
		}
	}
	return fmt.Sprintf("%v", v)
}
