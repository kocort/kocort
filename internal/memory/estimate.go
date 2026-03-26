package memory

import (
	"math"
	"strings"

	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
)

func DeriveContextTokensFromUsage(usage map[string]any) int {
	if len(usage) == 0 {
		return 0
	}
	for _, key := range []string{"total_tokens", "prompt_tokens", "input_tokens"} {
		if value := readUsageInt(usage, key); value > 0 {
			return value
		}
	}
	return 0
}

func ResolveConfiguredContextWindowTokens(cfg config.AppConfig, selection core.ModelSelection) int {
	if cfg.BrainLocalEnabled() && cfg.BrainLocal.ContextSize > 0 {
		return cfg.BrainLocal.ContextSize
	}
	modelCfg, err := config.ResolveConfiguredModel(cfg, selection.Provider, selection.Model)
	if err != nil {
		return 0
	}
	return modelCfg.ContextWindow
}

func EstimatePreRunContextTokens(
	entry *core.SessionEntry,
	history []core.TranscriptMessage,
	systemPrompt string,
	userMessage string,
) int {
	projected := 0
	if entry != nil && entry.ContextTokens > 0 {
		projected = entry.ContextTokens + estimateTextTokensApprox(userMessage)
	}
	freshEstimate := estimateTranscriptTokensApprox(history) +
		estimateTextTokensApprox(systemPrompt) +
		estimateTextTokensApprox(userMessage)
	if freshEstimate > projected {
		projected = freshEstimate
	}
	return projected
}

func readUsageInt(usage map[string]any, key string) int {
	raw, ok := usage[key]
	if !ok {
		return 0
	}
	switch value := raw.(type) {
	case int:
		return max(0, value)
	case int32:
		return max(0, int(value))
	case int64:
		return max(0, int(value))
	case float32:
		if value > 0 {
			return int(math.Round(float64(value)))
		}
	case float64:
		if value > 0 {
			return int(math.Round(value))
		}
	}
	return 0
}

func estimateTranscriptTokensApprox(history []core.TranscriptMessage) int {
	total := 0
	for _, message := range history {
		text := strings.TrimSpace(message.Text)
		if text == "" {
			text = strings.TrimSpace(message.Summary)
		}
		total += estimateTextTokensApprox(text)
	}
	return total
}

func estimateTextTokensApprox(text string) int {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0
	}
	words := len(strings.Fields(trimmed))
	charsEstimate := int(math.Ceil(float64(len(trimmed)) / 4.0))
	switch {
	case words <= 0:
		return charsEstimate
	case charsEstimate <= 0:
		return words
	default:
		return max(words, charsEstimate)
	}
}
