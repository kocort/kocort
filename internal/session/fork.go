package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
)

const DefaultParentForkMaxTokens = 100_000

func ResolveParentSessionKeyFromOptions(opts SessionResolveOptions, sessionKey string) string {
	if strings.TrimSpace(sessionKey) == "" {
		return ""
	}
	if idx := strings.Index(strings.TrimSpace(sessionKey), ":thread:"); idx > 0 {
		return strings.TrimSpace(sessionKey[:idx])
	}
	if strings.TrimSpace(opts.ThreadID) == "" {
		return ""
	}
	agentID := NormalizeAgentID(opts.AgentID)
	channel := strings.TrimSpace(strings.ToLower(opts.Channel))
	if channel == "" {
		channel = "webchat"
	}
	mainKey := strings.TrimSpace(opts.MainKey)
	if mainKey == "" {
		mainKey = DefaultMainKey
	}
	dmScope := strings.TrimSpace(strings.ToLower(opts.DMScope))
	if dmScope == "" {
		dmScope = "per-peer"
	}
	return ResolveBaseSessionKey(agentID, channel, opts.To, opts.ChatType, mainKey, dmScope)
}

func (s *SessionStore) forkSessionFromParentLocked(childKey string, childSessionID string, parentKey string, now time.Time, parentForkMaxTokens int) *core.SessionEntry {
	parentKey = strings.TrimSpace(parentKey)
	parent, ok := s.entries[parentKey]
	if !ok {
		return nil
	}
	if parent.ForkedFromParent {
		entry := core.SessionEntry{
			SessionID:          childSessionID,
			UpdatedAt:          now,
			ResetReason:        "fork_skipped",
			LastActivityReason: "fork_skipped",
			ForkedFromParent:   true,
		}
		s.entries[childKey] = entry
		return &entry
	}
	if parentForkMaxTokens <= 0 {
		parentForkMaxTokens = DefaultParentForkMaxTokens
	}
	if parent.ContextTokens > 0 && parent.ContextTokens > parentForkMaxTokens {
		entry := core.SessionEntry{
			SessionID:          childSessionID,
			UpdatedAt:          now,
			ResetReason:        "fork_skipped_parent_too_large",
			LastActivityReason: fmt.Sprintf("fork_skipped_parent_tokens_%d", parent.ContextTokens),
			ForkedFromParent:   true,
		}
		s.entries[childKey] = entry
		return &entry
	}
	entry := core.SessionEntry{
		SessionID:           childSessionID,
		UpdatedAt:           now,
		ThinkingLevel:       parent.ThinkingLevel,
		VerboseLevel:        parent.VerboseLevel,
		ProviderOverride:    parent.ProviderOverride,
		ModelOverride:       parent.ModelOverride,
		AuthProfileOverride: parent.AuthProfileOverride,
		ResetReason:         "fork",
		LastActivityReason:  "fork",
		ForkedFromParent:    true,
	}
	if messages, ok := s.loadTranscriptFromEntryLocked(parent); ok && len(messages) > 0 {
		sessionFile := filepath.Join(s.baseDir, "transcripts", childSessionID+".jsonl")
		if err := os.MkdirAll(filepath.Dir(sessionFile), 0o755); err == nil {
			if err := WriteTranscriptFile(sessionFile, childSessionID, messages); err == nil {
				entry.SessionFile = sessionFile
			}
		}
	}
	s.entries[childKey] = entry
	return &entry
}

func (s *SessionStore) loadTranscriptFromEntryLocked(entry core.SessionEntry) ([]core.TranscriptMessage, bool) {
	if strings.TrimSpace(entry.SessionFile) == "" {
		return nil, false
	}
	file, err := os.Open(entry.SessionFile)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	var out []core.TranscriptMessage
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(line, &raw); err != nil {
			return nil, false
		}
		var typeField string
		if rawType, ok := raw["type"]; ok {
			_ = json.Unmarshal(rawType, &typeField)
		}
		if typeField == "session" {
			continue
		}
		var msg core.TranscriptMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, false
		}
		out = append(out, msg)
	}
	return out, true
}
