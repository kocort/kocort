package backend

import (
	"context"
	"fmt"
	"strings"

	"github.com/kocort/kocort/internal/core"

	"github.com/kocort/kocort/utils"
)

var xHighModelRefs = map[string]struct{}{
	"openai/gpt-5.4":                   {},
	"openai/gpt-5.4-pro":               {},
	"openai/gpt-5.2":                   {},
	"openai-codex/gpt-5.4":             {},
	"openai-codex/gpt-5.3-codex":       {},
	"openai-codex/gpt-5.3-codex-spark": {},
	"openai-codex/gpt-5.2-codex":       {},
	"openai-codex/gpt-5.1-codex":       {},
	"github-copilot/gpt-5.2-codex":     {},
	"github-copilot/gpt-5.2":           {},
}

// NormalizeProviderID canonicalizes a provider name to its internal ID.
func NormalizeProviderID(provider string) string {
	normalized := strings.TrimSpace(strings.ToLower(provider))
	switch normalized {
	case "z.ai", "z-ai":
		return "zai"
	case "opencode-zen":
		return "opencode"
	case "qwen":
		return "qwen-portal"
	case "kimi-code":
		return "kimi-coding"
	case "bedrock", "aws-bedrock":
		return "amazon-bedrock"
	case "bytedance", "doubao":
		return "volcengine"
	default:
		return normalized
	}
}

// NormalizeModelRef normalizes both provider and model into a ModelCandidate.
func NormalizeModelRef(provider, model string) core.ModelCandidate {
	return core.ModelCandidate{
		Provider: NormalizeProviderID(provider),
		Model:    strings.TrimSpace(model),
	}
}

// ModelKey returns the canonical "provider/model" key.
func ModelKey(provider, model string) string {
	ref := NormalizeModelRef(provider, model)
	return ref.Provider + "/" + ref.Model
}

// ParseModelRef parses a "provider/model" string, falling back to
// defaultProvider when no slash is present.
func ParseModelRef(raw, defaultProvider string) (core.ModelCandidate, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return core.ModelCandidate{}, false
	}
	slash := strings.Index(trimmed, "/")
	if slash == -1 {
		return NormalizeModelRef(defaultProvider, trimmed), true
	}
	provider := strings.TrimSpace(trimmed[:slash])
	model := strings.TrimSpace(trimmed[slash+1:])
	if provider == "" || model == "" {
		return core.ModelCandidate{}, false
	}
	return NormalizeModelRef(provider, model), true
}

// NormalizeThinkLevel maps user-supplied thinking level strings to canonical values.
func NormalizeThinkLevel(raw string) string {
	key := strings.TrimSpace(strings.ToLower(raw))
	collapsed := strings.NewReplacer(" ", "", "_", "", "-", "").Replace(key)
	switch {
	case collapsed == "adaptive" || collapsed == "auto":
		return "adaptive"
	case collapsed == "xhigh" || collapsed == "extrahigh":
		return "xhigh"
	case key == "off":
		return "off"
	case key == "on" || key == "enable" || key == "enabled":
		return "low"
	case key == "min" || key == "minimal":
		return "minimal"
	case key == "low" || key == "thinkhard" || key == "think-hard" || key == "think_hard":
		return "low"
	case key == "mid" || key == "med" || key == "medium" || key == "thinkharder" || key == "think-harder" || key == "harder":
		return "medium"
	case key == "high" || key == "ultra" || key == "ultrathink" || key == "thinkhardest" || key == "highest" || key == "max":
		return "high"
	case key == "think":
		return "minimal"
	default:
		return ""
	}
}

// SupportsXHighThinking returns true if the provider/model pair supports
// extra-high thinking level.
func SupportsXHighThinking(provider, model string) bool {
	_, ok := xHighModelRefs[strings.ToLower(ModelKey(provider, model))]
	return ok
}

// ResolveThinkingDefault picks the default thinking level for an agent/model pair.
func ResolveThinkingDefault(identity core.AgentIdentity, provider, model string) string {
	if normalized := NormalizeThinkLevel(identity.ThinkingDefault); normalized != "" {
		return normalized
	}
	if strings.Contains(strings.ToLower(model), "claude-") {
		return "adaptive"
	}
	return "off"
}

