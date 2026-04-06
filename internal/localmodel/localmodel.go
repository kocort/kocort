// Package localmodel provides local GGUF model lifecycle management.
//
// This package is an independent domain that can be used by both the brain
// (大脑) and cerebellum (小脑). It provides:
//   - Model file discovery from a configured directory
//   - Model download with progress tracking and proxy support
//   - Model loading/unloading via ModelBackend (backed by llamawrapper.Runner)
//   - Lifecycle management (start/stop/restart)
//   - State snapshot for API responses
//
// Brain and cerebellum each hold their own Manager instance with separate
// model directories, catalogs, and model backends.
package localmodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/localmodel/llamawrapper"
)

// Status constants for the local model lifecycle.
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

// ModelPresetDefaults describes default parameters for a model preset.
type ModelPresetDefaults struct {
	Threads        int             `json:"threads,omitempty"`
	ContextSize    int             `json:"contextSize,omitempty"`
	GpuLayers      int             `json:"gpuLayers,omitempty"`
	Sampling       *SamplingParams `json:"sampling,omitempty"`
	EnableThinking *bool           `json:"enableThinking,omitempty"`
}

// LocalizedText stores Chinese and English display text.
type LocalizedText struct {
	Zh string `json:"zh,omitempty"`
	En string `json:"en,omitempty"`
}

// ModelPresetFile describes one downloadable file for a preset.
// Large GGUF models may be split into multiple shards.
type ModelPresetFile struct {
	DownloadURL string `json:"downloadUrl"`
	Filename    string `json:"filename"`
}

// ModelPreset describes a downloadable model preset.
type ModelPreset struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Description *LocalizedText       `json:"description,omitempty"`
	Size        string               `json:"size"`
	DownloadURL string               `json:"downloadUrl"`
	Filename    string               `json:"filename"`
	Files       []ModelPresetFile    `json:"files,omitempty"`
	Defaults    *ModelPresetDefaults `json:"defaults,omitempty"`
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

// Config holds the configuration for a local model manager instance.
type Config struct {
	ModelID        string
	ModelsDir      string
	Threads        int
	ContextSize    int
	GpuLayers      int
	Sampling       *SamplingParams
	EnableThinking bool // When true, prompt the model to use <think> blocks for reasoning.
}

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

// DefaultSamplingParams returns the built-in default sampling parameters.
// Values based on Qwen3.5 HuggingFace recommended settings for thinking mode.
func DefaultSamplingParams() SamplingParams {
	return SamplingParams{
		Temp:          0.6,
		TopP:          0.95,
		TopK:          20,
		MinP:          0.0,
		RepeatLastN:   256,
		PenaltyRepeat: 1.15,
	}
}

// progressWriter wraps an io.Writer and tracks bytes written via atomic counter.
type progressWriter struct {
	w          io.Writer
	downloaded *atomic.Int64
}

var splitGGUFPattern = regexp.MustCompile(`^(.*)-(\d{5})-of-(\d{5})$`)

func (p ModelPreset) DownloadFiles() []ModelPresetFile {
	if len(p.Files) > 0 {
		out := make([]ModelPresetFile, len(p.Files))
		copy(out, p.Files)
		return out
	}
	if strings.TrimSpace(p.DownloadURL) == "" || strings.TrimSpace(p.Filename) == "" {
		return nil
	}
	return []ModelPresetFile{{
		DownloadURL: strings.TrimSpace(p.DownloadURL),
		Filename:    strings.TrimSpace(p.Filename),
	}}
}

func (p ModelPreset) PrimaryFilename() string {
	files := p.DownloadFiles()
	if len(files) == 0 {
		return strings.TrimSpace(p.Filename)
	}
	return strings.TrimSpace(files[0].Filename)
}

func splitModelID(id string) (base string, shardIndex int, shardCount int, ok bool) {
	matches := splitGGUFPattern.FindStringSubmatch(strings.TrimSpace(id))
	if len(matches) != 4 {
		return "", 0, 0, false
	}
	var idx, total int
	if _, err := fmt.Sscanf(matches[2], "%d", &idx); err != nil {
		return "", 0, 0, false
	}
	if _, err := fmt.Sscanf(matches[3], "%d", &total); err != nil {
		return "", 0, 0, false
	}
	return matches[1], idx, total, true
}

func installedModelFiles(modelsDir, modelID string) []string {
	if strings.TrimSpace(modelsDir) == "" || strings.TrimSpace(modelID) == "" {
		return nil
	}

	directPath := filepath.Join(modelsDir, modelID+".gguf")
	files := make([]string, 0, 1)
	if _, err := os.Stat(directPath); err == nil {
		files = append(files, directPath)
	}

	pattern := filepath.Join(modelsDir, modelID+"-*.gguf")
	matches, _ := filepath.Glob(pattern)
	for _, match := range matches {
		stem := strings.TrimSuffix(filepath.Base(match), filepath.Ext(match))
		base, _, _, ok := splitModelID(stem)
		if ok && base == modelID {
			files = append(files, match)
		}
	}

	sort.Strings(files)
	return files
}

// findPresetDefaults returns the Defaults block for the preset whose ID
// matches modelID, or nil if no match is found.
func findPresetDefaults(catalog []ModelPreset, modelID string) *ModelPresetDefaults {
	for _, p := range catalog {
		if p.ID == modelID {
			return p.Defaults
		}
	}
	return nil
}

// ResolveEnableThinkingDefault determines the enableThinking setting using
// the following priority:
//  1. Explicit user configuration (*configured != nil).
//  2. Catalog preset default for the given modelID.
//  3. Fallback: true (thinking enabled by default).
func ResolveEnableThinkingDefault(configured *bool, modelID, modelsDir string, catalog []ModelPreset) bool {
	if configured != nil {
		return *configured
	}
	if defaults := findPresetDefaults(catalog, modelID); defaults != nil && defaults.EnableThinking != nil {
		return *defaults.EnableThinking
	}
	return true // default: thinking enabled
}

