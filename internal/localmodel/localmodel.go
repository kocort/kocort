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
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

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
	Threads     int             `json:"threads,omitempty"`
	ContextSize int             `json:"contextSize,omitempty"`
	GpuLayers   int             `json:"gpuLayers,omitempty"`
	Sampling    *SamplingParams `json:"sampling,omitempty"`
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

// progressWriter wraps an io.Writer and tracks bytes written.
type progressWriter struct {
	w       io.Writer
	manager *Manager
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
		pw.manager.mu.Lock()
		pw.manager.dlProgress.DownloadedBytes += int64(n)
		pw.manager.mu.Unlock()
	}
	return n, err
}

// Manager manages the lifecycle of a single local model instance.
// Both brain and cerebellum use their own Manager.
type Manager struct {
	mu             sync.RWMutex
	status         string
	modelID        string
	models         []ModelInfo
	lastError      string
	modelsDir      string
	backend        ModelBackend
	catalog        []ModelPreset
	dlProgress     DownloadProgress
	dlCancel       context.CancelFunc
	dc             *infra.DynamicHTTPClient
	sampling       SamplingParams
	threads        int
	contextSize    int
	gpuLayers      int
	enableThinking bool
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
		status:         StatusStopped,
		modelsDir:      strings.TrimSpace(cfg.ModelsDir),
		modelID:        strings.TrimSpace(cfg.ModelID),
		backend:        backend,
		catalog:        catalog,
		sampling:       sp,
		threads:        cfg.Threads,
		contextSize:    cfg.ContextSize,
		gpuLayers:      cfg.GpuLayers,
		enableThinking: cfg.EnableThinking,
	}
	backend.SetSamplingParams(sp)
	m.discoverModels()
	return m
}

// Backend returns the underlying ModelBackend.
func (m *Manager) Backend() ModelBackend {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.backend
}

// SetDynamicHTTPClient sets the dynamic HTTP client used for model downloads.
func (m *Manager) SetDynamicHTTPClient(dc *infra.DynamicHTTPClient) {
	m.mu.Lock()
	m.dc = dc
	m.mu.Unlock()
}

// GetSamplingParams returns the current sampling parameters.
func (m *Manager) GetSamplingParams() SamplingParams {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sampling
}

// ThinkingSetter is an optional interface implemented by inferencers
// that support dynamic thinking mode toggling.
type ThinkingSetter interface {
	SetEnableThinking(enabled bool)
}

// EnableThinking returns whether thinking mode is enabled.
func (m *Manager) EnableThinking() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enableThinking
}

// SetEnableThinking updates the thinking mode setting.
// The change is applied to the backend immediately.
func (m *Manager) SetEnableThinking(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enableThinking = enabled
}

// SamplingParamsSetter is an optional interface implemented by inferencers
// that support dynamic sampling parameter updates without restart.
type SamplingParamsSetter interface {
	SetSamplingParams(sp SamplingParams)
}

// SetSamplingParams updates the sampling parameters.
// The change is applied to the backend immediately. If the model is running
// and the backend requires a restart, one is triggered.
func (m *Manager) SetSamplingParams(sp SamplingParams) error {
	m.mu.Lock()
	m.sampling = sp
	m.backend.SetSamplingParams(sp)
	wasRunning := m.status == StatusRunning
	m.mu.Unlock()
	if wasRunning {
		return m.Restart()
	}
	return nil
}

// Threads returns the current inference thread count.
func (m *Manager) Threads() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.threads
}

// ContextSize returns the current context window size.
func (m *Manager) ContextSize() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.contextSize
}

// GpuLayers returns the current GPU layers setting.
func (m *Manager) GpuLayers() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.gpuLayers
}

// UpdateRuntimeParams updates threads, contextSize, and gpuLayers.
// If the model is running, it will be restarted to apply the new parameters.
func (m *Manager) UpdateRuntimeParams(threads, contextSize, gpuLayers int) error {
	m.mu.Lock()
	m.threads = threads
	m.contextSize = contextSize
	m.gpuLayers = gpuLayers
	wasRunning := m.status == StatusRunning
	m.mu.Unlock()
	if wasRunning {
		return m.Restart()
	}
	return nil
}

