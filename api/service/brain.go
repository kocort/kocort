package service

// Brain service: model management and configuration logic.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/kocort/kocort/api/presets"
	"github.com/kocort/kocort/api/types"
	"github.com/kocort/kocort/internal/backend"
	"github.com/kocort/kocort/internal/config"
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"
	"github.com/kocort/kocort/runtime"
)

// BuildBrainModelRecords builds model records for brain state.
func BuildBrainModelRecords(ctx context.Context, rt *runtime.Runtime) []types.BrainModelRecord {
	if rt == nil {
		return nil
	}
	healthByProvider := map[string]core.ProviderHealthSummary{}
	for _, item := range SummarizeProviders(ctx, rt) {
		healthByProvider[item.Provider] = probeBrainProviderHealth(ctx, rt, item)
	}
	primary, fallbacks := resolveDefaultAgentModelRefs(rt.Config)
	fallbackSet := map[string]struct{}{}
	for _, ref := range fallbacks {
		fallbackSet[strings.TrimSpace(ref)] = struct{}{}
	}
	out := make([]types.BrainModelRecord, 0)
	for providerID, provider := range rt.Config.Models.Providers {
		health := healthByProvider[providerID]
		for _, model := range provider.Models {
			ref := backend.ModelKey(providerID, model.ID)
			out = append(out, types.BrainModelRecord{
				Key:           providerID + "::" + model.ID,
				ProviderID:    providerID,
				ModelID:       model.ID,
				DisplayName:   strings.TrimSpace(model.Name),
				BaseURL:       strings.TrimSpace(provider.BaseURL),
				API:           strings.TrimSpace(provider.API),
				APIKey:        strings.TrimSpace(provider.APIKey),
				Reasoning:     model.Reasoning,
				ContextWindow: model.ContextWindow,
				MaxTokens:     model.MaxTokens,
				IsDefault:     ref == primary,
				IsFallback:    hasStringKey(fallbackSet, ref),
				Ready:         health.Ready,
				LastError:     strings.TrimSpace(health.LastError),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDefault != out[j].IsDefault {
			return out[i].IsDefault
		}
		if out[i].ProviderID != out[j].ProviderID {
			return out[i].ProviderID < out[j].ProviderID
		}
		return out[i].ModelID < out[j].ModelID
	})
	return out
}

func probeBrainProviderHealth(ctx context.Context, rt *runtime.Runtime, summary core.ProviderHealthSummary) core.ProviderHealthSummary {
	if rt == nil {
		return summary
	}
	providerCfg, _, err := runtime.ResolveConfiguredProviderWithEnvironment(rt.Config, rt.Environment, summary.Provider)
	if err != nil {
		summary.Ready = false
		summary.LastError = err.Error()
		return summary
	}
	if providerCfg.Command != nil && strings.TrimSpace(providerCfg.Command.Command) != "" {
		return summary
	}
	if strings.TrimSpace(providerCfg.BaseURL) == "" {
		summary.Ready = false
		if strings.TrimSpace(summary.LastError) == "" {
			summary.LastError = "provider baseUrl is required"
		}
		return summary
	}
	if err := probeBrainProvider(ctx, rt, providerCfg); err != nil {
		summary.Ready = false
		summary.LastError = err.Error()
		return summary
	}
	summary.Ready = true
	summary.LastError = ""
	return summary
}

func probeBrainProvider(ctx context.Context, rt *runtime.Runtime, providerCfg config.ProviderConfig) error {
	client := http.DefaultClient
	if rt != nil && rt.HTTPClient != nil {
		client = rt.HTTPClient.ClientWithTimeout(2 * time.Second)
	}
	probeURL, headers, err := brainProviderProbeRequest(providerCfg)
	if err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, probeURL, nil)
	if err != nil {
		return err
	}
	for key, value := range headers {
		if strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connection check failed: %w", err)
	}
	defer resp.Body.Close()
	return nil
}

func brainProviderProbeRequest(providerCfg config.ProviderConfig) (string, map[string]string, error) {
	api := strings.TrimSpace(strings.ToLower(providerCfg.API))
	headers := map[string]string{
		"accept": "application/json",
	}
	switch api {
	case "", "openai-completions":
		baseURL, err := backend.ResolveOpenAICompatBaseURL(providerCfg.BaseURL)
		if err != nil {
			return "", nil, err
		}
		if key := strings.TrimSpace(providerCfg.APIKey); key != "" {
			headers["authorization"] = "Bearer " + key
		}
		return joinBrainProbeURL(baseURL, "/models"), headers, nil
	case "anthropic-messages":
		baseURL, err := backend.ResolveAnthropicCompatBaseURL(providerCfg.BaseURL)
		if err != nil {
			return "", nil, err
		}
		if key := strings.TrimSpace(providerCfg.APIKey); key != "" {
			headers["x-api-key"] = key
		}
		headers["anthropic-version"] = "2023-06-01"
		return joinBrainProbeURL(baseURL, "/v1/models"), headers, nil
	default:
		baseURL := strings.TrimSpace(providerCfg.BaseURL)
		if baseURL == "" {
			return "", nil, fmt.Errorf("provider baseUrl is required")
		}
		if key := strings.TrimSpace(providerCfg.APIKey); key != "" {
			headers["authorization"] = "Bearer " + key
		}
		return baseURL, headers, nil
	}
}

func joinBrainProbeURL(baseURL string, suffix string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return strings.TrimRight(strings.TrimSpace(baseURL), "/") + suffix
	}
	rel, relErr := url.Parse(suffix)
	if relErr != nil {
		return strings.TrimRight(strings.TrimSpace(baseURL), "/") + suffix
	}
	return parsed.ResolveReference(rel).String()
}

