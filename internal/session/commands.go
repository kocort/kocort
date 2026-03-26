// commands.go — session command parsing helpers.
//
// Pure string-matching utilities for recognising session reset triggers and
// the /compact command.  They are placed in the session package because they
// represent session-level protocol, and they have no dependencies beyond the
// standard library.
package session

import (
	"strings"

	"github.com/kocort/kocort/internal/core"
)

// SessionResetCommandMatch describes a parsed reset/new command.
type SessionResetCommandMatch struct {
	Trigger   string
	Remainder string
	Reason    string
}

// SessionCompactCommandMatch describes a parsed compact command.
type SessionCompactCommandMatch struct {
	Trigger      string
	Instructions string
}

// ReplyText returns the default acknowledgement text for a matched reset
// command when there is no remainder to forward into a follow-up run.
func (m SessionResetCommandMatch) ReplyText() string {
	if strings.TrimSpace(strings.ToLower(m.Trigger)) == "/reset" {
		return "Session reset."
	}
	return "Started a new session."
}

// MatchSessionResetTrigger checks whether message matches one of the
// configured reset triggers.  Returns (trigger, remainder) on a match, or
// ("", "") when no trigger is found.
func MatchSessionResetTrigger(triggers []string, message string) (string, string) {
	trimmed := NormalizeSessionCommandMessage(message, "")
	lowered := strings.ToLower(trimmed)
	for _, trigger := range triggers {
		trigger = strings.TrimSpace(strings.ToLower(trigger))
		if trigger == "" {
			continue
		}
		if lowered == trigger {
			return trigger, ""
		}
		if strings.HasPrefix(lowered, trigger+" ") {
			return trigger, strings.TrimSpace(trimmed[len(trigger):])
		}
	}
	return "", ""
}

// MatchCompactCommand checks whether message is a /compact command.
// Returns (trigger, instructions) on a match, or ("", "") otherwise.
func MatchCompactCommand(message string) (string, string) {
	trimmed := NormalizeSessionCommandMessage(message, "")
	lowered := strings.ToLower(trimmed)
	if lowered == "/compact" {
		return "/compact", ""
	}
	if strings.HasPrefix(lowered, "/compact ") {
		return "/compact", strings.TrimSpace(trimmed[len("/compact"):])
	}
	return "", ""
}

// MatchCompactCommandForChatType matches a compact command after normalizing
// structural prefixes for the given chat type.
func MatchCompactCommandForChatType(message string, chatType core.ChatType) (string, string) {
	trimmed := NormalizeSessionCommandMessage(message, chatType)
	lowered := strings.ToLower(trimmed)
	if lowered == "/compact" {
		return "/compact", ""
	}
	if strings.HasPrefix(lowered, "/compact ") {
		return "/compact", strings.TrimSpace(trimmed[len("/compact"):])
	}
	return "", ""
}

// TriggerToReason converts a reset trigger string to a session reset reason.
func TriggerToReason(trigger string) string {
	if strings.TrimSpace(strings.ToLower(trigger)) == "/new" {
		return "new"
	}
	return "reset"
}

// MatchSessionResetTriggerForChatType matches reset triggers after normalizing
// structural prefixes for the given chat type.
func MatchSessionResetTriggerForChatType(triggers []string, message string, chatType core.ChatType) (string, string) {
	trimmed := NormalizeSessionCommandMessage(message, chatType)
	lowered := strings.ToLower(trimmed)
	for _, trigger := range triggers {
		trigger = strings.TrimSpace(strings.ToLower(trigger))
		if trigger == "" {
			continue
		}
		if lowered == trigger {
			return trigger, ""
		}
		if strings.HasPrefix(lowered, trigger+" ") {
			return trigger, strings.TrimSpace(trimmed[len(trigger):])
		}
	}
	return "", ""
}

// ParseSessionResetCommandForChatType parses a reset/new command into a single
// domain object that callers can use without re-deriving reason or reply text.
func ParseSessionResetCommandForChatType(triggers []string, message string, chatType core.ChatType) (SessionResetCommandMatch, bool) {
	trigger, remainder := MatchSessionResetTriggerForChatType(triggers, message, chatType)
	if trigger == "" {
		return SessionResetCommandMatch{}, false
	}
	return SessionResetCommandMatch{
		Trigger:   trigger,
		Remainder: remainder,
		Reason:    TriggerToReason(trigger),
	}, true
}

// ParseSessionCompactCommandForChatType parses a compact command into a single
// domain object.
func ParseSessionCompactCommandForChatType(message string, chatType core.ChatType) (SessionCompactCommandMatch, bool) {
	trigger, instructions := MatchCompactCommandForChatType(message, chatType)
	if trigger == "" {
		return SessionCompactCommandMatch{}, false
	}
	return SessionCompactCommandMatch{
		Trigger:      trigger,
		Instructions: instructions,
	}, true
}

// NormalizeSessionCommandMessage strips structural wrappers that should not
// affect session command matching.
func NormalizeSessionCommandMessage(message string, chatType core.ChatType) string {
	trimmed := stripCommandTimestampPrefix(message)
	switch chatType {
	case core.ChatTypeGroup, core.ChatTypeTopic, core.ChatTypeThread:
		trimmed = stripLeadingMentionToken(trimmed)
	}
	return strings.TrimSpace(trimmed)
}

func stripCommandTimestampPrefix(message string) string {
	trimmed := strings.TrimSpace(message)
	if strings.HasPrefix(trimmed, "[") {
		if end := strings.Index(trimmed, "] "); end > 0 {
			return strings.TrimSpace(trimmed[end+2:])
		}
	}
	return trimmed
}

func stripLeadingMentionToken(message string) string {
	trimmed := strings.TrimSpace(message)
	if !strings.HasPrefix(trimmed, "@") {
		return trimmed
	}
	fields := strings.Fields(trimmed)
	if len(fields) <= 1 {
		return trimmed
	}
	return strings.Join(fields[1:], " ")
}
