package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kocort/kocort/internal/core"
)

type PromptContextFile struct {
	Path      string
	Title     string
	Content   string
	Truncated bool
}

func LoadPromptContextFiles(workspaceDir string, chatType core.ChatType, includeHeartbeat bool) ([]PromptContextFile, []string) {
	workspaceDir = strings.TrimSpace(workspaceDir)
	if workspaceDir == "" {
		return nil, nil
	}
	candidates := PromptContextCandidates(chatType, includeHeartbeat)
	const perFileLimit = 8 * 1024
	const totalLimit = 24 * 1024
	var (
		files    []PromptContextFile
		warnings []string
		total    int
	)
	for _, candidate := range candidates {
		content, err := loadWorkspaceTextFile(workspaceDir, candidate.Path)
		if err != nil {
			continue
		}
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		truncated := false
		rawLen := len(content)
		if rawLen > perFileLimit {
			content = strings.TrimSpace(content[:perFileLimit])
			truncated = true
			warnings = append(warnings, fmt.Sprintf("%s: %d raw -> %d injected", candidate.Path, rawLen, len(content)))
		}
		if total+len(content) > totalLimit {
			remaining := totalLimit - total
			if remaining <= 0 {
				break
			}
			if remaining < len(content) {
				content = strings.TrimSpace(content[:remaining])
				truncated = true
				warnings = append(warnings, fmt.Sprintf("%s: total budget clipped to %d chars", candidate.Path, len(content)))
			}
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		files = append(files, PromptContextFile{
			Path:      candidate.Path,
			Title:     candidate.Title,
			Content:   content,
			Truncated: truncated,
		})
		total += len(content)
	}
	return files, warnings
}

func PromptContextCandidates(chatType core.ChatType, includeHeartbeat bool) []PromptContextFile {
	candidates := []PromptContextFile{
		{Path: "AGENTS.md", Title: "Workspace AGENTS"},
		{Path: DefaultMemoryFilename, Title: "Workspace MEMORY"},
		{Path: DefaultMemoryAltFile, Title: "Workspace memory"},
		{Path: "README.md", Title: "Workspace README"},
		{Path: "CONTEXT.md", Title: "Workspace CONTEXT"},
		{Path: "SYSTEM.md", Title: "Workspace SYSTEM"},
	}
	if chatType == core.ChatTypeGroup || chatType == core.ChatTypeThread || chatType == core.ChatTypeTopic {
		candidates = []PromptContextFile{
			{Path: "AGENTS.md", Title: "Workspace AGENTS"},
			{Path: "README.md", Title: "Workspace README"},
			{Path: "CONTEXT.md", Title: "Workspace CONTEXT"},
			{Path: "SYSTEM.md", Title: "Workspace SYSTEM"},
		}
	}
	if includeHeartbeat {
		candidates = append(candidates, PromptContextFile{Path: "HEARTBEAT.md", Title: "Workspace HEARTBEAT"})
	}
	return candidates
}

func BuildMemoryToolGuidance(toolNames map[string]struct{}) []string {
	if !hasAnyTool(toolNames, "memory_search", "memory_get") {
		return nil
	}
	return []string{
		"If the user asks about prior work, decisions, dates, preferences, or todos, check memory tools before answering.",
		"- Use memory_search first.",
		"- Use memory_get only for the minimum lines you need.",
		"If the user explicitly asks you to remember something, save a durable note to MEMORY.md or memory/YYYY-MM-DD.md instead of only keeping it in chat context.",
		"- Do not claim you will remember later unless the note was actually persisted or you clearly say persistence was unavailable.",
	}
}

func BuildPromptSection(memoryHits []core.MemoryHit) string {
	return BuildPromptSectionWithCitations(memoryHits, "auto")
}

func BuildPromptSectionWithCitations(memoryHits []core.MemoryHit, citationsMode string) string {
	if len(memoryHits) == 0 {
		return ""
	}
	lines := []string{"Recalled memory:"}
	mode := strings.TrimSpace(strings.ToLower(citationsMode))
	for _, hit := range memoryHits {
		if mode == "off" {
			lines = append(lines, "- "+hit.Snippet)
			continue
		}
		rangeSuffix := ""
		if hit.FromLine > 0 && hit.ToLine >= hit.FromLine {
			rangeSuffix = fmt.Sprintf(":%d-%d", hit.FromLine, hit.ToLine)
		}
		lines = append(lines, fmt.Sprintf("- [%s%s] %s", hit.Source, rangeSuffix, hit.Snippet))
	}
	return strings.Join(lines, "\n")
}

func hasAnyTool(toolNames map[string]struct{}, names ...string) bool {
	for _, name := range names {
		if _, ok := toolNames[name]; ok {
			return true
		}
	}
	return false
}

func loadWorkspaceTextFile(workspaceDir string, filename string) (string, error) {
	if strings.TrimSpace(workspaceDir) == "" || strings.TrimSpace(filename) == "" {
		return "", nil
	}
	content, err := os.ReadFile(filepath.Join(workspaceDir, filename))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(content), nil
}
