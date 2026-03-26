// Canonical implementation — migrated from runtime/skill_commands.go.
package skill

import (
	"strings"

	"github.com/kocort/kocort/internal/core"
)

type SkillCommandInvocation struct {
	Command core.SkillCommandSpec
	Args    string
}

func ResolveSkillCommandInvocation(snapshot *core.SkillSnapshot, message string) *SkillCommandInvocation {
	if snapshot == nil || len(snapshot.Commands) == 0 {
		return nil
	}
	trimmed := stripTimestampPrefix(message)
	if !strings.HasPrefix(trimmed, "/") {
		return nil
	}
	match := strings.Fields(trimmed)
	if len(match) == 0 {
		return nil
	}
	commandToken := strings.TrimPrefix(match[0], "/")
	if commandToken == "" {
		return nil
	}
	if strings.EqualFold(commandToken, "skill") {
		if len(match) < 2 {
			return nil
		}
		skillToken := match[1]
		command := findSkillCommand(snapshot.Commands, skillToken)
		if command == nil {
			return nil
		}
		args := ""
		if len(trimmed) > len(match[0])+len(skillToken)+1 {
			args = strings.TrimSpace(trimmed[len(match[0])+len(skillToken)+1:])
		}
		return &SkillCommandInvocation{
			Command: *command,
			Args:    args,
		}
	}
	args := ""
	if len(trimmed) > len(match[0]) {
		args = strings.TrimSpace(trimmed[len(match[0]):])
	}
	command := findSkillCommand(snapshot.Commands, commandToken)
	if command == nil {
		return nil
	}
	return &SkillCommandInvocation{
		Command: *command,
		Args:    args,
	}
}

func stripTimestampPrefix(message string) string {
	trimmed := strings.TrimSpace(message)
	if strings.HasPrefix(trimmed, "[") {
		if end := strings.Index(trimmed, "] "); end > 0 {
			return strings.TrimSpace(trimmed[end+2:])
		}
	}
	return trimmed
}

func normalizeSkillCommandLookup(value string) string {
	return strings.NewReplacer(" ", "-", "_", "-").Replace(strings.ToLower(strings.TrimSpace(value)))
}

func findSkillCommand(commands []core.SkillCommandSpec, rawName string) *core.SkillCommandSpec {
	trimmed := strings.TrimSpace(rawName)
	if trimmed == "" {
		return nil
	}
	lowered := strings.ToLower(trimmed)
	normalized := normalizeSkillCommandLookup(trimmed)
	for i := range commands {
		entry := &commands[i]
		if strings.ToLower(entry.Name) == lowered || strings.ToLower(entry.SkillName) == lowered {
			return entry
		}
		if normalizeSkillCommandLookup(entry.Name) == normalized || normalizeSkillCommandLookup(entry.SkillName) == normalized {
			return entry
		}
	}
	return nil
}
