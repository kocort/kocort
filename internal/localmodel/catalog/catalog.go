// Package catalog provides the built-in model preset catalog for local models.
//
// It embeds catalog.json and exports pre-parsed catalog entries for the
// cerebellum (safety-review) and brain (general agent) roles.
package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed catalog.json
var catalogJSON []byte

// SamplingParams configures the sampling parameters for inference.
// A nil value means "use defaults".
type SamplingParams struct {
	Temp           float32 `json:"temp"`
	TopP           float32 `json:"topP"`
	TopK           int     `json:"topK"`
	MinP           float32 `json:"minP"`
	TypicalP       float32 `json:"typicalP"`
	RepeatLastN    int     `json:"repeatLastN"`
	PenaltyRepeat  float32 `json:"penaltyRepeat"`
	PenaltyFreq    float32 `json:"penaltyFreq"`
	PenaltyPresent float32 `json:"penaltyPresent"`
}

// LocalizedText stores Chinese and English display text.
type LocalizedText struct {
	Zh string `json:"zh,omitempty"`
	En string `json:"en,omitempty"`
}

// PresetFile describes one downloadable file for a preset.
type PresetFile struct {
	DownloadURL string `json:"downloadUrl"`
	Filename    string `json:"filename"`
}

// PresetDefaults describes default parameters for a model preset.
type PresetDefaults struct {
	Threads        int             `json:"threads,omitempty"`
	ContextSize    int             `json:"contextSize,omitempty"`
	GpuLayers      int             `json:"gpuLayers,omitempty"`
	Sampling       *SamplingParams `json:"sampling,omitempty"`
	EnableThinking *bool           `json:"enableThinking,omitempty"`
}

// Preset describes a downloadable model preset.
type Preset struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Description  *LocalizedText  `json:"description,omitempty"`
	Size         string          `json:"size"`
	DownloadURL  string          `json:"downloadUrl"`
	Filename     string          `json:"filename"`
	Files        []PresetFile    `json:"files,omitempty"`
	Defaults     *PresetDefaults `json:"defaults,omitempty"`
	Capabilities *Capabilities   `json:"capabilities,omitempty"`
}

// DownloadFiles returns the list of files to download for this preset.
func (p Preset) DownloadFiles() []PresetFile {
	if len(p.Files) > 0 {
		out := make([]PresetFile, len(p.Files))
		copy(out, p.Files)
		return out
	}
	if strings.TrimSpace(p.DownloadURL) == "" || strings.TrimSpace(p.Filename) == "" {
		return nil
	}
	return []PresetFile{{
		DownloadURL: strings.TrimSpace(p.DownloadURL),
		Filename:    strings.TrimSpace(p.Filename),
	}}
}

// PrimaryFilename returns the filename of the primary (first) shard.
func (p Preset) PrimaryFilename() string {
	files := p.DownloadFiles()
	if len(files) == 0 {
		return strings.TrimSpace(p.Filename)
	}
	return strings.TrimSpace(files[0].Filename)
}

// presetEntry is a catalog entry with an optional role tag.
type presetEntry struct {
	Preset
	Role string `json:"role"` // "cerebellum", "brain", or "both"
}

// catalogData is the raw structure parsed from catalog.json.
type catalogData struct {
	Models []presetEntry `json:"models"`
}

// PresetWithRole pairs a Preset with its catalog role tag.
type PresetWithRole struct {
	Preset
	Role string // "cerebellum", "brain", or "both"
}

// BuiltinCatalog contains all models with their role tags.
var BuiltinCatalog []PresetWithRole

// BuiltinCatalogPresets returns the flat list of presets (without roles)
// suitable for passing to manager.NewManager.
func BuiltinCatalogPresets() []Preset {
	out := make([]Preset, len(BuiltinCatalog))
	for i, e := range BuiltinCatalog {
		out[i] = e.Preset
	}
	return out
}

func init() {
	var data catalogData
	if err := json.Unmarshal(catalogJSON, &data); err != nil {
		panic(fmt.Sprintf("catalog: failed to parse catalog.json: %v", err))
	}
	for _, entry := range data.Models {
		role := strings.TrimSpace(strings.ToLower(entry.Role))
		if role == "" {
			role = "both"
		}
		BuiltinCatalog = append(BuiltinCatalog, PresetWithRole{
			Preset: entry.Preset,
			Role:   role,
		})
	}
}