// UpsertBrainModelRecord upserts a model record in config.
func UpsertBrainModelRecord(cfg *config.AppConfig, req types.BrainModelUpsertRequest) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	req.ProviderID = strings.TrimSpace(req.ProviderID)
	req.ModelID = strings.TrimSpace(req.ModelID)
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	req.API = strings.TrimSpace(req.API)
	req.APIKey = strings.TrimSpace(req.APIKey)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.ExistingProviderID = strings.TrimSpace(req.ExistingProviderID)
	req.ExistingModelID = strings.TrimSpace(req.ExistingModelID)

	if presetID := strings.TrimSpace(req.PresetID); presetID != "" {
		preset, ok := presets.Find(presetID)
		if !ok {
			return fmt.Errorf("unknown model preset %q", presetID)
		}
		if req.ProviderID == "" {
			req.ProviderID = preset.ID
		}
		if req.BaseURL == "" {
			req.BaseURL = preset.BaseURL
		}
		if req.API == "" {
			req.API = preset.API
		}
		// If modelId is also specified, look up model-specific defaults.
		if req.ModelID != "" {
			for _, m := range preset.Models {
				if m.ID == req.ModelID {
					if req.DisplayName == "" {
						req.DisplayName = m.Name
					}
					if req.Reasoning == nil {
						value := m.Reasoning
						req.Reasoning = &value
					}
					if req.ContextWindow <= 0 {
						req.ContextWindow = m.ContextWindow
					}
					if req.MaxTokens <= 0 {
						req.MaxTokens = m.MaxTokens
					}
					break
				}
			}
		}
	}

	if req.ProviderID == "" {
		return fmt.Errorf("providerId is required")
	}
	if req.ModelID == "" {
		return fmt.Errorf("modelId is required")
	}
	if req.API == "" {
		return fmt.Errorf("api is required")
	}
	if req.BaseURL == "" {
		return fmt.Errorf("baseUrl is required")
	}

	renameProvider := req.ExistingProviderID != "" && req.ExistingProviderID != req.ProviderID
	renameModel := req.ExistingModelID != "" && req.ExistingModelID != req.ModelID
	existingPrimary, existingFallbacks := resolveDefaultAgentModelRefs(*cfg)
	oldRef := backend.ModelKey(req.ExistingProviderID, req.ExistingModelID)
	newRef := backend.ModelKey(req.ProviderID, req.ModelID)

	if cfg.Models.Providers == nil {
		cfg.Models.Providers = map[string]config.ProviderConfig{}
	}
	provider := cfg.Models.Providers[req.ProviderID]
	provider.BaseURL = req.BaseURL
	provider.API = req.API
	if req.APIKey != "" {
		provider.APIKey = req.APIKey
	}
	reasoning := false
	if req.Reasoning != nil {
		reasoning = *req.Reasoning
	}
	updatedModel := config.ProviderModelConfig{
		ID:            req.ModelID,
		Name:          req.DisplayName,
		Reasoning:     reasoning,
		ContextWindow: req.ContextWindow,
		MaxTokens:     req.MaxTokens,
	}
	replaced := false
	models := make([]config.ProviderModelConfig, 0, len(provider.Models)+1)
	for _, model := range provider.Models {
		if strings.TrimSpace(model.ID) == req.ModelID {
			models = append(models, updatedModel)
			replaced = true
			continue
		}
		models = append(models, model)
	}
	if !replaced {
		models = append(models, updatedModel)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	provider.Models = models
	cfg.Models.Providers[req.ProviderID] = provider

	if req.ExistingProviderID != "" && req.ExistingModelID != "" && (renameProvider || renameModel) {
		removeModelFromProvider(cfg, req.ExistingProviderID, req.ExistingModelID)
		existingPrimary, existingFallbacks = replaceModelRef(existingPrimary, existingFallbacks, oldRef, newRef)
		setDefaultAgentModelRefs(cfg, existingPrimary, existingFallbacks)
	}
	return nil
}

