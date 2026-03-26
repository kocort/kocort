package heartbeat

import "strings"

func BuildCronEventPrompt(pendingEvents []string, deliverToUser bool) string {
	eventText := strings.TrimSpace(strings.Join(pendingEvents, "\n"))
	if eventText == "" {
		if !deliverToUser {
			return "A scheduled cron event was triggered, but no event content was found. Handle this internally and reply HEARTBEAT_OK when nothing needs user-facing follow-up."
		}
		return "A scheduled cron event was triggered, but no event content was found. Reply HEARTBEAT_OK."
	}
	if !deliverToUser {
		return "A scheduled reminder has been triggered. The reminder content is:\n\n" +
			eventText +
			"\n\nHandle this reminder internally. Do not relay it to the user unless explicitly requested."
	}
	return "A scheduled reminder has been triggered. The reminder content is:\n\n" +
		eventText +
		"\n\nPlease relay this reminder to the user in a helpful and friendly way."
}

func BuildExecEventPrompt(deliverToUser bool) string {
	if !deliverToUser {
		return "An async command you ran earlier has completed. The result is shown in the system messages above. Handle the result internally. Do not relay it to the user unless explicitly requested."
	}
	return "An async command you ran earlier has completed. The result is shown in the system messages above. Please relay the command output to the user in a helpful way. If the command succeeded, share the relevant output. If it failed, explain what went wrong."
}

func IsExecCompletionEvent(text string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(text)), "exec finished")
}

func IsCronSystemEvent(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	return !strings.Contains(lower, "heartbeat poll") &&
		!strings.Contains(lower, "heartbeat wake") &&
		!strings.HasPrefix(lower, strings.ToLower(HeartbeatToken)) &&
		!IsExecCompletionEvent(trimmed)
}
