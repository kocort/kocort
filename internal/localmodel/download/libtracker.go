package download

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// ErrLibDLActive is returned when a library download is already in progress.
var ErrLibDLActive = errors.New("a library download is already in progress")

// LibDLFunc performs the actual library download. It receives a context for
// cancellation and a progress callback. It returns the library directory
// path on success, or an error.
type LibDLFunc func(ctx context.Context, progress ProgressCallback) (string, error)

// LibDLProgress is a point-in-time snapshot of a library download's state.
type LibDLProgress struct {
	Version         string
	GPUType         string
	DownloadedBytes int64
	TotalBytes      int64
	Active          bool
	Canceled        bool
	Error           string
}

// LibDownloadTracker manages library downloads with progress tracking, cancellation,
// and deduplication. It is safe for concurrent use. Only one download can be active
// at a time; concurrent Start calls return ErrLibDLActive.
type LibDownloadTracker struct {
	mu              sync.Mutex
	active          bool
	canceled        bool
	errMsg          string
	version         string
	gpuType         string
	downloadedBytes int64 // accessed atomically
	totalBytes      int64 // accessed atomically
	cancel          context.CancelFunc
	done            chan struct{} // closed when the current download finishes
}

// Start begins a library download in a background goroutine.
// version and gpuType describe what is being downloaded (for progress display).
// dlFunc performs the actual download work.
// Returns ErrLibDLActive if a download is already running.
func (t *LibDownloadTracker) Start(version, gpuType string, dlFunc LibDLFunc) error {
	t.mu.Lock()
	if t.active {
		t.mu.Unlock()
		return ErrLibDLActive
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.active = true
	t.canceled = false
	t.errMsg = ""
	t.version = version
	t.gpuType = gpuType
	t.cancel = cancel
	atomic.StoreInt64(&t.downloadedBytes, 0)
	atomic.StoreInt64(&t.totalBytes, 0)
	t.done = make(chan struct{})
	t.mu.Unlock()

	go func() {
		progress := func(downloaded, total int64) {
			atomic.StoreInt64(&t.downloadedBytes, downloaded)
			if total > 0 {
				atomic.StoreInt64(&t.totalBytes, total)
			}
		}

		_, err := dlFunc(ctx, progress)

		t.mu.Lock()
		t.active = false
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				t.canceled = true
			} else {
				t.errMsg = err.Error()
			}
		}
		t.cancel = nil
		done := t.done
		t.mu.Unlock()
		close(done)
	}()

	return nil
}

// Cancel cancels the active download. No-op if no download is running.
func (t *LibDownloadTracker) Cancel() {
	t.mu.Lock()
	if t.cancel != nil {
		t.cancel()
	}
	t.mu.Unlock()
}

// Progress returns a snapshot of the current download state.
func (t *LibDownloadTracker) Progress() LibDLProgress {
	t.mu.Lock()
	p := LibDLProgress{
		Version:  t.version,
		GPUType:  t.gpuType,
		Active:   t.active,
		Canceled: t.canceled,
		Error:    t.errMsg,
	}
	t.mu.Unlock()
	p.DownloadedBytes = atomic.LoadInt64(&t.downloadedBytes)
	p.TotalBytes = atomic.LoadInt64(&t.totalBytes)
	return p
}

// Done returns a channel that is closed when the current download finishes.
// Returns nil if no download has been started.
func (t *LibDownloadTracker) Done() <-chan struct{} {
	t.mu.Lock()
	d := t.done
	t.mu.Unlock()
	return d
}