func resolveInstalledModelPath(modelsDir, modelID string) string {
	if strings.TrimSpace(modelsDir) == "" || strings.TrimSpace(modelID) == "" {
		return modelID
	}
	files := installedModelFiles(modelsDir, modelID)
	if len(files) == 0 {
		return filepath.Join(modelsDir, modelID+".gguf")
	}
	for _, file := range files {
		stem := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		if base, idx, _, ok := splitModelID(stem); ok && base == modelID && idx == 1 {
			return file
		}
	}
	return files[0]
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.w.Write(p)
	if n > 0 {
		pw.downloaded.Add(int64(n))
	}
	return n, err
}

// ── Actor commands ──────────────────────────────────────────────────────────

// cmd is a command sent to the Manager's actor goroutine.
type cmd interface{}

type cmdStart struct{ reply chan<- error }
type cmdStop struct{ reply chan<- error }
type cmdRestart struct{ reply chan<- error }

type cmdSelectModel struct {
	modelID string
	reply   chan<- error
}

type cmdClearModel struct{ reply chan<- error }

type cmdDeleteModel struct {
	modelID string
	reply   chan<- error
}

type cmdUpdateAllParams struct {
	sp                              *SamplingParams
	threads, contextSize, gpuLayers int
	reply                           chan<- error
}

type cmdSetSamplingParams struct {
	sp    SamplingParams
	reply chan<- error
}

type cmdUpdateRuntimeParams struct {
	threads, contextSize, gpuLayers int
	reply                           chan<- error
}

type cmdSetEnableThinking struct{ enabled bool }
type cmdSetDynamicHTTPClient struct{ dc *infra.DynamicHTTPClient }
type cmdSetCatalog struct{ catalog []ModelPreset }

type cmdDownloadModel struct {
	presetID   string
	httpClient *http.Client
	reply      chan<- error
}

type cmdCancelDownload struct{ reply chan<- error }
type cmdSnapshot struct{ reply chan<- Snapshot }
type cmdWaitReady struct{ reply chan<- string }
type cmdGetModels struct{ reply chan<- []ModelInfo }
type cmdClose struct{}

// cmdInfer is sent when a caller wants to run streaming inference.
// The actor validates the model state and dispatches to the backend.
type cmdInfer struct {
	ctx   context.Context
	req   llamawrapper.ChatCompletionRequest
	reply chan<- inferResult
}

type inferResult struct {
	ch  <-chan llamawrapper.ChatCompletionChunk
	err error
}

// Internal completion events sent by background goroutines back to the actor.
type cmdLifecycleDone struct {
	err         error
	contextSize int    // >0 after successful start/restart
	op          string // "start", "stop", "restart", "stop-for-pending"
}

type cmdStatusHint struct{ status string }

type cmdDLDone struct {
	err      error
	canceled bool
}

// pendingOp describes a compound operation that must complete after an
// async stop (e.g. delete-after-stop, clear-after-stop).
type pendingOp struct {
	kind    string // "delete" or "clear"
	modelID string
	reply   chan<- error
}

// ── Manager ─────────────────────────────────────────────────────────────────

// Manager manages the lifecycle of a single local model instance.
// Both brain and cerebellum use their own Manager.
//
// All state mutations are serialised inside a single actor goroutine that
// reads from cmdCh.  External callers interact with the Manager through
// channel-based commands that provide immediate fast-fail responses.
//
// Read-hot fields (Status, ModelID, EnableThinking) are mirrored to atomic
// values so they can be read from any goroutine without round-tripping
// through the actor.
type Manager struct {
	cmdCh chan cmd // all mutations flow through this channel

	// Atomic mirrors — written only by the actor, read by any goroutine.
	atomicStatus         atomic.Value // string
	atomicModelID        atomic.Value // string
	atomicEnableThinking atomic.Bool
	atomicContextSize    atomic.Int64
	atomicLastError      atomic.Value // string

	// Download progress counters updated by the download goroutine.
	atomicDLDownloaded atomic.Int64
	atomicDLTotal      atomic.Int64
	atomicDLFilename   atomic.Value // string

	// Immutable after construction (safe to read from any goroutine).
	backend   ModelBackend
	modelsDir string
	isStub    bool

	// ── actor-owned state (touched only inside the run goroutine) ────
	status         string
	modelID        string
	models         []ModelInfo
	lastError      string
	catalog        []ModelPreset
	sampling       SamplingParams
	threads        int
	contextSize    int
	gpuLayers      int
	enableThinking bool
	dc             *infra.DynamicHTTPClient

	// Lifecycle tracking.
	lifecycleBusy bool            // true while an async start/stop/restart goroutine runs
	waiters       []chan<- string // WaitReady() callers blocked until lifecycle idle

	// Download state.
	dlProgress DownloadProgress
	dlCancel   context.CancelFunc

	// Pending compound operation (e.g. stop-before-delete).
	pendingAfterStop *pendingOp
}

// NewManager creates a new local model Manager.
// The model backend is created automatically based on build tags:
// with llamacpp → llamawrapper.Runner-backed engine;
// without llamacpp → stub backend.
func NewManager(cfg Config, catalog []ModelPreset) *Manager {
	return NewManagerWithBackend(cfg, newDefaultBackend(), catalog)
}

// NewManagerWithBackend creates a Manager with a custom ModelBackend.
// This is primarily used for testing; production code should use NewManager.
func NewManagerWithBackend(cfg Config, backend ModelBackend, catalog []ModelPreset) *Manager {
	if backend == nil {
		backend = newDefaultBackend()
	}
	sp := DefaultSamplingParams()
	if cfg.Sampling != nil {
		sp = *cfg.Sampling
	}
	m := &Manager{
		cmdCh:          make(chan cmd, 64),
		backend:        backend,
		modelsDir:      strings.TrimSpace(cfg.ModelsDir),
		status:         StatusStopped,
		modelID:        strings.TrimSpace(cfg.ModelID),
		catalog:        catalog,
		sampling:       sp,
		threads:        cfg.Threads,
		contextSize:    cfg.ContextSize,
		gpuLayers:      cfg.GpuLayers,
		enableThinking: cfg.EnableThinking,
	}
	m.isStub = backend.IsStub()
	m.atomicStatus.Store(StatusStopped)
	m.atomicModelID.Store(m.modelID)
	m.atomicEnableThinking.Store(cfg.EnableThinking)
	m.atomicContextSize.Store(int64(cfg.ContextSize))
	m.atomicLastError.Store("")
	m.atomicDLFilename.Store("")
	backend.SetSamplingParams(sp)
	m.models = scanModels(m.modelsDir)
	go m.run()
	return m
}

