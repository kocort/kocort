package presets

// Model presets for brain configuration.

import (
	_ "embed"
	"encoding/json"
	"strings"

	"github.com/kocort/kocort/api/types"
	"github.com/kocort/kocort/internal/config"
)

//go:embed models.json
var modelsJSON []byte

// OAuthPresetConfig holds OAuth configuration for device-code presets.
type OAuthPresetConfig struct {
	DeviceCodeURL string `json:"deviceCodeUrl"`
	TokenURL      string `json:"tokenUrl"`
	ClientID      string `json:"clientId"`
	Scope         string `json:"scope"`
}

// ModelPreset represents a provider preset configuration with multiple models.
type ModelPreset struct {
	ID          string                       `json:"id"`
	Label       string                       `json:"label"`
	LabelZh     string                       `json:"labelZh,omitempty"`
	Free        bool                         `json:"free,omitempty"`
	BaseURL     string                       `json:"baseUrl"`
	API         string                       `json:"api"`
	Models      []config.ProviderModelConfig `json:"models"`
	AuthKind    string                       `json:"authKind,omitempty"`
	OAuthConfig *OAuthPresetConfig           `json:"oauthConfig,omitempty"`
}

// Embedded is the loaded preset list.
var Embedded = mustLoad()

func mustLoad() []ModelPreset {
	var presets []ModelPreset
	if err := json.Unmarshal(modelsJSON, &presets); err != nil {
		panic(err)
	}
	for i := range presets {
		presets[i].ID = strings.TrimSpace(presets[i].ID)
		presets[i].Label = strings.TrimSpace(presets[i].Label)
		presets[i].BaseURL = strings.TrimSpace(presets[i].BaseURL)
		presets[i].API = strings.TrimSpace(presets[i].API)
		for j := range presets[i].Models {
			presets[i].Models[j].ID = strings.TrimSpace(presets[i].Models[j].ID)
			presets[i].Models[j].Name = strings.TrimSpace(presets[i].Models[j].Name)
		}
	}
	return presets
}

// Find returns a provider preset by provider ID.
func Find(providerID string) (ModelPreset, bool) {
	providerID = strings.TrimSpace(providerID)
	for _, p := range Embedded {
		if p.ID == providerID {
			return p, true
		}
	}
	return ModelPreset{}, false
}

// FindModel returns a provider preset and a specific model by provider ID and model ID.
func FindModel(providerID, modelID string) (ModelPreset, config.ProviderModelConfig, bool) {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	p, ok := Find(providerID)
	if !ok {
		return ModelPreset{}, config.ProviderModelConfig{}, false
	}
	for _, m := range p.Models {
		if m.ID == modelID {
			return p, m, true
		}
	}
	return ModelPreset{}, config.ProviderModelConfig{}, false
}

// AsTypes converts presets to types.BrainModelPreset slice.
func AsTypes() []types.BrainModelPreset {
	out := make([]types.BrainModelPreset, len(Embedded))
	for i, p := range Embedded {
		models := make([]types.BrainPresetModel, len(p.Models))
		for j, m := range p.Models {
			models[j] = types.BrainPresetModel{
				ID:            m.ID,
				Name:          m.Name,
				Reasoning:     m.Reasoning,
				ContextWindow: m.ContextWindow,
				MaxTokens:     m.MaxTokens,
			}
		}
		preset := types.BrainModelPreset{
			ID:       p.ID,
			Label:    p.Label,
			LabelZh:  p.LabelZh,
			Free:     p.Free,
			BaseURL:  p.BaseURL,
			API:      p.API,
			Models:   models,
			AuthKind: p.AuthKind,
		}
		if p.OAuthConfig != nil {
			preset.OAuthConfig = &types.BrainPresetOAuthConfig{
				DeviceCodeURL: p.OAuthConfig.DeviceCodeURL,
				TokenURL:      p.OAuthConfig.TokenURL,
				ClientID:      p.OAuthConfig.ClientID,
				Scope:         p.OAuthConfig.Scope,
			}
		}
		out[i] = preset
	}
	return out
}
