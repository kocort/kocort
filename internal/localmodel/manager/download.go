package manager

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/kocort/kocort/internal/localmodel/download"
	"github.com/kocort/kocort/internal/localmodel/ffi"
)

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
		progress := &download.AtomicProgress{}
		err := download.Do(context.Background(), files, preset.ID, modelsDir, cmd.httpClient, progress)
		m.models = scanModels(modelsDir)
		cmd.reply <- err
		return
	}

	// Trigger library download via the global tracker (if needed).
	cfgVer, cfgGPU, _, _ := ffi.LibraryStatus()
	if !ffi.CheckLibrariesExist(ffi.DownloadConfig{Version: cfgVer, GPUType: cfgGPU}) {
		var httpClient *http.Client
		if m.dc != nil {
			httpClient = m.dc.ClientWithTimeout(0)
		}
		// Best-effort start; ErrLibDLActive means another caller already started it.
		_ = ffi.StartLibDownload(ffi.DownloadConfig{
			Version:    cfgVer,
			GPUType:    cfgGPU,
			HTTPClient: httpClient,
		})
	}

	// Set up model download.
	modelCtx, modelCancel := context.WithCancel(context.Background())
	m.dlProgress = DownloadProgress{
		PresetID: cmd.presetID,
		Filename: preset.PrimaryFilename(),
		Active:   true,
	}
	m.dlCancel = modelCancel
	m.dlReporter = &download.AtomicProgress{}
	m.dlReporter.SetFilename(preset.PrimaryFilename())

	dc := m.dc
	ch := m.cmdCh
	reporter := m.dlReporter
	cmd.reply <- nil // accepted

	// Start model download goroutine (runs in parallel with lib download).
	go func() {
		var client *http.Client
		if dc != nil {
			client = dc.ClientWithTimeout(0)
		} else {
			client = &http.Client{}
		}
		err := download.Do(modelCtx, files, preset.ID, modelsDir, client, reporter)
		canceled := errors.Is(err, context.Canceled)
		ch <- &cmdDLDone{err: err, canceled: canceled}
	}()
}

func (m *Manager) handleCancelDownload(cmd *cmdCancelDownload) {
	if !m.dlProgress.Active || m.dlCancel == nil {
		cmd.reply <- fmt.Errorf("no model download is in progress")
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
	if m.dlReporter != nil {
		m.dlProgress.DownloadedBytes = m.dlReporter.Downloaded()
		if total := m.dlReporter.Total(); total > 0 {
			m.dlProgress.TotalBytes = total
		}
	}
	m.dlCancel = nil
	m.dlReporter = nil
	m.models = scanModels(m.modelsDir)
}