// ── Actor loop ──────────────────────────────────────────────────────────────

func (m *Manager) run() {
	for c := range m.cmdCh {
		switch v := c.(type) {
		case *cmdStart:
			m.handleStart(v)
		case *cmdStop:
			m.handleStop(v)
		case *cmdRestart:
			m.handleRestart(v)
		case *cmdSelectModel:
			m.handleSelectModel(v)
		case *cmdClearModel:
			m.handleClearModel(v)
		case *cmdDeleteModel:
			m.handleDeleteModel(v)
		case *cmdUpdateAllParams:
			m.handleUpdateAllParams(v)
		case *cmdSetSamplingParams:
			m.handleSetSamplingParams(v)
		case *cmdUpdateRuntimeParams:
			m.handleUpdateRuntimeParams(v)
		case *cmdSetEnableThinking:
			m.enableThinking = v.enabled
			m.syncAtomics()
		case *cmdSetDynamicHTTPClient:
			m.dc = v.dc
		case *cmdSetCatalog:
			m.catalog = make([]ModelPreset, len(v.catalog))
			copy(m.catalog, v.catalog)
		case *cmdDownloadModel:
			m.handleDownloadModel(v)
		case *cmdCancelDownload:
			m.handleCancelDownload(v)
		case *cmdSnapshot:
			m.handleSnapshot(v)
		case *cmdWaitReady:
			m.handleWaitReady(v)
		case *cmdGetModels:
			m.handleGetModels(v)
		case *cmdInfer:
			m.handleInfer(v)
		case *cmdLifecycleDone:
			m.handleLifecycleDone(v)
		case *cmdStatusHint:
			m.status = v.status
			m.syncAtomics()
		case *cmdDLDone:
			m.handleDLDone(v)
		case *cmdClose:
			return
		}
	}
}

// syncAtomics mirrors actor-owned state into atomic fields for lock-free reads.
func (m *Manager) syncAtomics() {
	m.atomicStatus.Store(m.status)
	m.atomicModelID.Store(m.modelID)
	m.atomicEnableThinking.Store(m.enableThinking)
	m.atomicContextSize.Store(int64(m.contextSize))
	m.atomicLastError.Store(m.lastError)
}

// notifyWaiters sends the current status to all WaitReady callers when no
// lifecycle operation is in progress.
func (m *Manager) notifyWaiters() {
	if !m.lifecycleBusy {
		for _, w := range m.waiters {
			w <- m.status
		}
		m.waiters = m.waiters[:0]
	}
}

// Close shuts down the actor goroutine. The Manager must not be used after Close.
func (m *Manager) Close() {
	m.cmdCh <- &cmdClose{}
}

// ── Public API (lock-free atomic reads) ─────────────────────────────────────

// Status returns the current lifecycle status (lock-free atomic read).
func (m *Manager) Status() string { return m.atomicStatus.Load().(string) }

// ModelID returns the currently selected model ID (lock-free atomic read).
func (m *Manager) ModelID() string { return m.atomicModelID.Load().(string) }

// EnableThinking returns whether thinking mode is enabled (lock-free atomic read).
func (m *Manager) EnableThinking() bool { return m.atomicEnableThinking.Load() }

// ContextSize returns the current context window size (lock-free atomic read).
func (m *Manager) ContextSize() int { return int(m.atomicContextSize.Load()) }

// LastError returns the last error message, if any (lock-free atomic read).
func (m *Manager) LastError() string { return m.atomicLastError.Load().(string) }

// IsStub returns true when the manager is using a stub backend
// (binary built without -tags llamacpp). Immutable after construction.
func (m *Manager) IsStub() bool { return m.isStub }

// IsStubInferencer is an alias for IsStub kept for backward compatibility.
// Deprecated: Use IsStub instead.
func (m *Manager) IsStubInferencer() bool { return m.isStub }

// HasVision returns true when the loaded model has a vision projector and can
// process image inputs.  Safe to call from any goroutine.
func (m *Manager) HasVision() bool { return m.backend.HasVision() }

// ── Public API (channel-based) ──────────────────────────────────────────────

// SetEnableThinking updates the thinking mode setting asynchronously.
func (m *Manager) SetEnableThinking(enabled bool) {
	m.cmdCh <- &cmdSetEnableThinking{enabled: enabled}
}

// SetDynamicHTTPClient sets the dynamic HTTP client used for model downloads.
func (m *Manager) SetDynamicHTTPClient(dc *infra.DynamicHTTPClient) {
	m.cmdCh <- &cmdSetDynamicHTTPClient{dc: dc}
}

// ThinkingSetter is an optional interface implemented by inferencers
// that support dynamic thinking mode toggling.
type ThinkingSetter interface {
	SetEnableThinking(enabled bool)
}

// SamplingParamsSetter is an optional interface implemented by inferencers
// that support dynamic sampling parameter updates without restart.
type SamplingParamsSetter interface {
	SetSamplingParams(sp SamplingParams)
}

// GetSamplingParams returns the current sampling parameters.
func (m *Manager) GetSamplingParams() SamplingParams {
	return m.Snapshot().Sampling
}

// Threads returns the current inference thread count.
func (m *Manager) Threads() int { return m.Snapshot().Threads }

// GpuLayers returns the current GPU layers setting.
func (m *Manager) GpuLayers() int { return m.Snapshot().GpuLayers }

// Models returns the list of discovered models (triggers disk scan).
func (m *Manager) Models() []ModelInfo {
	reply := make(chan []ModelInfo, 1)
	m.cmdCh <- &cmdGetModels{reply: reply}
	return <-reply
}

// Catalog returns the model catalog.
func (m *Manager) Catalog() []ModelPreset {
	return m.Snapshot().Catalog
}

// SetCatalog replaces the model catalog. Intended for testing.
func (m *Manager) SetCatalog(catalog []ModelPreset) {
	cp := make([]ModelPreset, len(catalog))
	copy(cp, catalog)
	m.cmdCh <- &cmdSetCatalog{catalog: cp}
}

