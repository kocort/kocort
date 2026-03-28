package heartbeat

import "strings"

// IsHeartbeatContentEffectivelyEmpty reports whether HEARTBEAT.md has no
// actionable content. Comment-only headings, blank lines, and empty checklist
// markers are treated as empty so the heartbeat runner can skip pointless runs.
func IsHeartbeatContentEffectivelyEmpty(content string) bool {
	if strings.TrimSpace(content) == "" {
		return true
	}
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if isMarkdownHeading(trimmed) {
			continue
		}
		if isEmptyChecklist(trimmed) {
			continue
		}
		return false
	}
	return true
}

func isMarkdownHeading(line string) bool {
	if line == "" || line[0] != '#' {
		return false
	}
	hashes := 0
	for hashes < len(line) && line[hashes] == '#' {
		hashes++
	}
	if hashes >= len(line) {
		return true
	}
	return line[hashes] == ' ' || line[hashes] == '\t'
}

func isEmptyChecklist(line string) bool {
	if len(line) == 0 {
		return false
	}
	switch line[0] {
	case '-', '*', '+':
	default:
		return false
	}
	rest := strings.TrimSpace(line[1:])
	if rest == "" {
		return true
	}
	switch rest {
	case "[]", "[ ]", "[x]", "[X]":
		return true
	default:
		return false
	}
}
