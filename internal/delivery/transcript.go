// transcript.go — run-level transcript helpers for the delivery layer.
//
// These helpers convert dispatcher output and final payloads to
// TranscriptMessages and persist run artifacts back to the session store.
// They live here (rather than in session/) because they depend on
// ReplyDispatcher, which itself imports session — moving them to session/
// would create an import cycle.
package delivery

import (
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/heartbeat"
	"github.com/kocort/kocort/internal/session"
)

// TranscriptMessagesForPersistence converts dispatcher messages and final
// payloads to a deduplicated slice of TranscriptMessages suitable for
// appending to the session transcript.
func TranscriptMessagesForPersistence(userMessage string, startedAt time.Time, dispatcher *ReplyDispatcher, finalPayloads []core.ReplyPayload, isHeartbeat bool, runID string) []core.TranscriptMessage {
	transcript := make([]core.TranscriptMessage, 0, len(finalPayloads))
	seen := map[string]struct{}{}
	if dispatcher != nil {
		for _, msg := range dispatcher.TranscriptMessages() {
			text := strings.TrimSpace(msg.Text)
			if isHeartbeat {
				if stripped, skip := heartbeat.StripHeartbeatToken(text, 0); skip {
					continue
				} else if stripped != "" {
					text = stripped
				}
			}
			if text == "" {
				continue
			}
			msg.Text = text
			msg.RunID = runID
			key := msg.Type + "\x00" + text
			seen[key] = struct{}{}
			transcript = append(transcript, msg)
		}
	}
	for _, payload := range finalPayloads {
		text := strings.TrimSpace(payload.Text)
		if isHeartbeat {
			if stripped, skip := heartbeat.StripHeartbeatToken(text, 0); skip {
				continue
			} else if stripped != "" {
				text = stripped
			}
		}
		if text == "" && payload.MediaURL == "" && len(payload.MediaURLs) == 0 {
			continue
		}
		key := "assistant_final\x00" + text
		if _, ok := seen[key]; ok {
			continue
		}
		transcript = append(transcript, core.TranscriptMessage{
			Type:      "assistant_final",
			Role:      "assistant",
			Text:      text,
			RunID:     runID,
			Timestamp: time.Now().UTC(),
			Final:     true,
			MediaURL:  payload.MediaURL,
			MediaURLs: payload.MediaURLs,
		})
	}
	return transcript
}

// PersistRunArtifacts persists the results of a tool-dispatch or model-call
// run: upserts the session entry with updated metadata and appends the
// assistant reply to the transcript.
func PersistRunArtifacts(sessions *session.SessionStore, sess core.SessionResolution, req core.AgentRunRequest, skillsSnapshot *core.SkillSnapshot, assistantText string) error {
	if err := sessions.Upsert(sess.SessionKey, core.SessionEntry{
		SessionID:          sess.SessionID,
		UpdatedAt:          time.Now().UTC(),
		ThinkingLevel:      req.Thinking,
		VerboseLevel:       req.Verbose,
		ProviderOverride:   req.SessionProviderOverride,
		ModelOverride:      req.SessionModelOverride,
		SpawnedBy:          req.SpawnedBy,
		SpawnDepth:         req.SpawnDepth,
		LastChannel:        req.Channel,
		LastTo:             req.To,
		LastAccountID:      req.AccountID,
		LastThreadID:       req.ThreadID,
		LastChatType:       req.ChatType,
		LastActivityReason: "turn",
		DeliveryContext: &core.DeliveryContext{
			Channel:   req.Channel,
			To:        req.To,
			AccountID: req.AccountID,
			ThreadID:  req.ThreadID,
		},
		SkillsSnapshot: skillsSnapshot,
	}); err != nil {
		return err
	}
	transcript := TranscriptMessagesForPersistence(req.Message, time.Now().UTC(), nil, []core.ReplyPayload{{Text: assistantText}}, req.IsHeartbeat, req.RunID)
	return sessions.AppendTranscript(sess.SessionKey, sess.SessionID, transcript...)
}