// Start begins running the local model asynchronously.
// Returns nil immediately if accepted; returns an error for fast-fail
// (e.g. model already starting, a lifecycle op in progress).
func (m *Manager) Start() error {
	reply := make(chan error, 1)
	m.cmdCh <- &cmdStart{reply: reply}
	return <-reply
}

// Stop shuts down the running model asynchronously.
func (m *Manager) Stop() error {
	reply := make(chan error, 1)
	m.cmdCh <- &cmdStop{reply: reply}
	return <-reply
}

// Restart stops and then starts the model asynchronously.
func (m *Manager) Restart() error {
	reply := make(chan error, 1)
	m.cmdCh <- &cmdRestart{reply: reply}
	return <-reply
}

// WaitReady blocks until any pending lifecycle operation finishes.
// Intended for testing. Returns the final status.
func (m *Manager) WaitReady() string {
	reply := make(chan string, 1)
	m.cmdCh <- &cmdWaitReady{reply: reply}
	return <-reply
}

// SelectModel sets the active model ID. If running, restarts with the new model.
func (m *Manager) SelectModel(modelID string) error {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return fmt.Errorf("model ID is required")
	}
	reply := make(chan error, 1)
	m.cmdCh <- &cmdSelectModel{modelID: modelID, reply: reply}
	return <-reply
}

// ClearSelectedModel clears the current default/selected model.
// If the selected model is currently running, it is stopped first.
func (m *Manager) ClearSelectedModel() error {
	reply := make(chan error, 1)
	m.cmdCh <- &cmdClearModel{reply: reply}
	return <-reply
}

// DeleteModel removes a downloaded model file from disk.
// If the model is selected, the selection is cleared first; if it is running,
// the model is stopped before deletion.
func (m *Manager) DeleteModel(modelID string) error {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return fmt.Errorf("model ID is required")
	}
	reply := make(chan error, 1)
	m.cmdCh <- &cmdDeleteModel{modelID: modelID, reply: reply}
	return <-reply
}

// UpdateAllParams updates sampling, threads, contextSize, and gpuLayers in one
// atomic operation, triggering at most one restart (avoids the double-restart
// that would happen if SetSamplingParams and UpdateRuntimeParams were called
// separately).
func (m *Manager) UpdateAllParams(sp *SamplingParams, threads, contextSize, gpuLayers int) error {
	reply := make(chan error, 1)
	m.cmdCh <- &cmdUpdateAllParams{
		sp:          sp,
		threads:     threads,
		contextSize: contextSize,
		gpuLayers:   gpuLayers,
		reply:       reply,
	}
	return <-reply
}

// UpdateRuntimeParams updates threads, contextSize, and gpuLayers.
// If the model is running, it will be restarted to apply the new parameters.
func (m *Manager) UpdateRuntimeParams(threads, contextSize, gpuLayers int) error {
	reply := make(chan error, 1)
	m.cmdCh <- &cmdUpdateRuntimeParams{
		threads:     threads,
		contextSize: contextSize,
		gpuLayers:   gpuLayers,
		reply:       reply,
	}
	return <-reply
}

// SetSamplingParams updates the sampling parameters.
// The change is applied to the backend immediately. If the model is running
// and the backend requires a restart, one is triggered.
func (m *Manager) SetSamplingParams(sp SamplingParams) error {
	reply := make(chan error, 1)
	m.cmdCh <- &cmdSetSamplingParams{sp: sp, reply: reply}
	return <-reply
}

// Snapshot returns the current state for API responses.
func (m *Manager) Snapshot() Snapshot {
	reply := make(chan Snapshot, 1)
	m.cmdCh <- &cmdSnapshot{reply: reply}
	return <-reply
}

// CreateChatCompletionStream creates a streaming chat completion.
// The request is routed through the actor goroutine which validates the
// model state and dispatches to the backend.  The actor is only occupied
// for the validation + dispatch (non-blocking); the actual streaming
// happens on the backend's own goroutine.
func (m *Manager) CreateChatCompletionStream(ctx context.Context, req llamawrapper.ChatCompletionRequest) (<-chan llamawrapper.ChatCompletionChunk, error) {
	reply := make(chan inferResult, 1)
	m.cmdCh <- &cmdInfer{ctx: ctx, req: req, reply: reply}
	res := <-reply
	return res.ch, res.err
}

// DownloadModel downloads a model from the catalog by preset ID.
func (m *Manager) DownloadModel(presetID string, httpClient *http.Client) error {
	reply := make(chan error, 1)
	m.cmdCh <- &cmdDownloadModel{presetID: presetID, httpClient: httpClient, reply: reply}
	return <-reply
}

// CancelDownload cancels the active model download.
func (m *Manager) CancelDownload() error {
	reply := make(chan error, 1)
	m.cmdCh <- &cmdCancelDownload{reply: reply}
	return <-reply
}

// ── Command handlers (lifecycle) ────────────────────────────────────────────

func (m *Manager) handleStart(cmd *cmdStart) {
	if m.status == StatusRunning || m.status == StatusStarting {
		cmd.reply <- nil
		return
	}
	if m.lifecycleBusy {
		cmd.reply <- fmt.Errorf("lifecycle operation in progress (status: %s)", m.status)
		return
	}
	if m.isStub {
		m.status = StatusError
		m.lastError = "local inference not available: binary built without llama.cpp support (use -tags llamacpp)"
		m.syncAtomics()
		cmd.reply <- fmt.Errorf("local inference not available: binary built without llama.cpp support (use -tags llamacpp)")
		return
	}
	if m.modelID == "" {
		m.status = StatusError
		m.lastError = "no model selected and no models available"
		m.syncAtomics()
		cmd.reply <- fmt.Errorf("no model selected")
		return
	}

	m.status = StatusStarting
	m.lastError = ""
	m.lifecycleBusy = true
	m.syncAtomics()
	cmd.reply <- nil // fast response: accepted

	modelPath := m.resolveModelPath()
	mmprojPath := m.resolveMmprojPath()
	threads := m.threads
	contextSize := m.contextSize
	gpuLayers := m.gpuLayers
	sampling := m.sampling
	enableThinking := m.enableThinking
	backend := m.backend
	ch := m.cmdCh

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("[localmodel] Start panicked — recovered", "panic", r)
				ch <- &cmdLifecycleDone{err: fmt.Errorf("start panicked: %v", r), op: "start"}
			}
		}()
		err := backend.Start(modelPath, mmprojPath, threads, contextSize, gpuLayers, sampling, enableThinking)
		cs := 0
		if err == nil {
			cs = backend.ContextSize()
		}
		ch <- &cmdLifecycleDone{err: err, contextSize: cs, op: "start"}
	}()
}

