// Pure tool parameter reading and result formatting utilities.
//
// These functions have no dependency on *Runtime, ToolContext, or AgentRunContext
// and can be used by any package that needs to parse tool call arguments or
// format tool results.
package tool

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
)

// ToolInputError represents an invalid parameter supplied to a tool call.
// It is a type alias for core.ToolInputError, kept here for backward
// compatibility with code that references tool.ToolInputError.
type ToolInputError = core.ToolInputError

// JSONResult marshals v to JSON and wraps it in a core.ToolResult.
func JSONResult(v any) (core.ToolResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return core.ToolResult{}, err
	}
	return core.ToolResult{
		Text: string(data),
		JSON: data,
	}, nil
}

// MediaResult creates a ToolResult with a media URL to be sent to the user.
// The text parameter is the visible description, and mediaURLs are the media URLs to send.
func MediaResult(text string, mediaURLs ...string) core.ToolResult {
	result := core.ToolResult{Text: strings.TrimSpace(text)}
	if len(mediaURLs) == 1 {
		result.MediaURL = strings.TrimSpace(mediaURLs[0])
	} else if len(mediaURLs) > 1 {
		result.MediaURLs = mediaURLs
	}
	return result
}

// ResolveToolResultText returns the visible text of a ToolResult, preferring
// the Text field and falling back to the JSON field.
func ResolveToolResultText(result core.ToolResult) string {
	text := strings.TrimSpace(result.Text)
	if text == "" && len(result.JSON) > 0 {
		text = strings.TrimSpace(string(result.JSON))
	}
	return text
}

// ResolveToolResultHistoryContent returns the content to record in history,
// defaulting to "{}" when the result is empty.
func ResolveToolResultHistoryContent(result core.ToolResult) string {
	text := ResolveToolResultText(result)
	if result.MediaURL != "" || len(result.MediaURLs) > 0 {
		mediaInfo := map[string]any{}
		if result.MediaURL != "" {
			mediaInfo["mediaUrl"] = result.MediaURL
		}
		if len(result.MediaURLs) > 0 {
			mediaInfo["mediaUrls"] = result.MediaURLs
		}
		if text == "" {
			if data, err := json.Marshal(mediaInfo); err == nil {
				return string(data)
			}
			return "{}"
		}
		// Merge with existing text if it's JSON
		var existing map[string]any
		if json.Unmarshal([]byte(text), &existing) == nil {
			for k, v := range mediaInfo {
				existing[k] = v
			}
			if data, err := json.Marshal(existing); err == nil {
				return string(data)
			}
		}
	}
	if text == "" {
		return "{}"
	}
	return text
}

// IsRecoverableToolFailureMessage returns true when the error message suggests
// a transient / correctable failure (missing param, invalid input, etc.).
func IsRecoverableToolFailureMessage(message string) bool {
	msg := strings.ToLower(strings.TrimSpace(message))
	switch {
	case strings.Contains(msg, "required"),
		strings.Contains(msg, "missing"),
		strings.Contains(msg, "invalid"):
		return true
	default:
		return false
	}
}

// ReadStringParam reads a string parameter from a tool args map.
func ReadStringParam(params map[string]any, key string, required bool) (string, error) {
	raw, ok := params[key]
	if !ok || raw == nil {
		if required {
			return "", ToolInputError{Message: fmt.Sprintf("missing required parameter %q", key)}
		}
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", ToolInputError{Message: fmt.Sprintf("parameter %q must be a string", key)}
	}
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", ToolInputError{Message: fmt.Sprintf("parameter %q must not be empty", key)}
	}
	return value, nil
}

// ReadOptionalPositiveDurationParam reads an optional numeric duration parameter.
func ReadOptionalPositiveDurationParam(params map[string]any, key string, unit time.Duration) (time.Duration, error) {
	raw, ok := params[key]
	if !ok || raw == nil {
		return 0, nil
	}
	switch value := raw.(type) {
	case float64:
		if value <= 0 {
			return 0, nil
		}
		return time.Duration(value * float64(unit)), nil
	case int:
		if value <= 0 {
			return 0, nil
		}
		return time.Duration(value) * unit, nil
	case int64:
		if value <= 0 {
			return 0, nil
		}
		return time.Duration(value) * unit, nil
	default:
		return 0, ToolInputError{Message: fmt.Sprintf("parameter %q must be a positive number", key)}
	}
}

// ReadOptionalIntParam reads an optional integer parameter from common JSON number encodings.
func ReadOptionalIntParam(params map[string]any, key string) (int, error) {
	raw, ok := params[key]
	if !ok || raw == nil {
		return 0, nil
	}
	switch value := raw.(type) {
	case float64:
		if math.Trunc(value) != value {
			return 0, ToolInputError{Message: fmt.Sprintf("parameter %q must be an integer", key)}
		}
		return int(value), nil
	case int:
		return value, nil
	case int64:
		return int(value), nil
	default:
		return 0, ToolInputError{Message: fmt.Sprintf("parameter %q must be an integer", key)}
	}
}

// ReadBoolParam reads an optional boolean parameter.
func ReadBoolParam(params map[string]any, key string) (bool, error) {
	raw, ok := params[key]
	if !ok || raw == nil {
		return false, nil
	}
	value, ok := raw.(bool)
	if !ok {
		return false, ToolInputError{Message: fmt.Sprintf("parameter %q must be a boolean", key)}
	}
	return value, nil
}

// ReadOptionalStringMapParam reads an optional map[string]string parameter.
func ReadOptionalStringMapParam(params map[string]any, key string) (map[string]string, error) {
	raw, ok := params[key]
	if !ok || raw == nil {
		return nil, nil
	}
	typed, ok := raw.(map[string]any)
	if !ok {
		return nil, ToolInputError{Message: fmt.Sprintf("parameter %q must be an object", key)}
	}
	out := make(map[string]string, len(typed))
	for mapKey, value := range typed {
		text, ok := value.(string)
		if !ok {
			return nil, ToolInputError{Message: fmt.Sprintf("parameter %q.%s must be a string", key, mapKey)}
		}
		out[mapKey] = text
	}
	return out, nil
}

// ExtractReservedToolRuntimeArgs strips reserved runtime arguments (like
// __toolCallId) from the args map and returns them separately.
func ExtractReservedToolRuntimeArgs(args map[string]any) (string, map[string]any) {
	if len(args) == 0 {
		return "", map[string]any{}
	}
	out := make(map[string]any, len(args))
	toolCallID := ""
	for key, value := range args {
		switch key {
		case "__toolCallId":
			if text, ok := value.(string); ok {
				toolCallID = strings.TrimSpace(text)
			}
		default:
			out[key] = value
		}
	}
	return toolCallID, out
}
