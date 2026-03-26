package backend

import (
	"strings"

	toolfn "github.com/kocort/kocort/internal/tool"
)

// ResolveSchemaProvider maps a provider name to a SchemaProvider enum for
// schema normalization. Providers not requiring special normalization return
// SchemaProviderGeneric.
func ResolveSchemaProvider(provider, modelAPI string) toolfn.SchemaProvider {
	provider = strings.ToLower(strings.TrimSpace(provider))
	modelAPI = strings.ToLower(strings.TrimSpace(modelAPI))
	switch {
	case isGoogleProvider(provider, modelAPI):
		return toolfn.SchemaProviderGemini
	case isXaiProvider(provider):
		return toolfn.SchemaProviderXAI
	case isAnthropicProvider(provider, modelAPI):
		return toolfn.SchemaProviderAnthropic
	case isOpenAIProvider(provider, modelAPI):
		return toolfn.SchemaProviderOpenAI
	default:
		return toolfn.SchemaProviderGeneric
	}
}

// TranscriptPolicy controls which history sanitization steps are applied

// transcript-policy.ts for provider-aware history cleaning.
type TranscriptPolicy struct {
	// DropThinkingBlocks removes <think>...</think> blocks from assistant
	// messages in history before sending to the provider.
	DropThinkingBlocks bool

	// SanitizeToolCallIDs rewrites tool call IDs to match provider format
	// requirements (e.g. Mistral requires alphanumeric 9-char IDs).
	SanitizeToolCallIDs bool

	// ToolCallIDMode specifies the target format: "strict" (alphanumeric)
	// or "strict9" (alphanumeric, 9 chars). Only used when SanitizeToolCallIDs is true.
	ToolCallIDMode string

	// RepairToolUseResultPairing ensures every assistant tool_call has a
	// matching tool result and vice versa. Missing results get synthetic
	// error entries; orphan results are dropped.
	RepairToolUseResultPairing bool

	// ValidateAnthropicTurns strips dangling tool_use blocks from assistant
	// messages and merges consecutive user turns.
	ValidateAnthropicTurns bool

	// ValidateGeminiTurns merges consecutive assistant turns (required by
	// Google Gemini API).
	ValidateGeminiTurns bool

	// ApplyGoogleTurnOrdering ensures strict user→assistant alternation
	// required by Google models.
	ApplyGoogleTurnOrdering bool

	// HistoryTurnLimit caps the number of user turns retained (0 = unlimited).
	HistoryTurnLimit int

	// TrimToolCallNames trims whitespace from tool call names in streaming
	// responses.
	TrimToolCallNames bool

	// RepairMalformedToolCallArgs attempts to extract valid JSON from
	// malformed tool call arguments (e.g. Kimi provider issues).
	RepairMalformedToolCallArgs bool

	// DecodeHTMLEntityToolCallArgs decodes HTML entities in tool call
	// arguments (e.g. xAI provider).
	DecodeHTMLEntityToolCallArgs bool
}

// ResolveTranscriptPolicy returns the appropriate sanitization policy for the

func ResolveTranscriptPolicy(provider, modelAPI, modelID string) TranscriptPolicy {
	provider = strings.ToLower(strings.TrimSpace(provider))
	modelAPI = strings.ToLower(strings.TrimSpace(modelAPI))
	modelID = strings.ToLower(strings.TrimSpace(modelID))

	policy := TranscriptPolicy{
		RepairToolUseResultPairing: true, // universally needed
		TrimToolCallNames:          true, // always safe
	}

	switch {
	case isAnthropicProvider(provider, modelAPI):
		policy.DropThinkingBlocks = shouldDropThinkingBlocks(provider, modelID)
		policy.SanitizeToolCallIDs = true
		policy.ToolCallIDMode = "strict"
		policy.ValidateAnthropicTurns = true

	case isGoogleProvider(provider, modelAPI):
		policy.SanitizeToolCallIDs = true
		policy.ToolCallIDMode = "strict"
		policy.ValidateGeminiTurns = true
		policy.ApplyGoogleTurnOrdering = true

	case isMistralProvider(provider, modelAPI):
		policy.SanitizeToolCallIDs = true
		policy.ToolCallIDMode = "strict9"

	case isOpenAIProvider(provider, modelAPI):
		policy.DropThinkingBlocks = shouldDropThinkingBlocks(provider, modelID)
		policy.SanitizeToolCallIDs = true
		policy.ToolCallIDMode = "strict"

	case isOllamaProvider(provider, modelAPI):
		// Ollama is lenient; minimal sanitization
		policy.DropThinkingBlocks = shouldDropThinkingBlocks(provider, modelID)

	case isKimiProvider(provider):
		policy.SanitizeToolCallIDs = true
		policy.ToolCallIDMode = "strict"
		policy.RepairMalformedToolCallArgs = true

	case isXaiProvider(provider):
		policy.SanitizeToolCallIDs = true
		policy.ToolCallIDMode = "strict"
		policy.DecodeHTMLEntityToolCallArgs = true
	}

	return policy
}

func isAnthropicProvider(provider, modelAPI string) bool {
	if modelAPI == "anthropic-messages" || modelAPI == "anthropic" {
		return true
	}
	return strings.Contains(provider, "anthropic") || strings.Contains(provider, "claude")
}

func isGoogleProvider(provider, modelAPI string) bool {
	if modelAPI == "google" || modelAPI == "gemini" || modelAPI == "google-genai" {
		return true
	}
	return strings.Contains(provider, "google") || strings.Contains(provider, "gemini")
}

func isMistralProvider(provider, modelAPI string) bool {
	if modelAPI == "mistral" {
		return true
	}
	return strings.Contains(provider, "mistral")
}

func isOpenAIProvider(provider, modelAPI string) bool {
	if modelAPI == "openai" || modelAPI == "openai-completions" || modelAPI == "openai-responses" {
		return true
	}
	return strings.Contains(provider, "openai") || strings.Contains(provider, "azure")
}

func isOllamaProvider(provider, modelAPI string) bool {
	if modelAPI == "ollama" {
		return true
	}
	return strings.Contains(provider, "ollama")
}

func isKimiProvider(provider string) bool {
	return strings.Contains(provider, "kimi") || strings.Contains(provider, "moonshot")
}

func isXaiProvider(provider string) bool {
	return strings.Contains(provider, "xai") || strings.Contains(provider, "grok")
}

// shouldDropThinkingBlocks returns true if the model is known to produce
// thinking blocks that may confuse the provider when replayed in history.
func shouldDropThinkingBlocks(provider, modelID string) bool {
	// Models with native extended thinking should have blocks dropped
	// to avoid provider rejection on replay.
	if strings.Contains(modelID, "deepseek") {
		return true
	}
	if strings.Contains(modelID, "qwq") || strings.Contains(modelID, "qwen3") {
		return true
	}
	// Anthropic Claude 3.5+ with extended thinking
	if strings.Contains(modelID, "claude") && strings.Contains(modelID, "thinking") {
		return true
	}
	return false
}
