package memory

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/kocort/kocort/internal/core"
)

func EnsureSessionTranscriptExport(workspaceDir string, session core.SessionResolution) (string, error) {
	if session.Entry == nil || strings.TrimSpace(session.Entry.SessionFile) == "" || strings.TrimSpace(session.SessionID) == "" {
		return "", nil
	}
	sourcePath := strings.TrimSpace(session.Entry.SessionFile)
	file, err := os.Open(sourcePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(scanner.Bytes(), &raw); err != nil {
			continue
		}
		if rawType, ok := raw["type"]; ok {
			var typeValue string
			if err := json.Unmarshal(rawType, &typeValue); err == nil && strings.EqualFold(strings.TrimSpace(typeValue), "session") {
				continue
			}
		}
		var msg core.TranscriptMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		if line := buildTranscriptSearchLine(msg); line != "" {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	exportDir := filepath.Join(strings.TrimSpace(workspaceDir), "memory", ".sessions")
	if err := os.MkdirAll(exportDir, 0o755); err != nil {
		return "", err
	}
	exportPath := filepath.Join(exportDir, session.SessionID+".md")
	content := strings.Join(lines, "\n")
	if existing, err := os.ReadFile(exportPath); err == nil && string(existing) == content {
		return exportPath, nil
	}
	if err := os.WriteFile(exportPath, []byte(content), 0o644); err != nil {
		return "", err
	}
	return exportPath, nil
}
