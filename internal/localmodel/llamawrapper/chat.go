package llamawrapper

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	case "gemma4":
		return "gemma4"
	default:
		return "chatml"
	}
}

// rendererStopTokens holds per-renderer stop token registrations.
// Each chat_*.go file registers its own tokens via init().
var rendererStopTokens = map[string][]string{}

// registerStopTokens is called by each renderer's init() to declare its stop tokens.
func registerStopTokens(renderer string, tokens []string) {
	rendererStopTokens[renderer] = tokens
}

// stopTokensForRenderer returns the stop tokens registered for the given renderer.
func stopTokensForRenderer(renderer string) []string {
	if tokens, ok := rendererStopTokens[renderer]; ok {
		return tokens
	}
	return []string{imEnd}
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
	case "gemma4":
		rendered, prompt, err := e.truncateAndRenderGemma4(msgs, req.Tools, thinkingEnabled)
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
			// Merge all text and image parts into a single renderMsg so that
			// chat renderers emit them inside one turn (e.g. Gemma4 expects
			// image placeholders and text content in the same user turn).
			var merged renderMsg
			merged.Role = msg.Role
			var textParts []string
			for _, part := range content {
				data, ok := part.(map[string]any)
				if !ok {
					return nil, nil, errors.New("invalid message content part")
				}
				switch data["type"] {
				case "text":
					text, _ := data["text"].(string)
					textParts = append(textParts, text)
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
					merged.Images = append(merged.Images, img)
				default:
					return nil, nil, errors.New("unsupported content part type")
				}
			}
			merged.Content = strings.Join(textParts, "\n")
			merged.Reasoning = msg.Reasoning
			merged.ToolCalls = msg.ToolCalls
			merged.Name = toolName
			merged.ToolCallID = msg.ToolCallID
			out = append(out, merged)
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
