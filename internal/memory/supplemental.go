package memory

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
)

func recallSessionTranscriptHits(identity core.AgentIdentity, session core.SessionResolution, query MemorySearchQuery) ([]core.MemoryHit, error) {
	if !includeSessionTranscriptSource(identity) || session.Entry == nil {
		return nil, nil
	}
	path := strings.TrimSpace(session.Entry.SessionFile)
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
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
		line := buildTranscriptSearchLine(msg)
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return nil, scanner.Err()
	}
	source := "session/" + session.SessionID + ".md"
	chunks := BuildMemoryChunksForFile(source, strings.Join(lines, "\n"), identity)
	hits := SearchMemoryIndex(chunks, query.Text, query.Terms, query.MaxResults)
	for i := range hits {
		hits[i].Source = source
		hits[i].Path = source
	}
	return hits, nil
}

func includeSessionTranscriptSource(identity core.AgentIdentity) bool {
	for _, source := range identity.MemorySources {
		switch strings.ToLower(strings.TrimSpace(source)) {
		case "session", "sessions", "transcript", "transcripts":
			return true
		}
	}
	return false
}

func buildTranscriptSearchLine(msg core.TranscriptMessage) string {
	role := strings.TrimSpace(msg.Role)
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		text = strings.TrimSpace(msg.Summary)
	}
	if text == "" {
		return ""
	}
	if role == "" {
		role = strings.TrimSpace(msg.Type)
	}
	if role == "" {
		role = "message"
	}
	return role + ": " + text
}

func mergeSupplementalMemoryHits(primary, supplemental []core.MemoryHit) []core.MemoryHit {
	if len(supplemental) == 0 {
		return primary
	}
	combined := append(append([]core.MemoryHit{}, primary...), supplemental...)
	sort.SliceStable(combined, func(i, j int) bool {
		if combined[i].Score == combined[j].Score {
			if combined[i].Path == combined[j].Path {
				return combined[i].FromLine < combined[j].FromLine
			}
			return combined[i].Path < combined[j].Path
		}
		return combined[i].Score > combined[j].Score
	})
	return combined
}

func resolveQMDCollectionNames(identity core.AgentIdentity, session core.SessionResolution, cfg config.AppConfig) []string {
	managed := resolveQMDManagedCollections(identity, session, cfg)
	if len(managed) == 0 {
		return nil
	}
	names := make([]string, 0, len(managed))
	for _, entry := range managed {
		names = append(names, entry.Name)
	}
	return names
}

func runQMDSearchCommand(ctx context.Context, command, workspaceDir string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	if cleaned, err := EnsureWorkspaceDir(workspaceDir); err == nil && cleaned != "" {
		cmd.Dir = cleaned
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}