func (m *Manager) handleStop(cmd *cmdStop) {
	if m.status == StatusStopped || m.status == StatusStopping {
		cmd.reply <- nil
		return
	}
	if m.lifecycleBusy {
		cmd.reply <- fmt.Errorf("lifecycle operation in progress (status: %s)", m.status)
		return
	}

	m.status = StatusStopping
	m.lastError = ""
	m.lifecycleBusy = true
	m.syncAtomics()
	cmd.reply <- nil

	backend := m.backend
	ch := m.cmdCh

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("[localmodel] Stop panicked — recovered", "panic", r)
				ch <- &cmdLifecycleDone{err: fmt.Errorf("stop panicked: %v", r), op: "stop"}
			}
		}()
		err := backend.Stop()
		ch <- &cmdLifecycleDone{err: err, op: "stop"}
	}()
}

func (m *Manager) handleRestart(cmd *cmdRestart) {
	if m.lifecycleBusy {
		cmd.reply <- fmt.Errorf("lifecycle operation in progress (status: %s)", m.status)
		return
	}
	if m.status == StatusStopped || m.status == StatusError {
		// Not running — just start.
		m.handleStart(&cmdStart{reply: cmd.reply})
		return
	}

	m.status = StatusStopping
	m.lastError = ""
	m.lifecycleBusy = true
	m.syncAtomics()
	cmd.reply <- nil

	modelPath := m.resolveModelPath()
	mmprojPath := m.resolveMmprojPath()
	threads := m.threads
	contextSize := m.contextSize
	gpuLayers := m.gpuLayers
	sampling := m.sampling
	enableThinking := m.enableThinking
	backend := m.backend
	ch := m.cmdCh

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("[localmodel] Restart panicked — recovered", "panic", r)
				ch <- &cmdLifecycleDone{err: fmt.Errorf("restart panicked: %v", r), op: "restart"}
			}
		}()
		// Phase 1: stop
		if err := backend.Stop(); err != nil {
			ch <- &cmdLifecycleDone{err: fmt.Errorf("stop during restart failed: %v", err), op: "restart"}
			return
		}
		// Notify actor to update observable status to "starting".
		ch <- &cmdStatusHint{status: StatusStarting}
		// Phase 2: start
		if err := backend.Start(modelPath, mmprojPath, threads, contextSize, gpuLayers, sampling, enableThinking); err != nil {
			ch <- &cmdLifecycleDone{err: fmt.Errorf("start during restart failed: %v", err), op: "restart"}
			return
		}
		cs := backend.ContextSize()
		ch <- &cmdLifecycleDone{err: nil, contextSize: cs, op: "restart"}
	}()
}

func (m *Manager) handleLifecycleDone(done *cmdLifecycleDone) {
	m.lifecycleBusy = false

	switch done.op {
	case "start":
		if done.err != nil {
			m.status = StatusError
			m.lastError = fmt.Sprintf("backend start failed: %v", done.err)
			slog.Error("[localmodel] backend start failed", "error", done.err)
		} else {
			if done.contextSize > 0 {
				m.contextSize = done.contextSize
			}
			m.status = StatusRunning
			slog.Info("[localmodel] model started")
		}
	case "stop":
		if done.err != nil {
			m.status = StatusError
			m.lastError = fmt.Sprintf("backend stop failed: %v", done.err)
			slog.Error("[localmodel] backend stop failed", "error", done.err)
		} else {
			m.status = StatusStopped
			slog.Info("[localmodel] model stopped")
		}
	case "restart":
		if done.err != nil {
			m.status = StatusError
			m.lastError = done.err.Error()
			slog.Error("[localmodel] restart failed", "error", done.err)
		} else {
			if done.contextSize > 0 {
				m.contextSize = done.contextSize
			}
			m.status = StatusRunning
			slog.Info("[localmodel] model restarted")
		}
	case "stop-for-pending":
		if done.err != nil {
			m.status = StatusError
			m.lastError = fmt.Sprintf("stop failed: %v", done.err)
			slog.Error("[localmodel] stop for pending op failed", "error", done.err)
		} else {
			m.status = StatusStopped
		}
	}
	m.syncAtomics()

	// Handle pending compound operation (delete-after-stop, clear-after-stop).
	if m.pendingAfterStop != nil {
		pending := m.pendingAfterStop
		m.pendingAfterStop = nil
		if done.err != nil {
			pending.reply <- fmt.Errorf("stop failed before %s: %w", pending.kind, done.err)
		} else {
			switch pending.kind {
			case "delete":
				m.executePendingDelete(pending)
			case "clear":
				m.modelID = ""
				m.lastError = ""
				m.syncAtomics()
				pending.reply <- nil
			}
		}
	}

	m.notifyWaiters()
}

// ── Command handlers (model selection / deletion) ───────────────────────────

func (m *Manager) handleSelectModel(cmd *cmdSelectModel) {
	if m.lifecycleBusy {
		cmd.reply <- fmt.Errorf("lifecycle operation in progress (status: %s)", m.status)
		return
	}

	m.models = scanModels(m.modelsDir)
	found := false
	for _, model := range m.models {
		if model.ID == cmd.modelID {
			found = true
			break
		}
	}
	if !found {
		cmd.reply <- fmt.Errorf("model %q not found", cmd.modelID)
		return
	}

	wasRunning := m.status == StatusRunning
	m.modelID = cmd.modelID
	m.syncAtomics()

	if wasRunning {
		m.handleRestart(&cmdRestart{reply: cmd.reply})
		return
	}
	cmd.reply <- nil
}

