package infra

import (
	"encoding/base64"
	"fmt"
	"mime"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/kocort/kocort/internal/core"
)

const (
	MaxChatAttachmentBytesValue     = 5_000_000
	MaxPromptAttachmentTextChars    = 12_000
	MaxPromptAttachmentPreviewChars = 200
)

func MaxChatAttachmentBytes() int {
	return MaxChatAttachmentBytesValue
}

var TextAttachmentExtensions = map[string]struct{}{
	".txt": {}, ".md": {}, ".markdown": {}, ".json": {}, ".jsonl": {}, ".csv": {}, ".tsv": {},
	".yaml": {}, ".yml": {}, ".xml": {}, ".html": {}, ".htm": {}, ".css": {}, ".js": {},
	".mjs": {}, ".cjs": {}, ".ts": {}, ".tsx": {}, ".jsx": {}, ".go": {}, ".py": {},
	".java": {}, ".kt": {}, ".rb": {}, ".php": {}, ".rs": {}, ".c": {}, ".cc": {},
	".cpp": {}, ".h": {}, ".hpp": {}, ".sql": {}, ".sh": {}, ".ps1": {}, ".bat": {},
	".toml": {}, ".ini": {}, ".cfg": {}, ".conf": {}, ".log": {}, ".dockerfile": {},
}

func AttachmentDisplayName(att core.Attachment, fallback string) string {
	name := strings.TrimSpace(att.Name)
	if name != "" {
		return name
	}
	if label := strings.TrimSpace(att.Type); label != "" {
		return label
	}
	if fallback != "" {
		return fallback
	}
	return "attachment"
}

func NormalizeAttachmentMime(att core.Attachment) string {
	if mimeType := strings.TrimSpace(strings.ToLower(strings.Split(att.MIMEType, ";")[0])); mimeType != "" {
		return mimeType
	}
	if ext := strings.TrimSpace(strings.ToLower(filepath.Ext(att.Name))); ext != "" {
		if guessed := strings.TrimSpace(strings.ToLower(strings.Split(mime.TypeByExtension(ext), ";")[0])); guessed != "" {
			return guessed
		}
	}
	return ""
}

func AttachmentIsImage(att core.Attachment) bool {
	return strings.HasPrefix(NormalizeAttachmentMime(att), "image/")
}

func AttachmentLikelyText(att core.Attachment) bool {
	mimeType := NormalizeAttachmentMime(att)
	if mimeType != "" {
		if strings.HasPrefix(mimeType, "text/") {
			return true
		}
		for _, token := range []string{"json", "xml", "yaml", "csv", "javascript", "typescript", "markdown", "sql"} {
			if strings.Contains(mimeType, token) {
				return true
			}
		}
	}
	ext := strings.TrimSpace(strings.ToLower(filepath.Ext(att.Name)))
	if ext == "" {
		base := strings.TrimSpace(strings.ToLower(filepath.Base(att.Name)))
		if base == "dockerfile" || base == "makefile" {
			return true
		}
		return false
	}
	_, ok := TextAttachmentExtensions[ext]
	return ok
}

func AttachmentPromptText(att core.Attachment) (string, bool, bool) {
	if len(att.Content) == 0 || !AttachmentLikelyText(att) || !utf8.Valid(att.Content) {
		return "", false, false
	}
	text := strings.ReplaceAll(string(att.Content), "\r\n", "\n")
	text = strings.TrimSpace(text)
	if text == "" {
		return "", true, false
	}
	truncated := false
	if len(text) > MaxPromptAttachmentTextChars {
		text = strings.TrimSpace(text[:MaxPromptAttachmentTextChars])
		truncated = true
	}
	return text, true, truncated
}

func AttachmentDataURL(att core.Attachment) string {
	if len(att.Content) == 0 {
		return ""
	}
	mimeType := NormalizeAttachmentMime(att)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(att.Content))
}
