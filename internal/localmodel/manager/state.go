package manager

import (
	"github.com/kocort/kocort/internal/localmodel/api"
	"github.com/kocort/kocort/internal/localmodel/catalog"
)

// ── Re-exported Catalog Types (type aliases) ─────────────────────────────────

// ModelPreset describes a downloadable model preset.
type ModelPreset = catalog.Preset

// ModelPresetDefaults describes default parameters for a model preset.
type ModelPresetDefaults = catalog.PresetDefaults

// ModelPresetFile describes one downloadable file for a preset.
type ModelPresetFile = catalog.PresetFile

// LocalizedText stores Chinese and English display text.
type LocalizedText = catalog.LocalizedText

// BuiltinCerebellumCatalog re-exports the cerebellum catalog for convenience.
var BuiltinCerebellumCatalog = catalog.BuiltinCerebellumCatalog

// BuiltinBrainCatalog re-exports the brain catalog for convenience.
var BuiltinBrainCatalog = catalog.BuiltinBrainCatalog

// ── API type aliases (used throughout manager) ───────────────────────────────

type ChatCompletionRequest = api.ChatCompletionRequest
type ChatCompletionChunk = api.ChatCompletionChunk

// BoolPtr returns a pointer to the given bool value.
var BoolPtr = api.BoolPtr

// ── Status constants ─────────────────────────────────────────────────────────

const (
	StatusRunning  = "running"
	StatusStopped  = "stopped"
	StatusStarting = "starting"
	StatusStopping = "stopping"
	StatusError    = "error"
)

// ModelInfo describes an available local model file.
type ModelInfo struct {
	ID   string
	Name string
	Size string
}

// DownloadProgress tracks the state of an ongoing model download.
type DownloadProgress struct {
	PresetID        string `json:"presetId"`
	Filename        string `json:"filename"`
	TotalBytes      int64  `json:"totalBytes"`
	DownloadedBytes int64  `json:"downloadedBytes"`
	Active          bool   `json:"active"`
	Canceled        bool   `json:"canceled,omitempty"`
	Error           string `json:"error,omitempty"`
}

// Snapshot is a point-in-time copy of local model state.
type Snapshot struct {
	Status           string
	ModelID          string
	Models           []ModelInfo
	LastError        string
	Catalog          []ModelPreset
	DownloadProgress *DownloadProgress
	Sampling         SamplingParams
	Threads          int
	ContextSize      int
	GpuLayers        int
	EnableThinking   bool
}
