// transcript.go — session-level transcript helpers.
//
// Functions here operate directly on a *SessionStore and have no dependency
// on the runtime orchestrator, making them reusable across the runtime and
// any future sub-systems.
package session

import (
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/utils"
)

// AppendIncomingUserTranscript appends the incoming user message to the
// session transcript.  It is a no-op for heartbeat/maintenance runs or when
// the message and all attachments are empty.
// workspaceDir is used to persist image attachments as files so they survive
// page refresh.  Pass an empty string to skip attachment persistence.
func AppendIncomingUserTranscript(sessions *SessionStore, session core.SessionResolution, req core.AgentRunRequest, timestamp time.Time, workspaceDir string) error {
	if sessions == nil || req.IsHeartbeat || req.IsMaintenance {
		return nil
	}
	text := strings.TrimSpace(req.Message)

	// Persist image attachments to the workspace uploads directory so they can
	// be served by the media proxy and displayed in chat history after refresh.
	var savedPaths []string
	if strings.TrimSpace(workspaceDir) != "" {
		uploadsDir := filepath.Join(workspaceDir, ".uploads")
		_ = os.MkdirAll(uploadsDir, 0o755)
		for i, att := range req.Attachments {
			if !attachmentIsImage(att) || len(att.Content) == 0 {
				continue
			}
			ext := resolveImageExt(att)
			fileName := fmt.Sprintf("%d_%d%s", timestamp.UnixMilli(), i, ext)
			fullPath := filepath.Join(uploadsDir, fileName)
			if err := os.WriteFile(fullPath, att.Content, 0o644); err == nil {
				savedPaths = append(savedPaths, utils.FileURI(fullPath))
			}
		}
	}

	if text == "" && len(savedPaths) == 0 {
		return nil
	}

	msg := core.TranscriptMessage{
		Role:      "user",
		Text:      text,
		RunID:     req.RunID,
		Timestamp: timestamp,
	}
	if req.IsSubagentAnnouncement {
		msg.Type = "subagent_completion"
	}
	if len(savedPaths) == 1 {
		msg.MediaURL = savedPaths[0]
	} else if len(savedPaths) > 1 {
		msg.MediaURLs = savedPaths
	}
	return sessions.AppendTranscript(session.SessionKey, session.SessionID, msg)
}

// resolveImageExt returns a file extension (.jpg, .png, etc.) for an image
// attachment based on its MIME type or original filename.
func resolveImageExt(att core.Attachment) string {
	mimeType := normalizeAttachmentMime(att)
	if mimeType != "" {
		exts, _ := mime.ExtensionsByType(mimeType)
		for _, e := range exts {
			switch e {
			case ".jpg", ".jpeg", ".png", ".gif", ".webp":
				return e
			}
		}
	}
	if ext := strings.ToLower(filepath.Ext(att.Name)); ext != "" {
		return ext
	}
	return ".jpg"
}

// normalizeAttachmentMime extracts the primary MIME type from an attachment.
func normalizeAttachmentMime(att core.Attachment) string {
	if mt := strings.TrimSpace(strings.ToLower(strings.Split(att.MIMEType, ";")[0])); mt != "" {
		return mt
	}
	if ext := strings.TrimSpace(strings.ToLower(filepath.Ext(att.Name))); ext != "" {
		if guessed := strings.TrimSpace(strings.ToLower(strings.Split(mime.TypeByExtension(ext), ";")[0])); guessed != "" {
			return guessed
		}
	}
	return ""
}

// attachmentIsImage reports whether the attachment is an image based on its MIME type.
func attachmentIsImage(att core.Attachment) bool {
	return strings.HasPrefix(normalizeAttachmentMime(att), "image/")
}
