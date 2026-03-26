// Package gateway — chat abort trigger detection.
package gateway

import "strings"

// ChatAbortTriggers is the set of recognized abort trigger phrases.
var ChatAbortTriggers = map[string]struct{}{
	"stop":                    {},
	"esc":                     {},
	"abort":                   {},
	"wait":                    {},
	"exit":                    {},
	"interrupt":               {},
	"detente":                 {},
	"deten":                   {},
	"detén":                   {},
	"arrete":                  {},
	"arrête":                  {},
	"停止":                      {},
	"やめて":                     {},
	"止めて":                     {},
	"रुको":                    {},
	"توقف":                    {},
	"стоп":                    {},
	"остановись":              {},
	"останови":                {},
	"остановить":              {},
	"прекрати":                {},
	"halt":                    {},
	"anhalten":                {},
	"aufhören":                {},
	"hoer auf":                {},
	"stopp":                   {},
	"pare":                    {},
	"stop kocort":             {},
	"kocort stop":             {},
	"stop action":             {},
	"stop current action":     {},
	"stop run":                {},
	"stop current run":        {},
	"stop agent":              {},
	"stop the agent":          {},
	"stop don't do anything":  {},
	"stop dont do anything":   {},
	"stop do not do anything": {},
	"stop doing anything":     {},
	"do not do that":          {},
	"please stop":             {},
	"stop please":             {},
}

// TrailingAbortPunctuation lists punctuation stripped when normalising abort text.
var TrailingAbortPunctuation = []string{
	".", "!", "?", "…", ",", "，", "。", ";", "；", ":", "：", "'", "\"", "'", "\u201d", ")", "]", "}",
}

// NormalizeAbortTriggerText normalises user text for abort-trigger matching.
func NormalizeAbortTriggerText(text string) string {
	normalized := strings.TrimSpace(strings.ToLower(text))
	normalized = strings.ReplaceAll(normalized, "\u2019", "'")
	normalized = strings.ReplaceAll(normalized, "`", "'")
	normalized = strings.Join(strings.Fields(normalized), " ")
	for {
		trimmed := normalized
		for _, suffix := range TrailingAbortPunctuation {
			trimmed = strings.TrimSuffix(trimmed, suffix)
		}
		trimmed = strings.TrimSpace(trimmed)
		if trimmed == normalized {
			break
		}
		normalized = trimmed
	}
	return normalized
}

// IsChatStopCommandText returns true when the user text is a recognised stop command.
func IsChatStopCommandText(text string) bool {
	normalized := NormalizeAbortTriggerText(text)
	if normalized == "" {
		return false
	}
	if normalized == "/stop" {
		return true
	}
	_, ok := ChatAbortTriggers[normalized]
	return ok
}

// FormatChatAbortReplyText returns the standard abort acknowledgment message.
func FormatChatAbortReplyText() string {
	return "⚙️ Agent was aborted."
}