// UpdateAllParams updates sampling, threads, contextSize, and gpuLayers in one
// atomic operation, triggering at most one restart (avoids the double-restart
// that would happen if SetSamplingParams and UpdateRuntimeParams were called
// separately).
func (m *Manager) UpdateAllParams(sp *SamplingParams, threads, contextSize, gpuLayers int) error {
	m.mu.Lock()
	needsRestart := false

	if sp != nil {
		m.sampling = *sp
		m.backend.SetSamplingParams(m.sampling)
		if m.status == StatusRunning {
			needsRestart = true
		}
	}

	if threads != m.threads || contextSize != m.contextSize || gpuLayers != m.gpuLayers {
		m.threads = threads
		m.contextSize = contextSize
		m.gpuLayers = gpuLayers
		if m.status == StatusRunning {
			needsRestart = true
		}
	}

	m.mu.Unlock()
	if needsRestart {
		return m.Restart()
	}
	return nil
}

// Status returns the current lifecycle status.
func (m *Manager) Status() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

// IsStub returns true when the manager is using a stub backend
// (binary built without -tags llamacpp). This means model loading
// nominally succeeds but inference always returns empty results.
func (m *Manager) IsStub() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.backend.IsStub()
}

// IsStubInferencer is an alias for IsStub kept for backward compatibility.
// Deprecated: Use IsStub instead.
func (m *Manager) IsStubInferencer() bool {
	return m.IsStub()
}

// ModelID returns the currently selected model ID.
func (m *Manager) ModelID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.modelID
}

// Models returns the list of discovered models.
func (m *Manager) Models() []ModelInfo {
	m.discoverModels()
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ModelInfo, len(m.models))
	copy(out, m.models)
	return out
}

// LastError returns the last error message, if any.
func (m *Manager) LastError() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastError
}

// Start begins running the local model.
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.status == StatusRunning || m.status == StatusStarting {
		return nil
	}

	// Early check: if using a stub backend, local inference is not available.
	if m.backend.IsStub() {
		m.status = StatusError
		m.lastError = "local inference not available: binary built without llama.cpp support (use -tags llamacpp)"
		return fmt.Errorf("local inference not available: binary built without llama.cpp support (use -tags llamacpp)")
	}

	if m.modelID == "" {
		m.status = StatusError
		m.lastError = "no model selected and no models available"
		return fmt.Errorf("no model selected")
	}

	m.status = StatusStarting
	m.lastError = ""

	modelPath := m.resolveModelPath()
	if err := m.backend.Start(modelPath, m.threads, m.contextSize, m.gpuLayers, m.sampling, m.enableThinking); err != nil {
		m.status = StatusError
		m.lastError = fmt.Sprintf("backend start failed: %v", err)
		return fmt.Errorf("backend start: %w", err)
	}

	// Sync the actual context size back from the backend.
	if actual := m.backend.ContextSize(); actual > 0 {
		m.contextSize = actual
	}

	m.status = StatusRunning
	return nil
}

// Stop shuts down the running model.
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.status == StatusStopped || m.status == StatusStopping {
		return nil
	}

	m.status = StatusStopping
	m.lastError = ""

	if err := m.backend.Stop(); err != nil {
		m.status = StatusError
		m.lastError = fmt.Sprintf("backend stop failed: %v", err)
		return fmt.Errorf("backend stop: %w", err)
	}
	m.status = StatusStopped
	return nil
}

// Restart stops and then starts the model.
func (m *Manager) Restart() error {
	if err := m.Stop(); err != nil {
		return fmt.Errorf("stop during restart: %w", err)
	}
	return m.Start()
}

