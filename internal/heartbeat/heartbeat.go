package heartbeat

import (
	"strings"
)

const (
	HeartbeatPromptDefault      = "Read HEARTBEAT.md if it exists (workspace context). Follow it strictly. Do not infer or repeat old tasks from prior chats. If nothing needs attention, reply HEARTBEAT_OK."
	HeartbeatEveryDefault       = "30m"
	HeartbeatAckMaxCharsDefault = 300
	HeartbeatToken              = "HEARTBEAT_OK"
)

func ResolveHeartbeatPrompt(raw string) string {
	if trimmed := strings.TrimSpace(raw); trimmed != "" {
		return trimmed
	}
	return HeartbeatPromptDefault
}

func StripHeartbeatToken(raw string, maxAckChars int) (text string, skip bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", true
	}
	if maxAckChars <= 0 {
		maxAckChars = HeartbeatAckMaxCharsDefault
	}
	if !strings.Contains(raw, HeartbeatToken) {
		return raw, false
	}
	cleaned := strings.TrimSpace(strings.ReplaceAll(raw, HeartbeatToken, ""))
	if cleaned == "" {
		return "", true
	}
	if len(cleaned) <= maxAckChars {
		return "", true
	}
	return cleaned, false
}