// DeleteBrainModelRecord deletes a model record from config.
func DeleteBrainModelRecord(cfg *config.AppConfig, providerID string, modelID string) error {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if providerID == "" || modelID == "" {
		return fmt.Errorf("providerId and modelId are required")
	}
	if !providerHasModel(*cfg, providerID, modelID) {
		return fmt.Errorf("model %q is not configured for provider %q", modelID, providerID)
	}
	removeModelFromProvider(cfg, providerID, modelID)
	primary, fallbacks := resolveDefaultAgentModelRefs(*cfg)
	ref := backend.ModelKey(providerID, modelID)
	filteredFallbacks := make([]string, 0, len(fallbacks))
	for _, item := range fallbacks {
		if item != ref {
			filteredFallbacks = append(filteredFallbacks, item)
		}
	}
	if primary == ref {
		primary = ""
		if len(filteredFallbacks) > 0 {
			primary = filteredFallbacks[0]
			filteredFallbacks = filteredFallbacks[1:]
		}
	}
	setDefaultAgentModelRefs(cfg, primary, filteredFallbacks)
	return nil
}

// SetBrainModelDefault sets the default model.
func SetBrainModelDefault(cfg *config.AppConfig, providerID string, modelID string) error {
	if !providerHasModel(*cfg, providerID, modelID) {
		return fmt.Errorf("model %q is not configured for provider %q", modelID, providerID)
	}
	ref := backend.ModelKey(providerID, modelID)
	_, fallbacks := resolveDefaultAgentModelRefs(*cfg)
	nextFallbacks := make([]string, 0, len(fallbacks))
	for _, item := range fallbacks {
		if item != ref {
			nextFallbacks = append(nextFallbacks, item)
		}
	}
	setDefaultAgentModelRefs(cfg, ref, nextFallbacks)
	return nil
}

// SetBrainModelFallback sets fallback status for a model.
func SetBrainModelFallback(cfg *config.AppConfig, providerID string, modelID string, enabled bool) error {
	if !providerHasModel(*cfg, providerID, modelID) {
		return fmt.Errorf("model %q is not configured for provider %q", modelID, providerID)
	}
	ref := backend.ModelKey(providerID, modelID)
	primary, fallbacks := resolveDefaultAgentModelRefs(*cfg)
	if primary == ref {
		return nil
	}
	next := make([]string, 0, len(fallbacks)+1)
	seen := false
	for _, item := range fallbacks {
		if item == ref {
			seen = true
			if enabled {
				next = append(next, item)
			}
			continue
		}
		next = append(next, item)
	}
	if enabled && !seen {
		next = append(next, ref)
	}
	setDefaultAgentModelRefs(cfg, primary, next)
	return nil
}

