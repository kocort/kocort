// Package utils provides common, business-agnostic utility functions shared
// across the entire kocort module.
package utils

import "strings"

// NonEmpty returns primary if it is non-blank (after trimming whitespace),
// otherwise it returns fallback.
func NonEmpty(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	return fallback
}

// FirstNonEmpty returns the first non-blank string (after trimming) from the
// variadic arguments. Returns "" if all values are blank.
func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
