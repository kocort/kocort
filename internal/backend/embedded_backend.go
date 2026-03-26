package backend

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"

	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/rtypes"
)

// EmbeddedBackend wraps an OpenAI-compatible backend with session state tracking.
type EmbeddedBackend struct {
	OpenAI   *OpenAICompatBackend
	mu       sync.Mutex
	sessions map[string]embeddedSessionState
}

type embeddedSessionState struct {
	LastUsedAt         time.Time
	StopReason         string
	LastProvider       string
	LastModel          string
	PreviousResponseID string
	PendingToolCalls   []string
}

// NewEmbeddedBackend creates a new EmbeddedBackend wrapping an OpenAI-compatible backend.
func NewEmbeddedBackend(cfg config.AppConfig, env *infra.EnvironmentRuntime, dc *infra.DynamicHTTPClient) *EmbeddedBackend {
	return &EmbeddedBackend{
		OpenAI:   NewOpenAICompatBackend(cfg, env, dc),
		sessions: map[string]embeddedSessionState{},
	}
}

func (b *EmbeddedBackend) Run(ctx context.Context, runCtx rtypes.AgentRunContext) (core.AgentRunResult, error) {
	runCtx.Runtime = ensureRuntime(runCtx)
	if b == nil || b.OpenAI == nil {
		return core.AgentRunResult{}, context.Canceled
	}
	attemptCtx := runCtx
	attemptCtx.Transcript = SanitizeTranscriptForOpenAI(runCtx.Transcript)
	entry := runCtx.Session.Entry
	previousID := ""
	if entry != nil {
		previousID = strings.TrimSpace(entry.OpenAIPreviousID)
	}
	if state, ok := b.getSessionState(runCtx.Session.SessionKey); ok && previousID == "" {
		previousID = strings.TrimSpace(state.PreviousResponseID)
	}
	if previousID != "" {
		if attemptCtx.SystemPrompt != "" {
			attemptCtx.SystemPrompt += "\n"
		}
		attemptCtx.SystemPrompt += "Resume the existing embedded session state when possible."
	}
	result, err := b.OpenAI.Run(ctx, attemptCtx)
	if err != nil {
		return core.AgentRunResult{}, err
	}
	if result.Meta == nil {
		result.Meta = map[string]any{}
	}
	result.Meta["backendKind"] = "embedded"
	result.Meta["provider"] = attemptCtx.ModelSelection.Provider
	result.Meta["model"] = attemptCtx.ModelSelection.Model
	if previousID != "" {
		result.Meta["embeddedPreviousResponseIdUsed"] = previousID
	}
	if strings.TrimSpace(result.StopReason) != "" {
		result.Meta["embeddedStopReason"] = strings.TrimSpace(result.StopReason)
	}
	if responseID, _ := result.Usage["previousResponseId"].(string); strings.TrimSpace(responseID) != "" { // zero value fallback is intentional
		result.Meta["embeddedPreviousResponseId"] = strings.TrimSpace(responseID)
	}
	if pending, ok := result.Meta["pendingToolCalls"].([]string); ok && len(pending) > 0 {
		result.Meta["embeddedPendingToolCalls"] = append([]string{}, pending...)
	}
	b.setSessionState(runCtx.Session.SessionKey, embeddedSessionState{
		LastUsedAt:         time.Now().UTC(),
		StopReason:         strings.TrimSpace(result.StopReason),
		LastProvider:       attemptCtx.ModelSelection.Provider,
		LastModel:          attemptCtx.ModelSelection.Model,
		PreviousResponseID: strings.TrimSpace(asUsageString(result.Usage, "previousResponseId")),
		PendingToolCalls:   cloneStringSlice(anyStrings(result.Meta["pendingToolCalls"])),
	})
	return result, nil
}

func (b *EmbeddedBackend) getSessionState(sessionKey string) (embeddedSessionState, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state, ok := b.sessions[sessionKey]
	return state, ok
}

func (b *EmbeddedBackend) setSessionState(sessionKey string, state embeddedSessionState) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sessions[sessionKey] = state
}

func asUsageString(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	raw, _ := values[key].(string) // zero value fallback is intentional
	return strings.TrimSpace(raw)
}

func anyStrings(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string{}, typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	default:
		return nil
	}
}

func cloneStringSlice(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	return append([]string{}, items...)
}