// Helper functions

func resolveDefaultAgentModelRefs(cfg config.AppConfig) (string, []string) {
	agentID := config.ResolveDefaultConfiguredAgentID(cfg)
	if agent := resolveAgentConfig(cfg, agentID); agent != nil {
		primary := normalizeModelRefString(agent.Model.Primary)
		fallbacks := normalizeModelRefStrings(agent.Model.Fallbacks)
		if primary != "" || len(fallbacks) > 0 {
			return primary, fallbacks
		}
	}
	if cfg.Agents.Defaults != nil {
		return normalizeModelRefString(cfg.Agents.Defaults.Model.Primary), normalizeModelRefStrings(cfg.Agents.Defaults.Model.Fallbacks)
	}
	return "", nil
}

func setDefaultAgentModelRefs(cfg *config.AppConfig, primary string, fallbacks []string) {
	if cfg == nil {
		return
	}
	agentID := config.ResolveDefaultConfiguredAgentID(*cfg)
	for i := range cfg.Agents.List {
		if session.NormalizeAgentID(cfg.Agents.List[i].ID) != agentID {
			continue
		}
		cfg.Agents.List[i].Model.Primary = primary
		cfg.Agents.List[i].Model.Fallbacks = append([]string{}, fallbacks...)
		return
	}
	if cfg.Agents.Defaults == nil {
		cfg.Agents.Defaults = &config.AgentDefaultsConfig{}
	}
	cfg.Agents.Defaults.Model.Primary = primary
	cfg.Agents.Defaults.Model.Fallbacks = append([]string{}, fallbacks...)
}

func providerHasModel(cfg config.AppConfig, providerID string, modelID string) bool {
	provider, ok := cfg.Models.Providers[strings.TrimSpace(providerID)]
	if !ok {
		return false
	}
	for _, model := range provider.Models {
		if strings.TrimSpace(model.ID) == strings.TrimSpace(modelID) {
			return true
		}
	}
	return false
}

func removeModelFromProvider(cfg *config.AppConfig, providerID string, modelID string) {
	if cfg == nil {
		return
	}
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	provider, ok := cfg.Models.Providers[providerID]
	if !ok {
		return
	}
	next := make([]config.ProviderModelConfig, 0, len(provider.Models))
	for _, model := range provider.Models {
		if strings.TrimSpace(model.ID) != modelID {
			next = append(next, model)
		}
	}
	if len(next) == 0 {
		delete(cfg.Models.Providers, providerID)
		return
	}
	provider.Models = next
	cfg.Models.Providers[providerID] = provider
}

func replaceModelRef(primary string, fallbacks []string, oldRef string, newRef string) (string, []string) {
	if primary == oldRef {
		primary = newRef
	}
	out := make([]string, 0, len(fallbacks))
	seen := map[string]struct{}{}
	for _, item := range fallbacks {
		if item == oldRef {
			item = newRef
		}
		if item == "" || item == primary {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return primary, out
}

func normalizeModelRefString(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if provider, model, ok := strings.Cut(value, "/"); ok {
		return backend.ModelKey(provider, model)
	}
	return value
}

func normalizeModelRefStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, item := range values {
		normalized := normalizeModelRefString(item)
		if normalized != "" {
			out = append(out, normalized)
		}
	}
	return out
}

func hasStringKey(values map[string]struct{}, key string) bool {
	_, ok := values[key]
	return ok
}

func resolveAgentConfig(cfg config.AppConfig, agentID string) *config.AgentConfig {
	agentID = session.NormalizeAgentID(agentID)
	for i := range cfg.Agents.List {
		if session.NormalizeAgentID(cfg.Agents.List[i].ID) == agentID {
			return &cfg.Agents.List[i]
		}
	}
	return nil
}
