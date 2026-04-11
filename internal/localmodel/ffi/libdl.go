package ffi

import (
	"context"
	"log/slog"

	"github.com/kocort/kocort/internal/localmodel/download"
)

// Re-export types from the download package so existing callers continue to work.
type LibDLProgress = download.LibDLProgress

// ErrLibDLActive is returned when a library download is already in progress.
var ErrLibDLActive = download.ErrLibDLActive

var globalLibDL = &download.LibDownloadTracker{}

// GlobalLibDownloadTracker returns the process-wide library download tracker.
func GlobalLibDownloadTracker() *download.LibDownloadTracker { return globalLibDL }

// StartLibDownload is a convenience wrapper that resolves GPU type and starts
// a library download using the global tracker. Callers that already hold a
// *download.LibDownloadTracker can call Start() directly with their own LibDLFunc.
func StartLibDownload(cfg DownloadConfig) error {
	cfg.GPUType = ResolveGPUType(cfg.GPUType)
	httpClient := cfg.HTTPClient
	return globalLibDL.Start(cfg.Version, cfg.GPUType, func(ctx context.Context, progress download.ProgressCallback) (string, error) {
		_ = httpClient // prevent premature GC
		libDir, err := EnsureLibrariesWithContext(ctx, cfg, LibProgressFunc(progress))
		if err != nil {
			return "", err
		}
		// Re-initialize the backend so the newly downloaded libraries
		// (potentially a different GPU variant) are loaded immediately.
		if reinitErr := BackendReinit(); reinitErr != nil {
			slog.Warn("[ffi] backend reinit after download failed", "error", reinitErr)
		}
		return libDir, nil
	})
}
