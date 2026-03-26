package utils

// BoolPtr returns a pointer to the given bool value.
func BoolPtr(v bool) *bool {
	return &v
}

// IntPtr returns a pointer to the given int value.
func IntPtr(v int) *int {
	return &v
}
