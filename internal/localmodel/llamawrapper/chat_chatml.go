package llamawrapper

import "strings"

// ── ChatML family shared constants ────────────────────────────────────────────
// Used by ChatML, Qwen3, and Qwen3.5 renderers/parsers.

const (
	imStart = "<|im_start|>"
	imEnd   = "<|im_end|>"

	thinkOpen  = "<think>"
	thinkClose = "</think>"
)

func init() {
	registerStopTokens("chatml", []string{imEnd})
}

// ── ChatML renderer (default/legacy) ─────────────────────────────────────────

// renderChatML converts messages to ChatML format.
func renderChatML(messages []ChatMessage) string {
	var sb strings.Builder
	for _, msg := range messages {
		sb.WriteString(imStart)
		sb.WriteString(msg.Role)
		sb.WriteByte('\n')
		if content, ok := msg.Content.(string); ok {
			sb.WriteString(content)
		}
		sb.WriteByte('\n')
		sb.WriteString(imEnd)
		sb.WriteByte('\n')
	}
	sb.WriteString(imStart)
	sb.WriteString("assistant\n")
	return sb.String()
}
