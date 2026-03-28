package heartbeat

import "strings"

func BuildExecEventPrompt(deliver bool) string {
	if deliver {
		return "An execution completed. Review the latest result and notify the user only if attention is needed."
	}
	return "An execution completed. Review the latest result and decide whether any follow-up is needed. If not, reply HEARTBEAT_OK."
}

func BuildCronEventPrompt(events []string, deliver bool) string {
	intro := "A scheduled reminder has been triggered."
	if len(events) > 0 {
		intro += "\nPending reminder events:\n- " + strings.Join(events, "\n- ")
	}
	if deliver {
		return intro + "\nIf action is needed, send the concise reminder to the user. Otherwise reply HEARTBEAT_OK."
	}
	return intro + "\nReview whether any action is needed. If not, reply HEARTBEAT_OK."
}

func IsExecCompletionEvent(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(text, "exec") && (strings.Contains(text, "completed") || strings.Contains(text, "exit"))
}

func IsCronSystemEvent(text string) bool {
	text = strings.TrimSpace(strings.ToLower(text))
	if text == "" {
		return false
	}
	if strings.Contains(text, "heartbeat_ok") || strings.Contains(text, "heartbeat poll") || strings.Contains(text, "heartbeat wake") {
		return false
	}
	return true
}
