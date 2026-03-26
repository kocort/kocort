package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
)

// ArchiveSessionToMemory persists a compact user-facing session archive under
// <workspace>/memory/YYYY-MM-DD-<reason>-HHMMSS.md before a reset/new rollover.
func ArchiveSessionToMemory(
	workspaceDir string,
	session core.SessionResolution,
	history []core.TranscriptMessage,
	reason string,
	now time.Time,
) error {
	if strings.TrimSpace(workspaceDir) == "" || len(history) == 0 {
		return nil
	}
	content := BuildResetMemoryArchive(history, session, reason, now)
	if strings.TrimSpace(content) == "" {
		return nil
	}
	memoryDir := filepath.Join(workspaceDir, "memory")
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		return err
	}
	filename := fmt.Sprintf("%s-%s-%s.md", now.Format("2006-01-02"), SanitizeMemoryArchiveReason(reason), now.Format("150405"))
	return os.WriteFile(filepath.Join(memoryDir, filename), []byte(content), 0o644)
}

func BuildResetMemoryArchive(
	history []core.TranscriptMessage,
	session core.SessionResolution,
	reason string,
	archivedAt time.Time,
) string {
	const maxMessages = 20
	var lines []string
	lines = append(lines,
		"# Session Archive",
		"",
		fmt.Sprintf("- Archived At: %s", archivedAt.UTC().Format(time.RFC3339)),
		fmt.Sprintf("- Reason: %s", SanitizeMemoryArchiveReason(reason)),
		fmt.Sprintf("- Session Key: %s", session.SessionKey),
		fmt.Sprintf("- Session ID: %s", session.SessionID),
		"",
		"## Conversation",
		"",
	)
	userFacing := FilterUserFacingTranscript(history)
	truncated := false
	if len(userFacing) > maxMessages {
		userFacing = userFacing[len(userFacing)-maxMessages:]
		truncated = true
	}
	if truncated {
		lines = append(lines, "_Only the most recent user/assistant messages were archived._", "")
	}
	for _, msg := range userFacing {
		role := strings.TrimSpace(strings.ToLower(msg.Role))
		text := strings.TrimSpace(msg.Text)
		if role == "" || text == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("### %s", formatArchiveRole(role)), "", text, "")
	}
	if len(lines) <= 8 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func FilterUserFacingTranscript(history []core.TranscriptMessage) []core.TranscriptMessage {
	out := make([]core.TranscriptMessage, 0, len(history))
	for _, msg := range history {
		role := strings.TrimSpace(strings.ToLower(msg.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		if strings.TrimSpace(msg.Text) == "" {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func SanitizeMemoryArchiveReason(reason string) string {
	switch strings.TrimSpace(strings.ToLower(reason)) {
	case "new", "reset", "overflow", "daily":
		return strings.TrimSpace(strings.ToLower(reason))
	case "":
		return "reset"
	default:
		return "reset"
	}
}

func formatArchiveRole(role string) string {
	switch role {
	case "assistant":
		return "Assistant"
	default:
		return "User"
	}
}
