// session_state.go — backend-specific session state application.
//
// Extracted from runtime/runtime_helpers.go: this logic applies backend
// metadata (ACP session IDs, CLI session IDs, OpenAI response IDs, etc.)
// to a session entry after a model call completes.  It has no runtime
// dependency — it only operates on its arguments.
package backend

import (
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"
)

// ApplySessionState applies backend-specific metadata from an AgentRunResult
// to the given SessionEntry.  It normalises the provider ID and handles
// ACP, CLI and OpenAI state propagation.
func ApplySessionState(entry *core.SessionEntry, provider string, result core.AgentRunResult) {
	if entry == nil {
		return
	}
	provider = NormalizeProviderID(provider)
	if result.Meta != nil {
		if kind, ok := result.Meta["backendKind"].(string); ok && strings.TrimSpace(kind) != "" {
			entry.CLIType = strings.TrimSpace(kind)
		}
		if backendKind, _ := result.Meta["backendKind"].(string); backendKind == "acp" { // zero value fallback is intentional
			meta := entry.ACP
			if meta == nil {
				meta = &core.AcpSessionMeta{}
			}
			meta.Backend = provider
			if strings.TrimSpace(meta.Agent) == "" {
				meta.Agent = session.DefaultAgentID
			}
			meta.State = "idle"
			if meta.Mode == "" {
				meta.Mode = core.AcpSessionModePersistent
			}
			meta.LastActivityAt = time.Now().UnixMilli()
			if sessionID, ok := result.Meta["acpSessionId"].(string); ok && strings.TrimSpace(sessionID) != "" {
				meta.BackendSessionID = strings.TrimSpace(sessionID)
				meta.AgentSessionID = strings.TrimSpace(sessionID)
			}
			entry.ACP = meta
		}
	}
	if result.Usage != nil {
		if sessionID, ok := result.Usage["sessionId"].(string); ok && strings.TrimSpace(sessionID) != "" {
			switch entry.CLIType {
			case "cli":
				SetCLISessionID(entry, provider, sessionID)
			case "acp":
				meta := entry.ACP
				if meta == nil {
					meta = &core.AcpSessionMeta{}
				}
				meta.Backend = provider
				meta.BackendSessionID = strings.TrimSpace(sessionID)
				meta.AgentSessionID = strings.TrimSpace(sessionID)
				meta.LastActivityAt = time.Now().UnixMilli()
				entry.ACP = meta
			}
		}
		if prevID, ok := result.Usage["previousResponseId"].(string); ok && strings.TrimSpace(prevID) != "" {
			entry.OpenAIPreviousID = strings.TrimSpace(prevID)
		}
	}
}
