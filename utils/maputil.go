package utils

// MapString extracts a string value for key from a map[string]any.
// Returns "" when the map is nil or the value is not a string.
func MapString(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	if value, ok := data[key].(string); ok {
		return value
	}
	return ""
}

// CloneAnyMap returns a shallow copy of a map[string]any.
// Returns nil if the input is nil or empty.
func CloneAnyMap(m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
