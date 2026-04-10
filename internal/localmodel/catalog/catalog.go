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
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description *LocalizedText  `json:"description,omitempty"`
	Size        string          `json:"size"`
	DownloadURL string          `json:"downloadUrl"`
	Filename    string          `json:"filename"`
	Files       []PresetFile    `json:"files,omitempty"`
	Defaults    *PresetDefaults `json:"defaults,omitempty"`
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

// catalogData is the raw structure parsed from catalog.json.
type catalogData struct {
	Cerebellum []Preset `json:"cerebellum"`
	Brain      []Preset `json:"brain"`
}

// BuiltinCerebellumCatalog contains recommended models for the cerebellum.
var BuiltinCerebellumCatalog []Preset

// BuiltinBrainCatalog contains recommended models for the brain local mode.
var BuiltinBrainCatalog []Preset

func init() {
	var data catalogData
	if err := json.Unmarshal(catalogJSON, &data); err != nil {
		panic(fmt.Sprintf("catalog: failed to parse catalog.json: %v", err))
	}
	BuiltinCerebellumCatalog = data.Cerebellum
	BuiltinBrainCatalog = data.Brain
}
