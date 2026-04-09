// Package llamadl provides pure-Go dynamic loading of llama.cpp shared libraries.
//
// It replaces the previous CGO-based internal/llama package with zero CGO dependency,
// using github.com/kocort/purego for dlopen/dlsym at runtime. Libraries are
// downloaded from official llama.cpp GitHub Releases on first use.
//
// # Quick start
//
//	llamadl.BackendInit()  // Initialize global library singleton
//	model, _ := llamadl.LoadModelFromFile(llamadl.DefaultLibrary(), path, params)
//	ctx, _ := llamadl.NewContextWithModel(llamadl.DefaultLibrary(), model, ctxParams)
//
// # Environment variables
//
//	KOCORT_LLAMA_LIB_DIR   — Override library directory (skip download)
//	KOCORT_LLAMA_VERSION   — Override download version (default: b8720)
//	KOCORT_LLAMA_GPU       — GPU type: cpu, vulkan, cuda-12.4, cuda-13.1, rocm-7.2
package llamadl

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
)

var (
	globalLib     *Library
	globalLibOnce sync.Once
	globalLibErr  error
)

// BackendInit initializes the llama.cpp backend by:
// 1. Locating or downloading the shared libraries
// 2. dlopen-ing all required libraries
// 3. Calling llama_backend_init()
//
// This is safe to call multiple times (idempotent via sync.Once).
func BackendInit() {
	globalLibOnce.Do(func() {
		lib, err := initLibrary()
		if err != nil {
			globalLibErr = err
			slog.Error("[llamadl] backend init failed", "error", err)
			return
		}
		globalLib = lib
		lib.fnLlamaBackendInit()

		// Set up log forwarding
		lib.fnLlamaLogSet(newLogCallback(), 0)

		slog.Info("[llamadl] backend initialized", "libDir", lib.libDir)
	})
}

// DefaultLibrary returns the global Library singleton.
// Must be called after BackendInit().
func DefaultLibrary() *Library {
	return globalLib
}

// LibraryError returns any error from library initialization.
func LibraryError() error {
	return globalLibErr
}

// initLibrary locates the library directory and opens all shared libraries.
func initLibrary() (*Library, error) {
	// 1. Check environment override
	libDir := os.Getenv("KOCORT_LLAMA_LIB_DIR")
	if libDir != "" {
		slog.Info("[llamadl] using KOCORT_LLAMA_LIB_DIR", "dir", libDir)
		return Open(libDir)
	}

	// 2. Try to find existing libraries
	libDir = findLibDir()
	if libDir != "" {
		slog.Info("[llamadl] found existing libraries", "dir", libDir)
		return Open(libDir)
	}

	// 3. Download from GitHub Releases
	version := os.Getenv("KOCORT_LLAMA_VERSION")
	if version == "" {
		version = LlamaCppVersion
	}
	gpuType := os.Getenv("KOCORT_LLAMA_GPU")

	libDir, err := EnsureLibraries(DownloadConfig{
		Version: version,
		GPUType: gpuType,
	})
	if err != nil {
		return nil, fmt.Errorf("ensure libraries: %w", err)
	}

	return Open(libDir)
}

// newLogCallback creates a purego callback for llama.cpp log messages.
func newLogCallback() uintptr {
	// ggml_log_level: GGML_LOG_LEVEL_NONE=0, ERROR=1, WARN=2, INFO=3, DEBUG=4
	const (
		ggmlLogLevelInfo = 3
	)

	return newCallback(func(level int32, text *byte, userData uintptr) {
		goText := gostr(text)
		slogLevel := slog.Level(int(level-ggmlLogLevelInfo) * 4)
		if slog.Default().Enabled(context.TODO(), slogLevel) {
			fmt.Fprint(os.Stderr, goText)
		}
	})
}