func (m *Manager) handleClearModel(cmd *cmdClearModel) {
	if m.modelID == "" {
		cmd.reply <- nil
		return
	}
	if m.lifecycleBusy {
		cmd.reply <- fmt.Errorf("lifecycle operation in progress (status: %s)", m.status)
		return
	}

	if m.status == StatusRunning || m.status == StatusStarting {
		// Need to stop first, then clear.
		m.status = StatusStopping
		m.lastError = ""
		m.lifecycleBusy = true
		m.syncAtomics()
		m.pendingAfterStop = &pendingOp{kind: "clear", reply: cmd.reply}
		backend := m.backend
		ch := m.cmdCh
		go func() {
			defer func() {
				if r := recover(); r != nil {
					ch <- &cmdLifecycleDone{err: fmt.Errorf("stop panicked: %v", r), op: "stop-for-pending"}
				}
			}()
			err := backend.Stop()
			ch <- &cmdLifecycleDone{err: err, op: "stop-for-pending"}
		}()
		return
	}

	m.modelID = ""
	m.lastError = ""
	if m.status == StatusError {
		m.status = StatusStopped
	}
	m.syncAtomics()
	cmd.reply <- nil
}

func (m *Manager) handleDeleteModel(cmd *cmdDeleteModel) {
	if m.lifecycleBusy {
		cmd.reply <- fmt.Errorf("lifecycle operation in progress (status: %s)", m.status)
		return
	}

	m.models = scanModels(m.modelsDir)
	found := false
	isSelected := m.modelID == cmd.modelID
	for _, model := range m.models {
		if model.ID == cmd.modelID {
			found = true
			break
		}
	}
	if !found {
		cmd.reply <- fmt.Errorf("model %q not found", cmd.modelID)
		return
	}
	if m.modelsDir == "" {
		cmd.reply <- fmt.Errorf("models directory is not configured")
		return
	}

	if isSelected && (m.status == StatusRunning || m.status == StatusStarting) {
		// Need to stop first, then delete.
		m.status = StatusStopping
		m.lastError = ""
		m.lifecycleBusy = true
		m.syncAtomics()
		m.pendingAfterStop = &pendingOp{kind: "delete", modelID: cmd.modelID, reply: cmd.reply}
		backend := m.backend
		ch := m.cmdCh
		go func() {
			defer func() {
				if r := recover(); r != nil {
					ch <- &cmdLifecycleDone{err: fmt.Errorf("stop panicked: %v", r), op: "stop-for-pending"}
				}
			}()
			err := backend.Stop()
			ch <- &cmdLifecycleDone{err: err, op: "stop-for-pending"}
		}()
		return
	}

	if isSelected {
		m.modelID = ""
		m.lastError = ""
		if m.status == StatusError {
			m.status = StatusStopped
		}
		m.syncAtomics()
	}

	m.executePendingDelete(&pendingOp{kind: "delete", modelID: cmd.modelID, reply: cmd.reply})
}

func (m *Manager) executePendingDelete(pending *pendingOp) {
	if m.modelID == pending.modelID {
		m.modelID = ""
		m.syncAtomics()
	}

	modelPaths := installedModelFiles(m.modelsDir, pending.modelID)
	if len(modelPaths) == 0 {
		pending.reply <- fmt.Errorf("model file not found: %s", resolveInstalledModelPath(m.modelsDir, pending.modelID))
		return
	}
	for _, modelPath := range modelPaths {
		if err := os.Remove(modelPath); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			pending.reply <- fmt.Errorf("delete model file: %w", err)
			return
		}
	}
	m.models = scanModels(m.modelsDir)
	pending.reply <- nil
}

// ── Command handlers (params) ───────────────────────────────────────────────

func (m *Manager) handleUpdateAllParams(cmd *cmdUpdateAllParams) {
	if m.lifecycleBusy {
		cmd.reply <- fmt.Errorf("lifecycle operation in progress (status: %s), try again after it completes", m.status)
		return
	}

	needsRestart := false

	if cmd.sp != nil {
		m.sampling = *cmd.sp
		m.backend.SetSamplingParams(m.sampling)
		if m.status == StatusRunning {
			needsRestart = true
		}
	}

	if cmd.threads != m.threads || cmd.contextSize != m.contextSize || cmd.gpuLayers != m.gpuLayers {
		m.threads = cmd.threads
		m.contextSize = cmd.contextSize
		m.gpuLayers = cmd.gpuLayers
		if m.status == StatusRunning {
			needsRestart = true
		}
	}

	if needsRestart {
		m.handleRestart(&cmdRestart{reply: cmd.reply})
		return
	}
	cmd.reply <- nil
}

func (m *Manager) handleSetSamplingParams(cmd *cmdSetSamplingParams) {
	if m.lifecycleBusy {
		cmd.reply <- fmt.Errorf("lifecycle operation in progress (status: %s)", m.status)
		return
	}
	m.sampling = cmd.sp
	m.backend.SetSamplingParams(m.sampling)
	if m.status == StatusRunning {
		m.handleRestart(&cmdRestart{reply: cmd.reply})
		return
	}
	cmd.reply <- nil
}

func (m *Manager) handleUpdateRuntimeParams(cmd *cmdUpdateRuntimeParams) {
	if m.lifecycleBusy {
		cmd.reply <- fmt.Errorf("lifecycle operation in progress (status: %s)", m.status)
		return
	}
	m.threads = cmd.threads
	m.contextSize = cmd.contextSize
	m.gpuLayers = cmd.gpuLayers
	if m.status == StatusRunning {
		m.handleRestart(&cmdRestart{reply: cmd.reply})
		return
	}
	cmd.reply <- nil
}

// ── Command handlers (snapshot / wait) ──────────────────────────────────────

func (m *Manager) handleSnapshot(cmd *cmdSnapshot) {
	m.models = scanModels(m.modelsDir)

	models := make([]ModelInfo, len(m.models))
	copy(models, m.models)
	catalog := make([]ModelPreset, len(m.catalog))
	copy(catalog, m.catalog)

	var dlProg *DownloadProgress
	if m.dlProgress.PresetID != "" {
		cp := m.dlProgress
		cp.DownloadedBytes = m.atomicDLDownloaded.Load()
		if total := m.atomicDLTotal.Load(); total > 0 {
			cp.TotalBytes = total
		}
		if fn, _ := m.atomicDLFilename.Load().(string); fn != "" {
			cp.Filename = fn
		}
		dlProg = &cp
	}

	cmd.reply <- Snapshot{
		Status:           m.status,
		ModelID:          m.modelID,
		Models:           models,
		LastError:        m.lastError,
		Catalog:          catalog,
		DownloadProgress: dlProg,
		Sampling:         m.sampling,
		Threads:          m.threads,
		ContextSize:      m.contextSize,
		GpuLayers:        m.gpuLayers,
		EnableThinking:   m.enableThinking,
	}
}