// SelectModel sets the active model ID. If running, restarts with the new model.
func (m *Manager) SelectModel(modelID string) error {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return fmt.Errorf("model ID is required")
	}

	m.discoverModels()

	m.mu.Lock()
	found := false
	for _, model := range m.models {
		if model.ID == modelID {
			found = true
			break
		}
	}
	if !found {
		m.mu.Unlock()
		return fmt.Errorf("model %q not found", modelID)
	}

	wasRunning := m.status == StatusRunning
	m.modelID = modelID
	m.mu.Unlock()

	if wasRunning {
		return m.Restart()
	}
	return nil
}

// ClearSelectedModel clears the current default/selected model.
// If the selected model is currently running, it is stopped first.
func (m *Manager) ClearSelectedModel() error {
	m.mu.RLock()
	selected := m.modelID
	status := m.status
	m.mu.RUnlock()

	if selected == "" {
		return nil
	}

	if status == StatusRunning || status == StatusStarting {
		if err := m.Stop(); err != nil {
			return err
		}
	}

	m.mu.Lock()
	m.modelID = ""
	m.lastError = ""
	if m.status == StatusError {
		m.status = StatusStopped
	}
	m.mu.Unlock()
	return nil
}

// DeleteModel removes a downloaded model file from disk.
// If the model is selected, the selection is cleared first; if it is running,
// the model is stopped before deletion.
func (m *Manager) DeleteModel(modelID string) error {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return fmt.Errorf("model ID is required")
	}

	m.discoverModels()

	m.mu.RLock()
	modelsDir := m.modelsDir
	found := false
	selected := m.modelID == modelID
	for _, model := range m.models {
		if model.ID == modelID {
			found = true
			break
		}
	}
	m.mu.RUnlock()

	if !found {
		return fmt.Errorf("model %q not found", modelID)
	}
	if modelsDir == "" {
		return fmt.Errorf("models directory is not configured")
	}
	if selected {
		if err := m.ClearSelectedModel(); err != nil {
			return err
		}
	}

	modelPaths := installedModelFiles(modelsDir, modelID)
	if len(modelPaths) == 0 {
		return fmt.Errorf("model file not found: %s", resolveInstalledModelPath(modelsDir, modelID))
	}
	for _, modelPath := range modelPaths {
		if err := os.Remove(modelPath); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("delete model file: %w", err)
		}
	}

	m.mu.Lock()
	m.models = nil
	m.mu.Unlock()
	m.discoverModels()
	return nil
}

// CreateChatCompletionStream creates a streaming chat completion.
// The caller provides an llamawrapper.ChatCompletionRequest (with RawPrompt
// already set if a custom prompt is needed). The implementation delegates to
// the ModelBackend for inference, <think> block parsing, and tool call extraction.
func (m *Manager) CreateChatCompletionStream(ctx context.Context, req llamawrapper.ChatCompletionRequest) (<-chan llamawrapper.ChatCompletionChunk, error) {
	m.mu.RLock()
	status := m.status
	enableThinking := m.enableThinking
	m.mu.RUnlock()

	if status != StatusRunning {
		return nil, fmt.Errorf("local model is not running (status: %s)", status)
	}

	return m.backend.CreateChatCompletionStream(ctx, req, enableThinking)
}

// Catalog returns the model catalog.
func (m *Manager) Catalog() []ModelPreset {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ModelPreset, len(m.catalog))
	copy(out, m.catalog)
	return out
}

// SetCatalog replaces the model catalog. Intended for testing.
func (m *Manager) SetCatalog(catalog []ModelPreset) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.catalog = make([]ModelPreset, len(catalog))
	copy(m.catalog, catalog)
}

