// Package utils provides common, business-agnostic utility functions shared
// across the entire kocort module.
package utils

import (
	"fmt"
	"strconv"
	"strings"
)

// ReadMap performs a type assertion to map[string]any.
// Returns (nil, false) if value is nil or not a map.
func ReadMap(value any) (map[string]any, bool) {
	if value == nil {
		return nil, false
	}
	typed, ok := value.(map[string]any)
	return typed, ok
}

// ReadString coerces a value to a string.
// Returns the empty string if value is nil, the string itself if it already is
// one, or fmt.Sprintf("%v", value) for any other type.
func ReadString(value any) string {
	if value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", value)
}

// ReadNumber coerces a value to float64, returning 0 on failure.
func ReadNumber(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case int32:
		return float64(typed)
	case string:
		n, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64) // zero fallback is acceptable
		return n
	default:
		return 0
	}
}

// ReadBool coerces a value to bool, returning false on failure.
// Strings "1", "true", "yes", and "on" (case-insensitive) are treated as true.
func ReadBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		normalized := strings.TrimSpace(strings.ToLower(typed))
		return normalized == "1" || normalized == "true" || normalized == "yes" || normalized == "on"
	default:
		return false
	}
}

// ClampInt clamps a float64 value into the integer range [minValue, maxValue].
func ClampInt(value float64, minValue, maxValue int) int {
	if value < float64(minValue) {
		return minValue
	}
	if value > float64(maxValue) {
		return maxValue
	}
	return int(value)
}
