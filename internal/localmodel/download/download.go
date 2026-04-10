// Package download implements model file download with progress reporting.
//
// It handles HTTP retrieval of GGUF model files (including multi-shard
// models) with atomic progress tracking, temporary file safety, and
// cancellation support via context.
package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/kocort/kocort/internal/localmodel/catalog"
)

// ProgressReporter receives progress updates during a download.
// Implementations must be safe for concurrent use.
type ProgressReporter interface {
	// SetFilename sets the current file being downloaded.
	SetFilename(name string)
	// SetDownloaded sets the total bytes downloaded so far.
	SetDownloaded(n int64)
	// SetTotal sets the total expected download size in bytes.
	SetTotal(n int64)
	// Downloaded returns the current downloaded byte count.
	Downloaded() int64
	// Total returns the current total byte count.
	Total() int64
}

// File describes one downloadable file (URL + destination filename).
type File = catalog.PresetFile

// Do downloads all files for the given preset into destDir.
// It reports progress through the ProgressReporter and supports
// cancellation via the context.
//
// On failure, any partially downloaded files are cleaned up.
func Do(ctx context.Context, files []File, presetID, destDir string, client *http.Client, progress ProgressReporter) error {
	if len(files) == 0 {
		return fmt.Errorf("preset %q has no downloadable files", presetID)
	}

	completedBytes := int64(0)
	downloadedPaths := make([]string, 0, len(files))

	for _, file := range files {
		destPath := filepath.Join(destDir, file.Filename)
		if err := downloadFile(ctx, file, destPath, completedBytes, client, progress); err != nil {
			// Clean up partially downloaded files.
			for _, path := range downloadedPaths {
				_ = os.Remove(path)
			}
			return err
		}
		downloadedPaths = append(downloadedPaths, destPath)
		if info, err := os.Stat(destPath); err == nil {
			completedBytes += info.Size()
		}
		progress.SetDownloaded(completedBytes)
		if progress.Total() < completedBytes {
			progress.SetTotal(completedBytes)
		}
	}

	return nil
}

func downloadFile(ctx context.Context, file File, destPath string, completedBytes int64, client *http.Client, progress ProgressReporter) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, file.DownloadURL, nil)
	if err != nil {
		return fmt.Errorf("create download request: %w", err)
	}

	resp, err := client.Do(req)
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

	progress.SetFilename(file.Filename)
	progress.SetDownloaded(completedBytes)
	if resp.ContentLength > 0 {
		progress.SetTotal(completedBytes + resp.ContentLength)
	} else if progress.Total() < completedBytes {
		progress.SetTotal(completedBytes)
	}

	tmpPath := destPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	pw := &progressWriter{w: f, downloaded: progress}
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

// progressWriter wraps an io.Writer and reports bytes written to a ProgressReporter.
type progressWriter struct {
	w          io.Writer
	downloaded ProgressReporter
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.w.Write(p)
	if n > 0 {
		pw.downloaded.SetDownloaded(pw.downloaded.Downloaded() + int64(n))
	}
	return n, err
}

// ── AtomicProgress ───────────────────────────────────────────────────────────

// AtomicProgress is a thread-safe ProgressReporter backed by atomic values.
type AtomicProgress struct {
	downloaded atomic.Int64
	total      atomic.Int64
	filename   atomic.Value // string
}

func (p *AtomicProgress) SetFilename(name string) { p.filename.Store(name) }
func (p *AtomicProgress) SetDownloaded(n int64)   { p.downloaded.Store(n) }
func (p *AtomicProgress) SetTotal(n int64)        { p.total.Store(n) }
func (p *AtomicProgress) Downloaded() int64       { return p.downloaded.Load() }
func (p *AtomicProgress) Total() int64            { return p.total.Load() }

// Filename returns the current filename being downloaded.
func (p *AtomicProgress) Filename() string {
	if v := p.filename.Load(); v != nil {
		return v.(string)
	}
	return ""
}
