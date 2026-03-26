// visible_payloads.go — extracts visible assistant text payloads from a
// ReplyDispatcher's transcript buffer.
//
// Moved from runtime/runtime_transcript.go (was visibleAssistantPayloadsFromDispatcher).
// Lives in the delivery package because it only operates on *ReplyDispatcher.
package delivery

import (
	"strings"

	"github.com/kocort/kocort/internal/core"
)

// VisibleAssistantPayloads extracts all visible assistant text payloads from
// the dispatcher's recorded transcript messages.  It is used by the pipeline
// to detect whether any output was streamed before an error occurred, and by
// compaction to extract the LLM summary text.
func VisibleAssistantPayloads(dispatcher *ReplyDispatcher) []core.ReplyPayload {
	if dispatcher == nil {
		return nil
	}
	messages := dispatcher.TranscriptMessages()
	if len(messages) == 0 {
		return nil
	}
	var (
		payloads []core.ReplyPayload
		partials strings.Builder
	)
	flushPartials := func() {
		text := strings.TrimSpace(partials.String())
		if text == "" {
			partials.Reset()
			return
		}
		payloads = append(payloads, core.ReplyPayload{Text: text})
		partials.Reset()
	}
	for _, msg := range messages {
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
			continue
		}
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			continue
		}
		switch strings.TrimSpace(strings.ToLower(msg.Type)) {
		case "assistant_partial":
			partials.WriteString(text)
		case "", "assistant_final":
			flushPartials()
			payloads = append(payloads, core.ReplyPayload{Text: text})
		}
	}
	flushPartials()
	return payloads
}