func (m *Manager) handleWaitReady(cmd *cmdWaitReady) {
	if !m.lifecycleBusy {
		cmd.reply <- m.status
		return
	}
	m.waiters = append(m.waiters, cmd.reply)
}

func (m *Manager) handleGetModels(cmd *cmdGetModels) {
	m.models = scanModels(m.modelsDir)
	out := make([]ModelInfo, len(m.models))
	copy(out, m.models)
	cmd.reply <- out
}

// ── Command handlers (download) ─────────────────────────────────────────────

func (m *Manager) handleDownloadModel(cmd *cmdDownloadModel) {
	if m.dlProgress.Active {
		cmd.reply <- fmt.Errorf("a download is already in progress")
		return
	}
	if m.modelsDir == "" {
		cmd.reply <- fmt.Errorf("models directory is not configured")
		return
	}

	var preset *ModelPreset
	for i := range m.catalog {
		if m.catalog[i].ID == cmd.presetID {
			preset = &m.catalog[i]
			break
		}
	}
	if preset == nil {
		cmd.reply <- fmt.Errorf("preset %q not found in catalog", cmd.presetID)
		return
	}
	files := preset.DownloadFiles()
	if len(files) == 0 {
		cmd.reply <- fmt.Errorf("preset %q has no downloadable files", cmd.presetID)
		return
	}

	modelsDir := m.modelsDir
	for _, file := range files {
		destPath := filepath.Join(modelsDir, file.Filename)
		if _, err := os.Stat(destPath); err == nil {
			cmd.reply <- fmt.Errorf("model file already exists: %s", destPath)
			return
		}
	}

	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		cmd.reply <- fmt.Errorf("create models dir: %w", err)
		return
	}

	if cmd.httpClient != nil {
		// Synchronous download (used by tests).
		err := m.doDownload(context.Background(), preset, modelsDir, cmd.httpClient)
		m.models = scanModels(modelsDir)
		cmd.reply <- err
		return
	}

	dlCtx, cancel := context.WithCancel(context.Background())
	m.dlProgress = DownloadProgress{
		PresetID: cmd.presetID,
		Filename: preset.PrimaryFilename(),
		Active:   true,
	}
	m.dlCancel = cancel
	m.atomicDLDownloaded.Store(0)
	m.atomicDLTotal.Store(0)
	m.atomicDLFilename.Store(preset.PrimaryFilename())

	dc := m.dc
	ch := m.cmdCh
	cmd.reply <- nil // accepted

	go func() {
		var client *http.Client
		if dc != nil {
			client = dc.ClientWithTimeout(0)
		} else {
			client = &http.Client{}
		}
		err := m.doDownload(dlCtx, preset, modelsDir, client)
		canceled := errors.Is(err, context.Canceled)
		ch <- &cmdDLDone{err: err, canceled: canceled}
	}()
}

func (m *Manager) handleCancelDownload(cmd *cmdCancelDownload) {
	if !m.dlProgress.Active || m.dlCancel == nil {
		cmd.reply <- fmt.Errorf("no download is in progress")
		return
	}
	m.dlCancel()
	cmd.reply <- nil
}

func (m *Manager) handleDLDone(done *cmdDLDone) {
	if done.err != nil {
		if done.canceled {
			m.dlProgress.Canceled = true
			m.dlProgress.Error = ""
		} else {
			m.dlProgress.Error = done.err.Error()
		}
	}
	m.dlProgress.Active = false
	m.dlProgress.DownloadedBytes = m.atomicDLDownloaded.Load()
	if total := m.atomicDLTotal.Load(); total > 0 {
		m.dlProgress.TotalBytes = total
	}
	m.dlCancel = nil
	m.models = scanModels(m.modelsDir)
}

// ── Inference ───────────────────────────────────────────────────────────────

func (m *Manager) handleInfer(cmd *cmdInfer) {
	if m.status != StatusRunning {
		cmd.reply <- inferResult{err: fmt.Errorf("local model is not running (status: %s)", m.status)}
		return
	}
	enableThinking := m.enableThinking
	// backend.CreateChatCompletionStream returns immediately with a channel;
	// actual streaming happens on the backend's own goroutine, so the actor
	// is not blocked.
	ch, err := m.backend.CreateChatCompletionStream(cmd.ctx, cmd.req, enableThinking)
	cmd.reply <- inferResult{ch: ch, err: err}
}

// ── Download I/O ────────────────────────────────────────────────────────────

// doDownload performs the actual HTTP download with progress tracking.
// Called from a background goroutine; only touches atomic counters.
func (m *Manager) doDownload(ctx context.Context, preset *ModelPreset, modelsDir string, httpClient *http.Client) error {
	files := preset.DownloadFiles()
	if len(files) == 0 {
		return fmt.Errorf("preset %q has no downloadable files", preset.ID)
	}

	completedBytes := int64(0)
	downloadedPaths := make([]string, 0, len(files))
	for _, file := range files {
		destPath := filepath.Join(modelsDir, file.Filename)
		if err := m.downloadFile(ctx, file, destPath, completedBytes, httpClient); err != nil {
			for _, path := range downloadedPaths {
				_ = os.Remove(path)
			}
			return err
		}
		downloadedPaths = append(downloadedPaths, destPath)
		if info, err := os.Stat(destPath); err == nil {
			completedBytes += info.Size()
		}
		m.atomicDLDownloaded.Store(completedBytes)
		total := m.atomicDLTotal.Load()
		if total < completedBytes {
			m.atomicDLTotal.Store(completedBytes)
		}
	}

	return nil
}

