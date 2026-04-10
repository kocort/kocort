package api

// ── Helpers ──────────────────────────────────────────────────────────────────

// BoolPtr returns a pointer to the given bool value.
func BoolPtr(v bool) *bool { return &v }

// IntPtr returns a pointer to the given int value.
func IntPtr(v int) *int { return &v }

// Float64Ptr returns a pointer to the given float64 value.
func Float64Ptr(v float64) *float64 { return &v }
