package engine

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"

	"github.com/kocort/kocort/internal/localmodel/chatfmt"
)

// ── Thinking mode ────────────────────────────────────────────────────────────

// thinkingMode maps the request-level thinking flags to chatfmt.ThinkingMode.
func (e *Engine) thinkingMode(req *ChatCompletionRequest) chatfmt.ThinkingMode {
	if hasJSONGrammar(req) {
		return chatfmt.ThinkingOff
	}
	if req.EnableThinking != nil {
		if *req.EnableThinking {
			return chatfmt.ThinkingOn
		}
		return chatfmt.ThinkingDisabled
	}
	if req.Reasoning != nil {
		if req.Reasoning.Effort == "none" {
			return chatfmt.ThinkingDisabled
		}
		return chatfmt.ThinkingOn
	}
	if req.ReasoningEffort != nil {
		if *req.ReasoningEffort == "none" {
			return chatfmt.ThinkingDisabled
		}
		return chatfmt.ThinkingOn
	}
	if e.enableThinking {
		return chatfmt.ThinkingOn
	}
	return chatfmt.ThinkingOff
}

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

// ── Public prompt building ───────────────────────────────────────────────────

// buildPrompt constructs the full prompt string and extracts images.
func (e *Engine) buildPrompt(req *ChatCompletionRequest) (string, []ImageData, []chatfmt.Message, error) {
	msgs, images, err := normalizeMessages(req.Messages)
	if err != nil {
		return "", nil, nil, err
	}

	// Count image-bearing messages.
	imgMsgCount := 0
	for _, m := range msgs {
		if m.ImageCount > 0 {
			imgMsgCount++
		}
	}
	slog.Info("[engine] buildPrompt",
		"totalMsgs", len(msgs), "imageDataCount", len(images),
		"imageMsgCount", imgMsgCount, "hasVision", e.image != nil)

	// When the engine has vision and images are present, add a brief hint to
	// the system message so small models are not misled by long text-heavy
	// system prompts into thinking they cannot see images.
	if e.image != nil && len(images) > 0 && len(msgs) > 0 && msgs[0].Role == "system" {
		msgs[0].Content = strings.TrimRight(msgs[0].Content, " \n") +
			"\n\nVision: enabled — images uploaded by the user are embedded inline. Analyze them directly."
	}

	thinking := e.thinkingMode(req)

	rendered, prompt, err := chatfmt.TruncateAndRender(
		e.format,
		msgs,
		req.Tools,
		thinking,
		e, // Engine implements chatfmt.Tokenizer
		e.ContextSize(),
		e.image != nil,
	)
	if err != nil {
		return "", nil, nil, err
	}

	return prompt, images, rendered, nil
}

// ── Message normalization ────────────────────────────────────────────────────

func normalizeMessages(messages []ChatMessage) ([]chatfmt.Message, []ImageData, error) {
	var out []chatfmt.Message
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
			out = append(out, chatfmt.Message{
				Role: msg.Role, Reasoning: msg.Reasoning, ToolCalls: msg.ToolCalls,
				Name: toolName, ToolCallID: msg.ToolCallID,
			})
		case string:
			out = append(out, chatfmt.Message{
				Role: msg.Role, Content: content, Reasoning: msg.Reasoning,
				ToolCalls: msg.ToolCalls, Name: toolName, ToolCallID: msg.ToolCallID,
			})
		case []any:
			// Merge all text and image parts into a single chatfmt.Message
			// so the template renderer places them in one turn.
			var textParts []string
			imgCount := 0
			for _, part := range content {
				data, ok := part.(map[string]any)
				if !ok {
					return nil, nil, errors.New("invalid message content part")
				}
				switch data["type"] {
				case "text":
					text, _ := data["text"].(string)
					if text != "" {
						textParts = append(textParts, text)
					}
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
					imgCount++
				default:
					return nil, nil, errors.New("unsupported content part type")
				}
			}
			out = append(out, chatfmt.Message{
				Role:       msg.Role,
				Content:    strings.Join(textParts, "\n"),
				ImageCount: imgCount,
				Reasoning:  msg.Reasoning,
				ToolCalls:  msg.ToolCalls,
				Name:       toolName,
				ToolCallID: msg.ToolCallID,
			})
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

// ── Stop sequence helpers ────────────────────────────────────────────────────

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
