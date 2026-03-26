package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/kocort/kocort/internal/core"
)

const (
	DefaultAgentID = "main"
	DefaultMainKey = "main"
)

func NormalizeAgentID(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return DefaultAgentID
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-':
			b.WriteRune(r)
		default:
			if b.Len() == 0 || b.String()[b.Len()-1] == '-' {
				continue
			}
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return DefaultAgentID
	}
	if len(out) > 64 {
		return out[:64]
	}
	return out
}

func BuildMainSessionKey(agentID string) string {
	return BuildMainSessionKeyWithMain(agentID, DefaultMainKey)
}

func BuildMainSessionKeyWithMain(agentID string, mainKey string) string {
	mainKey = strings.TrimSpace(strings.ToLower(mainKey))
	if mainKey == "" {
		mainKey = DefaultMainKey
	}
	return fmt.Sprintf("agent:%s:%s", NormalizeAgentID(agentID), mainKey)
}

func BuildDirectSessionKey(agentID, channel, peerID string) string {
	channel = strings.TrimSpace(strings.ToLower(channel))
	peerID = strings.TrimSpace(strings.ToLower(peerID))
	if channel == "" || peerID == "" {
		return BuildMainSessionKey(agentID)
	}
	return fmt.Sprintf("agent:%s:%s:direct:%s", NormalizeAgentID(agentID), channel, peerID)
}

func BuildGroupSessionKey(agentID, channel string, chatType core.ChatType, peerID string) string {
	channel = strings.TrimSpace(strings.ToLower(channel))
	peerID = strings.TrimSpace(strings.ToLower(peerID))
	kind := strings.TrimSpace(strings.ToLower(string(chatType)))
	if channel == "" || peerID == "" || kind == "" {
		return BuildMainSessionKey(agentID)
	}
	return fmt.Sprintf("agent:%s:%s:%s:%s", NormalizeAgentID(agentID), channel, kind, peerID)
}

func BuildThreadSessionKey(baseSessionKey, threadID string) string {
	threadID = strings.TrimSpace(strings.ToLower(threadID))
	if threadID == "" {
		return strings.TrimSpace(baseSessionKey)
	}
	return fmt.Sprintf("%s:thread:%s", strings.TrimSpace(baseSessionKey), threadID)
}

func BuildAcpSessionKey(agentID, backend, sessionRef string) string {
	backend = strings.TrimSpace(strings.ToLower(backend))
	sessionRef = strings.TrimSpace(strings.ToLower(sessionRef))
	if backend == "" {
		backend = "acp"
	}
	if sessionRef == "" {
		return fmt.Sprintf("agent:%s:acp:%s", NormalizeAgentID(agentID), backend)
	}
	return fmt.Sprintf("agent:%s:acp:%s:%s", NormalizeAgentID(agentID), backend, sessionRef)
}

func BuildSubagentSessionKey(agentID string) (string, error) {
	id, err := RandomToken(16)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("agent:%s:subagent:%s", NormalizeAgentID(agentID), id), nil
}

func ResolveAgentIDFromSessionKey(sessionKey string) string {
	parts := strings.Split(strings.TrimSpace(sessionKey), ":")
	if len(parts) >= 3 && parts[0] == "agent" {
		return NormalizeAgentID(parts[1])
	}
	return DefaultAgentID
}

func IsSubagentSessionKey(sessionKey string) bool {
	parts := strings.Split(strings.TrimSpace(strings.ToLower(sessionKey)), ":")
	return len(parts) >= 4 && parts[0] == "agent" && parts[2] == "subagent"
}

func IsAcpSessionKey(sessionKey string) bool {
	parts := strings.Split(strings.TrimSpace(strings.ToLower(sessionKey)), ":")
	return len(parts) >= 4 && parts[0] == "agent" && parts[2] == "acp"
}

// RandomToken generates a cryptographically random hex token of n bytes.
// Exported from the original unexported randomToken.
// NewRunID generates a new unique run identifier of the form "run_<token>".
func NewRunID() string {
	token, err := RandomToken(10)
	if err != nil {
		// Fallback: use a hex-encoded random 8-byte value.
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		return "run_" + hex.EncodeToString(b)
	}
	return "run_" + token
}

func RandomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
