// Canonical implementation of OpenAI history sanitization and transcript
// conversion functions.
//
// Pure functions operating on core.TranscriptMessage and openai wire types.
// No runtime dependency.
package backend

import (
	"fmt"
	"strings"

	openai "github.com/sashabaranov/go-openai"

	"github.com/kocort/kocort/internal/core"

	"github.com/kocort/kocort/utils"
)

// ---------------------------------------------------------------------------
// Transcript sanitization (TranscriptMessage → TranscriptMessage)
// ---------------------------------------------------------------------------

// SanitizeTranscriptForOpenAI cleans, normalizes, and deduplicates a
// transcript message slice for submission to an OpenAI-compatible backend.
func SanitizeTranscriptForOpenAI(history []core.TranscriptMessage) []core.TranscriptMessage {
	if len(history) == 0 {
		return nil
	}
	out := make([]core.TranscriptMessage, 0, len(history))
	for _, message := range history {
		msgType := strings.TrimSpace(strings.ToLower(message.Type))
		role := NormalizeTranscriptRole(message.Role, msgType)
		switch msgType {
		case "assistant_partial":
			continue
		case "compaction":
			text := strings.TrimSpace(message.Summary)
			if text == "" {
				text = strings.TrimSpace(message.Text)
			}
			if text == "" {
				continue
			}
			out = append(out, core.TranscriptMessage{
				ID:               strings.TrimSpace(message.ID),
				Type:             "compaction",
				Role:             "system",
				Text:             text,
				Summary:          text,
				Timestamp:        message.Timestamp,
				Event:            strings.TrimSpace(message.Event),
				FirstKeptEntryID: strings.TrimSpace(message.FirstKeptEntryID),
				TokensBefore:     message.TokensBefore,
				Instructions:     strings.TrimSpace(message.Instructions),
			})
			continue
		case "tool_call":
			if strings.TrimSpace(message.ToolName) == "" {
				continue
			}
			out = append(out, core.TranscriptMessage{
				ID:         strings.TrimSpace(message.ID),
				Type:       "tool_call",
				Role:       "assistant",
				ToolCallID: utils.NonEmpty(strings.TrimSpace(message.ToolCallID), "call_"+strings.ReplaceAll(strings.ToLower(strings.TrimSpace(message.ToolName)), " ", "_")),
				ToolName:   strings.TrimSpace(message.ToolName),
				Args:       utils.CloneAnyMap(message.Args),
				Timestamp:  message.Timestamp,
			})
			continue
		case "tool_result":
			if strings.TrimSpace(message.ToolCallID) == "" {
				continue
			}
			text := strings.TrimSpace(message.Text)
			out = append(out, core.TranscriptMessage{
				ID:         strings.TrimSpace(message.ID),
				Type:       "tool_result",
				Role:       "tool",
				ToolCallID: strings.TrimSpace(message.ToolCallID),
				ToolName:   strings.TrimSpace(message.ToolName),
				Text:       text,
				Timestamp:  message.Timestamp,
			})
			continue
		case "system_event", "internal_event":
			text := strings.TrimSpace(message.Text)
			if text == "" {
				continue
			}
			out = append(out, core.TranscriptMessage{
				ID:        strings.TrimSpace(message.ID),
				Type:      msgType,
				Role:      "system",
				Text:      text,
				Timestamp: message.Timestamp,
				Event:     strings.TrimSpace(message.Event),
			})
			continue
		}
		text := strings.TrimSpace(message.Text)
		if text == "" {
			continue
		}
		if len(out) > 0 && out[len(out)-1].Role == role && CanMergeTranscriptText(out[len(out)-1], msgType, role) {
			out[len(out)-1].Text = strings.TrimSpace(out[len(out)-1].Text + "\n\n" + text)
			continue
		}
		out = append(out, core.TranscriptMessage{
			ID:        strings.TrimSpace(message.ID),
			Type:      NormalizeTranscriptTextType(msgType, role),
			Role:      role,
			Text:      text,
			Timestamp: message.Timestamp,
			Final:     message.Final,
		})
	}
	return out
}

// CanMergeTranscriptText returns whether two adjacent transcript entries
// can be merged into a single text block.
func CanMergeTranscriptText(previous core.TranscriptMessage, nextType string, role string) bool {
	if role != "assistant" {
		return previous.Type == ""
	}
	previousType := strings.TrimSpace(previous.Type)
	nextType = strings.TrimSpace(nextType)
	if previousType == "" && nextType == "" {
		return true
	}
	if previousType == "assistant_final" && nextType == "" {
		return true
	}
	if previousType == "" && nextType == "assistant_final" {
		return true
	}
	return previousType == "assistant_final" && nextType == "assistant_final"
}

// NormalizeTranscriptRole maps a raw role string and message type to a
// canonical role string used by OpenAI-compatible backends.
func NormalizeTranscriptRole(role string, msgType string) string {
	normalized := strings.TrimSpace(strings.ToLower(role))
	switch msgType {
	case "tool_call":
		return "assistant"
	case "tool_result":
		return "tool"
	case "system_event", "internal_event":
		return "system"
	}
	switch normalized {
	case "user", "assistant", "system", "tool":
		return normalized
	default:
		return "user"
	}
}

// NormalizeTranscriptTextType returns the canonical transcript text type
// for a given message type and role.
func NormalizeTranscriptTextType(msgType string, role string) string {
	switch msgType {
	case "assistant_final":
		return "assistant_final"
	case "system_event", "internal_event", "compaction":
		return msgType
	}
	if role == "assistant" {
		return "assistant_final"
	}
	return ""
}