func (m *Manager) downloadFile(ctx context.Context, file ModelPresetFile, destPath string, completedBytes int64, httpClient *http.Client) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, file.DownloadURL, nil)
	if err != nil {
		return fmt.Errorf("create download request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	m.atomicDLFilename.Store(file.Filename)
	m.atomicDLDownloaded.Store(completedBytes)
	if resp.ContentLength > 0 {
		m.atomicDLTotal.Store(completedBytes + resp.ContentLength)
	} else {
		total := m.atomicDLTotal.Load()
		if total < completedBytes {
			m.atomicDLTotal.Store(completedBytes)
		}
	}

	tmpPath := destPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	pw := &progressWriter{w: f, downloaded: &m.atomicDLDownloaded}
	_, copyErr := io.Copy(pw, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		if errors.Is(copyErr, context.Canceled) {
			return context.Canceled
		}
		return fmt.Errorf("download write failed: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", closeErr)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename model file: %w", err)
	}

	return nil
}

// resolveModelPath returns the filesystem path for the currently selected model.
func (m *Manager) resolveModelPath() string {
	if m.modelsDir == "" || m.modelID == "" {
		return m.modelID
	}
	return resolveInstalledModelPath(m.modelsDir, m.modelID)
}

// resolveMmprojPath auto-detects a vision projector (mmproj) file in the
// models directory that matches the current model. It looks for files
// matching the pattern "mmproj-*<model-base>*.gguf" or "*<model-base>*mmproj*.gguf".
// Returns an empty string if no mmproj file is found.
func (m *Manager) resolveMmprojPath() string {
	if m.modelsDir == "" || m.modelID == "" {
		return ""
	}
	return findMmprojFile(m.modelsDir, m.modelID)
}

// findMmprojFile searches modelsDir for a vision projector GGUF file that
// corresponds to the given model ID. It returns the full path or "".
func findMmprojFile(modelsDir, modelID string) string {
	entries, err := os.ReadDir(modelsDir)
	if err != nil {
		return ""
	}

	modelIDLower := strings.ToLower(modelID)

	// Strategy 1: Look for "mmproj-<model-base>*.gguf" pattern.
	// e.g. model "gemma-4-E2B-it-Q4_K_M" → "mmproj-gemma-4-E2B-it-*.gguf"
	// Strip quantization suffix to get the base name.
	baseName := stripQuantSuffix(modelIDLower)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		nameLower := strings.ToLower(name)
		if !strings.HasSuffix(nameLower, ".gguf") {
			continue
		}
		if !strings.Contains(nameLower, "mmproj") {
			continue
		}
		// Check if the mmproj file relates to the same model family.
		nameWithoutExt := strings.TrimSuffix(nameLower, ".gguf")
		if baseName != "" && strings.Contains(nameWithoutExt, baseName) {
			return filepath.Join(modelsDir, name)
		}
	}

	// Strategy 2: Broader match — any mmproj file whose name shares a
	// significant prefix with the model.
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		nameLower := strings.ToLower(name)
		if !strings.HasSuffix(nameLower, ".gguf") {
			continue
		}
		if !strings.Contains(nameLower, "mmproj") {
			continue
		}
		// Extract model family from both filenames and compare.
		mmprojBase := extractModelFamily(strings.TrimSuffix(nameLower, ".gguf"))
		modelBase := extractModelFamily(modelIDLower)
		if mmprojBase != "" && modelBase != "" && mmprojBase == modelBase {
			return filepath.Join(modelsDir, name)
		}
	}

	return ""
}

// stripQuantSuffix removes common quantization suffixes like "-Q4_K_M", "-Q6_K", "-Q8_0", "-BF16", "-F16".
func stripQuantSuffix(name string) string {
	quantPattern := regexp.MustCompile(`(?i)[-_](Q\d+_K(_[MSL])?|Q\d+_\d+|[BF](F?)\d+|GGUF)$`)
	return quantPattern.ReplaceAllString(name, "")
}

// extractModelFamily extracts the model family name by stripping mmproj prefix
// and quantization suffixes. e.g. "mmproj-gemma-4-e2b-it-bf16" → "gemma-4-e2b-it"
func extractModelFamily(name string) string {
	name = strings.TrimPrefix(name, "mmproj-")
	return stripQuantSuffix(name)
}

// ── Utility functions ───────────────────────────────────────────────────────

func scanModels(modelsDir string) []ModelInfo {
	if modelsDir == "" {
		return nil
	}

	entries, err := os.ReadDir(modelsDir)
	if err != nil {
		return nil
	}

	type aggregate struct {
		size     int64
		hasFirst bool
	}
	aggregates := make(map[string]*aggregate, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".gguf") {
			continue
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		id := stem
		hasFirst := true
		if base, idx, _, ok := splitModelID(stem); ok {
			id = base
			hasFirst = idx == 1
		}
		agg := aggregates[id]
		if agg == nil {
			agg = &aggregate{}
			aggregates[id] = agg
		}
		if info, err := entry.Info(); err == nil {
			agg.size += info.Size()
		}
		agg.hasFirst = agg.hasFirst || hasFirst
	}

	ids := make([]string, 0, len(aggregates))
	for id, agg := range aggregates {
		if !agg.hasFirst {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)

	models := make([]ModelInfo, 0, len(ids))
	for _, id := range ids {
		agg := aggregates[id]
		sizeStr := ""
		if agg != nil && agg.size > 0 {
			sizeStr = FormatBytes(agg.size)
		}
		models = append(models, ModelInfo{
			ID:   id,
			Name: HumanModelName(id),
			Size: sizeStr,
		})
	}

	return models
}

// HumanModelName converts a model filename stem into a human-readable name.
func HumanModelName(id string) string {
	name := strings.ReplaceAll(id, "-", " ")
	name = strings.ReplaceAll(name, "_", " ")
	if name == "" {
		return id
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// FormatBytes formats a byte count as a human-readable string.
func FormatBytes(b int64) string {
	const (
		_        = iota
		kB int64 = 1 << (10 * iota)
		mB
		gB
	)
	switch {
	case b >= gB:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(gB))
	case b >= mB:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mB))
	case b >= kB:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(kB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// containsSensitiveKeywords checks if tool parameters contain sensitive keywords.
func ContainsSensitiveKeywords(params map[string]any) bool {
	if len(params) == 0 {
		return false
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(paramsJSON))
	for _, kw := range SensitiveKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// SensitiveKeywords triggers forced review when found in tool arguments.
var SensitiveKeywords = []string{
	"rm", "curl", "wget", "ssh", "scp", "token", "password", "secret",
	"credential", "key", "delete", "drop", "truncate", "format",
	"sudo", "chmod", "chown", "kill", "reboot", "shutdown",
}
