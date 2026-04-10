// Package localmodel provides local GGUF model lifecycle management.
//
// This is a thin façade that re-exports types and constructors from the
// manager/ sub-package.  External consumers should only import
// "internal/localmodel"; the manager/ sub-package is an internal
// implementation detail.
//
// Brain and cerebellum each hold their own Manager instance with separate
// model directories, catalogs, and model backends.
package localmodel

import "github.com/kocort/kocort/internal/localmodel/manager"

// ── Manager & Configuration (re-exported from manager/) ─────────────────────

// Manager manages the lifecycle of a single local model instance.
type Manager = manager.Manager

// Config holds the configuration for a local model manager instance.
type Config = manager.Config

// SamplingParams configures the sampling parameters for inference.
type SamplingParams = manager.SamplingParams

// ModelBackend abstracts the inference engine used by Manager.
type ModelBackend = manager.ModelBackend

// ── Constructors ─────────────────────────────────────────────────────────────

var (
	// NewManager creates a Manager with the default backend.
	NewManager = manager.NewManager
	// NewManagerWithBackend creates a Manager with a custom backend (for testing).
	NewManagerWithBackend = manager.NewManagerWithBackend
	// DefaultSamplingParams returns the built-in default sampling parameters.
	DefaultSamplingParams = manager.DefaultSamplingParams
)

// ── Catalog & Model Types ────────────────────────────────────────────────────

// ModelPreset describes a downloadable model preset.
type ModelPreset = manager.ModelPreset

// LocalizedText stores Chinese and English display text.
type LocalizedText = manager.LocalizedText

// ModelInfo describes an available local model file.
type ModelInfo = manager.ModelInfo

// DownloadProgress tracks the state of an ongoing model download.
type DownloadProgress = manager.DownloadProgress

// Snapshot is a point-in-time copy of local model state.
type Snapshot = manager.Snapshot

// ── Status Constants ─────────────────────────────────────────────────────────

const (
	StatusRunning  = manager.StatusRunning
	StatusStopped  = manager.StatusStopped
	StatusStarting = manager.StatusStarting
	StatusStopping = manager.StatusStopping
	StatusError    = manager.StatusError
)

// ── Catalog Variables ────────────────────────────────────────────────────────

var (
	BuiltinBrainCatalog      = manager.BuiltinBrainCatalog
	BuiltinCerebellumCatalog = manager.BuiltinCerebellumCatalog
)

// ── Utility Functions ────────────────────────────────────────────────────────

var (
	HumanModelName               = manager.HumanModelName
	FormatBytes                  = manager.FormatBytes
	ContainsSensitiveKeywords    = manager.ContainsSensitiveKeywords
	ResolveEnableThinkingDefault = manager.ResolveEnableThinkingDefault
	SensitiveKeywords            = manager.SensitiveKeywords
)
