// Package manager contains the Manager actor that manages the lifecycle of a
// single local model instance.  Both brain and cerebellum use their own
// Manager.
//
// All state mutations are serialised inside a single actor goroutine that
// reads from cmdCh.  External callers interact with the Manager through
// channel-based commands that provide immediate fast-fail responses.
//
// Read-hot fields (Status, ModelID, EnableThinking) are mirrored to atomic
// values so they can be read from any goroutine without round-tripping
// through the actor.
package manager

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/kocort/kocort/internal/infra"
	"github.com/kocort/kocort/internal/localmodel/download"
)

// ── Manager ─────────────────────────────────────────────────────────────────

// Manager manages the lifecycle of a single local model instance.
type Manager struct {
	cmdCh chan cmd // all mutations flow through this channel

	// Atomic mirrors — written only by the actor, read by any goroutine.
	atomicStatus         atomic.Value // string
	atomicModelID        atomic.Value // string
	atomicEnableThinking atomic.Bool
	atomicContextSize    atomic.Int64
	atomicLastError      atomic.Value // string

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
	dlProgress    DownloadProgress
	dlCancel      context.CancelFunc
	dlReporter    *download.AtomicProgress // progress reporter for model download

	// Pending compound operation (e.g. stop-before-delete).
	pendingAfterStop *pendingOp
}

// NewManager creates a new local model Manager.
// The model backend is created automatically based on build tags:
// with llamacpp → engine-backed engine;
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
		modelID:        strings.ToLower(strings.TrimSpace(cfg.ModelID)),
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

// HasVision returns true if the loaded model supports multimodal vision.
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
// atomic operation, triggering at most one restart.
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
// model state and dispatches to the backend.
func (m *Manager) CreateChatCompletionStream(ctx context.Context, req ChatCompletionRequest) (<-chan ChatCompletionChunk, error) {
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