// BuildAllowedModelSet builds the set of allowed model keys from an identity's
// allowlist plus the default model.
func BuildAllowedModelSet(identity core.AgentIdentity, defaultProvider, defaultModel string) (bool, map[string]struct{}) {
	keys := map[string]struct{}{}
	if len(identity.ModelAllowlist) == 0 {
		if strings.TrimSpace(defaultProvider) != "" && strings.TrimSpace(defaultModel) != "" {
			keys[ModelKey(defaultProvider, defaultModel)] = struct{}{}
		}
		return true, keys
	}
	for _, raw := range identity.ModelAllowlist {
		ref, ok := ParseModelRef(raw, defaultProvider)
		if !ok {
			continue
		}
		keys[ModelKey(ref.Provider, ref.Model)] = struct{}{}
	}
	if strings.TrimSpace(defaultProvider) != "" && strings.TrimSpace(defaultModel) != "" {
		keys[ModelKey(defaultProvider, defaultModel)] = struct{}{}
	}
	return false, keys
}

// ResolveModelSelection resolves which model/provider to use for a run,
// applying overrides, allowlists, thinking levels, and fallback candidates.
func ResolveModelSelection(_ context.Context, identity core.AgentIdentity, req core.AgentRunRequest, session core.SessionResolution) (core.ModelSelection, error) {
	defaultRef := NormalizeModelRef(identity.DefaultProvider, identity.DefaultModel)

	allowAny, allowedKeys := BuildAllowedModelSet(identity, defaultRef.Provider, defaultRef.Model)

	selected := defaultRef
	storedOverride := false
	overrideProvider := strings.TrimSpace(req.SessionProviderOverride)
	if overrideProvider == "" && session.Entry != nil {
		overrideProvider = strings.TrimSpace(session.Entry.ProviderOverride)
	}
	overrideModel := strings.TrimSpace(req.SessionModelOverride)
	if overrideModel == "" && session.Entry != nil {
		overrideModel = strings.TrimSpace(session.Entry.ModelOverride)
	}
	if overrideModel != "" {
		candidate := NormalizeModelRef(utils.NonEmpty(overrideProvider, defaultRef.Provider), overrideModel)
		key := ModelKey(candidate.Provider, candidate.Model)
		if allowAny {
			selected = candidate
			storedOverride = true
		} else {
			if _, ok := allowedKeys[key]; ok {
				selected = candidate
				storedOverride = true
			}
		}
	}

	thinkLevel := NormalizeThinkLevel(req.Thinking)
	if thinkLevel == "" {
		thinkLevel = ResolveThinkingDefault(identity, selected.Provider, selected.Model)
	}
	if thinkLevel == "xhigh" && !SupportsXHighThinking(selected.Provider, selected.Model) {
		if NormalizeThinkLevel(req.Thinking) == "xhigh" {
			return core.ModelSelection{}, fmt.Errorf("thinking level xhigh is unsupported for %s", ModelKey(selected.Provider, selected.Model))
		}
		thinkLevel = "high"
	}

	fallbacks := resolveFallbackCandidates(identity, selected, storedOverride)

	return core.ModelSelection{
		Provider:       selected.Provider,
		Model:          selected.Model,
		ThinkLevel:     thinkLevel,
		AllowedKeys:    allowedKeys,
		AllowAny:       allowAny,
		Fallbacks:      fallbacks,
		StoredOverride: storedOverride,
	}, nil
}

func resolveFallbackCandidates(identity core.AgentIdentity, primary core.ModelCandidate, hasSessionOverride bool) []core.ModelCandidate {
	seen := map[string]struct{}{ModelKey(primary.Provider, primary.Model): {}}
	candidates := []core.ModelCandidate{{Provider: primary.Provider, Model: primary.Model}}
	if hasSessionOverride {
		return candidates
	}
	for _, raw := range identity.ModelFallbacks {
		ref, ok := ParseModelRef(raw, primary.Provider)
		if !ok {
			continue
		}
		key := ModelKey(ref.Provider, ref.Model)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		candidates = append(candidates, ref)
	}
	return candidates
}