// ---------------------------------------------------------------------------
// OpenAI wire message sanitization
// ---------------------------------------------------------------------------

// SanitizeOpenAICompatMessages cleans and reorders OpenAI-compatible chat
// messages, ensuring tool-call/tool-result pairing and role constraints.
func SanitizeOpenAICompatMessages(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	if len(messages) == 0 {
		return nil
	}
	var sanitized []openai.ChatCompletionMessage
	for i := 0; i < len(messages); i++ {
		message := messages[i]
		role := strings.TrimSpace(strings.ToLower(message.Role))
		switch role {
		case "system", "user":
			if role == "user" && len(message.MultiContent) > 0 {
				parts := SanitizeOpenAICompatMultiContent(message.MultiContent)
				if len(parts) == 0 {
					continue
				}
				sanitized = append(sanitized, openai.ChatCompletionMessage{Role: role, MultiContent: parts})
				continue
			}
			text := strings.TrimSpace(extractOpenAICompatContent(message.Content))
			if text == "" {
				continue
			}
			if role == "system" && len(sanitized) > 0 && sanitized[len(sanitized)-1].Role == role {
				sanitized[len(sanitized)-1].Content = strings.TrimSpace(extractOpenAICompatContent(sanitized[len(sanitized)-1].Content) + "\n\n" + text)
				continue
			}
			sanitized = append(sanitized, openai.ChatCompletionMessage{
				Role:    role,
				Content: text,
			})
		case "assistant":
			validCalls, _ := validateOpenAICompatToolCalls(message.ToolCalls) // zero value fallback is intentional
			text := strings.TrimSpace(extractOpenAICompatContent(message.Content))
			if len(validCalls) == 0 {
				if text == "" {
					continue
				}
				sanitized = append(sanitized, openai.ChatCompletionMessage{
					Role:    "assistant",
					Content: text,
				})
				continue
			}
			responded := map[string]openai.ChatCompletionMessage{}
			j := i + 1
			for ; j < len(messages); j++ {
				next := messages[j]
				if strings.TrimSpace(strings.ToLower(next.Role)) != "tool" {
					break
				}
				toolCallID := strings.TrimSpace(next.ToolCallID)
				if toolCallID == "" {
					continue
				}
				for _, call := range validCalls {
					if call.ID == toolCallID {
						responded[toolCallID] = openai.ChatCompletionMessage{
							Role:       "tool",
							ToolCallID: toolCallID,
							Name:       strings.TrimSpace(next.Name),
							Content:    strings.TrimSpace(extractOpenAICompatContent(next.Content)),
						}
						break
					}
				}
			}
			filteredCalls := make([]openai.ToolCall, 0, len(validCalls))
			for _, call := range validCalls {
				if _, ok := responded[call.ID]; ok {
					filteredCalls = append(filteredCalls, call)
				}
			}
			if len(filteredCalls) == 0 {
				if text == "" {
					i = j - 1
					continue
				}
				sanitized = append(sanitized, openai.ChatCompletionMessage{
					Role:    "assistant",
					Content: text,
				})
				i = j - 1
				continue
			}
			sanitized = append(sanitized, openai.ChatCompletionMessage{
				Role:      "assistant",
				Content:   text,
				ToolCalls: filteredCalls,
			})
			for _, call := range filteredCalls {
				sanitized = append(sanitized, responded[call.ID])
			}
			i = j - 1
		case "tool":
			continue
		}
	}
	return sanitized
}

// SanitizeOpenAICompatMultiContent filters and returns only valid multi-content
// parts (text and image URL) for an OpenAI-compatible request.
func SanitizeOpenAICompatMultiContent(parts []openai.ChatMessagePart) []openai.ChatMessagePart {
	if len(parts) == 0 {
		return nil
	}
	filtered := make([]openai.ChatMessagePart, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case openai.ChatMessagePartTypeText:
			text := strings.TrimSpace(part.Text)
			if text == "" {
				continue
			}
			filtered = append(filtered, openai.ChatMessagePart{Type: openai.ChatMessagePartTypeText, Text: text})
		case openai.ChatMessagePartTypeImageURL:
			if part.ImageURL == nil || strings.TrimSpace(part.ImageURL.URL) == "" {
				continue
			}
			filtered = append(filtered, openai.ChatMessagePart{Type: openai.ChatMessagePartTypeImageURL, ImageURL: part.ImageURL})
		}
	}
	return filtered
}

// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------

func validateOpenAICompatToolCalls(calls []openai.ToolCall) ([]openai.ToolCall, error) {
	validated := make([]openai.ToolCall, 0, len(calls))
	for _, call := range calls {
		callID := strings.TrimSpace(call.ID)
		if callID == "" {
			return nil, fmt.Errorf("provider returned tool call with empty id")
		}
		if strings.TrimSpace(string(call.Type)) == "" {
			call.Type = openai.ToolTypeFunction
		}
		if strings.TrimSpace(string(call.Type)) != string(openai.ToolTypeFunction) {
			return nil, fmt.Errorf("provider returned unsupported tool call type %q", call.Type)
		}
		call.Function.Name = strings.TrimSpace(call.Function.Name)
		if call.Function.Name == "" {
			return nil, fmt.Errorf("provider returned tool call %q with empty function name", callID)
		}
		call.ID = callID
		validated = append(validated, call)
	}
	return validated, nil
}

func extractOpenAICompatContent(content any) string {
	switch value := content.(type) {
	case string:
		return value
	default:
		return ""
	}
}