// DownloadModel downloads a model from the catalog by preset ID.
func (m *Manager) DownloadModel(presetID string, httpClient *http.Client) error {
	m.mu.RLock()
	modelsDir := m.modelsDir
	active := m.dlProgress.Active
	m.mu.RUnlock()

	if active {
		return fmt.Errorf("a download is already in progress")
	}
	if modelsDir == "" {
		return fmt.Errorf("models directory is not configured")
	}

	var preset *ModelPreset
	for i := range m.catalog {
		if m.catalog[i].ID == presetID {
			preset = &m.catalog[i]
			break
		}
	}
	if preset == nil {
		return fmt.Errorf("preset %q not found in catalog", presetID)
	}
	files := preset.DownloadFiles()
	if len(files) == 0 {
		return fmt.Errorf("preset %q has no downloadable files", presetID)
	}

	for _, file := range files {
		destPath := filepath.Join(modelsDir, file.Filename)
		if _, err := os.Stat(destPath); err == nil {
			return fmt.Errorf("model file already exists: %s", destPath)
		}
	}

	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		return fmt.Errorf("create models dir: %w", err)
	}

	if httpClient != nil {
		return m.doDownload(context.Background(), preset, modelsDir, httpClient)
	}

	dlCtx, cancel := context.WithCancel(context.Background())

	m.mu.Lock()
	m.dlProgress = DownloadProgress{
		PresetID: presetID,
		Filename: preset.PrimaryFilename(),
		Active:   true,
	}
	m.dlCancel = cancel
	m.mu.Unlock()

	go func() {
		m.mu.RLock()
		dc := m.dc
		m.mu.RUnlock()
		var client *http.Client
		if dc != nil {
			client = dc.ClientWithTimeout(0)
		} else {
			client = &http.Client{}
		}
		err := m.doDownload(dlCtx, preset, modelsDir, client)
		m.mu.Lock()
		if err != nil {
			if errors.Is(err, context.Canceled) {
				m.dlProgress.Canceled = true
				m.dlProgress.Error = ""
			} else {
				m.dlProgress.Error = err.Error()
			}
		}
		m.dlProgress.Active = false
		m.dlCancel = nil
		m.mu.Unlock()
	}()

	return nil
}

// CancelDownload cancels the active model download.
func (m *Manager) CancelDownload() error {
	m.mu.RLock()
	active := m.dlProgress.Active
	cancel := m.dlCancel
	m.mu.RUnlock()

	if !active || cancel == nil {
		return fmt.Errorf("no download is in progress")
	}

	cancel()
	return nil
}

// Snapshot returns the current state for API responses.
func (m *Manager) Snapshot() Snapshot {
	m.discoverModels()

	m.mu.RLock()
	defer m.mu.RUnlock()
	models := make([]ModelInfo, len(m.models))
	copy(models, m.models)
	catalog := make([]ModelPreset, len(m.catalog))
	copy(catalog, m.catalog)
	var dlProg *DownloadProgress
	if m.dlProgress.PresetID != "" {
		cp := m.dlProgress
		dlProg = &cp
	}
	return Snapshot{
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
	}
}

// doDownload performs the actual HTTP download with progress tracking.
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
		m.mu.Lock()
		m.dlProgress.DownloadedBytes = completedBytes
		if m.dlProgress.TotalBytes < completedBytes {
			m.dlProgress.TotalBytes = completedBytes
		}
		m.mu.Unlock()
	}

	m.mu.Lock()
	m.models = nil
	m.mu.Unlock()
	m.discoverModels()

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

	m.mu.Lock()
	m.dlProgress.Filename = file.Filename
	m.dlProgress.DownloadedBytes = completedBytes
	if resp.ContentLength > 0 {
		m.dlProgress.TotalBytes = completedBytes + resp.ContentLength
	} else if m.dlProgress.TotalBytes < completedBytes {
		m.dlProgress.TotalBytes = completedBytes
	}
	m.mu.Unlock()

	tmpPath := destPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	pw := &progressWriter{w: f, manager: m}
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

// discoverModels scans the models directory for GGUF model files.
func (m *Manager) discoverModels() {
	m.mu.RLock()
	modelsDir := m.modelsDir
	m.mu.RUnlock()

	models := scanModels(modelsDir)

	m.mu.Lock()
	m.models = models
	m.mu.Unlock()
}

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
