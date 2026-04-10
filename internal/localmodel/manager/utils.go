package manager

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ── Utility functions ───────────────────────────────────────────────────────

// HumanModelName converts a model filename stem into a human-readable name.
func HumanModelName(id string) string {
	name := strings.ReplaceAll(id, "-", " ")
	name = strings.ReplaceAll(name, "_", " ")
	if name == "" {
		return id
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// FormatBytes formats a byte count as a human-readable string.
func FormatBytes(b int64) string {
	const (
		_        = iota
		kB int64 = 1 << (10 * iota)
		mB
		gB
	)
	switch {
	case b >= gB:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(gB))
	case b >= mB:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mB))
	case b >= kB:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(kB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// ContainsSensitiveKeywords checks if tool parameters contain sensitive keywords.
func ContainsSensitiveKeywords(params map[string]any) bool {
	if len(params) == 0 {
		return false
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(paramsJSON))
	for _, kw := range SensitiveKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// SensitiveKeywords triggers forced review when found in tool arguments.
var SensitiveKeywords = []string{
	"rm", "curl", "wget", "ssh", "scp", "token", "password", "secret",
	"credential", "key", "delete", "drop", "truncate", "format",
	"sudo", "chmod", "chown", "kill", "reboot", "shutdown",
}
